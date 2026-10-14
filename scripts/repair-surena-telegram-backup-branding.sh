#!/usr/bin/env bash
# Safe, targeted SURENA Telegram backup branding update for an installed Master.
# Changes only the Go backup Telegram caption and its webpanel binary.
set -Eeuo pipefail
umask 077
export PATH="/usr/local/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"
SERVICE=irangate-webpanel
BIN=/usr/local/bin/irangate-webpanel
ENV_FILE=/etc/irangate/webpanel.env
die() { echo "[SURENA][ERROR] $*" >&2; exit 1; }
say() { echo "[SURENA] $*"; }
[[ $EUID == 0 ]] || die "Run as root"
for c in go python3 timeout curl systemctl; do command -v "$c" >/dev/null || die "Missing $c"; done
[[ -s "$BIN" && -s "$ENV_FILE" ]] || die "Panel binary or environment missing"
PORT="$(sed -nE 's/^WEBPANEL_PORT=([0-9]+)$/\1/p' "$ENV_FILE" | tail -n 1)"
[[ "$PORT" =~ ^[0-9]+$ ]] || die "Cannot determine webpanel port"
health() { curl -fsS --noproxy '*' --max-time 5 "http://127.0.0.1:$PORT/api/health" >/dev/null; }
systemctl is-active --quiet "$SERVICE" && health || die "Panel is not healthy before update"

# Do not interrupt an ongoing database restoration or backup export/import.
if ps -eo args= | grep -Eq '[/]usr/local/bin/irangate-backup-v2-(export|import)|[p]g_restore'; then
    die "Backup/restore is running; retry after it finishes"
fi

SOURCE="$(cat /etc/irangate/source_dir 2>/dev/null || true)"
if [[ -n "$SOURCE" && ! -f "$SOURCE/webpanel/backend/telegram_backup_scheduler.go" ]]; then
    SOURCE=""
fi
if [[ -z "$SOURCE" ]]; then
    for candidate in /root/IranGate-OV-Reza /opt/IranGate-OV-Reza /root/SORENA-OVP-PANEL /root/surena-src; do
        if [[ -f "$candidate/webpanel/backend/telegram_backup_scheduler.go" ]]; then
            SOURCE="$candidate"
            break
        fi
    done
fi
[[ -n "$SOURCE" ]] || die "Installed source tree not found; no files changed"
BACKEND="$SOURCE/webpanel/backend"
SENDER="$BACKEND/telegram_backup_scheduler.go"
[[ -f "$BACKEND/go.mod" ]] || die "Unexpected Go source structure"
grep -Fq 'func sendTelegramBackupDocument(' "$SENDER" || die "Unexpected backup implementation"

BACK="$(mktemp -d /root/surena-telegram-caption-rollback.XXXXXXXX)"
cp -a "$BIN" "$BACK/original-binary"
cp -a "$SENDER" "$BACK/original-sender.go"
say "Source: $SOURCE"
say "Rollback files: $BACK"
SWAPPED=0
DONE=0
cleanup() {
    rc=$?
    trap - EXIT
    if [[ "$DONE" -ne 1 ]]; then
        cp -a "$BACK/original-sender.go" "$SENDER" || true
        if [[ "$SWAPPED" -eq 1 ]]; then
            install -m 0755 "$BACK/original-binary" "$BIN.rollback.$$" &&
                mv -f "$BIN.rollback.$$" "$BIN"
            systemctl restart "$SERVICE" || true
            if systemctl is-active --quiet "$SERVICE" && health; then
                say "Rollback completed; original panel healthy"
            else
                say "ALERT: inspect journalctl -u $SERVICE"
            fi
        fi
    fi
    exit "$rc"
}
trap cleanup EXIT

python3 - "$SENDER" <<'PY'
from pathlib import Path
import sys

p = Path(sys.argv[1])
s = p.read_text(encoding="utf-8")
old = "IranGate Backup v2"
new = "🛡️ SURENA Backup V2"
old_caption = '"IranGate Backup v2\\n'
new_caption = '"🛡️ SURENA Backup V2\\n'

# Installed Master versions may have a single caption or multipart variants.
# Restrict changes to the displayed Telegram backup title only.
if s.count(old) in (1, 3):
    if s.count(old_caption) not in (1, 2):
        raise SystemExit("STOP: unknown caption layout; no code changed")
    s = s.replace(old_caption, new_caption)
    s = s.replace("✅ IranGate Backup v2 sent in", "✅ SURENA Backup V2 sent in")
    if old in s or new not in s:
        raise SystemExit("STOP: title substitution did not complete")
elif s.count(old) == 0 and new in s:
    print("[SURENA] Local sender already uses SURENA branding")
else:
    raise SystemExit(
        f"STOP: unsupported sender title count: {s.count(old)}"
    )

p.write_text(s, encoding="utf-8")
PY

say "Testing and compiling the existing backend before any restart"
(
  cd "$BACKEND"
  export GOTOOLCHAIN=auto
  timeout 600 go test -run '^TestTelegramBackup' -count=1 .
  timeout 600 go build -trimpath -o "$BACK/candidate" .
)
[[ -s "$BACK/candidate" ]] || die "Build produced no executable"
systemctl is-active --quiet "$SERVICE" && health || die "Panel became unhealthy; abort"
if ps -eo args= | grep -Eq '[/]usr/local/bin/irangate-backup-v2-(export|import)|[p]g_restore'; then
    die "Backup/restore started during build; retry later"
fi

say "Replacing only the panel executable, then checking health"
SWAPPED=1
install -m 0755 "$BACK/candidate" "$BIN.new.$$"
mv -f "$BIN.new.$$" "$BIN"
systemctl restart "$SERVICE"
OK=0
for _ in $(seq 1 45); do
    if systemctl is-active --quiet "$SERVICE" && health; then OK=1; break; fi
    sleep 1
done
[[ "$OK" -eq 1 ]] || die "Panel failed health check; automatic rollback starting"
DONE=1
say "SUCCESS: next Telegram backup uses SURENA Backup V2"
say "Panel healthy; rollback copies: $BACK"
