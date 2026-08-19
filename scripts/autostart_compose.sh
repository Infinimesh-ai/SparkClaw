#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

ENV_FILE="${SPARKCLAW_AUTOSTART_ENV_FILE:-$ROOT/.env}"

dotenv_value() {
  sparkclaw_dotenv_value "$ENV_FILE" "$1"
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
TIMEOUT_BIN="${TIMEOUT_BIN:-timeout}"

for command_path in "$DOCKER_BIN" "$NVIDIA_SMI_BIN" "$NVIDIA_CONTAINER_CLI_BIN" "$BASH_BIN" "$TIMEOUT_BIN"; do
  command -v "$command_path" >/dev/null 2>&1 || {
    echo "required boot command is unavailable: $command_path" >&2
    exit 1
  }
done

ready_timeout_seconds="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_AUTOSTART_READY_TIMEOUT_SECONDS 600)"
ready_probe_timeout_seconds="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS 10)"
for timeout_value in "$ready_timeout_seconds" "$ready_probe_timeout_seconds"; do
  [[ "$timeout_value" =~ ^[1-9][0-9]*$ ]] || {
    echo "SparkClaw autostart timeouts must be positive integers" >&2
    exit 1
  }
done
readonly ready_timeout_seconds
readonly ready_probe_timeout_seconds
readonly ready_poll_seconds=5
ready_deadline=$((SECONDS + ready_timeout_seconds))

run_ready_probe() {
  local remaining_seconds=$((ready_deadline - SECONDS))
  local probe_seconds="$ready_probe_timeout_seconds"

  (( remaining_seconds > 0 )) || return 124
  if (( probe_seconds > remaining_seconds )); then
    probe_seconds="$remaining_seconds"
  fi
  "$TIMEOUT_BIN" --foreground --kill-after=1s "${probe_seconds}s" "$@"
}

echo "Waiting for Docker and the NVIDIA runtime"
while true; do
  docker_ready=false
  if run_ready_probe "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_ready=true
  elif command -v sudo >/dev/null 2>&1 && run_ready_probe sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_ready=true
  fi
  gpu_list="$(run_ready_probe "$NVIDIA_SMI_BIN" -L 2>/dev/null || true)"
  if [[ "$docker_ready" == "true" ]] &&
    [[ -n "$gpu_list" ]] &&
    run_ready_probe "$NVIDIA_CONTAINER_CLI_BIN" info >/dev/null 2>&1; then
    break
  fi
  if (( SECONDS >= ready_deadline )); then
    echo "Docker and the NVIDIA runtime did not become ready within ${ready_timeout_seconds}s" >&2
    exit 1
  fi
  sleep "$ready_poll_seconds"
done

echo "Reconciling the SparkClaw product runtime"
exec "$BASH_BIN" "$ROOT/scripts/start_compose.sh"
