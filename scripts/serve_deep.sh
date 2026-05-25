#!/usr/bin/env bash
set -euo pipefail

MODEL="${SPARKCLAW_DEEP_MODEL_ID:-${SPARKCLAW_DEEP_CHECKPOINT:-Qwen/Qwen3.6-27B-FP8}}"
SERVED_NAME="${SPARKCLAW_DEEP_SERVED_NAME:-sparkclaw-deep}"
HOST="${SPARKCLAW_DEEP_HOST:-127.0.0.1}"
PORT="${SPARKCLAW_DEEP_PORT:-8002}"
TENSOR_PARALLEL_SIZE="${SPARKCLAW_DEEP_TENSOR_PARALLEL_SIZE:-1}"
MAX_MODEL_LEN="${SPARKCLAW_DEEP_MAX_MODEL_LEN:-131072}"
GPU_MEMORY_UTILIZATION="${SPARKCLAW_DEEP_GPU_MEMORY_UTILIZATION:-0.82}"
KV_CACHE_MEMORY_BYTES="${SPARKCLAW_DEEP_KV_CACHE_MEMORY_BYTES:-}"
MAX_NUM_SEQS="${SPARKCLAW_DEEP_MAX_NUM_SEQS:-}"
SPECULATIVE_TOKENS="${SPARKCLAW_DEEP_SPECULATIVE_TOKENS:-2}"
SPECULATIVE_CONFIG="${SPARKCLAW_DEEP_SPECULATIVE_CONFIG:-}"
EXTRA_ARGS="${SPARKCLAW_DEEP_EXTRA_ARGS:-}"

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
  --gpu-memory-utilization "$GPU_MEMORY_UTILIZATION"
  --reasoning-parser qwen3
  --enable-auto-tool-choice
  --tool-call-parser qwen3_coder
  --language-model-only
)

if [[ -n "$KV_CACHE_MEMORY_BYTES" ]]; then
  args+=(--kv-cache-memory-bytes "$KV_CACHE_MEMORY_BYTES")
fi

if [[ -n "$MAX_NUM_SEQS" ]]; then
  args+=(--max-num-seqs "$MAX_NUM_SEQS")
fi

if [[ -n "$SPECULATIVE_CONFIG" ]]; then
  args+=(--speculative-config "$SPECULATIVE_CONFIG")
elif [[ "$SPECULATIVE_TOKENS" != "0" ]]; then
  args+=(--speculative-config "{\"method\":\"qwen3_next_mtp\",\"num_speculative_tokens\":$SPECULATIVE_TOKENS}")
fi

if [[ -n "$EXTRA_ARGS" ]]; then
  # shellcheck disable=SC2206
  extra=( $EXTRA_ARGS )
  args+=("${extra[@]}")
fi

exec vllm "${args[@]}"
