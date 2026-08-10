#!/usr/bin/env bash
set -euo pipefail

trap 'echo "[ERROR] Installation failed at line $LINENO" >&2; exit 1' ERR

OWNER="jsalamander"
REPO="baendaeli-client-led"
BINARY="baendaeli-client-led"
WORKDIR="/opt/${REPO}"
SERVICE_NAME="${REPO}.service"
INSTALLER_URL="https://jsalamander.github.io/baendaeli-client-led/installer.sh"
MATRIX_REPO_URL="https://github.com/hzeller/rpi-rgb-led-matrix.git"
MATRIX_BUILD_DIR="/tmp/rpi-rgb-led-matrix"

require_root() {
  if [[ $EUID -ne 0 ]]; then
    echo "[ERROR] This script must be run as root (sudo)." >&2
    exit 1
  fi
}

create_service_user() {
  local service_user="baendaeli-client-led"
  if ! id "$service_user" >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$service_user"
  fi
}

install_binary() {
  curl -fsSL "$INSTALLER_URL" | bash -s -- -b "/usr/local/bin"
}

install_matrix_viewer() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends \
    git \
    make \
    g++ \
    libgraphicsmagick++-dev \
    libwebp-dev

  rm -rf "$MATRIX_BUILD_DIR"
  git clone --depth 1 "$MATRIX_REPO_URL" "$MATRIX_BUILD_DIR"
  make -C "$MATRIX_BUILD_DIR/utils" led-image-viewer
  install -m 0755 "$MATRIX_BUILD_DIR/utils/led-image-viewer" /usr/local/bin/led-image-viewer
}

write_service() {
  cat >/etc/systemd/system/${SERVICE_NAME} <<'SERVICE'
[Unit]
Description=Baendaeli LED Client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/baendaeli-client-led
EnvironmentFile=-/opt/baendaeli-client-led/.env
ExecStart=/usr/local/bin/baendaeli-client-led
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SERVICE
}

main() {
  require_root
  create_service_user
  mkdir -p "$WORKDIR"
  chown baendaeli-client-led:baendaeli-client-led "$WORKDIR"
  install_binary
  install_matrix_viewer
  write_service
  systemctl daemon-reload
  systemctl enable --now ${SERVICE_NAME}
  echo "[SUCCESS] Installation complete. Add runtime env vars to ${WORKDIR}/.env" >&2
}

main "$@"
