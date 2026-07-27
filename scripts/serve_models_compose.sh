#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

LANES="${1:-fast}"
MODEL_PROFILE="${SPARKCLAW_MODEL_LOADING_PROFILE:-}"
INCLUDE_ASR=false
if [[ "$LANES" == "all" ]]; then
  LANES="fast,deep,embedding,guard"
elif [[ "$LANES" == "all-with-asr" ]]; then
  LANES="fast,deep,embedding,guard,asr"
  INCLUDE_ASR=true
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
if ! "$DOCKER_BIN" ps >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
  DOCKER_BIN="sudo -n $DOCKER_BIN"
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
    "") ;;
    *)
      echo "unknown lane: $lane" >&2
      exit 1
      ;;
  esac
done

if [[ "${#services[@]}" -eq 0 ]]; then
  echo "usage: $0 fast|deep|embedding|guard|asr|all|all-with-asr|dual-light|dual-light-asr|dual-light-chat|lane,lane" >&2
  exit 1
fi

mkdir -p data/models
compose_args=(compose)
if [[ -f .env ]]; then
  compose_args+=(--env-file .env)
fi
if [[ "$MODEL_PROFILE" == "dual-light" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.dual-light.env)
  compose_args+=(-f docker/compose.yaml -f docker/compose.dual-light.yaml)
else
  compose_args+=(-f docker/compose.yaml)
fi
if [[ "$INCLUDE_ASR" == "true" ]]; then
  compose_args+=(--env-file docker/env/sparkclaw.asr.env)
  compose_args+=(-f docker/compose.asr.yaml)
fi
compose_args+=(--profile models-local up -d "${services[@]}")

exec $DOCKER_BIN "${compose_args[@]}"
