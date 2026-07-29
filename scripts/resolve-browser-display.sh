#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "visible browser forwarding is supported only on the Linux host runtime" >&2
  exit 1
fi

browser_display="${SPARKCLAW_BROWSER_DISPLAY:-${DISPLAY:-}}"
if [[ -z "$browser_display" ]]; then
  shopt -s nullglob
  browser_sockets=(/tmp/.X11-unix/X*)
  shopt -u nullglob
  if [[ ${#browser_sockets[@]} -eq 1 && -S "${browser_sockets[0]}" ]]; then
    browser_display=":${browser_sockets[0]##*X}"
  elif [[ ${#browser_sockets[@]} -gt 1 ]]; then
    echo "multiple host X11 displays found; set SPARKCLAW_BROWSER_DISPLAY explicitly" >&2
    exit 1
  fi
fi

if [[ ! "$browser_display" =~ ^:([0-9]+)(\.[0-9]+)?$ ]]; then
  echo "a local X11/XWayland display is required; set SPARKCLAW_BROWSER_DISPLAY (for example :1)" >&2
  exit 1
fi

display_number="${BASH_REMATCH[1]}"
display_socket="/tmp/.X11-unix/X${display_number}"
if [[ ! -S "$display_socket" ]]; then
  echo "host display socket is unavailable: $display_socket" >&2
  exit 1
fi

browser_xauthority="${SPARKCLAW_BROWSER_XAUTHORITY:-${XAUTHORITY:-}}"
if [[ -z "$browser_xauthority" ]]; then
  runtime_dir="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
  candidates=(
    "$runtime_dir/gdm/Xauthority"
    "$runtime_dir/Xauthority"
  )
  shopt -s nullglob
  candidates+=("$runtime_dir"/.mutter-Xwaylandauth.*)
  shopt -u nullglob
  user_home=""
  if command -v getent >/dev/null 2>&1; then
    user_home="$(getent passwd "$(id -u)" | cut -d: -f6)"
  fi
  if [[ -n "$user_home" ]]; then
    candidates+=("$user_home/.Xauthority")
  fi
  for candidate in "${candidates[@]}"; do
    if [[ -f "$candidate" && -r "$candidate" && -s "$candidate" ]]; then
      browser_xauthority="$candidate"
      break
    fi
  done
fi

if [[ ! -f "$browser_xauthority" || ! -r "$browser_xauthority" || ! -s "$browser_xauthority" ]]; then
  echo "Xauthority is unavailable; set SPARKCLAW_BROWSER_XAUTHORITY to the active desktop authority file" >&2
  exit 1
fi

printf '%s\n%s\n' "$browser_display" "$browser_xauthority"
