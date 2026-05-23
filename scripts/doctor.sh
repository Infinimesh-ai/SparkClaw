#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "SparkClaw doctor"
echo "root=$ROOT"

check() {
  local name="$1"
  shift
  if "$@" >/tmp/sparkclaw-doctor.out 2>&1; then
    echo "ok  $name"
  else
    echo "err $name"
    cat /tmp/sparkclaw-doctor.out
    exit 1
  fi
}

check "go" go version
check "node" node --version
check "npm" npm --version
check "docker" docker --version
check "docker compose" docker compose version

for dir in data/workspaces data/traces data/artifacts data/logs data/memory data/eval configs docker; do
  if [[ ! -d "$dir" ]]; then
    echo "err missing $dir"
    exit 1
  fi
  echo "ok  $dir"
done

for file in scripts/serve_fast.sh scripts/serve_deep.sh benchmarks/model_baseline.md; do
  if [[ ! -f "$file" ]]; then
    echo "err missing $file"
    exit 1
  fi
  echo "ok  $file"
done

if command -v lsof >/dev/null 2>&1; then
  for port in 18789 18790 18889; do
    if lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      echo "warn port $port is already in use"
    else
      echo "ok  port $port available"
    fi
  done
fi

echo "done"
