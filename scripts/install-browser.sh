#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

ARTIFACT_MANIFEST="$ROOT/configs/host-browser-artifacts.json"
BRIDGE_MANIFEST="$ROOT/configs/browser-bridge-artifacts.json"
ENV_FILE="${SPARKCLAW_BROWSER_ENV_FILE:-$ROOT/.env.local}"
MODE="install"
TEMP_PATHS=()

usage() {
  cat <<'EOF'
Usage: bash scripts/install-browser.sh [--check] [--env-file PATH]

Install or verify the fixed SparkClaw Chromium artifact, Browser Bridge, owner
graphical service, desktop launcher, and persistent default profile.

Options:
  --check          Verify the existing installation without changing the host
  --env-file PATH  Write/read controller deployment paths in this env file
  -h, --help       Show this help
EOF
}

log() { printf '[sparkclaw-browser] %s\n' "$*"; }
fail() { printf '[sparkclaw-browser] error: %s\n' "$*" >&2; exit 1; }
cleanup() {
  local item
  for item in "${TEMP_PATHS[@]}"; do
    [[ -e "$item" ]] || continue
    [[ -d "$item" ]] && rm -rf -- "$item" || rm -f -- "$item"
  done
}
trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) MODE="check" ;;
    --env-file) shift; [[ $# -gt 0 ]] || fail "--env-file requires a path"; ENV_FILE="$1" ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
  shift
done

[[ "$(uname -s)" == "Linux" ]] || fail "SparkClaw Browser supports Linux hosts only"
[[ "$EUID" -ne 0 ]] || fail "run as the desktop owner, not root"
for command_name in python3 systemctl curl file ldd; do
  command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done
[[ -r "$ARTIFACT_MANIFEST" && -r "$BRIDGE_MANIFEST" ]] || fail "browser artifact metadata is missing"
[[ -f "$ENV_FILE" ]] || fail "environment file not found: $ENV_FILE"

case "$(uname -m)" in
  aarch64|arm64) ARCH="arm64" ;;
  x86_64|amd64) ARCH="x86_64" ;;
  *) fail "unsupported host architecture: $(uname -m)" ;;
esac
mapfile -t artifact_values < <(python3 "$ROOT/scripts/host_browser_artifacts.py" "$ARTIFACT_MANIFEST" "$ARCH")
(( ${#artifact_values[@]} == 5 )) || fail "host browser artifact manifest is invalid"
PINNED_VERSION="${artifact_values[0]}"
ARCHIVE_URL="${artifact_values[1]}"
ARCHIVE_SHA256="${artifact_values[2]}"
ARCHIVE_ROOT="${artifact_values[3]}"
INSTALL_ROOT="/opt/sparkclaw/chromium-${PINNED_VERSION}"

BRIDGE_VERSION="$(python3 "$ROOT/scripts/browser_bridge_artifacts.py" "$BRIDGE_MANIFEST" "$ROOT/tools/browser-bridge" --print-field version)" || fail "Browser Bridge source is invalid"
BRIDGE_SHA256="$(python3 "$ROOT/scripts/browser_bridge_artifacts.py" "$BRIDGE_MANIFEST" "$ROOT/tools/browser-bridge" --print-field sourceSHA256)" || fail "Browser Bridge source is invalid"
BRIDGE_ROOT="/opt/sparkclaw/browser-bridge-${BRIDGE_VERSION}-${BRIDGE_SHA256:0:12}"
runtime_base="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
controller_runtime_dir="$runtime_base/sparkclaw/browser-controller"
profile_dir="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/default/user-data"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/sparkclaw"
config_path="$config_dir/browser.json"
systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
unit_path="$systemd_dir/sparkclaw-browser.service"
applications_dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
desktop_path="$applications_dir/sparkclaw-browser.desktop"
browser_data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser"
browser_bin_dir="$browser_data_dir/bin"
launcher="$browser_bin_dir/sparkclaw-browser"
resolver="$browser_bin_dir/resolve-browser-display.sh"
BROWSER_VERSION_TEXT=""

set_env_value() {
  local key="$1" value="$2" temporary line found=false
  temporary="$(mktemp "${ENV_FILE}.browser.XXXXXX")"; TEMP_PATHS+=("$temporary")
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == "$key="* ]]; then
      if [[ "$found" == false ]]; then printf '%s=%s\n' "$key" "$value" >>"$temporary"; found=true; fi
    else
      printf '%s\n' "$line" >>"$temporary"
    fi
  done <"$ENV_FILE"
  [[ "$found" == true ]] || printf '%s=%s\n' "$key" "$value" >>"$temporary"
  chmod 600 "$temporary"; mv -f -- "$temporary" "$ENV_FILE"
}

remove_retired_env_values() {
  local temporary line
  temporary="$(mktemp "${ENV_FILE}.browser.XXXXXX")"; TEMP_PATHS+=("$temporary")
  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      SPARKCLAW_BROWSER_AUTOMATION_COMMAND=*|SPARKCLAW_BROWSER_AUTOMATION_TRANSPORT=*|SPARKCLAW_BROWSER_CDP_*=*) ;;
      *) printf '%s\n' "$line" >>"$temporary" ;;
    esac
  done <"$ENV_FILE"
  chmod 600 "$temporary"; mv -f -- "$temporary" "$ENV_FILE"
}

verify_browser() {
  local architecture
  [[ -x "$INSTALL_ROOT/chrome" ]] || fail "pinned browser is missing: $INSTALL_ROOT/chrome"
  [[ -f "$INSTALL_ROOT/chrome_sandbox" && ! -L "$INSTALL_ROOT/chrome_sandbox" && "$(stat -c '%u:%a' "$INSTALL_ROOT/chrome_sandbox")" == "0:4755" ]] ||
    fail "pinned browser sandbox is unavailable or unsafe"
  BROWSER_VERSION_TEXT="$("$INSTALL_ROOT/chrome" --version | sed 's/[[:space:]]*$//')"
  [[ "$BROWSER_VERSION_TEXT" =~ ^(Chromium|Google\ Chrome\ for\ Testing)[[:space:]][0-9]+\.[0-9]+\.[0-9]+\.[0-9]+ ]] || fail "unexpected browser version: $BROWSER_VERSION_TEXT"
  architecture="$(file -Lb "$INSTALL_ROOT/chrome")"
  case "$ARCH" in
    arm64) [[ "$architecture" == *"ARM aarch64"* ]] || fail "pinned browser is not ARM64" ;;
    x86_64) [[ "$architecture" == *"x86-64"* ]] || fail "pinned browser is not x86-64" ;;
  esac
  ldd "$INSTALL_ROOT/chrome" 2>&1 | grep -Fq 'not found' && fail "pinned browser has unresolved host libraries"
  command -v fc-list >/dev/null 2>&1 || fail "fontconfig is required for host Chromium"
  [[ -n "$(fc-list ':lang=zh-cn' family 2>/dev/null || true)" ]] || fail "host Chromium requires an installed CJK font"
  fc-match 'Noto Color Emoji' | grep -Fq 'NotoColorEmoji' || fail "host Chromium requires Noto Color Emoji"
  python3 - "$INSTALL_ROOT/sparkclaw-manifest.json" "$PINNED_VERSION" "$ARCHIVE_SHA256" "$ARCH" "$BROWSER_VERSION_TEXT" <<'PY'
import json
from pathlib import Path
import sys
path, version, archive_sha256, architecture, executable_version = sys.argv[1:]
value = json.loads(Path(path).read_text(encoding="utf-8"))
expected = {"artifactVersion": version, "archiveSHA256": archive_sha256, "architecture": architecture, "executableVersion": executable_version}
if any(value.get(key) != item for key, item in expected.items() if key != "executableVersion"):
    raise SystemExit("browser install manifest does not match the pinned artifact")
if not isinstance(value.get("executableVersion"), str) or value["executableVersion"].strip() != executable_version:
    raise SystemExit("browser install manifest does not match the pinned artifact")
PY
}

verify_runtime() {
  bash "$ROOT/scripts/install-browser-bridge.sh" --check >/dev/null
  bash "$ROOT/scripts/resolve-browser-display.sh" >/dev/null || fail "an active owner X11/XWayland session is required"
  for item in "$profile_dir" "$controller_runtime_dir"; do
    [[ -d "$item" && ! -L "$item" && "$(stat -c '%u:%a' "$item")" == "$(id -u):700" ]] || fail "owner-only browser directory is missing or unsafe: $item"
  done
  [[ -x "$launcher" && -x "$resolver" && -r "$config_path" && -r "$unit_path" && -r "$desktop_path" ]] || fail "browser runtime files are incomplete"
  cmp -s "$ROOT/scripts/sparkclaw-browser-launcher.sh" "$launcher" || fail "installed browser launcher is stale"
  cmp -s "$ROOT/scripts/resolve-browser-display.sh" "$resolver" || fail "installed display resolver is stale"
  python3 - "$config_path" "$INSTALL_ROOT/chrome" "$profile_dir" "$BRIDGE_ROOT" "$BROWSER_VERSION_TEXT" "$BRIDGE_VERSION" <<'PY'
import json
from pathlib import Path
import sys
path, executable, profile, bridge, browser_version, bridge_version = sys.argv[1:]
value = json.loads(Path(path).read_text(encoding="utf-8"))
expected = {"version": 1, "executable": executable, "profileDir": profile, "bridgeDir": bridge, "browserVersion": browser_version, "bridgeVersion": bridge_version}
if value != expected:
    raise SystemExit("SparkClaw browser config is stale")
PY
  grep -Fqx "ExecStart=$launcher serve" "$unit_path" || fail "browser service is stale"
  grep -Fqx "Exec=$launcher open" "$desktop_path" || fail "browser desktop launcher is stale"
  systemctl --user is-active --quiet sparkclaw-browser.service || fail "sparkclaw-browser is not active"
  main_pid="$(systemctl --user show --property MainPID --value sparkclaw-browser.service)"
  [[ "$main_pid" =~ ^[1-9][0-9]*$ && -r "/proc/$main_pid/cmdline" ]] || fail "SparkClaw browser PID is unavailable"
  python3 - "/proc/$main_pid/cmdline" "$INSTALL_ROOT/chrome" "$profile_dir" "$BRIDGE_ROOT" <<'PY'
from pathlib import Path
import sys
parts = [item.decode("utf-8") for item in Path(sys.argv[1]).read_bytes().split(b"\0") if item]
command_line = " ".join(parts)

def contains_argument(value: str) -> bool:
    start = 0
    while True:
        index = command_line.find(value, start)
        if index < 0:
            return False
        end = index + len(value)
        if (index == 0 or command_line[index - 1] == " ") and (end == len(command_line) or command_line[end] == " "):
            return True
        start = index + 1

required = {sys.argv[2], f"--user-data-dir={sys.argv[3]}", f"--disable-extensions-except={sys.argv[4]}", f"--load-extension={sys.argv[4]}"}
if not all(contains_argument(value) for value in required):
    raise SystemExit("SparkClaw browser command line is stale")
tokens = command_line.split()
if any(item == "--enable-automation" or item.startswith("--remote-debugging-") or item == "--headless" or item.startswith("--headless=") for item in tokens):
    raise SystemExit("SparkClaw browser command line contains forbidden automation flags")
PY
  grep -Fxq "SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST=$controller_runtime_dir" "$ENV_FILE" || fail "controller runtime mapping is stale"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET=/run/sparkclaw/browser-controller/controller.sock' "$ENV_FILE" || fail "controller socket mapping is stale"
  grep -Fxq 'SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID=default' "$ENV_FILE" || fail "browser profile identity is stale"
  if grep -Eq '^(SPARKCLAW_BROWSER_AUTOMATION_COMMAND|SPARKCLAW_BROWSER_AUTOMATION_TRANSPORT|SPARKCLAW_BROWSER_CDP_)=' "$ENV_FILE"; then
    fail "retired Host-CDP configuration remains in $ENV_FILE"
  fi
}

if [[ "$MODE" == "check" ]]; then
  verify_browser
  verify_runtime
  log "SparkClaw Browser is current ($PINNED_VERSION, Bridge $BRIDGE_VERSION)"
  exit 0
fi

command -v sudo >/dev/null 2>&1 || fail "sudo is required to install the host browser"
sudo -n env true >/dev/null 2>&1 || sudo -v
audio_package="libasound2"; apt-cache show libasound2t64 >/dev/null 2>&1 && audio_package="libasound2t64"
log "installing host browser runtime libraries"
sudo env DEBIAN_FRONTEND=noninteractive apt-get update
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
  ca-certificates curl file python3 unzip fonts-noto-cjk fonts-noto-color-emoji "$audio_package" \
  libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 libcairo2 libcups2 libdbus-1-3 libdrm2 libgbm1 \
  libglib2.0-0 libnspr4 libnss3 libpango-1.0-0 libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 \
  libxfixes3 libxkbcommon0 libxrandr2 libxshmfence1

if [[ ! -x "$INSTALL_ROOT/chrome" || ! -r "$INSTALL_ROOT/sparkclaw-manifest.json" ]]; then
  stage="$(mktemp -d "${TMPDIR:-/tmp}/sparkclaw-browser-install.XXXXXX")"; TEMP_PATHS+=("$stage")
  archive="$stage/chromium.zip"
  log "downloading pinned SparkClaw browser $PINNED_VERSION for $ARCH"
  curl -fL --retry 3 --connect-timeout 15 --max-time 1800 -o "$archive" "$ARCHIVE_URL"
  printf '%s  %s\n' "$ARCHIVE_SHA256" "$archive" | sha256sum -c -
  unzip -Z1 "$archive" | grep -Eq '(^/|(^|/)\.\.(/|$))' && fail "browser archive contains an unsafe path"
  unzip -q "$archive" -d "$stage/unpacked"
  [[ -x "$stage/unpacked/$ARCHIVE_ROOT/chrome" ]] || fail "browser archive is incomplete"
  [[ ! -e "$INSTALL_ROOT" ]] || sudo rm -rf -- "$INSTALL_ROOT"
  sudo install -d -m 0755 "$INSTALL_ROOT"
  sudo cp -a "$stage/unpacked/$ARCHIVE_ROOT/." "$INSTALL_ROOT/"
  sudo chmod 0755 "$INSTALL_ROOT/chrome"
  sudo chown root:root "$INSTALL_ROOT/chrome_sandbox"; sudo chmod 4755 "$INSTALL_ROOT/chrome_sandbox"
  installed_version="$("$INSTALL_ROOT/chrome" --version | sed 's/[[:space:]]*$//')"
  python3 - "$PINNED_VERSION" "${artifact_values[4]}" "$ARCHIVE_SHA256" "$ARCH" "$installed_version" <<'PY' | sudo tee "$INSTALL_ROOT/sparkclaw-manifest.json" >/dev/null
import json
import sys
artifact_version, playwright_build, archive_sha256, architecture, executable_version = sys.argv[1:]
print(json.dumps({"artifactVersion": artifact_version, "playwrightBuild": playwright_build, "archiveSHA256": archive_sha256, "architecture": architecture, "executableVersion": executable_version}, sort_keys=True))
PY
  sudo chmod 0644 "$INSTALL_ROOT/sparkclaw-manifest.json"
fi
verify_browser
bash "$ROOT/scripts/install-browser-bridge.sh"
bash "$ROOT/scripts/resolve-browser-display.sh" >/dev/null || fail "an active owner X11/XWayland session is required"

mkdir -p "$controller_runtime_dir" "$profile_dir" "$config_dir" "$systemd_dir" "$applications_dir" "$browser_bin_dir"
chmod 700 "$controller_runtime_dir" "$profile_dir" "$config_dir" "$browser_data_dir" "$browser_bin_dir"
install -m 700 "$ROOT/scripts/sparkclaw-browser-launcher.sh" "$launcher"
install -m 700 "$ROOT/scripts/resolve-browser-display.sh" "$resolver"
python3 - "$config_path" "$INSTALL_ROOT/chrome" "$profile_dir" "$BRIDGE_ROOT" "$BROWSER_VERSION_TEXT" "$BRIDGE_VERSION" <<'PY'
import json
from pathlib import Path
import sys
path, executable, profile, bridge, browser_version, bridge_version = sys.argv[1:]
target = Path(path)
temporary = target.with_name(f".{target.name}.tmp")
temporary.write_text(json.dumps({"version": 1, "executable": executable, "profileDir": profile, "bridgeDir": bridge, "browserVersion": browser_version, "bridgeVersion": bridge_version}, sort_keys=True) + "\n", encoding="utf-8")
temporary.chmod(0o600); temporary.replace(target)
PY
cat >"$unit_path" <<EOF
[Unit]
Description=SparkClaw Browser
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=$launcher serve
Restart=on-failure
RestartSec=3
TimeoutStopSec=15
KillMode=control-group
UMask=0077

[Install]
WantedBy=graphical-session.target
EOF
chmod 600 "$unit_path"
cat >"$desktop_path" <<EOF
[Desktop Entry]
Type=Application
Name=SparkClaw Browser
Comment=Open the persistent SparkClaw browser profile
Exec=$launcher open
Icon=web-browser
Terminal=false
Categories=Network;WebBrowser;
StartupNotify=true
EOF
chmod 644 "$desktop_path"

remove_retired_env_values
set_env_value SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST "$controller_runtime_dir"
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET /run/sparkclaw/browser-controller/controller.sock
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST "$controller_runtime_dir/controller.sock"
set_env_value SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID default
set_env_value SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS 20000
systemctl --user daemon-reload
systemctl --user enable sparkclaw-browser.service
log "browser runtime installed; controller setup will start the persistent profile"
