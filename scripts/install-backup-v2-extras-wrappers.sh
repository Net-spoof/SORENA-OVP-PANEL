#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

EXPORT_CORE="$REPO_DIR/deploy/master/scripts/irangate-backup-v2-export.py"
IMPORT_CORE="$REPO_DIR/deploy/master/scripts/irangate-backup-v2-import.py"

[[ -f "$EXPORT_CORE" ]] || {
    echo "Missing export core: $EXPORT_CORE" >&2
    exit 1
}

[[ -f "$IMPORT_CORE" ]] || {
    echo "Missing import core: $IMPORT_CORE" >&2
    exit 1
}

install -m 0755 \
    "$EXPORT_CORE" \
    /usr/local/bin/irangate-backup-v2-export.py

install -m 0755 \
    "$IMPORT_CORE" \
    /usr/local/bin/irangate-backup-v2-import.py

install -m 0755 \
    "$REPO_DIR/scripts/backup-v2/irangate-backup-v2-export-wrapper" \
    /usr/local/bin/irangate-backup-v2-export

install -m 0755 \
    "$REPO_DIR/scripts/backup-v2/irangate-backup-v2-import-wrapper" \
    /usr/local/bin/irangate-backup-v2-import

echo "Backup v2 unified wrappers installed ✅"
