#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

DOCKER_BIN="${DOCKER_BIN:-docker}"
ENV_FILE="${SPARKCLAW_CLOUD_ENV_FILE:-$ROOT/.env}"
ENV_TEMPLATE="$ROOT/docker/env/sparkclaw.cloud.example.env"
COMPOSE_FILE="$ROOT/docker/compose.yaml"
CLOUD_COMPOSE_FILE="$ROOT/docker/compose.cloud.yaml"
VISIBLE_BROWSER_COMPOSE_FILE="$ROOT/docker/compose.visible-browser.yaml"
BROWSER_DISPLAY_RESOLVER="${SPARKCLAW_BROWSER_DISPLAY_RESOLVER:-$ROOT/scripts/resolve-browser-display.sh}"
MODE="start"
EFFECTIVE_ENV_FILE=""
browser_display=""
browser_xauthority=""
visible_browser=false

cleanup() {
  if [[ -n "$EFFECTIVE_ENV_FILE" && -f "$EFFECTIVE_ENV_FILE" ]]; then
    rm -f -- "$EFFECTIVE_ENV_FILE"
  fi
}

trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: bash scripts/start_cloud_compose.sh [--check]

Start or reconcile the cloud-model runtime and verify Gateway plus Chromium
automation readiness.

Options:
  --check      Validate configuration and expanded Compose without changing containers
  -h, --help   Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      MODE="check"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
  shift
done

[[ -f "$ENV_FILE" ]] || {
  echo "cloud environment file not found: $ENV_FILE" >&2
  echo "copy docker/env/sparkclaw.cloud.example.env to .env first" >&2
  exit 1
}
[[ -f "$ENV_TEMPLATE" ]] || {
  echo "cloud environment template not found: $ENV_TEMPLATE" >&2
  exit 1
}

EFFECTIVE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-cloud-env.XXXXXX")"
merged_defaults="$(sparkclaw_dotenv_merge_missing "$ENV_TEMPLATE" "$ENV_FILE" "$EFFECTIVE_ENV_FILE")"
if (( merged_defaults > 0 )); then
  echo "Applied $merged_defaults missing cloud environment defaults in memory; $ENV_FILE was not changed"
fi

webchat_port="$(sparkclaw_resolve_env_value "$EFFECTIVE_ENV_FILE" SPARKCLAW_WEBCHAT_PORT 18790)"
sparkclaw_tcp_port_valid "$webchat_port" || {
  echo "SPARKCLAW_WEBCHAT_PORT must be an integer between 1 and 65535" >&2
  exit 1
}

model_mode="$(sparkclaw_resolve_env_value "$EFFECTIVE_ENV_FILE" SPARKCLAW_MODEL_MODE external)"
case "${model_mode,,}" in
  mock)
    expected_model_mode="mock"
    ;;
  external)
    expected_model_mode="external"
    ;;
  *)
    echo "SPARKCLAW_MODEL_MODE must be mock or external for the cloud runtime" >&2
    exit 1
    ;;
esac

if [[ "$expected_model_mode" == "external" ]]; then
  for key in \
    SPARKCLAW_FAST_BASE_URL SPARKCLAW_FAST_MODEL \
    SPARKCLAW_DEEP_BASE_URL SPARKCLAW_DEEP_MODEL \
    SPARKCLAW_EMBEDDING_BASE_URL SPARKCLAW_EMBEDDING_MODEL \
    SPARKCLAW_GUARD_BASE_URL SPARKCLAW_GUARD_MODEL; do
    value="$(sparkclaw_resolve_env_value "$EFFECTIVE_ENV_FILE" "$key" '')"
    if [[ -z "$value" || "$value" == *example.invalid* || "$value" == replace-with-* ]]; then
      echo "$key must be set to a non-placeholder value in external mode" >&2
      exit 1
    fi
  done
  openai_key="$(sparkclaw_resolve_env_value "$EFFECTIVE_ENV_FILE" OPENAI_API_KEY '')"
  if [[ "$openai_key" == replace-with-* ]]; then
    echo "OPENAI_API_KEY must be empty or set to a non-placeholder value in external mode" >&2
    exit 1
  fi
fi

state_backend="$(sparkclaw_resolve_env_value "$EFFECTIVE_ENV_FILE" SPARKCLAW_STATE_BACKEND postgres)"
[[ "${state_backend,,}" == "postgres" ]] || {
  echo "SPARKCLAW_STATE_BACKEND must be postgres for the cloud server runtime" >&2
  exit 1
}

if browser_display_info="$(bash "$BROWSER_DISPLAY_RESOLVER")"; then
  browser_display="${browser_display_info%%$'\n'*}"
  browser_xauthority="${browser_display_info#*$'\n'}"
  visible_browser=true
  export SPARKCLAW_BROWSER_DISPLAY="$browser_display"
  export SPARKCLAW_BROWSER_XAUTHORITY="$browser_xauthority"
  echo "Visible Chromium display: $browser_display"
else
  echo "warn visible Chromium is unavailable; Weixin QR login requires a local desktop display" >&2
  echo "warn hidden Chromium automation remains enabled" >&2
fi

docker_cmd=("$DOCKER_BIN")
if ! "${docker_cmd[@]}" ps >/dev/null 2>&1; then
  if command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_cmd=(sudo -n "$DOCKER_BIN")
  elif [[ "$MODE" != "check" && -t 0 ]] && sudo "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_cmd=(sudo "$DOCKER_BIN")
  else
    echo "docker is not available; set DOCKER_BIN or use an account with Docker access" >&2
    exit 1
  fi
fi
if [[ "$visible_browser" == true && "${docker_cmd[0]}" == "sudo" ]]; then
  docker_cmd=(
    sudo -n env
    "SPARKCLAW_BROWSER_DISPLAY=$SPARKCLAW_BROWSER_DISPLAY"
    "SPARKCLAW_BROWSER_XAUTHORITY=$SPARKCLAW_BROWSER_XAUTHORITY"
    "$DOCKER_BIN"
  )
fi

compose_args=(
  compose
  --env-file "$EFFECTIVE_ENV_FILE"
  -f "$COMPOSE_FILE"
  -f "$CLOUD_COMPOSE_FILE"
)
if [[ "$visible_browser" == true ]]; then
  compose_args+=(-f "$VISIBLE_BROWSER_COMPOSE_FILE")
fi
compose_args+=(--profile models-local)
services=(postgres sandbox-runner gateway webchat)

if [[ "$MODE" == "check" ]]; then
  "${docker_cmd[@]}" "${compose_args[@]}" config --quiet
  if [[ "$visible_browser" == true ]]; then
    echo "SparkClaw cloud configuration valid: $expected_model_mode/postgres; visible Chromium enabled"
  else
    echo "SparkClaw cloud configuration valid: $expected_model_mode/postgres; hidden Chromium only"
  fi
  exit 0
fi

"${docker_cmd[@]}" "${compose_args[@]}" up -d --build --wait --wait-timeout 600 "${services[@]}"

ready_url="${SPARKCLAW_GATEWAY_READY_URL:-http://127.0.0.1:$webchat_port/readyz}"
gateway_ready=false
for _ in $(seq 1 30); do
  ready_json="$(curl -fsS --connect-timeout 2 --max-time 5 \
    "$ready_url" 2>/dev/null || true)"
  if [[ -n "$ready_json" ]]; then
    if printf '%s' "$ready_json" | grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' &&
      printf '%s' "$ready_json" | grep -Eq '"auth_required"[[:space:]]*:[[:space:]]*false' &&
      printf '%s' "$ready_json" | grep -Fq "\"model_mode\":\"$expected_model_mode\"" &&
      printf '%s' "$ready_json" | grep -Fq '"state_backend":"postgres"'; then
      gateway_ready=true
      break
    fi
    echo "Gateway is healthy but not in the expected $expected_model_mode/postgres runtime:" >&2
    printf '%s\n' "$ready_json" >&2
    exit 1
  fi
  sleep 2
done

if [[ "$gateway_ready" != true ]]; then
  echo "Timed out waiting for Gateway ready check at $ready_url" >&2
  exit 1
fi

browser_namespace="sc-cloud-$$"
"${docker_cmd[@]}" "${compose_args[@]}" exec -T gateway sh -eu -c '
  namespace="$1"
  visible="$2"
  browser=/app/node_modules/.bin/agent-browser
  chromium=/usr/bin/chromium
  profile=/var/lib/sparkclaw/browser-profiles
  hidden_session=hidden
  visible_session=visible
  visible_namespace="${namespace}-v"

  test -x "$browser"
  test -x "$chromium"
  test -w "$profile"
  fc-list ":lang=zh-cn" family | grep -q Noto

  export AGENT_BROWSER_EXECUTABLE_PATH="$chromium"
  export AGENT_BROWSER_NAMESPACE="$namespace"
  cleanup_browser() {
    AGENT_BROWSER_NAMESPACE="$namespace" \
      "$browser" --session "$hidden_session" close >/dev/null 2>&1 || true
    AGENT_BROWSER_NAMESPACE="$visible_namespace" \
      "$browser" --session "$visible_session" close >/dev/null 2>&1 || true
  }
  trap cleanup_browser EXIT INT TERM

  agent_version="$("$browser" --version)"
  chromium_version="$("$chromium" --version)"
  "$browser" --session "$hidden_session" open about:blank >/dev/null
  snapshot="$("$browser" --session "$hidden_session" snapshot -i)"
  test -n "$snapshot"

  if [ "$visible" = true ]; then
    test -n "${DISPLAY:-}"
    test -n "${XAUTHORITY:-}"
    test -r "$XAUTHORITY"
    test -s "$XAUTHORITY"
    display_number="${DISPLAY#:}"
    display_number="${display_number%%.*}"
    test -S "/tmp/.X11-unix/X${display_number}"

    AGENT_BROWSER_NAMESPACE="$visible_namespace" \
      AGENT_BROWSER_HEADED=true \
      AGENT_BROWSER_NO_XVFB=true \
      "$browser" --session "$visible_session" open about:blank >/dev/null
    visible_snapshot="$(AGENT_BROWSER_NAMESPACE="$visible_namespace" \
      "$browser" --session "$visible_session" snapshot -i)"
    test -n "$visible_snapshot"
  fi
  printf "%s\n%s\n" "$agent_version" "$chromium_version"
' sh "$browser_namespace" "$visible_browser"

if [[ "$visible_browser" == true ]]; then
  echo "SparkClaw cloud runtime ready: $expected_model_mode/postgres; hidden and visible Chromium ready"
else
  echo "SparkClaw cloud runtime ready: $expected_model_mode/postgres; hidden Chromium ready"
fi
