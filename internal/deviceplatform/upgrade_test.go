package deviceplatform

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
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

func TestBuildUpgradeScriptCapturesFailureOutput(t *testing.T) {
	script := buildUpgradeScript("v2.0.5+build", "task-1", "/etc/corade/agent-upgrade-result")
	for _, expected := range []string{"2>&1", "upgrade-result.error", "tail -c 1800", "result_status", "upgrade-result"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("upgrade script %q does not contain %q", script, expected)
		}
	}
	if strings.Index(script, "mv \"$message_tmp\"") > strings.Index(script, "mv \"$result_tmp\"") {
		t.Fatal("terminal result must be written after the failure summary")
	}
}

func TestVersionsEqualIgnoresBuildMetadata(t *testing.T) {
	if !versionsEqual("v2.0.5+d9c44b9", "v2.0.5") {
		t.Fatal("versions with different build metadata should match their release version")
	}
	if versionsEqual("v2.0.6+d9c44b9", "v2.0.5") {
		t.Fatal("different release versions must not match")
	}
}

func TestClassifyUpgradeResult(t *testing.T) {
	tests := []struct {
		name           string
		state          upgradeState
		result         upgradeResult
		currentVersion string
		wantStatus     string
		wantMessage    string
	}{
		{
			name:           "installer failure remains failure when target version is running",
			state:          upgradeState{TargetVersion: "v2.0.5"},
			result:         upgradeResult{Status: "failed", Code: 1, Message: "service restart interrupted installer"},
			currentVersion: "v2.0.5+build.1",
			wantStatus:     "failed",
			wantMessage:    "service restart interrupted installer",
		},
		{
			name:           "target is not running",
			state:          upgradeState{TargetVersion: "v2.0.5"},
			result:         upgradeResult{Status: "failed", Code: 1, Message: "checksum mismatch"},
			currentVersion: "v2.0.4",
			wantStatus:     "failed",
			wantMessage:    "checksum mismatch",
		},
		{
			name:           "latest failure cannot be inferred as success",
			state:          upgradeState{TargetVersion: "latest"},
			result:         upgradeResult{Status: "failed", Code: 1},
			currentVersion: "v2.0.5",
			wantStatus:     "failed",
			wantMessage:    "upgrade command exited with status 1",
		},
		{
			name:           "successful result with wrong version",
			state:          upgradeState{TargetVersion: "v2.0.5"},
			result:         upgradeResult{Status: "succeeded"},
			currentVersion: "v2.0.4",
			wantStatus:     "failed",
			wantMessage:    `Agent reported version "v2.0.4", target was "v2.0.5"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := classifyUpgradeResult(test.state, test.result, test.currentVersion)
			if status != test.wantStatus || message != test.wantMessage {
				t.Fatalf("classifyUpgradeResult() = (%q, %q), want (%q, %q)", status, message, test.wantStatus, test.wantMessage)
			}
		})
	}
}

func TestLoadUpgradeResultIncludesFailureMessage(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/agent-upgrade-result"
	if err := os.WriteFile(path, []byte("task-1 failed 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upgradeErrorPath(path), []byte("download failed: checksum mismatch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := loadUpgradeResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "download failed: checksum mismatch" {
		t.Fatalf("result message = %q", result.Message)
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

func TestLoadUpgradeStatePreservesUpdatedAt(t *testing.T) {
	path := t.TempDir() + "/state.json"
	updated := time.Now().UTC().Add(-upgradePendingResultTimeout - time.Second)
	data, err := json.Marshal(upgradeState{TaskID: "task-1", TargetVersion: "v2.0.5", Status: "acknowledged", UpdatedAt: updated})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadUpgradeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UpdatedAt.IsZero() || !loaded.UpdatedAt.Equal(updated) {
		t.Fatalf("loaded UpdatedAt = %v, want %v", loaded.UpdatedAt, updated)
	}
}

func TestUpgradePendingResultTimeoutIsBelowPanelTimeout(t *testing.T) {
	if upgradePendingResultTimeout >= 15*time.Minute {
		t.Fatalf("pending result timeout %s must be below the panel timeout", upgradePendingResultTimeout)
	}
}

func TestSanitizeUnitSuffix(t *testing.T) {
	if got := sanitizeUnitSuffix("task/../42 !"); got != "task42" {
		t.Fatalf("sanitizeUnitSuffix() = %q, want task42", got)
	}
}
