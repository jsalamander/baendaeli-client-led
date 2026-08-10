#!/usr/bin/env bash
set -euo pipefail

trap 'echo "[ERROR] Update failed at line $LINENO" >&2; exit 1' ERR

INSTALLER_URL="https://jsalamander.github.io/baendaeli-client-led/installer.sh"
SERVICE="baendaeli-client-led.service"

if [[ $EUID -ne 0 ]]; then
  echo "[ERROR] This script must be run as root (sudo)." >&2
  exit 1
fi

curl -fsSL "$INSTALLER_URL" | bash -s -- -b "/usr/local/bin"
systemctl restart "$SERVICE"
echo "[SUCCESS] Update and restart complete" >&2
