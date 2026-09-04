#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ARTIFACT_MANIFEST="$ROOT/configs/host-browser-artifacts.json"
BROWSERD_ROOT="/opt/sparkclaw/browserd"
BROWSERD_BIN="$BROWSERD_ROOT/sparkclaw-browserd"
ENV_FILE="$ROOT/.env.local"
MODE="install"
TEMP_PATHS=()

usage() {
  cat <<'EOF'
Usage: bash scripts/install-host-browser.sh [--check] [--env-file PATH]

Install or verify the pinned SparkClaw host browser, browserd user service,
desktop launcher, dedicated profile, and capability endpoint.

Options:
  --check          Verify the existing installation without changing the host
  --env-file PATH  Write/read Host-CDP deployment paths in this env file
  -h, --help       Show this help
EOF
}

log() {
  printf '[sparkclaw-browser] %s\n' "$*"
}

fail() {
  printf '[sparkclaw-browser] error: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  local path
  for path in "${TEMP_PATHS[@]}"; do
    [[ -n "$path" && -e "$path" ]] || continue
    if [[ -d "$path" ]]; then
      rm -rf -- "$path"
    else
      rm -f -- "$path"
    fi
  done
}

trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      MODE="check"
      ;;
    --env-file)
      shift
      [[ $# -gt 0 ]] || fail "--env-file requires a path"
      ENV_FILE="$1"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      fail "unknown argument: $1"
      ;;
  esac
  shift
done

[[ "$(uname -s)" == "Linux" ]] || fail "Host-CDP supports Linux hosts only"
[[ "$EUID" -ne 0 ]] || fail "run as the desktop owner, not root"
command -v python3 >/dev/null 2>&1 || fail "python3 is required"
command -v systemctl >/dev/null 2>&1 || fail "systemd is required"
[[ -r "$ARTIFACT_MANIFEST" ]] || fail "host browser artifact manifest is missing"

case "$(uname -m)" in
  aarch64|arm64)
    ARCH="arm64"
    ;;
  x86_64|amd64)
    ARCH="x86_64"
    ;;
  *)
    fail "unsupported host architecture: $(uname -m)"
    ;;
esac

mapfile -t artifact_values < <(
  python3 "$ROOT/scripts/host_browser_artifacts.py" "$ARTIFACT_MANIFEST" "$ARCH"
)
(( ${#artifact_values[@]} == 5 )) || fail "host browser artifact manifest is invalid"
PINNED_VERSION="${artifact_values[0]}"
ARCHIVE_URL="${artifact_values[1]}"
ARCHIVE_SHA256="${artifact_values[2]}"
ARCHIVE_ROOT="${artifact_values[3]}"
PINNED_PLAYWRIGHT_BUILD="${artifact_values[4]}"
[[ -n "$PINNED_VERSION" && -n "$ARCHIVE_URL" && -n "$ARCHIVE_SHA256" && -n "$ARCHIVE_ROOT" ]] ||
  fail "host browser artifact manifest has empty required fields"
INSTALL_ROOT="/opt/sparkclaw/chromium-${PINNED_VERSION}"

runtime_base="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
runtime_dir="$runtime_base/sparkclaw/browserd"
controller_runtime_dir="$runtime_base/sparkclaw/browser-controller"
profile_dir="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/default/user-data"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/sparkclaw"
config_path="$config_dir/browserd.json"
systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
unit_path="$systemd_dir/sparkclaw-browserd.service"
applications_dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
desktop_path="$applications_dir/sparkclaw-browser.desktop"
endpoint_file="$runtime_dir/cdp-endpoint"
BROWSER_VERSION_TEXT=""

set_env_value() {
  local key="$1"
  local value="$2"
  local temporary
  local found=false
  local line

  [[ -f "$ENV_FILE" ]] || fail "environment file not found: $ENV_FILE"
  temporary="$(mktemp "${ENV_FILE}.browser.XXXXXX")"
  TEMP_PATHS+=("$temporary")
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

browser_install_current() {
  local output

  [[ -x "$INSTALL_ROOT/chrome" && -r "$INSTALL_ROOT/sparkclaw-manifest.json" ]] || return 1
  output="$("$INSTALL_ROOT/chrome" --version 2>/dev/null)" || return 1
  output="$(printf '%s' "$output" | sed 's/[[:space:]]*$//')"
  python3 - "$INSTALL_ROOT/sparkclaw-manifest.json" "$PINNED_VERSION" "$ARCHIVE_SHA256" "$ARCH" "$output" <<'PY'
import json
import sys

path, version, archive_sha256, architecture, executable_version = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as stream:
        manifest = json.load(stream)
except (OSError, ValueError):
    raise SystemExit(1)
expected = {
    "artifactVersion": version,
    "archiveSHA256": archive_sha256,
    "architecture": architecture,
    "executableVersion": executable_version,
}
if str(manifest.get("executableVersion", "")).strip() != executable_version:
    raise SystemExit(1)
if any(manifest.get(key) != value for key, value in expected.items() if key != "executableVersion"):
    raise SystemExit(1)
PY
}

verify_browser() {
  local output architecture
  [[ -x "$INSTALL_ROOT/chrome" ]] || fail "pinned browser is missing: $INSTALL_ROOT/chrome"
  [[ ! -L "$INSTALL_ROOT/chrome_sandbox" && -f "$INSTALL_ROOT/chrome_sandbox" ]] ||
    fail "pinned browser sandbox is missing or unsafe"
  [[ "$(stat -c '%u:%a' "$INSTALL_ROOT/chrome_sandbox")" == "0:4755" ]] ||
    fail "pinned browser sandbox must be root-owned and mode 4755"
  output="$("$INSTALL_ROOT/chrome" --version)"
  output="$(printf '%s' "$output" | sed 's/[[:space:]]*$//')"
  [[ "$output" =~ ^(Chromium|Google\ Chrome\ for\ Testing)[[:space:]][0-9]+\.[0-9]+\.[0-9]+\.[0-9]+ ]] ||
    fail "unexpected browser version: $output"
  BROWSER_VERSION_TEXT="$output"
  architecture="$(file -Lb "$INSTALL_ROOT/chrome")"
  case "$ARCH" in
    arm64) [[ "$architecture" == *"ARM aarch64"* ]] || fail "pinned browser is not ARM64: $architecture" ;;
    x86_64) [[ "$architecture" == *"x86-64"* ]] || fail "pinned browser is not x86-64: $architecture" ;;
  esac
  if ldd "$INSTALL_ROOT/chrome" 2>&1 | grep -Fq 'not found'; then
    ldd "$INSTALL_ROOT/chrome" >&2 || true
    fail "pinned browser has unresolved host libraries"
  fi
  command -v fc-list >/dev/null 2>&1 || fail "fontconfig is required for host Chromium"
  [[ -n "$(fc-list ':lang=zh-cn' family 2>/dev/null || true)" ]] ||
    fail "host Chromium requires an installed CJK font"
  fc-match 'Noto Color Emoji' | grep -Fq 'NotoColorEmoji' ||
    fail "host Chromium requires Noto Color Emoji"
  [[ -r "$INSTALL_ROOT/sparkclaw-manifest.json" ]] || fail "browser install manifest is missing"
  python3 - "$INSTALL_ROOT/sparkclaw-manifest.json" "$PINNED_VERSION" "$ARCHIVE_SHA256" "$ARCH" "$output" <<'PY'
import json
import sys

path, version, archive_sha256, architecture, executable_version = sys.argv[1:]
with open(path, encoding="utf-8") as stream:
    manifest = json.load(stream)
if (
    manifest.get("artifactVersion") != version
    or manifest.get("archiveSHA256") != archive_sha256
    or manifest.get("architecture") != architecture
    or str(manifest.get("executableVersion", "")).strip() != executable_version
):
    raise SystemExit("browser install manifest does not match the pinned artifact")
PY
}

verify_service_files() {
  local endpoint_owner
  [[ -x "$BROWSERD_BIN" ]] || fail "sparkclaw-browserd is not installed"
  cmp -s scripts/sparkclaw_browserd.py "$BROWSERD_BIN" || fail "installed sparkclaw-browserd is stale"
  [[ -r "$config_path" ]] || fail "browserd config is missing: $config_path"
  [[ -r "$unit_path" ]] || fail "browserd user unit is missing: $unit_path"
  [[ -r "$desktop_path" ]] || fail "SparkClaw Browser desktop launcher is missing"
  [[ "$(stat -c '%a' "$config_path")" == "600" ]] || fail "browserd config must be mode 0600"
  [[ "$(stat -c '%a' "$runtime_dir")" == "700" ]] || fail "browserd runtime directory must be mode 0700"
  [[ -d "$controller_runtime_dir" && ! -L "$controller_runtime_dir" ]] || fail "browser controller runtime directory is missing or unsafe"
  [[ "$(stat -c '%u:%a' "$controller_runtime_dir")" == "$(id -u):700" ]] || fail "browser controller runtime directory must be owner-only"
  [[ "$(stat -c '%a' "$profile_dir")" == "700" ]] || fail "browser profile directory must be mode 0700"
  [[ ! -L "$endpoint_file" && -f "$endpoint_file" ]] || fail "browserd endpoint must be a regular non-symlink file"
  [[ "$(stat -c '%a' "$endpoint_file")" == "600" ]] || fail "browserd endpoint must be mode 0600"
  endpoint_owner="$(stat -c '%u' "$endpoint_file")"
  [[ "$endpoint_owner" == "$(id -u)" ]] || fail "browserd endpoint must be owned by $(id -un)"
  python3 - \
    "$config_path" "$unit_path" "$desktop_path" "$endpoint_file" \
    "$INSTALL_ROOT/chrome" "$profile_dir" "$runtime_dir" "$BROWSERD_BIN" \
    "$BROWSER_VERSION_TEXT" <<'PY'
import json
from pathlib import Path
import sys

(
    config_path,
    unit_path,
    desktop_path,
    endpoint_path,
    executable,
    profile_dir,
    runtime_dir,
    browserd_bin,
    browser_version,
) = sys.argv[1:]
with open(config_path, encoding="utf-8") as stream:
    config = json.load(stream)
expected = {
    "version": 1,
    "executable": executable,
    "profileDir": profile_dir,
    "runtimeDir": runtime_dir,
    "profileID": "default",
    "browserVersion": browser_version,
    "proxyPort": 18791,
}
for key, value in expected.items():
    if config.get(key) != value:
        raise SystemExit(f"browserd config has stale {key}")
if set(config) != set(expected) | {"display", "xauthority"}:
    raise SystemExit("browserd config fields do not match the installed schema")

unit = Path(unit_path).read_text(encoding="utf-8")
if f"ExecStart={browserd_bin} --config {config_path} serve\n" not in unit:
    raise SystemExit("browserd user unit has a stale ExecStart")
if "KillMode=control-group\n" not in unit or "WantedBy=default.target\n" not in unit:
    raise SystemExit("browserd user unit is incomplete")

desktop = Path(desktop_path).read_text(encoding="utf-8")
if f"Exec={browserd_bin} --config {config_path} open-or-focus\n" not in desktop:
    raise SystemExit("SparkClaw Browser launcher has a stale Exec")

with open(endpoint_path, encoding="utf-8") as stream:
    endpoint = json.load(stream)
if endpoint.get("version") != 1 or endpoint.get("profileID") != "default":
    raise SystemExit("browserd endpoint identity is invalid")
if endpoint.get("browserVersion") != browser_version:
    raise SystemExit("browserd endpoint version is stale")
if endpoint.get("presentation") not in {"headed", "headless"}:
    raise SystemExit("browserd endpoint presentation is invalid")
if not isinstance(endpoint.get("browserPID"), int) or endpoint["browserPID"] <= 0:
    raise SystemExit("browserd endpoint PID is invalid")
PY
  systemctl --user is-active --quiet sparkclaw-browserd.service || fail "sparkclaw-browserd is not active"
  "$BROWSERD_BIN" --config "$config_path" status >/dev/null
}

if [[ "$MODE" == "check" ]]; then
  verify_browser
  verify_service_files
  [[ -f "$ENV_FILE" ]] || fail "environment file not found: $ENV_FILE"
  grep -Fxq "SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST=$runtime_dir" "$ENV_FILE" ||
    fail "Host-CDP runtime directory is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE=/run/sparkclaw/browserd/cdp-endpoint' "$ENV_FILE" ||
    fail "Host-CDP container endpoint is missing or stale in $ENV_FILE"
  grep -Fxq "SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST=$endpoint_file" "$ENV_FILE" ||
    fail "Host-CDP host endpoint is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_CDP_PROFILE_ID=default' "$ENV_FILE" ||
    fail "Host-CDP profile ID is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS=10000' "$ENV_FILE" ||
    fail "Host-CDP connect timeout is missing or stale in $ENV_FILE"
  grep -Fxq "SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST=$controller_runtime_dir" "$ENV_FILE" ||
    fail "browser controller runtime directory is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET=/run/sparkclaw/browser-controller/controller.sock' "$ENV_FILE" ||
    fail "browser controller container socket is missing or stale in $ENV_FILE"
  grep -Fxq "SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST=$controller_runtime_dir/controller.sock" "$ENV_FILE" ||
    fail "browser controller host socket is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID=default' "$ENV_FILE" ||
    fail "browser controller profile ID is missing or stale in $ENV_FILE"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS=20000' "$ENV_FILE" ||
    fail "browser controller connect timeout is missing or stale in $ENV_FILE"
  if grep -Eq '^(SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE|SPARKCLAW_BROWSER_PROFILE_DIR|SPARKCLAW_BROWSER_AUTOMATION_DAEMON_IDLE_TIMEOUT_MS|SPARKCLAW_BROWSER_DISPLAY|SPARKCLAW_BROWSER_XAUTHORITY)=' "$ENV_FILE"; then
    fail "legacy container-browser settings remain in $ENV_FILE"
  fi
  log "Host-CDP installation is current ($PINNED_VERSION, $ARCH)"
  exit 0
fi

command -v sudo >/dev/null 2>&1 || fail "sudo is required to install the host browser"
if ! sudo -n env true >/dev/null 2>&1; then
  sudo -v
fi

audio_package="libasound2"
if apt-cache show libasound2t64 >/dev/null 2>&1; then
  audio_package="libasound2t64"
fi
log "installing host browser runtime libraries"
sudo env DEBIAN_FRONTEND=noninteractive apt-get update
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
  ca-certificates curl file python3 unzip fonts-noto-cjk fonts-noto-color-emoji \
  "$audio_package" libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 libcairo2 \
  libcups2 libdbus-1-3 libdrm2 libgbm1 libglib2.0-0 libnspr4 libnss3 \
  libpango-1.0-0 libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 \
  libxfixes3 libxkbcommon0 libxrandr2 libxshmfence1

if ! browser_install_current; then
  stage="$(mktemp -d "${TMPDIR:-/tmp}/sparkclaw-browser-install.XXXXXX")"
  TEMP_PATHS+=("$stage")
  archive="$stage/chromium.zip"
  log "downloading pinned SparkClaw browser $PINNED_VERSION for $ARCH"
  curl -fL --retry 3 --connect-timeout 15 --max-time 1800 -o "$archive" "$ARCHIVE_URL"
  printf '%s  %s\n' "$ARCHIVE_SHA256" "$archive" | sha256sum -c -
  if unzip -Z1 "$archive" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
    fail "browser archive contains an unsafe path"
  fi
  unzip -q "$archive" -d "$stage/unpacked"
  [[ -x "$stage/unpacked/$ARCHIVE_ROOT/chrome" ]] || fail "browser archive is incomplete"
  if [[ -e "$INSTALL_ROOT" ]]; then
    log "replacing incomplete or stale browser installation at $INSTALL_ROOT"
    sudo rm -rf -- "$INSTALL_ROOT"
  fi
  sudo install -d -m 0755 "$INSTALL_ROOT"
  sudo cp -a "$stage/unpacked/$ARCHIVE_ROOT/." "$INSTALL_ROOT/"
  sudo chmod 0755 "$INSTALL_ROOT/chrome"
  for sandbox in chrome_sandbox chrome-sandbox; do
    if [[ -f "$INSTALL_ROOT/$sandbox" && ! -L "$INSTALL_ROOT/$sandbox" ]]; then
      sudo chown root:root "$INSTALL_ROOT/$sandbox"
      sudo chmod 4755 "$INSTALL_ROOT/$sandbox"
    fi
  done
  installed_version="$("$INSTALL_ROOT/chrome" --version)"
  installed_version="$(printf '%s' "$installed_version" | sed 's/[[:space:]]*$//')"
  python3 - "$PINNED_VERSION" "$PINNED_PLAYWRIGHT_BUILD" "$ARCHIVE_SHA256" "$ARCH" "$installed_version" <<'PY' |
import json
import sys

artifact_version, playwright_build, archive_sha256, architecture, executable_version = sys.argv[1:]
print(json.dumps({
    "artifactVersion": artifact_version,
    "playwrightBuild": playwright_build,
    "archiveSHA256": archive_sha256,
    "architecture": architecture,
    "executableVersion": executable_version,
}, sort_keys=True))
PY
    sudo tee "$INSTALL_ROOT/sparkclaw-manifest.json" >/dev/null
  sudo chmod 0644 "$INSTALL_ROOT/sparkclaw-manifest.json"
fi
verify_browser

log "installing sparkclaw-browserd and owner-scoped runtime files"
sudo install -d -m 0755 "$BROWSERD_ROOT"
sudo install -m 0755 scripts/sparkclaw_browserd.py "$BROWSERD_BIN"
mkdir -p "$runtime_dir" "$controller_runtime_dir" "$profile_dir" "$config_dir" "$systemd_dir" "$applications_dir"
chmod 700 "$runtime_dir" "$controller_runtime_dir" "$profile_dir" "$config_dir"

display=""
xauthority=""
if display_info="$(bash scripts/resolve-browser-display.sh 2>/dev/null)"; then
  display="${display_info%%$'\n'*}"
  xauthority="${display_info#*$'\n'}"
fi

SPARKCLAW_BROWSERD_EXECUTABLE="$INSTALL_ROOT/chrome" \
SPARKCLAW_BROWSERD_PROFILE_DIR="$profile_dir" \
SPARKCLAW_BROWSERD_RUNTIME_DIR="$runtime_dir" \
SPARKCLAW_BROWSERD_DISPLAY="$display" \
SPARKCLAW_BROWSERD_XAUTHORITY="$xauthority" \
SPARKCLAW_BROWSERD_VERSION="$BROWSER_VERSION_TEXT" \
python3 - "$config_path" <<'PY'
import json
import os
from pathlib import Path
import sys

path = Path(sys.argv[1])
temporary = path.with_name(f".{path.name}.{os.getpid()}")
value = {
    "version": 1,
    "executable": os.environ["SPARKCLAW_BROWSERD_EXECUTABLE"],
    "profileDir": os.environ["SPARKCLAW_BROWSERD_PROFILE_DIR"],
    "runtimeDir": os.environ["SPARKCLAW_BROWSERD_RUNTIME_DIR"],
    "profileID": "default",
    "browserVersion": os.environ["SPARKCLAW_BROWSERD_VERSION"],
    "proxyPort": 18791,
    "display": os.environ.get("SPARKCLAW_BROWSERD_DISPLAY", ""),
    "xauthority": os.environ.get("SPARKCLAW_BROWSERD_XAUTHORITY", ""),
}
temporary.write_text(json.dumps(value, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(path)
path.chmod(0o600)
PY

cat >"$unit_path" <<EOF
[Unit]
Description=SparkClaw Host Chromium and CDP proxy
After=network.target docker.service

[Service]
Type=simple
ExecStart=$BROWSERD_BIN --config $config_path serve
Restart=on-failure
RestartSec=3
TimeoutStopSec=15
KillMode=control-group
Environment=PYTHONUNBUFFERED=1

[Install]
WantedBy=default.target
EOF
chmod 600 "$unit_path"

cat >"$desktop_path" <<EOF
[Desktop Entry]
Type=Application
Name=SparkClaw Browser
Comment=Open the dedicated SparkClaw browser profile
Exec=$BROWSERD_BIN --config $config_path open-or-focus
Icon=web-browser
Terminal=false
Categories=Network;WebBrowser;
StartupNotify=true
EOF
chmod 644 "$desktop_path"

set_env_value SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST "$runtime_dir"
set_env_value SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE /run/sparkclaw/browserd/cdp-endpoint
set_env_value SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST "$endpoint_file"
set_env_value SPARKCLAW_BROWSER_CDP_PROFILE_ID default
set_env_value SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS 10000
set_env_value SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST "$controller_runtime_dir"
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET /run/sparkclaw/browser-controller/controller.sock
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST "$controller_runtime_dir/controller.sock"
set_env_value SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID default
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS 20000

sudo loginctl enable-linger "$(id -un)"
systemctl --user daemon-reload
systemctl --user enable sparkclaw-browserd.service
systemctl --user restart sparkclaw-browserd.service
for _ in $(seq 1 100); do
  if [[ -s "$endpoint_file" ]] && "$BROWSERD_BIN" --config "$config_path" status >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
verify_service_files
log "Host-CDP browser ready: $PINNED_VERSION ($ARCH)"
log "desktop launcher: SparkClaw Browser"
