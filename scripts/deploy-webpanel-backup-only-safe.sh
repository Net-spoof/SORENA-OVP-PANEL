#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# Deployment of backup endpoint fixes only. Never update service configs,
# VPN services, the database, or the working branch in the existing checkout.
# Run as root: bash deploy-webpanel-backup-only-safe.sh <40-char commit SHA>
if [[ "$(id -u)" -ne 0 ]]; then
    echo "ERROR: run as root" >&2
    exit 1
fi

if [[ "$#" -ne 1 || ! "$1" =~ ^[0-9a-f]{40}$ ]]; then
    echo "Usage: bash $0 <40-character Git commit SHA>" >&2
    exit 2
fi

REV="$1"
REPO="/root/IranGate-OV-Reza"
SERVICE="irangate-webpanel"
BIN="/usr/local/bin/irangate-webpanel"
FRONTEND="/opt/irangate/webpanel/frontend"
PORT=8614

BACK="/root/irangate-webpanel-backup-rollback-$(date +%Y%m%d-%H%M%S)"
WORK="$(mktemp -d /root/irangate-webpanel-staged.XXXXXX)"
STAGED="$BACK/irangate-webpanel.candidate"
WORKTREE_ADDED=0
DEPLOY_STARTED=0

health() {
    curl -fsS --max-time 4 "http://127.0.0.1:$PORT/api/health" |
        grep -q '"status":"healthy"'
}

restore_backup() {
    echo "===== ROLLBACK PREVIOUS HEALTHY WEBPANEL =====" >&2

    install -m 0755 "$BACK/original-binary" "$BIN.rollback.$$" &&
        mv -f "$BIN.rollback.$$" "$BIN"
    install -m 0644 "$BACK/original-settings.html" "$FRONTEND/settings.html.rollback.$$" &&
        mv -f "$FRONTEND/settings.html.rollback.$$" "$FRONTEND/settings.html"
    install -m 0644 "$BACK/original-api.js" "$FRONTEND/js/api.js.rollback.$$" &&
        mv -f "$FRONTEND/js/api.js.rollback.$$" "$FRONTEND/js/api.js"

    systemctl restart "$SERVICE" || true
    for wait_index in $(seq 1 40); do
        if systemctl is-active --quiet "$SERVICE" && health; then
            echo "ROLLBACK VERIFIED: original webpanel healthy" >&2
            return 0
        fi
        sleep 1
    done

    echo "CRITICAL: rollback did not pass health check" >&2
    echo "Original binary and frontend copies remain in $BACK" >&2
    return 1
}

cleanup() {
    rc=$?
    trap - EXIT
    set +e

    if [[ "$rc" -ne 0 && "$DEPLOY_STARTED" -eq 1 ]]; then
        restore_backup || true
    fi

    if [[ "$WORKTREE_ADDED" -eq 1 ]]; then
        git -C "$REPO" worktree remove --force "$WORK" >/dev/null 2>&1 || true
    fi
    rmdir "$WORK" >/dev/null 2>&1 || true

    if [[ "$rc" -ne 0 ]]; then
        echo "DEPLOYMENT FAILED (exit $rc). Rollback directory: $BACK" >&2
    fi
    exit "$rc"
}
trap cleanup EXIT

[[ -f "$REPO/.git" || -d "$REPO/.git" ]] || {
    echo "ERROR: source repository not found: $REPO" >&2
    exit 1
}
command -v go >/dev/null || {
    echo "ERROR: Go is not installed; no live files changed" >&2
    exit 1
}
command -v git >/dev/null
command -v curl >/dev/null
command -v timeout >/dev/null

[[ -f "$FRONTEND/settings.html" && -f "$FRONTEND/js/api.js" ]] || {
    echo "ERROR: unexpected frontend install location. No files changed." >&2
    exit 1
}
[[ -s "$BIN" ]] || {
    echo "ERROR: existing backend binary missing" >&2
    exit 1
}

echo "===== CHECK EXISTING PANEL ====="
systemctl is-active --quiet "$SERVICE"
health
echo "Live webpanel healthy."

# This host previously failed on port collision. Refuse deployment if the
# original protection has been removed, rather than risking another 502.
if ! sysctl -n net.ipv4.ip_local_reserved_ports |
    tr ',' '\n' | grep -Fxq "$PORT"; then
    echo "ERROR: TCP port $PORT is not in ip_local_reserved_ports" >&2
    echo "Previous port-collision protection is missing. No files changed." >&2
    exit 1
fi

git -C "$REPO" cat-file -e "$REV^{commit}" || {
    echo "ERROR: revision not fetched. Run git fetch first." >&2
    exit 1
}

mkdir -p -m 0700 "$BACK"
cp -a "$BIN" "$BACK/original-binary"
cp -a "$FRONTEND/settings.html" "$BACK/original-settings.html"
cp -a "$FRONTEND/js/api.js" "$BACK/original-api.js"

echo "===== ISOLATED SOURCE WORKTREE ====="
git -C "$REPO" worktree add --detach "$WORK" "$REV"
WORKTREE_ADDED=1

grep -Fq 'completed_run_id' "$WORK/webpanel/frontend/settings.html"
grep -Fq 'beginTelegramBackupRun' "$WORK/webpanel/backend/telegram_backup_scheduler.go"
grep -Fq 'validateSelectedBackup' "$WORK/webpanel/frontend/settings.html"

# Optional frontend syntax checks. These run before replacing live files.
if command -v node >/dev/null 2>&1; then
    echo "===== CHECK BACKUP UI JAVASCRIPT ====="
    node --check "$WORK/webpanel/frontend/js/api.js"

    python3 - "$WORK/webpanel/frontend/settings.html" "$BACK/settings.inline.js" <<'PY'
import re
import sys
from pathlib import Path

html = Path(sys.argv[1]).read_text(encoding="utf-8")
scripts = re.findall(r"<script\b[^>]*>(.*?)</script>", html, flags=re.I | re.S)
inline = [source for source in scripts if source.strip()]
if not inline:
    raise SystemExit("Missing inline settings JavaScript")
Path(sys.argv[2]).write_text("\n;\n".join(inline), encoding="utf-8")
PY
    node --check "$BACK/settings.inline.js"
fi

echo "===== BUILD & TEST BEFORE STOPPING PANEL ====="
(
    cd "$WORK/webpanel/backend"
    # GOTOOLCHAIN=auto may download Go 1.26; if unavailable, stop safely.
    export GOTOOLCHAIN=auto
    timeout 480 go test -run '^TestTelegramBackup' -count=1 .
    timeout 480 go build -trimpath -o "$STAGED" .
)
[[ -s "$STAGED" ]]
chmod 0755 "$STAGED"
go version -m "$STAGED" | head -12

echo "===== ATOMIC INSTALL & RESTART ====="
DEPLOY_STARTED=1

install -m 0755 "$STAGED" "$BIN.new.$$"
mv -f "$BIN.new.$$" "$BIN"

install -m 0644 "$WORK/webpanel/frontend/settings.html" "$FRONTEND/settings.html.new.$$"
mv -f "$FRONTEND/settings.html.new.$$" "$FRONTEND/settings.html"

install -m 0644 "$WORK/webpanel/frontend/js/api.js" "$FRONTEND/js/api.js.new.$$"
mv -f "$FRONTEND/js/api.js.new.$$" "$FRONTEND/js/api.js"

systemctl restart "$SERVICE"

echo "===== HEALTH CHECK ====="
HEALTHY=0
for wait_index in $(seq 1 45); do
    if systemctl is-active --quiet "$SERVICE" && health; then
        HEALTHY=1
        break
    fi
    sleep 1
done

if [[ "$HEALTHY" -ne 1 ]]; then
    echo "ERROR: updated backend failed health check" >&2
    journalctl -u "$SERVICE" -n 30 --no-pager >&2 || true
    exit 1
fi

# Confirm the updated frontend was published.
grep -Fq 'completed_run_id' "$FRONTEND/settings.html"
grep -Fq 'window.setTimeout(() => window.URL.revokeObjectURL' "$FRONTEND/js/api.js"

echo "===== SUCCESS ====="
echo "Webpanel: HEALTHY"
echo "Only backup code and its two frontend files deployed"
echo "Source revision: $REV"
echo "Previous binary and frontend: $BACK"
echo "VPN, node configuration, database and nginx: UNCHANGED"
