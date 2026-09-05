#!/usr/bin/env bash
set -Eeuo pipefail

launcher="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/bin/sparkclaw-browser"
[[ -x "$launcher" ]] || {
  printf 'SparkClaw Browser is not installed; run npm run setup:browser first\n' >&2
  exit 1
}
exec "$launcher" open
