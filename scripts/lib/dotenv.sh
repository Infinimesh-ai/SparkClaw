#!/usr/bin/env bash

sparkclaw_dotenv_key_valid() {
  [[ "${1:-}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]
}

sparkclaw_dotenv_has_key() {
  local env_file="$1"
  local key="$2"
  local line=""

  sparkclaw_dotenv_key_valid "$key" || return 2
  [[ -f "$env_file" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" == "$key="* ]]; then
      return 0
    fi
  done <"$env_file"
  return 1
}

sparkclaw_dotenv_value() {
  local env_file="$1"
  local key="$2"
  local line=""
  local candidate=""
  local value=""

  sparkclaw_dotenv_key_valid "$key" || return 2
  if [[ -f "$env_file" ]]; then
    while IFS= read -r candidate || [[ -n "$candidate" ]]; do
      if [[ "$candidate" == "$key="* ]]; then
        line="$candidate"
      fi
    done <"$env_file"
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

sparkclaw_resolve_env_value() {
  local env_file="$1"
  local key="$2"
  local default_value="$3"

  sparkclaw_dotenv_key_valid "$key" || return 2
  if [[ -v "$key" ]]; then
    printf '%s' "${!key}"
  elif sparkclaw_dotenv_has_key "$env_file" "$key"; then
    sparkclaw_dotenv_value "$env_file" "$key"
  else
    printf '%s' "$default_value"
  fi
}

sparkclaw_dotenv_merge_missing() {
  local template_file="$1"
  local env_file="$2"
  local output_file="$3"
  local line=""
  local key=""
  local added=0

  [[ -f "$template_file" && -f "$env_file" ]] || return 1
  [[ "$output_file" != "$template_file" && "$output_file" != "$env_file" ]] || return 2

  : >"$output_file"
  while IFS= read -r line || [[ -n "$line" ]]; do
    printf '%s\n' "$line" >>"$output_file"
  done <"$env_file"

  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -n "$line" && "$line" != \#* && "$line" == *=* ]] || continue
    key="${line%%=*}"
    sparkclaw_dotenv_key_valid "$key" || return 2
    if ! sparkclaw_dotenv_has_key "$output_file" "$key"; then
      printf '%s\n' "$line" >>"$output_file"
      added=$((added + 1))
    fi
  done <"$template_file"
  printf '%s' "$added"
}

sparkclaw_tcp_port_valid() {
  local value="${1:-}"

  [[ "$value" =~ ^[1-9][0-9]{0,4}$ ]] || return 1
  (( 10#$value <= 65535 ))
}
