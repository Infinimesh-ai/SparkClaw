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
cjk_font=""

mkdir -p "$profile_dir"
if [[ ! -w "$profile_dir" ]]; then
  echo "browser profile directory is not writable: $profile_dir" >&2
  exit 1
fi

if ! command -v fc-list >/dev/null 2>&1; then
  echo "fontconfig is required to verify Chromium CJK font support" >&2
  exit 1
fi
cjk_fonts="$(fc-list ':lang=zh-cn' family 2>/dev/null || true)"
if [[ -z "$cjk_fonts" ]]; then
  echo "Chromium requires a CJK font to render Chinese pages; install fonts-noto-cjk (Debian/Ubuntu) or an equivalent package" >&2
  exit 1
fi
cjk_font="${cjk_fonts%%$'\n'*}"

echo "$agent_browser_version"
echo "$chromium_version"
echo "Chromium executable: $chromium_executable"
echo "Browser profile directory: $profile_dir"
if [[ -n "$cjk_font" ]]; then
  echo "Browser CJK font: $cjk_font"
fi

if browser_display_info="$(bash scripts/resolve-browser-display.sh 2>/dev/null)"; then
  browser_display="${browser_display_info%%$'\n'*}"
  browser_xauthority="${browser_display_info#*$'\n'}"
  echo "Visible Chromium display: $browser_display"
  echo "Visible Chromium Xauthority: $browser_xauthority"
else
  echo "Visible Chromium display: unavailable (hidden automation only)"
fi
