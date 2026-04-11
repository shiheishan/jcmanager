package agent

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultRuntimeConfigPath = "/etc/jcmanager/agent.yaml"

type RuntimeConfig struct {
	Server          ServerConfig `yaml:"server"`
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
	if cfg.Server.Token == "" {
		return nil, fmt.Errorf("runtime config missing server.token")
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
