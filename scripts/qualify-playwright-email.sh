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
controller_socket=""
effective_env=""

usage() {
  cat <<'EOF'
Usage: bash scripts/qualify-playwright-email.sh [options]

Run the fixed Playwright Extension login probes through the installed host
controller. The default qualification does not send mail.
Set SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_DISCOVER=1 for read-only list discovery.
Also set SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_TIME_RANGE=1 to exercise a one-hour
timeline query. A passing smoke is not full incremental coverage qualification.
Set SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_READ=1 and SPARKCLAW_TEST_EMAIL_OWNER_ID
to also capture one unread inbox message per provider and verify its local files.
Set SPARKCLAW_TEST_NOTIFICATION_OBSERVE=1 only for an authorized real-mail
qualification, with SPARKCLAW_TEST_NOTIFICATION_OBSERVER_SOCKET and
SPARKCLAW_NOTIFICATION_OBSERVER_READY_DIR set to an isolated observer runtime.
This mode sends one uniquely marked Gmail-to-QQ test message by default.
SPARKCLAW_TEST_NOTIFICATION_ROUTE=sender:receiver selects any two distinct
qq_mail, gmail or outlook providers. Set SPARKCLAW_TEST_NOTIFICATION_DRY_RUN=1
to observe without sending. SPARKCLAW_TEST_NOTIFICATION_DURATION_MS bounds
the observation window (1000..120000). To verify an already sent message's
original without sending, set SPARKCLAW_TEST_NOTIFICATION_RECONCILE_MARKER.
For a no-send Reader concurrency control, set
SPARKCLAW_TEST_NOTIFICATION_DRY_RUN=1 and
SPARKCLAW_TEST_NOTIFICATION_TRIGGER_READ=1. Also set
SPARKCLAW_TEST_NOTIFICATION_QUERY_ONLY=1 for the same bounded query without
starting an observer. SPARKCLAW_TEST_NOTIFICATION_SAME_CONTROLLER=1 runs the
Reader query through the isolated observer Controller to test provider-slot
coordination. These controls never change receiving settings.

For the resident Controller capability use SPARKCLAW_TEST_RESIDENT_NOTIFICATION=1
and --controller-socket pointing to an isolated Controller built from this tree.
It starts all selected watchers, observes 30 idle seconds, runs a real Reader
query per provider through that same Controller, and stops its owned watchers.
SPARKCLAW_TEST_RESIDENT_HOLD_SECONDS=1..900 retains observation for authorized
manual mutual sends. SPARKCLAW_MAIL_OBSERVER_EVIDENCE=1 on the Controller enables
bounded redacted protocol evidence (off by default).
While that hold is active, SPARKCLAW_TEST_NOTIFICATION_RESIDENT_SEND=1 plus the
NOTIFICATION_OBSERVE/ROUTE settings sends once without the old blocking probe.
For an Outlook recipient, SPARKCLAW_TEST_NOTIFICATION_OUTLOOK_IDENTITY must name
a private JSON proof exported from a verified outgoing original with
SPARKCLAW_TEST_NOTIFICATION_RECEIPT_IDENTITY; a login alias is insufficient.
All uncertain sends are reconciled by unique-marker original, never repeated.

Options:
  --profile local|remote       Product profile to load (default: remote)
  --env-file PATH              Private profile overrides (default: .env.<profile>)
  --providers LIST             Comma-separated qq_mail,outlook,gmail subset
  --credential-key-file PATH   Host Vault key file override
  --controller-socket PATH     Isolated Controller socket for code qualification
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
    --controller-socket)
      shift
      [[ $# -gt 0 ]] || fail "--controller-socket requires a path"
      controller_socket="$1"
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

host_socket="${controller_socket:-${SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST:-}}"
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
export SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT="${SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT:-${SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT:-$ROOT/data/workspaces}}"

printf '[sparkclaw-playwright-email] profile=%s providers=%s\n' "$profile" "$providers"
cd "$ROOT/services/gateway"
live_test='^TestPlaywrightExtensionLiveEmailProbes$'
if [[ "${SPARKCLAW_TEST_QQ_GMAIL_TIMELINE:-}" == 1 ]]; then
  live_test='^TestQQGmailTimelineLiveQualification$'
fi
if [[ "${SPARKCLAW_TEST_EMAIL_PERFORMANCE:-}" == 1 ]]; then
  live_test='^TestTimelineLivePerformance$'
fi
if [[ "${SPARKCLAW_TEST_NOTIFICATION_OBSERVE:-}" == 1 ]]; then
  live_test='^TestEmailNotificationMutualSendLive$'
fi
if [[ "${SPARKCLAW_TEST_RESIDENT_NOTIFICATION:-}" == 1 ]]; then
  live_test='^TestEmailResidentNotificationLive$'
fi
go test -timeout=30m -count=1 -run "$live_test" -v ./internal/emailautomation
