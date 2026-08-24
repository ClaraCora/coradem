package deviceplatform

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/ClaraCora/CPanelde/internal/controlplane"
	"github.com/ClaraCora/CPanelde/internal/platformclient"
)

func TestAdvanceCursorIsMonotonicUnderConcurrency(t *testing.T) {
	orchestrator := &Orchestrator{}
	values := []string{"2", "19", "7", "4", "18", "3"}
	var wg sync.WaitGroup
	for _, value := range values {
		value := value
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := orchestrator.advanceCursor(value); err != nil {
				t.Errorf("advanceCursor(%q): %v", value, err)
			}
		}()
	}
	wg.Wait()
	if got := orchestrator.currentCursor(); got != "19" {
		t.Fatalf("cursor = %q, want 19", got)
	}
	if err := orchestrator.advanceCursor("8"); err != nil {
		t.Fatal(err)
	}
	if got := orchestrator.currentCursor(); got != "19" {
		t.Fatalf("older cursor replaced current cursor: %q", got)
	}
	if err := orchestrator.advanceCursor("invalid"); err == nil {
		t.Fatal("expected invalid cursor error")
	}
}

func TestDiffNodesFindsAddsAndRemovals(t *testing.T) {
	current := map[int]*nodeHandle{1: {}, 2: {}}
	wanted := map[int]platformclient.Node{
		2: {ID: 2, Name: "kept"},
		3: {ID: 3, Name: "added"},
	}
	remove, start := diffNodes(current, wanted)
	if len(remove) != 1 || remove[0] != 1 {
		t.Fatalf("remove = %v, want [1]", remove)
	}
	if len(start) != 1 || start[0].ID != 3 {
		t.Fatalf("start = %+v, want node 3", start)
	}
}

func TestNodeChangeNotifiesOnlyTargetNode(t *testing.T) {
	first := make(chan controlplane.StatusChange, 1)
	second := make(chan controlplane.StatusChange, 1)
	orchestrator := &Orchestrator{
		runCtx: context.Background(),
		nodes:  map[int]*nodeHandle{},
		statuses: map[int]chan<- controlplane.StatusChange{
			1: first,
			2: second,
		},
	}
	orchestrator.onEvent(platformclient.Event{
		ID: "chg_6", Type: "node.spec.replace", Data: json.RawMessage(`{"node_id":2}`),
	})

	select {
	case change := <-second:
		if !change.NeedsResync {
			t.Fatal("target notification must request reconciliation")
		}
	default:
		t.Fatal("target node was not notified")
	}
	select {
	case <-first:
		t.Fatal("unrelated node was notified")
	default:
	}
	if got := orchestrator.currentCursor(); got != "6" {
		t.Fatalf("cursor = %q, want 6", got)
	}
}

func TestHandleCommandsSchedulesAgentUpgrade(t *testing.T) {
	var scheduled string
	var targetVersion string
	orchestrator := &Orchestrator{upgradeStatePath: t.TempDir() + "/state.json", scheduleUpgrade: func(_ context.Context, taskID, target, _ string) error {
		scheduled = taskID
		targetVersion = target
		return nil
	}}
	// A nil client keeps the test focused on command scheduling; result reports
	// are best-effort and therefore do not affect scheduling.
	orchestrator.handleCommands(context.Background(), []platformclient.AgentCommand{{ID: "upg_test", Type: "agent.upgrade", TargetVersion: "v2.0.0"}})
	if scheduled != "upg_test" {
		t.Fatalf("scheduled task = %q, want upg_test", scheduled)
	}
	if targetVersion != "v2.0.0" {
		t.Fatalf("target version = %q, want v2.0.0", targetVersion)
	}
}
