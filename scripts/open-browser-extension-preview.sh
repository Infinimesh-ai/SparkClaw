#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
browser_config="${XDG_CONFIG_HOME:-$HOME/.config}/sparkclaw/browserd.json"
profile_dir="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/extension-qualification/user-data"

usage() {
  cat <<'EOF'
Usage: bash scripts/open-browser-extension-preview.sh [URL]

Open the pinned host Chromium with the disposable Playwright Extension
qualification profile. This profile must not contain everyday tabs or
production credentials.
EOF
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
esac
(( $# <= 1 )) || { usage >&2; exit 1; }
target_url="${1:-about:blank}"

[[ -r "$browser_config" ]] || {
  printf 'host browser config is missing; run npm run setup:browser first\n' >&2
  exit 1
}

browser_executable="$(python3 - "$browser_config" <<'PY'
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
)"
[[ -x "$browser_executable" ]] || {
  printf 'host browser executable is unavailable: %s\n' "$browser_executable" >&2
  exit 1
}
mkdir -p "$profile_dir"
chmod 700 "$profile_dir"
exec "$browser_executable" --user-data-dir="$profile_dir" "$target_url"
