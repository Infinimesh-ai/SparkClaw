#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "SparkClaw doctor"
echo "root=$ROOT"

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

check "node" node --version
check "npm" npm --version
check "curl" curl --version
check "docker" bash -lc "$DOCKER_BIN --version"
check "docker compose" bash -lc "$DOCKER_BIN compose version"
if go version >/tmp/sparkclaw-doctor.out 2>&1; then
  echo "ok  go"
else
  echo "warn go not found on host; checking Docker Go builder"
  check "go builder" bash -lc "$DOCKER_BIN run --rm golang:1.25-alpine go version"
fi

for dir in data/workspaces data/traces data/artifacts data/logs data/memory data/eval configs docker; do
  if [[ ! -d "$dir" ]]; then
    echo "err missing $dir"
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

if command -v lsof >/dev/null 2>&1; then
  for port in 18789 18790 18889; do
    if lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
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
  SPEECH_BASE_URL="${SPARKCLAW_SPEECH_BASE_URL:-https://sparkclaw.infinimesh.cloud/asr}"
  SPEECH_MODEL="${SPARKCLAW_SPEECH_MODEL:-sparkclaw-asr}"
  SPEECH_RUNTIME_VERSION="${SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION:-0.24.0}"
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
