package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	jcmanagerpb "jcmanager/proto"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

const (
	defaultGRPCAddr         = ":50051"
	defaultHTTPAddr         = ":8080"
	defaultDatabasePath     = "jcmanager.db"
	defaultShutdownTimeout  = 10 * time.Second
	pendingInstallSecretTTL = 7 * 24 * time.Hour

	nodeStatusPendingInstall = "pending_install"
	nodeStatusUnclaimed      = "unclaimed"
	nodeStatusActive         = "active"
)

type config struct {
	GRPCAddr    string `yaml:"grpc_addr"`
	HTTPAddr    string `yaml:"http_addr"`
	DBPath      string `yaml:"db_path"`
	Token       string `yaml:"token"`
	APIToken    string `yaml:"api_token"`
	ExternalURL string `yaml:"external_url"`
}

type server struct {
	jcmanagerpb.UnimplementedAgentServiceServer

	db            *gorm.DB
	token         string
	apiToken      string
	installConfig config
	dispatcher    *commandDispatcher
	tasks         *taskStore
	waiters       *commandResultWaiters
}

type commandResultWaiters struct {
	mu      sync.Mutex
	waiters map[string]chan *jcmanagerpb.CommandResult
}

type nodeRecord struct {
	ID                     string     `gorm:"primaryKey;size:64"`
	Hostname               string     `gorm:"not null;default:''"`
	DisplayName            string     `gorm:"not null;default:''"`
	PrimaryIP              string     `gorm:"not null;default:''"`
	OS                     string     `gorm:"column:os;not null;default:''"`
	Arch                   string     `gorm:"not null;default:''"`
	Kernel                 string     `gorm:"not null;default:''"`
	AgentVersion           string     `gorm:"not null;default:''"`
	SessionTokenHash       []byte     `gorm:"column:session_token_hash;not null;default:''"`
	InstallSecretHash      []byte     `gorm:"column:install_secret_hash"`
	Status                 string     `gorm:"not null;default:'active';index"`
	InstallSecretExpiresAt *time.Time `gorm:"column:install_secret_expires_at"`
	ServiceFlavorsJSON     []byte     `gorm:"column:service_flavors_json;not null;default:''"`
	AllowedPathsJSON       []byte     `gorm:"column:allowed_paths_json;not null;default:''"`
	ServicesJSON           []byte     `gorm:"column:services_json;not null;default:''"`
	RegisteredAt           time.Time  `gorm:"not null"`
	LastRegisterAt         time.Time  `gorm:"not null"`
	LastHeartbeatAt        *time.Time
	LastAgentTimeUnix      int64 `gorm:"not null;default:0"`
	Online                 bool  `gorm:"not null;default:false"`
	CPUPercent             float64
	MemoryUsedBytes        uint64  `gorm:"not null;default:0"`
	MemoryTotalBytes       uint64  `gorm:"not null;default:0"`
	DiskUsedBytes          uint64  `gorm:"not null;default:0"`
	DiskTotalBytes         uint64  `gorm:"not null;default:0"`
	Load1                  float64 `gorm:"column:load_1"`
	Load5                  float64 `gorm:"column:load_5"`
	Load15                 float64 `gorm:"column:load_15"`
	ConfigError            string  `gorm:"not null;default:''"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type commandResultRecord struct {
	ID                uint64 `gorm:"primaryKey;autoIncrement"`
	NodeID            string `gorm:"not null;index;uniqueIndex:idx_node_command"`
	CommandID         string `gorm:"not null;uniqueIndex:idx_node_command"`
	Type              int32  `gorm:"not null"`
	Status            int32  `gorm:"not null"`
	Message           string `gorm:"not null;default:''"`
	Stdout            string `gorm:"not null;default:''"`
	Stderr            string `gorm:"not null;default:''"`
	BackupPath        string `gorm:"not null;default:''"`
	Changed           bool   `gorm:"not null;default:false"`
	HealthCheckPassed bool   `gorm:"not null;default:false"`
	ReportedAtUnix    int64  `gorm:"not null;default:0"`
	RawJSON           []byte `gorm:"column:raw_json;not null;default:''"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type nodeSummaryResponse struct {
	ID                string              `json:"id"`
	Hostname          string              `json:"hostname"`
	DisplayName       string              `json:"display_name"`
	Status            string              `json:"status"`
	PrimaryIP         string              `json:"primary_ip"`
	OS                string              `json:"os"`
	Arch              string              `json:"arch"`
	AgentVersion      string              `json:"agent_version"`
	RegisteredAt      time.Time           `json:"registered_at"`
	LastRegisterAt    time.Time           `json:"last_register_at"`
	LastHeartbeatAt   *time.Time          `json:"last_heartbeat_at,omitempty"`
	LastAgentTimeUnix int64               `json:"last_agent_time_unix"`
	Online            bool                `json:"online"`
	CPUPercent        float64             `json:"cpu_percent"`
	MemoryUsedBytes   uint64              `json:"memory_used_bytes"`
	MemoryTotalBytes  uint64              `json:"memory_total_bytes"`
	DiskUsedBytes     uint64              `json:"disk_used_bytes"`
	DiskTotalBytes    uint64              `json:"disk_total_bytes"`
	Load1             float64             `json:"load_1"`
	Load5             float64             `json:"load_5"`
	Load15            float64             `json:"load_15"`
	ConfigError       string              `json:"config_error"`
	ServiceFlavors    []string            `json:"service_flavors"`
	Services          []*apiServiceStatus `json:"services"`
}

type nodeDetailResponse struct {
	nodeSummaryResponse
	Kernel    string    `json:"kernel"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type nodeConfigResponse struct {
	ID             string    `json:"id"`
	Hostname       string    `json:"hostname"`
	DisplayName    string    `json:"display_name"`
	Status         string    `json:"status"`
	PrimaryIP      string    `json:"primary_ip"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	Kernel         string    `json:"kernel"`
	AgentVersion   string    `json:"agent_version"`
	RegisteredAt   time.Time `json:"registered_at"`
	LastRegisterAt time.Time `json:"last_register_at"`
	ServiceFlavors []string  `json:"service_flavors"`
	AllowedPaths   []string  `json:"allowed_paths"`
	ConfigPaths    []string  `json:"config_paths"`
}

type nodeConfigContentResponse struct {
	NodeID            string    `json:"node_id"`
	Path              string    `json:"path"`
	Format            string    `json:"format"`
	RawContent        string    `json:"raw_content"`
	StructuredContent any       `json:"structured_content,omitempty"`
	StructuredError   string    `json:"structured_error,omitempty"`
	SizeBytes         uint64    `json:"size_bytes"`
	ModTimeUnix       int64     `json:"mod_time_unix"`
	FetchedAt         time.Time `json:"fetched_at"`
}

type createNodeRequest struct {
	DisplayName string `json:"display_name"`
}

type createNodeResponse struct {
	ID             string    `json:"id"`
	DisplayName    string    `json:"display_name"`
	InstallSecret  string    `json:"install_secret"`
	InstallCommand string    `json:"install_command"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type claimNodeRequest struct {
	DisplayName string `json:"display_name"`
}

type installCommandResponse struct {
	InstallCommand string `json:"install_command"`
}

type apiServiceStatus struct {
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	Listening  bool   `json:"listening"`
	ListenPort uint32 `json:"listen_port"`
	Message    string `json:"message"`
}

func main() {
	cfg := config{}
	configPath := flag.String("config", "", "path to a YAML config file")
	flag.StringVar(&cfg.GRPCAddr, "grpc-addr", defaultGRPCAddr, "address for the gRPC agent server")
	flag.StringVar(&cfg.HTTPAddr, "http-addr", defaultHTTPAddr, "address for the REST API server")
	flag.StringVar(&cfg.DBPath, "db-path", defaultDatabasePath, "path to the SQLite database")
	flag.StringVar(&cfg.Token, "token", "", "shared registration token required from agents")
	flag.StringVar(&cfg.APIToken, "api-token", "", "bearer token required for REST API access")
	flag.StringVar(&cfg.ExternalURL, "external-url", "", "public HTTP base URL used in generated install commands")
	flag.Parse()

	if strings.TrimSpace(*configPath) != "" {
		fileCfg, err := loadServerConfigFile(*configPath)
		if err != nil {
			log.Fatalf("load config file: %v", err)
		}
		mergeServerConfig(&cfg, fileCfg)
	}

	cfg.Token = strings.TrimSpace(firstNonEmpty(cfg.Token, os.Getenv("JCMANAGER_AGENT_TOKEN")))
	cfg.APIToken = strings.TrimSpace(firstNonEmpty(cfg.APIToken, os.Getenv("JCMANAGER_API_TOKEN")))
	cfg.ExternalURL = strings.TrimSpace(firstNonEmpty(cfg.ExternalURL, os.Getenv("JCMANAGER_EXTERNAL_URL")))
	if cfg.Token == "" {
		log.Fatalf("agent registration token is required, set -token or JCMANAGER_AGENT_TOKEN")
	}
	if cfg.APIToken == "" {
		log.Fatalf("REST API token is required, set -api-token or JCMANAGER_API_TOKEN")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := newServer(cfg.DBPath, cfg.Token, cfg.APIToken)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}
	srv.installConfig = cfg

	grpcListener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("listen gRPC %q: %v", cfg.GRPCAddr, err)
	}
	defer grpcListener.Close()

	grpcServer := grpc.NewServer()
	jcmanagerpb.RegisterAgentServiceServer(grpcServer, srv)

	router := gin.New()
	router.Use(gin.Recovery())
	srv.registerInstallRoutes(router, cfg)
	srv.registerFrontendRoutes(router)

	api := router.Group("/api")
	api.Use(srv.apiAuthMiddleware())
	srv.registerRoutes(api)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)

	go func() {
		log.Printf("gRPC server listening on %s", cfg.GRPCAddr)
		if err := grpcServer.Serve(grpcListener); err != nil {
			errCh <- fmt.Errorf("gRPC serve: %w", err)
		}
	}()

	go func() {
		log.Printf("HTTP API listening on %s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTP serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutdown requested")
	case err := <-errCh:
		stop()
		log.Fatalf("server exited: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()

	httpErrCh := make(chan error, 1)
	go func() {
		httpErrCh <- httpServer.Shutdown(shutdownCtx)
	}()

	grpcStopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcStopped)
	}()

	select {
	case <-grpcStopped:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}

	select {
	case err := <-httpErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("HTTP shutdown: %v", err)
		}
	case <-shutdownCtx.Done():
	}
}

func newServer(dbPath, token, apiToken string) (*server, error) {
	db, err := openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	return &server{
		db:         db,
		token:      strings.TrimSpace(token),
		apiToken:   strings.TrimSpace(apiToken),
		dispatcher: newCommandDispatcher(),
		tasks:      newTaskStore(),
		waiters:    newCommandResultWaiters(),
	}, nil
}

func openDatabase(dbPath string) (*gorm.DB, error) {
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := gorm.Open(sqlite.Dialector{
		DriverName: "sqlite",
		DSN:        dbPath,
	}, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql database handle: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	} {
		if err := db.Exec(pragma).Error; err != nil {
			return nil, fmt.Errorf("apply sqlite pragma %q: %w", pragma, err)
		}
	}

	if err := db.AutoMigrate(&nodeRecord{}, &commandResultRecord{}); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	if err := backfillNodeRecordDefaults(db); err != nil {
		return nil, fmt.Errorf("backfill node defaults: %w", err)
	}

	return db, nil
}

func (s *server) Register(ctx context.Context, req *jcmanagerpb.RegisterRequest) (*jcmanagerpb.RegisterResponse, error) {
	installSecret := strings.TrimSpace(req.GetInstallSecret())
	if installSecret == "" {
		if err := s.checkToken(req.GetToken()); err != nil {
			return nil, err
		}
	}

	node := req.GetNode()
	if node == nil {
		return nil, status.Error(codes.InvalidArgument, "register request missing node")
	}

	nodeID := strings.TrimSpace(node.GetNodeId())

	serviceFlavorsJSON, err := json.Marshal(compactStrings(node.GetServiceFlavors()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal service flavors: %v", err)
	}
	allowedPathsJSON, err := json.Marshal(compactStrings(node.GetAllowedPaths()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal allowed paths: %v", err)
	}

	now := time.Now().UTC()
	var record nodeRecord
	displayName := strings.TrimSpace(node.GetDisplayName())
	sessionToken := strings.TrimSpace(req.GetSessionToken())
	isNewNode := false

	switch {
	case installSecret != "":
		record, err = s.loadPendingNodeByInstallSecret(ctx, installSecret, now)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.PermissionDenied, "invalid install secret")
			}
			return nil, status.Errorf(codes.Internal, "load pending node: %v", err)
		}
		if nodeID != "" && nodeID != record.ID {
			return nil, status.Error(codes.PermissionDenied, "install secret does not match requested node")
		}
		nodeID = record.ID
		sessionToken, err = generateSessionToken()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "generate session token: %v", err)
		}
		record.SessionTokenHash = hashToken(sessionToken)
		record.Status = nodeStatusActive
		record.InstallSecretHash = nil
		record.InstallSecretExpiresAt = nil
		if strings.TrimSpace(record.DisplayName) != "" {
			displayName = strings.TrimSpace(record.DisplayName)
		}
	case nodeID != "":
		err = s.db.WithContext(ctx).First(&record, "id = ?", nodeID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.Internal, "load node: %v", err)
		}
		if err == nil {
			if err := validateSessionToken(record.SessionTokenHash, sessionToken); err != nil {
				return nil, err
			}
			if displayName == "" {
				displayName = strings.TrimSpace(record.DisplayName)
			}
			break
		}
		isNewNode = true
		fallthrough
	default:
		if !isNewNode {
			isNewNode = true
		}
		nodeID, err = generateNodeID()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "generate node id: %v", err)
		}
		sessionToken, err = generateSessionToken()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "generate session token: %v", err)
		}
		record = nodeRecord{
			ID:                 nodeID,
			Status:             nodeStatusUnclaimed,
			RegisteredAt:       now,
			SessionTokenHash:   hashToken(sessionToken),
			ServiceFlavorsJSON: serviceFlavorsJSON,
			AllowedPathsJSON:   allowedPathsJSON,
			ServicesJSON:       []byte("[]"),
		}
	}

	if displayName == "" {
		displayName = strings.TrimSpace(node.GetHostname())
	}
	if displayName == "" {
		displayName = nodeID
	}

	record.ID = nodeID
	record.Hostname = strings.TrimSpace(node.GetHostname())
	record.DisplayName = displayName
	record.PrimaryIP = strings.TrimSpace(node.GetPrimaryIp())
	record.OS = strings.TrimSpace(node.GetOs())
	record.Arch = strings.TrimSpace(node.GetArch())
	record.Kernel = strings.TrimSpace(node.GetKernel())
	record.AgentVersion = strings.TrimSpace(node.GetAgentVersion())
	record.ServiceFlavorsJSON = serviceFlavorsJSON
	record.AllowedPathsJSON = allowedPathsJSON
	record.Status = normalizedNodeStatus(record.Status)
	if installSecret == "" && isNewNode && record.Status == nodeStatusActive {
		record.Status = nodeStatusUnclaimed
	}
	if record.RegisteredAt.IsZero() {
		record.RegisteredAt = now
	}
	record.LastRegisterAt = now

	if err := s.db.WithContext(ctx).Save(&record).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "save node: %v", err)
	}

	return &jcmanagerpb.RegisterResponse{
		NodeId:           nodeID,
		DisplayName:      record.DisplayName,
		RegisteredAtUnix: record.RegisteredAt.Unix(),
		SessionToken:     sessionToken,
	}, nil
}

func (s *server) Heartbeat(ctx context.Context, req *jcmanagerpb.HeartbeatRequest) (*jcmanagerpb.Ack, error) {
	heartbeat := req.GetStatus()
	if heartbeat == nil {
		return nil, status.Error(codes.InvalidArgument, "heartbeat request missing status")
	}

	nodeID := strings.TrimSpace(heartbeat.GetNodeId())
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "heartbeat missing node_id")
	}
	if _, err := s.loadAuthenticatedNode(ctx, nodeID, req.GetSessionToken()); err != nil {
		return nil, err
	}

	servicesJSON, err := json.Marshal(heartbeat.GetServices())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal services: %v", err)
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"last_heartbeat_at":    &now,
		"last_agent_time_unix": heartbeat.GetAgentTimeUnix(),
		"online":               heartbeat.GetOnline(),
		"cpu_percent":          heartbeat.GetCpuPercent(),
		"memory_used_bytes":    heartbeat.GetMemoryUsedBytes(),
		"memory_total_bytes":   heartbeat.GetMemoryTotalBytes(),
		"disk_used_bytes":      heartbeat.GetDiskUsedBytes(),
		"disk_total_bytes":     heartbeat.GetDiskTotalBytes(),
		"load_1":               heartbeat.GetLoad_1(),
		"load_5":               heartbeat.GetLoad_5(),
		"load_15":              heartbeat.GetLoad_15(),
		"config_error":         strings.TrimSpace(heartbeat.GetConfigError()),
		"services_json":        servicesJSON,
	}

	tx := s.db.WithContext(ctx).Model(&nodeRecord{}).Where("id = ?", nodeID).Updates(updates)
	if tx.Error != nil {
		return nil, status.Errorf(codes.Internal, "store heartbeat: %v", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return nil, status.Error(codes.NotFound, "node not registered")
	}

	return ackf("heartbeat stored"), nil
}

func (s *server) WatchCommands(req *jcmanagerpb.WatchCommandsRequest, stream grpc.ServerStreamingServer[jcmanagerpb.Command]) error {
	nodeID := strings.TrimSpace(req.GetNodeId())
	if nodeID == "" {
		return status.Error(codes.InvalidArgument, "watch commands missing node_id")
	}
	if _, err := s.loadAuthenticatedNode(stream.Context(), nodeID, req.GetSessionToken()); err != nil {
		return err
	}

	queue := s.dispatcher.queue(nodeID)
	for {
		command, err := queue.peek(stream.Context())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := stream.Send(command); err != nil {
			return err
		}
		queue.ack(command)
	}
}

func (s *server) ReportResult(ctx context.Context, req *jcmanagerpb.ReportResultRequest) (*jcmanagerpb.Ack, error) {
	result := req.GetResult()
	if result == nil {
		return nil, status.Error(codes.InvalidArgument, "report result missing result")
	}

	nodeID := strings.TrimSpace(result.GetNodeId())
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "report result missing node_id")
	}
	if strings.TrimSpace(result.GetCommandId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "report result missing command_id")
	}
	if _, err := s.loadAuthenticatedNode(ctx, nodeID, req.GetSessionToken()); err != nil {
		return nil, err
	}

	rawJSON, err := protojson.Marshal(result)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal result: %v", err)
	}

	record := commandResultRecord{
		NodeID:            nodeID,
		CommandID:         strings.TrimSpace(result.GetCommandId()),
		Type:              int32(result.GetType()),
		Status:            int32(result.GetStatus()),
		Message:           result.GetMessage(),
		Stdout:            result.GetStdout(),
		Stderr:            result.GetStderr(),
		BackupPath:        result.GetBackupPath(),
		Changed:           result.GetChanged(),
		HealthCheckPassed: result.GetHealthCheckPassed(),
		ReportedAtUnix:    result.GetReportedAtUnix(),
		RawJSON:           rawJSON,
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_id"}, {Name: "command_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"type",
			"status",
			"message",
			"stdout",
			"stderr",
			"backup_path",
			"changed",
			"health_check_passed",
			"reported_at_unix",
			"raw_json",
			"updated_at",
		}),
	}).Create(&record).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "store result: %v", err)
	}

	s.tasks.applyCommandResult(result, s.dispatcher)
	s.waiters.deliver(result)

	return ackf("result stored"), nil
}

func (s *server) registerRoutes(routes gin.IRoutes) {
	routes.GET("/events", s.handleEvents)
	routes.GET("/install-command", s.handleGetInstallCommand)
	routes.GET("/nodes", s.handleListNodes)
	routes.POST("/nodes/create", s.handleCreateNode)
	routes.POST("/nodes/:id/claim", s.handleClaimNode)
	routes.GET("/nodes/:id", s.handleGetNode)
	routes.GET("/nodes/:id/config", s.handleGetNodeConfig)
	routes.GET("/nodes/:id/config/content", s.handleGetNodeConfigContent)
	routes.POST("/nodes/:id/config", s.handlePushNodeConfig)
	routes.POST("/batch/config", s.handleBatchConfig)
}

func (s *server) registerFrontendRoutes(router *gin.Engine) {
	distDir := frontendDistDir()
	if distDir == "" {
		return
	}

	indexPath := filepath.Join(distDir, "index.html")
	assetsDir := filepath.Join(distDir, "assets")
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		router.StaticFS("/assets", gin.Dir(assetsDir, false))
	}

	router.GET("/", func(c *gin.Context) {
		c.File(indexPath)
	})
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if !wantsHTML(c.Request) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.File(indexPath)
	})
}

func (s *server) apiAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if token == c.GetHeader("Authorization") {
			token = ""
		}
		if token == "" {
			token = strings.TrimSpace(c.GetHeader("X-API-Token"))
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.apiToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func (s *server) handleListNodes(c *gin.Context) {
	var records []nodeRecord
	if err := s.db.WithContext(c.Request.Context()).
		Order("display_name ASC").
		Order("id ASC").
		Find(&records).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]nodeSummaryResponse, 0, len(records))
	for _, record := range records {
		summary, err := buildNodeSummary(record)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		resp = append(resp, summary)
	}

	c.JSON(http.StatusOK, resp)
}

func (s *server) handleCreateNode(c *gin.Context) {
	var req createNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "display_name is required"})
		return
	}

	nodeID, err := generateNodeID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("generate node id: %v", err)})
		return
	}
	installSecret, err := generateInstallSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("generate install secret: %v", err)})
		return
	}

	now := time.Now().UTC()
	expiresAt := now.Add(pendingInstallSecretTTL)
	record := nodeRecord{
		ID:                     nodeID,
		DisplayName:            displayName,
		InstallSecretHash:      hashToken(installSecret),
		Status:                 nodeStatusPendingInstall,
		InstallSecretExpiresAt: &expiresAt,
		RegisteredAt:           now,
		LastRegisterAt:         now,
		ServiceFlavorsJSON:     []byte("[]"),
		AllowedPathsJSON:       []byte("[]"),
		ServicesJSON:           []byte("[]"),
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create node: %v", err)})
		return
	}

	c.JSON(http.StatusOK, createNodeResponse{
		ID:             record.ID,
		DisplayName:    record.DisplayName,
		InstallSecret:  installSecret,
		InstallCommand: buildInstallCommand(c.Request, s.installConfig, installSecret, ""),
		Status:         normalizedNodeStatus(record.Status),
		ExpiresAt:      expiresAt,
	})
}

func (s *server) handleGetInstallCommand(c *gin.Context) {
	c.JSON(http.StatusOK, installCommandResponse{
		InstallCommand: buildInstallCommand(c.Request, s.installConfig, "", s.token),
	})
}

func (s *server) handleClaimNode(c *gin.Context) {
	record, ok := s.lookupNodeForHTTP(c)
	if !ok {
		return
	}
	if normalizedNodeStatus(record.Status) != nodeStatusUnclaimed {
		c.JSON(http.StatusConflict, gin.H{"error": "node is not awaiting claim"})
		return
	}

	var req claimNodeRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(record.DisplayName)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(record.Hostname)
	}
	if displayName == "" {
		displayName = record.ID
	}

	if err := s.db.WithContext(c.Request.Context()).
		Model(&nodeRecord{}).
		Where("id = ? AND status = ?", record.ID, nodeStatusUnclaimed).
		Updates(map[string]any{
			"status":       nodeStatusActive,
			"display_name": displayName,
		}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	updated, err := s.loadNodeRecord(c.Request.Context(), record.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary, err := buildNodeSummary(updated)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (s *server) handleGetNode(c *gin.Context) {
	record, ok := s.lookupNodeForHTTP(c)
	if !ok {
		return
	}

	summary, err := buildNodeSummary(record)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, nodeDetailResponse{
		nodeSummaryResponse: summary,
		Kernel:              record.Kernel,
		CreatedAt:           record.CreatedAt,
		UpdatedAt:           record.UpdatedAt,
	})
}

func (s *server) handleGetNodeConfig(c *gin.Context) {
	record, ok := s.lookupNodeForHTTP(c)
	if !ok {
		return
	}

	serviceFlavors, err := decodeStringSlice(record.ServiceFlavorsJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("decode service flavors: %v", err)})
		return
	}
	allowedPaths, err := decodeStringSlice(record.AllowedPathsJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("decode allowed paths: %v", err)})
		return
	}

	c.JSON(http.StatusOK, nodeConfigResponse{
		ID:             record.ID,
		Hostname:       record.Hostname,
		DisplayName:    record.DisplayName,
		Status:         normalizedNodeStatus(record.Status),
		PrimaryIP:      record.PrimaryIP,
		OS:             record.OS,
		Arch:           record.Arch,
		Kernel:         record.Kernel,
		AgentVersion:   record.AgentVersion,
		RegisteredAt:   record.RegisteredAt,
		LastRegisterAt: record.LastRegisterAt,
		ServiceFlavors: serviceFlavors,
		AllowedPaths:   allowedPaths,
		ConfigPaths:    dedupeStrings(extractConfigPaths(record.ServicesJSON)),
	})
}

func (s *server) handleGetNodeConfigContent(c *gin.Context) {
	record, ok := s.lookupNodeForHTTP(c)
	if !ok {
		return
	}

	targetPath := strings.TrimSpace(c.Query("path"))
	if targetPath == "" {
		configPaths := dedupeStrings(extractConfigPaths(record.ServicesJSON))
		if len(configPaths) > 0 {
			targetPath = configPaths[0]
		}
	}
	if targetPath == "" {
		allowedPaths, err := decodeStringSlice(record.AllowedPathsJSON)
		if err == nil {
			for _, candidate := range allowedPaths {
				if strings.Contains(strings.ToLower(candidate), "config") {
					targetPath = candidate
					break
				}
			}
		}
	}
	if targetPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query parameter is required"})
		return
	}

	response, err := s.readNodeConfigContent(c.Request.Context(), record, targetPath)
	if err != nil {
		statusCode := http.StatusInternalServerError
		switch {
		case strings.Contains(err.Error(), "not allowed"), strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), "absolute"):
			statusCode = http.StatusBadRequest
		case strings.Contains(err.Error(), "timed out"):
			statusCode = http.StatusGatewayTimeout
		}
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

func (s *server) lookupNodeForHTTP(c *gin.Context) (nodeRecord, bool) {
	record, err := s.loadNodeRecord(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return nodeRecord{}, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nodeRecord{}, false
	}
	return record, true
}

func (s *server) loadNode(ctx context.Context, nodeID string) (*nodeRecord, error) {
	record, err := s.loadNodeRecord(ctx, nodeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "node not registered")
		}
		return nil, status.Errorf(codes.Internal, "load node: %v", err)
	}
	return &record, nil
}

func (s *server) loadAuthenticatedNode(ctx context.Context, nodeID, sessionToken string) (*nodeRecord, error) {
	record, err := s.loadNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionToken(record.SessionTokenHash, sessionToken); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *server) loadNodeRecord(ctx context.Context, nodeID string) (nodeRecord, error) {
	var record nodeRecord
	err := s.db.WithContext(ctx).First(&record, "id = ?", nodeID).Error
	return record, err
}

func (s *server) loadPendingNodeByInstallSecret(ctx context.Context, installSecret string, now time.Time) (nodeRecord, error) {
	var record nodeRecord
	err := s.db.WithContext(ctx).
		Where("install_secret_hash = ? AND status = ? AND (install_secret_expires_at IS NULL OR install_secret_expires_at > ?)", hashToken(installSecret), nodeStatusPendingInstall, now).
		First(&record).Error
	return record, err
}

func (s *server) checkToken(token string) error {
	if s.token == "" {
		return nil
	}
	if strings.TrimSpace(token) != s.token {
		return status.Error(codes.PermissionDenied, "invalid registration token")
	}
	return nil
}

func buildNodeSummary(record nodeRecord) (nodeSummaryResponse, error) {
	serviceFlavors, err := decodeStringSlice(record.ServiceFlavorsJSON)
	if err != nil {
		return nodeSummaryResponse{}, fmt.Errorf("decode service flavors: %w", err)
	}
	services, err := decodeServices(record.ServicesJSON)
	if err != nil {
		return nodeSummaryResponse{}, fmt.Errorf("decode services: %w", err)
	}

	return nodeSummaryResponse{
		ID:                record.ID,
		Hostname:          record.Hostname,
		DisplayName:       record.DisplayName,
		Status:            normalizedNodeStatus(record.Status),
		PrimaryIP:         record.PrimaryIP,
		OS:                record.OS,
		Arch:              record.Arch,
		AgentVersion:      record.AgentVersion,
		RegisteredAt:      record.RegisteredAt,
		LastRegisterAt:    record.LastRegisterAt,
		LastHeartbeatAt:   record.LastHeartbeatAt,
		LastAgentTimeUnix: record.LastAgentTimeUnix,
		Online:            record.Online,
		CPUPercent:        record.CPUPercent,
		MemoryUsedBytes:   record.MemoryUsedBytes,
		MemoryTotalBytes:  record.MemoryTotalBytes,
		DiskUsedBytes:     record.DiskUsedBytes,
		DiskTotalBytes:    record.DiskTotalBytes,
		Load1:             record.Load1,
		Load5:             record.Load5,
		Load15:            record.Load15,
		ConfigError:       record.ConfigError,
		ServiceFlavors:    serviceFlavors,
		Services:          sanitizeServicesForAPI(services),
	}, nil
}

func decodeStringSlice(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func decodeServices(raw []byte) ([]*jcmanagerpb.ServiceStatus, error) {
	if len(raw) == 0 {
		return []*jcmanagerpb.ServiceStatus{}, nil
	}
	var services []*jcmanagerpb.ServiceStatus
	if err := json.Unmarshal(raw, &services); err != nil {
		return nil, err
	}
	return services, nil
}

func extractConfigPaths(raw []byte) []string {
	services, err := decodeServices(raw)
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(services))
	for _, service := range services {
		if service == nil {
			continue
		}
		path := strings.TrimSpace(service.GetConfigPath())
		if path == "" {
			continue
		}
		out = append(out, path)
	}
	return out
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func frontendDistDir() string {
	if custom := strings.TrimSpace(os.Getenv("JCMANAGER_WEB_DIST")); custom != "" {
		if info, err := os.Stat(custom); err == nil && info.IsDir() {
			return custom
		}
	}

	candidates := []string{
		filepath.Join("web", "dist"),
		filepath.Join("..", "..", "web", "dist"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func wantsHTML(req *http.Request) bool {
	accept := strings.ToLower(req.Header.Get("Accept"))
	return accept == "" || accept == "*/*" || strings.Contains(accept, "text/html")
}

func (s *server) readNodeConfigContent(ctx context.Context, record nodeRecord, targetPath string) (*nodeConfigContentResponse, error) {
	allowedPaths, err := decodeStringSlice(record.AllowedPathsJSON)
	if err != nil {
		return nil, fmt.Errorf("decode allowed paths: %w", err)
	}
	if err := validateServerManagedPath(targetPath, allowedPaths); err != nil {
		return nil, err
	}

	commandID := nextLocalID("cmd")
	waitCh := s.waiters.register(commandID)
	defer s.waiters.remove(commandID)

	s.dispatcher.queue(record.ID).push(&jcmanagerpb.Command{
		CommandId:      commandID,
		Type:           jcmanagerpb.CommandType_COMMAND_TYPE_FILE_READ,
		IssuedAtUnix:   time.Now().UTC().Unix(),
		TimeoutSeconds: 30,
		Payload: &jcmanagerpb.Command_FileRead{
			FileRead: &jcmanagerpb.FileReadCommand{
				Path: targetPath,
			},
		},
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-waitCh:
		if result == nil {
			return nil, fmt.Errorf("file read result missing")
		}
		if result.GetStatus() != jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS {
			return nil, errors.New(firstNonEmpty(result.GetMessage(), result.GetStderr(), "file read failed"))
		}

		fileRead := result.GetFileRead()
		if fileRead == nil {
			return nil, fmt.Errorf("file read payload missing")
		}

		response := &nodeConfigContentResponse{
			NodeID:      record.ID,
			Path:        strings.TrimSpace(fileRead.GetPath()),
			Format:      detectConfigFormat(targetPath, fileRead.GetContent()),
			RawContent:  string(fileRead.GetContent()),
			SizeBytes:   fileRead.GetSizeBytes(),
			ModTimeUnix: fileRead.GetModTimeUnix(),
			FetchedAt:   time.Now().UTC(),
		}
		structured, parseErr := parseStructuredConfig(response.Format, fileRead.GetContent())
		if parseErr != nil {
			response.StructuredError = parseErr.Error()
		} else {
			response.StructuredContent = structured
		}
		return response, nil
	case <-time.After(35 * time.Second):
		return nil, fmt.Errorf("file read timed out")
	}
}

func validateServerManagedPath(targetPath string, allowedPaths []string) error {
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if targetPath == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(targetPath) {
		return fmt.Errorf("path must be absolute")
	}

	for _, allowedPath := range allowedPaths {
		allowedPath = filepath.Clean(strings.TrimSpace(allowedPath))
		if allowedPath == "" {
			continue
		}
		if targetPath == allowedPath {
			return nil
		}
		info, err := os.Stat(allowedPath)
		if err != nil || !info.IsDir() {
			continue
		}
		relativePath, err := filepath.Rel(allowedPath, targetPath)
		if err != nil {
			continue
		}
		if relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q is not allowed for this node", targetPath)
}

func detectConfigFormat(path string, content []byte) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	}

	trimmed := strings.TrimSpace(string(content))
	switch {
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "["):
		return "json"
	case strings.Contains(trimmed, ":"), strings.HasPrefix(trimmed, "- "):
		return "yaml"
	default:
		return "text"
	}
}

func parseStructuredConfig(format string, content []byte) (any, error) {
	switch format {
	case "json":
		var value any
		if err := json.Unmarshal(content, &value); err != nil {
			return nil, err
		}
		return normalizeStructuredValue(value), nil
	case "yaml":
		var value any
		if err := yaml.Unmarshal(content, &value); err != nil {
			return nil, err
		}
		normalized := normalizeStructuredValue(value)
		switch normalized.(type) {
		case map[string]any, []any:
			return normalized, nil
		default:
			return nil, fmt.Errorf("structured yaml content must be an object or array")
		}
	default:
		return nil, fmt.Errorf("plain text config has no structured schema")
	}
}

func normalizeStructuredValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeStructuredValue(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = normalizeStructuredValue(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, normalizeStructuredValue(child))
		}
		return out
	default:
		return typed
	}
}

func newCommandResultWaiters() *commandResultWaiters {
	return &commandResultWaiters{
		waiters: make(map[string]chan *jcmanagerpb.CommandResult),
	}
}

func (w *commandResultWaiters) register(commandID string) <-chan *jcmanagerpb.CommandResult {
	w.mu.Lock()
	defer w.mu.Unlock()

	ch := make(chan *jcmanagerpb.CommandResult, 1)
	w.waiters[strings.TrimSpace(commandID)] = ch
	return ch
}

func (w *commandResultWaiters) remove(commandID string) {
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return
	}

	w.mu.Lock()
	ch, ok := w.waiters[commandID]
	if ok {
		delete(w.waiters, commandID)
	}
	w.mu.Unlock()

	if ok {
		close(ch)
	}
}

func (w *commandResultWaiters) deliver(result *jcmanagerpb.CommandResult) {
	if result == nil {
		return
	}

	commandID := strings.TrimSpace(result.GetCommandId())
	if commandID == "" {
		return
	}

	w.mu.Lock()
	ch, ok := w.waiters[commandID]
	if ok {
		delete(w.waiters, commandID)
	}
	w.mu.Unlock()

	if !ok {
		return
	}

	ch <- result
	close(ch)
}

func generateNodeID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func generateSessionToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func generateInstallSecret() (string, error) {
	return generateSessionToken()
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	hashed := make([]byte, len(sum))
	copy(hashed, sum[:])
	return hashed
}

func backfillNodeRecordDefaults(db *gorm.DB) error {
	return db.Model(&nodeRecord{}).
		Where("status IS NULL OR TRIM(status) = ''").
		Update("status", nodeStatusActive).Error
}

func normalizedNodeStatus(value string) string {
	switch strings.TrimSpace(value) {
	case nodeStatusPendingInstall, nodeStatusUnclaimed, nodeStatusActive:
		return strings.TrimSpace(value)
	default:
		return nodeStatusActive
	}
}

func validateSessionToken(expectedHash []byte, token string) error {
	token = strings.TrimSpace(token)
	if len(expectedHash) == 0 || token == "" {
		return status.Error(codes.Unauthenticated, "invalid node session")
	}
	if subtle.ConstantTimeCompare(expectedHash, hashToken(token)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid node session")
	}
	return nil
}

func sanitizeServicesForAPI(services []*jcmanagerpb.ServiceStatus) []*apiServiceStatus {
	out := make([]*apiServiceStatus, 0, len(services))
	for _, service := range services {
		if service == nil {
			continue
		}
		out = append(out, &apiServiceStatus{
			Name:       service.GetName(),
			Active:     service.GetActive(),
			Listening:  service.GetListening(),
			ListenPort: service.GetListenPort(),
			Message:    service.GetMessage(),
		})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func loadServerConfigFile(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("read config file %q: %w", path, err)
	}

	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("parse config file %q: %w", path, err)
	}

	cfg.GRPCAddr = strings.TrimSpace(cfg.GRPCAddr)
	cfg.HTTPAddr = strings.TrimSpace(cfg.HTTPAddr)
	cfg.DBPath = strings.TrimSpace(cfg.DBPath)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.APIToken = strings.TrimSpace(cfg.APIToken)
	cfg.ExternalURL = strings.TrimSpace(cfg.ExternalURL)
	return cfg, nil
}

func mergeServerConfig(dst *config, src config) {
	if dst == nil {
		return
	}

	if (dst.GRPCAddr == "" || dst.GRPCAddr == defaultGRPCAddr) && src.GRPCAddr != "" {
		dst.GRPCAddr = src.GRPCAddr
	}
	if (dst.HTTPAddr == "" || dst.HTTPAddr == defaultHTTPAddr) && src.HTTPAddr != "" {
		dst.HTTPAddr = src.HTTPAddr
	}
	if (dst.DBPath == "" || dst.DBPath == defaultDatabasePath) && src.DBPath != "" {
		dst.DBPath = src.DBPath
	}
	if dst.Token == "" && src.Token != "" {
		dst.Token = src.Token
	}
	if dst.APIToken == "" && src.APIToken != "" {
		dst.APIToken = src.APIToken
	}
	if dst.ExternalURL == "" && src.ExternalURL != "" {
		dst.ExternalURL = src.ExternalURL
	}
}

func ackf(message string) *jcmanagerpb.Ack {
	return &jcmanagerpb.Ack{
		Ok:             true,
		Message:        message,
		ServerTimeUnix: time.Now().Unix(),
	}
}

// ── install & download routes (unauthenticated) ─────────

func (s *server) registerInstallRoutes(router *gin.Engine, cfg config) {
	router.GET("/install.sh", s.handleInstallScript(cfg))
	router.GET("/download/agent", s.handleDownloadAgent())
}

func (s *server) handleInstallScript(cfg config) gin.HandlerFunc {
	// Read the install script template once at startup.
	scriptTemplate := loadInstallScriptTemplate()

	return func(c *gin.Context) {
		script := scriptTemplate
		secret := strings.TrimSpace(c.Query("secret"))
		displayName := ""
		nodeID := ""
		installSecret := ""
		if secret != "" {
			record, err := s.loadPendingNodeByInstallSecret(c.Request.Context(), secret, time.Now().UTC())
			if err != nil {
				c.Data(http.StatusBadRequest, "text/plain; charset=utf-8", []byte(invalidInstallScript(err)))
				return
			}
			nodeID = record.ID
			displayName = strings.TrimSpace(record.DisplayName)
			installSecret = secret
		}

		script = strings.ReplaceAll(script, "__SERVER_GRPC__", serverGRPCAddress(c.Request, cfg))
		script = strings.ReplaceAll(script, "__SERVER_HTTP__", serverHTTPBase(c.Request, cfg))
		script = strings.ReplaceAll(script, "__AGENT_TOKEN__", "")
		script = strings.ReplaceAll(script, "__NODE_ID__", nodeID)
		script = strings.ReplaceAll(script, "__DISPLAY_NAME__", shellEscapeSingleLine(displayName))
		script = strings.ReplaceAll(script, "__INSTALL_SECRET__", installSecret)

		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
	}
}

func (s *server) handleDownloadAgent() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimSpace(c.Query("token"))
		secret := strings.TrimSpace(c.Query("secret"))
		switch {
		case secret != "":
			if _, err := s.loadPendingNodeByInstallSecret(c.Request.Context(), secret, time.Now().UTC()); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid install secret"})
				return
			}
		case subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1:
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		arch := c.DefaultQuery("arch", "amd64")
		if arch != "amd64" && arch != "arm64" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported arch, use amd64 or arm64"})
			return
		}

		binaryPath := agentBinaryPath(arch)
		if _, err := os.Stat(binaryPath); err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"error": fmt.Sprintf("agent binary not found for %s. Run: make build-agent-linux", arch),
			})
			return
		}

		c.FileAttachment(binaryPath, fmt.Sprintf("jcmanager-agent-linux-%s", arch))
	}
}

func buildInstallCommand(req *http.Request, cfg config, installSecret, agentToken string) string {
	base := serverHTTPBase(req, cfg)
	args := make([]string, 0, 2)
	if strings.TrimSpace(agentToken) != "" {
		args = append(args, "--token", shellQuoteArg(agentToken))
	}

	scriptURL := fmt.Sprintf("%s/install.sh", base)
	if strings.TrimSpace(installSecret) != "" {
		scriptURL += "?secret=" + url.QueryEscape(strings.TrimSpace(installSecret))
	}
	if len(args) == 0 {
		return fmt.Sprintf("curl -fsSL %s | bash", scriptURL)
	}
	return fmt.Sprintf("curl -fsSL %s | bash -s -- %s", scriptURL, strings.Join(args, " "))
}

func serverHTTPBase(req *http.Request, cfg config) string {
	if external := strings.TrimRight(strings.TrimSpace(cfg.ExternalURL), "/"); external != "" {
		return external
	}

	host := strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Forwarded-Host"), req.Host))
	if strings.Contains(host, ",") {
		host = strings.TrimSpace(strings.Split(host, ",")[0])
	}
	if host == "" {
		host = "localhost" + cfg.HTTPAddr
	}
	scheme := "http"
	if proto := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	} else if req.TLS != nil {
		scheme = "https"
	}
	return strings.TrimRight(scheme+"://"+host, "/")
}

func serverGRPCAddress(req *http.Request, cfg config) string {
	httpBase := serverHTTPBase(req, cfg)
	host := strings.TrimPrefix(strings.TrimPrefix(httpBase, "http://"), "https://")
	host = strings.TrimSpace(host)
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	if cfg.GRPCAddr == "" {
		return host
	}
	if strings.HasPrefix(cfg.GRPCAddr, ":") {
		hostOnly := host
		if strings.HasPrefix(hostOnly, "[") {
			if idx := strings.LastIndex(hostOnly, "]"); idx >= 0 {
				hostOnly = hostOnly[:idx+1]
			}
		} else if idx := strings.LastIndex(hostOnly, ":"); idx >= 0 {
			hostOnly = hostOnly[:idx]
		}
		return hostOnly + cfg.GRPCAddr
	}
	return cfg.GRPCAddr
}

func invalidInstallScript(err error) string {
	message := "invalid or expired install secret"
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		message = err.Error()
	}
	return fmt.Sprintf("#!/bin/bash\nset -e\nprintf '%%s\\n' %q\nexit 1\n", message)
}

func shellEscapeSingleLine(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "`", "\\`")
	value = strings.ReplaceAll(value, "$", "\\$")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func shellQuoteArg(value string) string {
	value = strings.ReplaceAll(value, `'`, `'"'"'`)
	return "'" + value + "'"
}

func agentBinaryPath(arch string) string {
	name := fmt.Sprintf("jcmanager-agent-linux-%s", arch)
	// Check ./agents/ directory first (standard location).
	candidates := []string{
		filepath.Join("agents", name),
		filepath.Join("..", "..", "agents", name),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join("agents", name)
}

func loadInstallScriptTemplate() string {
	candidates := []string{
		filepath.Join("scripts", "install.sh"),
		filepath.Join("..", "..", "scripts", "install.sh"),
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return string(data)
		}
	}
	log.Printf("WARNING: scripts/install.sh not found, /install.sh endpoint will return empty script")
	return "#!/bin/bash\necho 'ERROR: install script template not found on server'\nexit 1\n"
}
