#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="$REPO_DIR/deploy/master/l2tp"
SYSTEMD_DIR="$REPO_DIR/deploy/master/systemd"

NO_PACKAGES=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-packages)
            NO_PACKAGES=1
            shift
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
    echo "[l2tp-install] $*"
}

require_file() {
    [[ -f "$1" ]] || {
        echo "Required repository asset missing: $1" >&2
        exit 1
    }
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Required command missing: $1" >&2
        exit 1
    }
}

install_packages() {
    if [[ "$NO_PACKAGES" -eq 1 ]]; then
        return
    fi

    if command -v xl2tpd >/dev/null 2>&1 &&
       command -v pppd >/dev/null 2>&1 &&
       command -v swanctl >/dev/null 2>&1
    then
        return
    fi

    if ! command -v apt-get >/dev/null 2>&1; then
        echo "L2TP installer currently supports Debian/Ubuntu package management" >&2
        exit 1
    fi

    log "Installing xl2tpd/PPP/strongSwan dependencies"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y \
        xl2tpd \
        ppp \
        strongswan \
        strongswan-swanctl \
        charon-systemd \
        libcharon-extra-plugins \
        libstrongswan-extra-plugins \
        iptables \
        openssl \
        python3
}

generate_psk() {
    local psk_file="/opt/irangate/l2tp/ipsec-psk"

    if [[ -s "$psk_file" ]]; then
        chmod 0600 "$psk_file"
        log "Existing L2TP IPSec PSK preserved"
        return
    fi

    # 33 random bytes provide >256 bits of entropy. Base64 output has no
    # whitespace and is safe to quote in swanctl.conf.
    openssl rand -base64 33 | tr -d '\n\r' >"$psk_file"
    printf '\n' >>"$psk_file"
    chmod 0600 "$psk_file"
    log "Generated new L2TP IPSec PSK"
}

write_swanctl_connection() {
    local psk
    psk="$(tr -d '\r\n' </opt/irangate/l2tp/ipsec-psk)"
    [[ -n "$psk" ]] || {
        echo "L2TP IPSec PSK is empty" >&2
        exit 1
    }

    cat >/etc/swanctl/conf.d/irangate-l2tp.conf <<EOF
connections {
    irangate-l2tp {
        version = 1
        local_addrs = %any
        remote_addrs = %any

        # AES/SHA1 suites keep compatibility with built-in L2TP/IPSec
        # clients. 3DES/MODP1024 is last-resort legacy compatibility only.
        proposals = aes256-sha1-modp2048,aes128-sha1-modp2048,aes256-sha1-modp1024,aes128-sha1-modp1024,3des-sha1-modp1024
        reauth_time = 0
        unique = no
        dpd_delay = 30s

        local {
            auth = psk
        }

        remote {
            auth = psk
        }

        children {
            irangate-l2tp-transport {
                local_ts = dynamic[udp/1701]
                remote_ts = dynamic[udp]
                mode = transport
                esp_proposals = aes256-sha1,aes128-sha1,3des-sha1
                start_action = none
                dpd_action = clear
                close_action = clear
                rekey_time = 45m
            }
        }
    }
}

secrets {
    ike-irangate-l2tp {
        secret = "$psk"
    }
}
EOF

    chmod 0600 /etc/swanctl/conf.d/irangate-l2tp.conf
}

write_xl2tpd_config() {
    install -d -m 0755 /etc/xl2tpd /etc/ppp

    if [[ -f /etc/xl2tpd/xl2tpd.conf &&
          ! -f /etc/xl2tpd/xl2tpd.conf.irangate-before-l2tp ]]
    then
        cp -a \
            /etc/xl2tpd/xl2tpd.conf \
            /etc/xl2tpd/xl2tpd.conf.irangate-before-l2tp
    fi

    cat >/etc/xl2tpd/xl2tpd.conf <<'EOF'
[global]
port = 1701

[lns default]
ip range = 10.20.0.10-10.20.0.250
local ip = 10.20.0.1
require authentication = yes
name = l2tpd
pppoptfile = /etc/ppp/options.xl2tpd
length bit = yes
EOF

    cat >/etc/ppp/options.xl2tpd <<'EOF'
name l2tpd
auth
require-mschap-v2
refuse-pap
refuse-chap
refuse-mschap
refuse-eap
hide-password
lock
proxyarp
nodefaultroute
mtu 1280
mru 1280
ms-dns 1.1.1.1
ms-dns 8.8.8.8
lcp-echo-interval 30
lcp-echo-failure 4
ipcp-accept-local
ipcp-accept-remote
connect-delay 5000
noccp
EOF

    chmod 0644 /etc/xl2tpd/xl2tpd.conf
    chmod 0600 /etc/ppp/options.xl2tpd

    touch /etc/ppp/chap-secrets
    chmod 0600 /etc/ppp/chap-secrets
}

install_runtime_assets() {
    install -m 0755 \
        "$SOURCE_DIR/irangate-l2tp" \
        /usr/local/sbin/irangate-l2tp

    install -m 0755 \
        "$SOURCE_DIR/irangate-l2tp-stats-snapshot.py" \
        /usr/local/sbin/irangate-l2tp-stats-snapshot

    install -m 0755 \
        "$SOURCE_DIR/irangate-l2tp-firewall.sh" \
        /usr/local/sbin/irangate-l2tp-firewall.sh

    install -m 0755 \
        "$SOURCE_DIR/irangate-l2tp-session-up" \
        /etc/ppp/ip-up.d/90-irangate-l2tp

    install -m 0755 \
        "$SOURCE_DIR/irangate-l2tp-session-down" \
        /etc/ppp/ip-down.d/90-irangate-l2tp

    install -m 0644 \
        "$SYSTEMD_DIR/irangate-l2tp-firewall.service" \
        /etc/systemd/system/irangate-l2tp-firewall.service

    install -m 0644 \
        "$SYSTEMD_DIR/irangate-l2tp-stats.service" \
        /etc/systemd/system/irangate-l2tp-stats.service

    install -m 0644 \
        "$SYSTEMD_DIR/irangate-l2tp-stats.timer" \
        /etc/systemd/system/irangate-l2tp-stats.timer
}

initialize_state() {
    install -d -m 0700 \
        /opt/irangate/l2tp \
        /run/irangate-l2tp \
        /run/irangate-l2tp/sessions

    install -d -m 0755 \
        /opt/irangate/traffic \
        /etc/swanctl/conf.d

    touch /opt/irangate/l2tp/users.tsv
    chmod 0600 /opt/irangate/l2tp/users.tsv

    if [[ ! -s /opt/irangate/l2tp/stats_state.json ]]; then
        printf '%s\n' '{"users":{},"sessions":{}}' \
            >/opt/irangate/l2tp/stats_state.json
    fi
    chmod 0600 /opt/irangate/l2tp/stats_state.json

    for spec in \
        "l2tp_current_stats.json:[]" \
        "l2tp_sessions.json:[]" \
        "l2tp_last_seen.json:{}"
    do
        local file="${spec%%:*}"
        local default="${spec#*:}"
        local path="/opt/irangate/traffic/$file"
        if [[ ! -s "$path" ]]; then
            printf '%s\n' "$default" >"$path"
        fi
        chmod 0644 "$path"
    done
}

sync_existing_ikev2_users() {
    local ike_db="/opt/irangate/ikev2/users.tsv"
    [[ -s "$ike_db" ]] || {
        /usr/local/sbin/irangate-l2tp reload
        return
    }

    log "Synchronizing existing IKEv2 usernames/passwords into L2TP"

    while IFS=$'\t' read -r user pass _created status _rest; do
        [[ -n "$user" && -n "$pass" ]] || continue

        /usr/local/sbin/irangate-l2tp add "$user" "$pass" >/dev/null

        if [[ "$status" == "blocked" ]]; then
            /usr/local/sbin/irangate-l2tp block "$user" >/dev/null
        else
            /usr/local/sbin/irangate-l2tp unblock "$user" >/dev/null
        fi
    done <"$ike_db"
}

health_check() {
    systemctl is-active --quiet strongswan.service
    systemctl is-active --quiet xl2tpd.service
    systemctl is-active --quiet irangate-l2tp-firewall.service
    systemctl is-active --quiet irangate-l2tp-stats.timer

    if ! ss -lunH | awk '$4 ~ /:1701$/ {found=1} END {exit(found ? 0 : 1)}'; then
        echo "L2TP UDP port 1701 is not listening" >&2
        exit 1
    fi

    for port in 500 4500; do
        if ! ss -lunH | awk -v port="$port" '$4 ~ (":" port "$") {found=1} END {exit(found ? 0 : 1)}'; then
            echo "IPSec UDP port $port is not listening" >&2
            exit 1
        fi
    done
}

main() {
    for asset in \
        "$SOURCE_DIR/irangate-l2tp" \
        "$SOURCE_DIR/irangate-l2tp-stats-snapshot.py" \
        "$SOURCE_DIR/irangate-l2tp-firewall.sh" \
        "$SOURCE_DIR/irangate-l2tp-session-up" \
        "$SOURCE_DIR/irangate-l2tp-session-down" \
        "$SYSTEMD_DIR/irangate-l2tp-firewall.service" \
        "$SYSTEMD_DIR/irangate-l2tp-stats.service" \
        "$SYSTEMD_DIR/irangate-l2tp-stats.timer"
    do
        require_file "$asset"
    done

    install_packages

    require_command xl2tpd
    require_command pppd
    require_command swanctl
    require_command iptables
    require_command openssl
    require_command python3

    initialize_state
    generate_psk
    install_runtime_assets
    write_xl2tpd_config
    write_swanctl_connection

    cat >/etc/sysctl.d/99-irangate-l2tp.conf <<'EOF'
net.ipv4.ip_forward=1
net.ipv4.conf.all.accept_redirects=0
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.accept_redirects=0
net.ipv4.conf.default.send_redirects=0
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
EOF

    systemctl daemon-reload
    sysctl --system >/dev/null

    # IranGate uses one charon-systemd instance for both IKEv2 and L2TP/IPSec.
    # Never start the legacy starter daemon in parallel.
    systemctl disable --now strongswan-starter.service \
        >/dev/null 2>&1 || true
    systemctl enable --now strongswan.service

    swanctl --load-conns >/dev/null
    swanctl --load-creds >/dev/null

    sync_existing_ikev2_users

    systemctl enable --now xl2tpd.service
    systemctl restart xl2tpd.service

    systemctl enable irangate-l2tp-firewall.service
    systemctl restart irangate-l2tp-firewall.service

    systemctl enable --now irangate-l2tp-stats.timer
    /usr/local/sbin/irangate-l2tp-stats-snapshot

    health_check

    log "L2TP/IPSec installation healthy ✅"
    log "Shared credentials: IKEv2 username/password are reused for L2TP"
    log "Connection details: irangate-l2tp show USER"
}

main "$@"
