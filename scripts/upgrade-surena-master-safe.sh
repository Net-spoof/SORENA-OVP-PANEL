#!/usr/bin/env bash
# Safely stage and optionally deploy repository CODE onto an existing Master.
# Never rerun the fresh-machine .run on a populated Master.
set -Eeuo pipefail
umask 077
export PATH="/usr/local/go/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"
MODE="$1"
REV="$2"
AUTH="$SURENA_GITHUB_AUTH_CONFIG"
FRONT="/opt/irangate/webpanel/frontend"
BIN="/usr/local/bin/irangate-webpanel"
CLI="/usr/local/bin/irangate"
BACK=""
CHANGED=0
DONE=0

log() { printf '[SURENA UPGRADE] %s\n' "$*"; }
die() { log "STOP: $*" >&2; exit 1; }
[[ "$EUID" -eq 0 ]] || die "Must run as root"
[[ "$MODE" == --preflight || "$MODE" == --apply ]] || die "Usage: --preflight|--apply COMMIT"
[[ "$REV" =~ ^[a-f0-9]{40}$ ]] || die "Pin an exact 40-character commit"
[[ -f "$AUTH" && ! -L "$AUTH" ]] || die "Provide secure curl config"
for cmd in curl tar go python3 systemctl sha256sum pg_dump flock; do
    command -v "$cmd" >/dev/null || die "Missing $cmd"
done
[[ -s "$BIN" && -d "$FRONT" && -f /etc/irangate-db.env ]] ||
    die "Not an existing SURENA Master"
PORT="$(sed -nE 's/^WEBPANEL_PORT=([0-9]+)$/\1/p' /etc/irangate/webpanel.env | tail -1)"
[[ "$PORT" =~ ^[0-9]+$ ]] || die "Unknown webpanel port"
health() {
    systemctl is-active --quiet irangate-webpanel &&
    curl --noproxy '*' -fsS --max-time 5 "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1
}
health || die "Panel must be healthy before upgrade"
exec 9>/run/surena-code-upgrade.lock
flock -n 9 || die "Concurrent upgrade detected"

WORK="$(mktemp -d /root/surena-stage.XXXXXXXX)"
on_exit() {
    local rc=$?
    trap - EXIT
    if [[ "$CHANGED" -eq 1 && "$DONE" -ne 1 ]]; then
        log "Restoring prior code from $BACK"
        systemctl stop irangate-webpanel || true
        if [[ -d "$BACK/frontend" ]]; then
            rm -rf -- "$FRONT"
            cp -a "$BACK/frontend" "$FRONT"
        fi
        for file in "$BACK"/bin/*; do
            [[ -e "$file" ]] && cp -a "$file" "/usr/local/bin/$(basename "$file")"
        done
        for file in "$BACK"/sbin/*; do
            [[ -e "$file" ]] && cp -a "$file" "/usr/local/sbin/$(basename "$file")"
        done
        systemctl start irangate-webpanel || true
        health && log "Rollback healthy" || log "MANUAL RECOVERY REQUIRED: $BACK"
    fi
    rm -rf -- "$WORK"
    exit "$rc"
}
trap on_exit EXIT

mkdir "$WORK/source" "$WORK/build"
log "Downloading pinned private source $REV"
curl -fLsS --retry 2 --connect-timeout 15 --max-time 240 \
    --config "$AUTH" \
    "https://api.github.com/repos/Net-spoof/SORENA-OVP-PANEL/tarball/$REV" \
    -o "$WORK/source.tar.gz"
tar -tzf "$WORK/source.tar.gz" >/dev/null || die "Invalid source archive"
tar -xzf "$WORK/source.tar.gz" --strip-components=1 -C "$WORK/source"
SRC="$WORK/source"
[[ -s "$SRC/webpanel/backend/go.mod" ]] || die "Incomplete backend"
for tool in irangate-pg-usage-tick.py irangate-local-status-snapshot.py irangate-node-usage-sync.py; do
    python3 -m py_compile "$SRC/deploy/master/scripts/$tool"
    python3 "$SRC/deploy/master/scripts/$tool" --self-test
done
log "Compiling SURENA Master and CLI without installing them"
(cd "$SRC/webpanel/backend" && go test -run '^(TestTelegramBackup|TestBackupImport|TestCanonicalTrafficQuotaBytes)' -count=1 . && CGO_ENABLED=0 go build -trimpath -o "$WORK/build/webpanel" .)
(cd "$SRC" && CGO_ENABLED=0 go build -trimpath -o "$WORK/build/cli" ./main.go)
bash -n "$SRC/scripts/ovp-panel"
log "Current then proposed webpanel SHA256:"
sha256sum "$BIN" "$WORK/build/webpanel"
if [[ "$MODE" == --preflight ]]; then
    log "PREFLIGHT PASSED: no live Master file modified"
    exit 0
fi

[[ "$(df -Pk /root | awk 'END{print $4}')" -gt 2097152 ]] ||
    die "Require >2 GiB free on root volume for safe rollback"
[[ -d /opt/irangate/database/clients && -s /opt/irangate/webpanel/nodes.json ]] ||
    die "Clients or nodes store missing"
BACK="/root/surena-code-rollback.$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACK/bin" "$BACK/sbin"
log "Backup and database export: $BACK"
for f in irangate-webpanel irangate ovp-panel irangate-routing \
         irangate-local-status-snapshot.py irangate-node-usage-sync.py \
         irangate-pg-usage-tick.py irangate-pg-enforcer.py irangate-pg-sync-clients.py; do
    [[ ! -f "/usr/local/bin/$f" ]] || cp -a "/usr/local/bin/$f" "$BACK/bin/"
done
[[ ! -f /usr/local/sbin/irangate-ikev2-stats-snapshot ]] ||
    cp -a /usr/local/sbin/irangate-ikev2-stats-snapshot "$BACK/sbin/"
cp -a "$FRONT" "$BACK/frontend"
cp -a /opt/irangate/webpanel/nodes.json "$BACK/nodes.json"
cp -a /opt/irangate/database/clients "$BACK/clients"
# DSN is loaded without printing it; shell argument still visible briefly in ps.
DB_DSN="$(python3 - <<'PY'
from pathlib import Path
for line in Path("/etc/irangate-db.env").read_text().splitlines():
    if line.startswith("IRANGATE_DB_DSN="):
        print(line.split("=", 1)[1].strip())
        break
PY
)"
[[ -n "$DB_DSN" ]] || die "No DB DSN"
pg_dump --dbname="$DB_DSN" -Fc -f "$BACK/postgresql.dump"
unset DB_DSN
[[ -s "$BACK/postgresql.dump" ]] || die "PostgreSQL backup failed"
ORIG_CLIENTS="$(find /opt/irangate/database/clients -maxdepth 1 -name '*.json' -type f | wc -l)"
ORIG_NODES="$(sha256sum /opt/irangate/webpanel/nodes.json | cut -d' ' -f1)"
log "Backups verified. Applying code files only; no user/usage writes."
CHANGED=1
systemctl stop irangate-webpanel
install -m 0755 "$WORK/build/webpanel" "$BIN"
install -m 0755 "$WORK/build/cli" "$CLI"
for f in irangate-local-status-snapshot.py irangate-node-usage-sync.py \
         irangate-pg-usage-tick.py irangate-pg-enforcer.py irangate-pg-sync-clients.py; do
    install -m 0755 "$SRC/deploy/master/scripts/$f" "/usr/local/bin/$f"
done
install -m 0755 "$SRC/scripts/ovp-panel" /usr/local/bin/ovp-panel
[[ ! -f "$SRC/scripts/irangate-routing" ]] ||
    install -m 0755 "$SRC/scripts/irangate-routing" /usr/local/bin/irangate-routing
[[ ! -f "$SRC/deploy/master/ikev2/irangate-ikev2-stats-snapshot.py" ]] ||
    install -m 0755 "$SRC/deploy/master/ikev2/irangate-ikev2-stats-snapshot.py" \
        /usr/local/sbin/irangate-ikev2-stats-snapshot
rm -rf -- "$FRONT"
cp -a "$SRC/webpanel/frontend" "$FRONT"
ln -sfn /usr/local/bin/ovp-panel /usr/local/bin/surena-panel
systemctl start irangate-webpanel
OK=0
for n in $(seq 1 45); do
    if health; then OK=1; break; fi
    sleep 1
done
[[ "$OK" -eq 1 ]] || die "New panel failed health; rollback required"
NOW_CLIENTS="$(find /opt/irangate/database/clients -maxdepth 1 -name '*.json' -type f | wc -l)"
NOW_NODES="$(sha256sum /opt/irangate/webpanel/nodes.json | cut -d' ' -f1)"
[[ "$NOW_CLIENTS" == "$ORIG_CLIENTS" && "$NOW_NODES" == "$ORIG_NODES" ]] ||
    die "Client count or nodes metadata unexpectedly changed"
mkdir -p "/opt/surena-releases/$REV"
cp -a "$SRC/deploy" "$SRC/scripts" "$SRC/webpanel/backend" "/opt/surena-releases/$REV/"
printf '%s\n' "$REV" > /opt/surena-releases/CURRENT
DONE=1
log "SUCCESS: SURENA Master code updated to $REV"
log "Rollback data retained: $BACK"
log "Existing quotas and usage were NOT reset."
log "Inactive traffic timers were intentionally NOT auto-enabled; reconcile accounting first."
