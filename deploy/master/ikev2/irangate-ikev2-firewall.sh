#!/usr/bin/env bash

POOL_CIDR="10.10.0.0/24"
WAN_IF="$(ip route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}' | head -1)"
[ -z "$WAN_IF" ] && WAN_IF="eth0"

sysctl -w net.ipv4.ip_forward=1 >/dev/null
sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null
sysctl -w net.ipv4.conf.default.rp_filter=0 >/dev/null
sysctl -w net.ipv4.conf.all.send_redirects=0 >/dev/null
sysctl -w net.ipv4.conf.default.send_redirects=0 >/dev/null

iptables -C INPUT -p udp -m multiport --dports 500,4500 -j ACCEPT 2>/dev/null || \
iptables -I INPUT -p udp -m multiport --dports 500,4500 -j ACCEPT

iptables -C FORWARD -s "$POOL_CIDR" -j ACCEPT 2>/dev/null || \
iptables -I FORWARD -s "$POOL_CIDR" -j ACCEPT

iptables -C FORWARD -d "$POOL_CIDR" -j ACCEPT 2>/dev/null || \
iptables -I FORWARD -d "$POOL_CIDR" -j ACCEPT

iptables -t nat -C POSTROUTING -s "$POOL_CIDR" -o "$WAN_IF" -j MASQUERADE 2>/dev/null || \
iptables -t nat -A POSTROUTING -s "$POOL_CIDR" -o "$WAN_IF" -j MASQUERADE

echo "IKEv2 firewall active on $WAN_IF for $POOL_CIDR"
