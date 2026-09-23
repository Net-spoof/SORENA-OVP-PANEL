#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="$REPO_DIR/deploy/master/ikev2"
SYSTEMD_DIR="$REPO_DIR/deploy/master/systemd"

NO_PACKAGES=0
REQUESTED_HOST=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-packages)
            NO_PACKAGES=1
            shift
            ;;
        --host)
            REQUESTED_HOST="${2:-}"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 2
            ;;
    esac
done

[[ "${EUID:-$(id -u)}" -eq 0 ]] || {
    echo "Must run as root" >&2
    exit 1
}

log() {
    echo "[ikev2-install] $*"
}

detect_host() {
    REQUESTED_HOST="$REQUESTED_HOST" python3 - <<'PY_HOST'
import json
import os
import re
import subprocess
from pathlib import Path
from urllib.parse import urlparse

requested = os.environ.get("REQUESTED_HOST", "").strip()

paths = [
    Path("/opt/irangate/webpanel/server.json"),
    Path("/opt/irangate/server_config.json"),
    Path("/opt/irangate/database/settings.json"),
]

keys = (
    "ikev2_server_host",
    "server_domain",
    "server_ip",
    "public_ip",
)

def normalize(value):
    value = str(value or "").strip()

    if not value:
        return ""

    if "://" in value:
        value = urlparse(value).hostname or ""

    value = value.strip().strip("[]")

    if value.count(":") == 1:
        host, port = value.rsplit(":", 1)
        if port.isdigit():
            value = host

    if not re.fullmatch(r"[A-Za-z0-9._:-]+", value):
        return ""

    return value

value = normalize(requested)
if value:
    print(value)
    raise SystemExit

for path in paths:
    try:
        data = json.loads(path.read_text())
    except Exception:
        continue

    if not isinstance(data, dict):
        continue

    for key in keys:
        value = normalize(data.get(key))
        if value:
            print(value)
            raise SystemExit

try:
    output = subprocess.check_output(
        ["ip", "-4", "route", "get", "1.1.1.1"],
        text=True,
        stderr=subprocess.DEVNULL,
    )

    fields = output.split()

    if "src" in fields:
        value = normalize(fields[fields.index("src") + 1])
        if value:
            print(value)
            raise SystemExit
except Exception:
    pass

raise SystemExit("Unable to determine IKEv2 public host")
PY_HOST
}

install_packages() {
    if [[ "$NO_PACKAGES" -eq 1 ]]; then
        return
    fi

    if command -v swanctl >/dev/null 2>&1 &&
       command -v pki >/dev/null 2>&1
    then
        return
    fi

    log "Installing strongSwan packages"

    export DEBIAN_FRONTEND=noninteractive

    apt-get update

    apt-get install -y \
        strongswan \
        strongswan-pki \
        strongswan-swanctl \
        charon-systemd \
        libcharon-extra-plugins \
        libstrongswan-extra-plugins \
        iptables \
        openssl
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Required command missing: $1" >&2
        exit 1
    }
}

generate_ca_if_missing() {
    local ca_key="/etc/swanctl/private/irangate-ca-key.pem"
    local ca_cert="/etc/swanctl/x509ca/irangate-ca-cert.pem"

    if [[ -s "$ca_key" && -s "$ca_cert" ]]; then
        log "Existing IKEv2 CA preserved"
        return
    fi

    log "Generating IKEv2 CA"

    rm -f "$ca_key" "$ca_cert"

    pki --gen \
        --type rsa \
        --size 4096 \
        --outform pem \
        >"$ca_key"

    pki --self \
        --ca \
        --lifetime 3650 \
        --in "$ca_key" \
        --type rsa \
        --dn "CN=IranGate IKEv2 CA" \
        --outform pem \
        >"$ca_cert"

    chmod 0600 "$ca_key"
    chmod 0644 "$ca_cert"
}

generate_server_certificate() {
    local host="$1"

    local ca_key="/etc/swanctl/private/irangate-ca-key.pem"
    local ca_cert="/etc/swanctl/x509ca/irangate-ca-cert.pem"
    local server_key="/etc/swanctl/private/irangate-server-key.pem"
    local server_cert="/etc/swanctl/x509/irangate-server-cert.pem"

    local current_cn=""

    if [[ -s "$server_cert" ]]; then
        current_cn="$(
            openssl x509 \
                -in "$server_cert" \
                -noout \
                -subject \
                -nameopt RFC2253 2>/dev/null |
            sed -n 's/^subject=CN=//p'
        )"
    fi

    if [[ -s "$server_key" &&
          -s "$server_cert" &&
          "$current_cn" == "$host" ]]
    then
        log "Existing server certificate matches $host"
        return
    fi

    if [[ -e "$server_key" || -e "$server_cert" ]]; then
        local backup="/root/irangate-ikev2-cert-before-$(date +%Y%m%d-%H%M%S)"
        mkdir -p "$backup"

        cp -a "$server_key" "$server_cert" "$backup/" 2>/dev/null || true

        log "Previous server certificate saved to $backup"
    fi

    log "Generating server certificate for $host"

    pki --gen \
        --type rsa \
        --size 4096 \
        --outform pem \
        >"$server_key"

    pki --pub \
        --in "$server_key" \
        --type rsa |
    pki --issue \
        --lifetime 1825 \
        --cacert "$ca_cert" \
        --cakey "$ca_key" \
        --dn "CN=$host" \
        --san "$host" \
        --flag serverAuth \
        --flag ikeIntermediate \
        --outform pem \
        >"$server_cert"

    chmod 0600 "$server_key"
    chmod 0644 "$server_cert"
}

write_connection() {
    local host="$1"

    cat >/etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf <<EOF
connections {
    irangate-ikev2-mschapv2 {
        version = 2
        local_addrs = %any
        remote_addrs = %any
        proposals = aes256-sha256-modp2048,aes128-sha256-modp2048,aes256-sha1-modp2048,aes128-sha1-modp2048
        reauth_time = 0
        unique = no
        pools = irangate-ikev2-pool

        local {
            auth = pubkey
            certs = irangate-server-cert.pem
            id = $host
        }

        remote {
            auth = eap-mschapv2
            eap_id = %any
        }

        children {
            irangate-ikev2-child {
                local_ts = 0.0.0.0/0
                esp_proposals = aes256-sha256,aes128-sha256,aes256-sha1,aes128-sha1
                start_action = none
                dpd_action = clear
            }
        }
    }
}

pools {
    irangate-ikev2-pool {
        addrs = 10.10.0.10-10.10.0.250
        dns = 1.1.1.1,8.8.8.8
    }
}
EOF

    chmod 0600 \
        /etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf
}

main() {
    local host
    host="$(detect_host)"

    log "Public host: $host"

    install_packages

    require_command swanctl
    require_command pki
    require_command openssl
    require_command iptables
    require_command python3

    install -d -m 0700 \
        /opt/irangate/ikev2

    install -d -m 0755 \
        /opt/irangate/traffic \
        /etc/swanctl/conf.d \
        /etc/swanctl/private \
        /etc/swanctl/x509 \
        /etc/swanctl/x509ca

    touch /opt/irangate/ikev2/users.tsv
    chmod 0600 /opt/irangate/ikev2/users.tsv

    for file in \
        /opt/irangate/traffic/ikev2_current_stats.json \
        /opt/irangate/traffic/ikev2_last_seen.json
    do
        if [[ ! -s "$file" ]]; then
            printf '%s\n' '[]' >"$file"
        fi

        chmod 0600 "$file"
    done

    install -m 0755 \
        "$SOURCE_DIR/irangate-ikev2" \
        /usr/local/sbin/irangate-ikev2

    install -m 0755 \
        "$SOURCE_DIR/irangate-ikev2-identity-sync" \
        /usr/local/sbin/irangate-ikev2-identity-sync

    install -m 0755 \
        "$SOURCE_DIR/irangate-ikev2-stats-snapshot.py" \
        /usr/local/sbin/irangate-ikev2-stats-snapshot

    install -m 0755 \
        "$SOURCE_DIR/irangate-ikev2-firewall.sh" \
        /usr/local/sbin/irangate-ikev2-firewall.sh

    install -m 0644 \
        "$SYSTEMD_DIR/irangate-ikev2-firewall.service" \
        /etc/systemd/system/irangate-ikev2-firewall.service

    install -m 0644 \
        "$SYSTEMD_DIR/irangate-ikev2-stats.service" \
        /etc/systemd/system/irangate-ikev2-stats.service

    install -m 0644 \
        "$SYSTEMD_DIR/irangate-ikev2-stats.timer" \
        /etc/systemd/system/irangate-ikev2-stats.timer

    cat >/etc/sysctl.d/99-irangate-ikev2.conf <<'EOF'
net.ipv4.ip_forward=1
net.ipv4.conf.all.accept_redirects=0
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.accept_redirects=0
net.ipv4.conf.default.send_redirects=0
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0

# GOST delivers IKEv2/NAT-T to 127.0.0.1.
net.ipv4.conf.lo.disable_policy=0
net.ipv4.conf.lo.disable_xfrm=0
net.ipv4.conf.lo.rp_filter=0
EOF

    generate_ca_if_missing
    generate_server_certificate "$host"
    write_connection "$host"

    if [[ ! -s /etc/swanctl/conf.d/irangate-ikev2-users.conf ]]; then
        printf '%s\n' 'secrets {' '}' \
            >/etc/swanctl/conf.d/irangate-ikev2-users.conf

        chmod 0600 \
            /etc/swanctl/conf.d/irangate-ikev2-users.conf
    fi

    systemctl daemon-reload
    sysctl --system >/dev/null

    # IranGate uses one strongSwan backend for both IKEv2 and L2TP/IPSec:
    # charon-systemd + swanctl. "disable" alone is not sufficient because
    # ipsec.service aliases the legacy starter unit and may still be started
    # explicitly, creating a second charon that competes for UDP/500,4500.
    systemctl stop \
        strongswan-starter.service \
        ipsec.service \
        >/dev/null 2>&1 || true

    systemctl disable strongswan-starter.service \
        >/dev/null 2>&1 || true

    systemctl mask strongswan-starter.service \
        >/dev/null 2>&1 || {
            echo "Unable to mask legacy strongswan-starter backend" >&2
            exit 1
        }

    systemctl unmask strongswan.service \
        >/dev/null 2>&1 || true

    systemctl enable strongswan.service \
        >/dev/null

    systemctl restart strongswan.service

    if systemctl is-active --quiet strongswan-starter.service; then
        echo "Legacy strongswan-starter is still active" >&2
        exit 1
    fi

    if pgrep -x charon >/dev/null 2>&1; then
        echo "Legacy charon process is still running" >&2
        ps -ef | grep -E '[c]haron|[s]tarter' || true
        exit 1
    fi

    if ! pgrep -x charon-systemd >/dev/null 2>&1; then
        echo "charon-systemd is not running" >&2
        exit 1
    fi

    /usr/local/sbin/irangate-ikev2 reload

    systemctl enable irangate-ikev2-firewall.service
    systemctl restart irangate-ikev2-firewall.service

    systemctl enable --now irangate-ikev2-stats.timer

    /usr/local/sbin/irangate-ikev2-stats-snapshot

    systemctl is-active --quiet strongswan
    systemctl is-active --quiet irangate-ikev2-firewall.service
    systemctl is-active --quiet irangate-ikev2-stats.timer

    for port in 500 4500; do
        if ! ss -lunH |
            awk -v port="$port" '
                $4 ~ (":" port "$") {
                    found = 1
                }

                END {
                    exit(found ? 0 : 1)
                }
            '
        then
            echo "IKEv2 UDP port $port is not listening" >&2
            exit 1
        fi
    done

    log "IKEv2 installation healthy ✅"
}

main "$@"
