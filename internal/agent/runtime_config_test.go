package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRuntimeConfig(t *testing.T) {
	const sample = `
server:
  address: manager.example.com:8443
  token: secret-token
  insecure: true
  tls:
    server_name: manager.internal
node_id: preassigned-node
install_secret: install-secret
display_name: edge-node-01
primary_ip: 203.0.113.10
agent_version: 1.2.3
xrayr_config_path: /etc/XrayR/custom.yml
v2bx_config_path: /etc/V2bX/custom.yml
allowed_paths:
  - /etc/XrayR
  - /etc/V2bX
`

	cfg, err := ParseRuntimeConfig([]byte(sample))
	if err != nil {
		t.Fatalf("ParseRuntimeConfig() error = %v", err)
	}

	if cfg.Server.Address != "manager.example.com:8443" {
		t.Fatalf("unexpected server address: %q", cfg.Server.Address)
	}
	if cfg.Server.Token != "secret-token" {
		t.Fatalf("unexpected token: %q", cfg.Server.Token)
	}
	if !cfg.Server.Insecure {
		t.Fatalf("expected insecure transport")
	}
	if cfg.Server.TLS.ServerName != "manager.internal" {
		t.Fatalf("unexpected tls server name: %q", cfg.Server.TLS.ServerName)
	}
	if cfg.NodeID != "preassigned-node" {
		t.Fatalf("unexpected node id: %q", cfg.NodeID)
	}
	if cfg.InstallSecret != "install-secret" {
		t.Fatalf("unexpected install secret: %q", cfg.InstallSecret)
	}
	if len(cfg.AllowedPaths) != 2 {
		t.Fatalf("unexpected allowed paths: %#v", cfg.AllowedPaths)
	}

	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fileCfg, err := ParseRuntimeConfigFile(path)
	if err != nil {
		t.Fatalf("ParseRuntimeConfigFile() error = %v", err)
	}
	if fileCfg.DisplayName != "edge-node-01" {
		t.Fatalf("unexpected display name: %q", fileCfg.DisplayName)
	}

	fileCfg.InstallSecret = ""
	fileCfg.NodeID = "persisted-node"
	if err := WriteRuntimeConfigFile(path, fileCfg); err != nil {
		t.Fatalf("WriteRuntimeConfigFile() error = %v", err)
	}

	writtenCfg, err := ParseRuntimeConfigFile(path)
	if err != nil {
		t.Fatalf("ParseRuntimeConfigFile() after write error = %v", err)
	}
	if writtenCfg.NodeID != "persisted-node" {
		t.Fatalf("unexpected persisted node id: %q", writtenCfg.NodeID)
	}
	if writtenCfg.InstallSecret != "" {
		t.Fatalf("expected cleared install secret after write, got %q", writtenCfg.InstallSecret)
	}
}

func TestParseRuntimeConfigRequiresServerAddress(t *testing.T) {
	if _, err := ParseRuntimeConfig([]byte("server:\n  token: abc\n")); err == nil {
		t.Fatalf("expected error for missing server.address")
	}
}

func TestParseRuntimeConfigRequiresServerToken(t *testing.T) {
	if _, err := ParseRuntimeConfig([]byte("server:\n  address: manager.example.com:8443\n")); err == nil {
		t.Fatalf("expected error for missing server.token")
	}
}

func TestParseRuntimeConfigAllowsInstallSecretWithoutServerToken(t *testing.T) {
	cfg, err := ParseRuntimeConfig([]byte(`
server:
  address: manager.example.com:8443
install_secret: install-secret
`))
	if err != nil {
		t.Fatalf("expected install secret config to parse, got %v", err)
	}
	if cfg.InstallSecret != "install-secret" {
		t.Fatalf("unexpected install secret: %q", cfg.InstallSecret)
	}
}
