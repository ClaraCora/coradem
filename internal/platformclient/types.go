package platformclient

import (
	"encoding/json"
	"fmt"

	"github.com/ClaraCora/CPanelde/internal/config"
	"github.com/ClaraCora/CPanelde/internal/model"
	"github.com/ClaraCora/CPanelde/internal/panel"
)

const ProtocolVersion = "1.0"

type Handshake struct {
	ProtocolVersion string `json:"protocol_version"`
	Machine         struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"machine"`
	Stream struct {
		Enabled bool   `json:"enabled"`
		URL     string `json:"url"`
	} `json:"stream"`
	Intervals struct {
		HeartbeatSeconds    int `json:"heartbeat_seconds"`
		TelemetrySeconds    int `json:"telemetry_seconds"`
		FallbackPullSeconds int `json:"fallback_pull_seconds"`
	} `json:"intervals"`
	Cursor   string `json:"cursor"`
	Security struct {
		PanelPublicKey string `json:"mbgy"`
	} `json:"aq,omitempty"`
}

type Node struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

type NodesResponse struct {
	Nodes      []Node `json:"nodes"`
	BaseConfig struct {
		PushInterval int `json:"push_interval"`
		PullInterval int `json:"pull_interval"`
	} `json:"base_config"`
}

type NodeSpec struct {
	NodeID     int             `json:"node_id"`
	Revision   int             `json:"revision"`
	Protocol   string          `json:"protocol"`
	ListenIP   string          `json:"listen_ip"`
	ServerPort int             `json:"server_port"`
	KernelType string          `json:"kernel_type"`
	Settings   json.RawMessage `json:"settings"`
	BaseConfig struct {
		PushInterval int `json:"push_interval"`
		PullInterval int `json:"pull_interval"`
	} `json:"base_config"`
}

func (s NodeSpec) Transport() string {
	var settings map[string]any
	if err := json.Unmarshal(s.Settings, &settings); err != nil {
		return ""
	}
	if value, ok := settings["transport"].(string); ok {
		return value
	}
	value, _ := settings["network"].(string)
	return value
}

func (s NodeSpec) Model(kcfg config.KernelConfig) (*model.NodeSpec, error) {
	settings := make(map[string]any)
	if len(s.Settings) > 0 && string(s.Settings) != "null" {
		if err := json.Unmarshal(s.Settings, &settings); err != nil {
			return nil, fmt.Errorf("decode node settings: %w", err)
		}
	}

	if transport, ok := settings["transport"].(string); ok && settings["network"] == nil {
		settings["network"] = transport
	}
	if networkSettings, ok := settings["network_settings"]; ok && settings["networkSettings"] == nil {
		settings["networkSettings"] = networkSettings
	}
	if obfsPassword, ok := settings["obfs_password"]; ok && settings["obfs-password"] == nil {
		settings["obfs-password"] = obfsPassword
	}
	if tls, ok := settings["tls"].(map[string]any); ok {
		settings["tls"] = boolInt(tls["enabled"])
		if explicit, ok := tls["settings"].(map[string]any); ok {
			settings["tls_settings"] = explicit
		} else {
			tlsSettings := make(map[string]any, len(tls))
			for key, value := range tls {
				if key != "enabled" {
					tlsSettings[key] = value
				}
			}
			if len(tlsSettings) > 0 {
				settings["tls_settings"] = tlsSettings
			}
		}
	}

	settings["node_id"] = s.NodeID
	settings["protocol"] = s.Protocol
	settings["listen_ip"] = s.ListenIP
	settings["server_port"] = s.ServerPort
	settings["kernel_type"] = s.KernelType
	settings["base_config"] = map[string]int{
		"push_interval": s.BaseConfig.PushInterval,
		"pull_interval": s.BaseConfig.PullInterval,
	}

	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encode normalized node settings: %w", err)
	}
	var legacyShape panel.NodeConfig
	if err := json.Unmarshal(raw, &legacyShape); err != nil {
		return nil, fmt.Errorf("decode normalized node settings: %w", err)
	}
	spec, err := model.NodeSpecFromPanelValidated(&legacyShape, kcfg)
	if err != nil {
		return nil, fmt.Errorf("validate node settings: %w", err)
	}
	return spec, nil
}

func boolInt(value any) int {
	switch typed := value.(type) {
	case bool:
		if typed {
			return 1
		}
	case float64:
		return int(typed)
	case int:
		return typed
	}
	return 0
}

type User struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`
	DeviceLimit int    `json:"device_limit"`
	RouteScope  string `json:"route_scope"`
}

type UsersResponse struct {
	Users []User `json:"users"`
}

func (r UsersResponse) Models() []model.UserSpec {
	users := make([]model.UserSpec, 0, len(r.Users))
	for _, user := range r.Users {
		users = append(users, model.UserSpec{ID: user.ID, UUID: user.UUID, SpeedLimit: user.SpeedLimit, DeviceLimit: user.DeviceLimit, RouteScope: user.RouteScope})
	}
	return users
}

type Change struct {
	Cursor     int64           `json:"cursor"`
	NodeID     *int            `json:"node_id,omitempty"`
	Type       string          `json:"type"`
	Revision   int             `json:"revision"`
	Data       json.RawMessage `json:"data"`
	OccurredAt string          `json:"occurred_at"`
}

type ChangesResponse struct {
	Changes    []Change `json:"changes"`
	NextCursor string   `json:"next_cursor"`
}

type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Revision   int             `json:"revision"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

func (e Event) NodeID() int {
	var value struct {
		NodeID int `json:"node_id"`
	}
	_ = json.Unmarshal(e.Data, &value)
	return value.NodeID
}

type Heartbeat struct {
	Version      string         `json:"version"`
	Kernel       string         `json:"kernel"`
	Capabilities map[string]any `json:"capabilities"`
	Metrics      map[string]any `json:"metrics"`
}

type AgentCommand struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type HeartbeatResponse struct {
	Accepted bool           `json:"accepted"`
	Commands []AgentCommand `json:"commands"`
}

type TelemetryBatch struct {
	Events []TelemetryEvent `json:"events"`
}

type TelemetryEvent struct {
	Type       string         `json:"type"`
	NodeID     int            `json:"node_id,omitempty"`
	OccurredAt string         `json:"occurred_at"`
	Data       map[string]any `json:"data"`
}
