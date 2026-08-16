package platformclient

import "encoding/json"

type v2Handshake struct {
	ProtocolVersion string `json:"xybb"`
	Machine         struct {
		ID   string `json:"bh"`
		Name string `json:"mc"`
	} `json:"fwq"`
	Stream struct {
		Enabled bool   `json:"qy"`
		URL     string `json:"dz"`
	} `json:"td"`
	Intervals struct {
		HeartbeatSeconds    int `json:"xtjg"`
		TelemetrySeconds    int `json:"ycjg"`
		FallbackPullSeconds int `json:"ldjg"`
	} `json:"jg"`
	Cursor string `json:"yb"`
}

func (wire v2Handshake) public() Handshake {
	var result Handshake
	result.ProtocolVersion = wire.ProtocolVersion
	result.Machine.ID, result.Machine.Name = wire.Machine.ID, wire.Machine.Name
	result.Stream.Enabled, result.Stream.URL = wire.Stream.Enabled, wire.Stream.URL
	result.Intervals.HeartbeatSeconds = wire.Intervals.HeartbeatSeconds
	result.Intervals.TelemetrySeconds = wire.Intervals.TelemetrySeconds
	result.Intervals.FallbackPullSeconds = wire.Intervals.FallbackPullSeconds
	result.Cursor = wire.Cursor
	return result
}

type v2NodesResponse struct {
	Nodes []struct {
		ID   int    `json:"bh"`
		Type string `json:"lx"`
		Name string `json:"mc"`
	} `json:"jd"`
	BaseConfig struct {
		PushInterval int `json:"tsjg"`
		PullInterval int `json:"lqjg"`
	} `json:"jcpz"`
}

func (wire v2NodesResponse) public() NodesResponse {
	result := NodesResponse{Nodes: make([]Node, 0, len(wire.Nodes))}
	for _, node := range wire.Nodes {
		result.Nodes = append(result.Nodes, Node{ID: node.ID, Type: node.Type, Name: node.Name})
	}
	result.BaseConfig.PushInterval = wire.BaseConfig.PushInterval
	result.BaseConfig.PullInterval = wire.BaseConfig.PullInterval
	return result
}

type v2NodeSpec struct {
	NodeID     int             `json:"jdbh"`
	Revision   int             `json:"bb"`
	Protocol   string          `json:"xy"`
	ListenIP   string          `json:"jtdz"`
	ServerPort int             `json:"fwqdk"`
	KernelType string          `json:"nh"`
	Settings   json.RawMessage `json:"sz"`
	BaseConfig struct {
		PushInterval int `json:"tsjg"`
		PullInterval int `json:"lqjg"`
	} `json:"jcpz"`
}

func (wire v2NodeSpec) public() NodeSpec {
	result := NodeSpec{
		NodeID: wire.NodeID, Revision: wire.Revision, Protocol: wire.Protocol, ListenIP: wire.ListenIP,
		ServerPort: wire.ServerPort, KernelType: wire.KernelType, Settings: wire.Settings,
	}
	result.BaseConfig.PushInterval = wire.BaseConfig.PushInterval
	result.BaseConfig.PullInterval = wire.BaseConfig.PullInterval
	return result
}

type v2UsersResponse struct {
	Users []struct {
		ID          int    `json:"bh"`
		UUID        string `json:"wybs"`
		SpeedLimit  int    `json:"xs"`
		DeviceLimit int    `json:"sbs"`
		RouteScope  string `json:"lyfw"`
	} `json:"yh"`
}

func (wire v2UsersResponse) public() UsersResponse {
	result := UsersResponse{Users: make([]User, 0, len(wire.Users))}
	for _, user := range wire.Users {
		result.Users = append(result.Users, User{ID: user.ID, UUID: user.UUID, SpeedLimit: user.SpeedLimit, DeviceLimit: user.DeviceLimit, RouteScope: user.RouteScope})
	}
	return result
}

type v2ChangesResponse struct {
	Changes []struct {
		Cursor     int64           `json:"yb"`
		NodeID     *int            `json:"jdbh,omitempty"`
		Type       string          `json:"lx"`
		Revision   int             `json:"bb"`
		Data       json.RawMessage `json:"sj"`
		OccurredAt string          `json:"fssj"`
	} `json:"bg"`
	NextCursor string `json:"xyb"`
}

func (wire v2ChangesResponse) public() ChangesResponse {
	result := ChangesResponse{Changes: make([]Change, 0, len(wire.Changes)), NextCursor: wire.NextCursor}
	for _, change := range wire.Changes {
		result.Changes = append(result.Changes, Change{
			Cursor: change.Cursor, NodeID: change.NodeID, Type: change.Type, Revision: change.Revision,
			Data: change.Data, OccurredAt: change.OccurredAt,
		})
	}
	return result
}

type v2HeartbeatResponse struct {
	Accepted bool `json:"js"`
	Commands []struct {
		ID   string `json:"bh"`
		Type string `json:"lx"`
	} `json:"rw"`
}

func (wire v2HeartbeatResponse) public() HeartbeatResponse {
	result := HeartbeatResponse{Accepted: wire.Accepted, Commands: make([]AgentCommand, 0, len(wire.Commands))}
	for _, command := range wire.Commands {
		result.Commands = append(result.Commands, AgentCommand{ID: command.ID, Type: command.Type})
	}
	return result
}

type v2Event struct {
	ID         string          `json:"bh"`
	Type       string          `json:"lx"`
	Revision   int             `json:"bb"`
	OccurredAt string          `json:"fssj"`
	Data       json.RawMessage `json:"sj"`
}

func (wire v2Event) public() Event {
	return Event{ID: wire.ID, Type: wire.Type, Revision: wire.Revision, OccurredAt: wire.OccurredAt, Data: wire.Data}
}
