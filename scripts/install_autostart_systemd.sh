#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT_NAME="sparkclaw-autostart.service"
UNIT_DIR="${SPARKCLAW_SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
SYSTEMCTL_BIN="${SYSTEMCTL_BIN:-systemctl}"
MODE="install"

case "${1:-}" in
  "") ;;
  --check) MODE="check" ;;
  *)
    echo "usage: $0 [--check]" >&2
    exit 2
    ;;
esac
[[ $# -le 1 ]] || {
  echo "usage: $0 [--check]" >&2
  exit 2
}

[[ "$(uname -s)" == "Linux" ]] || {
  echo "SparkClaw boot autostart requires Linux with systemd" >&2
  exit 1
}
command -v "$SYSTEMCTL_BIN" >/dev/null 2>&1 || {
  echo "systemctl is required to install SparkClaw boot autostart" >&2
  exit 1
}

service_user="${SPARKCLAW_AUTOSTART_USER:-$(id -un)}"
service_group="$(id -gn "$service_user")"
bash_path="$(command -v bash)"

systemd_quote() {
  local value="$1"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || {
    echo "systemd values cannot contain newlines" >&2
    exit 1
  }
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//\$/\$\$}"
  value="${value//%/%%}"
  printf '"%s"' "$value"
}

unit_file="$(mktemp --suffix=.service)"
trap 'rm -f -- "$unit_file"' EXIT

cat >"$unit_file" <<EOF
[Unit]
Description=SparkClaw product runtime reconciliation after host boot
Wants=docker.service network-online.target
After=docker.service network-online.target
RequiresMountsFor=$(systemd_quote "$ROOT")

[Service]
Type=oneshot
User=$service_user
Group=$service_group
ExecStart=$(systemd_quote "$bash_path") $(systemd_quote "$ROOT/scripts/autostart_compose.sh")
RemainAfterExit=yes
TimeoutStartSec=4h
UMask=0077
Environment="PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/snap/bin"

[Install]
WantedBy=multi-user.target
EOF

if command -v systemd-analyze >/dev/null 2>&1; then
  systemd-analyze verify "$unit_file"
fi

if [[ "$MODE" == "check" ]]; then
  installed_unit="$UNIT_DIR/$UNIT_NAME"
  [[ -f "$installed_unit" ]] || {
    echo "SparkClaw boot autostart unit is not installed: $installed_unit" >&2
    exit 1
  }
  cmp -s "$unit_file" "$installed_unit" || {
    echo "SparkClaw boot autostart unit is stale: $installed_unit" >&2
    exit 1
  }
  echo "Installed $UNIT_NAME matches the current repository"
  exit 0
fi

privileged=()
if [[ "$UNIT_DIR" == "/etc/systemd/system" && "$EUID" -ne 0 ]]; then
  command -v sudo >/dev/null 2>&1 || {
    echo "sudo is required to install the system service" >&2
    exit 1
  }
  privileged=(sudo)
fi

"${privileged[@]}" install -D -m 0644 "$unit_file" "$UNIT_DIR/$UNIT_NAME"
"${privileged[@]}" "$SYSTEMCTL_BIN" daemon-reload
"${privileged[@]}" "$SYSTEMCTL_BIN" enable "$UNIT_NAME"

echo "Installed and enabled $UNIT_NAME for $service_user"
echo "The service will run at the next boot; it was not started now"
