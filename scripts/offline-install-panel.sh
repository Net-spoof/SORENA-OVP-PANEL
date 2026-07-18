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

detect_go() {
  if command -v go >/dev/null 2>&1; then command -v go; return 0; fi
  if [[ -x /usr/local/go/bin/go ]]; then echo /usr/local/go/bin/go; return 0; fi
  if [[ -x /usr/bin/go ]]; then echo /usr/bin/go; return 0; fi
  if [[ -x /snap/bin/go ]]; then echo /snap/bin/go; return 0; fi
  return 1
}

ensure_env() {
  mkdir -p "$ENV_DIR"

  if [[ ! -f "$ENV_FILE" ]]; then
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
    log "Created $ENV_FILE"
  else
    log "Keeping existing $ENV_FILE"

    grep -q '^WEBPANEL_STATIC_DIR=' "$ENV_FILE" \
      || echo "WEBPANEL_STATIC_DIR=${FRONTEND_DIR}" >> "$ENV_FILE"
  fi

  printf '%s\n' "$INSTALL_DIR" > "$SOURCE_FILE"
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
  elif [[ -d "$INSTALL_DIR/webpanel/backend" ]]; then
    local go_bin
    go_bin="$(detect_go || true)"
    [[ -n "$go_bin" ]] || fail "No prebuilt webpanel binary and Go not found"

    log "Building webpanel with $("$go_bin" version)"
    (
      cd "$INSTALL_DIR/webpanel/backend"
      "$go_bin" mod tidy
      "$go_bin" build -o /tmp/irangate-webpanel .
    )
    install -m 0755 /tmp/irangate-webpanel /usr/local/bin/irangate-webpanel
  else
    fail "webpanel backend source not found"
  fi

  if [[ -f "$INSTALL_DIR/scripts/ovp-panel" ]]; then
    install -m 0755 "$INSTALL_DIR/scripts/ovp-panel" /usr/local/bin/ovp-panel
  fi

  if [[ -f "$INSTALL_DIR/scripts/irangate-routing" ]]; then
    install -m 0755 "$INSTALL_DIR/scripts/irangate-routing" /usr/local/bin/irangate-routing
  fi
}

deploy_frontend() {
  log "Deploying frontend to $FRONTEND_DIR"

  mkdir -p "$FRONTEND_DIR/js"

  if [[ -d "$INSTALL_DIR/webpanel/frontend" ]]; then
    find "$INSTALL_DIR/webpanel/frontend" -maxdepth 1 -type f -print0 \
      | xargs -0 -r -I{} install -m 0644 "{}" "$FRONTEND_DIR/"

    if [[ -d "$INSTALL_DIR/webpanel/frontend/js" ]]; then
      find "$INSTALL_DIR/webpanel/frontend/js" -maxdepth 1 -type f -print0 \
        | xargs -0 -r -I{} install -m 0644 "{}" "$FRONTEND_DIR/js/"
    fi

    local version
    version="$(date +%s)"
    find "$FRONTEND_DIR" -maxdepth 1 -type f -name "*.html" -print0 \
      | xargs -0 -r sed -i -E \
        "s#js/sidebar\.js(\?v=[A-Za-z0-9._-]+)?#js/sidebar.js?v=offline-${version}#g"
  fi
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

apply_panel_extras() {
  if [[ -x "$INSTALL_DIR/scripts/apply-panel-extras.sh" ]]; then
    bash "$INSTALL_DIR/scripts/apply-panel-extras.sh" || warn "panel extras apply had warnings"
  fi
}

health_check() {
  local port
  port="$(grep '^WEBPANEL_PORT=' "$ENV_FILE" | cut -d= -f2 | tail -1)"
  port="${port:-$WEBPANEL_PORT}"

  sleep 2

  echo
  echo "===== OFFLINE INSTALL HEALTH CHECK ====="
  systemctl is-active irangate-webpanel || true
  ss -lntp | grep ":${port}" || true
  curl -sS -o /dev/null -w "Web Panel HTTP=%{http_code}\n" "http://127.0.0.1:${port}/dashboard.html" || true
  curl -sS -o /dev/null -w "Routing Page HTTP=%{http_code}\n" "http://127.0.0.1:${port}/routing.html" || true
}

main() {
  need_cmd systemctl
  need_cmd rsync
  need_cmd curl
  need_cmd ss

  install_source_tree
  ensure_env
  install_binaries
  deploy_frontend
  install_systemd
  install_backup_wrappers
  apply_panel_extras

  systemctl restart irangate-webpanel

  health_check

  echo
  log "Offline panel install/update completed ✅"
  echo "Open: http://SERVER-IP:${WEBPANEL_PORT}/"
}

main "$@"
