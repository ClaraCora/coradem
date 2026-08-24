package deviceplatform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClaraCora/CPanelde/internal/config"
	"github.com/ClaraCora/CPanelde/internal/controlplane"
	"github.com/ClaraCora/CPanelde/internal/model"
	"github.com/ClaraCora/CPanelde/internal/monitor"
	"github.com/ClaraCora/CPanelde/internal/nlog"
	"github.com/ClaraCora/CPanelde/internal/platformclient"
	"github.com/ClaraCora/CPanelde/internal/service"
)

type nodeHandle struct {
	cancel     context.CancelFunc
	done       chan struct{}
	node       platformclient.Node
	kernelType string
}

type Orchestrator struct {
	cfg     *config.Config
	version string
	client  *platformclient.Client
	stream  *platformclient.Stream
	initErr error

	mu       sync.Mutex
	nodes    map[int]*nodeHandle
	statuses map[int]chan<- controlplane.StatusChange
	stopping bool

	runCtx            context.Context
	pushInterval      int
	pullInterval      int
	heartbeatInterval time.Duration
	cursor            atomic.Int64
	scheduleUpgrade   func(context.Context, string, string, string) error
	upgradeStatePath  string
}

func New(cfg *config.Config, version string) *Orchestrator {
	client := platformclient.New(cfg.Control.URL, cfg.Control.Token, cfg.Control.MachineID)
	initErr := platformclient.ValidateControlEndpoint(cfg.Control.URL)
	identityPath := filepath.Join(cfg.Kernel.ConfigDir, "agent-v2-identity.json")
	panelPublicKey := strings.TrimSpace(cfg.Control.PanelPublicKey)
	if initErr == nil && panelPublicKey == "" {
		panelPublicKey, initErr = platformclient.StoredV2PanelPublicKey(identityPath)
	}
	if initErr == nil && panelPublicKey != "" {
		initErr = client.EnableV2(panelPublicKey, identityPath)
	}
	return &Orchestrator{
		cfg: cfg, version: version, client: client, initErr: initErr,
		nodes: make(map[int]*nodeHandle), statuses: make(map[int]chan<- controlplane.StatusChange),
		scheduleUpgrade:  scheduleAgentUpgrade,
		upgradeStatePath: upgradeStatePath(cfg.Kernel.ConfigDir),
	}
}

func (o *Orchestrator) Run(ctx context.Context) error {
	if o.initErr != nil {
		return fmt.Errorf("initialize encrypted device platform client: %w", o.initErr)
	}
	o.runCtx = ctx
	handshake, err := o.client.Handshake(ctx)
	if err != nil {
		return fmt.Errorf("device platform handshake: %w", err)
	}
	if o.client.V2Credentials() == nil && strings.TrimSpace(handshake.Security.PanelPublicKey) != "" {
		identityPath := filepath.Join(o.cfg.Kernel.ConfigDir, "agent-v2-identity.json")
		if err := o.client.EnableV2(handshake.Security.PanelPublicKey, identityPath); err != nil {
			return fmt.Errorf("enable encrypted device platform client: %w", err)
		}
		handshake, err = o.client.Handshake(ctx)
		if err != nil {
			return fmt.Errorf("device platform V2 handshake: %w", err)
		}
	}
	o.applyIntervals(handshake)
	// A previous upgrade may have restarted this process before its result could
	// be sent. Retry that result before normal command processing begins.
	o.reportPendingUpgrade(ctx)
	if err := o.advanceCursor(handshake.Cursor); err != nil {
		return fmt.Errorf("device platform handshake cursor: %w", err)
	}
	if handshake.Stream.Enabled && handshake.Stream.URL != "" {
		o.stream = platformclient.NewStream(
			handshake.Stream.URL, o.cfg.Control.Token,
			time.Duration(o.cfg.WS.BackoffInitial)*time.Second,
			time.Duration(o.cfg.WS.BackoffMax)*time.Second,
			o.onEvent, o.onStreamStatus,
		).SetMachineID(o.cfg.Control.MachineID)
		if credentials := o.client.V2Credentials(); credentials != nil {
			if err := o.stream.EnableV2(credentials); err != nil {
				return fmt.Errorf("initialize encrypted device platform stream: %w", err)
			}
		}
		go o.stream.Run(ctx)
	}

	nodes, err := o.client.Nodes(ctx)
	if err != nil {
		return fmt.Errorf("device platform initial node discovery: %w", err)
	}
	for _, node := range nodes.Nodes {
		o.startNode(ctx, node)
	}
	nlog.Core().Info("device platform connected", "machine_id", handshake.Machine.ID, "machine_name", handshake.Machine.Name, "nodes", len(nodes.Nodes))

	discoveryTicker := time.NewTicker(time.Duration(o.pullInterval) * time.Second)
	heartbeatTicker := time.NewTicker(o.heartbeatInterval)
	defer discoveryTicker.Stop()
	defer heartbeatTicker.Stop()
	defer o.stopAll()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-discoveryTicker.C:
			o.rediscover(ctx)
			o.pullChanges(ctx)
		case <-heartbeatTicker.C:
			o.reportPendingUpgrade(ctx)
			o.sendHeartbeat(ctx)
		}
	}
}

func (o *Orchestrator) startNode(ctx context.Context, node platformclient.Node) {
	if ctx.Err() != nil {
		return
	}
	nodeCfg := o.cfg.ExpandDevicePlatformNode(node.ID, node.Type)
	if spec, err := o.client.NodeSpec(ctx, node.ID); err == nil {
		kernelType := strings.ToLower(strings.TrimSpace(spec.KernelType))
		if kernelType == "" {
			kernelType = nodeCfg.Kernel.Type
		}
		nodeCfg.Kernel.Type = model.ResolveKernelForTransport(spec.Transport(), kernelType)
	} else {
		nlog.Core().Warn("device platform node preflight failed", "node_id", node.ID, "error", err)
	}
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		return
	}
	if _, exists := o.nodes[node.ID]; exists {
		o.mu.Unlock()
		return
	}
	nodeCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	o.nodes[node.ID] = &nodeHandle{cancel: cancel, done: done, node: node, kernelType: nodeCfg.Kernel.Type}
	o.mu.Unlock()

	var push controlplane.PushClient
	if o.stream != nil {
		push = &nodePush{stream: o.stream}
	}
	registerFn := func(statuses chan<- controlplane.StatusChange) *controlplane.NodeMailbox {
		o.mu.Lock()
		o.statuses[node.ID] = statuses
		o.mu.Unlock()
		return nil
	}
	cp := controlplane.NewDevicePlatformControlPlane(
		o.client, node.ID, nodeCfg.Kernel, o.pushInterval, o.pullInterval, push, registerFn,
	)
	svc := service.NewWithControlPlane(nodeCfg, cp)
	nlog.Core().Info("device platform starting node", "node_id", node.ID, "type", node.Type, "name", node.Name, "kernel", nodeCfg.Kernel.Type)

	go func() {
		defer close(done)
		defer func() {
			o.mu.Lock()
			if current, ok := o.nodes[node.ID]; ok && current.done == done {
				delete(o.nodes, node.ID)
				delete(o.statuses, node.ID)
			}
			o.mu.Unlock()
		}()
		if err := svc.Run(nodeCtx); err != nil {
			nlog.Core().Error("device platform node exited", "node_id", node.ID, "error", err)
		}
	}()
}

func (o *Orchestrator) stopNode(nodeID int) {
	o.mu.Lock()
	handle, ok := o.nodes[nodeID]
	if ok {
		delete(o.nodes, nodeID)
		delete(o.statuses, nodeID)
	}
	o.mu.Unlock()
	if !ok {
		return
	}
	handle.cancel()
	<-handle.done
	nlog.Core().Info("device platform stopped node", "node_id", nodeID)
}

func (o *Orchestrator) stopAll() {
	o.mu.Lock()
	o.stopping = true
	handles := make([]*nodeHandle, 0, len(o.nodes))
	for nodeID, handle := range o.nodes {
		handles = append(handles, handle)
		handle.cancel()
		delete(o.nodes, nodeID)
		delete(o.statuses, nodeID)
	}
	o.mu.Unlock()
	for _, handle := range handles {
		<-handle.done
	}
}

func (o *Orchestrator) rediscover(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	response, err := o.client.Nodes(ctx)
	if err != nil {
		nlog.Core().Warn("device platform node discovery failed", "error", err)
		return
	}
	wanted := make(map[int]platformclient.Node, len(response.Nodes))
	for _, node := range response.Nodes {
		wanted[node.ID] = node
	}
	o.mu.Lock()
	remove, start := diffNodes(o.nodes, wanted)
	o.mu.Unlock()
	for _, nodeID := range remove {
		o.stopNode(nodeID)
	}
	for _, node := range start {
		o.startNode(ctx, node)
	}
}

func (o *Orchestrator) pullChanges(ctx context.Context) {
	changes, err := o.client.Changes(ctx, o.currentCursor())
	if err != nil {
		nlog.Core().Warn("device platform change pull failed", "error", err)
		return
	}
	for _, change := range changes.Changes {
		o.handleChange(change.Type, changeNodeID(change.NodeID, change.Data))
	}
	if err := o.advanceCursor(changes.NextCursor); err != nil {
		nlog.Core().Warn("device platform returned invalid change cursor", "cursor", changes.NextCursor, "error", err)
	}
}

func (o *Orchestrator) onEvent(event platformclient.Event) {
	if strings.HasPrefix(event.ID, "chg_") {
		if err := o.advanceCursor(strings.TrimPrefix(event.ID, "chg_")); err != nil {
			nlog.Core().Warn("device platform stream returned invalid cursor", "event_id", event.ID, "error", err)
		}
	}
	o.handleChange(event.Type, event.NodeID())
}

func (o *Orchestrator) currentCursor() string {
	return strconv.FormatInt(o.cursor.Load(), 10)
}

func (o *Orchestrator) advanceCursor(candidate string) error {
	value, err := strconv.ParseInt(strings.TrimSpace(candidate), 10, 64)
	if err != nil || value < 0 {
		return fmt.Errorf("invalid cursor %q", candidate)
	}
	for {
		current := o.cursor.Load()
		if value <= current {
			return nil
		}
		if o.cursor.CompareAndSwap(current, value) {
			return nil
		}
	}
}

func diffNodes(current map[int]*nodeHandle, wanted map[int]platformclient.Node) (remove []int, start []platformclient.Node) {
	for nodeID := range current {
		if _, ok := wanted[nodeID]; !ok {
			remove = append(remove, nodeID)
		}
	}
	for nodeID, node := range wanted {
		if _, ok := current[nodeID]; !ok {
			start = append(start, node)
		}
	}
	return remove, start
}

func (o *Orchestrator) handleChange(eventType string, nodeID int) {
	switch eventType {
	case "machine.nodes.replace":
		go o.rediscover(o.runCtx)
	case "agent.command.sync":
		go o.rediscover(o.runCtx)
		o.broadcast(controlplane.StatusChange{Connected: o.stream != nil && o.stream.IsConnected(), NeedsResync: true})
	case "node.spec.replace":
		o.mu.Lock()
		handle := o.nodes[nodeID]
		o.mu.Unlock()
		if o.client == nil || handle == nil {
			o.notifyNode(nodeID, controlplane.StatusChange{Connected: o.stream != nil && o.stream.IsConnected(), NeedsResync: true})
		} else {
			go o.reconcileNodeSpec(nodeID)
		}
	case "node.members.replace", "node.members.patch", "node.devices.replace":
		o.notifyNode(nodeID, controlplane.StatusChange{Connected: o.stream != nil && o.stream.IsConnected(), NeedsResync: true})
	}
}

func (o *Orchestrator) reconcileNodeSpec(nodeID int) {
	ctx := o.runCtx
	if ctx == nil || ctx.Err() != nil {
		return
	}
	o.mu.Lock()
	handle := o.nodes[nodeID]
	o.mu.Unlock()
	if handle == nil {
		return
	}
	spec, err := o.client.NodeSpec(ctx, nodeID)
	if err != nil {
		nlog.Core().Warn("device platform node spec reconciliation failed", "node_id", nodeID, "error", err)
		o.notifyNode(nodeID, controlplane.StatusChange{Connected: o.stream != nil && o.stream.IsConnected(), NeedsResync: true})
		return
	}
	nodeCfg := o.cfg.ExpandDevicePlatformNode(handle.node.ID, handle.node.Type)
	desiredKernel := strings.ToLower(strings.TrimSpace(spec.KernelType))
	if desiredKernel == "" {
		desiredKernel = nodeCfg.Kernel.Type
	}
	desiredKernel = model.ResolveKernelForTransport(spec.Transport(), desiredKernel)
	if desiredKernel == handle.kernelType {
		o.notifyNode(nodeID, controlplane.StatusChange{Connected: o.stream != nil && o.stream.IsConnected(), NeedsResync: true})
		return
	}
	o.mu.Lock()
	if o.nodes[nodeID] != handle {
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()
	nlog.Core().Info("device platform node kernel changed, restarting node", "node_id", nodeID, "old", handle.kernelType, "new", desiredKernel)
	o.stopNode(nodeID)
	o.startNode(ctx, handle.node)
}

func (o *Orchestrator) onStreamStatus(connected bool) {
	o.broadcast(controlplane.StatusChange{Connected: connected, NeedsResync: connected})
}

func (o *Orchestrator) notifyNode(nodeID int, change controlplane.StatusChange) {
	o.mu.Lock()
	status := o.statuses[nodeID]
	o.mu.Unlock()
	if status == nil {
		return
	}
	select {
	case status <- change:
	default:
	}
}

func (o *Orchestrator) broadcast(change controlplane.StatusChange) {
	o.mu.Lock()
	statuses := make([]chan<- controlplane.StatusChange, 0, len(o.statuses))
	for _, status := range o.statuses {
		statuses = append(statuses, status)
	}
	o.mu.Unlock()
	for _, status := range statuses {
		select {
		case status <- change:
		default:
		}
	}
}

func (o *Orchestrator) sendHeartbeat(ctx context.Context) {
	metrics := monitor.Collect()
	response, err := o.client.SendHeartbeat(ctx, platformclient.Heartbeat{
		Version: o.version, Kernel: o.cfg.Kernel.Type,
		Capabilities: map[string]any{"machine_mode": true, "dynamic_nodes": true, "protocol_version": o.client.ProtocolVersion()},
		Metrics: map[string]any{
			"cpu":  metrics.CPU,
			"mem":  map[string]uint64{"total": metrics.MemTotal, "used": metrics.MemUsed},
			"swap": map[string]uint64{"total": metrics.SwapTotal, "used": metrics.SwapUsed},
			"disk": map[string]uint64{"total": metrics.DiskTotal, "used": metrics.DiskUsed},
			"net":  map[string]float64{"in_speed": metrics.NetInSpeed, "out_speed": metrics.NetOutSpeed},
		},
	})
	if err != nil {
		nlog.Core().Warn("device platform heartbeat failed", "error", err)
		return
	}
	o.handleCommands(ctx, response.Commands)
}

func (o *Orchestrator) handleCommands(ctx context.Context, commands []platformclient.AgentCommand) {
	for _, command := range commands {
		switch command.Type {
		case "agent.upgrade":
			if strings.TrimSpace(command.ID) == "" {
				nlog.Core().Error("Agent upgrade command has no task id")
				continue
			}
			// ACK is sent before scheduling because scheduling is detached and may
			// stop this process. A persisted state lets the replacement process
			// finish the lifecycle if the response is lost during restart.
			if err := o.persistUpgradeState(command.ID, command.TargetVersion, "acknowledged", ""); err != nil {
				nlog.Core().Error("persist Agent upgrade state", "task_id", command.ID, "error", err)
				o.failUpgrade(ctx, command.ID, "could not persist Agent upgrade state")
				continue
			}
			if err := o.sendUpgradeResult(ctx, command.ID, "acknowledged", "", ""); err != nil {
				nlog.Core().Warn("acknowledge Agent upgrade", "task_id", command.ID, "error", err)
			}
			if o.scheduleUpgrade == nil {
				nlog.Core().Error("Agent upgrade scheduler is unavailable", "task_id", command.ID)
				o.failUpgrade(ctx, command.ID, "Agent upgrade scheduler is unavailable")
				continue
			}
			if err := o.scheduleUpgrade(ctx, command.ID, command.TargetVersion, o.upgradeStatePath); err != nil {
				nlog.Core().Error("schedule Agent upgrade", "task_id", command.ID, "error", err)
				o.failUpgrade(ctx, command.ID, err.Error())
				continue
			}
			nlog.Core().Info("Agent upgrade scheduled", "task_id", command.ID, "target_version", command.TargetVersion)
		default:
			nlog.Core().Warn("unsupported Agent command", "task_id", command.ID, "type", command.Type)
		}
	}
}

func (o *Orchestrator) persistUpgradeState(taskID, targetVersion, status, message string) error {
	return saveUpgradeState(o.upgradeStatePath, upgradeState{TaskID: taskID, TargetVersion: targetVersion, Status: status, Error: message})
}

func (o *Orchestrator) sendUpgradeResult(ctx context.Context, taskID, status, version, message string) error {
	if o.client == nil {
		return errors.New("device platform client is unavailable")
	}
	resultCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return o.client.SendUpgradeResult(resultCtx, taskID, status, version, message)
}

func (o *Orchestrator) failUpgrade(ctx context.Context, taskID, message string) {
	_ = o.persistUpgradeState(taskID, "", "failed", message)
	if err := o.sendUpgradeResult(ctx, taskID, "failed", o.version, message); err != nil {
		nlog.Core().Warn("report Agent upgrade failure", "task_id", taskID, "error", err)
		return
	}
	_ = os.Remove(o.upgradeStatePath)
}

func (o *Orchestrator) reportPendingUpgrade(ctx context.Context) {
	state, err := loadUpgradeState(o.upgradeStatePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			nlog.Core().Warn("read Agent upgrade state", "error", err)
		}
		return
	}
	result, resultErr := loadUpgradeResult(upgradeResultPath(o.upgradeStatePath))
	if resultErr == nil && result.TaskID == state.TaskID {
		message := ""
		status := result.Status
		if status == "succeeded" && state.TargetVersion != "" && !targetVersionMatches(o.version, state.TargetVersion) {
			status = "failed"
			message = fmt.Sprintf("Agent reported version %q, target was %q", o.version, state.TargetVersion)
		} else if status == "failed" {
			message = fmt.Sprintf("upgrade command exited with status %d", result.Code)
		}
		if err := o.sendUpgradeResult(ctx, state.TaskID, status, o.version, message); err != nil {
			nlog.Core().Warn("report Agent upgrade result", "task_id", state.TaskID, "status", status, "error", err)
			return
		}
		_ = os.Remove(upgradeResultPath(o.upgradeStatePath))
		_ = os.Remove(o.upgradeStatePath)
		return
	}
	if state.Status == "acknowledged" && state.TargetVersion != "" && targetVersionMatches(o.version, state.TargetVersion) {
		if err := o.sendUpgradeResult(ctx, state.TaskID, "succeeded", o.version, ""); err != nil {
			nlog.Core().Warn("report inferred Agent upgrade success", "task_id", state.TaskID, "error", err)
			return
		}
		_ = os.Remove(o.upgradeStatePath)
		return
	}
	if state.Status == "failed" {
		if err := o.sendUpgradeResult(ctx, state.TaskID, "failed", o.version, state.Error); err != nil {
			nlog.Core().Warn("retry Agent upgrade failure", "task_id", state.TaskID, "error", err)
			return
		}
		_ = os.Remove(o.upgradeStatePath)
		return
	}
	if state.Status == "acknowledged" {
		if err := o.sendUpgradeResult(ctx, state.TaskID, "acknowledged", "", ""); err != nil {
			nlog.Core().Warn("retry Agent upgrade acknowledgement", "task_id", state.TaskID, "error", err)
		}
	}
}

func (o *Orchestrator) applyIntervals(handshake platformclient.Handshake) {
	o.pushInterval = handshake.Intervals.TelemetrySeconds
	if o.pushInterval < 10 {
		o.pushInterval = 60
	}
	o.pullInterval = handshake.Intervals.FallbackPullSeconds
	if o.pullInterval < 15 {
		o.pullInterval = 60
	}
	o.heartbeatInterval = time.Duration(handshake.Intervals.HeartbeatSeconds) * time.Second
	if o.heartbeatInterval < 5*time.Second {
		o.heartbeatInterval = 15 * time.Second
	}
}

func changeNodeID(nodeID *int, data []byte) int {
	if nodeID != nil {
		return *nodeID
	}
	return (platformclient.Event{Data: data}).NodeID()
}

type nodePush struct {
	stream *platformclient.Stream
}

func (p *nodePush) Run(ctx context.Context)                   { <-ctx.Done() }
func (p *nodePush) IsConnected() bool                         { return p.stream != nil && p.stream.IsConnected() }
func (p *nodePush) SendDeviceReport(devices map[int][]string) {}
