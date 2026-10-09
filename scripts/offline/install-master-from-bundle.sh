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
MODE="${2:-}"
case "$MODE" in
    ""|--resume) ;;
    *) die "Unknown option: $MODE (supported: --resume)" ;;
esac
STATE_DIR="/var/lib/irangate-offline"
STATE_FILE="$STATE_DIR/install.env"
CREDENTIALS="/root/irangate-offline-initial-credentials.txt"
if [[ "$MODE" == "--resume" && ! -s "$STATE_FILE" ]]; then
    die "No resumable offline install on this server"
fi
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
if [[ "$MODE" != "--resume" ]]; then
    if [[ -e /etc/irangate/webpanel.env || -e /etc/irangate-db.env ||
          -e /etc/openvpn/easy-rsa/pki/ca.crt || -s "$STATE_FILE" ]]; then
        die "Existing installation detected. To finish a previous run of THIS offline installer, use: bash FILE.run --resume. Never overwrite an unrelated deployment."
    fi
else
    [[ -s "$STATE_FILE" && -d "$STATE_DIR/stages" ]] ||
        die "Missing recovery state; refusing to overwrite existing PKI"
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
need openssl

# Optional AI support ships prebuilt Python wheels; install from the payload
# only. Never run a pip/network resolver on the target machine.
if [[ -f "$ROOT/pkg/ai/requirements.txt" ]]; then
    info "Installing bundled AI Python packages from local wheelhouse"
    python3 -m venv /opt/irangate-ai-venv
    PIP_NO_INDEX=1 /opt/irangate-ai-venv/bin/pip install --no-index \
        --find-links "$ROOT/offline-assets/wheelhouse" \
        -r "$ROOT/pkg/ai/requirements.txt"
fi

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

# The first install asks for all site settings exactly once, before touching
# OpenVPN PKI or database. --resume reuses saved root-only settings.
if [[ "$MODE" == "--resume" ]]; then
    # shellcheck disable=SC1090
    source "$STATE_FILE"
    info "Resuming with saved IP/ports; existing keys and admin credentials are preserved"
else
    # The route lookup does not contact the Internet.
    SERVER_IP="${IRANGATE_SERVER_IP:-}"
    if [[ -z "$SERVER_IP" ]]; then
        SERVER_IP="$(ip -4 route get 1.1.1.1 2>/dev/null |
            awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
    fi

    prompt_value() {
        local title="$1" initial="$2" answer=""
        if [[ -t 0 && -r /dev/tty ]]; then
            read -r -p "$title [$initial]: " answer </dev/tty ||
                die "Input interrupted"
        fi
        printf '%s' "${answer:-$initial}"
    }

    check_port() {
        local proto="$1" port="$2" exclude_a="${3:-}" exclude_b="${4:-}"
        [[ "$port" =~ ^[0-9]+$ ]] || return 1
        (( 10#$port >= 1024 && 10#$port <= 65535 )) || return 1
        [[ "$port" != "4500" && "$port" != "500" && "$port" != "80" ]] || return 1
        [[ "$port" != "$exclude_a" && "$port" != "$exclude_b" ]] || return 1
        local ss_flag="-ltn"
        [[ "$proto" == "udp" ]] && ss_flag="-lun"
        ! ss -H "$ss_flag" |
            awk -v port=":$port" '$4 ~ port "$" { found=1 } END { exit(!found) }'
    }

    select_port() {
        local label="$1" proto="$2" initial="$3" candidates="$4"
        local exclude_a="${5:-}" exclude_b="${6:-}" candidate="" selected=""
        if check_port "$proto" "$initial" "$exclude_a" "$exclude_b"; then
            selected="$initial"
        else
            for candidate in $candidates; do
                if check_port "$proto" "$candidate" "$exclude_a" "$exclude_b"; then
                    selected="$candidate"
                    printf '[IranGate Offline] %s port %s unavailable; choosing %s\n' \
                        "$label" "$initial" "$selected" >&2
                    break
                fi
            done
        fi
        [[ -n "$selected" ]] || die "No available $label port in candidate list"
        local attempt
        for attempt in {1..20}; do
            candidate="$(prompt_value "$label ($proto)" "$selected")"
            if check_port "$proto" "$candidate" "$exclude_a" "$exclude_b"; then
                printf '%s' "$candidate"
                return 0
            fi
            printf '[IranGate Offline] Port %s unavailable, reserved or invalid; choose another\n' \
                "$candidate" >&2
            if [[ ! -t 0 ]]; then return 1; fi
        done
        die "Too many invalid $label choices"
    }

    SERVER_IP="$(prompt_value 'Public server IPv4' "$SERVER_IP")"
    python3 - "$SERVER_IP" <<'PY_IP'
import ipaddress,sys
try:
    ipaddress.IPv4Address(sys.argv[1])
except ipaddress.AddressValueError:
    raise SystemExit("Invalid public IPv4 address")
PY_IP
    PROTOCOL="$(prompt_value 'OpenVPN protocol (udp/tcp)' "${IRANGATE_OPENVPN_PROTOCOL:-udp}")"
    [[ "$PROTOCOL" == "udp" || "$PROTOCOL" == "tcp" ]] ||
        die "OpenVPN protocol must be udp or tcp"

    PORT="$(select_port 'OpenVPN' "$PROTOCOL" "${IRANGATE_OPENVPN_PORT:-1194}" \
        '1194 1195 1196 1443 2443 4443')"
    WEBPANEL_PORT="$(select_port 'WebPanel' tcp "${IRANGATE_PANEL_PORT:-8614}" \
        '18614 18615 18616 8615 8616 28614' "$PORT")"
    SUBSCRIPTION_PORT="$(select_port 'Subscription' tcp "${IRANGATE_SUBSCRIPTION_PORT:-2098}" \
        '2098 2099 2100 12098 22098' "$PORT" "$WEBPANEL_PORT")"

    ADMIN_USER="$(prompt_value 'Panel admin username' "${IRANGATE_ADMIN_USER:-admin}")"
    ADMIN_PASS="${IRANGATE_ADMIN_PASS:-}"
    if [[ -z "$ADMIN_PASS" ]]; then ADMIN_PASS="$(openssl rand -hex 18)"; fi
    [[ "$ADMIN_USER" =~ ^[A-Za-z0-9_.@-]{1,64}$ && "${#ADMIN_PASS}" -ge 12 ]] ||
        die "Admin username or password invalid"

    DNS=2
    CLIENT_NAME=client
    PANEL_DOMAIN=""
    SUBSCRIPTION_PUBLIC_HOST="$SERVER_IP"
    SUBSCRIPTION_SCHEME=http
    WEB_PATH=""

    info "Setup preview: IP=$SERVER_IP VPN=$PORT/$PROTOCOL Panel=$WEBPANEL_PORT/tcp Subscription=$SUBSCRIPTION_PORT/tcp"
    info "IKEv2 will use 500/udp and 4500/udp; legacy strongSwan backend will be replaced by charon-systemd"
    if [[ -t 0 && -r /dev/tty ]]; then
        answer="$(prompt_value 'Proceed with the complete offline installation? (yes/no)' 'yes')"
        [[ "$answer" == yes || "$answer" == y ]] || die "Installation cancelled before PKI changes"
    fi

    mkdir -p "$STATE_DIR/stages"
    chmod 700 "$STATE_DIR" "$STATE_DIR/stages"
    # %q escapes every user-provided value; the saved file is root-readable only.
    {
        for key in SERVER_IP PORT PROTOCOL WEBPANEL_PORT SUBSCRIPTION_PORT \
                   ADMIN_USER ADMIN_PASS DNS CLIENT_NAME PANEL_DOMAIN \
                   SUBSCRIPTION_PUBLIC_HOST SUBSCRIPTION_SCHEME WEB_PATH; do
            printf '%s=%q\n' "$key" "${!key}"
        done
    } > "$STATE_FILE"
    chmod 600 "$STATE_FILE"

    printf 'Username: %s\nPassword: %s\nPanel: http://%s:%s/\n' \
        "$ADMIN_USER" "$ADMIN_PASS" "$SERVER_IP" "$WEBPANEL_PORT" > "$CREDENTIALS"
    chmod 600 "$CREDENTIALS"
    info "Generated admin credentials: $CREDENTIALS (root-only)"
fi

# Credentials and config are stable across --resume.
[[ "$PORT" =~ ^[0-9]+$ && "$WEBPANEL_PORT" =~ ^[0-9]+$ &&
   "$SUBSCRIPTION_PORT" =~ ^[0-9]+$ ]] || die "Invalid saved port settings"
[[ "$WEBPANEL_PORT" != "$SUBSCRIPTION_PORT" &&
   "$PORT" != "$WEBPANEL_PORT" && "$PORT" != "$SUBSCRIPTION_PORT" ]] ||
    die "Saved service ports conflict"
[[ "$PROTOCOL" == udp || "$PROTOCOL" == tcp ]] ||
    die "Invalid saved OpenVPN protocol"

# Confirm project source and assets exist before touching live configuration.
[[ -s "$SCRIPT_DIR/deploy/master/postgres/schema.sql" ]] ||
    die "PostgreSQL schema not included"
[[ -x "$SCRIPT_DIR/deploy/master/scripts/irangate-install-maintenance-guards" ]] ||
    die "Maintenance guards not bundled"

# Reserve the panel port while preserving previously reserved local ports.
CURRENT_RESERVED="$(sysctl -n net.ipv4.ip_local_reserved_ports 2>/dev/null || true)"
case ",$CURRENT_RESERVED," in
    *,"$WEBPANEL_PORT",*) RESERVED_PORTS="$CURRENT_RESERVED" ;;
    *) RESERVED_PORTS="${CURRENT_RESERVED:+$CURRENT_RESERVED,}$WEBPANEL_PORT" ;;
esac
printf 'net.ipv4.ip_local_reserved_ports = %s\n' "$RESERVED_PORTS" \
    > /etc/sysctl.d/99-irangate-offline-reserve-port.conf
sysctl -p /etc/sysctl.d/99-irangate-offline-reserve-port.conf >/dev/null

# Strictly offline from now on; child scripts receive --no-packages/--no-download.
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
    die "Unexpected apt-get operation in bundled installer: $*"
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
    WEBPANEL_ADMIN_USER="$ADMIN_USER" WEBPANEL_ADMIN_PASS="$ADMIN_PASS" \
        IRANGATE_DATA_DIR=/opt/irangate \
        /usr/local/bin/irangate-webpanel --sync-admin-from-env
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
    systemctl restart irangate-webpanel
}

wait_for_panel() {
    local code i
    for i in {1..25}; do
        code="$(curl --noproxy '*' -sS --max-time 3 \
            -o /tmp/irangate-offline-health.json -w '%{http_code}' \
            "http://127.0.0.1:$WEBPANEL_PORT/api/health" 2>/dev/null || true)"
        if [[ "$code" == 200 ]] &&
            grep -Eq '"status"[[:space:]]*:[[:space:]]*"healthy"' \
                /tmp/irangate-offline-health.json; then
            return 0
        fi
        sleep 2
    done
    journalctl -u irangate-webpanel --no-pager -n 35 || true
    die "Webpanel did not become healthy on port $WEBPANEL_PORT"
}

run_stage() {
    local stage_name="$1"
    shift
    if [[ -f "$STATE_DIR/stages/$stage_name.done" ]]; then
        info "Skipping completed stage: $stage_name"
        return 0
    fi
    info "START: $stage_name"
    if [[ "$stage_name" == openvpn ]]; then
        if [[ -f "$STATE_DIR/stages/openvpn.started" ]] &&
           validate_pki &&
           [[ -s /opt/irangate/clients/client.ovpn ]] &&
           systemctl is-active --quiet openvpn@server; then
            info "OpenVPN was successfully initialized before interruption; preserving existing PKI and client"
            touch "$STATE_DIR/stages/openvpn.done"
            return 0
        fi
        if [[ -f "$STATE_DIR/stages/openvpn.started" ]] &&
           [[ -f /etc/openvpn/easy-rsa/pki/private/ca.key ]] &&
           ! validate_pki; then
            die "Previous run left incomplete PKI with a private CA key. Refusing to regenerate it; recover the PKI manually."
        fi
        touch "$STATE_DIR/stages/openvpn.started"
    fi
    "$@"
    touch "$STATE_DIR/stages/$stage_name.done"
    info "DONE: $stage_name"
}

prepare_runtime() {
    mkdir -p /var/log
    touch "$LOG_FILE"
    check_system
    check_tun
    create_irangate_directories
    mkdir -p /etc/irangate /opt/irangate
    printf '%s\n' "$SCRIPT_DIR" > /etc/irangate/source_dir
    chmod 600 /etc/irangate/source_dir
    python3 - "$SERVER_IP" <<'PY'
import json,sys
from pathlib import Path
from datetime import datetime,timezone
Path("/opt/irangate/server_config.json").write_text(json.dumps({
    "server_ip":sys.argv[1], "server_domain":"", "auto_detect_ip":False,
    "last_updated":datetime.now(timezone.utc).isoformat(),
    "settings":{"use_domain":False,"fallback_to_ip":True,"update_interval":300}
},indent=2))
PY
}

install_panel_stage() {
    install_webpanel
    configure_webpanel_access
    wait_for_panel
}

install_postgres_stage() {
    wait_for_panel
    local attempt
    for attempt in 1 2 3; do
        if install_master_postgres_stack; then
            return 0
        fi
        info "PostgreSQL client-sync attempt $attempt failed, retrying after panel check..."
        sleep 4
        wait_for_panel
    done
    die "PostgreSQL initialization failed after 3 attempts"
}

install_extras_stage() {
    info "Installing IKEv2, routing, sing-box and unified Backup V2 wrappers"
    bash "$SCRIPT_DIR/scripts/apply-panel-extras.sh" \
        --skip-build --no-packages --no-sing-box-download
    # Routing service is intentionally inactive until the operator configures
    # a foreign outbound and explicitly applies nftables/routing.
    if systemctl is-active --quiet irangate-routing.service; then
        die "Routing unexpectedly active without explicit operator configuration"
    fi
}

check_final_services() {
    wait_for_panel
    for service in irangate-webpanel openvpn@server postgresql nginx strongswan; do
        systemctl is-active --quiet "$service" ||
            die "Required service not active: $service"
    done
    for timer in irangate-node-usage-sync irangate-pg-usage-tick \
                 irangate-pg-enforcer irangate-pg-sync-clients \
                 irangate-retention-cleanup irangate-syslog-guard \
                 irangate-ikev2-stats; do
        systemctl is-active --quiet "$timer.timer" ||
            die "Required timer not active: $timer.timer"
    done
    systemctl is-active --quiet irangate-ikev2-firewall.service ||
        die "IKEv2 firewall service not active"
    if systemctl is-active --quiet strongswan-starter.service ||
        pgrep -x charon >/dev/null 2>&1; then
        die "Legacy strongSwan daemon still running; IKEv2 is not healthy"
    fi
    pgrep -x charon-systemd >/dev/null 2>&1 ||
        die "charon-systemd not running"
    need irangate-backup-v2-export
    need irangate-backup-v2-import
    need irangate-routing
    need sing-box
    need ovp-panel
    irangate-routing generate-engine --write --check >/dev/null ||
        die "Bundled sing-box routing configuration failed validation"
    if ! ss -H -ltn | awk -v p=":$WEBPANEL_PORT" '$4 ~ p "$" {found=1} END {exit(!found)}'; then
        die "Webpanel TCP port $WEBPANEL_PORT is not listening"
    fi
    info "All critical offline installation checks passed"
}

run_stage prepare-runtime prepare_runtime
run_stage openvpn install_openvpn_core
run_stage cli install_irangate_cli
run_stage webpanel install_panel_stage
run_stage postgres install_postgres_stage
run_stage maintenance install_maintenance_guards
run_stage extras install_extras_stage
run_stage panel-manager install_panel_manager
run_stage openvpn-runtime fix_openvpn_runtime
run_stage verification check_final_services
touch "$STATE_DIR/complete"
info "OFFLINE MASTER INSTALL COMPLETE"
printf 'Panel: http://%s:%s/\nSubscription: http://%s:%s/sub/\n' \
    "$SERVER_IP" "$WEBPANEL_PORT" "$SERVER_IP" "$SUBSCRIPTION_PORT"
info "Admin credentials: $CREDENTIALS (root only)"
info "Routing remains disabled until an outbound is configured and applied."
info "No Internet access was used on the destination server."
