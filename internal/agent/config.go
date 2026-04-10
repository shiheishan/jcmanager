package agent

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	XrayRConfigPath = "/etc/XrayR/config.yml"
	V2bXConfigPath  = "/etc/V2bX/config.yml"
)

func ParseXrayRConfig(data []byte) (*XrayRConfig, error) {
	var cfg XrayRConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse XrayR config: %w", err)
	}
	return &cfg, nil
}

func ParseXrayRConfigFile(path string) (*XrayRConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read XrayR config %q: %w", path, err)
	}
	return ParseXrayRConfig(data)
}

func ParseV2bXConfig(data []byte) (*V2bXConfig, error) {
	var cfg V2bXConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse V2bX config: %w", err)
	}
	return &cfg, nil
}

func ParseV2bXConfigFile(path string) (*V2bXConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read V2bX config %q: %w", path, err)
	}
	return ParseV2bXConfig(data)
}

type XrayRConfig struct {
	LogConfig          *XrayRLogConfig        `yaml:"Log"`
	DNSConfigPath      string                 `yaml:"DnsConfigPath"`
	InboundConfigPath  string                 `yaml:"InboundConfigPath"`
	OutboundConfigPath string                 `yaml:"OutboundConfigPath"`
	RouteConfigPath    string                 `yaml:"RouteConfigPath"`
	ConnectionConfig   *XrayRConnectionConfig `yaml:"ConnectionConfig"`
	Nodes              []*XrayRNodeConfig     `yaml:"Nodes"`
}

type XrayRNodeConfig struct {
	PanelType        string                 `yaml:"PanelType"`
	APIConfig        *XrayRAPIConfig        `yaml:"ApiConfig"`
	ControllerConfig *XrayRControllerConfig `yaml:"ControllerConfig"`
}

type XrayRLogConfig struct {
	Level      string `yaml:"Level"`
	AccessPath string `yaml:"AccessPath"`
	ErrorPath  string `yaml:"ErrorPath"`
}

type XrayRConnectionConfig struct {
	Handshake    uint32 `yaml:"Handshake"`
	ConnIdle     uint32 `yaml:"ConnIdle"`
	UplinkOnly   uint32 `yaml:"UplinkOnly"`
	DownlinkOnly uint32 `yaml:"DownlinkOnly"`
	BufferSize   int32  `yaml:"BufferSize"`
}

type XrayRAPIConfig struct {
	APIHost             string  `yaml:"ApiHost"`
	NodeID              int     `yaml:"NodeID"`
	Key                 string  `yaml:"ApiKey"`
	NodeType            string  `yaml:"NodeType"`
	EnableVless         bool    `yaml:"EnableVless"`
	VlessFlow           string  `yaml:"VlessFlow"`
	Timeout             int     `yaml:"Timeout"`
	SpeedLimit          float64 `yaml:"SpeedLimit"`
	DeviceLimit         int     `yaml:"DeviceLimit"`
	RuleListPath        string  `yaml:"RuleListPath"`
	DisableCustomConfig bool    `yaml:"DisableCustomConfig"`
}

type XrayRControllerConfig struct {
	ListenIP                  string                        `yaml:"ListenIP"`
	SendIP                    string                        `yaml:"SendIP"`
	UpdatePeriodic            int                           `yaml:"UpdatePeriodic"`
	CertConfig                *TLSCertConfig                `yaml:"CertConfig"`
	EnableDNS                 bool                          `yaml:"EnableDNS"`
	DNSType                   string                        `yaml:"DNSType"`
	DisableUploadTraffic      bool                          `yaml:"DisableUploadTraffic"`
	DisableGetRule            bool                          `yaml:"DisableGetRule"`
	EnableProxyProtocol       bool                          `yaml:"EnableProxyProtocol"`
	EnableFallback            bool                          `yaml:"EnableFallback"`
	DisableIVCheck            bool                          `yaml:"DisableIVCheck"`
	DisableSniffing           bool                          `yaml:"DisableSniffing"`
	AutoSpeedLimitConfig      *XrayRAutoSpeedLimitConfig    `yaml:"AutoSpeedLimitConfig"`
	GlobalDeviceLimitConfig   *XrayRGlobalDeviceLimitConfig `yaml:"GlobalDeviceLimitConfig"`
	FallBackConfigs           []*XrayRFallBackConfig        `yaml:"FallBackConfigs"`
	DisableLocalREALITYConfig bool                          `yaml:"DisableLocalREALITYConfig"`
	EnableREALITY             bool                          `yaml:"EnableREALITY"`
	REALITYConfigs            *XrayRREALITYConfig           `yaml:"REALITYConfigs"`
}

type XrayRAutoSpeedLimitConfig struct {
	Limit         int `yaml:"Limit"`
	WarnTimes     int `yaml:"WarnTimes"`
	LimitSpeed    int `yaml:"LimitSpeed"`
	LimitDuration int `yaml:"LimitDuration"`
}

type XrayRGlobalDeviceLimitConfig struct {
	Enable        bool   `yaml:"Enable"`
	RedisNetwork  string `yaml:"RedisNetwork"`
	RedisAddr     string `yaml:"RedisAddr"`
	RedisUsername string `yaml:"RedisUsername"`
	RedisPassword string `yaml:"RedisPassword"`
	RedisDB       int    `yaml:"RedisDB"`
	Timeout       int    `yaml:"Timeout"`
	Expiry        int    `yaml:"Expiry"`
}

type XrayRFallBackConfig struct {
	SNI              string `yaml:"SNI"`
	Alpn             string `yaml:"Alpn"`
	Path             string `yaml:"Path"`
	Dest             string `yaml:"Dest"`
	ProxyProtocolVer uint64 `yaml:"ProxyProtocolVer"`
}

type XrayRREALITYConfig struct {
	Show             bool     `yaml:"Show"`
	Dest             string   `yaml:"Dest"`
	ProxyProtocolVer uint64   `yaml:"ProxyProtocolVer"`
	ServerNames      []string `yaml:"ServerNames"`
	PrivateKey       string   `yaml:"PrivateKey"`
	MinClientVer     string   `yaml:"MinClientVer"`
	MaxClientVer     string   `yaml:"MaxClientVer"`
	MaxTimeDiff      uint64   `yaml:"MaxTimeDiff"`
	ShortIDs         []string `yaml:"ShortIds"`
}

type TLSCertConfig struct {
	CertMode         string            `yaml:"CertMode"`
	RejectUnknownSNI bool              `yaml:"RejectUnknownSni"`
	CertDomain       string            `yaml:"CertDomain"`
	CertFile         string            `yaml:"CertFile"`
	KeyFile          string            `yaml:"KeyFile"`
	Provider         string            `yaml:"Provider"`
	Email            string            `yaml:"Email"`
	DNSEnv           map[string]string `yaml:"DNSEnv"`
}

type V2bXConfig struct {
	LogConfig V2bXLogConfig     `yaml:"Log"`
	Cores     []*V2bXCoreConfig `yaml:"Cores"`
	Nodes     []*V2bXNodeConfig `yaml:"Nodes"`
}

type V2bXLogConfig struct {
	Level  string `yaml:"Level"`
	Output string `yaml:"Output"`
}

type V2bXCoreConfig struct {
	Type            string               `yaml:"Type"`
	Name            string               `yaml:"Name"`
	XrayConfig      *V2bXXrayConfig      `yaml:"-"`
	SingConfig      *V2bXSingConfig      `yaml:"-"`
	Hysteria2Config *V2bXHysteria2Config `yaml:"-"`
	Raw             map[string]any       `yaml:"-"`
}

func (c *V2bXCoreConfig) UnmarshalYAML(node *yaml.Node) error {
	type header struct {
		Type string `yaml:"Type"`
		Name string `yaml:"Name"`
	}

	var h header
	if err := node.Decode(&h); err != nil {
		return err
	}
	if err := node.Decode(&c.Raw); err != nil {
		return err
	}

	c.Type = h.Type
	c.Name = h.Name

	switch h.Type {
	case "xray":
		var decoded struct {
			Type           string `yaml:"Type"`
			Name           string `yaml:"Name"`
			V2bXXrayConfig `yaml:",inline"`
		}
		if err := node.Decode(&decoded); err != nil {
			return err
		}
		cfg := decoded.V2bXXrayConfig
		c.XrayConfig = &cfg
	case "sing":
		var decoded struct {
			Type           string `yaml:"Type"`
			Name           string `yaml:"Name"`
			V2bXSingConfig `yaml:",inline"`
		}
		if err := node.Decode(&decoded); err != nil {
			return err
		}
		cfg := decoded.V2bXSingConfig
		c.SingConfig = &cfg
	case "hysteria2":
		var decoded struct {
			Type                string `yaml:"Type"`
			Name                string `yaml:"Name"`
			V2bXHysteria2Config `yaml:",inline"`
		}
		if err := node.Decode(&decoded); err != nil {
			return err
		}
		cfg := decoded.V2bXHysteria2Config
		c.Hysteria2Config = &cfg
	}

	return nil
}

type V2bXXrayConfig struct {
	LogConfig          *V2bXXrayLogConfig        `yaml:"Log"`
	AssetPath          string                    `yaml:"AssetPath"`
	DNSConfigPath      string                    `yaml:"DnsConfigPath"`
	RouteConfigPath    string                    `yaml:"RouteConfigPath"`
	ConnectionConfig   *V2bXXrayConnectionConfig `yaml:"XrayConnectionConfig"`
	InboundConfigPath  string                    `yaml:"InboundConfigPath"`
	OutboundConfigPath string                    `yaml:"OutboundConfigPath"`
}

type V2bXXrayLogConfig struct {
	Level      string `yaml:"Level"`
	AccessPath string `yaml:"AccessPath"`
	ErrorPath  string `yaml:"ErrorPath"`
}

type V2bXXrayConnectionConfig struct {
	Handshake    uint32 `yaml:"handshake"`
	ConnIdle     uint32 `yaml:"connIdle"`
	UplinkOnly   uint32 `yaml:"uplinkOnly"`
	DownlinkOnly uint32 `yaml:"downlinkOnly"`
	BufferSize   int32  `yaml:"bufferSize"`
}

type V2bXSingConfig struct {
	LogConfig    V2bXSingLogConfig `yaml:"Log"`
	NTPConfig    V2bXSingNTPConfig `yaml:"NTP"`
	OriginalPath string            `yaml:"OriginalPath"`
}

type V2bXSingLogConfig struct {
	Disabled  bool   `yaml:"Disable"`
	Level     string `yaml:"Level"`
	Output    string `yaml:"Output"`
	Timestamp bool   `yaml:"Timestamp"`
}

type V2bXSingNTPConfig struct {
	Enable     bool   `yaml:"Enable"`
	Server     string `yaml:"Server"`
	ServerPort uint16 `yaml:"ServerPort"`
}

type V2bXHysteria2Config struct {
	LogConfig V2bXHysteria2LogConfig `yaml:"Log"`
}

type V2bXHysteria2LogConfig struct {
	Level string `yaml:"Level"`
}

type V2bXNodeConfig struct {
	Include   string           `yaml:"Include"`
	APIConfig *V2bXAPIConfig   `yaml:"ApiConfig"`
	Options   *V2bXNodeOptions `yaml:"Options"`
}

type V2bXAPIConfig struct {
	APIHost      string `yaml:"ApiHost"`
	APISendIP    string `yaml:"ApiSendIP"`
	NodeID       int    `yaml:"NodeID"`
	Key          string `yaml:"ApiKey"`
	NodeType     string `yaml:"NodeType"`
	Timeout      int    `yaml:"Timeout"`
	RuleListPath string `yaml:"RuleListPath"`
}

type V2bXNodeOptions struct {
	Name                   string           `yaml:"Name"`
	Core                   string           `yaml:"Core"`
	CoreName               string           `yaml:"CoreName"`
	ListenIP               string           `yaml:"ListenIP"`
	SendIP                 string           `yaml:"SendIP"`
	DeviceOnlineMinTraffic int64            `yaml:"DeviceOnlineMinTraffic"`
	ReportMinTraffic       int64            `yaml:"ReportMinTraffic"`
	LimitConfig            V2bXLimitConfig  `yaml:"LimitConfig"`
	RawOptions             map[string]any   `yaml:"RawOptions"`
	XrayOptions            *V2bXXrayOptions `yaml:"XrayOptions"`
	SingOptions            *V2bXSingOptions `yaml:"SingOptions"`
	Hysteria2ConfigPath    string           `yaml:"Hysteria2ConfigPath"`
	CertConfig             *TLSCertConfig   `yaml:"CertConfig"`
	Raw                    map[string]any   `yaml:"-"`
}

func (o *V2bXNodeOptions) UnmarshalYAML(node *yaml.Node) error {
	type plain V2bXNodeOptions

	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return err
	}

	*o = V2bXNodeOptions(decoded)
	o.Raw = raw

	switch o.Core {
	case "xray":
		if o.XrayOptions == nil {
			o.XrayOptions = &V2bXXrayOptions{}
		}
		merged := *o.XrayOptions
		if err := node.Decode(&merged); err != nil {
			return err
		}
		o.XrayOptions = &merged
	case "sing":
		if o.SingOptions == nil {
			o.SingOptions = &V2bXSingOptions{}
		}
		merged := *o.SingOptions
		if err := node.Decode(&merged); err != nil {
			return err
		}
		o.SingOptions = &merged
	}

	return nil
}

type V2bXLimitConfig struct {
	EnableRealtime          bool                         `yaml:"EnableRealtime"`
	SpeedLimit              int                          `yaml:"SpeedLimit"`
	DeviceLimit             int                          `yaml:"DeviceLimit"`
	ConnLimit               int                          `yaml:"ConnLimit"`
	EnableIPRecorder        bool                         `yaml:"EnableIpRecorder"`
	IPRecorderConfig        *V2bXIPReportConfig          `yaml:"IpRecorderConfig"`
	EnableDynamicSpeedLimit bool                         `yaml:"EnableDynamicSpeedLimit"`
	DynamicSpeedLimitConfig *V2bXDynamicSpeedLimitConfig `yaml:"DynamicSpeedLimitConfig"`
}

type V2bXIPReportConfig struct {
	Periodic       int                 `yaml:"Periodic"`
	Type           string              `yaml:"Type"`
	RecorderConfig *V2bXRecorderConfig `yaml:"RecorderConfig"`
	RedisConfig    *V2bXRedisConfig    `yaml:"RedisConfig"`
	EnableIPSync   bool                `yaml:"EnableIpSync"`
}

type V2bXRecorderConfig struct {
	URL     string `yaml:"Url"`
	Token   string `yaml:"Token"`
	Timeout int    `yaml:"Timeout"`
}

type V2bXRedisConfig struct {
	Address  string `yaml:"Address"`
	Password string `yaml:"Password"`
	DB       int    `yaml:"Db"`
	Expiry   int    `yaml:"Expiry"`
}

type V2bXDynamicSpeedLimitConfig struct {
	Periodic   int   `yaml:"Periodic"`
	Traffic    int64 `yaml:"Traffic"`
	SpeedLimit int   `yaml:"SpeedLimit"`
	ExpireTime int   `yaml:"ExpireTime"`
}

type V2bXXrayOptions struct {
	EnableProxyProtocol bool                     `yaml:"EnableProxyProtocol"`
	EnableDNS           bool                     `yaml:"EnableDNS"`
	DNSType             string                   `yaml:"DNSType"`
	EnableUOT           bool                     `yaml:"EnableUot"`
	EnableTFO           bool                     `yaml:"EnableTFO"`
	DisableIVCheck      bool                     `yaml:"DisableIVCheck"`
	DisableSniffing     bool                     `yaml:"DisableSniffing"`
	EnableFallback      bool                     `yaml:"EnableFallback"`
	FallBackConfigs     []V2bXXrayFallBackConfig `yaml:"FallBackConfigs"`
}

type V2bXXrayFallBackConfig struct {
	SNI              string `yaml:"SNI"`
	Alpn             string `yaml:"Alpn"`
	Path             string `yaml:"Path"`
	Dest             string `yaml:"Dest"`
	ProxyProtocolVer uint64 `yaml:"ProxyProtocolVer"`
}

type V2bXSingOptions struct {
	TCPFastOpen              bool                    `yaml:"EnableTFO"`
	SniffEnabled             bool                    `yaml:"EnableSniff"`
	SniffOverrideDestination bool                    `yaml:"SniffOverrideDestination"`
	EnableDNS                bool                    `yaml:"EnableDNS"`
	DomainStrategy           string                  `yaml:"DomainStrategy"`
	FallBackConfigs          *V2bXSingFallBackConfig `yaml:"FallBackConfigs"`
	Multiplex                *V2bXMultiplexConfig    `yaml:"MultiplexConfig"`
}

type V2bXSingFallBackConfig struct {
	FallBack        V2bXSingFallBack            `yaml:"FallBack"`
	FallBackForALPN map[string]V2bXSingFallBack `yaml:"FallBackForALPN"`
}

type V2bXSingFallBack struct {
	Server     string `yaml:"Server"`
	ServerPort string `yaml:"ServerPort"`
}

type V2bXMultiplexConfig struct {
	Enabled bool              `yaml:"Enable"`
	Padding bool              `yaml:"Padding"`
	Brutal  V2bXBrutalOptions `yaml:"Brutal"`
}

type V2bXBrutalOptions struct {
	Enabled  bool `yaml:"Enable"`
	UpMbps   int  `yaml:"UpMbps"`
	DownMbps int  `yaml:"DownMbps"`
}
