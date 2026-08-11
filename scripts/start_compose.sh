#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ ! -f .env ]]; then
  echo "local environment file not found: copy docker/env/sparkclaw.example.env to .env first" >&2
  exit 1
fi

# This command owns joint loading, drift detection, readiness, and warmup for
# Fast, embedding, guard, and OCR. Do not start product models independently.
bash scripts/serve_models_compose.sh single-fast

exec bash scripts/restart_runtime_compose.sh
