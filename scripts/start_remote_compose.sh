#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"
source "$ROOT/scripts/lib/deployment-profile.sh"
source "$ROOT/scripts/lib/host-browser.sh"

DOCKER_BIN="${DOCKER_BIN:-docker}"
PRODUCT_ENV="$ROOT/docker/env/sparkclaw.product.env"
MODE_ENV="$ROOT/docker/env/sparkclaw.remote.env"
PRIVATE_ENV="${SPARKCLAW_REMOTE_ENV_FILE:-$ROOT/.env.remote}"
LOCAL_MODE_ENV="$ROOT/docker/env/sparkclaw.local.env"
COMPOSE_FILE="$ROOT/docker/compose.yaml"
LOCAL_MODELS_COMPOSE_FILE="$ROOT/docker/compose.models.local.yaml"
JINGSI_COMPOSE_FILE="$ROOT/docker/compose.jingsi-lan.yaml"
MODE="start"
JINGSI_LAN=false
EFFECTIVE_ENV_FILE=""
LOCAL_EFFECTIVE_ENV_FILE=""

cleanup() {
  [[ -z "$EFFECTIVE_ENV_FILE" || ! -f "$EFFECTIVE_ENV_FILE" ]] || rm -f -- "$EFFECTIVE_ENV_FILE"
  [[ -z "$LOCAL_EFFECTIVE_ENV_FILE" || ! -f "$LOCAL_EFFECTIVE_ENV_FILE" ]] || rm -f -- "$LOCAL_EFFECTIVE_ENV_FILE"
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: bash scripts/start_remote_compose.sh [--check] [--env-file PATH] [--jingsi-lan]

Start or reconcile the full-remote SparkClaw product. All model adapters use
remote HTTP(S) endpoints. Before application startup, any local GPU model
containers in the shared SparkClaw Compose project are explicitly stopped.

Options:
  --check          Validate the remote profile, Host-CDP, and expanded Compose only
  --env-file PATH  Use a private override file instead of .env.remote
  --jingsi-lan     Apply the fixed JingSi LAN presentation overlay
  -h, --help       Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) MODE="check" ;;
    --jingsi-lan) JINGSI_LAN=true ;;
    --env-file)
      shift
      [[ $# -gt 0 ]] || { echo "--env-file requires a path" >&2; exit 1; }
      PRIVATE_ENV="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; echo "unknown argument: $1" >&2; exit 1 ;;
  esac
  shift
done

PRIVATE_ENV="$(realpath -m "$PRIVATE_ENV")"
[[ -f "$PRODUCT_ENV" ]] || { echo "product profile not found: $PRODUCT_ENV" >&2; exit 1; }
[[ -f "$MODE_ENV" ]] || { echo "remote profile not found: $MODE_ENV" >&2; exit 1; }
[[ -f "$PRIVATE_ENV" ]] || {
  echo "remote private environment file not found: $PRIVATE_ENV" >&2
  echo "run npm run deploy:remote first" >&2
  exit 1
}
sparkclaw_validate_product_profile remote "$PRODUCT_ENV" "$MODE_ENV" "$PRIVATE_ENV"

EFFECTIVE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-remote-env.XXXXXX")"
sparkclaw_merge_profile_env "$PRODUCT_ENV" "$MODE_ENV" "$PRIVATE_ENV" "$EFFECTIVE_ENV_FILE"
sparkclaw_export_profile_env "$EFFECTIVE_ENV_FILE"
webchat_port="$(sparkclaw_profile_value "$PRODUCT_ENV" "$MODE_ENV" "$PRIVATE_ENV" SPARKCLAW_WEBCHAT_PORT 18790)"
sparkclaw_tcp_port_valid "$webchat_port" || {
  echo "SPARKCLAW_WEBCHAT_PORT must be an integer between 1 and 65535" >&2
  exit 1
}
export SPARKCLAW_WEBCHAT_PORT="$webchat_port"

sparkclaw_check_host_browser "$ROOT" "$EFFECTIVE_ENV_FILE"

docker_cmd=("$DOCKER_BIN")
if ! "${docker_cmd[@]}" ps >/dev/null 2>&1; then
  if command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_cmd=(sudo -n "$DOCKER_BIN")
  elif [[ "$MODE" != "check" && -t 0 ]] && sudo "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_cmd=(sudo "$DOCKER_BIN")
  else
    echo "docker is not available; set DOCKER_BIN or use an account with Docker access" >&2
    exit 1
  fi
fi

compose_args=(
  compose
  --env-file "$EFFECTIVE_ENV_FILE"
  -f "$COMPOSE_FILE"
)
if [[ "$JINGSI_LAN" == true ]]; then
  compose_args+=(-f "$JINGSI_COMPOSE_FILE")
fi
compose_args+=(--profile product)
services=(postgres sandbox-runner gotenberg gateway webchat)

if [[ "$MODE" == "check" ]]; then
  "${docker_cmd[@]}" "${compose_args[@]}" config --quiet
  echo "SparkClaw remote configuration valid: five public model endpoints plus five application services; Host-CDP ready"
  exit 0
fi

LOCAL_EFFECTIVE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-local-model-env.XXXXXX")"
sparkclaw_merge_profile_env "$PRODUCT_ENV" "$LOCAL_MODE_ENV" "" "$LOCAL_EFFECTIVE_ENV_FILE"
local_model_args=(
  compose
  --env-file "$LOCAL_EFFECTIVE_ENV_FILE"
  -f "$LOCAL_MODELS_COMPOSE_FILE"
  --profile models-local
)
local_model_services=(
  sparkclaw-fast
  sparkclaw-deep
  sparkclaw-embedding
  sparkclaw-guard
  sparkclaw-asr
  sparkclaw-ocr
)
echo "Stopping local SparkClaw model containers before remote startup"
"${docker_cmd[@]}" "${local_model_args[@]}" stop "${local_model_services[@]}"

sparkclaw_export_profile_env "$EFFECTIVE_ENV_FILE"
browser_pid="$(sparkclaw_host_browser_pid "$EFFECTIVE_ENV_FILE")"
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
    echo "Gateway is healthy but not in the expected remote/external/postgres runtime:" >&2
    printf '%s\n' "$ready_json" >&2
    exit 1
  fi
  sleep 2
done
[[ "$gateway_ready" == true ]] || { echo "Timed out waiting for Gateway ready check at $ready_url" >&2; exit 1; }

"${docker_cmd[@]}" "${compose_args[@]}" exec -T gateway \
  node /app/scripts/host_browser_mcp_smoke.mjs
sparkclaw_assert_host_browser_pid_alive "$browser_pid"

echo "SparkClaw remote runtime ready: local models stopped; PostgreSQL, Sandbox Runner, Gotenberg, Gateway, WebChat, and Host-CDP ready"
