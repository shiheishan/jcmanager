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
