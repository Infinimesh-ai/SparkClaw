#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
profile_dir="${XDG_DATA_HOME:-$HOME/.local/share}/sparkclaw/browser/default/user-data"
case "${1:-}" in
  --check)
    python3 "$ROOT/scripts/browser_components.py" check
    python3 "$ROOT/scripts/browser_components.py" check-profile "$profile_dir"
    exit 0 ;;
  '') ;;
  *) echo 'usage: install-browser-components.sh [--check]' >&2; exit 2 ;;
esac
stage="$(mktemp -d "${TMPDIR:-/tmp}/sparkclaw-browser-components.XXXXXX")"
restore_browser=false
cleanup() {
  code=$?
  rm -rf -- "$stage"
  if [[ $code -ne 0 && "$restore_browser" == true ]]; then
    systemctl --user start sparkclaw-browser.service || true
  fi
}
trap cleanup EXIT
current=false
if python3 "$ROOT/scripts/browser_components.py" check >/dev/null 2>&1; then
  # Reuse only a completely verified deployment. Identical release inputs now
  # stage identical bytes, but reuse also avoids unnecessary public-file writes.
  # Real manifest/source/policy changes still fail check and are staged.
  current=true
  printf 'Reusing verified pinned browser components and managed policy\n'
else
  python3 "$ROOT/scripts/browser_components.py" stage "$stage"
fi
if systemctl --user is-active --quiet sparkclaw-browser.service; then
  restore_browser=true
  systemctl --user stop sparkclaw-browser.service
fi
if [[ "$current" == false ]]; then
  sudo -n python3 "$ROOT/scripts/browser_components.py" install-system "$stage"
fi
python3 "$ROOT/scripts/browser_components.py" prepare-profile "$profile_dir"
# setup-browser-controller.sh restarts the dedicated profile after launcher setup.
