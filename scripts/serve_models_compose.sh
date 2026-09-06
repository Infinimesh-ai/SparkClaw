#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"
source "$ROOT/scripts/lib/deployment-profile.sh"

LANES="${1:-single-fast}"
MODEL_PROFILE="${SPARKCLAW_MODEL_LOADING_PROFILE:-}"
PRODUCT_ENV="$ROOT/docker/env/sparkclaw.product.env"
LOCAL_MODE_ENV="$ROOT/docker/env/sparkclaw.local.env"
PRIVATE_ENV="${SPARKCLAW_LOCAL_ENV_FILE:-$ROOT/.env.local}"
SINGLE_FAST=false
if [[ "$LANES" == "single-fast" ]]; then
  LANES="fast,embedding,guard,asr,ocr"
  MODEL_PROFILE="single-fast"
  SINGLE_FAST=true
elif [[ "$LANES" == "all" ]]; then
  LANES="fast,deep,embedding,guard"
elif [[ "$LANES" == "all-with-asr" ]]; then
  LANES="fast,deep,embedding,guard,asr"
elif [[ "$LANES" == "all-with-ocr" ]]; then
  LANES="fast,deep,embedding,guard,ocr"
elif [[ "$LANES" == "dual-light" ]]; then
  LANES="fast,deep,embedding,guard"
  MODEL_PROFILE="dual-light"
elif [[ "$LANES" == "dual-light-asr" ]]; then
  LANES="fast,deep,embedding,guard,asr"
  MODEL_PROFILE="dual-light"
elif [[ "$LANES" == "dual-light-chat" ]]; then
  LANES="fast,deep"
  MODEL_PROFILE="dual-light"
fi
case "$MODEL_PROFILE" in
  single-fast|dual-light) ;;
  *)
    echo "SPARKCLAW_MODEL_LOADING_PROFILE must select single-fast or dual-light" >&2
    exit 1
    ;;
esac

DOCKER_BIN="${DOCKER_BIN:-docker}"
force_model_recreate="$(sparkclaw_profile_value "$PRODUCT_ENV" "$LOCAL_MODE_ENV" "$PRIVATE_ENV" SPARKCLAW_FORCE_MODEL_RECREATE false)"
case "$(printf '%s' "$force_model_recreate" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes|on)
    force_model_recreate=true
    ;;
  0|false|no|off)
    force_model_recreate=false
    ;;
  *)
    echo "SPARKCLAW_FORCE_MODEL_RECREATE must be true or false" >&2
    exit 1
    ;;
esac
MODEL_STARTUP_TIMEOUT_SECONDS="${SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS:-10800}"
if [[ ! "$MODEL_STARTUP_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]]; then
  echo "SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS must be a positive integer" >&2
  exit 1
fi
MODEL_HEALTH_START_PERIOD="${SPARKCLAW_MODEL_HEALTH_START_PERIOD:-${MODEL_STARTUP_TIMEOUT_SECONDS}s}"
export SPARKCLAW_MODEL_HEALTH_START_PERIOD="$MODEL_HEALTH_START_PERIOD"
docker_cmd=("$DOCKER_BIN")
if ! "$DOCKER_BIN" ps >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
  docker_cmd=(
    sudo -n env
    "SPARKCLAW_MODEL_HEALTH_START_PERIOD=$SPARKCLAW_MODEL_HEALTH_START_PERIOD"
    "$DOCKER_BIN"
  )
fi

services=()
IFS=',' read -ra requested <<< "$LANES"
for lane in "${requested[@]}"; do
  lane="$(printf '%s' "$lane" | tr '[:upper:]' '[:lower:]' | xargs)"
  case "$lane" in
    fast|sparkclaw-fast) services+=(sparkclaw-fast) ;;
    deep|sparkclaw-deep) services+=(sparkclaw-deep) ;;
    embedding|embed|sparkclaw-embedding) services+=(sparkclaw-embedding) ;;
    guard|safety|sparkclaw-guard) services+=(sparkclaw-guard) ;;
    asr|speech|sparkclaw-asr) services+=(sparkclaw-asr) ;;
    ocr|ovis|ovisocr2|sparkclaw-ocr) services+=(sparkclaw-ocr) ;;
    "") ;;
    *)
      echo "unknown lane: $lane" >&2
      exit 1
      ;;
  esac
done

if [[ "${#services[@]}" -eq 0 ]]; then
  echo "usage: $0 single-fast|fast|deep|embedding|guard|asr|ocr|all|all-with-asr|all-with-ocr|dual-light|dual-light-asr|dual-light-chat|lane,lane" >&2
  exit 1
fi

mkdir -p data/models
EFFECTIVE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-local-model-env.XXXXXX")"
trap 'rm -f -- "$EFFECTIVE_ENV_FILE"' EXIT
sparkclaw_validate_product_profile local "$PRODUCT_ENV" "$LOCAL_MODE_ENV" "$PRIVATE_ENV"
sparkclaw_merge_profile_env "$PRODUCT_ENV" "$LOCAL_MODE_ENV" "$PRIVATE_ENV" "$EFFECTIVE_ENV_FILE"
sparkclaw_export_profile_env "$EFFECTIVE_ENV_FILE"
compose_args=(compose --env-file "$EFFECTIVE_ENV_FILE" -f docker/compose.models.local.yaml)
if [[ "$MODEL_PROFILE" == "single-fast" ]]; then
  :
elif [[ "$MODEL_PROFILE" == "dual-light" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.dual-light.env)
  compose_args+=(-f docker/compose.dual-light.yaml)
fi
compose_args+=(--profile models-local)

if [[ "$SINGLE_FAST" == "true" ]]; then
  "${docker_cmd[@]}" "${compose_args[@]}" stop sparkclaw-deep
fi

recreate_reason=""
if [[ "$force_model_recreate" == "true" ]]; then
  recreate_reason="requested by SPARKCLAW_FORCE_MODEL_RECREATE"
else
  for service in "${services[@]}"; do
    if ! container_id="$("${docker_cmd[@]}" "${compose_args[@]}" ps --all -q "$service")"; then
      echo "failed to inspect Compose container for $service" >&2
      exit 1
    fi
    if [[ -z "$container_id" ]]; then
      recreate_reason="$service is absent"
      break
    fi
    if ! state_status="$(
      "${docker_cmd[@]}" inspect --format '{{.State.Status}}' "$container_id"
    )"; then
      echo "failed to inspect container state for $service" >&2
      exit 1
    fi
    if [[ "$state_status" != "running" ]]; then
      recreate_reason="$service is $state_status"
      break
    fi
    if ! health_status="$(
      "${docker_cmd[@]}" inspect \
        --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' \
        "$container_id"
    )"; then
      echo "failed to inspect container health for $service" >&2
      exit 1
    fi
    if [[ "$health_status" != "healthy" ]]; then
      recreate_reason="$service health is $health_status"
      break
    fi
    if ! config_hash_line="$(
      "${docker_cmd[@]}" "${compose_args[@]}" config --hash "$service"
    )"; then
      echo "failed to resolve Compose configuration hash for $service" >&2
      exit 1
    fi
    expected_config_hash="${config_hash_line##* }"
    if [[ -z "$expected_config_hash" || "$expected_config_hash" == "$config_hash_line" ]]; then
      echo "Compose returned an invalid configuration hash for $service" >&2
      exit 1
    fi
    if ! actual_config_hash="$(
      "${docker_cmd[@]}" inspect \
        --format '{{index .Config.Labels "com.docker.compose.config-hash"}}' \
        "$container_id"
    )"; then
      echo "failed to inspect running configuration hash for $service" >&2
      exit 1
    fi
    if [[ "$actual_config_hash" != "$expected_config_hash" ]]; then
      recreate_reason="$service configuration drifted"
      break
    fi
  done
fi

recreate_args=()
if [[ -n "$recreate_reason" ]]; then
  echo "Force-recreating requested model group: $recreate_reason"
  "${docker_cmd[@]}" "${compose_args[@]}" stop "${services[@]}"
  recreate_args+=(--force-recreate)
else
  echo "Requested model group is running, healthy, and configuration-current; retaining containers"
  recreate_args+=(--no-recreate)
fi

"${docker_cmd[@]}" "${compose_args[@]}" up -d --wait \
  --wait-timeout "$MODEL_STARTUP_TIMEOUT_SECONDS" --build \
  "${recreate_args[@]}" "${services[@]}"
