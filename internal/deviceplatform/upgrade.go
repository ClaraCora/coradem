package deviceplatform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	agentUpgradeScript = `set -eu; upgrade_script=$(mktemp); trap "rm -f \"$upgrade_script\"" EXIT; curl -fsSL https://raw.githubusercontent.com/ClaraCora/CPP/main/corade-install.sh -o "$upgrade_script"; /bin/sh "$upgrade_script" upgrade`
	upgradeLogDir      = "/var/log/corade"
	upgradeLogPath     = "/var/log/corade/upgrade.log"
)

type upgradeInitSystem string

const (
	upgradeSystemd upgradeInitSystem = "systemd"
	upgradeOpenRC  upgradeInitSystem = "openrc"
)

func scheduleAgentUpgrade(ctx context.Context, taskID string) error {
	unitSuffix := sanitizeUnitSuffix(taskID)
	if unitSuffix == "" {
		return fmt.Errorf("invalid upgrade task id %q", taskID)
	}

	initSystem, err := detectUpgradeInitSystem()
	if err != nil {
		return err
	}
	if initSystem == upgradeOpenRC {
		if err := os.MkdirAll(upgradeLogDir, 0o750); err != nil {
			return fmt.Errorf("create upgrade log directory: %w", err)
		}
	}

	command := buildUpgradeCommand(ctx, initSystem, unitSuffix)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		return fmt.Errorf("schedule Agent upgrade with %s: %w: %s", initSystem, commandErr, strings.TrimSpace(string(output)))
	}
	return nil
}

func detectUpgradeInitSystem() (upgradeInitSystem, error) {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		if _, lookupErr := exec.LookPath("systemd-run"); lookupErr == nil {
			return upgradeSystemd, nil
		}
	}
	if _, err := exec.LookPath("rc-service"); err == nil {
		return upgradeOpenRC, nil
	}
	return "", fmt.Errorf("no supported service manager found for Agent upgrade")
}

func buildUpgradeCommand(ctx context.Context, initSystem upgradeInitSystem, unitSuffix string) *exec.Cmd {
	if initSystem == upgradeSystemd {
		return exec.CommandContext(ctx, "systemd-run",
			"--unit=corade-agent-upgrade-"+unitSuffix,
			"--description=Upgrade Corade Agent",
			"--property=Type=oneshot",
			"--collect",
			"--no-block",
			"/bin/sh", "-c", agentUpgradeScript,
		)
	}

	// OpenRC has no transient-unit equivalent. nohup detaches the upgrade from
	// the Agent so rc-service can stop and replace the running binary safely.
	detached := "nohup /bin/sh -c 'sleep 2; " + agentUpgradeScript + "' >>" + upgradeLogPath + " 2>&1 </dev/null &"
	return exec.CommandContext(ctx, "/bin/sh", "-c", detached)
}

func sanitizeUnitSuffix(value string) string {
	var result strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			result.WriteRune(char)
		}
	}
	return result.String()
}
