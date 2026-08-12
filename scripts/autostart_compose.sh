#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ENV_FILE="${SPARKCLAW_AUTOSTART_ENV_FILE:-$ROOT/.env}"

dotenv_value() {
  local key="$1"
  local line=""
  local value=""

  if [[ -f "$ENV_FILE" ]]; then
    line="$(grep -E "^${key}=" "$ENV_FILE" | tail -n 1 || true)"
  fi
  if [[ -n "$line" ]]; then
    value="${line#*=}"
    value="${value%$'\r'}"
    if [[ "$value" == \"*\" && ${#value} -ge 2 ]]; then
      value="${value:1:${#value}-2}"
    elif [[ "$value" == \'*\' && ${#value} -ge 2 ]]; then
      value="${value:1:${#value}-2}"
    fi
  fi
  printf '%s' "$value"
}

if [[ -z "${SPARKCLAW_AUTOSTART_ENABLED+x}" ]]; then
  SPARKCLAW_AUTOSTART_ENABLED="$(dotenv_value SPARKCLAW_AUTOSTART_ENABLED)"
fi
SPARKCLAW_AUTOSTART_ENABLED="${SPARKCLAW_AUTOSTART_ENABLED:-true}"

case "$(printf '%s' "$SPARKCLAW_AUTOSTART_ENABLED" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes|on) ;;
  0|false|no|off)
    echo "SparkClaw boot autostart is disabled by SPARKCLAW_AUTOSTART_ENABLED"
    exit 0
    ;;
  *)
    echo "SPARKCLAW_AUTOSTART_ENABLED must be true or false" >&2
    exit 1
    ;;
esac

[[ -f "$ENV_FILE" ]] || {
  echo "SparkClaw environment file not found: $ENV_FILE" >&2
  exit 1
}

DOCKER_BIN="${DOCKER_BIN:-docker}"
NVIDIA_SMI_BIN="${NVIDIA_SMI_BIN:-nvidia-smi}"
NVIDIA_CONTAINER_CLI_BIN="${NVIDIA_CONTAINER_CLI_BIN:-nvidia-container-cli}"
BASH_BIN="${BASH_BIN:-bash}"

for command_path in "$DOCKER_BIN" "$NVIDIA_SMI_BIN" "$NVIDIA_CONTAINER_CLI_BIN" "$BASH_BIN"; do
  command -v "$command_path" >/dev/null 2>&1 || {
    echo "required boot command is unavailable: $command_path" >&2
    exit 1
  }
done

readonly ready_timeout_seconds=600
readonly ready_poll_seconds=5
ready_deadline=$((SECONDS + ready_timeout_seconds))

echo "Waiting for Docker and the NVIDIA runtime"
while true; do
  docker_ready=false
  if "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_ready=true
  elif command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_ready=true
  fi
  gpu_list="$($NVIDIA_SMI_BIN -L 2>/dev/null || true)"
  if [[ "$docker_ready" == "true" ]] &&
    [[ -n "$gpu_list" ]] &&
    "$NVIDIA_CONTAINER_CLI_BIN" info >/dev/null 2>&1; then
    break
  fi
  if (( SECONDS >= ready_deadline )); then
    echo "Docker and the NVIDIA runtime did not become ready within ${ready_timeout_seconds}s" >&2
    exit 1
  fi
  sleep "$ready_poll_seconds"
done

echo "Starting SparkClaw through the cold-recreate product path"
exec "$BASH_BIN" "$ROOT/scripts/start_compose.sh"
