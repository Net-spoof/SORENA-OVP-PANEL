#!/usr/bin/env bash
# Installed by the self-extracting .run bundle. No network operation is allowed.
set -Eeuo pipefail
umask 077

ROOT="${1:?offline payload directory required}"
ROOT="$(cd "$ROOT" && pwd -P)"
export DEBIAN_FRONTEND=noninteractive
export PATH="/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"

die() { echo "[IranGate Offline][ERROR] $*" >&2; exit 1; }
info() { echo "[IranGate Offline] $*"; }
need() { command -v "$1" >/dev/null 2>&1 || die "Required command missing: $1"; }
[[ "$EUID" == 0 ]] || die "Run as root: sudo bash IranGate-....run"
[[ -f "$ROOT/install.sh" ]] || die "Project source is missing"
[[ -s "$ROOT/offline-assets/irangate-webpanel" ]] || die "Bundled webpanel binary is missing"
[[ -s "$ROOT/offline-assets/irangate-cli" ]] || die "Bundled IranGate CLI binary is missing"
[[ -s "$ROOT/offline-assets/sing-box" ]] || die "Bundled sing-box binary is missing"
need dpkg
need apt-get
need sha256sum
need systemctl
need tar
need awk
need sed
need openssl
[[ -f /etc/os-release ]] || die "Unsupported Linux distribution"
. /etc/os-release
[[ "$ID" == ubuntu && ( "$VERSION_ID" == "22.04" || "$VERSION_ID" == "24.04" ) ]] ||
    die "This bundle requires matching Ubuntu 22.04 or 24.04 amd64. Got $ID $VERSION_ID"
[[ "$(dpkg --print-architecture)" == amd64 ]] || die "Only amd64 is supported"
EXPECTED_OS="$(cat "$ROOT/offline-assets/ubuntu-version")"
[[ "$EXPECTED_OS" == "$VERSION_ID" ]] ||
    die "Wrong bundle: Ubuntu $EXPECTED_OS assets cannot install on Ubuntu $VERSION_ID"
[[ -c /dev/net/tun ]] || die "TUN device is unavailable; enable TUN in the VM/VPS first"
[[ -d /run/systemd/system ]] || die "systemd is required"

# A full fresh installation is destructive to existing PKI, auth, and node state.
# Refuse to overwrite an already-deployed IranGate host.
if [[ -e /etc/irangate/webpanel.env || -e /etc/irangate-db.env ||
      -e /etc/openvpn/easy-rsa/pki/ca.crt ]]; then
    die "Existing IranGate/OpenVPN state detected. Offline installer is FRESH-ONLY; restore a backup on a separate clean host."
fi

if [[ -f "$ROOT/offline-assets/SHA256SUMS" ]]; then
    info "Verifying embedded payload"
    ( cd "$ROOT" && sha256sum -c offline-assets/SHA256SUMS --quiet ) ||
        die "Payload checksum verification failed"
else
    die "Payload SHA256SUMS missing"
fi

info "Installing local Ubuntu packages: no external repository access"
APTROOT="$(mktemp -d /tmp/irangate-offline-apt.XXXXXX)"
trap 'rm -rf "$APTROOT"' EXIT

# Explicit file:// source. No /etc/apt/sources.list, mirror, PPA, or network.
printf 'deb [trusted=yes] file:%s/offline-assets/apt ./\n' "$ROOT" > "$APTROOT/local.list"
mkdir -p "$APTROOT/lists/partial"
APT_OPTS=(
    -o "Dir::Etc::sourcelist=$APTROOT/local.list"
    -o "Dir::Etc::sourceparts=-"
    -o "Dir::State::lists=$APTROOT/lists"
    -o "APT::Get::List-Cleanup=0"
    -o "Acquire::Languages=none"
    -o "Acquire::Retries=0"
)

apt-get "${APT_OPTS[@]}" update
mapfile -t REQUIRED < <(grep -vE '^($|#)' "$ROOT/offline-assets/apt-required.txt")
[[ "${#REQUIRED[@]}" -gt 20 ]] || die "Dependency manifest is incomplete"
apt-get "${APT_OPTS[@]}" install -y --no-install-recommends "${REQUIRED[@]}"

info "Verifying required programs after offline APT"
for cmd in openvpn make-cadir psql pg_dump pg_restore nginx rsync curl jq            python3 iptables ip ss swanctl pki; do
    need "$cmd"
done
need runuser

info "Installing the complete source/runtime tree"
INSTALL_ROOT="/opt/IranGate-OV-Reza"
mkdir -p "$INSTALL_ROOT"
rsync -a --exclude=/offline-assets --exclude=/.git --exclude=/offline-bundles     "$ROOT/" "$INSTALL_ROOT/"
chmod +x "$INSTALL_ROOT/scripts/"*.sh 2>/dev/null || true
install -m 0755 "$ROOT/offline-assets/irangate-cli" /usr/local/bin/irangate
install -m 0755 "$ROOT/offline-assets/irangate-webpanel" /usr/local/bin/irangate-webpanel
install -m 0755 "$ROOT/offline-assets/sing-box" /usr/local/bin/sing-box

# The official installer has the tested OpenVPN PKI, PostgreSQL schema,
# traffic/enforcer, Nginx, IKEv2 and routing configuration functions. Invoke
# those locally with only prebuilt binaries and already-installed packages.
# Remove ONLY the final 'main "$@"' call from a temporary source copy.
LEGACY_FUNCTIONS="$(mktemp "$ROOT/.offline-functions.XXXXXX.sh")"
sed '$d' "$ROOT/install.sh" > "$LEGACY_FUNCTIONS"
if grep -qE '^main "\$@"$' "$LEGACY_FUNCTIONS"; then
    die "Unexpected upstream installer entry point"
fi
# shellcheck disable=SC1090
source "$LEGACY_FUNCTIONS"
rm -f "$LEGACY_FUNCTIONS"

SCRIPT_DIR="$INSTALL_ROOT"
SERVER_IP="${IRANGATE_SERVER_IP:-}"
if [[ -z "$SERVER_IP" ]]; then
    SERVER_IP="$(ip -4 route get 1.1.1.1 2>/dev/null |
        awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
fi
[[ "$SERVER_IP" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] ||
    die "Set IRANGATE_SERVER_IP to the IPv4 address of the server"
PORT="${IRANGATE_OPENVPN_PORT:-1194}"
PROTOCOL=udp
DNS=2
CLIENT_NAME=client
WEBPANEL_PORT="${IRANGATE_PANEL_PORT:-8614}"
SUBSCRIPTION_PORT="${IRANGATE_SUBSCRIPTION_PORT:-2098}"
PANEL_DOMAIN=""
SUBSCRIPTION_PUBLIC_HOST="$SERVER_IP"
SUBSCRIPTION_SCHEME=http
WEB_PATH=""
ADMIN_USER="${IRANGATE_ADMIN_USER:-admin}"
ADMIN_PASS="${IRANGATE_ADMIN_PASS:-}"
if [[ -z "$ADMIN_PASS" ]]; then
    ADMIN_PASS="$(openssl rand -hex 18)"
    CREDENTIALS="/root/irangate-offline-initial-credentials.txt"
    printf 'Username: %s\nPassword: %s\nPanel: http://%s:%s/\n'         "$ADMIN_USER" "$ADMIN_PASS" "$SERVER_IP" "$WEBPANEL_PORT" > "$CREDENTIALS"
    chmod 600 "$CREDENTIALS"
    info "Generated admin credentials: $CREDENTIALS (root-only)"
fi
[[ "$WEBPANEL_PORT" =~ ^[0-9]{2,5}$ && "$SUBSCRIPTION_PORT" =~ ^[0-9]{2,5}$ ]] ||
    die "Panel and subscription ports must be numeric"
[[ "$WEBPANEL_PORT" != "$SUBSCRIPTION_PORT" ]] || die "Panel/subscription ports conflict"
[[ "$ADMIN_USER" =~ ^[A-Za-z0-9_.@-]{1,64}$ && "${#ADMIN_PASS}" -ge 12 ]] ||
    die "Admin credentials invalid: username 1-64 characters, password at least 12 characters"

# Confirm project source and assets exist before touching live configuration.
[[ -s "$SCRIPT_DIR/deploy/master/postgres/schema.sql" ]] ||
    die "PostgreSQL schema not included"
[[ -x "$SCRIPT_DIR/deploy/master/scripts/irangate-install-maintenance-guards" ]] ||
    die "Maintenance guards not bundled"

# The server must not allocate the panel's listening port as an ephemeral
# source port (the previously diagnosed TIME-WAIT port-collision bug).
printf 'net.ipv4.ip_local_reserved_ports = %s\n' "$WEBPANEL_PORT"     > /etc/sysctl.d/99-irangate-offline-reserve-port.conf
sysctl -p /etc/sysctl.d/99-irangate-offline-reserve-port.conf >/dev/null

# No package manager or network download is allowed after local APT.
apt-get() {
    if [[ "${1:-}" == install ]]; then
        shift
        for arg in "$@"; do
            [[ "$arg" == -* ]] && continue
            dpkg-query -W -f='${db:Status-Abbrev}' "$arg" 2>/dev/null |
                grep -q '^ii' || die "Missing prebundled Ubuntu package: $arg"
        done
        return 0
    fi
    die "Unexpected apt-get network operation in bundled installer: $*"
}
install_golang() { :; }
install_dependencies() { :; }
install_irangate_cli() {
    info "Using packaged IranGate CLI (no Go build/download)"
    test -x /usr/local/bin/irangate
}
install_webpanel() {
    info "Configuring prebuilt webpanel without any external downloads"
    mkdir -p /opt/irangate/webpanel/frontend /etc/irangate
    rsync -a "$SCRIPT_DIR/webpanel/frontend/" /opt/irangate/webpanel/frontend/
    local secret
    secret="$(openssl rand -hex 32)"
    cat > /etc/irangate/webpanel.env <<ENV
WEBPANEL_PORT=$WEBPANEL_PORT
SUBSCRIPTION_PORT=$SUBSCRIPTION_PORT
SUBSCRIPTION_PUBLIC_HOST=$SUBSCRIPTION_PUBLIC_HOST
SUBSCRIPTION_SCHEME=http
PANEL_DOMAIN=
WEBPANEL_BASEPATH=/
WEBPANEL_STATIC_DIR=/opt/irangate/webpanel/frontend
WEBPANEL_ADMIN_USER=$ADMIN_USER
WEBPANEL_ADMIN_EMAIL=admin@irangate.local
JWT_SECRET=$secret
ENV
    chmod 600 /etc/irangate/webpanel.env
    WEBPANEL_ADMIN_USER="$ADMIN_USER" WEBPANEL_ADMIN_PASS="$ADMIN_PASS"         IRANGATE_DATA_DIR=/opt/irangate         /usr/local/bin/irangate-webpanel --sync-admin-from-env
    cat > /etc/systemd/system/irangate-webpanel.service <<'UNIT'
[Unit]
Description=IranGate Web Panel
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
EnvironmentFile=/etc/irangate/webpanel.env
ExecStart=/usr/local/bin/irangate-webpanel
User=root
Restart=on-failure
RestartSec=3
[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now irangate-webpanel
}

mkdir -p /var/log
touch "$LOG_FILE"
check_system
check_tun
create_irangate_directories
printf '%s\n' "$SCRIPT_DIR" > /etc/irangate/source_dir
chmod 600 /etc/irangate/source_dir
mkdir -p /opt/irangate
python3 - "$SERVER_IP" <<'PY'
import json, sys
from pathlib import Path
from datetime import datetime, timezone
Path("/opt/irangate/server_config.json").write_text(json.dumps({
    "server_ip":sys.argv[1],"server_domain":"","auto_detect_ip":False,
    "last_updated":datetime.now(timezone.utc).isoformat(),
    "settings":{"use_domain":False,"fallback_to_ip":True,"update_interval":300}
},indent=2))
PY

info "Initializing OpenVPN PKI and runtime"
install_openvpn_core
install_irangate_cli
install_webpanel
configure_webpanel_access
info "Installing PostgreSQL schema and traffic/enforcer services"
install_master_postgres_stack
install_maintenance_guards
info "Installing IKEv2, routing, sing-box and unified backup wrappers"
bash "$SCRIPT_DIR/scripts/apply-panel-extras.sh" --skip-build --no-packages --no-sing-box-download
install_panel_manager
fix_openvpn_runtime

info "Verifying post-install services"
systemctl is-active --quiet irangate-webpanel || die "Webpanel failed"
systemctl is-active --quiet openvpn@server || die "OpenVPN failed"
systemctl is-active --quiet postgresql || die "PostgreSQL failed"
systemctl is-active --quiet nginx || die "Nginx failed"
curl -fsS --max-time 15 "http://127.0.0.1:$WEBPANEL_PORT/api/health" |
    grep -q '"status":"healthy"' || die "Webpanel health failed"
need irangate-backup-v2-export
need irangate-backup-v2-import

info "OFFLINE MASTER INSTALL COMPLETE"
printf 'Panel: http://%s:%s/\nSubscription: http://%s:%s/sub/\n' \
    "$SERVER_IP" "$WEBPANEL_PORT" "$SERVER_IP" "$SUBSCRIPTION_PORT"
if [[ -n "${CREDENTIALS:-}" ]]; then info "Admin credentials saved to $CREDENTIALS"; fi
info "No outbound internet was used by the installer. Add domains/TLS later only if certificate assets are available."
