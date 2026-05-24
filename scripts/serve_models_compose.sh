#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

LANES="${1:-fast}"
if [[ "$LANES" == "all" ]]; then
  LANES="fast,deep,embedding,reranker"
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
    reranker|rerank|score|sparkclaw-reranker) services+=(sparkclaw-reranker) ;;
    "") ;;
    *)
      echo "unknown lane: $lane" >&2
      exit 1
      ;;
  esac
done

if [[ "${#services[@]}" -eq 0 ]]; then
  echo "usage: $0 fast|deep|embedding|reranker|all|lane,lane" >&2
  exit 1
fi

mkdir -p data/models
compose_args=(compose)
if [[ -f .env ]]; then
  compose_args+=(--env-file .env)
fi
compose_args+=(-f docker/compose.yaml --profile models-local up -d "${services[@]}")

exec $DOCKER_BIN "${compose_args[@]}"
