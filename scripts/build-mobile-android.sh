#!/usr/bin/env bash
# Build the SparkClaw node-issuer client library for Android (gomobile bind).
# Output: dist/mobile/android/sparkclawmobile.aar (+ sources jar). 64-bit ABIs only:
# the gateway dependency graph has constants that overflow 32-bit int.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/dist/mobile/android}"
ANDROID_API="${SPARKCLAW_ANDROID_API:-26}"
: "${ANDROID_HOME:=${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
export ANDROID_HOME
if [[ -z "${ANDROID_NDK_HOME:-}" && -d "$ANDROID_HOME/ndk" ]]; then
  ANDROID_NDK_HOME="$ANDROID_HOME/ndk/$(ls "$ANDROID_HOME/ndk" | sort -V | tail -1)"
  export ANDROID_NDK_HOME
fi
command -v gomobile >/dev/null || { echo "gomobile not found: go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20261011034528-197770444867 && gomobile init" >&2; exit 1; }
mkdir -p "$OUT"
cd "$ROOT/services/gateway/mobile"
# The mobile module is outside go.work on purpose (gomobile needs Go 1.26).
GOWORK=off gomobile bind -trimpath -ldflags="-s -w" \
  -target="${SPARKCLAW_ANDROID_TARGETS:-android/arm64,android/amd64}" -androidapi "$ANDROID_API" \
  -javapkg ai.infinimesh.sparkclaw \
  -o "$OUT/sparkclawmobile.aar" ./sparkclawmobile
echo "built $OUT/sparkclawmobile.aar"
