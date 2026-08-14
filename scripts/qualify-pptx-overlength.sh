#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BENCHMARK="$ROOT/benchmarks/pptx-overlength-phase0"
IMAGE="docker.io/gotenberg/gotenberg:8.15.3@sha256:664f1851e03fc230f194c114efa3ad7694e29951ac9ba04991c7b6e47bc243a8"
REPETITIONS="${PPTX_PHASE0_REPETITIONS:-100}"
OUTPUT="${PPTX_PHASE0_OUTPUT:-$ROOT/benchmarks/pptx-overlength-phase0-result.json}"
WORK_BASE="${TMPDIR:-/tmp}"
WORK_DIR="$(mktemp -d "$WORK_BASE/sparkclaw-pptx-phase0.XXXXXX")"
CONTAINER="sparkclaw-pptx-phase0-$$"
export PYTHONDONTWRITEBYTECODE=1

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if [[ "${PPTX_PHASE0_KEEP_WORK:-0}" != "1" ]]; then
    if [[ "$WORK_DIR" == "$WORK_BASE"/sparkclaw-pptx-phase0.* && -d "$WORK_DIR" ]]; then
      rm -rf -- "$WORK_DIR"
    fi
  else
    echo "Qualification work directory retained at $WORK_DIR" >&2
  fi
}
trap cleanup EXIT INT TERM

if [[ "$(uname -m)" != "aarch64" ]]; then
  echo "PPTX Phase 0 requires the target aarch64 host." >&2
  exit 1
fi
for command in docker libreoffice node npm python3 curl; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "$command is required for PPTX Phase 0." >&2
    exit 1
  fi
done

npm ci --prefix "$BENCHMARK" --ignore-scripts --no-audit --no-fund
python3 -m unittest discover -s "$BENCHMARK" -p 'test_*.py'

docker pull "$IMAGE"
docker run --detach --rm \
  --name "$CONTAINER" \
  --cpus 2 \
  --memory 4g \
  --pids-limit 512 \
  --publish 127.0.0.1::3000 \
  --volume /usr/share/fonts/opentype/noto:/usr/local/share/fonts/sparkclaw/noto:ro \
  "$IMAGE" >/dev/null

PORT="$(docker port "$CONTAINER" 3000/tcp | sed -n 's/.*:\([0-9][0-9]*\)$/\1/p' | head -1)"
if [[ -z "$PORT" ]]; then
  echo "Unable to resolve the isolated Gotenberg port." >&2
  exit 1
fi
GOTENBERG_URL="http://127.0.0.1:$PORT"
for _ in $(seq 1 60); do
  if curl --fail --silent "$GOTENBERG_URL/health" >/dev/null; then
    break
  fi
  sleep 0.5
done
if ! curl --fail --silent "$GOTENBERG_URL/health" >/dev/null; then
  echo "The isolated Gotenberg container did not become healthy." >&2
  exit 1
fi

docker exec "$CONTAINER" fc-list --format '%{family}\n' >"$WORK_DIR/gotenberg-fonts.txt"
python3 - "$WORK_DIR/gotenberg-fonts.txt" "$WORK_DIR/gotenberg-fonts.json" <<'PY'
import json
import sys
from pathlib import Path

families = set()
for line in Path(sys.argv[1]).read_text(encoding="utf-8").splitlines():
    families.update(item.strip() for item in line.split(",") if item.strip())
Path(sys.argv[2]).write_text(json.dumps({"families": sorted(families)}) + "\n", encoding="utf-8")
PY

powerpoint_args=()
if [[ -n "${PPTX_PHASE0_POWERPOINT_EVIDENCE:-}" ]]; then
  powerpoint_args=(--powerpoint-evidence "$PPTX_PHASE0_POWERPOINT_EVIDENCE")
fi

python3 "$BENCHMARK/phase0.py" \
  --work-dir "$WORK_DIR" \
  --gotenberg-url "$GOTENBERG_URL" \
  --gotenberg-image "$IMAGE" \
  --gotenberg-fonts "$WORK_DIR/gotenberg-fonts.json" \
  --repetitions "$REPETITIONS" \
  --output "$OUTPUT" \
  "${powerpoint_args[@]}"
