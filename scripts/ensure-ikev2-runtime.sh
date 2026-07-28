#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

CONNECTION_FILE="/etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf"
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

systemctl disable --now strongswan-starter.service \
    >/dev/null 2>&1 || true

systemctl unmask strongswan.service \
    >/dev/null 2>&1 || true

systemctl enable strongswan.service \
    >/dev/null

systemctl restart strongswan.service

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

ss -lunp |
grep -Eq ':(500|4500)[[:space:]]' || {
    log "UDP 500/4500 listeners are missing ❌"
    exit 1
}

log "VICI enabled and IKEv2 configuration loaded ✅"
