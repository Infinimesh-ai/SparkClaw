#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

LANES="${1:-single-fast}"
MODEL_PROFILE="${SPARKCLAW_MODEL_LOADING_PROFILE:-}"
INCLUDE_ASR=false
INCLUDE_OCR=false
SINGLE_FAST=false
if [[ "$LANES" == "single-fast" || "$LANES" == "fast-only" || "$LANES" == "single-fast-with-ocr" ]]; then
  LANES="fast,embedding,guard,asr,ocr"
  MODEL_PROFILE="single-fast"
  SINGLE_FAST=true
  INCLUDE_ASR=true
  INCLUDE_OCR=true
elif [[ "$LANES" == "all" ]]; then
  LANES="fast,deep,embedding,guard"
elif [[ "$LANES" == "all-with-asr" ]]; then
  LANES="fast,deep,embedding,guard,asr"
  INCLUDE_ASR=true
elif [[ "$LANES" == "all-with-ocr" ]]; then
  LANES="fast,deep,embedding,guard,ocr"
  INCLUDE_OCR=true
elif [[ "$LANES" == "dual-light" || "$LANES" == "light-dual" ]]; then
  LANES="fast,deep,embedding,guard"
  MODEL_PROFILE="dual-light"
elif [[ "$LANES" == "dual-light-asr" || "$LANES" == "light-dual-asr" ]]; then
  LANES="fast,deep,embedding,guard,asr"
  MODEL_PROFILE="dual-light"
  INCLUDE_ASR=true
elif [[ "$LANES" == "dual-light-chat" || "$LANES" == "light-dual-chat" ]]; then
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
force_model_recreate="$(sparkclaw_resolve_env_value "$ROOT/.env" SPARKCLAW_FORCE_MODEL_RECREATE false)"
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
    asr|speech|sparkclaw-asr) services+=(sparkclaw-asr); INCLUDE_ASR=true ;;
    ocr|ovis|ovisocr2|sparkclaw-ocr) services+=(sparkclaw-ocr); INCLUDE_OCR=true ;;
    "") ;;
    *)
      echo "unknown lane: $lane" >&2
      exit 1
      ;;
  esac
done

if [[ "${#services[@]}" -eq 0 ]]; then
  echo "usage: $0 single-fast|single-fast-with-ocr|fast|deep|embedding|guard|asr|ocr|all|all-with-asr|all-with-ocr|dual-light|dual-light-asr|dual-light-chat|lane,lane" >&2
  exit 1
fi

mkdir -p data/models
compose_args=(compose)
if [[ -f .env ]]; then
  compose_args+=(--env-file .env)
fi
if [[ "$MODEL_PROFILE" == "single-fast" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.single-fast.env)
  compose_args+=(-f docker/compose.yaml -f docker/compose.dual-light.yaml)
elif [[ "$MODEL_PROFILE" == "dual-light" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.dual-light.env)
  compose_args+=(-f docker/compose.yaml -f docker/compose.dual-light.yaml)
else
  compose_args+=(-f docker/compose.yaml)
fi
if [[ "$INCLUDE_ASR" == "true" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.asr.env)
  compose_args+=(-f docker/compose.asr.yaml)
fi
if [[ "$INCLUDE_OCR" == "true" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.ocr.env)
  compose_args+=(-f docker/compose.ocr.yaml)
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

exec "${docker_cmd[@]}" "${compose_args[@]}" up -d --wait \
  --wait-timeout "$MODEL_STARTUP_TIMEOUT_SECONDS" --build \
  "${recreate_args[@]}" "${services[@]}"
