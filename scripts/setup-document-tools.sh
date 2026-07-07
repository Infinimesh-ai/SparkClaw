#!/usr/bin/env bash
# Installs the document tool runtimes the gateway shells out to
# (see services/gateway/internal/toolhub/document_tools.go).
# Node modules land in .tools/document-node, Python packages in a
# .tools/document-python virtualenv — the exact paths the gateway probes.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NODE_DIR="$ROOT/.tools/document-node"
PY_DIR="$ROOT/.tools/document-python"
MANIFEST_DIR="$ROOT/tools/document-runtime"

if command -v npm >/dev/null 2>&1; then
  mkdir -p "$NODE_DIR"
  cp "$MANIFEST_DIR/package.json" "$NODE_DIR/package.json"
  echo "Installing Node document dependencies into $NODE_DIR ..."
  (cd "$NODE_DIR" && npm install --no-audit --no-fund)
else
  echo "npm not found; skipping Node document dependencies (xlsx tools will be unavailable)." >&2
fi

if command -v python3 >/dev/null 2>&1; then
  if [ ! -x "$PY_DIR/bin/python" ]; then
    echo "Creating Python virtualenv at $PY_DIR ..."
    python3 -m venv "$PY_DIR"
  fi
  echo "Installing Python document dependencies into $PY_DIR ..."
  "$PY_DIR/bin/pip" install --quiet --upgrade pip
  "$PY_DIR/bin/pip" install --quiet -r "$MANIFEST_DIR/requirements.txt"
else
  echo "python3 not found; skipping Python document dependencies (docx/pptx/pdf tools will be unavailable)." >&2
fi

echo "Document tool runtimes are ready."
