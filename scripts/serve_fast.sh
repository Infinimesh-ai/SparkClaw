#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL="${SPARKCLAW_FAST_MODEL_ID:-${SPARKCLAW_FAST_CHECKPOINT:-nvidia/Qwen3.6-35B-A3B-NVFP4}}"
SERVED_NAME="${SPARKCLAW_FAST_SERVED_NAME:-sparkclaw-fast}"
HOST="${SPARKCLAW_FAST_HOST:-127.0.0.1}"
PORT="${SPARKCLAW_FAST_PORT:-8001}"
TENSOR_PARALLEL_SIZE="${SPARKCLAW_FAST_TENSOR_PARALLEL_SIZE:-1}"
CAPACITY_CATALOG="${SPARKCLAW_MODEL_CAPACITY_CATALOG:-$ROOT/configs/model.profiles.json}"
MAX_MODEL_LEN="$(python3 "$ROOT/scripts/model_capacity_entrypoint.py" --resolve-context "$CAPACITY_CATALOG" --lane fast)"
GPU_MEMORY_UTILIZATION="${SPARKCLAW_FAST_GPU_MEMORY_UTILIZATION:-0.42}"
KV_CACHE_MEMORY_BYTES="${SPARKCLAW_FAST_KV_CACHE_MEMORY_BYTES:-8G}"
MAX_NUM_SEQS="${SPARKCLAW_FAST_MAX_NUM_SEQS:-4}"
SPECULATIVE_TOKENS="${SPARKCLAW_FAST_SPECULATIVE_TOKENS:-0}"
SPECULATIVE_CONFIG="${SPARKCLAW_FAST_SPECULATIVE_CONFIG:-}"
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
  --gpu-memory-utilization "$GPU_MEMORY_UTILIZATION"
  --trust-remote-code
  --enable-prefix-caching
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
