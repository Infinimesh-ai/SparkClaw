#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"
source "$ROOT/scripts/lib/deployment-profile.sh"
source "$ROOT/scripts/lib/browser-runtime.sh"

DOCKER_BIN="${DOCKER_BIN:-docker}"
PRODUCT_ENV="$ROOT/docker/env/sparkclaw.product.env"
MODE_ENV="$ROOT/docker/env/sparkclaw.local.env"
PRIVATE_ENV="${SPARKCLAW_LOCAL_ENV_FILE:-$ROOT/.env.local}"
COMPOSE_FILE="$ROOT/docker/compose.yaml"
LOCAL_MODELS_COMPOSE_FILE="$ROOT/docker/compose.models.local.yaml"
JINGSI_COMPOSE_FILE="$ROOT/docker/compose.jingsi-lan.yaml"
MODE="start"
JINGSI_LAN=false
EFFECTIVE_ENV_FILE=""

cleanup() {
  [[ -z "$EFFECTIVE_ENV_FILE" || ! -f "$EFFECTIVE_ENV_FILE" ]] || rm -f -- "$EFFECTIVE_ENV_FILE"
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: bash scripts/start_local_compose.sh [--check] [--jingsi-lan]

Start or reconcile the full-local SparkClaw product. Fast, Embedding, Guard,
ASR, and OCR run in the local Compose model group; PostgreSQL, Sandbox Runner,
Gotenberg, Gateway, and WebChat run in the application group.

Options:
  --check      Validate the local profile, Browser Bridge, and expanded Compose only
  --jingsi-lan Apply the fixed JingSi LAN presentation overlay
  -h, --help   Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) MODE="check" ;;
    --jingsi-lan) JINGSI_LAN=true ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; echo "unknown argument: $1" >&2; exit 1 ;;
  esac
  shift
done

[[ -f "$PRODUCT_ENV" ]] || { echo "product profile not found: $PRODUCT_ENV" >&2; exit 1; }
[[ -f "$MODE_ENV" ]] || { echo "local profile not found: $MODE_ENV" >&2; exit 1; }
[[ -f "$PRIVATE_ENV" ]] || {
  echo "local private environment file not found: $PRIVATE_ENV" >&2
  echo "run npm run deploy:local first" >&2
  exit 1
}
sparkclaw_validate_product_profile local "$PRODUCT_ENV" "$MODE_ENV" "$PRIVATE_ENV"

EFFECTIVE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-local-env.XXXXXX")"
sparkclaw_merge_profile_env "$PRODUCT_ENV" "$MODE_ENV" "$PRIVATE_ENV" "$EFFECTIVE_ENV_FILE"
sparkclaw_export_profile_env "$EFFECTIVE_ENV_FILE"
webchat_port="$(sparkclaw_profile_value "$PRODUCT_ENV" "$MODE_ENV" "$PRIVATE_ENV" SPARKCLAW_WEBCHAT_PORT 18790)"
sparkclaw_tcp_port_valid "$webchat_port" || {
  echo "SPARKCLAW_WEBCHAT_PORT must be an integer between 1 and 65535" >&2
  exit 1
}
export SPARKCLAW_WEBCHAT_PORT="$webchat_port"

sparkclaw_check_browser_runtime "$ROOT" "$EFFECTIVE_ENV_FILE"

docker_cmd=("$DOCKER_BIN")
if ! "${docker_cmd[@]}" ps >/dev/null 2>&1; then
  if command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_cmd=(sudo -n "$DOCKER_BIN")
  else
    echo "docker is not available; set DOCKER_BIN or use an account with Docker access" >&2
    exit 1
  fi
fi

compose_args=(
  compose
  --env-file "$EFFECTIVE_ENV_FILE"
  -f "$COMPOSE_FILE"
  -f "$LOCAL_MODELS_COMPOSE_FILE"
)
if [[ "$JINGSI_LAN" == true ]]; then
  compose_args+=(-f "$JINGSI_COMPOSE_FILE")
fi
compose_args+=(--profile product --profile models-local)
services=(postgres sandbox-runner gotenberg gateway webchat)

if [[ "$MODE" == "check" ]]; then
  "${docker_cmd[@]}" "${compose_args[@]}" config --quiet
  echo "SparkClaw local configuration valid: five local models plus five application services; Browser Bridge ready"
  exit 0
fi

browser_pid="$(sparkclaw_browser_main_pid "$EFFECTIVE_ENV_FILE")"
SPARKCLAW_LOCAL_ENV_FILE="$PRIVATE_ENV" \
  bash "$ROOT/scripts/serve_models_compose.sh" single-fast
"${docker_cmd[@]}" "${compose_args[@]}" up -d --build --wait --wait-timeout 600 "${services[@]}"

ready_url="${SPARKCLAW_GATEWAY_READY_URL:-http://127.0.0.1:$webchat_port/readyz}"
gateway_ready=false
for _ in $(seq 1 30); do
  ready_json="$(curl -fsS --connect-timeout 2 --max-time 5 "$ready_url" 2>/dev/null || true)"
  if [[ -n "$ready_json" ]]; then
    if printf '%s' "$ready_json" | grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' &&
      printf '%s' "$ready_json" | grep -Fq '"model_mode":"external"' &&
      printf '%s' "$ready_json" | grep -Fq '"state_backend":"postgres"'; then
      gateway_ready=true
      break
    fi
    echo "Gateway is healthy but not in the expected local/external/postgres runtime:" >&2
    printf '%s\n' "$ready_json" >&2
    exit 1
  fi
  sleep 2
done
[[ "$gateway_ready" == true ]] || { echo "Timed out waiting for Gateway ready check at $ready_url" >&2; exit 1; }

"${docker_cmd[@]}" "${compose_args[@]}" exec -T gateway \
  node /app/scripts/browser_controller_smoke.mjs
sparkclaw_assert_browser_pid_alive "$browser_pid"

echo "SparkClaw local runtime ready: five local models, PostgreSQL, Sandbox Runner, Gotenberg, Gateway, WebChat, and Browser Bridge"
