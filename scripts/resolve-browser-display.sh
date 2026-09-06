#!/usr/bin/env bash
set -euo pipefail

append_browser_xauthority() {
  local candidate="$1"
  local existing=""

  if [[ ! -f "$candidate" || ! -r "$candidate" || ! -s "$candidate" ]]; then
    return
  fi
  for existing in "${browser_xauthorities[@]:-}"; do
    [[ "$existing" != "$candidate" ]] || return 0
  done
  browser_xauthorities+=("$candidate")
  return 0
}

discover_browser_xauthorities() {
  local runtime_dir="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
  local user_home=""
  local candidate=""

  browser_xauthorities=()
  if [[ -n "${SPARKCLAW_BROWSER_XAUTHORITY:-}" ]]; then
    append_browser_xauthority "$SPARKCLAW_BROWSER_XAUTHORITY"
    return
  fi

  if [[ -n "${XAUTHORITY:-}" ]]; then
    append_browser_xauthority "$XAUTHORITY"
  fi
  append_browser_xauthority "$runtime_dir/gdm/Xauthority"
  append_browser_xauthority "$runtime_dir/Xauthority"

  shopt -s nullglob
  for candidate in "$runtime_dir"/.mutter-Xwaylandauth.*; do
    append_browser_xauthority "$candidate"
  done
  shopt -u nullglob

  if command -v getent >/dev/null 2>&1; then
    user_home="$(getent passwd "$(id -u)" | cut -d: -f6)"
  fi
  if [[ -n "$user_home" ]]; then
    append_browser_xauthority "$user_home/.Xauthority"
  fi
}

probe_browser_display() {
  local display="$1"
  local xauthority="$2"

  if command -v xset >/dev/null 2>&1; then
    timeout 3 env DISPLAY="$display" XAUTHORITY="$xauthority" xset q >/dev/null 2>&1
    return
  fi
  if command -v xauth >/dev/null 2>&1; then
    [[ -n "$(timeout 3 xauth -f "$xauthority" nlist "$display" 2>/dev/null)" ]]
    return
  fi
  return 1
}

resolve_browser_display() {
  local x11_socket_dir="${1:-/tmp/.X11-unix}"
  local requested_display="${SPARKCLAW_BROWSER_DISPLAY:-${DISPLAY:-}}"
  local candidate_display=""
  local candidate_xauthority=""
  local socket=""
  local socket_name=""
  local display_number=""
  local -a browser_sockets=()
  local -a display_numbers=()
  local -a display_candidates=()

  if [[ "$(uname -s)" != "Linux" ]]; then
    echo "visible browser forwarding is supported only on the Linux host runtime" >&2
    return 1
  fi

  if [[ -n "$requested_display" ]]; then
    if [[ ! "$requested_display" =~ ^:([0-9]+)(\.[0-9]+)?$ ]]; then
      echo "a local X11/XWayland display is required; set SPARKCLAW_BROWSER_DISPLAY (for example :1)" >&2
      return 1
    fi
    display_number="${BASH_REMATCH[1]}"
    if [[ ! -S "$x11_socket_dir/X${display_number}" ]]; then
      echo "host display socket is unavailable: $x11_socket_dir/X${display_number}" >&2
      return 1
    fi
    display_candidates+=("$requested_display")
  else
    shopt -s nullglob
    browser_sockets=("$x11_socket_dir"/X*)
    shopt -u nullglob
    mapfile -t display_numbers < <(
      for socket in "${browser_sockets[@]}"; do
        [[ -S "$socket" ]] || continue
        socket_name="${socket##*/}"
        if [[ "$socket_name" =~ ^X([0-9]+)$ ]]; then
          printf '%s\n' "${BASH_REMATCH[1]}"
        fi
      done | sort -n -u
    )
    for display_number in "${display_numbers[@]}"; do
      display_candidates+=(":$display_number")
    done
    if [[ ${#display_candidates[@]} -eq 0 ]]; then
      echo "a local X11/XWayland display is required; set SPARKCLAW_BROWSER_DISPLAY (for example :1)" >&2
      return 1
    fi
  fi

  discover_browser_xauthorities
  if [[ ${#browser_xauthorities[@]} -eq 0 ]]; then
    echo "Xauthority is unavailable; set SPARKCLAW_BROWSER_XAUTHORITY to the active desktop authority file" >&2
    return 1
  fi

  for candidate_display in "${display_candidates[@]}"; do
    for candidate_xauthority in "${browser_xauthorities[@]}"; do
      if probe_browser_display "$candidate_display" "$candidate_xauthority"; then
        printf '%s\n%s\n' "$candidate_display" "$candidate_xauthority"
        return
      fi
    done
  done

  if [[ -n "$requested_display" ]]; then
    echo "configured host display cannot be opened with the available Xauthority: $requested_display" >&2
  else
    echo "no usable local X11/XWayland display accepted the available Xauthority files" >&2
  fi
  return 1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  resolve_browser_display
fi
