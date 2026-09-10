#!/usr/bin/env bash
# Called after Docker access is verified but before any product services start.
# Arguments are ROOT followed by the already-selected Docker command (including sudo).
sparkclaw_begin_email_deployment() {
  local deploy_root="$1"
  shift
  local old_volumes old_containers
  old_volumes="$("$@" volume ls --filter label=com.docker.compose.project=sparkclaw --format '{{.Name}}')" || return 1
  old_containers="$("$@" ps -a --filter label=com.docker.compose.project=sparkclaw --format '{{.ID}}')" || return 1
  local legacy_args=()
  if [[ -n "$old_volumes" || -n "$old_containers" || -f "$deploy_root/data/memory/gateway-credentials.key" || -f "$deploy_root/data/memory/gateway.db" ]]; then
    legacy_args=(--legacy)
  fi
  python3 "$deploy_root/scripts/record-deployment.py" begin "$deploy_root/data/workspaces" "${legacy_args[@]}"
}
