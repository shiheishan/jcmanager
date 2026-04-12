package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jcmanagerpb "jcmanager/proto"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRegisterRequiresExistingSessionForNodeReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	ctx := context.Background()

	firstResp, err := srv.Register(ctx, &jcmanagerpb.RegisterRequest{
		Node:  &jcmanagerpb.NodeInfo{Hostname: "node-a"},
		Token: "agent-secret",
	})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if firstResp.GetNodeId() == "" || firstResp.GetSessionToken() == "" {
		t.Fatalf("register should return node id and session token: %#v", firstResp)
	}

	_, err = srv.Register(ctx, &jcmanagerpb.RegisterRequest{
		Node:  &jcmanagerpb.NodeInfo{NodeId: firstResp.GetNodeId(), Hostname: "attacker"},
		Token: "agent-secret",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated for reused node id without session, got %v", err)
	}

	secondResp, err := srv.Register(ctx, &jcmanagerpb.RegisterRequest{
		Node:         &jcmanagerpb.NodeInfo{NodeId: firstResp.GetNodeId(), Hostname: "node-a"},
		Token:        "agent-secret",
		SessionToken: firstResp.GetSessionToken(),
	})
	if err != nil {
		t.Fatalf("re-register with valid session: %v", err)
	}
	if secondResp.GetNodeId() != firstResp.GetNodeId() {
		t.Fatalf("expected same node id, got %q want %q", secondResp.GetNodeId(), firstResp.GetNodeId())
	}
	if secondResp.GetSessionToken() != firstResp.GetSessionToken() {
		t.Fatalf("expected stable session token on authenticated re-register")
	}
}

func TestAPIRequiresTokenAndSanitizesNodeData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)

	record := nodeRecord{
		ID:                 "node-1",
		Hostname:           "host-1",
		DisplayName:        "Node One",
		PrimaryIP:          "10.0.0.1",
		OS:                 "linux",
		Arch:               "amd64",
		Kernel:             "6.8.0",
		AgentVersion:       "dev",
		SessionTokenHash:   hashToken("node-session"),
		ServiceFlavorsJSON: mustJSON(t, []string{"xrayr"}),
		AllowedPathsJSON:   mustJSON(t, []string{"/etc/jcmanager", "/etc/xrayr/config.yml"}),
		ServicesJSON: mustJSON(t, []*jcmanagerpb.ServiceStatus{
			{
				Name:       "xrayr",
				Active:     true,
				Listening:  true,
				ListenPort: 443,
				ConfigPath: "/etc/xrayr/config.yml",
				Message:    "ok",
			},
		}),
		RegisteredAt:    time.Now().UTC(),
		LastRegisterAt:  time.Now().UTC(),
		LastHeartbeatAt: timePtr(time.Now().UTC()),
		Online:          true,
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	router := gin.New()
	api := router.Group("/api")
	api.Use(srv.apiAuthMiddleware())
	srv.registerRoutes(api)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-1", nil)
	req.Header.Set("Authorization", "Bearer api-secret")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for detail, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "config_path") {
		t.Fatalf("detail endpoint leaked service config path: %s", body)
	}
	if strings.Contains(body, "allowed_paths") {
		t.Fatalf("detail endpoint leaked allowed paths: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/config", nil)
	req.Header.Set("X-API-Token", "api-secret")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for config, got %d body=%s", w.Code, w.Body.String())
	}

	var configResp nodeConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &configResp); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	if len(configResp.AllowedPaths) != 2 {
		t.Fatalf("expected allowed paths in config endpoint, got %#v", configResp.AllowedPaths)
	}
	if len(configResp.ConfigPaths) != 1 || configResp.ConfigPaths[0] != "/etc/xrayr/config.yml" {
		t.Fatalf("expected detected config path, got %#v", configResp.ConfigPaths)
	}
	if configResp.Status != nodeStatusActive {
		t.Fatalf("expected active status in config response, got %#v", configResp.Status)
	}
}

func TestOpenDatabaseBackfillsNodeStatusForExistingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "server.db")

	legacyDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	createLegacyTable := `
CREATE TABLE node_records (
  id TEXT PRIMARY KEY,
  hostname TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT '',
  primary_ip TEXT NOT NULL DEFAULT '',
  os TEXT NOT NULL DEFAULT '',
  arch TEXT NOT NULL DEFAULT '',
  kernel TEXT NOT NULL DEFAULT '',
  agent_version TEXT NOT NULL DEFAULT '',
  session_token_hash BLOB NOT NULL DEFAULT '',
  service_flavors_json BLOB NOT NULL DEFAULT '',
  allowed_paths_json BLOB NOT NULL DEFAULT '',
  services_json BLOB NOT NULL DEFAULT '',
  registered_at DATETIME NOT NULL,
  last_register_at DATETIME NOT NULL,
  last_heartbeat_at DATETIME,
  last_agent_time_unix INTEGER NOT NULL DEFAULT 0,
  online NUMERIC NOT NULL DEFAULT false,
  cpu_percent REAL,
  memory_used_bytes INTEGER NOT NULL DEFAULT 0,
  memory_total_bytes INTEGER NOT NULL DEFAULT 0,
  disk_used_bytes INTEGER NOT NULL DEFAULT 0,
  disk_total_bytes INTEGER NOT NULL DEFAULT 0,
  load_1 REAL,
  load_5 REAL,
  load_15 REAL,
  config_error TEXT NOT NULL DEFAULT '',
  created_at DATETIME,
  updated_at DATETIME
);`
	if err := legacyDB.Exec(createLegacyTable).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	now := time.Now().UTC()
	if err := legacyDB.Exec(
		`INSERT INTO node_records (id, hostname, display_name, registered_at, last_register_at) VALUES (?, ?, ?, ?, ?)`,
		"legacy-node",
		"legacy-host",
		"Legacy Host",
		now,
		now,
	).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	db, err := openDatabase(dbPath)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}

	var record nodeRecord
	if err := db.First(&record, "id = ?", "legacy-node").Error; err != nil {
		t.Fatalf("load migrated row: %v", err)
	}
	if record.Status != nodeStatusActive {
		t.Fatalf("expected migrated row status %q, got %q", nodeStatusActive, record.Status)
	}
}

func TestLoadServerConfigFileAndMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(`
grpc_addr: ":5443"
http_addr: ":9090"
db_path: "/var/lib/jcmanager/custom.db"
token: "from-file-agent"
api_token: "from-file-api"
external_url: "https://panel.example.test"
`), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	fileCfg, err := loadServerConfigFile(path)
	if err != nil {
		t.Fatalf("load server config: %v", err)
	}

	cfg := config{
		GRPCAddr: defaultGRPCAddr,
		HTTPAddr: ":7777",
		DBPath:   defaultDatabasePath,
	}
	mergeServerConfig(&cfg, fileCfg)

	if cfg.GRPCAddr != ":5443" {
		t.Fatalf("expected grpc addr from file, got %q", cfg.GRPCAddr)
	}
	if cfg.HTTPAddr != ":7777" {
		t.Fatalf("expected explicit flag value to win for http addr, got %q", cfg.HTTPAddr)
	}
	if cfg.DBPath != "/var/lib/jcmanager/custom.db" {
		t.Fatalf("expected db path from file, got %q", cfg.DBPath)
	}
	if cfg.Token != "from-file-agent" || cfg.APIToken != "from-file-api" {
		t.Fatalf("expected tokens from file, got %#v", cfg)
	}
	if cfg.ExternalURL != "https://panel.example.test" {
		t.Fatalf("expected external url from file, got %q", cfg.ExternalURL)
	}
}

func TestGetNodeConfigContentReturnsStructuredContent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "config.yaml")

	record := nodeRecord{
		ID:               "node-1",
		DisplayName:      "node-1",
		Hostname:         "node-1",
		SessionTokenHash: hashToken("session-node-1"),
		AllowedPathsJSON: mustJSON(t, []string{tempDir}),
		ServicesJSON: mustJSON(t, []*jcmanagerpb.ServiceStatus{
			{Name: "xrayr", ConfigPath: targetPath},
		}),
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	router := newTestAPIRouter(srv)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		command, err := srv.dispatcher.queue("node-1").pop(ctx)
		if err != nil {
			t.Errorf("pop queued command: %v", err)
			return
		}
		if command.GetFileRead() == nil || command.GetFileRead().GetPath() != targetPath {
			t.Errorf("unexpected file read command: %#v", command)
			return
		}

		srv.waiters.deliver(&jcmanagerpb.CommandResult{
			NodeId:    "node-1",
			CommandId: command.GetCommandId(),
			Type:      jcmanagerpb.CommandType_COMMAND_TYPE_FILE_READ,
			Status:    jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS,
			Payload: &jcmanagerpb.CommandResult_FileRead{
				FileRead: &jcmanagerpb.FileReadResponse{
					Path:        targetPath,
					Content:     []byte("log:\n  level: info\n"),
					SizeBytes:   19,
					ModTimeUnix: 1_710_000_000,
				},
			},
		})
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/config/content?path="+targetPath, nil)
	req.Header.Set("Authorization", "Bearer api-secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	<-done

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var response nodeConfigContentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Format != "yaml" {
		t.Fatalf("expected yaml format, got %#v", response)
	}
	structured, ok := response.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content map, got %#v", response.StructuredContent)
	}
	logValue, ok := structured["log"].(map[string]any)
	if !ok || logValue["level"] != "info" {
		t.Fatalf("unexpected structured content %#v", response.StructuredContent)
	}
}

func TestCreateNodeReturnsPendingInstallCommand(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	srv.installConfig = config{
		GRPCAddr: ":50051",
		HTTPAddr: ":8080",
	}

	router := newTestAPIRouter(srv)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/create", strings.NewReader(`{"display_name":"HK-01"}`))
	req.Header.Set("Authorization", "Bearer api-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Host = "panel.example.test:8080"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var response createNodeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode create node response: %v", err)
	}
	if response.Status != nodeStatusPendingInstall {
		t.Fatalf("expected pending_install status, got %#v", response)
	}
	if response.ID == "" || response.InstallSecret == "" {
		t.Fatalf("expected id and install secret, got %#v", response)
	}
	if !strings.Contains(response.InstallCommand, "/install.sh?secret="+response.InstallSecret) {
		t.Fatalf("expected install command with secret, got %q", response.InstallCommand)
	}
	if strings.Contains(response.InstallCommand, "agent-secret") {
		t.Fatalf("install command must not leak shared agent token, got %q", response.InstallCommand)
	}

	var record nodeRecord
	if err := srv.db.First(&record, "id = ?", response.ID).Error; err != nil {
		t.Fatalf("load pending node: %v", err)
	}
	if record.Status != nodeStatusPendingInstall {
		t.Fatalf("expected pending status in db, got %q", record.Status)
	}
	if string(record.InstallSecretHash) == response.InstallSecret || len(record.InstallSecretHash) == 0 {
		t.Fatalf("expected hashed install secret in db, got %#v", record.InstallSecretHash)
	}
	if record.InstallSecretExpiresAt == nil || !record.InstallSecretExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expected future install secret expiry, got %#v", record.InstallSecretExpiresAt)
	}
}

func TestInstallScriptInjectsPendingNodeValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	record := nodeRecord{
		ID:                     "pending-node",
		DisplayName:            "HK-01",
		Status:                 nodeStatusPendingInstall,
		InstallSecretHash:      hashToken("install-secret"),
		InstallSecretExpiresAt: timePtr(time.Now().UTC().Add(time.Hour)),
		RegisteredAt:           time.Now().UTC(),
		LastRegisterAt:         time.Now().UTC(),
		ServiceFlavorsJSON:     mustJSON(t, []string{}),
		AllowedPathsJSON:       mustJSON(t, []string{}),
		ServicesJSON:           mustJSON(t, []string{}),
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed pending node: %v", err)
	}

	router := gin.New()
	srv.registerInstallRoutes(router, config{
		GRPCAddr: ":50051",
		HTTPAddr: ":8080",
	})

	req := httptest.NewRequest(http.MethodGet, "/install.sh?secret=install-secret", nil)
	req.Host = "panel.example.test:8080"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `NODE_ID="pending-node"`) {
		t.Fatalf("expected node id injection, got %s", body)
	}
	if !strings.Contains(body, `INSTALL_SECRET="install-secret"`) {
		t.Fatalf("expected install secret injection, got %s", body)
	}
	if !strings.Contains(body, `INJECTED_DISPLAY_NAME="HK-01"`) {
		t.Fatalf("expected display name injection, got %s", body)
	}
	if !strings.Contains(body, `SERVER_GRPC="panel.example.test:50051"`) {
		t.Fatalf("expected grpc host injection, got %s", body)
	}
	if strings.Contains(body, `AGENT_TOKEN="agent-secret"`) {
		t.Fatalf("install script must not inject shared agent token for secret installs, got %s", body)
	}
}

func TestRegisterWithInstallSecretActivatesPendingNode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	now := time.Now().UTC()
	record := nodeRecord{
		ID:                     "pending-node",
		DisplayName:            "HK-01",
		Status:                 nodeStatusPendingInstall,
		InstallSecretHash:      hashToken("install-secret"),
		InstallSecretExpiresAt: timePtr(now.Add(time.Hour)),
		RegisteredAt:           now,
		LastRegisterAt:         now,
		ServiceFlavorsJSON:     mustJSON(t, []string{}),
		AllowedPathsJSON:       mustJSON(t, []string{}),
		ServicesJSON:           mustJSON(t, []string{}),
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed pending node: %v", err)
	}

	resp, err := srv.Register(context.Background(), &jcmanagerpb.RegisterRequest{
		Node: &jcmanagerpb.NodeInfo{
			NodeId:       "pending-node",
			Hostname:     "hk-host",
			DisplayName:  "ignored-name",
			PrimaryIp:    "10.0.0.8",
			Os:           "linux",
			Arch:         "amd64",
			Kernel:       "6.8.0",
			AgentVersion: "dev",
		},
		Token:         "agent-secret",
		InstallSecret: "install-secret",
	})
	if err != nil {
		t.Fatalf("register with install secret: %v", err)
	}
	if resp.GetNodeId() != "pending-node" {
		t.Fatalf("expected claimed node id, got %#v", resp)
	}
	if resp.GetDisplayName() != "HK-01" {
		t.Fatalf("expected precreated display name, got %#v", resp)
	}
	if resp.GetSessionToken() == "" {
		t.Fatalf("expected session token in response, got %#v", resp)
	}

	var updated nodeRecord
	if err := srv.db.First(&updated, "id = ?", "pending-node").Error; err != nil {
		t.Fatalf("load updated node: %v", err)
	}
	if updated.Status != nodeStatusActive {
		t.Fatalf("expected active status after claim, got %q", updated.Status)
	}
	if len(updated.InstallSecretHash) != 0 {
		t.Fatalf("expected install secret cleared after claim, got %#v", updated.InstallSecretHash)
	}
	if updated.InstallSecretExpiresAt != nil {
		t.Fatalf("expected install secret expiry cleared after claim, got %#v", updated.InstallSecretExpiresAt)
	}
	if updated.DisplayName != "HK-01" {
		t.Fatalf("expected precreated display name to win, got %q", updated.DisplayName)
	}
}

func TestDownloadAgentAcceptsPendingInstallSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	record := nodeRecord{
		ID:                     "pending-node",
		DisplayName:            "HK-01",
		Status:                 nodeStatusPendingInstall,
		InstallSecretHash:      hashToken("install-secret"),
		InstallSecretExpiresAt: timePtr(time.Now().UTC().Add(time.Hour)),
		RegisteredAt:           time.Now().UTC(),
		LastRegisterAt:         time.Now().UTC(),
		ServiceFlavorsJSON:     mustJSON(t, []string{}),
		AllowedPathsJSON:       mustJSON(t, []string{}),
		ServicesJSON:           mustJSON(t, []string{}),
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed pending node: %v", err)
	}

	rootDir := t.TempDir()
	agentsDir := filepath.Join(rootDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	binaryPath := filepath.Join(agentsDir, "jcmanager-agent-linux-amd64")
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(rootDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()

	router := gin.New()
	srv.registerInstallRoutes(router, config{})

	req := httptest.NewRequest(http.MethodGet, "/download/agent?arch=amd64&secret=install-secret", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for secret download, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetInstallCommandRequiresAPIAuthAndUsesBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	srv.installConfig = config{
		GRPCAddr: ":50051",
		HTTPAddr: ":8080",
	}
	router := newTestAPIRouter(srv)

	req := httptest.NewRequest(http.MethodGet, "/api/install-command", nil)
	req.Host = "panel.example.test:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer api-secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var response installCommandResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode install command response: %v", err)
	}
	if !strings.Contains(response.InstallCommand, "https://panel.example.test:8080/install.sh") {
		t.Fatalf("expected forwarded https origin, got %q", response.InstallCommand)
	}
	if !strings.Contains(response.InstallCommand, "--token 'agent-secret'") {
		t.Fatalf("expected command to carry shared agent token via authenticated api, got %q", response.InstallCommand)
	}
}

func TestClaimNodePromotesUnclaimedNode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := newTestServer(t)
	now := time.Now().UTC()
	record := nodeRecord{
		ID:                 "unclaimed-node",
		DisplayName:        "Host 01",
		Hostname:           "host-01",
		Status:             nodeStatusUnclaimed,
		SessionTokenHash:   hashToken("session"),
		RegisteredAt:       now,
		LastRegisterAt:     now,
		ServiceFlavorsJSON: mustJSON(t, []string{}),
		AllowedPathsJSON:   mustJSON(t, []string{}),
		ServicesJSON:       mustJSON(t, []string{}),
	}
	if err := srv.db.Create(&record).Error; err != nil {
		t.Fatalf("seed unclaimed node: %v", err)
	}

	router := newTestAPIRouter(srv)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/unclaimed-node/claim", strings.NewReader(`{"display_name":"HK-01"}`))
	req.Header.Set("Authorization", "Bearer api-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var updated nodeRecord
	if err := srv.db.First(&updated, "id = ?", "unclaimed-node").Error; err != nil {
		t.Fatalf("load updated node: %v", err)
	}
	if updated.Status != nodeStatusActive {
		t.Fatalf("expected active status after claim, got %q", updated.Status)
	}
	if updated.DisplayName != "HK-01" {
		t.Fatalf("expected updated display name after claim, got %q", updated.DisplayName)
	}
}

func TestRegisterFrontendRoutesServesSPA(t *testing.T) {
	gin.SetMode(gin.TestMode)

	distDir := t.TempDir()
	assetsDir := filepath.Join(distDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("create assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html>spa</html>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	t.Setenv("JCMANAGER_WEB_DIST", distDir)

	srv := newTestServer(t)
	router := gin.New()
	srv.registerFrontendRoutes(router)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Fatalf("expected index html on /, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/nodes/node-1", nil)
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Fatalf("expected spa fallback for app route, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "console.log") {
		t.Fatalf("expected asset file, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for api route fallback, got %d body=%s", w.Code, w.Body.String())
	}
}

func newTestServer(t *testing.T) *server {
	t.Helper()

	srv, err := newServer(filepath.Join(t.TempDir(), "server.db"), "agent-secret", "api-secret")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

func newTestAPIRouter(srv *server) *gin.Engine {
	router := gin.New()
	api := router.Group("/api")
	api.Use(srv.apiAuthMiddleware())
	srv.registerRoutes(api)
	return router
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}

func timePtr(v time.Time) *time.Time {
	return &v
}
