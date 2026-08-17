#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

DOCKER_BIN="${DOCKER_BIN:-docker}"
RUNTIME_ENV="${SPARKCLAW_RUNTIME_ENV:-docker/env/sparkclaw.single-fast.env}"
RUNTIME_OVERRIDE_ENV="${SPARKCLAW_RUNTIME_OVERRIDE_ENV:-}"
COMPOSE_FILE="${SPARKCLAW_COMPOSE_FILE:-docker/compose.yaml}"
EXTRA_COMPOSE_FILE="${SPARKCLAW_RUNTIME_EXTRA_COMPOSE_FILE:-}"
OCR_ENV="docker/env/sparkclaw.ocr.env"
OCR_COMPOSE_FILE="docker/compose.ocr.yaml"
PROFILE="${SPARKCLAW_COMPOSE_PROFILE:-models-local}"
webchat_port="$(sparkclaw_resolve_env_value "$ROOT/.env" SPARKCLAW_WEBCHAT_PORT 18790)"
sparkclaw_tcp_port_valid "$webchat_port" || {
  echo "SPARKCLAW_WEBCHAT_PORT must be an integer between 1 and 65535" >&2
  exit 1
}
export SPARKCLAW_WEBCHAT_PORT="$webchat_port"
GATEWAY_READY_URL="${SPARKCLAW_GATEWAY_READY_URL:-http://127.0.0.1:$webchat_port/readyz}"
EXPECTED_MODEL_MODE="${SPARKCLAW_EXPECTED_MODEL_MODE:-external}"
EXPECTED_STATE_BACKEND="${SPARKCLAW_EXPECTED_STATE_BACKEND:-postgres}"
services=("$@")
browser_display=""
browser_xauthority=""
visible_browser=false

if [[ ${#services[@]} -eq 0 ]]; then
  services=(sandbox-runner gateway webchat)
fi

start_gateway=false
for service in "${services[@]}"; do
  case "$service" in
    gateway)
      start_gateway=true
      ;;
    sandbox-runner|webchat) ;;
    *)
      echo "unsupported runtime service: $service (expected sandbox-runner, gateway, or webchat)" >&2
      exit 1
      ;;
  esac
done

if [[ ! -f "$RUNTIME_ENV" ]]; then
  echo "runtime env file not found: $RUNTIME_ENV" >&2
  exit 1
fi
if [[ ! -f "$OCR_ENV" || ! -f "$OCR_COMPOSE_FILE" ]]; then
  echo "OCR runtime files not found: $OCR_ENV or $OCR_COMPOSE_FILE" >&2
  exit 1
fi
if [[ -n "$EXTRA_COMPOSE_FILE" && ! -f "$EXTRA_COMPOSE_FILE" ]]; then
  echo "runtime extra compose file not found: $EXTRA_COMPOSE_FILE" >&2
  exit 1
fi
if [[ -n "$RUNTIME_OVERRIDE_ENV" && ! -f "$RUNTIME_OVERRIDE_ENV" ]]; then
  echo "runtime override env file not found: $RUNTIME_OVERRIDE_ENV" >&2
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
    visible_browser=true
    echo "Visible Chromium display: $browser_display"
  else
    echo "warn visible Chromium is unavailable; hidden browser automation remains enabled" >&2
  fi
fi
if [[ "$visible_browser" == true ]]; then
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
fi

compose_args=(compose)
if [[ -f .env ]]; then
  compose_args+=(--env-file .env)
fi
compose_args+=(--env-file "$RUNTIME_ENV" --env-file "$OCR_ENV")
if [[ -n "$RUNTIME_OVERRIDE_ENV" ]]; then
  compose_args+=(--env-file "$RUNTIME_OVERRIDE_ENV")
fi
compose_args+=(-f "$COMPOSE_FILE" -f "$OCR_COMPOSE_FILE")
if [[ -n "$EXTRA_COMPOSE_FILE" ]]; then
  compose_args+=(-f "$EXTRA_COMPOSE_FILE")
fi
if [[ "$visible_browser" == true ]]; then
  compose_args+=(-f docker/compose.visible-browser.yaml)
fi
compose_args+=(--profile "$PROFILE")

# Model dependencies are jointly loaded and warmed by serve_models_compose.sh.
# Recreating them here would split that ownership and invalidate warmup state.
if [[ "$start_gateway" == true && "$EXPECTED_STATE_BACKEND" == "postgres" ]]; then
  echo "Ensuring PostgreSQL is healthy"
  "${docker_cmd[@]}" "${compose_args[@]}" up -d --wait --wait-timeout 120 --no-deps postgres
fi

"${docker_cmd[@]}" "${compose_args[@]}" up -d --build --force-recreate --no-deps "${services[@]}"

for _ in $(seq 1 30); do
  ready_json="$(curl -fsS "$GATEWAY_READY_URL" 2>/dev/null || true)"
  if [[ -n "$ready_json" ]]; then
    if printf '%s' "$ready_json" | grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' &&
      printf '%s' "$ready_json" | grep -Fq "\"model_mode\":\"$EXPECTED_MODEL_MODE\"" &&
      printf '%s' "$ready_json" | grep -Fq "\"state_backend\":\"$EXPECTED_STATE_BACKEND\""; then
      echo "SparkClaw runtime ready: $EXPECTED_MODEL_MODE/$EXPECTED_STATE_BACKEND"
      exit 0
    fi
    echo "Gateway is healthy but not in the expected $EXPECTED_MODEL_MODE/$EXPECTED_STATE_BACKEND runtime:" >&2
    printf '%s\n' "$ready_json" >&2
    exit 1
  fi
  sleep 2
done

echo "Timed out waiting for Gateway ready check at $GATEWAY_READY_URL" >&2
exit 1
