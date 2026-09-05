#!/usr/bin/env bash
set -Eeuo pipefail
set +x
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/dotenv.sh"
source "$ROOT/scripts/lib/deployment-profile.sh"

profile="remote"
env_file=""
providers="qq_mail,outlook,gmail"
credential_key_file=""
effective_env=""

usage() {
  cat <<'EOF'
Usage: bash scripts/qualify-playwright-email.sh [options]

Run the fixed Playwright Extension login probes through the installed host
controller. This command never invokes an email send script.

Options:
  --profile local|remote       Product profile to load (default: remote)
  --env-file PATH              Private profile overrides (default: .env.<profile>)
  --providers LIST             Comma-separated qq_mail,outlook,gmail subset
  --credential-key-file PATH   Host Vault key file override
  -h, --help                   Show this help
EOF
}

fail() {
  printf '[sparkclaw-playwright-email] error: %s\n' "$*" >&2
  exit 1
}

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

normalize_providers() {
  local value="$1"
  local raw=""
  local provider=""
  local normalized=""
  local -a entries=()
  local -A seen=()

  IFS=',' read -r -a entries <<<"$value"
  (( ${#entries[@]} > 0 )) || fail "--providers must not be empty"
  for raw in "${entries[@]}"; do
    provider="$(trim "$raw")"
    provider="${provider,,}"
    case "$provider" in
      qq_mail|outlook|gmail) ;;
      *) fail "unsupported provider in --providers" ;;
    esac
    [[ -z "${seen[$provider]:-}" ]] || fail "--providers contains a duplicate provider"
    seen["$provider"]=1
    normalized+="${normalized:+,}$provider"
  done
  printf '%s' "$normalized"
}

cleanup() {
  [[ -z "$effective_env" || ! -e "$effective_env" ]] || unlink "$effective_env"
}
trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      shift
      [[ $# -gt 0 ]] || fail "--profile requires local or remote"
      profile="$1"
      ;;
    --env-file)
      shift
      [[ $# -gt 0 ]] || fail "--env-file requires a path"
      env_file="$1"
      ;;
    --providers)
      shift
      [[ $# -gt 0 ]] || fail "--providers requires a provider list"
      providers="$1"
      ;;
    --credential-key-file)
      shift
      [[ $# -gt 0 ]] || fail "--credential-key-file requires a path"
      credential_key_file="$1"
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

case "$profile" in
  local|remote) ;;
  *) fail "--profile must be local or remote" ;;
esac
providers="$(normalize_providers "$providers")"

product_env="$ROOT/docker/env/sparkclaw.product.env"
mode_env="$ROOT/docker/env/sparkclaw.$profile.env"
if [[ -z "$env_file" ]]; then
  env_file="$ROOT/.env.$profile"
else
  env_file="$(realpath -m "$env_file")"
fi

[[ -f "$env_file" ]] || fail "private environment file is missing"
sparkclaw_validate_product_profile "$profile" "$product_env" "$mode_env" "$env_file" ||
  fail "$profile product profile is invalid"

effective_env="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-playwright-email.XXXXXX.env")"
sparkclaw_merge_profile_env "$product_env" "$mode_env" "$env_file" "$effective_env" ||
  fail "could not build the effective product environment"
sparkclaw_export_profile_env "$effective_env" || fail "could not load the effective product environment"
unset SPARKCLAW_POSTGRES_DSN

[[ "${SPARKCLAW_STATE_BACKEND:-}" == "postgres" ]] ||
  fail "live qualification requires the product PostgreSQL state backend"
case "${SPARKCLAW_STATE_DSN:-}" in
  postgres://*@postgres:5432/*)
    export SPARKCLAW_STATE_DSN="${SPARKCLAW_STATE_DSN/@postgres:5432/@127.0.0.1:15432}"
    ;;
  postgres://*@127.0.0.1:15432/*) ;;
  *) fail "product PostgreSQL DSN does not use the supported container or host endpoint" ;;
esac

host_socket="${SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST:-}"
[[ "$host_socket" == /* && -S "$host_socket" ]] ||
  fail "host browser-controller socket is unavailable"
export SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET="$host_socket"

if [[ -n "$credential_key_file" ]]; then
  credential_key_file="$(realpath -m "$credential_key_file")"
  export SPARKCLAW_CREDENTIAL_KEY=
elif [[ -n "${SPARKCLAW_CREDENTIAL_KEY:-}" ]]; then
  credential_key_file=""
  export SPARKCLAW_CREDENTIAL_KEY_FILE=
else
  credential_key_file="$ROOT/data/memory/gateway-credentials.key"
fi
if [[ -n "$credential_key_file" ]]; then
  [[ -f "$credential_key_file" && -r "$credential_key_file" && -O "$credential_key_file" ]] ||
    fail "host Vault credential key file is unavailable"
  [[ "$(stat -c '%a' "$credential_key_file")" == "600" ]] ||
    fail "host Vault credential key file must be owner-only mode 0600"
  export SPARKCLAW_CREDENTIAL_KEY_FILE="$credential_key_file"
fi

command -v go >/dev/null 2>&1 || fail "go is required"
export SPARKCLAW_TEST_CONFIG="$ROOT/configs/sparkclaw.default.json"
export SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS="$providers"

printf '[sparkclaw-playwright-email] profile=%s providers=%s\n' "$profile" "$providers"
cd "$ROOT/services/gateway"
go test -count=1 -run '^TestPlaywrightExtensionLiveEmailProbes$' -v ./internal/emailautomation
