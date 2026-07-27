#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

agent_browser_version="$(./node_modules/.bin/agent-browser --version)"
if [[ "$agent_browser_version" != "agent-browser 0.32.3" ]]; then
  echo "expected agent-browser 0.32.3, got: $agent_browser_version" >&2
  exit 1
fi

chromium_executable="$(bash scripts/resolve-chromium.sh)"
chromium_version="$($chromium_executable --version)"
profile_dir="${SPARKCLAW_BROWSER_PROFILE_DIR:-$ROOT/data/browser-profiles}"

mkdir -p "$profile_dir"
if [[ ! -w "$profile_dir" ]]; then
  echo "browser profile directory is not writable: $profile_dir" >&2
  exit 1
fi

echo "$agent_browser_version"
echo "$chromium_version"
echo "Chromium executable: $chromium_executable"
echo "Browser profile directory: $profile_dir"
