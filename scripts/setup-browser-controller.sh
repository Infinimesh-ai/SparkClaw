#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

ENV_FILE="${SPARKCLAW_BROWSER_ENV_FILE:-$ROOT/.env.local}"
MODE="install"
PACKAGE_DIR="$ROOT/tools/browser-controller"

usage() {
  cat <<'EOF'
Usage: bash scripts/setup-browser-controller.sh [--check] [--env-file PATH]

Install or verify the preview-only Playwright Extension host controller. The
controller uses a disposable qualification profile and does not replace the
Host-CDP production browser backend.

Options:
  --check          Verify dependencies, service files, and the private socket
  --env-file PATH  Write/read non-secret controller paths in this env file
  -h, --help       Show this help
EOF
}

log() {
  printf '[sparkclaw-browser-controller] %s\n' "$*"
}

fail() {
  printf '[sparkclaw-browser-controller] error: %s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) MODE="check" ;;
    --env-file)
      shift
      [[ $# -gt 0 ]] || fail "--env-file requires a path"
      ENV_FILE="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
  shift
done

[[ "$(uname -s)" == "Linux" ]] || fail "the browser controller supports Linux hosts only"
[[ "$EUID" -ne 0 ]] || fail "run as the desktop owner, not root"
for command_name in node npm python3 systemctl curl; do
  command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done
[[ -f "$PACKAGE_DIR/package.json" && -f "$PACKAGE_DIR/package-lock.json" ]] ||
  fail "browser controller package metadata is missing"
[[ -f "$ENV_FILE" ]] || fail "environment file not found: $ENV_FILE"

node_major="$(node -p 'process.versions.node.split(".")[0]')"
npm_major="$(npm --version | cut -d. -f1)"
[[ "$node_major" == "26" ]] || fail "Node.js 26 is required; found $(node --version)"
[[ "$npm_major" == "11" ]] || fail "npm 11 is required; found $(npm --version)"

runtime_base="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
runtime_dir="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST "$runtime_base/sparkclaw/browser-controller")"
socket_path="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST "$runtime_dir/controller.sock")"
container_socket="/run/sparkclaw/browser-controller/controller.sock"
profile_dir="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/extension-qualification/user-data"
browser_config="${XDG_CONFIG_HOME:-$HOME/.config}/sparkclaw/browserd.json"
systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
unit_path="$systemd_dir/sparkclaw-browser-controller.service"
node_path="$(command -v node)"
entry_path="$PACKAGE_DIR/src/main.mjs"

[[ "$runtime_dir" == /* && "$socket_path" == /* ]] || fail "controller runtime paths must be absolute"
[[ "$socket_path" == "$runtime_dir/controller.sock" ]] || fail "controller socket must be inside its private runtime directory"
[[ -r "$browser_config" ]] || fail "host browser config is missing; run npm run setup:browser first"

mapfile -t browser_values < <(python3 - "$browser_config" <<'PY'
import json
from pathlib import Path
import sys

with Path(sys.argv[1]).open(encoding="utf-8") as stream:
    value = json.load(stream)
executable = str(value.get("executable", "")).strip()
if not executable.startswith("/"):
    raise SystemExit("host browser executable is invalid")
print(executable)
PY
)
(( ${#browser_values[@]} == 1 )) || fail "host browser config is invalid"
browser_executable="${browser_values[0]}"
[[ -x "$browser_executable" ]] || fail "host browser executable is unavailable: $browser_executable"

set_env_value() {
  local key="$1"
  local value="$2"
  local temporary line
  local found=false

  temporary="$(mktemp "${ENV_FILE}.browser-controller.XXXXXX")"
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == "$key="* ]]; then
      if [[ "$found" == false ]]; then
        printf '%s=%s\n' "$key" "$value" >>"$temporary"
        found=true
      fi
    else
      printf '%s\n' "$line" >>"$temporary"
    fi
  done <"$ENV_FILE"
  if [[ "$found" == false ]]; then
    printf '%s=%s\n' "$key" "$value" >>"$temporary"
  fi
  chmod 600 "$temporary"
  mv -f -- "$temporary" "$ENV_FILE"
}

systemd_quote() {
  python3 - "$1" <<'PY'
import json
import sys
print(json.dumps(sys.argv[1], ensure_ascii=False))
PY
}

verify_installation() {
  local health

  npm --prefix "$PACKAGE_DIR" ls --depth=0 >/dev/null
  [[ -d "$runtime_dir" && ! -L "$runtime_dir" ]] || fail "controller runtime directory is missing or unsafe"
  [[ "$(stat -c '%u:%a' "$runtime_dir")" == "$(id -u):700" ]] ||
    fail "controller runtime directory must be owner-only"
  [[ -r "$unit_path" ]] || fail "browser controller user unit is missing"
  grep -Fqx "WorkingDirectory=$PACKAGE_DIR" "$unit_path" ||
    fail "browser controller user unit has a stale working directory"
  grep -Fqx "ExecStart=$(systemd_quote "$node_path") $(systemd_quote "$entry_path")" "$unit_path" ||
    fail "browser controller user unit has a stale entrypoint"
  grep -Fqx "Environment=$(systemd_quote "SPARKCLAW_BROWSER_CONTROLLER_SOCKET=$socket_path")" "$unit_path" ||
    fail "browser controller user unit has a stale socket"
  grep -Fqx "Environment=$(systemd_quote "SPARKCLAW_BROWSER_USER_DATA_DIR=$profile_dir")" "$unit_path" ||
    fail "browser controller user unit has a stale qualification profile"
  grep -Fqx "Environment=$(systemd_quote "SPARKCLAW_BROWSER_CHANNEL=chromium")" "$unit_path" ||
    fail "browser controller user unit has a stale browser channel"
  systemctl --user is-active --quiet sparkclaw-browser-controller.service ||
    fail "sparkclaw-browser-controller is not active"
  [[ -S "$socket_path" && ! -L "$socket_path" ]] || fail "browser controller socket is unavailable"
  [[ "$(stat -c '%u:%a' "$socket_path")" == "$(id -u):600" ]] ||
    fail "browser controller socket must be owner-only"
  health="$(curl --fail --silent --show-error --max-time 5 --unix-socket "$socket_path" http://localhost/v1/health)" ||
    fail "browser controller health request failed"
  python3 - "$health" <<'PY'
import json
import sys

value = json.loads(sys.argv[1])
if value.get("schema_version") != 1 or value.get("profile_id") != "default":
    raise SystemExit("browser controller health response is invalid")
if value.get("state") not in {"ready", "busy"}:
    raise SystemExit("browser controller is not ready")
PY
  grep -Fxq "SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST=$runtime_dir" "$ENV_FILE" ||
    fail "controller host runtime path is missing or stale in $ENV_FILE"
  grep -Fxq "SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET=$container_socket" "$ENV_FILE" ||
    fail "controller container socket is missing or stale in $ENV_FILE"
  grep -Fxq "SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST=$socket_path" "$ENV_FILE" ||
    fail "controller host socket is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID=default' "$ENV_FILE" ||
    fail "controller profile ID is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS=20000' "$ENV_FILE" ||
    fail "controller timeout is missing or stale in $ENV_FILE"
}

if [[ "$MODE" == "check" ]]; then
  verify_installation
  log "Playwright Extension preview controller is current"
  exit 0
fi

log "installing pinned Playwright controller dependencies without browser downloads"
PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm_config_ignore_scripts=true npm_config_audit=false \
  npm ci --prefix "$PACKAGE_DIR" --omit=dev --ignore-scripts

mkdir -p "$runtime_dir" "$profile_dir" "$systemd_dir"
chmod 700 "$runtime_dir" "$profile_dir" "$systemd_dir"

cat >"$unit_path" <<EOF
[Unit]
Description=SparkClaw Playwright Extension preview controller
After=graphical-session.target

[Service]
Type=simple
WorkingDirectory=$PACKAGE_DIR
ExecStart=$(systemd_quote "$node_path") $(systemd_quote "$entry_path")
Restart=on-failure
RestartSec=3
TimeoutStopSec=10
KillMode=control-group
UMask=0077
Environment=$(systemd_quote "SPARKCLAW_BROWSER_CONTROLLER_SOCKET=$socket_path")
Environment=$(systemd_quote "SPARKCLAW_BROWSER_PROFILE_ID=default")
Environment=$(systemd_quote "SPARKCLAW_BROWSER_CHANNEL=chromium")
Environment=$(systemd_quote "SPARKCLAW_BROWSER_EXECUTABLE=$browser_executable")
Environment=$(systemd_quote "SPARKCLAW_BROWSER_USER_DATA_DIR=$profile_dir")

[Install]
WantedBy=default.target
EOF
chmod 600 "$unit_path"

set_env_value SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST "$runtime_dir"
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET "$container_socket"
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST "$socket_path"
set_env_value SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID default
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS 20000

systemctl --user daemon-reload
systemctl --user enable sparkclaw-browser-controller.service
systemctl --user restart sparkclaw-browser-controller.service
for _ in $(seq 1 50); do
  [[ -S "$socket_path" ]] && break
  sleep 0.2
done
verify_installation
log "controller ready; qualification profile: $profile_dir"
log "Host-CDP remains the production browser backend"
