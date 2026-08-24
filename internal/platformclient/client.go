package platformclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClaraCora/CPanelde/internal/agentv2"
)

type APIError struct {
	Status  int
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("platform request failed with status %d", e.Status)
	}
	return fmt.Sprintf("platform request failed: %s: %s", e.Code, e.Message)
}

type Client struct {
	baseURL    string
	token      string
	machineID  string
	httpClient *http.Client
	v2         *V2Credentials
	sessionMu  sync.Mutex
	session    v2Session
	success    atomic.Uint64
	failure    atomic.Uint64
}

type v2Session interface {
	Seal(method, path string, plaintext []byte) ([]byte, error)
	Open(method, path string, envelope []byte) ([]byte, error)
}

func New(baseURL, token string, machineID ...string) *Client {
	configuredMachineID := ""
	if len(machineID) > 0 {
		configuredMachineID = strings.TrimSpace(machineID[0])
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		machineID: configuredMachineID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (c *Client) EnableV2(panelPublicKey, identityPath string) error {
	if err := validateV2Endpoint(c.baseURL, false); err != nil {
		return err
	}
	credentials, err := loadV2Credentials(c.machineID, c.token, panelPublicKey, identityPath)
	if err != nil {
		return err
	}
	c.v2 = credentials
	return nil
}

func (c *Client) V2Credentials() *V2Credentials { return c.v2 }

func (c *Client) ProtocolVersion() string {
	if c.v2 != nil {
		return agentv2.ProtocolVersion
	}
	return ProtocolVersion
}

func (c *Client) Handshake(ctx context.Context) (Handshake, error) {
	if c.v2 != nil {
		var wire v2Handshake
		if err := c.requestV2(ctx, http.MethodPost, "/ca/cc/ws", nil, &wire); err != nil {
			return Handshake{}, err
		}
		result := wire.public()
		if result.ProtocolVersion != agentv2.ProtocolVersion {
			return Handshake{}, fmt.Errorf("unsupported platform protocol version %q", result.ProtocolVersion)
		}
		return result, nil
	}
	var result Handshake
	err := c.request(ctx, http.MethodPost, "/ca/cc/ws", map[string]string{"protocol_version": ProtocolVersion}, nil, &result)
	if err == nil && result.ProtocolVersion != ProtocolVersion {
		return Handshake{}, fmt.Errorf("unsupported platform protocol version %q", result.ProtocolVersion)
	}
	return result, err
}

func (c *Client) Nodes(ctx context.Context) (NodesResponse, error) {
	if c.v2 != nil {
		var wire v2NodesResponse
		err := c.requestV2(ctx, http.MethodPost, "/ca/cc/fwq/jd", nil, &wire)
		return wire.public(), err
	}
	var result NodesResponse
	err := c.request(ctx, http.MethodGet, "/ca/cc/fwq/jd", nil, nil, &result)
	return result, err
}

func (c *Client) NodeSpec(ctx context.Context, nodeID int) (NodeSpec, error) {
	if c.v2 != nil {
		var wire v2NodeSpec
		err := c.requestV2(ctx, http.MethodPost, "/ca/cc/jd/"+strconv.Itoa(nodeID)+"/pz", nil, &wire)
		return wire.public(), err
	}
	var result NodeSpec
	err := c.request(ctx, http.MethodGet, "/ca/cc/jd/"+strconv.Itoa(nodeID)+"/pz", nil, nil, &result)
	return result, err
}

func (c *Client) NodeUsers(ctx context.Context, nodeID int) (UsersResponse, error) {
	if c.v2 != nil {
		var wire v2UsersResponse
		err := c.requestV2(ctx, http.MethodPost, "/ca/cc/jd/"+strconv.Itoa(nodeID)+"/yh", nil, &wire)
		return wire.public(), err
	}
	var result UsersResponse
	err := c.request(ctx, http.MethodGet, "/ca/cc/jd/"+strconv.Itoa(nodeID)+"/yh", nil, nil, &result)
	return result, err
}

func (c *Client) Changes(ctx context.Context, cursor string) (ChangesResponse, error) {
	if c.v2 != nil {
		var wire v2ChangesResponse
		err := c.requestV2(ctx, http.MethodPost, "/ca/cc/bg", map[string]string{"yb": cursor}, &wire)
		return wire.public(), err
	}
	path := "/ca/cc/bg"
	if cursor != "" {
		path += "?" + url.Values{"yb": []string{cursor}}.Encode()
	}
	var result ChangesResponse
	err := c.request(ctx, http.MethodGet, path, nil, nil, &result)
	return result, err
}

func (c *Client) SendHeartbeat(ctx context.Context, heartbeat Heartbeat) (HeartbeatResponse, error) {
	if c.v2 != nil {
		var wire v2HeartbeatResponse
		payload := map[string]any{"bb": heartbeat.Version, "nh": heartbeat.Kernel, "nl": heartbeat.Capabilities, "zb": heartbeat.Metrics}
		err := c.requestV2(ctx, http.MethodPost, "/ca/cc/fwq/xt", payload, &wire)
		return wire.public(), err
	}
	var result HeartbeatResponse
	err := c.request(ctx, http.MethodPost, "/ca/cc/fwq/xt", heartbeat, nil, &result)
	return result, err
}

// SendUpgradeResult reports the lifecycle state of an Agent upgrade task.
// The result is intentionally separate from heartbeat telemetry so an upgrade
// can be acknowledged before the current process schedules its own restart.
func (c *Client) SendUpgradeResult(ctx context.Context, taskID, status, version, message string) error {
	if c.v2 != nil {
		return c.requestV2(ctx, http.MethodPost, "/ca/cc/fwq/jg", map[string]string{
			"bh": taskID,
			"zt": status,
			"bb": version,
			"cw": message,
		}, nil)
	}
	return c.request(ctx, http.MethodPost, "/ca/cc/fwq/jg", map[string]string{
		"task_id": taskID,
		"status":  status,
		"version": version,
		"error":   message,
	}, nil, nil)
}

func (c *Client) SendTelemetry(ctx context.Context, key string, batch TelemetryBatch) error {
	if key == "" {
		key = NewIdempotencyKey("tel")
	}
	if c.v2 != nil {
		events := make([]map[string]any, 0, len(batch.Events))
		for _, event := range batch.Events {
			events = append(events, map[string]any{"lx": event.Type, "jdbh": event.NodeID, "fssj": event.OccurredAt, "sj": event.Data})
		}
		return c.requestV2(ctx, http.MethodPost, "/ca/cc/yc", map[string]any{"mdj": key, "sj": events}, nil)
	}
	return c.request(ctx, http.MethodPost, "/ca/cc/yc", batch, map[string]string{"Idempotency-Key": key}, nil)
}

func (c *Client) Metrics() (success, failure uint64) {
	return c.success.Load(), c.failure.Load()
}

func (c *Client) Token() string { return c.token }

func NewIdempotencyKey(prefix string) string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(random)
}

func (c *Client) request(ctx context.Context, method, path string, payload any, headers map[string]string, target any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode platform request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Corade-Protocol-Version", ProtocolVersion)
	if c.machineID != "" {
		req.Header.Set("X-CPanel-Machine-ID", c.machineID)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.failure.Add(1)
		return err
	}
	defer drainAndClose(resp.Body)

	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *APIError       `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(&envelope); err != nil {
		c.failure.Add(1)
		return fmt.Errorf("decode platform response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Error != nil {
		c.failure.Add(1)
		if envelope.Error == nil {
			envelope.Error = &APIError{}
		}
		envelope.Error.Status = resp.StatusCode
		return envelope.Error
	}
	if target != nil {
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			c.failure.Add(1)
			return fmt.Errorf("decode platform data: %w", err)
		}
	}
	c.success.Add(1)
	return nil
}

func (c *Client) requestV2(ctx context.Context, method, path string, payload, target any) error {
	plain, err := encodeV2Payload(payload)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		session, err := c.ensureV2Session(ctx)
		if err != nil {
			c.failure.Add(1)
			return err
		}
		envelope, err := session.Seal(method, path, plain)
		if err != nil {
			c.failure.Add(1)
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(envelope))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", agentv2.ContentType)
		req.Header.Set("Accept", agentv2.ContentType)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.failure.Add(1)
			return err
		}
		body, readErr := readLimitedAndClose(resp.Body, 2<<20)
		if readErr != nil {
			c.failure.Add(1)
			return readErr
		}
		if resp.StatusCode == http.StatusPreconditionFailed && attempt == 0 {
			c.invalidateV2Session(session)
			continue
		}
		decrypted, openErr := session.Open(method, path, body)
		if openErr != nil {
			c.failure.Add(1)
			return &APIError{Status: resp.StatusCode, Message: "encrypted platform response was rejected"}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			c.failure.Add(1)
			return decodeV2APIError(resp.StatusCode, decrypted)
		}
		if target != nil && len(decrypted) > 0 {
			if err := json.Unmarshal(decrypted, target); err != nil {
				c.failure.Add(1)
				return fmt.Errorf("decode encrypted platform response: %w", err)
			}
		}
		c.success.Add(1)
		return nil
	}
	c.failure.Add(1)
	return errors.New("encrypted platform session could not be renewed")
}

func encodeV2Payload(payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode encrypted platform request: %w", err)
	}
	return encoded, nil
}

func decodeV2APIError(status int, payload []byte) error {
	var envelope struct {
		Error struct {
			Code string `json:"dm"`
		} `json:"cw"`
	}
	_ = json.Unmarshal(payload, &envelope)
	return &APIError{Status: status, Code: envelope.Error.Code}
}

func (c *Client) ensureV2Session(ctx context.Context) (v2Session, error) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.session != nil {
		return c.session, nil
	}
	session, err := establishV2Session(ctx, c.httpClient, c.baseURL+"/ca/cc/ws", c.v2, false)
	if err != nil {
		return nil, err
	}
	c.session = session
	return session, nil
}

func (c *Client) invalidateV2Session(session v2Session) {
	c.sessionMu.Lock()
	if c.session == session {
		c.session = nil
	}
	c.sessionMu.Unlock()
}

func establishV2Session(ctx context.Context, httpClient *http.Client, endpoint string, credentials *V2Credentials, forceEnrollment bool) (*agentv2.Session, error) {
	if credentials == nil {
		return nil, errors.New("V2 credentials are unavailable")
	}
	publicKey, privateKey, enrollmentKey, enrolled := credentials.handshakeIdentity()
	if forceEnrollment {
		enrolled = false
	}
	handshake, err := agentv2.NewClientHandshake(credentials.machineID, publicKey, privateKey, enrollmentKey, enrolled, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	encoded, err := agentv2.EncodeHandshake(handshake.Request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", agentv2.ContentType)
	req.Header.Set("Accept", agentv2.ContentType)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := readLimitedAndClose(resp.Body, 16<<10)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode == http.StatusUnauthorized && enrolled && !forceEnrollment {
		credentials.markNeedsEnrollment()
		return establishV2Session(ctx, httpClient, endpoint, credentials, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode}
	}
	var response agentv2.HandshakeResponse
	if err := agentv2.DecodeHandshake(body, &response); err != nil {
		return nil, fmt.Errorf("decode V2 platform handshake: %w", err)
	}
	session, err := handshake.Complete(response, credentials.panelIdentity)
	if err != nil {
		return nil, err
	}
	if !response.Enrolled {
		return nil, errors.New("platform did not enroll the V2 Agent identity")
	}
	if err := credentials.markEnrolled(); err != nil {
		return nil, err
	}
	return session, nil
}

func readLimitedAndClose(body io.ReadCloser, limit int64) ([]byte, error) {
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("platform response exceeded the size limit")
	}
	return data, nil
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.CopyN(io.Discard, body, 512)
	_ = body.Close()
}
