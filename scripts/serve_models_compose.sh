#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

LANES="${1:-single-fast}"
MODEL_PROFILE="${SPARKCLAW_MODEL_LOADING_PROFILE:-}"
INCLUDE_ASR=false
INCLUDE_OCR=false
SINGLE_FAST=false
if [[ "$LANES" == "single-fast" || "$LANES" == "fast-only" || "$LANES" == "single-fast-with-ocr" ]]; then
  LANES="fast,embedding,guard,ocr"
  MODEL_PROFILE="single-fast"
  SINGLE_FAST=true
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

DOCKER_BIN="${DOCKER_BIN:-docker}"
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
  reload_single_fast=false
  for service in "${services[@]}"; do
    container_id="$("${docker_cmd[@]}" "${compose_args[@]}" ps -q "$service")"
    if [[ -z "$container_id" ]]; then
      reload_single_fast=true
      break
    fi
    health_status="$(
      "${docker_cmd[@]}" inspect \
        --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \
        "$container_id"
    )"
    if [[ "$health_status" != "healthy" ]]; then
      reload_single_fast=true
      break
    fi
    config_hash_line="$("${docker_cmd[@]}" "${compose_args[@]}" config --hash "$service")"
    expected_config_hash="${config_hash_line##* }"
    actual_config_hash="$(
      "${docker_cmd[@]}" inspect \
        --format '{{index .Config.Labels "com.docker.compose.config-hash"}}' \
        "$container_id"
    )"
    if [[ -z "$expected_config_hash" || "$actual_config_hash" != "$expected_config_hash" ]]; then
      reload_single_fast=true
      break
    fi
  done
  if [[ "$reload_single_fast" == "true" ]]; then
    echo "Reloading Fast, embedding, guard, and OCR together"
    "${docker_cmd[@]}" "${compose_args[@]}" stop "${services[@]}"
  fi
fi

exec "${docker_cmd[@]}" "${compose_args[@]}" up -d --wait \
  --wait-timeout "$MODEL_STARTUP_TIMEOUT_SECONDS" "${services[@]}"
