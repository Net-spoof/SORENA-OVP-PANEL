#!/usr/bin/env bash
set -euo pipefail

EXPORT_LIVE="/usr/local/bin/irangate-backup-v2-export"
EXPORT_REAL="/usr/local/bin/irangate-backup-v2-export.real"

IMPORT_LIVE="/usr/local/bin/irangate-backup-v2-import"
IMPORT_REAL="/usr/local/bin/irangate-backup-v2-import.real"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER_DIR="$SCRIPT_DIR/backup-v2"

echo "===== Installing Backup v2 extras wrappers ====="

if [[ ! -x "$EXPORT_LIVE" ]]; then
  echo "$EXPORT_LIVE not found or not executable ❌"
  exit 1
fi

if [[ ! -e "$EXPORT_REAL" ]]; then
  cp -a "$EXPORT_LIVE" "$EXPORT_REAL"
fi

install -m 0755 "$WRAPPER_DIR/irangate-backup-v2-export-wrapper" "$EXPORT_LIVE"

if [[ -x "$IMPORT_LIVE" ]]; then
  if [[ ! -e "$IMPORT_REAL" ]]; then
    cp -a "$IMPORT_LIVE" "$IMPORT_REAL"
  fi

  install -m 0755 "$WRAPPER_DIR/irangate-backup-v2-import-wrapper" "$IMPORT_LIVE"
else
  echo "$IMPORT_LIVE not found; import wrapper skipped ⚠️"
fi

echo "Backup v2 extras wrappers installed ✅"
