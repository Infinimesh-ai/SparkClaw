#!/usr/bin/env bash
set +x
set -Eeuo pipefail

umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"

ENV_FILE="${SPARKCLAW_CLOUD_ENV_FILE:-$ROOT/.env}"
ENV_TEMPLATE="$ROOT/docker/env/sparkclaw.cloud.example.env"
MODE="deploy"
FORCE_CONFIG=false
TEMP_ENV=""

usage() {
  cat <<'EOF'
Usage: bash scripts/deploy_cloud_vm.sh [--check] [--configure] [--env-file PATH]

Configure and deploy the SparkClaw cloud-model runtime on an Ubuntu VM. The
script installs Docker when needed, starts the application services, and smoke
tests Chromium automation inside the Gateway container.

Options:
  --check          Validate Ubuntu, local configuration, Docker, and Compose
                   without installing packages or changing containers
  --configure      Re-enter private model endpoint configuration
  --env-file PATH  Use a local env file instead of .env
  -h, --help       Show this help
EOF
}

log() {
  printf '[sparkclaw-cloud] %s\n' "$*"
}

fail() {
  printf '[sparkclaw-cloud] error: %s\n' "$*" >&2
  exit 1
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
  printf '[sparkclaw-cloud] failed at line %s (exit %s)\n' "$line" "$exit_code" >&2
  printf '[sparkclaw-cloud] inspect services with: bash scripts/start_cloud_compose.sh\n' >&2
  exit "$exit_code"
}

trap cleanup EXIT
trap 'on_error "$LINENO"' ERR

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      [[ "$FORCE_CONFIG" == false ]] || fail "--check and --configure cannot be combined"
      MODE="check"
      ;;
    --configure)
      [[ "$MODE" == "deploy" ]] || fail "--check and --configure cannot be combined"
      FORCE_CONFIG=true
      ;;
    --env-file)
      shift
      [[ $# -gt 0 ]] || fail "--env-file requires a path"
      ENV_FILE="$1"
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

[[ "$(uname -s)" == "Linux" ]] || fail "this deployment supports Ubuntu Linux only"
[[ -r /etc/os-release ]] || fail "cannot identify the operating system"
# shellcheck disable=SC1091
source /etc/os-release
[[ "${ID:-}" == "ubuntu" ]] || fail "this deployment supports Ubuntu only; detected ${ID:-unknown}"
[[ "$EUID" -ne 0 ]] || fail "run this script as a normal sudo-capable user, not as root"

ENV_FILE="$(realpath -m "$ENV_FILE")"
[[ "$ENV_FILE" != "$ENV_TEMPLATE" ]] || fail "the example environment file cannot be used as the private env file"

dotenv_value() {
  sparkclaw_dotenv_value "$ENV_FILE" "$1"
}

set_dotenv_value() {
  local key="$1"
  local value="$2"
  local line=""
  local found=false

  sparkclaw_dotenv_key_valid "$key" || fail "invalid environment key: $key"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || fail "invalid newline in $key"
  TEMP_ENV="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
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

is_true() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

is_placeholder() {
  local value="${1:-}"
  [[ -z "$value" || "$value" == replace-with-* || "$value" == *example.invalid* ]]
}

http_url_valid() {
  local value="${1:-}"
  local authority=""

  [[ "$value" =~ ^https?://[^[:space:]#]+$ ]] || return 1
  [[ "$value" != *'?'* && "$value" != *'$'* ]] || return 1
  authority="${value#*://}"
  authority="${authority%%/*}"
  [[ -n "$authority" && "$authority" != *"@"* ]]
}

model_name_valid() {
  [[ "${1:-}" =~ ^[A-Za-z0-9][A-Za-z0-9._/+:-]*$ ]]
}

configured_model_name() {
  local key="$1"
  local default_value="$2"
  local value=""

  value="$(dotenv_value "$key")"
  if [[ -z "$value" || "$value" == replace-with-* ]]; then
    value="$default_value"
  fi
  model_name_valid "$value" || fail "$key is invalid"
  printf '%s' "$value"
}

api_key_valid() {
  local value="${1:-}"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* && "$value" != *' '* &&
    "$value" != *'$'* && "$value" != *'#'* ]]
}

url_host() {
  local value="$1"
  local authority="${value#*://}"
  authority="${authority%%/*}"
  authority="${authority%%:*}"
  printf '%s' "$authority"
}

prompt_required() {
  local label="$1"
  local current="$2"
  local validator="$3"
  local answer=""
  local value=""

  while true; do
    if [[ -n "$current" ]]; then
      printf '%s [%s]: ' "$label" "$current" >&2
    else
      printf '%s: ' "$label" >&2
    fi
    IFS= read -r answer || fail "input ended while reading $label"
    value="${answer:-$current}"
    if "$validator" "$value"; then
      printf '%s' "$value"
      return 0
    fi
    printf '[sparkclaw-cloud] invalid value for %s\n' "$label" >&2
  done
}

prompt_yes_no() {
  local label="$1"
  local default_value="$2"
  local answer=""
  local suffix="[y/N]"

  if [[ "$default_value" == true ]]; then
    suffix="[Y/n]"
  fi
  while true; do
    printf '%s %s: ' "$label" "$suffix" >&2
    IFS= read -r answer || fail "input ended while reading $label"
    answer="$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')"
    case "$answer" in
      '') [[ "$default_value" == true ]] ; return ;;
      y|yes) return 0 ;;
      n|no) return 1 ;;
      *) printf '[sparkclaw-cloud] enter y or n\n' >&2 ;;
    esac
  done
}

configure_models() {
  local fast_url fast_model deep_url deep_model embedding_url embedding_model
  local guard_url guard_model api_key speech_url speech_model ocr_url ocr_model
  local current_key=""
  local reuse_deep_default=true

  log "configuration is written only to $ENV_FILE"
  fast_url="$(prompt_required "Fast base URL" "$(dotenv_value SPARKCLAW_FAST_BASE_URL)" http_url_valid)"
  fast_model="$(configured_model_name SPARKCLAW_FAST_MODEL sparkclaw-fast)"
  set_dotenv_value SPARKCLAW_FAST_BASE_URL "$fast_url"
  set_dotenv_value SPARKCLAW_FAST_MODEL "$fast_model"
  set_dotenv_value SPARKCLAW_FAST_SERVED_NAME "$fast_model"

  if [[ -n "$(dotenv_value SPARKCLAW_DEEP_BASE_URL)" ]] &&
    { [[ "$(dotenv_value SPARKCLAW_DEEP_BASE_URL)" != "$fast_url" ]] ||
      [[ "$(dotenv_value SPARKCLAW_DEEP_MODEL)" != "$fast_model" ]]; }; then
    reuse_deep_default=false
  fi
  if prompt_yes_no "Reuse Fast for the logical Deep lane" "$reuse_deep_default"; then
    deep_url="$fast_url"
    deep_model="$fast_model"
  else
    deep_url="$(prompt_required "Deep base URL" "$(dotenv_value SPARKCLAW_DEEP_BASE_URL)" http_url_valid)"
    deep_model="$(configured_model_name SPARKCLAW_DEEP_MODEL sparkclaw-deep)"
  fi
  set_dotenv_value SPARKCLAW_DEEP_BASE_URL "$deep_url"
  set_dotenv_value SPARKCLAW_DEEP_MODEL "$deep_model"
  set_dotenv_value SPARKCLAW_DEEP_SERVED_NAME "$deep_model"

  embedding_url="$(prompt_required "Embedding base URL" "$(dotenv_value SPARKCLAW_EMBEDDING_BASE_URL)" http_url_valid)"
  embedding_model="$(configured_model_name SPARKCLAW_EMBEDDING_MODEL sparkclaw-embedding)"
  set_dotenv_value SPARKCLAW_EMBEDDING_BASE_URL "$embedding_url"
  set_dotenv_value SPARKCLAW_EMBEDDING_MODEL "$embedding_model"

  guard_url="$(prompt_required "Guard base URL" "$(dotenv_value SPARKCLAW_GUARD_BASE_URL)" http_url_valid)"
  guard_model="$(configured_model_name SPARKCLAW_GUARD_MODEL sparkclaw-guard)"
  set_dotenv_value SPARKCLAW_GUARD_BASE_URL "$guard_url"
  set_dotenv_value SPARKCLAW_GUARD_MODEL "$guard_model"

  current_key="$(dotenv_value OPENAI_API_KEY)"
  if [[ "$current_key" == replace-with-* ]]; then
    current_key=""
  fi
  if [[ -n "$current_key" ]]; then
    printf 'Shared model API key (hidden; Enter keeps the current key, - removes it): ' >&2
  else
    printf 'Shared model API key (optional, hidden; Enter for no key): ' >&2
  fi
  IFS= read -r -s api_key || fail "input ended while reading the optional model API key"
  printf '\n' >&2
  if [[ -z "$api_key" && -n "$current_key" ]]; then
    api_key="$current_key"
  elif [[ "$api_key" == "-" ]]; then
    api_key=""
  fi
  api_key_valid "$api_key" || fail "the API key contains unsupported whitespace, $, #, or newline characters"
  set_dotenv_value OPENAI_API_KEY "$api_key"

  if prompt_yes_no "Enable speech/ASR" "$(is_true "$(dotenv_value SPARKCLAW_SPEECH_ENABLED)" && printf true || printf false)"; then
    speech_url="$(prompt_required "Speech service root URL" "$(dotenv_value SPARKCLAW_SPEECH_BASE_URL)" http_url_valid)"
    speech_model="$(configured_model_name SPARKCLAW_SPEECH_MODEL sparkclaw-asr)"
    set_dotenv_value SPARKCLAW_SPEECH_ENABLED true
    set_dotenv_value SPARKCLAW_SPEECH_BASE_URL "$speech_url"
    set_dotenv_value SPARKCLAW_SPEECH_ALLOWED_HOSTS "$(url_host "$speech_url")"
    set_dotenv_value SPARKCLAW_SPEECH_MODEL "$speech_model"
  else
    set_dotenv_value SPARKCLAW_SPEECH_ENABLED false
  fi

  if prompt_yes_no "Enable document OCR" "$(is_true "$(dotenv_value SPARKCLAW_OCR_ENABLED)" && printf true || printf false)"; then
    ocr_url="$(prompt_required "OCR OpenAI base URL" "$(dotenv_value SPARKCLAW_OCR_BASE_URL)" http_url_valid)"
    ocr_model="$(configured_model_name SPARKCLAW_OCR_MODEL sparkclaw-ocr)"
    set_dotenv_value SPARKCLAW_OCR_ENABLED true
    set_dotenv_value SPARKCLAW_OCR_PROVIDER openai-http
    set_dotenv_value SPARKCLAW_OCR_BASE_URL "$ocr_url"
    set_dotenv_value SPARKCLAW_OCR_ALLOWED_HOSTS "$(url_host "$ocr_url")"
    set_dotenv_value SPARKCLAW_OCR_MODEL "$ocr_model"
  else
    set_dotenv_value SPARKCLAW_OCR_ENABLED false
  fi
}

configuration_complete() {
  local key value
  for key in \
    SPARKCLAW_FAST_BASE_URL SPARKCLAW_FAST_MODEL \
    SPARKCLAW_DEEP_BASE_URL SPARKCLAW_DEEP_MODEL \
    SPARKCLAW_EMBEDDING_BASE_URL SPARKCLAW_EMBEDDING_MODEL \
    SPARKCLAW_GUARD_BASE_URL SPARKCLAW_GUARD_MODEL; do
    value="$(dotenv_value "$key")"
    is_placeholder "$value" && return 1
  done
  return 0
}

validate_configuration() {
  local key value

  value="$(dotenv_value SPARKCLAW_API_TOKEN)"
  [[ -n "$value" && "$value" != replace-with-* ]] || fail "SPARKCLAW_API_TOKEN is missing"
  [[ "$(dotenv_value SPARKCLAW_MODEL_MODE)" == "external" ]] || fail "SPARKCLAW_MODEL_MODE must be external"
  [[ "$(dotenv_value SPARKCLAW_STATE_BACKEND)" == "postgres" ]] || fail "SPARKCLAW_STATE_BACKEND must be postgres"

  for key in SPARKCLAW_FAST_BASE_URL SPARKCLAW_DEEP_BASE_URL \
    SPARKCLAW_EMBEDDING_BASE_URL SPARKCLAW_GUARD_BASE_URL; do
    value="$(dotenv_value "$key")"
    http_url_valid "$value" || fail "$key must be a non-placeholder HTTP(S) base URL without embedded credentials"
  done
  for key in SPARKCLAW_FAST_MODEL SPARKCLAW_DEEP_MODEL \
    SPARKCLAW_EMBEDDING_MODEL SPARKCLAW_GUARD_MODEL; do
    value="$(dotenv_value "$key")"
    model_name_valid "$value" || fail "$key is missing or invalid"
  done
  value="$(dotenv_value OPENAI_API_KEY)"
  [[ "$value" != replace-with-* ]] || fail "OPENAI_API_KEY must be empty or contain an actual key"
  api_key_valid "$value" || fail "OPENAI_API_KEY contains unsupported characters"

  if is_true "$(dotenv_value SPARKCLAW_SPEECH_ENABLED)"; then
    http_url_valid "$(dotenv_value SPARKCLAW_SPEECH_BASE_URL)" || fail "SPARKCLAW_SPEECH_BASE_URL is invalid"
    model_name_valid "$(dotenv_value SPARKCLAW_SPEECH_MODEL)" || fail "SPARKCLAW_SPEECH_MODEL is invalid"
  fi
  if is_true "$(dotenv_value SPARKCLAW_OCR_ENABLED)"; then
    http_url_valid "$(dotenv_value SPARKCLAW_OCR_BASE_URL)" || fail "SPARKCLAW_OCR_BASE_URL is invalid"
    model_name_valid "$(dotenv_value SPARKCLAW_OCR_MODEL)" || fail "SPARKCLAW_OCR_MODEL is invalid"
  fi
}

install_docker() {
  local architecture=""
  local codename="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"

  command -v sudo >/dev/null 2>&1 || fail "sudo is required to install Docker"
  [[ "$codename" =~ ^[a-z0-9.-]+$ ]] || fail "cannot determine the Ubuntu release codename"
  sudo -v
  log "installing Docker Engine and the Compose plugin from Docker's Ubuntu repository"
  sudo env DEBIAN_FRONTEND=noninteractive apt-get update
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl gnupg
  sudo install -m 0755 -d /etc/apt/keyrings
  if [[ ! -s /etc/apt/keyrings/docker.gpg ]]; then
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg |
      sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    sudo chmod a+r /etc/apt/keyrings/docker.gpg
  fi
  architecture="$(dpkg --print-architecture)"
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu %s stable\n' \
    "$architecture" "$codename" |
    sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
  sudo env DEBIAN_FRONTEND=noninteractive apt-get update
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  sudo systemctl enable --now docker
}

docker_installed() {
  command -v "${DOCKER_BIN:-docker}" >/dev/null 2>&1 &&
    "${DOCKER_BIN:-docker}" compose version >/dev/null 2>&1
}

if [[ "$MODE" == "check" ]]; then
  [[ -f "$ENV_FILE" ]] || fail "cloud environment file not found: $ENV_FILE"
else
  if [[ ! -f "$ENV_FILE" ]]; then
    [[ -f "$ENV_TEMPLATE" ]] || fail "environment template not found: $ENV_TEMPLATE"
    [[ -d "$(dirname "$ENV_FILE")" ]] || fail "environment directory does not exist: $(dirname "$ENV_FILE")"
    install -m 600 "$ENV_TEMPLATE" "$ENV_FILE"
    log "created private environment file: $ENV_FILE"
    FORCE_CONFIG=true
  else
    chmod go-rwx "$ENV_FILE"
    log "preserving private environment file: $ENV_FILE"
  fi

  api_token="$(dotenv_value SPARKCLAW_API_TOKEN)"
  if [[ -z "$api_token" || "$api_token" == replace-with-* ]]; then
    api_token="$(LC_ALL=C od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
    [[ ${#api_token} -eq 64 ]] || fail "failed to generate the WebChat owner token"
    set_dotenv_value SPARKCLAW_API_TOKEN "$api_token"
    log "generated the WebChat owner token"
  fi
  unset api_token

  set_dotenv_value SPARKCLAW_CONTAINER_UID "$(id -u)"
  set_dotenv_value SPARKCLAW_CONTAINER_GID "$(id -g)"
  set_dotenv_value SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT "$ROOT/data/workspaces"
  set_dotenv_value SPARKCLAW_MODEL_MODE external
  set_dotenv_value SPARKCLAW_STATE_BACKEND postgres

  if ! configuration_complete; then
    FORCE_CONFIG=true
  fi
  if [[ "$FORCE_CONFIG" == true ]]; then
    configure_models
  fi
  set_dotenv_value SPARKCLAW_FAST_SERVED_NAME "$(dotenv_value SPARKCLAW_FAST_MODEL)"
  set_dotenv_value SPARKCLAW_DEEP_SERVED_NAME "$(dotenv_value SPARKCLAW_DEEP_MODEL)"

  for directory in \
    data/workspaces data/traces data/artifacts data/logs data/memory \
    data/eval data/browser-profiles; do
    mkdir -p "$directory"
    [[ -w "$directory" ]] || fail "$ROOT/$directory is not writable by $(id -un)"
    chmod u+rwx "$directory"
  done
fi

validate_configuration

if ! docker_installed; then
  if [[ "$MODE" == "check" ]]; then
    fail "Docker Engine and the Compose plugin are required"
  fi
  install_docker
fi

if [[ "$MODE" != "check" ]] && ! "${DOCKER_BIN:-docker}" ps >/dev/null 2>&1 &&
  command -v systemctl >/dev/null 2>&1 && ! systemctl is-active --quiet docker; then
  command -v sudo >/dev/null 2>&1 || fail "sudo is required to start Docker"
  sudo -v
  sudo systemctl enable --now docker
fi

command -v curl >/dev/null 2>&1 || fail "curl is required"

if ! "${DOCKER_BIN:-docker}" ps >/dev/null 2>&1; then
  command -v sudo >/dev/null 2>&1 || fail "Docker is unavailable to this user and sudo is not installed"
  if [[ "$MODE" == "check" ]]; then
    if ! sudo -n "${DOCKER_BIN:-docker}" ps >/dev/null 2>&1; then
      [[ -t 0 ]] || fail "Docker is unavailable; run the check interactively or configure Docker access"
      sudo -v
      sudo "${DOCKER_BIN:-docker}" ps >/dev/null
    fi
  else
    sudo -v
    sudo "${DOCKER_BIN:-docker}" ps >/dev/null
  fi
fi

export SPARKCLAW_CLOUD_ENV_FILE="$ENV_FILE"
if [[ "$MODE" == "check" ]]; then
  bash "$ROOT/scripts/start_cloud_compose.sh" --check
  log "Ubuntu VM deployment check passed"
  exit 0
fi

bash "$ROOT/scripts/start_cloud_compose.sh"

webchat_port="$(sparkclaw_resolve_env_value "$ENV_FILE" SPARKCLAW_WEBCHAT_PORT 18790)"
vm_address="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
if [[ -n "$vm_address" ]]; then
  log "WebChat: http://${vm_address}:${webchat_port}"
else
  log "WebChat port: $webchat_port"
fi
log "owner token: read SPARKCLAW_API_TOKEN from $ENV_FILE"
log "deployment complete"
