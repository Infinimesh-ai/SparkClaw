#!/usr/bin/env bash
set -Eeuo pipefail

mode="${1:-serve}"
config_path="${SPARKCLAW_BROWSER_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/sparkclaw/browser.json}"
resolver="${SPARKCLAW_BROWSER_DISPLAY_RESOLVER:-}"

case "$mode" in
  serve) ;;
  open) ;;
  *) printf 'usage: sparkclaw-browser-launcher [serve|open]\n' >&2; exit 2 ;;
esac

[[ -r "$config_path" ]] || { printf 'SparkClaw browser config is unavailable: %s\n' "$config_path" >&2; exit 1; }
mapfile -t browser_values < <(python3 - "$config_path" <<'PY'
import json
from pathlib import Path
import sys

value = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
required = {"version", "executable", "profileDir", "bridgeDir", "browserVersion", "bridgeVersion"}
if set(value) != required or value.get("version") != 1:
    raise SystemExit("SparkClaw browser config is invalid")
for key in ("executable", "profileDir", "bridgeDir"):
    item = value.get(key)
    if not isinstance(item, str) or not item.startswith("/"):
        raise SystemExit("SparkClaw browser config is invalid")
print(value["executable"])
print(value["profileDir"])
print(value["bridgeDir"])
PY
)
(( ${#browser_values[@]} == 3 )) || exit 1
browser_executable="${browser_values[0]}"
profile_dir="${browser_values[1]}"
bridge_dir="${browser_values[2]}"
[[ -x "$browser_executable" && -d "$profile_dir" && -d "$bridge_dir" ]] || {
  printf 'SparkClaw browser installation is incomplete\n' >&2
  exit 1
}

if [[ -z "$resolver" ]]; then
  resolver="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/resolve-browser-display.sh"
fi
[[ -r "$resolver" ]] || { printf 'SparkClaw display resolver is unavailable\n' >&2; exit 1; }
display_info="$(bash "$resolver")" || exit 1
display="${display_info%%$'\n'*}"
xauthority="${display_info#*$'\n'}"
browser_sandbox="$(dirname "$browser_executable")/chrome_sandbox"

browser_args=(
  --ozone-platform=x11
  --force-renderer-accessibility
  --user-data-dir="$profile_dir"
  --disable-extensions-except="$bridge_dir"
  --load-extension="$bridge_dir"
)

if [[ "$mode" == "open" ]]; then
  systemctl --user start sparkclaw-browser.service
  for _ in $(seq 1 50); do
    systemctl --user is-active --quiet sparkclaw-browser.service && break
    sleep 0.1
  done
  systemctl --user is-active --quiet sparkclaw-browser.service || {
    printf 'SparkClaw browser service did not start\n' >&2
    exit 1
  }
  exec env DISPLAY="$display" XAUTHORITY="$xauthority" CHROME_DEVEL_SANDBOX="$browser_sandbox" \
    "$browser_executable" "${browser_args[@]}" about:blank
fi

exec env DISPLAY="$display" XAUTHORITY="$xauthority" CHROME_DEVEL_SANDBOX="$browser_sandbox" \
  "$browser_executable" "${browser_args[@]}"
