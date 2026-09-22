#!/usr/bin/env bash
set -Eeuo pipefail

config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
config_path="${SPARKCLAW_DESKTOP_LAUNCHER_CONFIG:-$config_home/sparkclaw/desktop-launcher.conf}"

[[ -f "$config_path" && ! -L "$config_path" ]] || {
  printf 'SparkClaw desktop launcher configuration is missing: %s\n' "$config_path" >&2
  exit 1
}

desktop_executable=""
descriptor_path=""
credential_path=""
while IFS='=' read -r key value || [[ -n "$key$value" ]]; do
  case "$key" in
    executable) desktop_executable="$value" ;;
    descriptor) descriptor_path="$value" ;;
    credential) credential_path="$value" ;;
    ""|'#'*) ;;
    *) printf 'Unsupported SparkClaw desktop launcher setting: %s\n' "$key" >&2; exit 1 ;;
  esac
done <"$config_path"

for value in "$desktop_executable" "$descriptor_path" "$credential_path"; do
  [[ "$value" == /* && "$value" != *$'\n'* && "$value" != *$'\r'* ]] || {
    printf 'SparkClaw desktop launcher configuration is invalid\n' >&2
    exit 1
  }
done
[[ -x "$desktop_executable" && -f "$descriptor_path" && -f "$credential_path" ]] || {
  printf 'SparkClaw desktop executable or local workbench configuration is unavailable\n' >&2
  exit 1
}

exec env \
  SPARKCLAW_DESKTOP_CONNECTION_FILE="$descriptor_path" \
  SPARKCLAW_DESKTOP_CREDENTIAL_FILE="$credential_path" \
  "$desktop_executable" "$@"
