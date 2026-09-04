#!/usr/bin/env bash

sparkclaw_profile_value() {
  local product_file="$1"
  local mode_file="$2"
  local private_file="$3"
  local key="$4"
  local default_value="$5"

  sparkclaw_dotenv_key_valid "$key" || return 2
  if sparkclaw_dotenv_has_key "$private_file" "$key"; then
    sparkclaw_dotenv_value "$private_file" "$key"
  elif sparkclaw_dotenv_has_key "$mode_file" "$key"; then
    sparkclaw_dotenv_value "$mode_file" "$key"
  elif sparkclaw_dotenv_has_key "$product_file" "$key"; then
    sparkclaw_dotenv_value "$product_file" "$key"
  else
    printf '%s' "$default_value"
  fi
}

sparkclaw_merge_profile_env() {
  local product_file="$1"
  local mode_file="$2"
  local private_file="$3"
  local output_file="$4"
  local intermediate=""

  [[ -f "$product_file" && -f "$mode_file" ]] || return 1
  if [[ -f "$private_file" ]]; then
    intermediate="$(mktemp "${TMPDIR:-/tmp}/sparkclaw-mode-env.XXXXXX")"
    sparkclaw_dotenv_merge_missing "$mode_file" "$private_file" "$intermediate" >/dev/null || {
      rm -f -- "$intermediate"
      return 1
    }
    sparkclaw_dotenv_merge_missing "$product_file" "$intermediate" "$output_file" >/dev/null || {
      rm -f -- "$intermediate"
      return 1
    }
    rm -f -- "$intermediate"
  else
    sparkclaw_dotenv_merge_missing "$product_file" "$mode_file" "$output_file" >/dev/null
  fi
  chmod 600 "$output_file"
}

sparkclaw_export_profile_env() {
  local env_file="$1"
  local line key value
  local -a passthrough_keys=(
    SPARKCLAW_JINGSI_LAN_ENABLED
    SPARKCLAW_JINGSI_LAN_BIND
    SPARKCLAW_JINGSI_LAN_PORT
    SPARKCLAW_JINGSI_SESSION_ID
  )
  local -A passthrough_values=()
  local -A passthrough_present=()

  [[ -f "$env_file" ]] || return 1
  for key in "${passthrough_keys[@]}"; do
    if [[ -v "$key" ]]; then
      passthrough_present["$key"]=1
      passthrough_values["$key"]="${!key}"
    fi
  done
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -n "$line" && "$line" != \#* && "$line" == *=* ]] || continue
    key="${line%%=*}"
    sparkclaw_dotenv_key_valid "$key" || return 2
    value="$(sparkclaw_dotenv_value "$env_file" "$key")"
    printf -v "$key" '%s' "$value"
    export "$key"
  done <"$env_file"
  for key in "${passthrough_keys[@]}"; do
    if [[ "${passthrough_present[$key]:-}" == 1 ]]; then
      printf -v "$key" '%s' "${passthrough_values[$key]}"
      export "$key"
    fi
  done
}

sparkclaw_generate_webchat_proxy_token() {
  python3 - <<'PY'
import secrets

print(secrets.token_urlsafe(32))
PY
}

sparkclaw_private_override_allowed() {
  local key="$1"

  case "$key" in
    HF_TOKEN|HUGGING_FACE_HUB_TOKEN|OPENAI_API_KEY) return 0 ;;
    SPARKCLAW_CONTAINER_UID|SPARKCLAW_CONTAINER_GID) return 0 ;;
    SPARKCLAW_WEBCHAT_BIND|SPARKCLAW_WEBCHAT_PORT|SPARKCLAW_WEBCHAT_PROXY_TOKEN) return 0 ;;
    SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT) return 0 ;;
    SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST|SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE|SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST|SPARKCLAW_BROWSER_CDP_PROFILE_ID|SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS) return 0 ;;
    SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST|SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET|SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST|SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID|SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS) return 0 ;;
    SPARKCLAW_AUTOSTART_ENABLED|SPARKCLAW_AUTOSTART_READY_TIMEOUT_SECONDS|SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS|SPARKCLAW_FORCE_MODEL_RECREATE|SPARKCLAW_MODEL_CACHE|SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS) return 0 ;;
    SPARKCLAW_STATE_ENCRYPTION_KEY|SPARKCLAW_STATE_ENCRYPTION_KEY_FILE|SPARKCLAW_CREDENTIAL_KEY) return 0 ;;
    SPARKCLAW_S3_ACCESS_KEY|SPARKCLAW_S3_SECRET_KEY) return 0 ;;
    SPARKCLAW_WEB_SEARCH_ENABLED|SPARKCLAW_INFINIMESH_INFO_BASE_URL|SPARKCLAW_INFINIMESH_INFO_LICENSE_ID|SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY|SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE) return 0 ;;
    SPARKCLAW_ISCP_PAIRING_ENABLED|SPARKCLAW_ISCP_DOMAIN_ID|SPARKCLAW_ISCP_AUTHORITY_URL|SPARKCLAW_ISCP_AUTHORITY_TOKEN_FILE|SPARKCLAW_ISCP_AUTHORITY_TOKEN|LOCALMIND_MCP_URL|LOCALMIND_MCP_TOKEN) return 0 ;;
  esac
  return 1
}

sparkclaw_validate_env_file_unique() {
  local env_file="$1"
  local label="$2"
  local line key
  local -A seen=()

  [[ -f "$env_file" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -n "$line" && "$line" != \#* && "$line" == *=* ]] || continue
    key="${line%%=*}"
    sparkclaw_dotenv_key_valid "$key" || {
      printf 'invalid key in %s: %s\n' "$label" "$key" >&2
      return 1
    }
    if [[ -n "${seen[$key]:-}" ]]; then
      printf '%s contains duplicate key %s\n' "$label" "$key" >&2
      return 1
    fi
    seen["$key"]=1
  done <"$env_file"
}

sparkclaw_validate_profile_layers() {
  local product_file="$1"
  local mode_file="$2"
  local private_file="$3"
  local line key

  [[ -f "$product_file" && -f "$mode_file" ]] || return 1
  sparkclaw_validate_env_file_unique "$product_file" "product profile" || return 1
  sparkclaw_validate_env_file_unique "$mode_file" "mode profile" || return 1
  sparkclaw_validate_env_file_unique "$private_file" "private environment" || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -n "$line" && "$line" != \#* && "$line" == *=* ]] || continue
    key="${line%%=*}"
    sparkclaw_dotenv_key_valid "$key" || {
      printf 'invalid key in mode profile: %s\n' "$key" >&2
      return 1
    }
    if sparkclaw_dotenv_has_key "$product_file" "$key"; then
      printf '%s must have one owner; it appears in product and mode profiles\n' "$key" >&2
      return 1
    fi
  done <"$mode_file"

  [[ -f "$private_file" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -n "$line" && "$line" != \#* && "$line" == *=* ]] || continue
    key="${line%%=*}"
    sparkclaw_dotenv_key_valid "$key" || {
      printf 'invalid key in private environment: %s\n' "$key" >&2
      return 1
    }
    if ! sparkclaw_private_override_allowed "$key"; then
      printf '%s is not a supported credential or machine override in %s\n' "$key" "$private_file" >&2
      return 1
    fi
  done <"$private_file"
}

sparkclaw_http_url_host() {
  local value="$1"

  python3 - "$value" <<'PY'
from urllib.parse import urlsplit
import sys

value = sys.argv[1]
try:
    parsed = urlsplit(value)
    port = parsed.port
except ValueError:
    raise SystemExit(1)
if (
    parsed.scheme not in {"http", "https"}
    or not parsed.hostname
    or parsed.username is not None
    or parsed.password is not None
    or parsed.query
    or parsed.fragment
    or any(character.isspace() for character in value)
):
    raise SystemExit(1)
if port is not None and not 1 <= port <= 65535:
    raise SystemExit(1)
print(parsed.hostname.rstrip(".").lower())
PY
}

sparkclaw_remote_host_valid() {
  local host="${1,,}"

  python3 - "$host" <<'PY'
import ipaddress
import sys

host = sys.argv[1]
blocked_names = {
    "localhost",
    "host.docker.internal",
    "gateway.docker.internal",
    "docker.for.mac.localhost",
}
blocked_suffixes = (
    ".localhost",
    ".local",
    ".internal",
    ".lan",
    ".home",
    ".home.arpa",
)
if (
    host in blocked_names
    or host.endswith(blocked_suffixes)
    or host.startswith("sparkclaw-")
    or "." not in host and ":" not in host
):
    raise SystemExit(1)
try:
    address = ipaddress.ip_address(host)
except ValueError:
    raise SystemExit(0)
if not address.is_global:
    raise SystemExit(1)
PY
}

sparkclaw_local_model_host_valid() {
  local key="$1"
  local host="${2,,}"
  local expected=""

  case "$key" in
    SPARKCLAW_FAST_BASE_URL|SPARKCLAW_DEEP_BASE_URL) expected="sparkclaw-fast" ;;
    SPARKCLAW_EMBEDDING_BASE_URL) expected="sparkclaw-embedding" ;;
    SPARKCLAW_GUARD_BASE_URL) expected="sparkclaw-guard" ;;
    SPARKCLAW_SPEECH_BASE_URL) expected="sparkclaw-asr" ;;
    SPARKCLAW_OCR_BASE_URL) expected="sparkclaw-ocr" ;;
    *) return 2 ;;
  esac
  [[ "$host" == "$expected" ]]
}

sparkclaw_validate_product_profile() {
  local expected_profile="$1"
  local product_file="$2"
  local mode_file="$3"
  local private_file="$4"
  local key value host
  local -a url_keys=(
    SPARKCLAW_FAST_BASE_URL
    SPARKCLAW_DEEP_BASE_URL
    SPARKCLAW_EMBEDDING_BASE_URL
    SPARKCLAW_GUARD_BASE_URL
    SPARKCLAW_SPEECH_BASE_URL
    SPARKCLAW_OCR_BASE_URL
  )
  local -a model_keys=(
    SPARKCLAW_FAST_MODEL
    SPARKCLAW_DEEP_MODEL
    SPARKCLAW_EMBEDDING_MODEL
    SPARKCLAW_GUARD_MODEL
    SPARKCLAW_SPEECH_MODEL
    SPARKCLAW_OCR_MODEL
  )

  sparkclaw_validate_profile_layers "$product_file" "$mode_file" "$private_file" || return 1
  value="$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_DEPLOYMENT_PROFILE '')"
  [[ "$value" == "$expected_profile" ]] || {
    printf 'SPARKCLAW_DEPLOYMENT_PROFILE must be %s for this entrypoint\n' "$expected_profile" >&2
    return 1
  }
  [[ "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_MODEL_MODE '')" == "external" ]] || {
    printf 'SPARKCLAW_MODEL_MODE must be external\n' >&2
    return 1
  }
  [[ "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_STATE_BACKEND '')" == "postgres" ]] || {
    printf 'SPARKCLAW_STATE_BACKEND must be postgres\n' >&2
    return 1
  }
  [[ "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_MODEL_CAPACITY_PROFILE '')" == "sparkclaw-product-v1" ]] || {
    printf 'SPARKCLAW_MODEL_CAPACITY_PROFILE must be sparkclaw-product-v1\n' >&2
    return 1
  }
  [[ "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_FAST_BASE_URL '')" == \
    "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_DEEP_BASE_URL '')" ]] || {
    printf 'SPARKCLAW_DEEP_BASE_URL must match SPARKCLAW_FAST_BASE_URL in both product modes\n' >&2
    return 1
  }
  [[ -z "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_API_TOKEN '')" ]] || {
    printf 'SPARKCLAW_API_TOKEN is disabled for product entrypoints\n' >&2
    return 1
  }
  [[ "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_PAIRING_REQUIRED '')" == "true" ]] || {
    printf 'SPARKCLAW_PAIRING_REQUIRED must be true for product entrypoints\n' >&2
    return 1
  }
  value="$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_WEBCHAT_PROXY_TOKEN '')"
  [[ "$value" =~ ^[A-Za-z0-9_-]{43,128}$ ]] || {
    printf 'SPARKCLAW_WEBCHAT_PROXY_TOKEN must be a 43-128 character private base64url token; rerun the deployment entrypoint\n' >&2
    return 1
  }
  for key in \
    SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES:200000 \
    SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES:72000 \
    SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES:96000; do
    value="${key#*:}"
    key="${key%%:*}"
    [[ "$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" "$key" '')" == "$value" ]] || {
      printf '%s must be %s in both product modes\n' "$key" "$value" >&2
      return 1
    }
  done
  for key in SPARKCLAW_SPEECH_ENABLED SPARKCLAW_OCR_ENABLED; do
    value="$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" "$key" '')"
    [[ "${value,,}" == "true" ]] || {
      printf '%s must be true in the %s product profile\n' "$key" "$expected_profile" >&2
      return 1
    }
  done
  for key in "${url_keys[@]}"; do
    value="$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" "$key" '')"
    host="$(sparkclaw_http_url_host "$value")" || {
      printf '%s must be a valid HTTP(S) URL without credentials, query, or fragment\n' "$key" >&2
      return 1
    }
    if [[ "$expected_profile" == "remote" ]]; then
      sparkclaw_remote_host_valid "$host" || {
        printf '%s must not use a local model address (%s)\n' "$key" "$host" >&2
        return 1
      }
    else
      sparkclaw_local_model_host_valid "$key" "$host" || {
        printf '%s must use the local Compose model service, not %s\n' "$key" "$host" >&2
        return 1
      }
    fi
  done
  value="$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" SPARKCLAW_SPEECH_BASE_URL '')"
  [[ "${value%/}" != */v1 ]] || {
    printf 'SPARKCLAW_SPEECH_BASE_URL must be the service root; the runtime appends /v1/audio/transcriptions\n' >&2
    return 1
  }
  for key in "${model_keys[@]}"; do
    value="$(sparkclaw_profile_value "$product_file" "$mode_file" "$private_file" "$key" '')"
    [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._/+:-]*$ ]] || {
      printf '%s is missing or invalid\n' "$key" >&2
      return 1
    }
  done
}
