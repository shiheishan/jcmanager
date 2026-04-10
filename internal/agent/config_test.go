package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseXrayRConfig(t *testing.T) {
	const sample = `
Log:
  Level: warning
  AccessPath: /var/log/xrayr/access.log
  ErrorPath: /var/log/xrayr/error.log
DnsConfigPath: /etc/XrayR/dns.json
InboundConfigPath: /etc/XrayR/inbound.json
OutboundConfigPath: /etc/XrayR/outbound.json
RouteConfigPath: /etc/XrayR/route.json
ConnectionConfig:
  Handshake: 8
  ConnIdle: 42
  UplinkOnly: 4
  DownlinkOnly: 6
  BufferSize: 128
Nodes:
  - PanelType: V2board
    ApiConfig:
      ApiHost: https://panel.example.com
      NodeID: 42
      ApiKey: super-secret
      NodeType: V2ray
      EnableVless: true
      VlessFlow: xtls-rprx-vision
      Timeout: 30
      SpeedLimit: 150.5
      DeviceLimit: 3
      RuleListPath: /etc/XrayR/rules.txt
      DisableCustomConfig: true
    ControllerConfig:
      ListenIP: 0.0.0.0
      SendIP: 203.0.113.10
      UpdatePeriodic: 60
      CertConfig:
        CertMode: dns
        CertDomain: node.example.com
        CertFile: /etc/ssl/node.crt
        KeyFile: /etc/ssl/node.key
        Provider: cloudflare
        Email: admin@example.com
        DNSEnv:
          CF_API_TOKEN: token
        RejectUnknownSni: true
      EnableDNS: true
      DNSType: UseIPv4
      DisableUploadTraffic: true
      DisableGetRule: false
      EnableProxyProtocol: true
      EnableFallback: true
      DisableIVCheck: true
      DisableSniffing: true
      AutoSpeedLimitConfig:
        Limit: 100
        WarnTimes: 3
        LimitSpeed: 20
        LimitDuration: 60
      GlobalDeviceLimitConfig:
        Enable: true
        RedisNetwork: tcp
        RedisAddr: 127.0.0.1:6379
        RedisUsername: default
        RedisPassword: pass
        RedisDB: 1
        Timeout: 5
        Expiry: 120
      FallBackConfigs:
        - SNI: tls.example.com
          Alpn: h2
          Path: /ws
          Dest: 127.0.0.1:8080
          ProxyProtocolVer: 2
      DisableLocalREALITYConfig: true
      EnableREALITY: true
      REALITYConfigs:
        Show: true
        Dest: example.com:443
        ProxyProtocolVer: 1
        ServerNames:
          - a.example.com
          - b.example.com
        PrivateKey: private-key
        MinClientVer: 1.8.0
        MaxClientVer: 1.9.0
        MaxTimeDiff: 10
        ShortIds:
          - "01"
          - "02"
`

	cfg, err := ParseXrayRConfig([]byte(sample))
	if err != nil {
		t.Fatalf("ParseXrayRConfig() error = %v", err)
	}

	if cfg.LogConfig == nil || cfg.LogConfig.AccessPath != "/var/log/xrayr/access.log" {
		t.Fatalf("unexpected log config: %#v", cfg.LogConfig)
	}
	if cfg.ConnectionConfig == nil || cfg.ConnectionConfig.BufferSize != 128 {
		t.Fatalf("unexpected connection config: %#v", cfg.ConnectionConfig)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(cfg.Nodes))
	}

	node := cfg.Nodes[0]
	if node.PanelType != "V2board" {
		t.Fatalf("unexpected panel type: %q", node.PanelType)
	}
	if node.APIConfig == nil || !node.APIConfig.EnableVless || node.APIConfig.SpeedLimit != 150.5 {
		t.Fatalf("unexpected API config: %#v", node.APIConfig)
	}
	if node.ControllerConfig == nil || node.ControllerConfig.CertConfig == nil {
		t.Fatalf("unexpected controller config: %#v", node.ControllerConfig)
	}
	if node.ControllerConfig.CertConfig.DNSEnv["CF_API_TOKEN"] != "token" {
		t.Fatalf("unexpected cert dns env: %#v", node.ControllerConfig.CertConfig.DNSEnv)
	}
	if node.ControllerConfig.REALITYConfigs == nil || len(node.ControllerConfig.REALITYConfigs.ShortIDs) != 2 {
		t.Fatalf("unexpected reality config: %#v", node.ControllerConfig.REALITYConfigs)
	}

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fileCfg, err := ParseXrayRConfigFile(path)
	if err != nil {
		t.Fatalf("ParseXrayRConfigFile() error = %v", err)
	}
	if fileCfg.Nodes[0].ControllerConfig.GlobalDeviceLimitConfig.RedisDB != 1 {
		t.Fatalf("unexpected file parse result: %#v", fileCfg.Nodes[0].ControllerConfig.GlobalDeviceLimitConfig)
	}
}

func TestParseV2bXConfig(t *testing.T) {
	const sample = `
Log:
  Level: info
  Output: /var/log/v2bx.log
Cores:
  - Type: xray
    Name: core-xray
    Log:
      Level: warning
      AccessPath: /var/log/v2bx/xray-access.log
      ErrorPath: /var/log/v2bx/xray-error.log
    AssetPath: /etc/V2bX/
    DnsConfigPath: /etc/V2bX/dns.json
    RouteConfigPath: /etc/V2bX/route.json
    XrayConnectionConfig:
      handshake: 4
      connIdle: 30
      uplinkOnly: 2
      downlinkOnly: 4
      bufferSize: 64
    InboundConfigPath: /etc/V2bX/inbound.json
    OutboundConfigPath: /etc/V2bX/outbound.json
  - Type: sing
    Name: core-sing
    Log:
      Disable: true
      Level: info
      Output: /var/log/v2bx/sing.log
      Timestamp: true
    NTP:
      Enable: true
      Server: time.cloudflare.com
      ServerPort: 123
    OriginalPath: /etc/V2bX/sing-origin.json
  - Type: hysteria2
    Name: core-hy2
    Log:
      Level: error
  - Type: custom-core
    Name: core-custom
    Endpoint: unix:///tmp/custom.sock
Nodes:
  - Include: /etc/V2bX/nodes/xray-node.yml
    ApiConfig:
      ApiHost: https://panel.example.com
      ApiSendIP: 203.0.113.20
      NodeID: 7
      ApiKey: api-key
      NodeType: V2ray
      Timeout: 10
      RuleListPath: /etc/V2bX/rules.txt
    Options:
      Name: Xray Node
      Core: xray
      CoreName: core-xray
      ListenIP: 0.0.0.0
      SendIP: 198.51.100.5
      DeviceOnlineMinTraffic: 1048576
      ReportMinTraffic: 2048
      LimitConfig:
        EnableRealtime: true
        SpeedLimit: 100
        DeviceLimit: 5
        ConnLimit: 50
        EnableIpRecorder: true
        IpRecorderConfig:
          Periodic: 60
          Type: redis
          RecorderConfig:
            Url: https://recorder.example.com/report
            Token: recorder-token
            Timeout: 5
          RedisConfig:
            Address: 127.0.0.1:6379
            Password: redis-pass
            Db: 2
            Expiry: 300
          EnableIpSync: true
        EnableDynamicSpeedLimit: true
        DynamicSpeedLimitConfig:
          Periodic: 15
          Traffic: 1073741824
          SpeedLimit: 25
          ExpireTime: 60
      EnableProxyProtocol: true
      EnableDNS: true
      DNSType: UseIPv4
      EnableUot: true
      EnableTFO: true
      DisableIVCheck: true
      DisableSniffing: false
      EnableFallback: true
      FallBackConfigs:
        - SNI: node.example.com
          Alpn: h2
          Path: /fallback
          Dest: 127.0.0.1:8443
          ProxyProtocolVer: 2
      CertConfig:
        CertMode: file
        RejectUnknownSni: true
        CertDomain: node.example.com
        CertFile: /etc/ssl/node.crt
        KeyFile: /etc/ssl/node.key
        Provider: cloudflare
        Email: admin@example.com
        DNSEnv:
          CF_API_TOKEN: token
  - ApiConfig:
      ApiHost: https://panel.example.com
      ApiSendIP: 203.0.113.21
      NodeID: 8
      ApiKey: sing-key
      NodeType: Trojan
      Timeout: 15
      RuleListPath: /etc/V2bX/sing-rules.txt
    Options:
      Name: Sing Node
      Core: sing
      CoreName: core-sing
      ListenIP: 127.0.0.1
      SendIP: 127.0.0.1
      DeviceOnlineMinTraffic: 4096
      ReportMinTraffic: 2048
      LimitConfig:
        EnableRealtime: false
        SpeedLimit: 0
        DeviceLimit: 0
        ConnLimit: 0
        EnableIpRecorder: false
        EnableDynamicSpeedLimit: false
      EnableTFO: true
      EnableSniff: true
      SniffOverrideDestination: false
      EnableDNS: true
      DomainStrategy: prefer_ipv4
      FallBackConfigs:
        FallBack:
          Server: 127.0.0.1
          ServerPort: "443"
        FallBackForALPN:
          h2:
            Server: 127.0.0.1
            ServerPort: "8443"
      MultiplexConfig:
        Enable: true
        Padding: true
        Brutal:
          Enable: true
          UpMbps: 50
          DownMbps: 100
      CertConfig:
        CertMode: dns
        CertDomain: sing.example.com
        Provider: alidns
        Email: ops@example.com
        DNSEnv:
          ALICLOUD_ACCESS_KEY: key
  - ApiConfig:
      ApiHost: https://panel.example.com
      ApiSendIP: 203.0.113.22
      NodeID: 9
      ApiKey: hy-key
      NodeType: Hysteria2
      Timeout: 20
      RuleListPath: /etc/V2bX/hy-rules.txt
    Options:
      Name: Hy2 Node
      Core: hysteria2
      CoreName: core-hy2
      ListenIP: 0.0.0.0
      SendIP: 0.0.0.0
      DeviceOnlineMinTraffic: 0
      ReportMinTraffic: 0
      LimitConfig:
        EnableRealtime: false
        SpeedLimit: 0
        DeviceLimit: 0
        ConnLimit: 0
        EnableIpRecorder: false
        EnableDynamicSpeedLimit: false
      Hysteria2ConfigPath: /etc/V2bX/hysteria2.json
      ExtraTransport: quic
      CertConfig:
        CertMode: none
`

	cfg, err := ParseV2bXConfig([]byte(sample))
	if err != nil {
		t.Fatalf("ParseV2bXConfig() error = %v", err)
	}

	if cfg.LogConfig.Output != "/var/log/v2bx.log" {
		t.Fatalf("unexpected log output: %q", cfg.LogConfig.Output)
	}
	if len(cfg.Cores) != 4 {
		t.Fatalf("expected 4 cores, got %d", len(cfg.Cores))
	}
	if cfg.Cores[0].XrayConfig == nil || cfg.Cores[0].XrayConfig.ConnectionConfig == nil {
		t.Fatalf("unexpected xray core: %#v", cfg.Cores[0])
	}
	if cfg.Cores[1].SingConfig == nil || !cfg.Cores[1].SingConfig.NTPConfig.Enable {
		t.Fatalf("unexpected sing core: %#v", cfg.Cores[1])
	}
	if cfg.Cores[2].Hysteria2Config == nil || cfg.Cores[2].Hysteria2Config.LogConfig.Level != "error" {
		t.Fatalf("unexpected hysteria2 core: %#v", cfg.Cores[2])
	}
	if cfg.Cores[3].Type != "custom-core" || cfg.Cores[3].Raw["Endpoint"] != "unix:///tmp/custom.sock" {
		t.Fatalf("unexpected custom core preservation: %#v", cfg.Cores[3])
	}

	if len(cfg.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(cfg.Nodes))
	}
	if cfg.Nodes[0].APIConfig == nil || cfg.Nodes[0].Options == nil {
		t.Fatalf("unexpected first node: %#v", cfg.Nodes[0])
	}
	if cfg.Nodes[0].Options.XrayOptions == nil || !cfg.Nodes[0].Options.XrayOptions.EnableUOT {
		t.Fatalf("unexpected xray node options: %#v", cfg.Nodes[0].Options.XrayOptions)
	}
	if cfg.Nodes[1].Options.SingOptions == nil || cfg.Nodes[1].Options.SingOptions.Multiplex == nil {
		t.Fatalf("unexpected sing node options: %#v", cfg.Nodes[1].Options.SingOptions)
	}
	if cfg.Nodes[1].Options.Raw["DomainStrategy"] != "prefer_ipv4" {
		t.Fatalf("unexpected raw option preservation: %#v", cfg.Nodes[1].Options.Raw)
	}
	if cfg.Nodes[2].Options.Hysteria2ConfigPath != "/etc/V2bX/hysteria2.json" || cfg.Nodes[2].Options.Raw["ExtraTransport"] != "quic" {
		t.Fatalf("unexpected hysteria2 option preservation: %#v", cfg.Nodes[2].Options)
	}

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fileCfg, err := ParseV2bXConfigFile(path)
	if err != nil {
		t.Fatalf("ParseV2bXConfigFile() error = %v", err)
	}
	if fileCfg.Nodes[0].Options.CertConfig == nil || fileCfg.Nodes[0].Options.CertConfig.DNSEnv["CF_API_TOKEN"] != "token" {
		t.Fatalf("unexpected file parse result: %#v", fileCfg.Nodes[0].Options.CertConfig)
	}
}
