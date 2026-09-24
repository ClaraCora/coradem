#!/bin/sh
set -eu

REPOSITORY="ClaraCora/CPP"
SERVICE_NAME="corade"
SYSTEMD_SERVICE_NAME="corade.service"
BINARY_PATH="/usr/local/bin/corade"
CLI_PATH="/usr/local/bin/coradectl"
CONFIG_DIR="/etc/corade"
CONFIG_FILE="${CONFIG_DIR}/config.yml"
ENV_FILE="${CONFIG_DIR}/agent.env"
SYSTEMD_SERVICE_FILE="/etc/systemd/system/${SYSTEMD_SERVICE_NAME}"
OPENRC_SERVICE_FILE="/etc/init.d/${SERVICE_NAME}"
OPENRC_LOG_DIR="/var/log/corade"
OPENRC_LOG_FILE="${OPENRC_LOG_DIR}/corade.log"
ACTION="install"
CONTROL_URL="${CORADE_CONTROL_URL:-}"
COMMUNICATION_KEY="${CORADE_AGENT_TOKEN:-}"
MACHINE_ID=""
PANEL_PUBLIC_KEY=""
KERNEL_TYPE="xray"
HEALTH_PORT="65530"
BINARY_SOURCE=""
VERSION="latest"
INIT_SYSTEM=""
CLI_REPLACED=0

usage() {
  cat <<'HELP'
Corade Agent installer

Usage:
  install.sh [install] --control-url URL --communication-key KEY --machine-id ID [options]
  install.sh upgrade [--version VERSION]

Required for install:
  --control-url URL            CPanel public URL
  --communication-key KEY     Shared Agent communication key
  --machine-id ID             CPanel server ID
  --panel-public-key KEY      Pinned CPanel Ed25519 public key

Optional:
  --kernel singbox|xray       Default kernel type (default: xray)
  --health-port PORT          Local health endpoint (default: 65530)
  --binary PATH               Install a local Corade binary
  --version VERSION           CPP Corade release version (default: latest)
  --help                      Show this help

CORADE_CONTROL_URL and CORADE_AGENT_TOKEN may be used instead of putting those
values on the command line. Both systemd and Alpine OpenRC are supported.
HELP
}

log() { printf '[corade] %s\n' "$*"; }
fail() { printf '[corade] ERROR: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" = "install" ] || [ "${1:-}" = "upgrade" ]; then
  ACTION="$1"
  shift
fi

while [ "$#" -gt 0 ]; do
  case "$1" in
    --control-url) CONTROL_URL="${2:-}"; shift 2 ;;
    --communication-key) COMMUNICATION_KEY="${2:-}"; shift 2 ;;
    --machine-id) MACHINE_ID="${2:-}"; shift 2 ;;
    --panel-public-key) PANEL_PUBLIC_KEY="${2:-}"; shift 2 ;;
    --kernel) KERNEL_TYPE="${2:-}"; shift 2 ;;
    --health-port) HEALTH_PORT="${2:-}"; shift 2 ;;
    --binary) BINARY_SOURCE="${2:-}"; shift 2 ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || fail "run this installer as root"
command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"

detect_init_system() {
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    INIT_SYSTEM="systemd"
    return
  fi
  if command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1; then
    INIT_SYSTEM="openrc"
    return
  fi
  fail "a running systemd or OpenRC installation is required"
}

service_file_exists() {
  case "$INIT_SYSTEM" in
    systemd) [ -f "$SYSTEMD_SERVICE_FILE" ] ;;
    openrc) [ -f "$OPENRC_SERVICE_FILE" ] ;;
  esac
}

service_stop() {
  case "$INIT_SYSTEM" in
    systemd) systemctl stop "$SYSTEMD_SERVICE_NAME" ;;
    openrc) rc-service "$SERVICE_NAME" stop ;;
  esac
}

service_reload_manager() {
  if [ "$INIT_SYSTEM" = "systemd" ]; then
    systemctl daemon-reload
  fi
}

service_enable() {
  case "$INIT_SYSTEM" in
    systemd) systemctl enable "$SYSTEMD_SERVICE_NAME" ;;
    openrc) rc-update add "$SERVICE_NAME" default ;;
  esac
}

service_restart() {
  case "$INIT_SYSTEM" in
    systemd) systemctl restart "$SYSTEMD_SERVICE_NAME" ;;
    openrc) rc-service "$SERVICE_NAME" restart ;;
  esac
}

service_is_active() {
  case "$INIT_SYSTEM" in
    systemd) systemctl is-active --quiet "$SYSTEMD_SERVICE_NAME" ;;
    openrc) rc-service --quiet "$SERVICE_NAME" status ;;
  esac
}

print_service_logs() {
  case "$INIT_SYSTEM" in
    systemd) journalctl -u "$SYSTEMD_SERVICE_NAME" -n 50 --no-pager >&2 || true ;;
    openrc) tail -n 50 "$OPENRC_LOG_FILE" >&2 2>/dev/null || true ;;
  esac
}

detect_init_system
log "detected init system: ${INIT_SYSTEM}"

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

TMP_DIR=$(mktemp -d)
trap 'rm -rf -- "$TMP_DIR"' EXIT HUP INT TERM
STAGED_BINARY="${TMP_DIR}/corade"
STAGED_CLI="${TMP_DIR}/coradectl"
STAGED_OPENRC_SERVICE="${TMP_DIR}/corade.openrc"
PREVIOUS_BINARY="${TMP_DIR}/corade.previous"
PREVIOUS_CLI="${TMP_DIR}/coradectl.previous"
AGENT_ARTIFACT="corade-linux-${ARCH}"
CLI_ARTIFACT="coradectl-linux-${ARCH}"

verify_download() {
  artifact="$1"
  # BusyBox (Alpine) and GNU coreutils both support -c, not --check/--status.
  (cd "$TMP_DIR" && sha256sum -c "${artifact}.sha256" >/dev/null)
}

try_download() {
  artifact="$1"
  destination="$2"
  base_url="$3"
  rm -f "${TMP_DIR}/${artifact}" "${TMP_DIR}/${artifact}.sha256"
  curl --fail --location --silent --show-error --retry 3 "${base_url}/${artifact}" -o "${TMP_DIR}/${artifact}" || return 1
  curl --fail --location --silent --show-error --retry 3 "${base_url}/${artifact}.sha256" -o "${TMP_DIR}/${artifact}.sha256" || return 1
  verify_download "$artifact" || return 1
  mv "${TMP_DIR}/${artifact}" "$destination"
}

stage_artifacts() {
  if [ -n "$BINARY_SOURCE" ]; then
    [ -f "$BINARY_SOURCE" ] || fail "binary not found: $BINARY_SOURCE"
    cp "$BINARY_SOURCE" "$STAGED_BINARY"
  else
    case "$VERSION" in
      latest) release_tag="corade-latest" ;;
      corade-*) release_tag="$VERSION" ;;
      *) release_tag="corade-${VERSION}" ;;
    esac
    base_url="https://github.com/${REPOSITORY}/releases/download/${release_tag}"
    log "downloading ${REPOSITORY} release ${VERSION}"
    try_download "$AGENT_ARTIFACT" "$STAGED_BINARY" "$base_url" || fail "could not download a verified Agent binary"
    try_download "$CLI_ARTIFACT" "$STAGED_CLI" "$base_url" || fail "could not download a verified coradectl binary"
  fi

  chmod 755 "$STAGED_BINARY"
  "$STAGED_BINARY" -v >/dev/null 2>&1 || fail "downloaded binary failed its version check"
  if [ -f "$STAGED_CLI" ]; then
    chmod 755 "$STAGED_CLI"
    "$STAGED_CLI" version >/dev/null 2>&1 || fail "downloaded coradectl failed its version check"
  fi
}

service_is_healthy() {
  service_is_active || return 1
  if [ "$HEALTH_PORT" != "0" ]; then
    curl --fail --silent "http://127.0.0.1:${HEALTH_PORT}/healthz" >/dev/null 2>&1 || return 1
  fi
}

wait_until_healthy() {
  attempts=0
  while [ "$attempts" -lt 30 ]; do
    if service_is_healthy; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  return 1
}

copy_with_mode() {
  source_file="$1"
  destination="$2"
  mode="$3"
  cp "$source_file" "$destination" || return 1
  chmod "$mode" "$destination" || return 1
}

restore_previous_version() {
  restored=0
  if [ -f "$PREVIOUS_BINARY" ]; then
    copy_with_mode "$PREVIOUS_BINARY" "$BINARY_PATH" 755
    restored=1
  fi
  if [ "$CLI_REPLACED" -eq 1 ] && [ -f "$PREVIOUS_CLI" ]; then
    copy_with_mode "$PREVIOUS_CLI" "$CLI_PATH" 755 || return 1
  fi
  [ "$restored" -eq 1 ] || return 1
  service_reload_manager || true
  service_restart || return 1
  wait_until_healthy
}

install_artifacts_and_restart() {
  if [ -f "$BINARY_PATH" ]; then
    cp "$BINARY_PATH" "$PREVIOUS_BINARY"
  fi
  if [ -f "$STAGED_CLI" ] && [ -f "$CLI_PATH" ]; then
    cp "$CLI_PATH" "$PREVIOUS_CLI"
  fi

  service_stop >/dev/null 2>&1 || true
  copy_with_mode "$STAGED_BINARY" "$BINARY_PATH" 755
  if [ -f "$STAGED_CLI" ]; then
    copy_with_mode "$STAGED_CLI" "$CLI_PATH" 755
    CLI_REPLACED=1
  fi
  service_reload_manager
  service_enable >/dev/null
  service_restart
  if wait_until_healthy; then
    return
  fi

  print_service_logs
  if [ -f "$PREVIOUS_BINARY" ]; then
    log "new Agent did not become healthy; restoring the previous version"
    if restore_previous_version; then
      fail "Agent did not become healthy; the previous version was restored"
    fi
    fail "Agent did not become healthy and automatic rollback failed"
  fi
  fail "Agent did not become healthy"
}

if [ "$ACTION" = "upgrade" ]; then
  [ -f "$CONFIG_FILE" ] && [ -f "$ENV_FILE" ] && service_file_exists || fail "Corade is not installed; run install first"
  HEALTH_PORT=$(awk '/^[[:space:]]*health_port:/ {print $2; exit}' "$CONFIG_FILE")
  HEALTH_PORT=${HEALTH_PORT:-65530}
  stage_artifacts
  install_artifacts_and_restart
  log "upgrade completed"
  log "service manager: ${INIT_SYSTEM}"
  exit 0
fi

[ -n "$CONTROL_URL" ] || fail "--control-url or CORADE_CONTROL_URL is required"
[ -n "$MACHINE_ID" ] || fail "--machine-id is required"
[ "${#COMMUNICATION_KEY}" -ge 32 ] || fail "--communication-key or CORADE_AGENT_TOKEN must contain at least 32 characters"
case "$CONTROL_URL" in http://*|https://*) ;; *) fail "control URL must start with http:// or https://" ;; esac
case "$CONTROL_URL" in *[[:space:]\"\\]*) fail "control URL contains unsupported characters" ;; esac
case "$MACHINE_ID" in *[!A-Za-z0-9_-]*|'') fail "machine ID contains unsupported characters" ;; esac
if [ -n "$PANEL_PUBLIC_KEY" ]; then
  [ "${#PANEL_PUBLIC_KEY}" -eq 43 ] || fail "panel public key is invalid"
  case "$PANEL_PUBLIC_KEY" in *[!A-Za-z0-9_-]*) fail "panel public key is invalid" ;; esac
fi
case "$COMMUNICATION_KEY" in *[!A-Za-z0-9_+/=-]*) fail "communication key contains unsupported characters" ;; esac
case "$HEALTH_PORT" in *[!0-9]*|'') fail "health port must be a number" ;; esac
case "$KERNEL_TYPE" in singbox|xray) ;; *) fail "kernel must be singbox or xray" ;; esac

stage_artifacts
mkdir -p "$CONFIG_DIR"
if [ -f "$CONFIG_FILE" ]; then
  cp "$CONFIG_FILE" "${CONFIG_FILE}.backup.$(date +%Y%m%d%H%M%S)"
fi

cat >"${TMP_DIR}/config.yml" <<EOF
control:
  mode: "device-platform"
  url: "${CONTROL_URL%/}"
  token_env: "CORADE_AGENT_TOKEN"
  machine_id: "${MACHINE_ID}"
EOF

if [ -n "$PANEL_PUBLIC_KEY" ]; then
  printf '  panel_public_key: "%s"\n' "$PANEL_PUBLIC_KEY" >>"${TMP_DIR}/config.yml"
fi

cat >>"${TMP_DIR}/config.yml" <<EOF
kernel:
  type: "${KERNEL_TYPE}"
  log_level: "warn"

log:
  level: "info"
  output: "stdout"

health_port: ${HEALTH_PORT}
EOF

printf 'CORADE_AGENT_TOKEN=%s\n' "$COMMUNICATION_KEY" >"${TMP_DIR}/agent.env"
chmod 600 "${TMP_DIR}/agent.env"

if [ "$INIT_SYSTEM" = "systemd" ]; then
  mkdir -p /etc/systemd/system
  cat >"${TMP_DIR}/${SYSTEMD_SERVICE_NAME}" <<EOF
[Unit]
Description=Corade device platform Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${CONFIG_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${BINARY_PATH} -c ${CONFIG_FILE}
Restart=always
RestartSec=5
LimitNOFILE=1048576
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
else
  mkdir -p "$OPENRC_LOG_DIR"
  chmod 750 "$OPENRC_LOG_DIR"
  : > "$OPENRC_LOG_FILE"
  chmod 640 "$OPENRC_LOG_FILE"
  cat >"$STAGED_OPENRC_SERVICE" <<EOF
#!/sbin/openrc-run

name="Corade device platform Agent"
description="Corade device platform Agent"
supervisor="supervise-daemon"
command="${BINARY_PATH}"
command_args="-c ${CONFIG_FILE}"
directory="${CONFIG_DIR}"
pidfile="/run/\${RC_SVCNAME}.pid"
respawn_delay=5
respawn_max=0
output_log="${OPENRC_LOG_FILE}"
error_log="${OPENRC_LOG_FILE}"
required_files="${CONFIG_FILE} ${ENV_FILE}"

if [ -r "${ENV_FILE}" ]; then
  . "${ENV_FILE}"
  export CORADE_AGENT_TOKEN
fi

depend() {
  need net
  after firewall
}
EOF
fi

copy_with_mode "${TMP_DIR}/config.yml" "$CONFIG_FILE" 600
copy_with_mode "${TMP_DIR}/agent.env" "$ENV_FILE" 600
if [ "$INIT_SYSTEM" = "systemd" ]; then
  copy_with_mode "${TMP_DIR}/${SYSTEMD_SERVICE_NAME}" "$SYSTEMD_SERVICE_FILE" 644
else
  copy_with_mode "$STAGED_OPENRC_SERVICE" "$OPENRC_SERVICE_FILE" 755
fi
install_artifacts_and_restart

log "installation completed"
log "service manager: ${INIT_SYSTEM}"
log "config: ${CONFIG_FILE}"
if [ "$INIT_SYSTEM" = "systemd" ]; then
  log "logs: journalctl -u ${SYSTEMD_SERVICE_NAME} -f"
else
  log "logs: tail -f ${OPENRC_LOG_FILE}"
fi
