#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

DOCKER_BIN="${DOCKER_BIN:-docker}"
ENV_FILE="${SPARKCLAW_CLOUD_ENV_FILE:-$ROOT/.env}"
COMPOSE_FILE="$ROOT/docker/compose.yaml"
CLOUD_COMPOSE_FILE="$ROOT/docker/compose.cloud.yaml"

[[ -f "$ENV_FILE" ]] || {
  echo "cloud environment file not found: $ENV_FILE" >&2
  echo "copy docker/env/sparkclaw.cloud.example.env to .env first" >&2
  exit 1
}

webchat_port="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_WEBCHAT_PORT 18790)"
sparkclaw_tcp_port_valid "$webchat_port" || {
  echo "SPARKCLAW_WEBCHAT_PORT must be an integer between 1 and 65535" >&2
  exit 1
}

api_token="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_API_TOKEN '')"
if [[ -z "$api_token" || "$api_token" == replace-with-* ]]; then
  echo "SPARKCLAW_API_TOKEN must be set to a non-placeholder value" >&2
  exit 1
fi

model_mode="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_MODEL_MODE external)"
case "${model_mode,,}" in
  mock)
    expected_model_mode="mock"
    ;;
  external)
    expected_model_mode="external"
    for key in \
      SPARKCLAW_FAST_BASE_URL SPARKCLAW_FAST_MODEL \
      SPARKCLAW_DEEP_BASE_URL SPARKCLAW_DEEP_MODEL \
      SPARKCLAW_EMBEDDING_BASE_URL SPARKCLAW_EMBEDDING_MODEL \
      SPARKCLAW_GUARD_BASE_URL SPARKCLAW_GUARD_MODEL; do
      value="$(sparkclaw_resolve_env_value "$ENV_FILE" "$key" '')"
      if [[ -z "$value" || "$value" == *example.invalid* || "$value" == replace-with-* ]]; then
        echo "$key must be set to a non-placeholder value in external mode" >&2
        exit 1
      fi
    done
    openai_key="$(sparkclaw_resolve_env_value "$ENV_FILE" OPENAI_API_KEY '')"
    if [[ "$openai_key" == replace-with-* ]]; then
      echo "OPENAI_API_KEY must be empty or set to a non-placeholder value in external mode" >&2
      exit 1
    fi
    ;;
  *)
    echo "SPARKCLAW_MODEL_MODE must be mock or external for the cloud runtime" >&2
    exit 1
    ;;
esac

state_backend="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_STATE_BACKEND postgres)"
[[ "${state_backend,,}" == "postgres" ]] || {
  echo "SPARKCLAW_STATE_BACKEND must be postgres for the cloud server runtime" >&2
  exit 1
}

docker_cmd=("$DOCKER_BIN")
if ! "$DOCKER_BIN" ps >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
  docker_cmd=(sudo -n "$DOCKER_BIN")
fi
if ! "${docker_cmd[@]}" ps >/dev/null 2>&1; then
  echo "docker is not available; set DOCKER_BIN or use an account with Docker access" >&2
  exit 1
fi

compose_args=(
  compose
  --env-file "$ENV_FILE"
  -f "$COMPOSE_FILE"
  -f "$CLOUD_COMPOSE_FILE"
  --profile models-local
)
services=(postgres sandbox-runner gateway webchat)

"${docker_cmd[@]}" "${compose_args[@]}" up -d --build --wait --wait-timeout 600 "${services[@]}"

ready_url="${SPARKCLAW_GATEWAY_READY_URL:-http://127.0.0.1:$webchat_port/readyz}"
for _ in $(seq 1 30); do
  ready_json="$(curl -fsS --connect-timeout 2 --max-time 5 \
    -H "Authorization: Bearer $api_token" "$ready_url" 2>/dev/null || true)"
  if [[ -n "$ready_json" ]]; then
    if printf '%s' "$ready_json" | grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' &&
      printf '%s' "$ready_json" | grep -Eq '"auth_required"[[:space:]]*:[[:space:]]*true' &&
      printf '%s' "$ready_json" | grep -Fq "\"model_mode\":\"$expected_model_mode\"" &&
      printf '%s' "$ready_json" | grep -Fq '"state_backend":"postgres"'; then
      echo "SparkClaw cloud runtime ready: $expected_model_mode/postgres"
      exit 0
    fi
    echo "Gateway is healthy but not in the expected $expected_model_mode/postgres runtime:" >&2
    printf '%s\n' "$ready_json" >&2
    exit 1
  fi
  sleep 2
done

echo "Timed out waiting for Gateway ready check at $ready_url" >&2
exit 1
