#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DOCKER_BIN="${DOCKER_BIN:-docker}"
RUNTIME_ENV="${SPARKCLAW_RUNTIME_ENV:-docker/env/sparkclaw.external-postgres.env}"
COMPOSE_FILE="${SPARKCLAW_COMPOSE_FILE:-docker/compose.yaml}"
PROFILE="${SPARKCLAW_COMPOSE_PROFILE:-models-local}"
GATEWAY_READY_URL="${SPARKCLAW_GATEWAY_READY_URL:-http://127.0.0.1:18789/readyz}"
services=("$@")
browser_display=""
browser_xauthority="/dev/null"

if [[ ${#services[@]} -eq 0 ]]; then
  services=(gateway webchat)
fi

start_gateway=false
for service in "${services[@]}"; do
  case "$service" in
    gateway)
      start_gateway=true
      ;;
    webchat) ;;
    *)
      echo "unsupported runtime service: $service (expected gateway or webchat)" >&2
      exit 1
      ;;
  esac
done

if [[ ! -f "$RUNTIME_ENV" ]]; then
  echo "runtime env file not found: $RUNTIME_ENV" >&2
  exit 1
fi

docker_cmd=("$DOCKER_BIN")
if ! "$DOCKER_BIN" ps >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
  docker_cmd=(sudo -n "$DOCKER_BIN")
fi

if ! "${docker_cmd[@]}" ps >/dev/null 2>&1; then
  echo "docker is not available; set DOCKER_BIN or run with a user that can access Docker" >&2
  exit 1
fi

if [[ "$start_gateway" == true ]]; then
  if browser_display_info="$(bash scripts/resolve-browser-display.sh)"; then
    browser_display="${browser_display_info%%$'\n'*}"
    browser_xauthority="${browser_display_info#*$'\n'}"
    echo "Visible Chromium display: $browser_display"
  else
    echo "warn visible Chromium is unavailable; hidden browser automation remains enabled" >&2
  fi
fi
export SPARKCLAW_BROWSER_DISPLAY="$browser_display"
export SPARKCLAW_BROWSER_XAUTHORITY="$browser_xauthority"
if [[ "${docker_cmd[0]}" == "sudo" ]]; then
  docker_cmd=(
    sudo -n env
    "SPARKCLAW_BROWSER_DISPLAY=$SPARKCLAW_BROWSER_DISPLAY"
    "SPARKCLAW_BROWSER_XAUTHORITY=$SPARKCLAW_BROWSER_XAUTHORITY"
    "$DOCKER_BIN"
  )
fi

compose_args=(compose)
if [[ -f .env ]]; then
  compose_args+=(--env-file .env)
fi
compose_args+=(--env-file "$RUNTIME_ENV" -f "$COMPOSE_FILE" --profile "$PROFILE")

"${docker_cmd[@]}" "${compose_args[@]}" up -d --build --force-recreate "${services[@]}"

for _ in $(seq 1 30); do
  ready_json="$(curl -fsS "$GATEWAY_READY_URL" 2>/dev/null || true)"
  if [[ -n "$ready_json" ]]; then
    if printf '%s' "$ready_json" | grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' &&
      printf '%s' "$ready_json" | grep -Eq '"model_mode"[[:space:]]*:[[:space:]]*"external"' &&
      printf '%s' "$ready_json" | grep -Eq '"state_backend"[[:space:]]*:[[:space:]]*"postgres"'; then
      echo "SparkClaw runtime ready: external/postgres"
      exit 0
    fi
    echo "Gateway is healthy but not in the expected external/postgres runtime:" >&2
    printf '%s\n' "$ready_json" >&2
    exit 1
  fi
  sleep 2
done

echo "Timed out waiting for Gateway ready check at $GATEWAY_READY_URL" >&2
exit 1
