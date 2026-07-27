#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "SparkClaw doctor"
echo "root=$ROOT"

load_dotenv_var() {
  local name="$1"
  local line
  if [[ -n "${!name+x}" || ! -f .env ]]; then
    return
  fi
  line="$(grep -E "^${name}=" .env | tail -n 1 || true)"
  if [[ -n "$line" ]]; then
    export "$name=${line#*=}"
  fi
}

for name in \
  SPARKCLAW_WEB_SEARCH_ENABLED \
  SPARKCLAW_WEB_SEARCH_PROVIDER \
  SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF \
  SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE \
  SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION \
  SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE \
  SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF \
  SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE \
  SPARKCLAW_TELEGRAM_ENABLED \
  SPARKCLAW_SPEECH_ENABLED \
  SPARKCLAW_SPEECH_BASE_URL \
  SPARKCLAW_SPEECH_MODEL \
  SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION; do
  load_dotenv_var "$name"
done

DOCKER_BIN="${DOCKER_BIN:-docker}"
if ! "$DOCKER_BIN" ps >/dev/null 2>&1 && command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
  DOCKER_BIN="sudo -n $DOCKER_BIN"
fi

check() {
  local name="$1"
  shift
  if "$@" >/tmp/sparkclaw-doctor.out 2>&1; then
    echo "ok  $name"
  else
    echo "err $name"
    cat /tmp/sparkclaw-doctor.out
    exit 1
  fi
}

is_true() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on|required) return 0 ;;
    *) return 1 ;;
  esac
}

secret_configured() {
  local direct_name="$1"
  local file_name="$2"
  local direct_value="${!direct_name:-}"
  local file_path="${!file_name:-}"
  [[ -n "$direct_value" ]] || [[ -n "$file_path" && -r "$file_path" && -s "$file_path" ]]
}

check_system_chromium() {
  CHROMIUM_EXECUTABLE="$(bash scripts/resolve-chromium.sh)"
  [[ "$($CHROMIUM_EXECUTABLE --version)" == Chromium* ]]
}

check_agent_browser_snapshot() {
  local browser="./node_modules/.bin/agent-browser"
  local namespace="sparkclaw-doctor-$$"
  local session="snapshot-$$"
  local snapshot
  export AGENT_BROWSER_NAMESPACE="$namespace"
  export AGENT_BROWSER_EXECUTABLE_PATH="$CHROMIUM_EXECUTABLE"
  trap '"$browser" --session "$session" close >/dev/null 2>&1 || true' RETURN
  "$browser" --session "$session" open about:blank >/dev/null
  snapshot="$("$browser" --session "$session" snapshot -i)"
  [[ -n "$snapshot" ]]
  "$browser" --session "$session" close >/dev/null
  trap - RETURN
}

check_npm_install_script_approvals() {
  npm approve-scripts --allow-scripts-pending --json |
    node -e '
      let input = "";
      process.stdin.on("data", (chunk) => { input += chunk; });
      process.stdin.on("end", () => {
        const pending = JSON.parse(input).allowScripts;
        process.exit(Array.isArray(pending) && pending.length === 0 ? 0 : 1);
      });
    '
}

check "Node.js 26" node -e 'if (process.versions.node.split(".")[0] !== "26") process.exit(1)'
check "npm 11" bash -lc '[[ "$(npm --version)" == 11.* ]]'
check "npm install scripts approved" check_npm_install_script_approvals
check "Node document dependencies" node -e 'for (const name of ["@mozilla/readability", "jsdom", "exceljs"]) require(name)'
check "Python 3.12" python3 -c 'import sys; raise SystemExit(sys.version_info[:2] != (3, 12))'
check "pip" python3 -m pip --version
check "Python document dependencies" python3 -c 'import docx, pptx, pypdf'
check "repository-private .tools absent" test ! -e "$ROOT/.tools"
check "agent-browser 0.32.3" bash -lc '[[ "$(./node_modules/.bin/agent-browser --version)" == "agent-browser 0.32.3" ]]'
CHROMIUM_EXECUTABLE=""
check "system Chromium" check_system_chromium
check "agent-browser Chromium snapshot smoke" check_agent_browser_snapshot
check "curl" curl --version
check "docker" bash -lc "$DOCKER_BIN --version"
check "docker compose" bash -lc "$DOCKER_BIN compose version"
GO_BIN="${GO_BIN:-$(command -v go 2>/dev/null || true)}"
if [[ -z "$GO_BIN" ]]; then
  echo "err Go 1.25 is required on the host"
  exit 1
fi
check "Go 1.25" bash -lc '[[ "$("$1" version)" == "go version go1.25."* ]]' _ "$GO_BIN"

for dir in data/workspaces data/traces data/artifacts data/logs data/memory data/eval data/browser-profiles configs docker; do
  if [[ ! -d "$dir" ]]; then
    echo "err missing $dir"
    exit 1
  fi
  if [[ "$dir" == "data/browser-profiles" && ! -w "$dir" ]]; then
    echo "err $dir is not writable"
    exit 1
  fi
  echo "ok  $dir"
done

for file in scripts/serve_fast.sh scripts/serve_deep.sh benchmarks/model_baseline.md; do
  if [[ ! -f "$file" ]]; then
    echo "err missing $file"
    exit 1
  fi
  echo "ok  $file"
done

if is_true "${SPARKCLAW_WEB_SEARCH_ENABLED:-false}"; then
  if [[ "${SPARKCLAW_WEB_SEARCH_PROVIDER:-infinimesh-info}" == "infinimesh-info" ]] &&
    secret_configured SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE &&
    secret_configured SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE &&
    secret_configured SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE; then
    echo "ok  infinimesh info credentials configured"
  elif [[ "${SPARKCLAW_WEB_SEARCH_PROVIDER:-infinimesh-info}" == "infinimesh-info" ]]; then
    echo "err infinimesh info credentials missing"
    exit 1
  else
    echo "warn legacy web search provider configured"
  fi
else
  echo "ok  web search disabled"
fi

port_is_listening() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -H -ltn "sport = :$port" | grep -q .
  else
    lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
  fi
}

if command -v ss >/dev/null 2>&1 || command -v lsof >/dev/null 2>&1; then
  for port in 18789 18790 18889; do
    if port_is_listening "$port"; then
      echo "warn port $port is already in use"
    else
      echo "ok  port $port available"
    fi
  done
fi

TELEGRAM_ENABLED="${SPARKCLAW_TELEGRAM_ENABLED:-false}"
if is_true "$TELEGRAM_ENABLED"; then
  echo "ok  Telegram connector enabled"
  if command -v ffmpeg >/dev/null 2>&1; then
    echo "ok  ffmpeg"
  else
    echo "warn ffmpeg unavailable; Telegram text and attachments work, but voice normalization is unavailable"
  fi
else
  echo "ok  Telegram connector disabled"
fi

SPEECH_ENABLED="${SPARKCLAW_SPEECH_ENABLED:-false}"
if [[ "$SPEECH_ENABLED" == "true" || "$SPEECH_ENABLED" == "1" ]]; then
  SPEECH_BASE_URL="${SPARKCLAW_SPEECH_BASE_URL:-}"
  SPEECH_MODEL="${SPARKCLAW_SPEECH_MODEL:-sparkclaw-asr}"
  SPEECH_RUNTIME_VERSION="${SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION:-0.24.0}"
  if [[ -z "$SPEECH_BASE_URL" ]]; then
    echo "err speech endpoint missing"
    exit 1
  fi
  check "speech health" curl --fail --silent --show-error --max-time 5 "$SPEECH_BASE_URL/health"
  check "speech runtime" bash -lc '
    payload="$(curl --fail --silent --show-error --max-time 5 "$1/version")" || exit 1
    node -e '\''const body = JSON.parse(process.argv[1]); if (body.version !== process.argv[2]) { throw new Error(`expected vLLM ${process.argv[2]}, got ${body.version ?? "missing"}`); }'\'' "$payload" "$2"
  ' _ "$SPEECH_BASE_URL" "$SPEECH_RUNTIME_VERSION"
  check "speech model" bash -lc '
    payload="$(curl --fail --silent --show-error --max-time 5 "$1/v1/models")" || exit 1
    node -e '\''const body = JSON.parse(process.argv[1]); if (!body.data?.some((item) => item.id === process.argv[2])) { throw new Error(`served model ${process.argv[2]} is unavailable`); }'\'' "$payload" "$2"
  ' _ "$SPEECH_BASE_URL" "$SPEECH_MODEL"
else
  echo "ok  speech disabled"
fi

echo "done"
