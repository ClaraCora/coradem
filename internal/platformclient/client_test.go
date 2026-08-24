package platformclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestClientUsesCACCRoutesAndBearerAuthentication(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 7)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-secret" {
			t.Errorf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Corade-Protocol-Version") != ProtocolVersion {
			t.Errorf("unexpected protocol header %q", r.Header.Get("X-Corade-Protocol-Version"))
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()

		var data any = map[string]any{"accepted": true}
		switch r.URL.Path {
		case "/ca/cc/ws":
			data = map[string]any{"protocol_version": ProtocolVersion, "machine": map[string]any{"id": "mch_test"}}
		case "/ca/cc/fwq/jd":
			data = map[string]any{"nodes": []any{map[string]any{"id": 12, "type": "vless", "name": "edge"}}}
		case "/ca/cc/jd/12/pz":
			data = map[string]any{"node_id": 12, "revision": 3, "protocol": "vless", "listen_ip": "0.0.0.0", "server_port": 443, "kernel_type": "singbox", "settings": map[string]any{"transport": "tcp"}}
		case "/ca/cc/jd/12/yh":
			data = map[string]any{"users": []any{map[string]any{"id": 9, "uuid": "user-uuid"}}}
		case "/ca/cc/bg":
			data = map[string]any{"changes": []any{}, "next_cursor": "8"}
		case "/ca/cc/fwq/xt":
			data = map[string]any{"accepted": true, "commands": []any{map[string]any{"id": "upg_test", "type": "agent.upgrade", "target_version": "v2.0.0"}}}
		case "/ca/cc/fwq/jg":
			data = map[string]any{"accepted": true}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": map[string]any{"request_id": "req_test"}, "error": nil})
	}))
	defer server.Close()

	client := New(server.URL, "agent-secret")
	ctx := context.Background()
	if _, err := client.Handshake(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Nodes(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NodeSpec(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NodeUsers(ctx, 12); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Changes(ctx, "7"); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := client.SendHeartbeat(ctx, Heartbeat{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !heartbeat.Accepted || len(heartbeat.Commands) != 1 || heartbeat.Commands[0].Type != "agent.upgrade" || heartbeat.Commands[0].TargetVersion != "v2.0.0" {
		t.Fatalf("unexpected heartbeat response: %+v", heartbeat)
	}
	if err := client.SendUpgradeResult(ctx, "upg_test", "acknowledged", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTelemetry(ctx, "batch-1", TelemetryBatch{}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"POST /ca/cc/ws", "GET /ca/cc/fwq/jd", "GET /ca/cc/jd/12/pz", "GET /ca/cc/jd/12/yh",
		"GET /ca/cc/bg?yb=7", "POST /ca/cc/fwq/xt", "POST /ca/cc/fwq/jg", "POST /ca/cc/yc",
	}
	if len(requests) != len(want) {
		t.Fatalf("got requests %v", requests)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, requests[i], want[i])
		}
	}
}

type passthroughV2Session struct{}

func (passthroughV2Session) Seal(_, _ string, plaintext []byte) ([]byte, error) {
	return append([]byte{2}, plaintext...), nil
}

func (passthroughV2Session) Open(_, _ string, envelope []byte) ([]byte, error) {
	if len(envelope) == 0 || envelope[0] != 2 {
		return nil, errors.New("invalid test envelope")
	}
	return envelope[1:], nil
}

func TestV2ClientUsesEncryptedPostRoutesWithoutBrandHeaders(t *testing.T) {
	type capturedRequest struct {
		method, path string
		headers      http.Header
		payload      map[string]any
	}
	var mu sync.Mutex
	requests := make([]capturedRequest, 0, 7)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		payload := map[string]any{}
		if len(body) > 1 {
			_ = json.Unmarshal(body[1:], &payload)
		}
		mu.Lock()
		requests = append(requests, capturedRequest{method: r.Method, path: r.URL.RequestURI(), headers: r.Header.Clone(), payload: payload})
		mu.Unlock()

		var response any = map[string]any{"js": true}
		switch r.URL.Path {
		case "/ca/cc/ws":
			response = map[string]any{"xybb": "2", "fwq": map[string]any{"bh": "mch_test", "mc": "test"}, "td": map[string]any{"qy": true, "dz": "wss://example.com/ca/cc/td"}, "jg": map[string]any{"xtjg": 60, "ycjg": 60, "ldjg": 60}, "yb": "6"}
		case "/ca/cc/fwq/jd":
			response = map[string]any{"jd": []any{map[string]any{"bh": 12, "lx": "vless", "mc": "edge"}}, "jcpz": map[string]any{"tsjg": 60, "lqjg": 60}}
		case "/ca/cc/jd/12/pz":
			response = map[string]any{"jdbh": 12, "bb": 3, "xy": "vless", "jtdz": "0.0.0.0", "fwqdk": 443, "nh": "xray", "sz": map[string]any{"transport": "tcp"}}
		case "/ca/cc/jd/12/yh":
			response = map[string]any{"yh": []any{map[string]any{"bh": 9, "wybs": "user-uuid", "xs": 20, "sbs": 2}}}
		case "/ca/cc/bg":
			response = map[string]any{"bg": []any{}, "xyb": "8"}
		case "/ca/cc/fwq/xt":
			response = map[string]any{"js": true, "rw": []any{map[string]any{"bh": "upg_test", "lx": "agent.upgrade", "bb": "v2.0.0"}}}
		case "/ca/cc/fwq/jg":
			response = map[string]any{"js": true}
		}
		encoded, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(append([]byte{2}, encoded...))
	}))
	defer server.Close()

	client := New(server.URL, "0123456789abcdef0123456789abcdef", "mch_test")
	client.v2 = &V2Credentials{}
	client.session = passthroughV2Session{}
	ctx := context.Background()
	handshake, err := client.Handshake(ctx)
	if err != nil || handshake.Machine.ID != "mch_test" || handshake.Cursor != "6" {
		t.Fatalf("handshake = %+v, %v", handshake, err)
	}
	nodes, err := client.Nodes(ctx)
	if err != nil || len(nodes.Nodes) != 1 || nodes.Nodes[0].ID != 12 {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
	spec, err := client.NodeSpec(ctx, 12)
	if err != nil || spec.KernelType != "xray" || spec.ServerPort != 443 {
		t.Fatalf("spec = %+v, %v", spec, err)
	}
	users, err := client.NodeUsers(ctx, 12)
	if err != nil || len(users.Users) != 1 || users.Users[0].UUID != "user-uuid" {
		t.Fatalf("users = %+v, %v", users, err)
	}
	changes, err := client.Changes(ctx, "7")
	if err != nil || changes.NextCursor != "8" {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	heartbeat, err := client.SendHeartbeat(ctx, Heartbeat{Version: "test", Kernel: "xray", Capabilities: map[string]any{"encrypted": true}, Metrics: map[string]any{"cpu": 1}})
	if err != nil || !heartbeat.Accepted || len(heartbeat.Commands) != 1 || heartbeat.Commands[0].TargetVersion != "v2.0.0" {
		t.Fatalf("heartbeat = %+v, %v", heartbeat, err)
	}
	if err := client.SendUpgradeResult(ctx, "upg_test", "acknowledged", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTelemetry(ctx, "batch-1", TelemetryBatch{Events: []TelemetryEvent{{Type: "node.telemetry", NodeID: 12, OccurredAt: "now", Data: map[string]any{"online": 1}}}}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 8 {
		t.Fatalf("requests = %d, want 7", len(requests))
	}
	for _, request := range requests {
		if request.method != http.MethodPost {
			t.Errorf("%s used %s", request.path, request.method)
		}
		if request.headers.Get("Content-Type") != "application/octet-stream" || request.headers.Get("Accept") != "application/octet-stream" {
			t.Errorf("%s has wrong content headers: %v", request.path, request.headers)
		}
		for _, header := range []string{"Authorization", "X-Corade-Protocol-Version", "X-CPanel-Machine-ID", "Idempotency-Key"} {
			if request.headers.Get(header) != "" {
				t.Errorf("%s leaked header %s", request.path, header)
			}
		}
	}
	if requests[4].path != "/ca/cc/bg" || requests[4].payload["yb"] != "7" {
		t.Errorf("cursor was not moved into encrypted payload: %+v", requests[4])
	}
	if requests[5].payload["bb"] != "test" || requests[5].payload["nh"] != "xray" {
		t.Errorf("heartbeat did not use V2 fields: %+v", requests[5].payload)
	}
	if requests[6].path != "/ca/cc/fwq/jg" || requests[6].payload["bh"] != "upg_test" || requests[6].payload["zt"] != "acknowledged" {
		t.Errorf("upgrade result did not use V2 fields: %+v", requests[6].payload)
	}
	if requests[7].payload["mdj"] != "batch-1" {
		t.Errorf("idempotency key did not use encrypted payload: %+v", requests[7].payload)
	}
}

func TestClientReturnsStructuredPlatformError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "error": map[string]any{"code": "AGENT_TOKEN_INVALID", "message": "invalid"}})
	}))
	defer server.Close()

	_, err := New(server.URL, "bad").Nodes(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusUnauthorized || apiErr.Code != "AGENT_TOKEN_INVALID" {
		t.Fatalf("unexpected error %#v", err)
	}
}
