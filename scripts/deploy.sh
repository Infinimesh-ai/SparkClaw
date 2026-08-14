#!/usr/bin/env bash
set +x
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ENV_FILE="$ROOT/.env"
ENV_TEMPLATE="$ROOT/docker/env/sparkclaw.example.env"
MODE="deploy"
TEMP_ENV=""

usage() {
  cat <<'EOF'
Usage: bash scripts/deploy.sh [--check]

Deploy SparkClaw on an NVIDIA GB10 DGX Spark. The command prepares .env,
validates the host, downloads and warms the single-fast model group, then
builds and starts Gateway, Sandbox Runner, and WebChat.

Options:
  --check  Prepare configuration and run preflight checks without starting
  -h, --help  Show this help
EOF
}

log() {
  printf '[sparkclaw] %s\n' "$*"
}

warn() {
  printf '[sparkclaw] warning: %s\n' "$*" >&2
}

fail() {
  printf '[sparkclaw] error: %s\n' "$*" >&2
  exit 1
}

is_true() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

cleanup() {
  if [[ -n "$TEMP_ENV" && -f "$TEMP_ENV" ]]; then
    rm -f -- "$TEMP_ENV"
  fi
}

on_error() {
  local exit_code=$?
  local line="$1"
  trap - ERR
  printf '[sparkclaw] deployment failed at line %s (exit %s)\n' "$line" "$exit_code" >&2
  printf '[sparkclaw] inspect containers with: docker ps --filter label=com.docker.compose.project=sparkclaw\n' >&2
  exit "$exit_code"
}

trap cleanup EXIT
trap 'on_error "$LINENO"' ERR

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
      fail "unknown argument: $1"
      ;;
  esac
  shift
done

[[ "$(uname -s)" == "Linux" ]] || fail "this deployment entrypoint supports Linux hosts only"
[[ "$EUID" -ne 0 ]] || fail "run this script as a normal user; it uses passwordless sudo only when Docker requires it"

dotenv_value() {
  local key="$1"
  local line=""
  local value=""
  if [[ -f "$ENV_FILE" ]]; then
    line="$(grep -E "^${key}=" "$ENV_FILE" | tail -n 1 || true)"
  fi
  if [[ -n "$line" ]]; then
    value="${line#*=}"
    value="${value%$'\r'}"
    if [[ "$value" == \"*\" && ${#value} -ge 2 ]]; then
      value="${value:1:${#value}-2}"
    elif [[ "$value" == \'*\' && ${#value} -ge 2 ]]; then
      value="${value:1:${#value}-2}"
    fi
  fi
  printf '%s' "$value"
}

set_dotenv_value() {
  local key="$1"
  local value="$2"
  local line=""
  local found=false

  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || fail "invalid newline in $key"
  TEMP_ENV="$(mktemp "$ROOT/.env.tmp.XXXXXX")"
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == "$key="* ]]; then
      if [[ "$found" == false ]]; then
        printf '%s=%s\n' "$key" "$value" >>"$TEMP_ENV"
        found=true
      fi
    else
      printf '%s\n' "$line" >>"$TEMP_ENV"
    fi
  done <"$ENV_FILE"
  if [[ "$found" == false ]]; then
    printf '%s=%s\n' "$key" "$value" >>"$TEMP_ENV"
  fi
  chmod 600 "$TEMP_ENV"
  mv -f -- "$TEMP_ENV" "$ENV_FILE"
  TEMP_ENV=""
}

if [[ ! -f "$ENV_FILE" ]]; then
  [[ -f "$ENV_TEMPLATE" ]] || fail "environment template not found: $ENV_TEMPLATE"
  install -m 600 "$ENV_TEMPLATE" "$ENV_FILE"
  log "created $ENV_FILE from the project template"
else
  chmod go-rwx "$ENV_FILE"
  log "preserving existing $ENV_FILE"
fi

autostart_enabled="$(dotenv_value SPARKCLAW_AUTOSTART_ENABLED)"
if [[ -z "$autostart_enabled" ]]; then
  set_dotenv_value SPARKCLAW_AUTOSTART_ENABLED true
  autostart_enabled=true
  log "enabled boot autostart by default"
fi
case "$(printf '%s' "$autostart_enabled" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes|on|0|false|no|off) ;;
  *) fail "SPARKCLAW_AUTOSTART_ENABLED must be true or false" ;;
esac

hf_token="$(dotenv_value HF_TOKEN)"
if [[ -z "$hf_token" ]]; then
  hf_token="$(dotenv_value HUGGING_FACE_HUB_TOKEN)"
fi
if [[ -z "$hf_token" && -n "${HF_TOKEN:-}" ]]; then
  hf_token="$HF_TOKEN"
fi
if [[ -z "$hf_token" && -n "${HUGGING_FACE_HUB_TOKEN:-}" ]]; then
  hf_token="$HUGGING_FACE_HUB_TOKEN"
fi
if [[ -z "$hf_token" ]]; then
  if [[ -t 0 ]]; then
    read -r -s -p "Hugging Face token (input hidden): " hf_token
    printf '\n'
  else
    fail "set HF_TOKEN or HUGGING_FACE_HUB_TOKEN before a non-interactive deployment"
  fi
fi
[[ -n "$hf_token" ]] || fail "a Hugging Face token is required to download the configured models"
if [[ -z "$(dotenv_value HF_TOKEN)" && -z "$(dotenv_value HUGGING_FACE_HUB_TOKEN)" ]]; then
  set_dotenv_value HF_TOKEN "$hf_token"
  log "stored the Hugging Face token in the local mode-0600 .env file"
fi
unset hf_token

host_uid="$(id -u)"
host_gid="$(id -g)"
set_dotenv_value SPARKCLAW_CONTAINER_UID "$host_uid"
set_dotenv_value SPARKCLAW_CONTAINER_GID "$host_gid"
set_dotenv_value SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT "$ROOT/data/workspaces"

for directory in \
  data/models data/workspaces data/traces data/artifacts data/logs data/memory \
  data/eval data/browser-profiles; do
  mkdir -p "$directory"
  chmod u+rwx "$directory"
done

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v docker >/dev/null 2>&1 || fail "Docker is required; install Docker Engine and the Compose plugin first"
command -v nvidia-smi >/dev/null 2>&1 || fail "nvidia-smi is required; install the NVIDIA driver and container toolkit first"
command -v nvidia-container-cli >/dev/null 2>&1 ||
  fail "nvidia-container-cli is required; install and configure the NVIDIA Container Toolkit first"
nvidia_smi_output="$(nvidia-smi -L 2>/dev/null || true)"
[[ -n "$nvidia_smi_output" ]] || fail "no NVIDIA GPU is visible to the host"

case "$(uname -m)" in
  aarch64|arm64) ;;
  *) fail "DGX Spark deployment requires Linux/ARM64; detected $(uname -m)" ;;
esac
printf '%s' "$nvidia_smi_output" | grep -Fq 'NVIDIA GB10' ||
  fail "DGX Spark deployment requires an NVIDIA GB10 GPU"

memory_gib="$(( $(awk '/^MemTotal:/ {print $2}' /proc/meminfo) / 1024 / 1024 ))"
(( memory_gib >= 100 )) ||
  fail "DGX Spark deployment requires at least 100 GiB of system/unified memory; detected ${memory_gib} GiB"
log "validated DGX Spark host (Linux/ARM64, NVIDIA GB10, ${memory_gib} GiB memory)"
unset nvidia_smi_output

DOCKER_BIN="${DOCKER_BIN:-docker}"
docker_cmd=("$DOCKER_BIN")
if ! "$DOCKER_BIN" ps >/dev/null 2>&1; then
  if command -v sudo >/dev/null 2>&1 && sudo -n "$DOCKER_BIN" ps >/dev/null 2>&1; then
    docker_cmd=(sudo -n "$DOCKER_BIN")
  else
    fail "Docker is unavailable to this user; configure Docker access or passwordless sudo"
  fi
fi
"${docker_cmd[@]}" compose version >/dev/null
"${docker_cmd[@]}" compose up --help | grep -Fq -- '--wait-timeout' ||
  fail "the Docker Compose plugin is too old; install a release that supports --wait-timeout"

model_cache_setting="$(dotenv_value SPARKCLAW_MODEL_CACHE)"
model_cache_setting="${model_cache_setting:-../data/models}"
if [[ "$model_cache_setting" == /* ]]; then
  model_cache="$model_cache_setting"
else
  model_cache="$(realpath -m "$ROOT/docker/$model_cache_setting")"
fi
mkdir -p "$model_cache"
chmod u+rwx "$model_cache"

cache_gib="$(( $(du -sk "$model_cache" | awk '{print $1}') / 1024 / 1024 ))"
free_gib="$(( $(df -Pk "$model_cache" | awk 'NR == 2 {print $4}') / 1024 / 1024 ))"
# Reserve the uncached portion of the resident checkpoints plus image/build headroom.
remaining_model_gib=$((85 - cache_gib))
if (( remaining_model_gib < 10 )); then
  remaining_model_gib=10
fi
required_free_gib=$((remaining_model_gib + 40))
if (( free_gib < required_free_gib )); then
  if is_true "${SPARKCLAW_ALLOW_LOW_DISK:-false}"; then
    warn "only ${free_gib} GiB is free; the conservative requirement is ${required_free_gib} GiB"
  else
    fail "only ${free_gib} GiB is free in $model_cache; ${required_free_gib} GiB is required (set SPARKCLAW_ALLOW_LOW_DISK=true to override)"
  fi
fi
log "model cache: $model_cache (${cache_gib} GiB present, ${free_gib} GiB free)"

model_compose=(
  compose
  --env-file .env
  --env-file docker/env/sparkclaw.single-fast.env
  --env-file docker/env/sparkclaw.ocr.env
  -f docker/compose.yaml
  -f docker/compose.dual-light.yaml
  -f docker/compose.ocr.yaml
  --profile models-local
)
runtime_compose=(
  compose
  --env-file .env
  --env-file docker/env/sparkclaw.single-fast.env
  --env-file docker/env/sparkclaw.ocr.env
  -f docker/compose.yaml
  -f docker/compose.ocr.yaml
  --profile models-local
)
"${docker_cmd[@]}" "${model_compose[@]}" config --quiet
"${docker_cmd[@]}" "${runtime_compose[@]}" config --quiet
log "Docker, NVIDIA GPU, storage, permissions, and Compose configuration are ready"

if [[ "$MODE" == "check" ]]; then
  log "preflight complete; no containers were changed"
  exit 0
fi

log "starting the single-fast model group; a cold download can take up to several hours"
log "models are cached under $model_cache and reused on later runs"
bash scripts/start_compose.sh

webchat_ready=false
for _ in $(seq 1 60); do
  if curl -fsS --max-time 3 http://127.0.0.1:18790/ >/dev/null 2>&1; then
    webchat_ready=true
    break
  fi
  sleep 2
done
[[ "$webchat_ready" == true ]] || fail "Gateway is ready, but WebChat did not respond at http://127.0.0.1:18790"

ready_json="$(curl -fsS --max-time 5 http://127.0.0.1:18790/readyz)"
printf '%s' "$ready_json" | grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' || fail "Gateway ready check returned an unexpected response"

log "installing the boot autostart service"
bash scripts/install_autostart_systemd.sh

lan_ip=""
if command -v ip >/dev/null 2>&1; then
  lan_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}' || true)"
fi

log "deployment complete"
printf '  WebChat (local): http://127.0.0.1:18790\n'
if [[ -n "$lan_ip" ]]; then
  printf '  WebChat (LAN):   http://%s:18790\n' "$lan_ip"
fi
printf '  Gateway ready:  http://127.0.0.1:18790/readyz (WebChat ingress)\n'
"${docker_cmd[@]}" ps \
  --filter label=com.docker.compose.project=sparkclaw \
  --format '  {{.Names}}: {{.Status}}'
