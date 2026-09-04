#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"
source "$ROOT/scripts/lib/host-browser.sh"

ENV_FILE="${SPARKCLAW_BROWSER_ENV_FILE:-$ROOT/.env.local}"
if [[ ! -f "$ENV_FILE" ]]; then
  install -m 600 /dev/null "$ENV_FILE"
fi

bash "$ROOT/scripts/install-host-browser.sh" --env-file "$ENV_FILE"

agent_browser_version="$(./node_modules/.bin/agent-browser --version)"
if [[ "$agent_browser_version" != "agent-browser 0.32.3" ]]; then
  echo "expected agent-browser 0.32.3, got: $agent_browser_version" >&2
  exit 1
fi

browser_pid="$(sparkclaw_host_browser_pid "$ENV_FILE")"
endpoint_file="$(sparkclaw_host_browser_endpoint_file "$ENV_FILE")"
SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE="$endpoint_file" \
SPARKCLAW_BROWSER_AUTOMATION_COMMAND="$ROOT/node_modules/.bin/agent-browser" \
SPARKCLAW_BROWSER_SMOKE_USE_HOST_ENDPOINT=true \
  node "$ROOT/scripts/host_browser_mcp_smoke.mjs"
sparkclaw_assert_host_browser_pid_alive "$browser_pid"

echo "$agent_browser_version"
echo "Host-CDP endpoint: $endpoint_file"
echo "Host Chromium PID: $browser_pid"
