#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-$REPO_DIR/offline-bundles}"
mkdir -p "$OUT_DIR"

DEPS_TAR="${DEPS_TAR:-$(ls -t "$OUT_DIR"/irangate-deps-offline-*.tar.gz 2>/dev/null | head -1 || true)}"
PANEL_TAR="${PANEL_TAR:-$(ls -t "$OUT_DIR"/irangate-panel-offline-*.tar.gz 2>/dev/null | head -1 || true)}"

if [[ -z "$DEPS_TAR" || ! -f "$DEPS_TAR" ]]; then
  echo "deps bundle not found ❌"
  exit 1
fi

if [[ -z "$PANEL_TAR" || ! -f "$PANEL_TAR" ]]; then
  echo "panel bundle not found ❌"
  exit 1
fi

OS_ID="$(. /etc/os-release && echo "${ID}")"
OS_VER="$(. /etc/os-release && echo "${VERSION_ID}")"
ARCH="$(dpkg --print-architecture)"
STAMP="$(date +%Y%m%d-%H%M%S)"
COMMIT="$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"

NAME="irangate-full-offline-kit-${OS_ID}${OS_VER}-${ARCH}-${COMMIT}-${STAMP}"
WORK="/tmp/${NAME}"

log() { echo "[full-kit] $*"; }

rm -rf "$WORK"
mkdir -p "$WORK/bundles"

log "Using deps:  $DEPS_TAR"
log "Using panel: $PANEL_TAR"

cp -av "$DEPS_TAR" "$WORK/bundles/"
cp -av "${DEPS_TAR}.sha256" "$WORK/bundles/" 2>/dev/null || true

cp -av "$PANEL_TAR" "$WORK/bundles/"
cp -av "${PANEL_TAR}.sha256" "$WORK/bundles/" 2>/dev/null || true

cat > "$WORK/install-irangate-full-offline.sh" <<'INSTALL'
#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUNDLES_DIR="$BASE_DIR/bundles"

WEBPANEL_PORT="${WEBPANEL_PORT:-8614}"
SUBSCRIPTION_PORT="${SUBSCRIPTION_PORT:-2098}"
OPENVPN_PORT="${OPENVPN_PORT:-443}"
OPENVPN_PROTO="${OPENVPN_PROTO:-udp}"
OPENVPN_SUBNET="${OPENVPN_SUBNET:-10.8.0.0}"
OPENVPN_NETMASK="${OPENVPN_NETMASK:-255.255.0.0}"
SETUP_NGINX_PROXY="${SETUP_NGINX_PROXY:-0}"

log() { echo "[full-offline] $*"; }
warn() { echo "[full-offline][WARN] $*" >&2; }
fail() { echo "[full-offline][ERROR] $*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "Missing command: $1"
}

verify_bundle() {
  local tar="$1"
  local base
  base="$(basename "$tar")"

  if [[ -f "$BUNDLES_DIR/${base}.sha256" ]]; then
    (
      cd "$BUNDLES_DIR"
      sha256sum -c "${base}.sha256"
    )
  else
    warn "sha256 file not found for $base"
  fi
}

extract_one() {
  local tar="$1"
  local dest="$2"
  rm -rf "$dest"
  mkdir -p "$dest"
  tar -xzf "$tar" -C "$dest" --strip-components=1
}

install_deps_bundle() {
  local tar
  tar="$(ls -t "$BUNDLES_DIR"/irangate-deps-offline-*.tar.gz 2>/dev/null | head -1 || true)"
  [[ -n "$tar" && -f "$tar" ]] || fail "deps bundle not found"

  log "Verifying deps bundle"
  verify_bundle "$tar"

  log "Extracting deps bundle"
  local dest="/tmp/irangate-deps-full-offline"
  extract_one "$tar" "$dest"

  log "Installing offline dependencies"
  bash "$dest/install-offline-deps.sh"
}

ensure_base_dirs() {
  log "Ensuring IranGate directories"

  mkdir -p \
    /etc/irangate \
    /opt/irangate/database/clients \
    /opt/irangate/database/devices \
    /opt/irangate/backups/v2 \
    /opt/irangate/routing \
    /opt/irangate/webpanel/frontend \
    /etc/openvpn/server \
    /etc/openvpn/server/ccd

  chmod 700 /opt/irangate/database || true
}

install_panel_bundle() {
  local tar
  tar="$(ls -t "$BUNDLES_DIR"/irangate-panel-offline-*.tar.gz 2>/dev/null | head -1 || true)"
  [[ -n "$tar" && -f "$tar" ]] || fail "panel bundle not found"

  log "Verifying panel bundle"
  verify_bundle "$tar"

  log "Extracting panel bundle"
  local dest="/tmp/irangate-panel-full-offline"
  extract_one "$tar" "$dest"

  log "Installing panel offline bundle"
  WEBPANEL_PORT="$WEBPANEL_PORT" \
  SUBSCRIPTION_PORT="$SUBSCRIPTION_PORT" \
  WEBPANEL_BASEPATH="/" \
  bash "$dest/scripts/offline-install-panel.sh"
}

enable_postgresql() {
  log "Starting PostgreSQL"
  systemctl enable postgresql >/dev/null 2>&1 || true
  systemctl restart postgresql || systemctl start postgresql || true

  sleep 2

  if systemctl is-active --quiet postgresql; then
    log "PostgreSQL active ✅"
  else
    warn "PostgreSQL is not active"
  fi
}

create_openvpn_pki() {
  log "Preparing OpenVPN PKI"

  local easy="/etc/openvpn/easy-rsa"

  if [[ ! -d "$easy" ]]; then
    if command -v make-cadir >/dev/null 2>&1; then
      make-cadir "$easy"
    elif [[ -d /usr/share/easy-rsa ]]; then
      mkdir -p "$easy"
      cp -a /usr/share/easy-rsa/* "$easy/"
    else
      fail "easy-rsa files not found"
    fi
  fi

  cd "$easy"

  if [[ ! -x ./easyrsa ]]; then
    chmod +x ./easyrsa 2>/dev/null || true
  fi

  if [[ ! -f pki/ca.crt ]]; then
    EASYRSA_BATCH=1 EASYRSA_REQ_CN="IranGate-CA" ./easyrsa init-pki
    EASYRSA_BATCH=1 EASYRSA_REQ_CN="IranGate-CA" ./easyrsa build-ca nopass
  fi

  if [[ ! -f pki/issued/server.crt || ! -f pki/private/server.key ]]; then
    EASYRSA_BATCH=1 ./easyrsa build-server-full server nopass
  fi

  if [[ ! -f pki/dh.pem ]]; then
    EASYRSA_BATCH=1 ./easyrsa gen-dh
  fi

  EASYRSA_BATCH=1 ./easyrsa gen-crl || true

  install -m 0644 pki/ca.crt /etc/openvpn/server/ca.crt
  install -m 0644 pki/issued/server.crt /etc/openvpn/server/server.crt
  install -m 0600 pki/private/server.key /etc/openvpn/server/server.key
  install -m 0644 pki/dh.pem /etc/openvpn/server/dh.pem
  install -m 0644 pki/crl.pem /etc/openvpn/server/crl.pem 2>/dev/null || true

  if [[ ! -f /etc/openvpn/server/tls-crypt.key ]]; then
    openvpn --genkey secret /etc/openvpn/server/tls-crypt.key 2>/dev/null \
      || openvpn --genkey --secret /etc/openvpn/server/tls-crypt.key
    chmod 0600 /etc/openvpn/server/tls-crypt.key
  fi
}

write_openvpn_config() {
  log "Writing OpenVPN server config"

  cat > /etc/openvpn/server/server.conf <<EOF
port ${OPENVPN_PORT}
proto ${OPENVPN_PROTO}
dev tun

topology subnet
server ${OPENVPN_SUBNET} ${OPENVPN_NETMASK}

client-config-dir /etc/openvpn/server/ccd
ifconfig-pool-persist /opt/irangate/database/ipp.txt

push "redirect-gateway def1 bypass-dhcp"
push "dhcp-option DNS 1.1.1.1"
push "dhcp-option DNS 8.8.8.8"

keepalive 10 120
duplicate-cn

ca /etc/openvpn/server/ca.crt
cert /etc/openvpn/server/server.crt
key /etc/openvpn/server/server.key
dh /etc/openvpn/server/dh.pem
crl-verify /etc/openvpn/server/crl.pem

tls-crypt /etc/openvpn/server/tls-crypt.key

data-ciphers AES-256-GCM:AES-128-GCM:CHACHA20-POLY1305
data-ciphers-fallback AES-256-CBC
auth SHA256

user nobody
group nogroup
persist-key
persist-tun

status /run/openvpn-server/status-server.log
management 127.0.0.1 7505
management-client-auth

verb 3
explicit-exit-notify 1
EOF

  install -m 0644 /etc/openvpn/server/server.conf /etc/openvpn/server.conf

  mkdir -p /run/openvpn-server

  cat > /etc/tmpfiles.d/irangate-openvpn.conf <<'EOF_TMP'
d /run/openvpn-server 0755 root root - -
EOF_TMP

  systemd-tmpfiles --create /etc/tmpfiles.d/irangate-openvpn.conf 2>/dev/null || true
}

enable_routing_nat() {
  log "Enabling IPv4 forwarding and NAT"

  cat > /etc/sysctl.d/99-irangate-forward.conf <<EOF
net.ipv4.ip_forward=1
EOF

  sysctl --system >/dev/null 2>&1 || true

  local iface
  iface="${IRANGATE_WAN_IFACE:-$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')}"
  if [[ -n "$iface" ]]; then
    iptables -t nat -C POSTROUTING -s 10.8.0.0/16 -o "$iface" -j MASQUERADE 2>/dev/null \
      || iptables -t nat -A POSTROUTING -s 10.8.0.0/16 -o "$iface" -j MASQUERADE || true
    log "NAT interface: $iface"
  else
    warn "Could not detect default interface for NAT"
  fi
}

start_openvpn() {
  log "Starting OpenVPN"

  systemctl daemon-reload
  systemctl enable openvpn@server >/dev/null 2>&1 || true
  systemctl restart openvpn@server || true

  sleep 3

  if systemctl is-active --quiet openvpn@server; then
    log "OpenVPN active ✅"
  else
    warn "OpenVPN did not become active"
    journalctl -u openvpn@server -n 80 --no-pager || true
  fi
}

optional_nginx_proxy() {
  if [[ "$SETUP_NGINX_PROXY" != "1" ]]; then
    log "Skipping nginx reverse proxy config"
    return 0
  fi

  local panel_port
  panel_port="$(grep '^WEBPANEL_PORT=' /etc/irangate/webpanel.env 2>/dev/null | cut -d= -f2 | tail -1)"
  panel_port="${panel_port:-$WEBPANEL_PORT}"

  log "Installing nginx proxy to panel port $panel_port"

  cat > /etc/nginx/sites-available/irangate-panel-offline.conf <<EOF
server {
    listen 80;
    server_name _;

    location / {
        proxy_pass http://127.0.0.1:${panel_port};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF

  ln -sf /etc/nginx/sites-available/irangate-panel-offline.conf /etc/nginx/sites-enabled/irangate-panel-offline.conf
  nginx -t && systemctl restart nginx || true
}

health_check() {
  local panel_port
  panel_port="$(grep '^WEBPANEL_PORT=' /etc/irangate/webpanel.env 2>/dev/null | cut -d= -f2 | tail -1)"
  panel_port="${panel_port:-$WEBPANEL_PORT}"

  echo
  echo "===== FULL OFFLINE HEALTH CHECK ====="
  echo "WEBPANEL_PORT=$panel_port"
  echo "OPENVPN_PORT=$OPENVPN_PORT/$OPENVPN_PROTO"

  systemctl is-active irangate-webpanel || true
  systemctl is-active nginx || true
  systemctl is-active postgresql || true
  systemctl is-active openvpn@server || true

  echo
  ss -lntup | grep -E ":${panel_port}|:${SUBSCRIPTION_PORT}|:${OPENVPN_PORT}" || true

  echo
  curl -sS -o /dev/null -w "dashboard HTTP=%{http_code}\n" "http://127.0.0.1:${panel_port}/dashboard.html" || true
  curl -sS -o /dev/null -w "routing HTTP=%{http_code}\n" "http://127.0.0.1:${panel_port}/routing.html" || true
  curl -sS -o /dev/null -w "settings HTTP=%{http_code}\n" "http://127.0.0.1:${panel_port}/settings.html" || true
}

main() {
  need_cmd tar
  need_cmd systemctl
  need_cmd sha256sum

  install_deps_bundle
  ensure_base_dirs
  enable_postgresql
  install_panel_bundle
  create_openvpn_pki
  write_openvpn_config
  enable_routing_nat
  start_openvpn
  optional_nginx_proxy
  health_check

  echo
  log "Full offline IranGate install completed ✅"
}

main "$@"
INSTALL

chmod +x "$WORK/install-irangate-full-offline.sh"

cat > "$WORK/README.txt" <<EOF
IranGate Full Offline Kit
OS: ${OS_ID} ${OS_VER}
Arch: ${ARCH}
Commit: ${COMMIT}
Created: $(date -Is)

Included:
- $(basename "$DEPS_TAR")
- $(basename "$PANEL_TAR")

Run:
  tar -xzf ${NAME}.tar.gz
  cd ${NAME}
  sudo bash install-irangate-full-offline.sh

Optional:
  WEBPANEL_PORT=8614 SUBSCRIPTION_PORT=2098 OPENVPN_PORT=443 sudo -E bash install-irangate-full-offline.sh
EOF

ARCHIVE="$OUT_DIR/${NAME}.tar.gz"

log "Creating archive: $ARCHIVE"
tar -C /tmp -czf "$ARCHIVE" "$NAME"

cd "$OUT_DIR"
sha256sum "$(basename "$ARCHIVE")" > "$(basename "$ARCHIVE").sha256"

rm -rf "$WORK"

echo
echo "Bundle: $ARCHIVE"
echo "SHA256: ${ARCHIVE}.sha256"
