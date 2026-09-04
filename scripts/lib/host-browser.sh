#!/usr/bin/env bash

sparkclaw_host_browser_installer() {
  local root="$1"
  printf '%s' "${SPARKCLAW_HOST_BROWSER_INSTALLER:-$root/scripts/install-host-browser.sh}"
}

sparkclaw_install_host_browser() {
  local root="$1"
  local env_file="$2"
  local installer

  installer="$(sparkclaw_host_browser_installer "$root")"
  [[ -f "$installer" ]] || {
    printf 'Host-CDP installer not found: %s\n' "$installer" >&2
    return 1
  }
  bash "$installer" --env-file "$env_file"
}

sparkclaw_check_host_browser() {
  local root="$1"
  local env_file="$2"
  local installer

  installer="$(sparkclaw_host_browser_installer "$root")"
  [[ -f "$installer" ]] || {
    printf 'Host-CDP installer not found: %s\n' "$installer" >&2
    return 1
  }
  bash "$installer" --check --env-file "$env_file"
}

sparkclaw_host_browser_endpoint_file() {
  local env_file="$1"
  local endpoint_file runtime_dir

  endpoint_file="$(sparkclaw_resolve_env_value "$env_file" SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST '')"
  if [[ -z "$endpoint_file" ]]; then
    runtime_dir="$(sparkclaw_resolve_env_value "$env_file" SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST '')"
    [[ -n "$runtime_dir" ]] || {
      printf 'Host-CDP host endpoint path is not configured in %s\n' "$env_file" >&2
      return 1
    }
    endpoint_file="$runtime_dir/cdp-endpoint"
  fi
  [[ "$endpoint_file" == /* ]] || {
    printf 'Host-CDP host endpoint path must be absolute: %s\n' "$endpoint_file" >&2
    return 1
  }
  printf '%s' "$endpoint_file"
}

sparkclaw_host_browser_pid() {
  local env_file="$1"
  local endpoint_file

  endpoint_file="$(sparkclaw_host_browser_endpoint_file "$env_file")"
  python3 - "$endpoint_file" "$(id -u)" <<'PY'
import json
import os
from pathlib import Path
import stat
import sys

path = Path(sys.argv[1])
expected_uid = int(sys.argv[2])
metadata = path.lstat()
if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
    raise SystemExit("Host-CDP endpoint must be a regular non-symlink file")
if metadata.st_uid != expected_uid:
    raise SystemExit("Host-CDP endpoint must be owned by the deployment user")
if stat.S_IMODE(metadata.st_mode) & 0o077:
    raise SystemExit("Host-CDP endpoint must not be accessible by group or other users")
with path.open(encoding="utf-8") as stream:
    endpoint = json.load(stream)
pid = endpoint.get("browserPID")
if not isinstance(pid, int) or pid <= 0:
    raise SystemExit("Host-CDP endpoint has an invalid browser PID")
print(pid)
PY
}

sparkclaw_assert_host_browser_pid_alive() {
  local pid="$1"

  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || {
    printf 'invalid Host-CDP browser PID: %s\n' "$pid" >&2
    return 1
  }
  kill -0 "$pid" 2>/dev/null || {
    printf 'Host Chromium PID %s exited when the agent-browser MCP process stopped\n' "$pid" >&2
    return 1
  }
}
