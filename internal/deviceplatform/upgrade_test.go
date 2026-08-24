package deviceplatform

import (
	"context"
	"strings"
	"testing"
)

func TestBuildUpgradeCommandSystemdUsesPOSIXShell(t *testing.T) {
	command := buildUpgradeCommand(context.Background(), upgradeSystemd, "task-1")
	joined := strings.Join(command.Args, " ")
	if !strings.Contains(joined, "systemd-run") || !strings.Contains(joined, "/bin/sh -c") {
		t.Fatalf("unexpected systemd upgrade command: %q", joined)
	}
	if strings.Contains(joined, "/bin/bash") {
		t.Fatalf("systemd command must not require bash: %q", joined)
	}
}

func TestBuildUpgradeCommandOpenRCIsDetachedAndLogged(t *testing.T) {
	command := buildUpgradeCommand(context.Background(), upgradeOpenRC, "task-1", "v2.0.0", "task-1", "/etc/corade/agent-upgrade-result")
	joined := strings.Join(command.Args, " ")
	for _, expected := range []string{"nohup", "sleep 2", upgradeLogPath, "upgrade_script", "/bin/sh", "--version", "v2.0.0", "agent-upgrade-result"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("OpenRC command %q does not contain %q", joined, expected)
		}
	}
}

func TestBuildUpgradeScriptRejectsShellMetacharacters(t *testing.T) {
	if err := validateUpgradeVersion("v2.0.0; touch /tmp/pwned"); err == nil {
		t.Fatal("expected shell metacharacters to be rejected")
	}
}

func TestUpgradeStateRoundTrip(t *testing.T) {
	path := t.TempDir() + "/state.json"
	state := upgradeState{TaskID: "upg_test", TargetVersion: "v2.0.0", Status: "acknowledged"}
	if err := saveUpgradeState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TaskID != state.TaskID || loaded.TargetVersion != state.TargetVersion || loaded.Status != state.Status {
		t.Fatalf("loaded state = %+v, want %+v", loaded, state)
	}
}

func TestSanitizeUnitSuffix(t *testing.T) {
	if got := sanitizeUnitSuffix("task/../42 !"); got != "task42" {
		t.Fatalf("sanitizeUnitSuffix() = %q, want task42", got)
	}
}
