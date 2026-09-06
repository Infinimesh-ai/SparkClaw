#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ENV_FILE="${SPARKCLAW_BROWSER_ENV_FILE:-$ROOT/.env.local}"
if [[ ! -f "$ENV_FILE" ]]; then
  install -m 600 /dev/null "$ENV_FILE"
fi

mode=()
if [[ "${1:-}" == "--check" ]]; then
  mode=(--check)
  shift
fi
[[ $# -eq 0 ]] || { printf 'usage: bash scripts/setup-browser.sh [--check]\n' >&2; exit 2; }

bash "$ROOT/scripts/install-browser.sh" "${mode[@]}" --env-file "$ENV_FILE"
bash "$ROOT/scripts/setup-browser-controller.sh" "${mode[@]}" --env-file "$ENV_FILE"

echo "SparkClaw Browser and Browser Bridge ready"
