#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

bind="${SPARKCLAW_JINGSI_LAN_BIND:-}"
session_id="${SPARKCLAW_JINGSI_SESSION_ID:-}"
port="${SPARKCLAW_JINGSI_LAN_PORT:-18793}"

if [[ ! "$port" =~ ^[0-9]+$ ]] || ((10#$port < 1 || 10#$port > 65535)); then
  echo "SPARKCLAW_JINGSI_LAN_PORT must be a TCP port number" >&2
  exit 1
fi

if [[ -z "$bind" ]]; then
  echo "SPARKCLAW_JINGSI_LAN_BIND must be one literal RFC1918 host address" >&2
  exit 1
fi
if [[ -z "${session_id//[[:space:]]/}" ]]; then
  echo "SPARKCLAW_JINGSI_SESSION_ID must name one existing visible WebChat session" >&2
  exit 1
fi
if [[ ! "$bind" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
  echo "SPARKCLAW_JINGSI_LAN_BIND must be a literal IPv4 address" >&2
  exit 1
fi

IFS=. read -r first second third fourth <<<"$bind"
for octet in "$first" "$second" "$third" "$fourth"; do
  if [[ ${#octet} -gt 1 && ${octet:0:1} == 0 ]]; then
    echo "SPARKCLAW_JINGSI_LAN_BIND contains an ambiguous IPv4 octet" >&2
    exit 1
  fi
  if ((10#$octet > 255)); then
    echo "SPARKCLAW_JINGSI_LAN_BIND contains an invalid IPv4 octet" >&2
    exit 1
  fi
done
first=$((10#$first))
second=$((10#$second))
third=$((10#$third))
fourth=$((10#$fourth))
if ! ((first == 10 || first == 172 && second >= 16 && second <= 31 || first == 192 && second == 168)); then
  echo "SPARKCLAW_JINGSI_LAN_BIND must be an RFC1918 address" >&2
  exit 1
fi

echo "JingSi LAN binding validated on port $port"
if [[ "${1:-}" == "--check" ]]; then
  exit 0
fi
runtime_profile="${1:-remote}"
if [[ "$runtime_profile" != "remote" && "$runtime_profile" != "local" ]]; then
  echo "usage: $0 [remote|local]" >&2
  exit 1
fi

export SPARKCLAW_JINGSI_LAN_ENABLED=true
export SPARKCLAW_JINGSI_LAN_PORT="$port"
bash "scripts/start_${runtime_profile}_compose.sh" --jingsi-lan

for _ in $(seq 1 30); do
  if curl --noproxy '*' -fsS "http://$bind:$port/api/jingsi/v0/readyz" >/dev/null 2>&1; then
    echo "JingSi LAN presentation ready on the configured private address"
    exit 0
  fi
  sleep 2
done

echo "Timed out waiting for JingSi LAN readiness on the configured private address" >&2
exit 1
