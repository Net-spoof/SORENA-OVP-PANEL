#!/usr/bin/env bash
set -Eeuo pipefail

SUBNET="10.20.0.0/24"
INPUT_CHAIN="IRANGATE_L2TP_IN"
FORWARD_CHAIN="IRANGATE_L2TP_FWD"
NAT_CHAIN="IRANGATE_L2TP_NAT"

wan_interface() {
    ip -4 route get 1.1.1.1 2>/dev/null \
        | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}'
}

ensure_chain() {
    local table="$1"
    local chain="$2"
    iptables -t "$table" -N "$chain" 2>/dev/null || true
    iptables -t "$table" -F "$chain"
}

ensure_jump() {
    local table="$1"
    local parent="$2"
    local chain="$3"
    if ! iptables -t "$table" -C "$parent" -j "$chain" 2>/dev/null; then
        iptables -t "$table" -I "$parent" 1 -j "$chain"
    fi
}

remove_jump() {
    local table="$1"
    local parent="$2"
    local chain="$3"
    while iptables -t "$table" -C "$parent" -j "$chain" 2>/dev/null; do
        iptables -t "$table" -D "$parent" -j "$chain" || break
    done
}

start_rules() {
    local wan
    wan="$(wan_interface)"
    [ -n "$wan" ] || {
        echo "Unable to determine outbound interface" >&2
        exit 1
    }

    sysctl -w net.ipv4.ip_forward=1 >/dev/null

    ensure_chain filter "$INPUT_CHAIN"
    ensure_chain filter "$FORWARD_CHAIN"
    ensure_chain nat "$NAT_CHAIN"

    # IKE/NAT-T is handled by strongSwan. UDP/1701 itself must never be
    # reachable in plaintext; accept it only when the inbound packet has an
    # IPsec policy and drop all other direct L2TP packets.
    iptables -A "$INPUT_CHAIN" -p udp --dport 500 -j ACCEPT
    iptables -A "$INPUT_CHAIN" -p udp --dport 4500 -j ACCEPT
    iptables -A "$INPUT_CHAIN" -p udp --dport 1701 \
        -m policy --dir in --pol ipsec --proto esp -j ACCEPT
    iptables -A "$INPUT_CHAIN" -p udp --dport 1701 -j DROP
    iptables -A "$INPUT_CHAIN" -j RETURN

    iptables -A "$FORWARD_CHAIN" -s "$SUBNET" -j ACCEPT
    iptables -A "$FORWARD_CHAIN" -d "$SUBNET" \
        -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    iptables -A "$FORWARD_CHAIN" -j RETURN

    iptables -t nat -A "$NAT_CHAIN" -s "$SUBNET" -o "$wan" -j MASQUERADE
    iptables -t nat -A "$NAT_CHAIN" -j RETURN

    ensure_jump filter INPUT "$INPUT_CHAIN"
    ensure_jump filter FORWARD "$FORWARD_CHAIN"
    ensure_jump nat POSTROUTING "$NAT_CHAIN"
}

stop_rules() {
    remove_jump filter INPUT "$INPUT_CHAIN"
    remove_jump filter FORWARD "$FORWARD_CHAIN"
    remove_jump nat POSTROUTING "$NAT_CHAIN"

    iptables -F "$INPUT_CHAIN" 2>/dev/null || true
    iptables -X "$INPUT_CHAIN" 2>/dev/null || true
    iptables -F "$FORWARD_CHAIN" 2>/dev/null || true
    iptables -X "$FORWARD_CHAIN" 2>/dev/null || true
    iptables -t nat -F "$NAT_CHAIN" 2>/dev/null || true
    iptables -t nat -X "$NAT_CHAIN" 2>/dev/null || true
}

case "${1:-start}" in
    start|apply)
        start_rules
        ;;
    stop|remove)
        stop_rules
        ;;
    restart)
        stop_rules
        start_rules
        ;;
    *)
        echo "usage: $0 start|stop|restart" >&2
        exit 2
        ;;
esac
