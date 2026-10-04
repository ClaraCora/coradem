package deviceplatform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	upgradeStateFileName  = "agent-upgrade-state.json"
	upgradeResultFileName = "agent-upgrade-result"
	// Keep this below the control-plane timeout so an upgrade that never
	// produces a result is reported as failed before the panel marks it stale.
	// This also prevents an acknowledged task from remaining in "upgrading"
	// forever when the installer or init system exits unexpectedly.
	upgradePendingResultTimeout = 12 * time.Minute
)

type upgradeState struct {
	TaskID        string    `json:"task_id"`
	TargetVersion string    `json:"target_version,omitempty"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type upgradeResult struct {
	TaskID  string
	Status  string
	Code    int
	Message string
}

func upgradeStatePath(configDir string) string {
	return filepath.Join(configDir, upgradeStateFileName)
}

func upgradeResultPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), upgradeResultFileName)
}

func upgradeErrorPath(resultPath string) string {
	return resultPath + ".error"
}

func loadUpgradeState(path string) (upgradeState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return upgradeState{}, err
	}
	if len(data) > 16<<10 {
		return upgradeState{}, errors.New("Agent upgrade state is too large")
	}
	var state upgradeState
	if err := json.Unmarshal(data, &state); err != nil {
		return upgradeState{}, fmt.Errorf("decode Agent upgrade state: %w", err)
	}
	state.TaskID = strings.TrimSpace(state.TaskID)
	state.TargetVersion = strings.TrimSpace(state.TargetVersion)
	state.Status = strings.ToLower(strings.TrimSpace(state.Status))
	if state.TaskID == "" || sanitizeUnitSuffix(state.TaskID) != state.TaskID {
		return upgradeState{}, errors.New("Agent upgrade state has an invalid task id")
	}
	return state, nil
}

func saveUpgradeState(path string, state upgradeState) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("Agent upgrade state path is empty")
	}
	if state.TaskID == "" || sanitizeUnitSuffix(state.TaskID) != state.TaskID {
		return errors.New("Agent upgrade task id is invalid")
	}
	if err := validateUpgradeVersion(state.TargetVersion); err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create Agent upgrade state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agent-upgrade-state-*")
	if err != nil {
		return fmt.Errorf("create Agent upgrade state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Agent upgrade state: %w", err)
	}
	return nil
}

func loadUpgradeResult(path string) (upgradeResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return upgradeResult{}, err
	}
	if len(data) > 1024 {
		return upgradeResult{}, errors.New("Agent upgrade result is too large")
	}
	fields := strings.Fields(string(data))
	if len(fields) != 3 || sanitizeUnitSuffix(fields[0]) != fields[0] {
		return upgradeResult{}, errors.New("Agent upgrade result is invalid")
	}
	if fields[1] != "succeeded" && fields[1] != "failed" {
		return upgradeResult{}, errors.New("Agent upgrade result has an invalid status")
	}
	code, err := strconv.Atoi(fields[2])
	if err != nil || code < 0 || code > 255 {
		return upgradeResult{}, errors.New("Agent upgrade result has an invalid exit code")
	}
	result := upgradeResult{TaskID: fields[0], Status: fields[1], Code: code}
	if message, messageErr := os.ReadFile(upgradeErrorPath(path)); messageErr == nil {
		if len(message) > 4096 {
			message = message[:4096]
		}
		result.Message = strings.TrimSpace(string(message))
	}
	return result, nil
}

func versionsEqual(current, target string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "corade-")
		value = strings.TrimPrefix(value, "v")
		if index := strings.IndexByte(value, '+'); index >= 0 {
			value = value[:index]
		}
		return value
	}
	return normalize(current) != "" && normalize(current) == normalize(target)
}

func targetVersionMatches(current, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "latest" || target == "corade-latest" {
		return true // the rolling tag has no immutable value to compare locally
	}
	return versionsEqual(current, target)
}
