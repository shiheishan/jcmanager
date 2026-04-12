package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultRuntimeConfigPath = "/etc/jcmanager/agent.yaml"

type RuntimeConfig struct {
	Server          ServerConfig `yaml:"server"`
	NodeID          string       `yaml:"node_id"`
	InstallSecret   string       `yaml:"install_secret"`
	DisplayName     string       `yaml:"display_name"`
	PrimaryIP       string       `yaml:"primary_ip"`
	AgentVersion    string       `yaml:"agent_version"`
	XrayRConfigPath string       `yaml:"xrayr_config_path"`
	V2bXConfigPath  string       `yaml:"v2bx_config_path"`
	AllowedPaths    []string     `yaml:"allowed_paths"`
}

type ServerConfig struct {
	Address  string    `yaml:"address"`
	Token    string    `yaml:"token"`
	Insecure bool      `yaml:"insecure"`
	TLS      TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	ServerName string `yaml:"server_name"`
}

func ParseRuntimeConfig(data []byte) (*RuntimeConfig, error) {
	var cfg RuntimeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse runtime config: %w", err)
	}

	cfg.Server.Address = strings.TrimSpace(cfg.Server.Address)
	cfg.Server.Token = strings.TrimSpace(cfg.Server.Token)
	cfg.NodeID = strings.TrimSpace(cfg.NodeID)
	cfg.InstallSecret = strings.TrimSpace(cfg.InstallSecret)
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.PrimaryIP = strings.TrimSpace(cfg.PrimaryIP)
	cfg.AgentVersion = strings.TrimSpace(cfg.AgentVersion)
	cfg.XrayRConfigPath = strings.TrimSpace(cfg.XrayRConfigPath)
	cfg.V2bXConfigPath = strings.TrimSpace(cfg.V2bXConfigPath)
	cfg.Server.TLS.ServerName = strings.TrimSpace(cfg.Server.TLS.ServerName)

	allowed := make([]string, 0, len(cfg.AllowedPaths))
	for _, path := range cfg.AllowedPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		allowed = append(allowed, path)
	}
	cfg.AllowedPaths = allowed

	if cfg.Server.Address == "" {
		return nil, fmt.Errorf("runtime config missing server.address")
	}
	if cfg.Server.Token == "" && cfg.InstallSecret == "" {
		return nil, fmt.Errorf("runtime config requires server.token or install_secret")
	}

	return &cfg, nil
}

func ParseRuntimeConfigFile(path string) (*RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime config %q: %w", path, err)
	}
	return ParseRuntimeConfig(data)
}

func WriteRuntimeConfigFile(path string, cfg *RuntimeConfig) error {
	if cfg == nil {
		return fmt.Errorf("runtime config is required")
	}
	if err := os.MkdirAll(filepath.Dir(strings.TrimSpace(path)), 0o755); err != nil {
		return fmt.Errorf("create runtime config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal runtime config: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write runtime config temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace runtime config: %w", err)
	}
	return nil
}
