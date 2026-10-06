#!/usr/bin/env bash
set -Eeuo pipefail

# Safe frontend-only deploy for the Remote Node ON/OFF feature.
# This feature is implemented entirely with the existing node API.
# It MUST NOT rebuild, stop, start or restart irangate-webpanel.

if [[ ${EUID} -ne 0 ]]; then
    echo "Run as root: sudo bash $0"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_NODES="$REPO_ROOT/webpanel/frontend/nodes.html"
SRC_CLIENTS="$REPO_ROOT/webpanel/frontend/clients.html"

DST_NODES="/opt/irangate/webpanel/frontend/nodes.html"
DST_CLIENTS="/opt/irangate/webpanel/frontend/clients.html"

BACKUP_DIR="/root/irangate-update-backups/node-toggle-$(date +%Y%m%d-%H%M%S)"

for file in "$SRC_NODES" "$SRC_CLIENTS" "$DST_NODES" "$DST_CLIENTS"; do
    if [[ ! -f "$file" ]]; then
        echo "ERROR: required file not found: $file"
        exit 1
    fi
done

if ! grep -q 'toggleNodeEnabled' "$SRC_NODES"; then
    echo "ERROR: nodes.html does not contain the ON/OFF feature"
    exit 1
fi

if ! grep -q 'node.enabled !== false' "$SRC_CLIENTS"; then
    echo "ERROR: clients.html does not contain enabled-node filtering"
    exit 1
fi

mkdir -p "$BACKUP_DIR"
cp -a "$DST_NODES" "$BACKUP_DIR/nodes.html"
cp -a "$DST_CLIENTS" "$BACKUP_DIR/clients.html"

rollback() {
    echo "Rolling back frontend files..."
    cp -af "$BACKUP_DIR/nodes.html" "$DST_NODES"
    cp -af "$BACKUP_DIR/clients.html" "$DST_CLIENTS"
}
trap 'echo "ERROR: frontend deployment failed"; rollback' ERR

install -m 0644 "$SRC_NODES" "$DST_NODES"
install -m 0644 "$SRC_CLIENTS" "$DST_CLIENTS"

cmp -s "$SRC_NODES" "$DST_NODES"
cmp -s "$SRC_CLIENTS" "$DST_CLIENTS"

trap - ERR

echo "Node ON/OFF feature deployed successfully."
echo "Backup: $BACKUP_DIR"
echo
echo "Behavior:"
echo "  - OFF remote nodes stay configured but are hidden from client creation/edit lists."
echo "  - ON remote nodes appear again immediately."
echo "  - Local/master node cannot be switched off from the Nodes page."
echo
echo "Safety:"
echo "  - Backend binary was NOT rebuilt."
echo "  - irangate-webpanel was NOT stopped, started or restarted."
echo "  - systemd, ports, nginx and webpanel.env were NOT changed."
echo
echo "Refresh the browser with Ctrl+F5."
