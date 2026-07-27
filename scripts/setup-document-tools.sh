#!/usr/bin/env bash
# Installs the Node/Python dependencies used by Gateway document adapters.
# Node packages use the root npm workspace; Python packages use the host
# user's standard site-packages directory.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST_DIR="$ROOT/tools/document-runtime"
cd "$ROOT"

if ! command -v npm >/dev/null 2>&1; then
  echo "npm is required to install Node document dependencies." >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1 || ! python3 -m pip --version >/dev/null 2>&1; then
  echo "python3 and pip are required to install Python document dependencies." >&2
  exit 1
fi

echo "Installing Node dependencies through the root npm workspace ..."
npm install --no-audit --no-fund

echo "Installing Python document dependencies into the host user site ..."
pip_args=(--quiet --user)
if python3 -m pip install --help | grep -q -- "--break-system-packages"; then
  pip_args+=(--break-system-packages)
fi
python3 -m pip install "${pip_args[@]}" -r "$MANIFEST_DIR/requirements.txt"

node -e 'for (const name of ["@mozilla/readability", "jsdom", "exceljs"]) require(name)'
python3 -c 'import docx, pptx, pypdf'

echo "Host document dependencies are ready."
