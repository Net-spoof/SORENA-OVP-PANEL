#!/usr/bin/env bash
set -euo pipefail

BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-/opt/IranGate-OV-Reza}"
FRONTEND_DIR="${FRONTEND_DIR:-/opt/irangate/webpanel/frontend}"
ENV_DIR="/etc/irangate"
ENV_FILE="$ENV_DIR/webpanel.env"
SOURCE_FILE="$ENV_DIR/source_dir"

WEBPANEL_PORT="${WEBPANEL_PORT:-8614}"
SUBSCRIPTION_PORT="${SUBSCRIPTION_PORT:-2098}"
PANEL_DOMAIN="${PANEL_DOMAIN:-}"
SUBSCRIPTION_PUBLIC_HOST="${SUBSCRIPTION_PUBLIC_HOST:-}"
WEBPANEL_BASEPATH="${WEBPANEL_BASEPATH:-/}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@irangate.local}"

log() { echo "[offline-install] $*"; }
warn() { echo "[offline-install][WARN] $*" >&2; }
fail() { echo "[offline-install][ERROR] $*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "Missing command: $1"
}

port_is_free() {
  local port="$1"
  ! ss -lntup 2>/dev/null | awk '{print $5}' | grep -Eq "[:.]${port}$"
}

choose_webpanel_port() {
  if port_is_free "$WEBPANEL_PORT"; then
    echo "$WEBPANEL_PORT"
    return 0
  fi

  warn "WEBPANEL_PORT=${WEBPANEL_PORT} is busy; trying fallback ports"

  for p in 8644 8714 8814 8914 9014; do
    if port_is_free "$p"; then
      echo "$p"
      return 0
    fi
  done

  fail "No free webpanel port found"
}

ensure_dirs() {
  mkdir -p \
    /opt/irangate/database/clients \
    /opt/irangate/database/devices \
    /opt/irangate/backups/v2 \
    /opt/irangate/routing \
    "$FRONTEND_DIR"
}

ensure_env() {
  mkdir -p "$ENV_DIR"

  WEBPANEL_PORT="$(choose_webpanel_port)"

  local jwt
  jwt="$(openssl rand -hex 32 2>/dev/null || date +%s%N | sha256sum | awk '{print $1}')"

  cat > "$ENV_FILE" <<EOF
WEBPANEL_PORT=${WEBPANEL_PORT}
SUBSCRIPTION_PORT=${SUBSCRIPTION_PORT}
SUBSCRIPTION_PUBLIC_HOST=${SUBSCRIPTION_PUBLIC_HOST}
SUBSCRIPTION_SCHEME=http
PANEL_DOMAIN=${PANEL_DOMAIN}
WEBPANEL_BASEPATH=${WEBPANEL_BASEPATH}
WEBPANEL_STATIC_DIR=${FRONTEND_DIR}
WEBPANEL_ADMIN_USER=${ADMIN_USER}
WEBPANEL_ADMIN_EMAIL=${ADMIN_EMAIL}
JWT_SECRET=${jwt}
EOF

  chmod 0600 "$ENV_FILE"
  printf '%s\n' "$INSTALL_DIR" > "$SOURCE_FILE"

  log "Written $ENV_FILE"
}

install_source_tree() {
  log "Installing source tree to $INSTALL_DIR"

  mkdir -p "$INSTALL_DIR"

  rsync -a --delete \
    --exclude='.git' \
    --exclude='offline-bundles' \
    "$BUNDLE_DIR/" "$INSTALL_DIR/"

  chmod +x "$INSTALL_DIR/scripts/"*.sh 2>/dev/null || true
  [[ -f "$INSTALL_DIR/scripts/ovp-panel" ]] && chmod +x "$INSTALL_DIR/scripts/ovp-panel" || true
  [[ -f "$INSTALL_DIR/scripts/irangate-routing" ]] && chmod +x "$INSTALL_DIR/scripts/irangate-routing" || true
}

install_binaries() {
  log "Installing binaries"

  if [[ -x "$INSTALL_DIR/offline-assets/irangate-webpanel" ]]; then
    install -m 0755 "$INSTALL_DIR/offline-assets/irangate-webpanel" /usr/local/bin/irangate-webpanel
  else
    fail "offline-assets/irangate-webpanel not found; rebuild offline bundle"
  fi

  # IRANGATE_OPENVPN_PORT_LOCATION_RUNTIME_STAGE5C
  if [[ -f "$INSTALL_DIR/scripts/irangate-openvpn-port-locations" ]]; then
    install -m 0755 "$INSTALL_DIR/scripts/irangate-openvpn-port-locations" /usr/local/sbin/irangate-openvpn-port-locations

    if [[ -f "/etc/openvpn/server.conf" ]]; then
      /usr/local/sbin/irangate-openvpn-port-locations apply \
        || warn "OpenVPN port-location runtime apply had warnings"
    fi
  fi

  if [[ -f "$INSTALL_DIR/scripts/ovp-panel" ]]; then
    install -m 0755 "$INSTALL_DIR/scripts/ovp-panel" /usr/local/bin/ovp-panel
  fi

  if [[ -f "$INSTALL_DIR/scripts/irangate-routing" ]]; then
    install -m 0755 "$INSTALL_DIR/scripts/irangate-routing" /usr/local/bin/irangate-routing

  if [[ -f "$INSTALL_DIR/offline-assets/irangate-cli" ]]; then
    install -m 0755 "$INSTALL_DIR/offline-assets/irangate-cli" /usr/local/bin/irangate
  fi
  fi
}

install_backup_core() {
  log "Installing backup v2 core scripts"

  local exp="$INSTALL_DIR/deploy/master/scripts/irangate-backup-v2-export.py"
  local imp="$INSTALL_DIR/deploy/master/scripts/irangate-backup-v2-import.py"

  if [[ -f "$exp" ]]; then
    install -m 0755 "$exp" /usr/local/bin/irangate-backup-v2-export
    install -m 0755 "$exp" /usr/local/bin/irangate-backup-v2-export.py
  else
    warn "backup export core script not found: $exp"
  fi

  if [[ -f "$imp" ]]; then
    install -m 0755 "$imp" /usr/local/bin/irangate-backup-v2-import
    install -m 0755 "$imp" /usr/local/bin/irangate-backup-v2-import.py
  else
    warn "backup import core script not found: $imp"
  fi
}

deploy_frontend() {
  log "Deploying full frontend to $FRONTEND_DIR"

  if [[ ! -d "$INSTALL_DIR/webpanel/frontend" ]]; then
    fail "frontend source not found: $INSTALL_DIR/webpanel/frontend"
  fi

  mkdir -p "$FRONTEND_DIR"

  rsync -a --delete \
    "$INSTALL_DIR/webpanel/frontend/" \
    "$FRONTEND_DIR/"

  local version
  version="$(date +%s)"

  find "$FRONTEND_DIR" -maxdepth 1 -type f -name "*.html" -print0 \
    | xargs -0 -r sed -i -E \
      "s#js/sidebar\.js(\?v=[A-Za-z0-9._-]+)?#js/sidebar.js?v=offline-${version}#g"
}

install_systemd() {
  log "Installing systemd service"

  cat > /etc/systemd/system/irangate-webpanel.service <<EOF
[Unit]
Description=IranGate Web Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=/usr/local/bin/irangate-webpanel
User=root
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable irangate-webpanel >/dev/null 2>&1 || true
}

install_backup_wrappers() {
  if [[ -x "$INSTALL_DIR/scripts/install-backup-v2-extras-wrappers.sh" ]]; then
    bash "$INSTALL_DIR/scripts/install-backup-v2-extras-wrappers.sh" || warn "backup wrapper installer had warnings"
  fi
}

health_check() {
  sleep 3

  echo
  echo "===== OFFLINE INSTALL HEALTH CHECK ====="
  echo "WEBPANEL_PORT=${WEBPANEL_PORT}"
  systemctl is-active irangate-webpanel || true
  ss -lntup | grep -E ":${WEBPANEL_PORT}|:${SUBSCRIPTION_PORT}|:443" || true
  curl -sS -o /dev/null -w "dashboard HTTP=%{http_code}\n" "http://127.0.0.1:${WEBPANEL_PORT}/dashboard.html" || true
  curl -sS -o /dev/null -w "routing HTTP=%{http_code}\n" "http://127.0.0.1:${WEBPANEL_PORT}/routing.html" || true
  curl -sS -o /dev/null -w "settings HTTP=%{http_code}\n" "http://127.0.0.1:${WEBPANEL_PORT}/settings.html" || true
}

main() {
  need_cmd systemctl
  need_cmd rsync
  need_cmd curl
  need_cmd ss

  ensure_dirs
  install_source_tree
  ensure_env
  install_binaries
  install_backup_core
  deploy_frontend
  install_systemd
  install_backup_wrappers

  systemctl restart irangate-webpanel

  health_check

  echo
  log "Offline panel install/update completed ✅"
  echo "Open: http://SERVER-IP:${WEBPANEL_PORT}/"
}

main "$@"
