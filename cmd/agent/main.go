package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentcfg "jcmanager/internal/agent"
	jcmanagerpb "jcmanager/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultHeartbeatInterval = 10 * time.Second
	defaultRPCTimeout        = 5 * time.Second
	defaultRetryDelay        = 3 * time.Second
	agentIDPath              = "/etc/jcmanager/agent.id"
)

var errStopAgent = errors.New("stop agent")
var errReRegister = errors.New("re-register")

type serviceSnapshot struct {
	flavors     []string
	allowedPath []string
	services    []*jcmanagerpb.ServiceStatus
	configError string
}

type agentState struct {
	NodeID       string `json:"node_id"`
	SessionToken string `json:"session_token"`
}

func main() {
	configPath := flag.String("config", agentcfg.DefaultRuntimeConfigPath, "path to the agent runtime config")
	flag.Parse()

	cfg, err := agentcfg.ParseRuntimeConfigFile(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent exited with error: %v", err)
	}
}

func run(ctx context.Context, cfg *agentcfg.RuntimeConfig) error {
	conn, err := dialServer(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := jcmanagerpb.NewAgentServiceClient(conn)

	state, err := loadAgentState(agentIDPath)
	if err != nil {
		return fmt.Errorf("load agent state: %w", err)
	}

	for {
		nodeInfo, _ := buildNodeInfo(cfg, state.NodeID)
		registeredState, err := registerUntilSuccess(ctx, client, cfg, nodeInfo, state.SessionToken)
		if err != nil {
			return err
		}
		state = registeredState
		log.Printf("registered node_id=%s display_name=%s", state.NodeID, nodeInfo.GetDisplayName())

		sessionCtx, cancel := context.WithCancel(ctx)
		errCh := make(chan error, 2)
		go func() {
			errCh <- heartbeatLoop(sessionCtx, client, cfg, state)
		}()
		go func() {
			errCh <- watchCommandsLoop(sessionCtx, client, cfg, state)
		}()

		select {
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case err := <-errCh:
			cancel()
			if errors.Is(err, errStopAgent) {
				return nil
			}
			if errors.Is(err, errReRegister) {
				log.Printf("server requested fresh registration for node_id=%s", state.NodeID)
				if err := clearAgentState(agentIDPath); err != nil {
					log.Printf("clear agent state: %v", err)
				}
				state = agentState{}
				if !sleepContext(ctx, defaultRetryDelay) {
					return ctx.Err()
				}
				continue
			}
			return err
		}
	}
}

func dialServer(ctx context.Context, cfg *agentcfg.RuntimeConfig) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(transportCredentials(cfg)),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	}

	dialCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, cfg.Server.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial gRPC server %q: %w", cfg.Server.Address, err)
	}
	return conn, nil
}

func transportCredentials(cfg *agentcfg.RuntimeConfig) credentials.TransportCredentials {
	if cfg.Server.Insecure {
		return insecure.NewCredentials()
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.Server.TLS.ServerName,
	})
}

func registerUntilSuccess(ctx context.Context, client jcmanagerpb.AgentServiceClient, cfg *agentcfg.RuntimeConfig, nodeInfo *jcmanagerpb.NodeInfo, sessionToken string) (agentState, error) {
	nodeID := strings.TrimSpace(nodeInfo.GetNodeId())
	sessionToken = strings.TrimSpace(sessionToken)

	for {
		rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
		resp, err := client.Register(rpcCtx, &jcmanagerpb.RegisterRequest{
			Node:         nodeInfo,
			Token:        cfg.Server.Token,
			SessionToken: sessionToken,
		})
		cancel()
		if err == nil {
			if strings.TrimSpace(resp.GetNodeId()) == "" {
				err = errors.New("register response missing node_id")
			} else if strings.TrimSpace(resp.GetSessionToken()) == "" {
				err = errors.New("register response missing session_token")
			} else {
				nodeID = strings.TrimSpace(resp.GetNodeId())
				sessionToken = strings.TrimSpace(resp.GetSessionToken())
				nodeInfo.NodeId = nodeID
				if strings.TrimSpace(resp.GetDisplayName()) != "" {
					nodeInfo.DisplayName = strings.TrimSpace(resp.GetDisplayName())
				}
				state := agentState{
					NodeID:       nodeID,
					SessionToken: sessionToken,
				}
				if err := persistAgentState(agentIDPath, state); err != nil {
					return agentState{}, fmt.Errorf("persist agent state: %w", err)
				}
				return state, nil
			}
		}

		if status.Code(err) == codes.Unauthenticated && (nodeID != "" || sessionToken != "") {
			log.Printf("persisted session rejected, requesting fresh registration")
			nodeID = ""
			sessionToken = ""
			nodeInfo.NodeId = ""
			if err := clearAgentState(agentIDPath); err != nil {
				log.Printf("clear agent state: %v", err)
			}
			continue
		}

		log.Printf("register failed: %v", err)
		if !sleepContext(ctx, defaultRetryDelay) {
			return agentState{}, ctx.Err()
		}
	}
}

func heartbeatLoop(ctx context.Context, client jcmanagerpb.AgentServiceClient, cfg *agentcfg.RuntimeConfig, state agentState) error {
	ticker := time.NewTicker(defaultHeartbeatInterval)
	defer ticker.Stop()

	for {
		if err := sendHeartbeat(ctx, client, cfg, state); err != nil && ctx.Err() == nil {
			if errors.Is(err, errReRegister) {
				return err
			}
			log.Printf("heartbeat failed: %v", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func sendHeartbeat(ctx context.Context, client jcmanagerpb.AgentServiceClient, cfg *agentcfg.RuntimeConfig, state agentState) error {
	snapshot := loadServiceSnapshot(cfg)
	status := &jcmanagerpb.NodeStatus{
		NodeId:        state.NodeID,
		AgentTimeUnix: time.Now().Unix(),
		Online:        true,
		ConfigError:   snapshot.configError,
		Services:      snapshot.services,
		Load_1:        readLoadAvg(0),
		Load_5:        readLoadAvg(1),
		Load_15:       readLoadAvg(2),
	}

	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	_, err := client.Heartbeat(rpcCtx, &jcmanagerpb.HeartbeatRequest{
		Status:       status,
		SessionToken: state.SessionToken,
	})
	if err != nil {
		if shouldReRegister(err) {
			return errReRegister
		}
		return fmt.Errorf("heartbeat RPC: %w", err)
	}
	return nil
}

func watchCommandsLoop(ctx context.Context, client jcmanagerpb.AgentServiceClient, cfg *agentcfg.RuntimeConfig, state agentState) error {
	for {
		stream, err := client.WatchCommands(ctx, &jcmanagerpb.WatchCommandsRequest{
			NodeId:       state.NodeID,
			SessionToken: state.SessionToken,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if shouldReRegister(err) {
				return errReRegister
			}
			log.Printf("open command stream failed: %v", err)
			if !sleepContext(ctx, defaultRetryDelay) {
				return nil
			}
			continue
		}

		log.Printf("watching commands for node_id=%s", state.NodeID)
		err = consumeCommandStream(ctx, client, cfg, state, stream)
		if errors.Is(err, errStopAgent) {
			return err
		}
		if errors.Is(err, errReRegister) {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("command stream error: %v", err)
		}
		if !sleepContext(ctx, defaultRetryDelay) {
			return nil
		}
	}
}

func consumeCommandStream(ctx context.Context, client jcmanagerpb.AgentServiceClient, cfg *agentcfg.RuntimeConfig, state agentState, stream jcmanagerpb.AgentService_WatchCommandsClient) error {
	for {
		command, err := stream.Recv()
		if err != nil {
			if shouldReRegister(err) {
				return errReRegister
			}
			return err
		}

		log.Printf("received command command_id=%s type=%s", command.GetCommandId(), command.GetType().String())
		if err := handleCommand(ctx, client, cfg, state, command); err != nil {
			return err
		}
	}
}

func handleCommand(ctx context.Context, client jcmanagerpb.AgentServiceClient, cfg *agentcfg.RuntimeConfig, state agentState, command *jcmanagerpb.Command) error {
	commandCtx := ctx
	cancel := func() {}
	if timeoutSeconds := command.GetTimeoutSeconds(); timeoutSeconds > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	}
	defer cancel()

	if deregister := command.GetDeregister(); deregister != nil && deregister.GetStopAgent() {
		result := &jcmanagerpb.CommandResult{
			NodeId:         state.NodeID,
			CommandId:      command.GetCommandId(),
			Type:           command.GetType(),
			ReportedAtUnix: time.Now().Unix(),
			Status:         jcmanagerpb.ResultStatus_RESULT_STATUS_SUCCESS,
			Message:        "agent stopping on deregister command",
		}
		if err := clearAgentState(agentIDPath); err != nil {
			log.Printf("clear agent state: %v", err)
		}
		if err := reportResult(ctx, client, state.SessionToken, result); err != nil {
			log.Printf("report deregister result: %v", err)
		}
		return errStopAgent
	}

	result := executeCommand(commandCtx, cfg, state, command)
	if err := reportResult(ctx, client, state.SessionToken, result); err != nil {
		if errors.Is(err, errReRegister) {
			return err
		}
		log.Printf("report result for command %s: %v", command.GetCommandId(), err)
	}
	return nil
}

func reportResult(ctx context.Context, client jcmanagerpb.AgentServiceClient, sessionToken string, result *jcmanagerpb.CommandResult) error {
	rpcCtx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	defer cancel()

	_, err := client.ReportResult(rpcCtx, &jcmanagerpb.ReportResultRequest{
		Result:       result,
		SessionToken: sessionToken,
	})
	if err != nil {
		if shouldReRegister(err) {
			return errReRegister
		}
		return fmt.Errorf("report result RPC: %w", err)
	}
	return nil
}

func buildNodeInfo(cfg *agentcfg.RuntimeConfig, nodeID string) (*jcmanagerpb.NodeInfo, serviceSnapshot) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	snapshot := loadServiceSnapshot(cfg)
	displayName := cfg.DisplayName
	if displayName == "" {
		displayName = hostname
	}

	primaryIP := cfg.PrimaryIP
	if primaryIP == "" {
		primaryIP = detectPrimaryIP()
	}

	agentVersion := cfg.AgentVersion
	if agentVersion == "" {
		agentVersion = "dev"
	}

	return &jcmanagerpb.NodeInfo{
		NodeId:         nodeID,
		Hostname:       hostname,
		DisplayName:    displayName,
		PrimaryIp:      primaryIP,
		Os:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Kernel:         detectKernel(),
		AgentVersion:   agentVersion,
		ServiceFlavors: snapshot.flavors,
		AllowedPaths:   snapshot.allowedPath,
	}, snapshot
}

func shouldReRegister(err error) bool {
	code := status.Code(err)
	return code == codes.NotFound || code == codes.FailedPrecondition || code == codes.Unauthenticated
}

func loadServiceSnapshot(cfg *agentcfg.RuntimeConfig) serviceSnapshot {
	var (
		errs         []string
		flavors      []string
		allowedPaths = append([]string(nil), cfg.AllowedPaths...)
		services     []*jcmanagerpb.ServiceStatus
	)

	xrayRuntime := detectServiceRuntime([]string{"XrayR", "xrayr"}, []string{"XrayR", "xrayr"})
	v2bxRuntime := detectServiceRuntime([]string{"V2bX", "v2bx"}, []string{"V2bX", "v2bx"})

	xrayPath := cfg.XrayRConfigPath
	if xrayPath == "" {
		xrayPath = agentcfg.XrayRConfigPath
	}
	flavors, allowedPaths, services, errs = appendXrayRSnapshot(flavors, allowedPaths, services, errs, xrayPath, cfg.XrayRConfigPath != "", xrayRuntime)

	v2bxPath := cfg.V2bXConfigPath
	if v2bxPath == "" {
		v2bxPath = agentcfg.V2bXConfigPath
	}
	flavors, allowedPaths, services, errs = appendV2bXSnapshot(flavors, allowedPaths, services, errs, v2bxPath, cfg.V2bXConfigPath != "", v2bxRuntime)

	flavors = dedupeSorted(flavors)
	allowedPaths = dedupeSorted(allowedPaths)

	return serviceSnapshot{
		flavors:     flavors,
		allowedPath: allowedPaths,
		services:    services,
		configError: strings.Join(errs, "; "),
	}
}

type serviceRuntime struct {
	active     bool
	listening  bool
	listenPort uint32
	message    string
}

func appendXrayRSnapshot(flavors, allowedPaths []string, services []*jcmanagerpb.ServiceStatus, errs []string, path string, required bool, runtime serviceRuntime) ([]string, []string, []*jcmanagerpb.ServiceStatus, []string) {
	if path == "" {
		return flavors, allowedPaths, services, errs
	}
	if _, err := os.Stat(path); err != nil {
		if required || !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Sprintf("xrayr config %q: %v", path, err))
			services = append(services, &jcmanagerpb.ServiceStatus{
				Name:       "xrayr",
				ConfigPath: path,
				Message:    err.Error(),
			})
		}
		return flavors, allowedPaths, services, errs
	}

	cfg, err := agentcfg.ParseXrayRConfigFile(path)
	if err != nil {
		errs = append(errs, err.Error())
		services = append(services, &jcmanagerpb.ServiceStatus{
			Name:       "xrayr",
			ConfigPath: path,
			Message:    err.Error(),
		})
		return flavors, allowedPaths, services, errs
	}

	flavors = append(flavors, "xrayr")
	allowedPaths = append(allowedPaths, path, filepath.Dir(path), cfg.DNSConfigPath, cfg.InboundConfigPath, cfg.OutboundConfigPath, cfg.RouteConfigPath)

	if len(cfg.Nodes) == 0 {
		services = append(services, &jcmanagerpb.ServiceStatus{
			Name:       "xrayr",
			Active:     runtime.active,
			Listening:  runtime.listening,
			ListenPort: runtime.listenPort,
			ConfigPath: path,
			Message:    composeServiceMessage(runtime.message, "config parsed"),
		})
	}

	for idx, node := range cfg.Nodes {
		name := "xrayr"
		var messageParts []string
		if node != nil {
			if node.PanelType != "" {
				messageParts = append(messageParts, "panel="+node.PanelType)
			}
			if node.APIConfig != nil {
				if node.APIConfig.NodeType != "" {
					messageParts = append(messageParts, "node_type="+node.APIConfig.NodeType)
				}
				allowedPaths = append(allowedPaths, node.APIConfig.RuleListPath)
			}
			if node.ControllerConfig != nil && node.ControllerConfig.CertConfig != nil {
				allowedPaths = append(allowedPaths, node.ControllerConfig.CertConfig.CertFile, node.ControllerConfig.CertConfig.KeyFile)
			}
		}
		if len(messageParts) == 0 {
			messageParts = append(messageParts, fmt.Sprintf("node_index=%d", idx))
		}
		services = append(services, &jcmanagerpb.ServiceStatus{
			Name:       name,
			Active:     runtime.active,
			Listening:  runtime.listening,
			ListenPort: runtime.listenPort,
			ConfigPath: path,
			Message:    composeServiceMessage(runtime.message, strings.Join(messageParts, " ")),
		})
	}

	return flavors, allowedPaths, services, errs
}

func appendV2bXSnapshot(flavors, allowedPaths []string, services []*jcmanagerpb.ServiceStatus, errs []string, path string, required bool, runtime serviceRuntime) ([]string, []string, []*jcmanagerpb.ServiceStatus, []string) {
	if path == "" {
		return flavors, allowedPaths, services, errs
	}
	if _, err := os.Stat(path); err != nil {
		if required || !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Sprintf("v2bx config %q: %v", path, err))
			services = append(services, &jcmanagerpb.ServiceStatus{
				Name:       "v2bx",
				ConfigPath: path,
				Message:    err.Error(),
			})
		}
		return flavors, allowedPaths, services, errs
	}

	cfg, err := agentcfg.ParseV2bXConfigFile(path)
	if err != nil {
		errs = append(errs, err.Error())
		services = append(services, &jcmanagerpb.ServiceStatus{
			Name:       "v2bx",
			ConfigPath: path,
			Message:    err.Error(),
		})
		return flavors, allowedPaths, services, errs
	}

	flavors = append(flavors, "v2bx")
	allowedPaths = append(allowedPaths, path, filepath.Dir(path))

	for _, core := range cfg.Cores {
		if core == nil {
			continue
		}
		switch core.Type {
		case "xray":
			if core.XrayConfig != nil {
				allowedPaths = append(allowedPaths,
					core.XrayConfig.AssetPath,
					core.XrayConfig.DNSConfigPath,
					core.XrayConfig.RouteConfigPath,
					core.XrayConfig.InboundConfigPath,
					core.XrayConfig.OutboundConfigPath,
				)
			}
		case "sing":
			if core.SingConfig != nil {
				allowedPaths = append(allowedPaths, core.SingConfig.OriginalPath)
			}
		}
	}

	if len(cfg.Nodes) == 0 {
		services = append(services, &jcmanagerpb.ServiceStatus{
			Name:       "v2bx",
			Active:     runtime.active,
			Listening:  runtime.listening,
			ListenPort: runtime.listenPort,
			ConfigPath: path,
			Message:    composeServiceMessage(runtime.message, "config parsed"),
		})
	}

	for idx, node := range cfg.Nodes {
		name := "v2bx"
		var messageParts []string
		if node != nil {
			if node.Options != nil && node.Options.Name != "" {
				name = node.Options.Name
			}
			if node.APIConfig != nil {
				if node.APIConfig.NodeType != "" {
					messageParts = append(messageParts, "node_type="+node.APIConfig.NodeType)
				}
				allowedPaths = append(allowedPaths, node.APIConfig.RuleListPath)
			}
			allowedPaths = append(allowedPaths, node.Include)
			if node.Options != nil {
				allowedPaths = append(allowedPaths, node.Options.Hysteria2ConfigPath)
				if node.Options.CertConfig != nil {
					allowedPaths = append(allowedPaths, node.Options.CertConfig.CertFile, node.Options.CertConfig.KeyFile)
				}
				if node.Options.Core != "" {
					messageParts = append(messageParts, "core="+node.Options.Core)
				}
			}
		}
		if len(messageParts) == 0 {
			messageParts = append(messageParts, fmt.Sprintf("node_index=%d", idx))
		}
		services = append(services, &jcmanagerpb.ServiceStatus{
			Name:       name,
			Active:     runtime.active,
			Listening:  runtime.listening,
			ListenPort: runtime.listenPort,
			ConfigPath: path,
			Message:    composeServiceMessage(runtime.message, strings.Join(messageParts, " ")),
		})
	}

	return flavors, allowedPaths, services, errs
}

func loadAgentState(path string) (agentState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return agentState{}, nil
	}
	if err != nil {
		return agentState{}, err
	}

	var state agentState
	if err := json.Unmarshal(data, &state); err == nil {
		state.NodeID = strings.TrimSpace(state.NodeID)
		state.SessionToken = strings.TrimSpace(state.SessionToken)
		return state, nil
	}

	return agentState{NodeID: strings.TrimSpace(string(data))}, nil
}

func persistAgentState(path string, state agentState) error {
	state.NodeID = strings.TrimSpace(state.NodeID)
	state.SessionToken = strings.TrimSpace(state.SessionToken)
	if state.NodeID == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func clearAgentState(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func dedupeSorted(values []string) []string {
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
	sort.Strings(out)
	return out
}

func composeServiceMessage(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, "; ")
}

func detectServiceRuntime(unitNames, processNames []string) serviceRuntime {
	activeBySystemd, checkedBySystemd := isSystemdUnitActive(unitNames)
	if activeBySystemd {
		listening, port := detectListeningPort(processNames)
		message := "systemd active"
		if listening {
			message = fmt.Sprintf("%s, listening on %d", message, port)
		}
		return serviceRuntime{
			active:     true,
			listening:  listening,
			listenPort: port,
			message:    message,
		}
	}

	active := isProcessRunning(processNames)
	listening, port := detectListeningPort(processNames)
	message := "process not running"
	if active {
		message = "process running"
		if listening {
			message = fmt.Sprintf("%s, listening on %d", message, port)
		}
	} else if checkedBySystemd {
		message = "systemd inactive"
	}
	return serviceRuntime{
		active:     active,
		listening:  listening,
		listenPort: port,
		message:    message,
	}
}

func isSystemdUnitActive(unitNames []string) (bool, bool) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, false
	}

	for _, unit := range unitNames {
		unit = strings.TrimSpace(unit)
		if unit == "" {
			continue
		}
		if err := exec.Command("systemctl", "is-active", "--quiet", unit).Run(); err == nil {
			return true, true
		}
	}

	return false, true
}

func isProcessRunning(processNames []string) bool {
	if _, err := exec.LookPath("pgrep"); err != nil {
		return false
	}

	for _, name := range processNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := exec.Command("pgrep", "-x", name).Run(); err == nil {
			return true
		}
	}

	return false
}

func detectListeningPort(processNames []string) (bool, uint32) {
	if _, err := exec.LookPath("ss"); err != nil {
		return false, 0
	}

	output, err := exec.Command("ss", "-ltnpH").Output()
	if err != nil {
		return false, 0
	}

	for _, line := range strings.Split(string(output), "\n") {
		if line == "" {
			continue
		}
		if !containsProcessName(line, processNames) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		port, ok := parsePort(fields[3])
		if !ok {
			return true, 0
		}
		return true, port
	}

	return false, 0
}

func containsProcessName(line string, processNames []string) bool {
	for _, name := range processNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if strings.Contains(line, "\""+name+"\"") {
			return true
		}
	}
	return false
}

func parsePort(addr string) (uint32, bool) {
	idx := strings.LastIndex(addr, ":")
	if idx == -1 || idx == len(addr)-1 {
		return 0, false
	}
	value, err := strconv.ParseUint(addr[idx+1:], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(value), true
}

func detectPrimaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var fallback string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip == nil || !ip.IsGlobalUnicast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
			if fallback == "" {
				fallback = ip.String()
			}
		}
	}

	return fallback
}

func detectKernel() string {
	output, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func readLoadAvg(index int) float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) <= index {
		return 0
	}

	value, err := strconv.ParseFloat(fields[index], 64)
	if err != nil {
		return 0
	}
	return value
}
