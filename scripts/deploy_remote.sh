#!/usr/bin/env bash
set +x
set -Eeuo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/lib/dotenv.sh"
source "$ROOT/scripts/lib/deployment-profile.sh"
source "$ROOT/scripts/lib/host-browser.sh"

ENV_FILE="${SPARKCLAW_REMOTE_ENV_FILE:-$ROOT/.env.remote}"
PRODUCT_ENV="$ROOT/docker/env/sparkclaw.product.env"
MODE_ENV="$ROOT/docker/env/sparkclaw.remote.env"
EFFECTIVE_ENV_FILE=""
MODE="deploy"
CONFIGURE_CREDENTIALS=false

usage() {
  cat <<'EOF'
Usage: bash scripts/deploy_remote.sh [--check] [--configure] [--env-file PATH]

Deploy the full-remote SparkClaw product on Ubuntu. Public model endpoints and
non-secret defaults come from the versioned remote profile. The private
mode-0600 env file stores only credentials and machine-specific overrides.

Options:
  --check          Validate Ubuntu, private overrides, Host-CDP, Docker, and Compose
  --configure      Re-enter the optional shared remote-model API key
  --env-file PATH  Use a private override file instead of .env.remote
  -h, --help       Show this help
EOF
}

log() {
  printf '[sparkclaw-remote] %s\n' "$*"
}

fail() {
  printf '[sparkclaw-remote] error: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  [[ -z "$EFFECTIVE_ENV_FILE" || ! -f "$EFFECTIVE_ENV_FILE" ]] || rm -f -- "$EFFECTIVE_ENV_FILE"
}

on_error() {
  local exit_code=$?
  local line="$1"
  trap - ERR
  printf '[sparkclaw-remote] failed at line %s (exit %s)\n' "$line" "$exit_code" >&2
  printf '[sparkclaw-remote] inspect services with: npm run start:remote\n' >&2
  exit "$exit_code"
}

trap cleanup EXIT
trap 'on_error "$LINENO"' ERR

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      [[ "$CONFIGURE_CREDENTIALS" == false ]] || fail "--check and --configure cannot be combined"
      MODE="check"
      ;;
    --configure)
      [[ "$MODE" == "deploy" ]] || fail "--check and --configure cannot be combined"
      CONFIGURE_CREDENTIALS=true
      ;;
    --env-file)
      shift
      [[ $# -gt 0 ]] || fail "--env-file requires a path"
      ENV_FILE="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
  shift
done

[[ "$(uname -s)" == "Linux" ]] || fail "remote deployment supports Ubuntu Linux only"
[[ -r /etc/os-release ]] || fail "cannot identify the operating system"
# shellcheck disable=SC1091
source /etc/os-release
[[ "${ID:-}" == "ubuntu" ]] || fail "remote deployment supports Ubuntu only; detected ${ID:-unknown}"
[[ "$EUID" -ne 0 ]] || fail "run this script as a normal sudo-capable user, not as root"
command -v python3 >/dev/null 2>&1 || fail "python3 is required"

ENV_FILE="$(realpath -m "$ENV_FILE")"
[[ -f "$PRODUCT_ENV" ]] || fail "product profile not found: $PRODUCT_ENV"
[[ -f "$MODE_ENV" ]] || fail "remote profile not found: $MODE_ENV"
[[ "$ENV_FILE" != "$PRODUCT_ENV" && "$ENV_FILE" != "$MODE_ENV" ]] || fail "a versioned profile cannot be used as the private env file"

set_dotenv_value() {
  local key="$1"
  local value="$2"
  local line=""
  local found=false
  local temporary

  sparkclaw_dotenv_key_valid "$key" || fail "invalid environment key: $key"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || fail "invalid newline in $key"
  temporary="$(mktemp "${ENV_FILE}.tmp.XXXXXX")"
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == "$key="* ]]; then
      if [[ "$found" == false ]]; then
        printf '%s=%s\n' "$key" "$value" >>"$temporary"
        found=true
      fi
    else
      printf '%s\n' "$line" >>"$temporary"
    fi
  done <"$ENV_FILE"
  if [[ "$found" == false ]]; then
    printf '%s=%s\n' "$key" "$value" >>"$temporary"
  fi
  chmod 600 "$temporary"
  mv -f -- "$temporary" "$ENV_FILE"
}

refresh_effective_env() {
  if [[ -n "$EFFECTIVE_ENV_FILE" && -f "$EFFECTIVE_ENV_FILE" ]]; then
    rm -f -- "$EFFECTIVE_ENV_FILE"
  fi
  EFFECTIVE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-remote-env.XXXXXX")"
  sparkclaw_merge_profile_env "$PRODUCT_ENV" "$MODE_ENV" "$ENV_FILE" "$EFFECTIVE_ENV_FILE"
}

configure_credentials() {
  local current answer
  current="$(sparkclaw_profile_value "$PRODUCT_ENV" "$MODE_ENV" "$ENV_FILE" OPENAI_API_KEY '')"
  if [[ -n "$current" ]]; then
    printf 'Shared remote-model API key (hidden; Enter keeps current, - removes it): ' >&2
  else
    printf 'Shared remote-model API key (optional, hidden; Enter keeps it empty): ' >&2
  fi
  IFS= read -r -s answer || fail "input ended while reading the optional API key"
  printf '\n' >&2
  if [[ -z "$answer" ]]; then
    answer="$current"
  elif [[ "$answer" == "-" ]]; then
    answer=""
  fi
  if [[ "$answer" =~ [[:space:]] || "$answer" == *'$'* || "$answer" == *'#'* ]]; then
    fail "the API key contains unsupported whitespace, $, or # characters"
  fi
  set_dotenv_value OPENAI_API_KEY "$answer"
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
  [[ -f "$ENV_FILE" ]] || fail "remote private environment file not found: $ENV_FILE"
else
  if [[ ! -f "$ENV_FILE" ]]; then
    [[ -d "$(dirname "$ENV_FILE")" ]] || fail "environment directory does not exist: $(dirname "$ENV_FILE")"
    install -m 600 /dev/null "$ENV_FILE"
    log "created private override file: $ENV_FILE"
  else
    chmod go-rwx "$ENV_FILE"
    log "preserving private overrides in $ENV_FILE"
  fi
  set_dotenv_value SPARKCLAW_CONTAINER_UID "$(id -u)"
  set_dotenv_value SPARKCLAW_CONTAINER_GID "$(id -g)"
  set_dotenv_value SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT "$ROOT/data/workspaces"
  if [[ -z "$(sparkclaw_profile_value "$PRODUCT_ENV" "$MODE_ENV" "$ENV_FILE" SPARKCLAW_WEBCHAT_PROXY_TOKEN '')" ]]; then
    set_dotenv_value SPARKCLAW_WEBCHAT_PROXY_TOKEN "$(sparkclaw_generate_webchat_proxy_token)"
    log "created the private WebChat-to-Gateway pairing credential"
  fi
  if [[ "$CONFIGURE_CREDENTIALS" == true ]]; then
    configure_credentials
  fi
  for directory in data/workspaces data/traces data/artifacts data/logs data/memory data/eval; do
    mkdir -p "$directory"
    [[ -w "$directory" ]] || fail "$ROOT/$directory is not writable by $(id -un)"
    chmod u+rwx "$directory"
  done
fi

refresh_effective_env
sparkclaw_validate_product_profile remote "$PRODUCT_ENV" "$MODE_ENV" "$ENV_FILE" || fail "invalid remote product profile"

if [[ "$MODE" == "check" ]]; then
  sparkclaw_check_host_browser "$ROOT" "$EFFECTIVE_ENV_FILE"
else
  sparkclaw_install_host_browser "$ROOT" "$ENV_FILE"
  refresh_effective_env
fi

if ! docker_installed; then
  [[ "$MODE" != "check" ]] || fail "Docker Engine and the Compose plugin are required"
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
    sudo -n "${DOCKER_BIN:-docker}" ps >/dev/null 2>&1 ||
      fail "Docker is unavailable; configure Docker access or passwordless sudo"
  else
    sudo -v
    sudo "${DOCKER_BIN:-docker}" ps >/dev/null
  fi
fi

export SPARKCLAW_REMOTE_ENV_FILE="$ENV_FILE"
if [[ "$MODE" == "check" ]]; then
  bash "$ROOT/scripts/start_remote_compose.sh" --check
  log "Ubuntu remote deployment check passed"
  exit 0
fi

bash "$ROOT/scripts/start_remote_compose.sh"

webchat_port="$(sparkclaw_profile_value "$PRODUCT_ENV" "$MODE_ENV" "$ENV_FILE" SPARKCLAW_WEBCHAT_PORT 18790)"
vm_address="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
if [[ -n "$vm_address" ]]; then
  log "WebChat: http://${vm_address}:${webchat_port}"
else
  log "WebChat port: $webchat_port"
fi
log "deployment complete"
