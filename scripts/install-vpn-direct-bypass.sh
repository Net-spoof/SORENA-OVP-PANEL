#!/usr/bin/env bash
set -Eeuo pipefail

# IRANGATE_VPN_DIRECT_BYPASS_INSTALLER_V1
#
# This bypass affects only the legacy:
#   IRANGATE_WARP_GEMINI
#   IRANGATE_WARP_QUIC
#
# The main irangate-routing TPROXY/outbound engine remains available.

MODE="${1:-master}"

case "$MODE" in
    master)
        VPN_SUBNETS="${IRANGATE_VPN_BYPASS_SUBNETS:-10.8.0.0/24 10.10.0.0/24}"
        ;;

    node)
        VPN_SUBNETS="${IRANGATE_VPN_BYPASS_SUBNETS:-10.88.0.0/24}"
        ;;

    *)
        echo "Usage: $0 {master|node}" >&2
        exit 2
        ;;
esac

[ "$(id -u)" -eq 0 ] || {
    echo "Run as root." >&2
    exit 1
}

SCRIPT_DIR="$(
    cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &&
    pwd
)"

REPO_ROOT="$(
    cd -- "$SCRIPT_DIR/.." 2>/dev/null &&
    pwd || true
)"

RAW_BASE="${IRANGATE_RAW_BASE:-https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat}"

HELPER_REL="deploy/common/usr/local/sbin/irangate-vpn-direct-bypass"
SERVICE_REL="deploy/common/etc/systemd/system/irangate-vpn-direct-bypass.service"

HELPER_SOURCE="${REPO_ROOT:+$REPO_ROOT/}$HELPER_REL"
SERVICE_SOURCE="${REPO_ROOT:+$REPO_ROOT/}$SERVICE_REL"

TEMP_DIR=""

cleanup() {
    [ -z "$TEMP_DIR" ] || rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

download_runtime_sources() {
    TEMP_DIR="$(mktemp -d)"

    HELPER_SOURCE="$TEMP_DIR/irangate-vpn-direct-bypass"
    SERVICE_SOURCE="$TEMP_DIR/irangate-vpn-direct-bypass.service"

    curl -4 \
        -fsSL \
        --retry 5 \
        --retry-delay 2 \
        --connect-timeout 15 \
        -o "$HELPER_SOURCE" \
        "$RAW_BASE/$HELPER_REL"

    curl -4 \
        -fsSL \
        --retry 5 \
        --retry-delay 2 \
        --connect-timeout 15 \
        -o "$SERVICE_SOURCE" \
        "$RAW_BASE/$SERVICE_REL"
}

if [ ! -f "$HELPER_SOURCE" ] ||
   [ ! -f "$SERVICE_SOURCE" ]; then
    download_runtime_sources
fi

bash -n "$HELPER_SOURCE"

install -m 0755 \
    "$HELPER_SOURCE" \
    /usr/local/sbin/irangate-vpn-direct-bypass

install -m 0644 \
    "$SERVICE_SOURCE" \
    /etc/systemd/system/irangate-vpn-direct-bypass.service

cat >/etc/default/irangate-vpn-direct-bypass <<EOF
VPN_SUBNETS="$VPN_SUBNETS"
EOF

chmod 0644 \
    /etc/default/irangate-vpn-direct-bypass

mkdir -p \
    /etc/systemd/system/irangate-warp-gemini-rules.service.d

cat >/etc/systemd/system/irangate-warp-gemini-rules.service.d/90-vpn-direct-bypass.conf <<'EOF'
[Service]
ExecStartPost=/usr/local/sbin/irangate-vpn-direct-bypass apply
EOF

systemctl daemon-reload

systemctl enable \
    irangate-vpn-direct-bypass.service \
    >/dev/null

systemctl restart \
    irangate-vpn-direct-bypass.service

echo
/usr/local/sbin/irangate-vpn-direct-bypass status

echo
echo "VPN direct bypass installed ✅"
echo "Mode:    $MODE"
echo "Subnets: $VPN_SUBNETS"
echo "Main IranGate outbound routing remains available ✅"
