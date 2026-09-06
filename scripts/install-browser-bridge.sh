#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$ROOT/configs/browser-bridge-artifacts.json"
SOURCE_DIR="$ROOT/tools/browser-bridge"
MODE="install"

usage() {
  cat <<'EOF'
Usage: bash scripts/install-browser-bridge.sh [--check]

Install or verify the checksum-pinned SparkClaw Browser Bridge in its immutable
host directory. This command never reads or writes the browser control token.

Options:
  --check     Verify the source and installed package without changing the host
  -h, --help  Show this help
EOF
}

log() {
  printf '[sparkclaw-browser-bridge] %s\n' "$*"
}

fail() {
  printf '[sparkclaw-browser-bridge] error: %s\n' "$*" >&2
  exit 1
}

case "${1:-}" in
  "") ;;
  --check) MODE="check" ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; fail "unknown argument: $1" ;;
esac
(( $# <= 1 )) || { usage >&2; fail "too many arguments"; }

[[ "$(uname -s)" == "Linux" ]] || fail "the Browser Bridge supports Linux hosts only"
[[ "$EUID" -ne 0 ]] || fail "run as the desktop owner, not root"
command -v python3 >/dev/null 2>&1 || fail "python3 is required"
[[ -r "$MANIFEST" ]] || fail "browser bridge artifact manifest is missing"

version="$(python3 "$ROOT/scripts/browser_bridge_artifacts.py" "$MANIFEST" "$SOURCE_DIR" --print-field version)" ||
  fail "browser bridge source verification failed"
source_sha256="$(python3 "$ROOT/scripts/browser_bridge_artifacts.py" "$MANIFEST" "$SOURCE_DIR" --print-field sourceSHA256)" ||
  fail "browser bridge source verification failed"
install_root="/opt/sparkclaw/browser-bridge-${version}-${source_sha256:0:12}"

verify_installation() {
  [[ -d "$install_root" && ! -L "$install_root" ]] || fail "browser bridge installation is missing: $install_root"
  [[ "$(stat -c '%u:%a' "$install_root")" == "0:755" ]] ||
    fail "browser bridge install root must be root-owned and mode 0755"
  python3 "$ROOT/scripts/browser_bridge_artifacts.py" "$MANIFEST" "$install_root" --installed ||
    fail "installed browser bridge verification failed"
}

if [[ "$MODE" == "check" ]]; then
  verify_installation
  log "Browser Bridge $version is current (sha256:$source_sha256)"
  exit 0
fi

command -v sudo >/dev/null 2>&1 || fail "sudo is required to install the Browser Bridge"
if [[ -e "$install_root" ]]; then
  verify_installation
  log "Browser Bridge $version is already installed"
  exit 0
fi

stage="$(mktemp -d "${TMPDIR:-/tmp}/sparkclaw-browser-bridge.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT
python3 - "$MANIFEST" "$SOURCE_DIR" "$stage" <<'PY'
import json
from pathlib import Path
import shutil
import sys

manifest_path, source_value, stage_value = sys.argv[1:]
manifest = json.loads(Path(manifest_path).read_text(encoding="utf-8"))
source = Path(source_value)
stage = Path(stage_value)
for relative in sorted(manifest["files"]):
    target = stage / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source / relative, target)
PY
python3 "$ROOT/scripts/browser_bridge_artifacts.py" "$MANIFEST" "$stage" ||
  fail "staged browser bridge verification failed"

sudo install -d -o root -g root -m 0755 "$install_root"
sudo cp -a "$stage/." "$install_root/"
sudo chown -R root:root "$install_root"
sudo find "$install_root" -type d -exec chmod 0755 {} +
sudo find "$install_root" -type f -exec chmod 0644 {} +
verify_installation
log "installed Browser Bridge $version at $install_root"
