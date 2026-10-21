#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

EXPORT_CORE="$REPO_DIR/deploy/master/scripts/irangate-backup-v2-export.py"
IMPORT_CORE="$REPO_DIR/deploy/master/scripts/irangate-backup-v2-import.py"
IKEV2_GUARD="$REPO_DIR/scripts/backup-v2/ikev2-credential-guard.py"

[[ -f "$EXPORT_CORE" ]] || {
    echo "Missing export core: $EXPORT_CORE" >&2
    exit 1
}

[[ -f "$IMPORT_CORE" ]] || {
    echo "Missing import core: $IMPORT_CORE" >&2
    exit 1
}

[[ -s "$IKEV2_GUARD" ]] || {
    echo "Missing IKEv2 credential guard: $IKEV2_GUARD" >&2
    exit 1
}

# Never install backup tools that require a missing or invalid guard.
python3 "$IKEV2_GUARD" --self-test >/dev/null
install -m 0755 \
    "$IKEV2_GUARD" \
    /usr/local/sbin/irangate-ikev2-backup-guard

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

echo "Backup v2 wrappers and IKEv2 credential guard installed ✅"
