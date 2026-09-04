#!/usr/bin/env bash
set +x
set -Eeuo pipefail

umask 077

REPOSITORY_URL="${SPARKCLAW_REPOSITORY_URL:-https://github.com/Infinimesh-ai/SparkClaw.git}"
GIT_REF="${SPARKCLAW_GIT_REF:-main}"
INSTALL_DIR="${SPARKCLAW_INSTALL_DIR:-${HOME:-}/SparkClaw}"
BOOTSTRAP_TIMEOUT_SECONDS="${SPARKCLAW_BOOTSTRAP_TIMEOUT_SECONDS:-900}"
MODE="deploy"

usage() {
  cat <<'EOF'
Usage: curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
         --connect-timeout 15 --max-time 300 \
         https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/main/install-remote.sh | bash
       bash install-remote.sh [--check] [--configure]

Install or safely update SparkClaw, then deploy the Ubuntu full-remote runtime.
Public endpoints are versioned; optional credentials and machine overrides stay
only in the local mode-0600 .env.remote file.

Options:
  --check      Update the checkout and validate an existing deployment only
  --configure  Re-enter the private model configuration before deployment
  -h, --help   Show this help

Environment:
  SPARKCLAW_INSTALL_DIR                 Install path (default: $HOME/SparkClaw)
  SPARKCLAW_REPOSITORY_URL              HTTPS or SSH Git repository URL
  SPARKCLAW_GIT_REF                     Branch or tag (default: main)
  SPARKCLAW_BOOTSTRAP_TIMEOUT_SECONDS   Git network timeout (default: 900)
  SPARKCLAW_REMOTE_ENV_FILE             Optional private override file path
EOF
}

log() {
  printf '[sparkclaw-remote-install] %s\n' "$*"
}

fail() {
  printf '[sparkclaw-remote-install] error: %s\n' "$*" >&2
  exit 1
}

on_error() {
  local exit_code=$?
  local line="$1"
  trap - ERR
  printf '[sparkclaw-remote-install] failed at line %s (exit %s)\n' "$line" "$exit_code" >&2
  exit "$exit_code"
}

trap 'on_error "$LINENO"' ERR

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      [[ "$MODE" == "deploy" ]] || fail "--check and --configure cannot be combined"
      MODE="check"
      ;;
    --configure)
      [[ "$MODE" == "deploy" ]] || fail "--check and --configure cannot be combined"
      MODE="configure"
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

[[ "$(uname -s)" == "Linux" ]] || fail "the remote installer supports Ubuntu Linux only"
[[ -r /etc/os-release ]] || fail "cannot identify the operating system"
# shellcheck disable=SC1091
source /etc/os-release
[[ "${ID:-}" == "ubuntu" ]] || fail "the remote installer supports Ubuntu only; detected ${ID:-unknown}"
[[ "$EUID" -ne 0 ]] || fail "run the installer as a normal sudo-capable user, not as root"
[[ -n "${HOME:-}" ]] || fail "HOME is required to choose the default installation directory"
[[ "$BOOTSTRAP_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]] ||
  fail "SPARKCLAW_BOOTSTRAP_TIMEOUT_SECONDS must be a positive integer"

if ! command -v git >/dev/null 2>&1; then
  command -v sudo >/dev/null 2>&1 || fail "sudo is required to install Git"
  log "installing Git"
  sudo -v
  sudo env DEBIAN_FRONTEND=noninteractive apt-get update
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates git
fi

for command_name in git realpath timeout; do
  command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done

case "$REPOSITORY_URL" in
  https://*|ssh://*|git@*) ;;
  *) fail "SPARKCLAW_REPOSITORY_URL must use HTTPS or SSH" ;;
esac
case "$REPOSITORY_URL" in
  https://*@*) fail "credentials are not allowed inside SPARKCLAW_REPOSITORY_URL" ;;
  https://*[?#]*) fail "query strings and fragments are not allowed in SPARKCLAW_REPOSITORY_URL" ;;
esac
git check-ref-format --branch "$GIT_REF" >/dev/null 2>&1 ||
  fail "SPARKCLAW_GIT_REF is not a valid branch or tag name"

INSTALL_DIR="$(realpath -m "$INSTALL_DIR")"
user_home="$(realpath -m "$HOME")"
case "$INSTALL_DIR" in
  /|"$user_home") fail "refusing broad installation directory: $INSTALL_DIR" ;;
esac
unset user_home

git_with_timeout=(
  timeout --foreground --kill-after=10 "$BOOTSTRAP_TIMEOUT_SECONDS"
  env GIT_TERMINAL_PROMPT=0
  git
)

if [[ ! -e "$INSTALL_DIR" ]]; then
  mkdir -p "$(dirname "$INSTALL_DIR")"
  log "cloning SparkClaw ($GIT_REF) into $INSTALL_DIR"
  "${git_with_timeout[@]}" clone --branch "$GIT_REF" --single-branch -- \
    "$REPOSITORY_URL" "$INSTALL_DIR"
elif [[ -d "$INSTALL_DIR/.git" ]]; then
  configured_remote="$(git -C "$INSTALL_DIR" remote get-url origin)"
  [[ "$configured_remote" == "$REPOSITORY_URL" ]] ||
    fail "existing origin does not match SPARKCLAW_REPOSITORY_URL"
  if [[ -n "$(git -C "$INSTALL_DIR" status --porcelain --untracked-files=normal)" ]]; then
    fail "existing installation has local changes; preserve or remove them before updating $INSTALL_DIR"
  fi

  log "checking $INSTALL_DIR for a fast-forward update to $GIT_REF"
  "${git_with_timeout[@]}" -C "$INSTALL_DIR" fetch --tags origin "$GIT_REF"
  git -C "$INSTALL_DIR" merge-base --is-ancestor HEAD FETCH_HEAD ||
    fail "existing installation cannot fast-forward to $GIT_REF"
  git -C "$INSTALL_DIR" merge --ff-only FETCH_HEAD
else
  fail "installation path exists and is not a SparkClaw Git checkout: $INSTALL_DIR"
fi

deploy_script="$INSTALL_DIR/scripts/deploy_remote.sh"
[[ -f "$deploy_script" ]] || fail "remote deployment entrypoint not found after checkout: $deploy_script"
log "repository ready at $(git -C "$INSTALL_DIR" rev-parse --short HEAD)"

deploy_args=()
case "$MODE" in
  check) deploy_args+=(--check) ;;
  configure) deploy_args+=(--configure) ;;
esac

# A curl pipe owns stdin. Reattach the deployment to the controlling terminal
# so sudo and hidden credential prompts remain interactive.
if ( : </dev/tty ) 2>/dev/null; then
  exec bash "$deploy_script" "${deploy_args[@]}" </dev/tty
fi
exec bash "$deploy_script" "${deploy_args[@]}" </dev/null
