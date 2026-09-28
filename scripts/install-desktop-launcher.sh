#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="install"
DESKTOP_EXECUTABLE=""
RUNTIME_DIR="$ROOT/data/runtime"

usage() {
  cat <<'EOF'
Usage: bash scripts/install-desktop-launcher.sh --executable PATH [--runtime-dir PATH] [--check]

Install or verify the current user's SparkClaw desktop launcher. The launcher
passes the absolute local-workbench description and private desktop Client
credential paths to the packaged Electron application.

Options:
  --executable PATH  Installed SparkClaw Electron executable
  --runtime-dir PATH Directory containing local-workbench.json and desktop-client.json
  --check            Verify without changing files
  -h, --help         Show this help
EOF
}

fail() { printf '[sparkclaw-desktop] error: %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) MODE="check" ;;
    --executable) shift; [[ $# -gt 0 ]] || fail "--executable requires a path"; DESKTOP_EXECUTABLE="$1" ;;
    --runtime-dir) shift; [[ $# -gt 0 ]] || fail "--runtime-dir requires a path"; RUNTIME_DIR="$1" ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
  shift
done

[[ "$(uname -s)" == "Linux" ]] || fail "the desktop launcher supports Linux hosts only"
[[ "$EUID" -ne 0 ]] || fail "run as the desktop owner, not root"
[[ "$DESKTOP_EXECUTABLE" == /* && "$RUNTIME_DIR" == /* ]] || fail "executable and runtime directory paths must be absolute"
DESKTOP_EXECUTABLE="$(realpath -e -- "$DESKTOP_EXECUTABLE")"
RUNTIME_DIR="$(realpath -e -- "$RUNTIME_DIR")"
[[ -f "$DESKTOP_EXECUTABLE" && -x "$DESKTOP_EXECUTABLE" ]] || fail "desktop executable is not an executable regular file"

descriptor_path="$RUNTIME_DIR/local-workbench.json"
credential_path="$RUNTIME_DIR/desktop-client.json"
[[ -f "$descriptor_path" && ! -L "$descriptor_path" ]] || fail "local-workbench.json is missing or unsafe"
[[ -f "$credential_path" && ! -L "$credential_path" ]] || fail "desktop-client.json is missing or unsafe"
[[ "$(stat -c '%u:%a' "$RUNTIME_DIR")" == "$(id -u):700" ]] || fail "runtime directory must be owner-only (0700)"
[[ "$(stat -c '%u:%a' "$credential_path")" == "$(id -u):600" ]] || fail "desktop-client.json must be owner-only (0600)"

data_home="${XDG_DATA_HOME:-${HOME}/.local/share}"
config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
launcher_dir="$data_home/sparkclaw/desktop/bin"
launcher_path="$launcher_dir/sparkclaw-desktop"
applications_dir="$data_home/applications"
desktop_path="$applications_dir/sparkclaw.desktop"
icon_source="$ROOT/apps/desktop/src/assets/icon.png"
icon_dir="$data_home/icons/hicolor/512x512/apps"
icon_path="$icon_dir/sparkclaw.png"
config_dir="$config_home/sparkclaw"
config_path="$config_dir/desktop-launcher.conf"
launcher_source="$ROOT/scripts/sparkclaw-desktop-launcher.sh"

expected_config="$(printf 'executable=%s\ndescriptor=%s\ncredential=%s\n' "$DESKTOP_EXECUTABLE" "$descriptor_path" "$credential_path")"

verify() {
  [[ -x "$launcher_path" && -f "$config_path" && -f "$desktop_path" && -f "$icon_path" ]] || fail "desktop launcher installation is incomplete"
  cmp -s "$launcher_source" "$launcher_path" || fail "installed desktop launcher is stale"
  cmp -s "$icon_source" "$icon_path" || fail "installed desktop icon is stale"
  [[ "$(cat "$config_path")" == "$expected_config" ]] || fail "desktop launcher configuration is stale"
  grep -Fqx "TryExec=$launcher_path" "$desktop_path" || fail "desktop entry TryExec is stale"
  grep -Fqx "Exec=$launcher_path" "$desktop_path" || fail "desktop entry Exec is stale"
  grep -Fqx "Icon=sparkclaw" "$desktop_path" || fail "desktop entry icon is stale"
  [[ "$(stat -c '%u:%a' "$config_path")" == "$(id -u):600" ]] || fail "desktop launcher configuration must be mode 0600"
}

if [[ "$MODE" == "check" ]]; then
  verify
  printf 'SparkClaw desktop launcher is valid: %s\n' "$launcher_path"
  exit 0
fi

mkdir -p "$launcher_dir" "$applications_dir" "$icon_dir" "$config_dir"
chmod 700 "$launcher_dir" "$config_dir"
install -m 700 "$launcher_source" "$launcher_path"
install -m 644 "$icon_source" "$icon_path"
temporary_config="$(mktemp "$config_dir/desktop-launcher.conf.XXXXXX")"
printf '%s\n' "$expected_config" >"$temporary_config"
chmod 600 "$temporary_config"
mv -f -- "$temporary_config" "$config_path"
temporary_desktop="$(mktemp "$applications_dir/sparkclaw.desktop.XXXXXX")"
cat >"$temporary_desktop" <<EOF
[Desktop Entry]
Type=Application
Name=SparkClaw
Comment=SparkClaw local workbench
Exec=$launcher_path
TryExec=$launcher_path
Icon=sparkclaw
Terminal=false
Categories=Utility;
StartupNotify=true
EOF
chmod 644 "$temporary_desktop"
mv -f -- "$temporary_desktop" "$desktop_path"
verify
printf 'Installed SparkClaw desktop launcher: %s\n' "$launcher_path"
