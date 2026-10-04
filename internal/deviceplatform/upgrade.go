package deviceplatform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	agentUpgradeScriptPrefix = `upgrade_script=$(mktemp) || exit $?; trap "rm -f \"$upgrade_script\"" EXIT; curl -fsSL https://raw.githubusercontent.com/ClaraCora/CPP/main/corade-install.sh -o "$upgrade_script" || exit $?; /bin/sh "$upgrade_script"`
	upgradeLogDir            = "/var/log/corade"
	upgradeLogPath           = "/var/log/corade/upgrade.log"
)

type upgradeInitSystem string

const (
	upgradeSystemd upgradeInitSystem = "systemd"
	upgradeOpenRC  upgradeInitSystem = "openrc"
)

func scheduleAgentUpgrade(ctx context.Context, taskID, targetVersion, statePath string) error {
	unitSuffix := sanitizeUnitSuffix(taskID)
	if unitSuffix == "" {
		return fmt.Errorf("invalid upgrade task id %q", taskID)
	}
	targetVersion = strings.TrimSpace(targetVersion)
	if err := validateUpgradeVersion(targetVersion); err != nil {
		return err
	}

	initSystem, err := detectUpgradeInitSystem()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(upgradeLogDir, 0o750); err != nil {
		return fmt.Errorf("create upgrade log directory: %w", err)
	}

	command := buildUpgradeCommand(ctx, initSystem, unitSuffix, targetVersion, taskID, upgradeResultPath(statePath))
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

func buildUpgradeCommand(ctx context.Context, initSystem upgradeInitSystem, unitSuffix string, targetVersion ...string) *exec.Cmd {
	version := ""
	taskID := ""
	resultPath := ""
	if len(targetVersion) > 0 {
		version = strings.TrimSpace(targetVersion[0])
	}
	if len(targetVersion) > 1 {
		taskID = strings.TrimSpace(targetVersion[1])
	}
	if len(targetVersion) > 2 {
		resultPath = strings.TrimSpace(targetVersion[2])
	}
	script := buildUpgradeScript(version, taskID, resultPath)
	if initSystem == upgradeSystemd {
		return exec.CommandContext(ctx, "systemd-run",
			"--unit=corade-agent-upgrade-"+unitSuffix,
			"--description=Upgrade Corade Agent",
			"--property=Type=oneshot",
			"--collect",
			"--no-block",
			"--property=StandardOutput=append:"+upgradeLogPath,
			"--property=StandardError=append:"+upgradeLogPath,
			"/bin/sh", "-c", script,
		)
	}

	// OpenRC has no transient-unit equivalent. nohup detaches the upgrade from
	// the Agent so rc-service can stop and replace the running binary safely.
	detached := "nohup /bin/sh -c " + shellQuote("sleep 2; "+script) + " >>" + shellQuote(upgradeLogPath) + " 2>&1 </dev/null &"
	return exec.CommandContext(ctx, "/bin/sh", "-c", detached)
}

func buildUpgradeScript(targetVersion, taskID, resultPath string) string {
	installer := agentUpgradeScriptPrefix + " upgrade"
	if strings.TrimSpace(targetVersion) != "" {
		installer += " --version " + shellQuote(strings.TrimSpace(targetVersion))
	}
	if taskID == "" || resultPath == "" {
		return installer
	}
	// The installer stops and restarts this Agent. The detached shell therefore
	// records its terminal result on disk so either the current process (failure)
	// or the replacement process (success) can report it to CPanel.
	resultErrorPath := upgradeErrorPath(resultPath)
	return "umask 077; mkdir -p " + shellQuote(upgradeLogDir) + " 2>/dev/null || true; " +
		"printf '%s\n' " + shellQuote("Corade Agent upgrade started: task="+taskID+" target="+targetVersion) + "; " +
		"rm -f " + shellQuote(resultPath) + " " + shellQuote(resultErrorPath) + "; " +
		"output_file=$(mktemp) || { printf '%s failed 125\n' " + shellQuote(taskID) + " >" + shellQuote(resultPath) + " 2>/dev/null || true; printf '%s' 'could not create upgrade output file' >" + shellQuote(resultErrorPath) + " 2>/dev/null || true; exit 125; }; trap 'rm -f \"$output_file\"' EXIT; " +
		"result_status=succeeded; result_code=0; if /bin/sh -c " + shellQuote(installer) + " >\"$output_file\" 2>&1; then :; else result_code=$?; result_status=failed; fi; " +
		"if [ \"$result_status\" = failed ]; then " +
		"message_tmp=" + shellQuote(resultErrorPath) + ".$$; " +
		"tail -c 1800 \"$output_file\" 2>/dev/null | tr '\\r\\n' ' ' >\"$message_tmp\" || true; " +
		"if [ ! -s \"$message_tmp\" ]; then printf '%s' \"upgrade installer exited with status $result_code without diagnostic output\" >\"$message_tmp\"; fi; " +
		"mv \"$message_tmp\" " + shellQuote(resultErrorPath) + " 2>/dev/null || { cat \"$message_tmp\" >" + shellQuote(resultErrorPath) + " 2>/dev/null || true; rm -f \"$message_tmp\"; }; " +
		"fi; result_tmp=" + shellQuote(resultPath) + ".$$; " +
		"printf '%s %s %s\\n' " + shellQuote(taskID) + " \"$result_status\" \"$result_code\" >\"$result_tmp\"; " +
		"mv \"$result_tmp\" " + shellQuote(resultPath) + " 2>/dev/null || { cat \"$result_tmp\" >" + shellQuote(resultPath) + " 2>/dev/null || true; rm -f \"$result_tmp\"; }; exit \"$result_code\""
}

// Upgrade versions become shell arguments in a detached command. Keep the
// accepted alphabet deliberately narrow so a server-side value can never
// inject another command into the installer.
func validateUpgradeVersion(version string) error {
	if len(version) > 128 {
		return fmt.Errorf("invalid upgrade target version: too long")
	}
	for _, char := range version {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' || char == '+' {
			continue
		}
		return fmt.Errorf("invalid upgrade target version %q", version)
	}
	return nil
}

func shellQuote(value string) string {
	// validateUpgradeVersion currently excludes single quotes. Keep this helper
	// general so the command construction remains safe if the validator grows.
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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
