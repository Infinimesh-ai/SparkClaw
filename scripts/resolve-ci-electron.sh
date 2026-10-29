#!/usr/bin/env bash
set -euo pipefail
# Electron 44 lazily installs its binary on the first require(). Its installer
# inherits stdout, so keep the whole warm-up (including children) on stderr.
# The subsequent require returns only the actual executable path.
node -e 'require(process.argv[1])' "$1" >&2
node -p 'require(process.argv[1])' "$1"
