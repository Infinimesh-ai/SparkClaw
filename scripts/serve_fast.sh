#!/usr/bin/env bash
set -euo pipefail

MODEL="${SPARKCLAW_FAST_MODEL_ID:-${SPARKCLAW_FAST_CHECKPOINT:-Qwen/Qwen3.6-35B-A3B-FP8}}"
SERVED_NAME="${SPARKCLAW_FAST_SERVED_NAME:-sparkclaw-fast}"
HOST="${SPARKCLAW_FAST_HOST:-127.0.0.1}"
PORT="${SPARKCLAW_FAST_PORT:-8001}"
TENSOR_PARALLEL_SIZE="${SPARKCLAW_FAST_TENSOR_PARALLEL_SIZE:-1}"
MAX_MODEL_LEN="${SPARKCLAW_FAST_MAX_MODEL_LEN:-131072}"
SPECULATIVE_TOKENS="${SPARKCLAW_FAST_SPECULATIVE_TOKENS:-2}"
EXTRA_ARGS="${SPARKCLAW_FAST_EXTRA_ARGS:-}"

command -v vllm >/dev/null 2>&1 || {
  echo "vllm is not installed or not on PATH"
  exit 1
}

args=(
  serve "$MODEL"
  --host "$HOST"
  --port "$PORT"
  --served-model-name "$SERVED_NAME"
  --tensor-parallel-size "$TENSOR_PARALLEL_SIZE"
  --max-model-len "$MAX_MODEL_LEN"
  --reasoning-parser qwen3
  --enable-auto-tool-choice
  --tool-call-parser qwen3_coder
  --language-model-only
)

if [[ "$SPECULATIVE_TOKENS" != "0" ]]; then
  args+=(--speculative-config "{\"method\":\"qwen3_next_mtp\",\"num_speculative_tokens\":$SPECULATIVE_TOKENS}")
fi

if [[ -n "$EXTRA_ARGS" ]]; then
  # shellcheck disable=SC2206
  extra=( $EXTRA_ARGS )
  args+=("${extra[@]}")
fi

exec vllm "${args[@]}"
