package panel

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StringOrArray is a type that can unmarshal from either a JSON string or an array of strings.
// When it's an array, the elements are joined with newlines.
type StringOrArray string

func (s *StringOrArray) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = StringOrArray(str)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*s = StringOrArray(strings.Join(arr, "\n"))
		return nil
	}
	return fmt.Errorf("StringOrArray: expected string or []string, got %s", string(data))
}

// HandshakeResponse is the response from POST /api/v2/server/handshake
type HandshakeResponse struct {
	WebSocket WSConfig `json:"websocket"`
	Settings  Settings `json:"settings"`
}

func (h *HandshakeResponse) UnmarshalJSON(data []byte) error {
	type Alias HandshakeResponse
	aux := &struct {
		Settings json.RawMessage `json:"settings"`
		*Alias
	}{
		Alias: (*Alias)(h),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	// If settings is an empty array [], skip unmarshaling it into the struct
	if len(aux.Settings) > 0 && string(aux.Settings) != "[]" {
		if err := json.Unmarshal(aux.Settings, &h.Settings); err != nil {
			return err
		}
	}
	return nil
}

func firstRaw(raw map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if v, ok := raw[key]; ok && len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}

func unmarshalJSONOrString(data json.RawMessage, dst any) error {
	if len(data) == 0 || string(data) == "null" || string(data) == `""` {
		return nil
	}
	if err := json.Unmarshal(data, dst); err == nil {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

func mergeAdvancedSettings(raw map[string]json.RawMessage, nc *NodeConfig) error {
	var advanced map[string]json.RawMessage
	if v := firstRaw(raw, "advanced_settings", "advancedSettings", "advanced", "settings"); len(v) > 0 {
		if err := unmarshalJSONOrString(v, &advanced); err != nil {
			return fmt.Errorf("advanced settings: %w", err)
		}
	}
	if advanced == nil {
		advanced = raw
	}

	if v := firstRaw(raw, "custom_outbounds", "customOutbounds"); len(v) == 0 {
		if v = firstRaw(advanced, "custom_outbounds", "customOutbounds"); len(v) > 0 {
			if err := unmarshalJSONOrString(v, &nc.CustomOutbounds); err != nil {
				return fmt.Errorf("custom_outbounds: %w", err)
			}
		}
	} else if err := unmarshalJSONOrString(v, &nc.CustomOutbounds); err != nil {
		return fmt.Errorf("custom_outbounds: %w", err)
	}

	if v := firstRaw(raw, "custom_routes", "customRoutes"); len(v) == 0 {
		if v = firstRaw(advanced, "custom_routes", "customRoutes"); len(v) > 0 {
			if err := unmarshalJSONOrString(v, &nc.CustomRoutes); err != nil {
				return fmt.Errorf("custom_routes: %w", err)
			}
		}
	} else if err := unmarshalJSONOrString(v, &nc.CustomRoutes); err != nil {
		return fmt.Errorf("custom_routes: %w", err)
	}

	if v := firstRaw(raw, "custom_route_rules", "customRouteRules"); len(v) == 0 {
		if v = firstRaw(advanced, "custom_route_rules", "customRouteRules"); len(v) > 0 {
			if err := unmarshalJSONOrString(v, &nc.CustomRouteRules); err != nil {
				return fmt.Errorf("custom_route_rules: %w", err)
			}
		}
	} else if err := unmarshalJSONOrString(v, &nc.CustomRouteRules); err != nil {
		return fmt.Errorf("custom_route_rules: %w", err)
	}

	return nil
}

// WSConfig holds WebSocket connection settings from the panel
type WSConfig struct {
	Enabled bool   `json:"enabled"`
	WSURL   string `json:"ws_url,omitempty"`
}

// Settings holds panel-defined intervals
type Settings struct {
	PushInterval int `json:"push_interval"`
	PullInterval int `json:"pull_interval"`
}

// MachineNode is a single entry returned by GET /api/v2/server/machine/nodes.
type MachineNode struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// MachineNodesResponse is the response from GET /api/v2/server/machine/nodes.
type MachineNodesResponse struct {
	Nodes      []MachineNode     `json:"nodes"`
	BaseConfig MachineBaseConfig `json:"base_config"`
}

// MachineBaseConfig holds polling intervals for machine mode.
type MachineBaseConfig struct {
	PushInterval int `json:"push_interval"`
	PullInterval int `json:"pull_interval"`
}

// NodeConfig is the response from GET /api/v1/server/UniProxy/config
type NodeConfig struct {
	// NodeID is populated in machine-mode WS events for routing.
	NodeID          int                    `json:"node_id,omitempty"`
	Protocol        string                 `json:"protocol"`
	ListenIP        string                 `json:"listen_ip"`
	ServerPort      int                    `json:"server_port"`
	Network         string                 `json:"network"`
	NetworkSettings map[string]interface{} `json:"networkSettings"`
	BaseConfig      BaseConfig             `json:"base_config"`
	Routes          []RouteRule            `json:"routes"`

	// Kernel settings (Xboard extension)
	KernelType         string            `json:"kernel_type,omitempty"`      // "singbox" or "xray"
	KernelLogLevel     string            `json:"kernel_log_level,omitempty"` // "info", "warn", etc.
	CustomOutbounds    []OutboundConfig  `json:"custom_outbounds,omitempty"`
	CustomRoutes       []map[string]any  `json:"custom_routes,omitempty"`
	CustomRouteRules   []CustomRouteRule `json:"custom_route_rules,omitempty"`
	DefaultOutboundTag string            `json:"default_outbound_tag,omitempty"`

	// Certificate settings (Xboard extension)
	CertConfig *CertConfig `json:"cert_config,omitempty"`
	AutoTLS    bool        `json:"auto_tls,omitempty"` // Deprecated: use CertConfig
	Domain     string      `json:"domain,omitempty"`   // Deprecated: use CertConfig

	// Shadowsocks
	Cipher    string `json:"cipher,omitempty"`
	Plugin    string `json:"plugin,omitempty"`
	PluginOpt string `json:"plugin_opts,omitempty"`
	ServerKey string `json:"server_key,omitempty"`

	// VMess / VLESS
	TLS         int                    `json:"tls,omitempty"`
	Flow        string                 `json:"flow,omitempty"`
	Decryption  string                 `json:"decryption,omitempty"`
	TLSSettings map[string]interface{} `json:"tls_settings,omitempty"`

	// Trojan
	Host       string `json:"host,omitempty"`
	ServerName string `json:"server_name,omitempty"`

	// Hysteria
	Version      int    `json:"version,omitempty"`
	UpMbps       int    `json:"up_mbps,omitempty"`
	DownMbps     int    `json:"down_mbps,omitempty"`
	Obfs         string `json:"obfs,omitempty"`
	ObfsPassword string `json:"obfs-password,omitempty"`

	// TUIC
	CongestionControl string `json:"congestion_control,omitempty"`

	// AnyTLS
	PaddingScheme StringOrArray `json:"padding_scheme,omitempty"`

	// Mieru
	Transport      string `json:"transport,omitempty"`
	TrafficPattern string `json:"traffic_pattern,omitempty"`

	// Multiplex
	Multiplex *MultiplexConfig `json:"multiplex,omitempty"`

	// Proxy Protocol (supports both top-level and networkSettings for compatibility)
	AcceptProxyProtocol bool `json:"accept_proxy_protocol,omitempty"`
}

func (nc *NodeConfig) UnmarshalJSON(data []byte) error {
	type Alias NodeConfig
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*nc = NodeConfig(alias)
	return mergeAdvancedSettings(raw, nc)
}

// GetProxyProtocol returns true if AcceptProxyProtocol is set either at node level
// or in networkSettings (for panel compatibility).
func (nc *NodeConfig) GetProxyProtocol() bool {
	if nc.AcceptProxyProtocol {
		return true
	}
	if nc.NetworkSettings != nil {
		if v, ok := nc.NetworkSettings["acceptProxyProtocol"]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

type MultiplexConfig struct {
	Enabled        bool          `json:"enabled"`
	Protocol       string        `json:"protocol"`
	MaxConnections int           `json:"max_connections"`
	MinStreams     int           `json:"min_streams"`
	MaxStreams     int           `json:"max_streams"`
	Padding        bool          `json:"padding"`
	Brutal         *BrutalConfig `json:"brutal,omitempty"`
}

type BrutalConfig struct {
	Enabled  bool `json:"enabled"`
	UpMbps   int  `json:"up_mbps"`
	DownMbps int  `json:"down_mbps"`
}

// CertConfig holds certificate automation settings from the panel.
// The panel may send the mode field as either "cert_mode" or "mode";
// a custom UnmarshalJSON handles both.
type CertConfig struct {
	CertMode    string            `json:"cert_mode"`    // none, dns, http, self, file, content
	Domain      string            `json:"domain"`       // Certificate domain
	Email       string            `json:"email"`        // ACME email
	DNSProvider string            `json:"dns_provider"` // dns mode: cloudflare, alidns, etc.
	DNSEnv      map[string]string `json:"dns_env"`      // Provider-specific API keys/tokens
	HTTPPort    int               `json:"http_port"`    // ACME HTTP-01 local port (default 80)
	CertFile    string            `json:"cert_file"`    // file mode: path to cert file
	KeyFile     string            `json:"key_file"`     // file mode: path to key file
	CertContent string            `json:"cert_content"` // content mode: certificate raw string
	KeyContent  string            `json:"key_content"`  // content mode: private key raw string
}

func (c *CertConfig) UnmarshalJSON(data []byte) error {
	type plain CertConfig
	var raw struct {
		plain
		ModeFallback string `json:"mode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = CertConfig(raw.plain)
	if c.CertMode == "" && raw.ModeFallback != "" {
		c.CertMode = raw.ModeFallback
	}
	return nil
}

// OutboundConfig defines a custom outbound for kernel
type OutboundConfig struct {
	Tag      string         `json:"tag"`                 // Unique tag for routing
	Protocol string         `json:"protocol"`            // vmess, vless, shadowsocks, wireguard, etc.
	Settings map[string]any `json:"settings,omitempty"`  // Protocol-specific settings
	ProxyTag string         `json:"proxy_tag,omitempty"` // Chain proxy: next outbound tag
}

func (o *OutboundConfig) UnmarshalJSON(data []byte) error {
	type Alias OutboundConfig
	var raw struct {
		Alias
		ProxyTagCamel string `json:"proxyTag"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = OutboundConfig(raw.Alias)
	if o.ProxyTag == "" {
		o.ProxyTag = raw.ProxyTagCamel
	}
	return nil
}

type BaseConfig struct {
	PushInterval int `json:"push_interval"`
	PullInterval int `json:"pull_interval"`
}

type RouteRule struct {
	ID          int      `json:"id"`
	Match       []string `json:"match"`
	Action      string   `json:"action"`
	ActionValue string   `json:"action_value,omitempty"`
}

type CustomRouteRule struct {
	Name     string      `json:"name,omitempty"`
	Disabled bool        `json:"disabled,omitempty"`
	Match    RouteMatch  `json:"match,omitempty"`
	Action   RouteAction `json:"action"`
}

type RouteMatch struct {
	Domains        []string `json:"domains,omitempty"`
	DomainSuffixes []string `json:"domain_suffixes,omitempty"`
	IPCIDRs        []string `json:"ip_cidrs,omitempty"`
	Ports          []string `json:"ports,omitempty"`
	Networks       []string `json:"networks,omitempty"`
	SourceCIDRs    []string `json:"source_cidrs,omitempty"`
	SourcePorts    []string `json:"source_ports,omitempty"`
}

func (m *RouteMatch) UnmarshalJSON(data []byte) error {
	type Alias RouteMatch
	var raw struct {
		Alias
		DomainSuffixesCamel []string `json:"domainSuffixes"`
		IPCIDRsCamel        []string `json:"ipCidrs"`
		SourceCIDRsCamel    []string `json:"sourceCidrs"`
		SourcePortsCamel    []string `json:"sourcePorts"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = RouteMatch(raw.Alias)
	if len(m.DomainSuffixes) == 0 {
		m.DomainSuffixes = raw.DomainSuffixesCamel
	}
	if len(m.IPCIDRs) == 0 {
		m.IPCIDRs = raw.IPCIDRsCamel
	}
	if len(m.SourceCIDRs) == 0 {
		m.SourceCIDRs = raw.SourceCIDRsCamel
	}
	if len(m.SourcePorts) == 0 {
		m.SourcePorts = raw.SourcePortsCamel
	}
	return nil
}

type RouteAction struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
}

// User represents a user returned by the panel
type User struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`  // Mbps, 0 = unlimited
	DeviceLimit int    `json:"device_limit"` // max devices, 0 = unlimited
}

type UsersResponse struct {
	Users []User `json:"users"`
}
