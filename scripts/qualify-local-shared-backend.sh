#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POSTGRES_IMAGE="${SPARKCLAW_QUALIFICATION_POSTGRES_IMAGE:-postgres:16-alpine}"
CURL_IMAGE="${SPARKCLAW_QUALIFICATION_CURL_IMAGE:-curlimages/curl:latest}"
CONTAINER_NAME="sparkclaw-dual-client-$PPID-$$"
DATABASE_PASSWORD="sparkclaw-qualification"

cleanup() {
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker run --rm --detach --name "$CONTAINER_NAME" \
  --env POSTGRES_USER=sparkclaw \
  --env POSTGRES_PASSWORD="$DATABASE_PASSWORD" \
  --env POSTGRES_DB=sparkclaw \
  --publish 127.0.0.1::5432 \
  "$POSTGRES_IMAGE" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$CONTAINER_NAME" pg_isready --username sparkclaw --dbname sparkclaw >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$CONTAINER_NAME" pg_isready --username sparkclaw --dbname sparkclaw >/dev/null

MAPPED_PORT="$(docker port "$CONTAINER_NAME" 5432/tcp | sed -n 's/.*://p' | head -n 1)"
if [[ ! "$MAPPED_PORT" =~ ^[0-9]+$ ]]; then
  echo "could not resolve the isolated PostgreSQL port" >&2
  exit 1
fi
LAN_HOST="${SPARKCLAW_QUALIFICATION_LAN_HOST:-$(ip -4 route get 1.1.1.1 | sed -n 's/.* src \([^ ]*\).*/\1/p' | head -n 1)}"
if [[ -z "$LAN_HOST" || "$LAN_HOST" == 127.* ]]; then
  echo "a non-loopback SPARKCLAW_QUALIFICATION_LAN_HOST is required" >&2
  exit 1
fi

cd "$ROOT_DIR"
SPARKCLAW_DUAL_CLIENT_POSTGRES_DSN="postgres://sparkclaw:${DATABASE_PASSWORD}@127.0.0.1:${MAPPED_PORT}/sparkclaw?sslmode=disable" \
SPARKCLAW_DUAL_CLIENT_LAN_HOST="$LAN_HOST" \
SPARKCLAW_DUAL_CLIENT_CURL_IMAGE="$CURL_IMAGE" \
go test ./services/gateway/internal/gateway -run '^TestLocalSharedBackendDualClientQualification$' -count=1 -v
