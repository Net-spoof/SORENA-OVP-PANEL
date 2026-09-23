#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

CONNECTION_FILE="/etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf"
L2TP_CONNECTION_FILE="/etc/swanctl/conf.d/irangate-l2tp.conf"
VICI_CONF="/etc/strongswan.d/charon/vici.conf"
SWANCTL_MAIN="/etc/swanctl/swanctl.conf"

log() {
    echo "[ikev2-runtime] $*"
}

if [ ! -f "$CONNECTION_FILE" ]; then
    log "IKEv2 connection file not present yet; skipped."
    exit 0
fi

command -v swanctl >/dev/null 2>&1 || {
    log "swanctl command not found ❌"
    exit 1
}

mkdir -p \
    /etc/strongswan.d/charon \
    /etc/swanctl/conf.d

python3 - "$VICI_CONF" <<'PY'
from pathlib import Path
import re
import sys

path = Path(sys.argv[1])
text = path.read_text() if path.exists() else ""

if re.search(r"(?m)^\s*load\s*=", text):
    text = re.sub(
        r"(?m)^(\s*load\s*=\s*).*$",
        r"\1yes",
        text,
    )
elif re.search(r"(?m)^\s*vici\s*\{", text):
    text = re.sub(
        r"(?m)^(\s*vici\s*\{)",
        r"\1\n    load = yes",
        text,
        count=1,
    )
else:
    if text and not text.endswith("\n"):
        text += "\n"

    text += """
vici {
    load = yes
}
"""

path.write_text(text)
PY

chmod 0644 "$VICI_CONF"

if [ ! -f "$SWANCTL_MAIN" ]; then
    cat >"$SWANCTL_MAIN" <<'EOF_MAIN'
include conf.d/*.conf
EOF_MAIN
elif ! grep -Eq \
    '^[[:space:]]*include[[:space:]]+conf\.d/\*\.conf' \
    "$SWANCTL_MAIN"
then
    printf '\ninclude conf.d/*.conf\n' >>"$SWANCTL_MAIN"
fi

chmod 0644 "$SWANCTL_MAIN"

# IranGate deliberately uses a single strongSwan backend:
# charon-systemd + swanctl for both IKEv2 and L2TP/IPSec.
#
# Disabling strongswan-starter is not sufficient. ipsec.service is an alias
# for the legacy starter unit and may still be started explicitly while the
# unit is disabled, which creates a second charon process competing for
# UDP/500 and UDP/4500. Stop and mask the legacy backend so this cannot recur.
systemctl stop \
    strongswan-starter.service \
    ipsec.service \
    >/dev/null 2>&1 || true

systemctl disable strongswan-starter.service \
    >/dev/null 2>&1 || true

systemctl mask strongswan-starter.service \
    >/dev/null 2>&1 || {
        log "Unable to mask legacy strongswan-starter backend ❌"
        exit 1
    }

systemctl unmask strongswan.service \
    >/dev/null 2>&1 || true

systemctl enable strongswan.service \
    >/dev/null

systemctl restart strongswan.service

if systemctl is-active --quiet strongswan-starter.service; then
    log "Legacy strongswan-starter is still active ❌"
    exit 1
fi

if pgrep -x charon >/dev/null 2>&1; then
    log "Legacy charon process is still running ❌"
    ps -ef | grep -E '[c]haron|[s]tarter' || true
    exit 1
fi

if ! pgrep -x charon-systemd >/dev/null 2>&1; then
    log "charon-systemd is not running ❌"
    exit 1
fi

VICI_SOCKET=""

for _ in $(seq 1 30); do
    for socket in \
        /run/charon.vici \
        /var/run/charon.vici
    do
        if [ -S "$socket" ]; then
            VICI_SOCKET="$socket"
            break 2
        fi
    done

    sleep 1
done

[ -n "$VICI_SOCKET" ] || {
    log "VICI socket was not created ❌"
    journalctl \
        -u strongswan.service \
        --since '-3 minutes' \
        --no-pager \
        -n 150 || true
    exit 1
}

URI="unix://$VICI_SOCKET"

swanctl \
    --load-all \
    --noprompt \
    --uri "$URI"

CONNECTIONS="$(
    swanctl \
        --list-conns \
        --uri "$URI"
)"

printf '%s\n' "$CONNECTIONS"

grep -q 'irangate-ikev2-mschapv2' <<<"$CONNECTIONS" || {
    log "IranGate IKEv2 connection was not loaded ❌"
    exit 1
}

if [ -f "$L2TP_CONNECTION_FILE" ]; then
    grep -q 'irangate-l2tp' <<<"$CONNECTIONS" || {
        log "IranGate L2TP connection file exists but was not loaded ❌"
        exit 1
    }
fi

SOCKETS="$(ss -lunpH 2>/dev/null || true)"

for port in 500 4500; do
    if ! grep -Eq \
        "[:.]${port}[[:space:]].*charon-systemd" \
        <<<"$SOCKETS"
    then
        log "UDP $port is not owned by charon-systemd ❌"
        printf '%s\n' "$SOCKETS" |
            grep -E ':(500|4500)[[:space:]]' || true
        exit 1
    fi
done

log "VICI enabled, IKEv2/L2TP loaded, UDP 500/4500 owned by charon-systemd ✅"
