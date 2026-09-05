#!/usr/bin/env bash

sparkclaw_browser_setup() {
  local root="$1"
  printf '%s' "${SPARKCLAW_BROWSER_SETUP:-$root/scripts/setup-browser.sh}"
}

sparkclaw_install_browser_runtime() {
  local root="$1" env_file="$2" setup
  setup="$(sparkclaw_browser_setup "$root")"
  [[ -f "$setup" ]] || { printf 'SparkClaw browser setup not found: %s\n' "$setup" >&2; return 1; }
  SPARKCLAW_BROWSER_ENV_FILE="$env_file" bash "$setup"
}

sparkclaw_check_browser_runtime() {
  local root="$1" env_file="$2" setup
  setup="$(sparkclaw_browser_setup "$root")"
  [[ -f "$setup" ]] || { printf 'SparkClaw browser setup not found: %s\n' "$setup" >&2; return 1; }
  SPARKCLAW_BROWSER_ENV_FILE="$env_file" bash "$setup" --check
}

sparkclaw_browser_main_pid() {
  local pid
  pid="$(systemctl --user show --property MainPID --value sparkclaw-browser.service)"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || { printf 'SparkClaw browser PID is unavailable\n' >&2; return 1; }
  printf '%s' "$pid"
}

sparkclaw_assert_browser_pid_alive() {
  local pid="$1"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || { printf 'invalid SparkClaw browser PID: %s\n' "$pid" >&2; return 1; }
  kill -0 "$pid" 2>/dev/null || { printf 'SparkClaw browser PID %s exited during controller smoke\n' "$pid" >&2; return 1; }
}
