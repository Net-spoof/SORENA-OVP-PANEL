#!/usr/bin/env bash
set -Eeuo pipefail

# Frontend-only deploy for the Nodes ON/OFF UI.
# IMPORTANT: this change does not require rebuilding or restarting irangate-webpanel.

if [[ ${EUID} -ne 0 ]]; then
    echo "Run as root: sudo bash $0"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SRC="$REPO_ROOT/webpanel/frontend/nodes.html"
DST="/opt/irangate/webpanel/frontend/nodes.html"
BACKUP_DIR="/root/irangate-update-backups"
STAMP="$(date +%Y%m%d-%H%M%S)"

if [[ ! -f "$SRC" ]]; then
    echo "ERROR: source file not found: $SRC"
    exit 1
fi

if ! grep -q 'toggleNodeEnabled' "$SRC"; then
    echo "ERROR: source does not contain the node ON/OFF UI"
    exit 1
fi

if [[ ! -f "$DST" ]]; then
    echo "ERROR: live nodes page not found: $DST"
    exit 1
fi

mkdir -p "$BACKUP_DIR"
cp -a "$DST" "$BACKUP_DIR/nodes.html.before-toggle-$STAMP.bak"

install -m 0644 "$SRC" "$DST"

if ! cmp -s "$SRC" "$DST"; then
    echo "ERROR: deployed file does not match source; restoring backup"
    cp -af "$BACKUP_DIR/nodes.html.before-toggle-$STAMP.bak" "$DST"
    exit 1
fi

echo "Node ON/OFF UI deployed successfully."
echo "Backup: $BACKUP_DIR/nodes.html.before-toggle-$STAMP.bak"
echo "No backend rebuild or irangate-webpanel restart was performed."
echo "Refresh the Nodes page with Ctrl+F5."
