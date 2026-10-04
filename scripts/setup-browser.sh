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

if [[ ${#mode[@]} -eq 0 ]]; then
  # Drain application work while the current browser is still alive. Restarting
  # the browser first destroys owned pages and forces the Controller to retain a
  # cleanup fence, which makes the replacement Executor fail closed.
  if systemctl --user cat sparkclaw-app-cli-executor.service >/dev/null 2>&1; then
    systemctl --user stop sparkclaw-app-cli-executor.service
  fi
  if systemctl --user cat sparkclaw-browser-controller.service >/dev/null 2>&1; then
    systemctl --user stop sparkclaw-browser-controller.service
  fi
fi

bash "$ROOT/scripts/install-browser.sh" "${mode[@]}" --env-file "$ENV_FILE"
bash "$ROOT/scripts/setup-browser-controller.sh" "${mode[@]}" --env-file "$ENV_FILE"

if [[ ${#mode[@]} -eq 0 ]]; then
  python3 "$ROOT/scripts/browser_components.py" wait-profile "${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/default/user-data"
fi

echo "SparkClaw Browser, Browser Bridge, Tampermonkey, and managed userscripts ready"
