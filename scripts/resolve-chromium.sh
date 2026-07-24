#!/usr/bin/env bash
set -euo pipefail

candidates=()
configured="${SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE:-}"

if [[ -n "$configured" ]]; then
  candidates+=("$configured")
else
  case "$(uname -s)" in
    Darwin)
      candidates+=(
        "/Applications/Chromium.app/Contents/MacOS/Chromium"
        "$HOME/Applications/Chromium.app/Contents/MacOS/Chromium"
      )
      ;;
    Linux)
      candidates+=("/usr/bin/chromium" "/usr/bin/chromium-browser" "/snap/bin/chromium")
      ;;
    MINGW*|MSYS*|CYGWIN*)
      [[ -n "${LOCALAPPDATA:-}" ]] && candidates+=("$LOCALAPPDATA/Chromium/Application/chrome.exe")
      [[ -n "${PROGRAMFILES:-}" ]] && candidates+=("$PROGRAMFILES/Chromium/Application/chrome.exe")
      ;;
  esac

  for name in chromium chromium-browser; do
    if resolved="$(command -v "$name" 2>/dev/null)"; then
      candidates+=("$resolved")
    fi
  done
fi

for candidate in "${candidates[@]}"; do
  [[ -x "$candidate" ]] || continue
  version="$($candidate --version 2>/dev/null || true)"
  if [[ "$version" == Chromium* ]]; then
    directory="$(cd "$(dirname "$candidate")" && pwd -P)"
    printf '%s/%s\n' "$directory" "$(basename "$candidate")"
    exit 0
  fi
done

if [[ -n "$configured" ]]; then
  echo "configured Chromium executable is unavailable or is not Chromium: $configured" >&2
else
  echo "system Chromium was not found; install Chromium or set SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE" >&2
fi
exit 1
