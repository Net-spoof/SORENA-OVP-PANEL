#!/usr/bin/env bash
# SURENA Backup V2: caption-only fix for an existing Master.
# Patches exact single-part and multipart strings in the existing Go ELF binary.
# No Go rebuild, database/PKI/config changes, or backup-format changes.
set -Eeuo pipefail
umask 077

SERVICE="irangate-webpanel"
BIN="/usr/local/bin/irangate-webpanel"
ENV_FILE="/etc/irangate/webpanel.env"

fail() { echo "[SURENA][ERROR] $*" >&2; exit 1; }
log() { echo "[SURENA] $*"; }

[[ "$EUID" == 0 ]] || fail "Run as root"
for command_name in python3 curl systemctl cp mv mktemp stat sha256sum flock; do
  command -v "$command_name" >/dev/null || fail "Missing: $command_name"
done

[[ -f "$ENV_FILE" && -s "$BIN" ]] || fail "Master environment/binary not found"
PORT="$(sed -nE 's/^WEBPANEL_PORT=([0-9]+)$/\1/p' "$ENV_FILE" | tail -1)"
[[ "$PORT" =~ ^[0-9]+$ ]] || fail "Cannot read WEBPANEL_PORT"

health() {
  systemctl is-active --quiet "$SERVICE" &&
  curl --noproxy '*' --fail --silent --show-error --max-time 5 \
    "http://127.0.0.1:$PORT/api/health" >/dev/null
}
health || fail "Active webpanel is unhealthy; no changes made"

# Do not interrupt exports/restores or a concurrent deployment.
if pgrep -af '[/]usr/local/bin/irangate-backup-v2-(export|import)|[p]g_restore' >/dev/null 2>&1; then
  fail "Backup/restore in progress; retry when idle"
fi
exec 9>/run/surena-telegram-branding.lock
flock -n 9 || fail "Another branding deployment is already running"

BACK="$(mktemp -d /root/surena-caption-binary-rollback.XXXXXXXX)"
cp -a "$BIN" "$BACK/original-binary"
CANDIDATE="$BACK/candidate-binary"
cp -a "$BIN" "$CANDIDATE"
ORIGINAL_SHA="$(sha256sum "$BIN" | awk '{print $1}')"
log "Original binary SHA256: $ORIGINAL_SHA"
log "Rollback directory: $BACK"

# Go embeds the two Telegram format strings independently. Replace only exact
# templates; preserve their byte lengths (16 title bytes + 2 spaces = 18).
# Supports previously-patched single-part caption and unpatched multipart.
python3 - "$CANDIDATE" <<'PY'
from pathlib import Path
import sys

p = Path(sys.argv[1])
original = p.read_bytes()
if not original.startswith(b"\x7fELF"):
    raise SystemExit("STOP: not an ELF executable")
old_title = b"IranGate Backup v2"
new_title = b"SURENA Backup V2  "
if len(old_title) != len(new_title):
    raise SystemExit("STOP: title byte lengths differ")

formats = {
    "single-part": b"\nServer: %s\nCreated: %s\nSize: %.2f MB",
    "multipart": b"\nServer: %s\nCreated: %s\nTotal size: %.2f MB\nPart: %d/%d",
}

patched = original
for name, suffix in formats.items():
    old = old_title + suffix
    new = new_title + suffix
    old_count = patched.count(old)
    new_count = patched.count(new)
    if old_count + new_count != 1:
        raise SystemExit(
            f"STOP: {name} caption layout unexpected "
            f"(old={old_count}, new={new_count}); active binary untouched"
        )
    if old_count == 1:
        patched = patched.replace(old, new, 1)
        print(f"[SURENA] Patched {name} caption: IranGate -> SURENA")
    else:
        print(f"[SURENA] {name} caption already SURENA; unchanged")

# Optional multipart completion note (if present in this installed build).
# Also replace it in-place, without changing other notifications.
old_note = "✅ IranGate Backup v2 sent in %d parts.".encode()
new_note = "✅ SURENA Backup V2   sent in %d parts.".encode()
if len(old_note) != len(new_note):
    raise SystemExit("STOP: note lengths differ")
if patched.count(old_note) > 1 or patched.count(new_note) > 1:
    raise SystemExit("STOP: duplicate multipart notices")
if patched.count(old_note) == 1:
    if patched.count(new_note):
        raise SystemExit("STOP: mixed multipart notices")
    patched = patched.replace(old_note, new_note, 1)
    print("[SURENA] Patched multipart completion note")

if len(patched) != len(original):
    raise SystemExit("STOP: binary length changed unexpectedly")
for name, suffix in formats.items():
    assert patched.count(old_title + suffix) == 0, name
    assert patched.count(new_title + suffix) == 1, name

p.write_bytes(patched)
print("[SURENA] Binary size preserved:", len(patched))
PY

# Check that the candidate remains a Go binary when Go tooling is present.
if command -v go >/dev/null 2>&1; then
  go version -m "$CANDIDATE" >/dev/null ||
    fail "Candidate failed Go binary inspection"
fi
[[ "$(stat -c '%s' "$CANDIDATE")" == "$(stat -c '%s' "$BIN")" ]] ||
  fail "Binary size changed unexpectedly"
health || fail "Panel became unhealthy during preflight; no changes made"

SWAPPED=0
COMPLETED=0
cleanup() {
  rc=$?
  trap - EXIT
  if [[ "$SWAPPED" == 1 && "$COMPLETED" != 1 ]]; then
    log "Reverting to the original binary..."
    cp -a "$BACK/original-binary" "$BIN.rollback.$$" &&
      mv -f "$BIN.rollback.$$" "$BIN"
    systemctl restart "$SERVICE" || true
    if health; then log "Rollback successful"; else log "WARNING: rollback needs investigation"; fi
  fi
  exit "$rc"
}
trap cleanup EXIT

if cmp -s "$CANDIDATE" "$BIN"; then
  log "ALREADY UPDATED: single-part and multipart captions are SURENA"
  COMPLETED=1
  exit 0
fi

log "Applying caption-only binary update"
cp -a "$CANDIDATE" "$BIN.candidate.$$"
SWAPPED=1
mv -f "$BIN.candidate.$$" "$BIN"
systemctl restart "$SERVICE"

HEALTHY=0
for n in $(seq 1 35); do
  if health; then HEALTHY=1; break; fi
  sleep 1
done
if [[ "$HEALTHY" != 1 ]]; then
  journalctl -u "$SERVICE" -n 40 --no-pager >&2 || true
  fail "Updated webpanel is not healthy"
fi

python3 - "$BIN" <<'PY'
from pathlib import Path
import sys
b = Path(sys.argv[1]).read_bytes()
for suffix in (
    b"\nServer: %s\nCreated: %s\nSize: %.2f MB",
    b"\nServer: %s\nCreated: %s\nTotal size: %.2f MB\nPart: %d/%d",
):
    assert b.count(b"SURENA Backup V2  " + suffix) == 1
    assert b.count(b"IranGate Backup v2" + suffix) == 0
print("[SURENA] Both backup caption formats verified")
PY
COMPLETED=1
log "SUCCESS: active webpanel healthy; Telegram backup caption now says SURENA Backup V2"
log "Only the title was changed. Previous backups and services remain intact."
log "Original binary retained in: $BACK"
