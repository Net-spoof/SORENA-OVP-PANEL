#!/usr/bin/env bash
# Code-only repair of an existing, restored SURENA Master.
# Offline release 4b928bcd8c51 is pinned by OS-specific SHA256.
set -Eeuo pipefail
umask 077
MODE="$(printf '%s' "$1")"
[[ "$MODE" == "--audit" || "$MODE" == "--apply" ]] || { echo "Usage: --audit|--apply"; exit 2; }
[[ "$EUID" == 0 ]] || { echo "Run as root"; exit 1; }
COMMIT="4b928bcd8c510aaf3d51eb05cf21d714afc14402"
RELEASE="offline-4b928bcd8c51"
FRONT="/opt/irangate/webpanel/frontend"
SOURCE_POINTER="/etc/irangate/source_dir"
AUTH_FILE="${SURENA_GITHUB_AUTH_CONFIG:-}"
BACK=""
TMP=""
MODIFIED=0
SUCCESS=0
msg() { printf '[SURENA POST-RESTORE] %s\n' "$*"; }
stop() { msg "STOP: $*"; exit 1; }
for cmd in python3 pg_dump curl tar rsync sha256sum systemctl flock; do
    command -v "$cmd" >/dev/null || stop "Missing command: $cmd"
done
. /etc/os-release
[[ "$ID" == ubuntu && ( "$VERSION_ID" == "22.04" || "$VERSION_ID" == "24.04" ) ]] ||
    stop "Only Ubuntu 22.04/24.04 supported"
[[ "$(dpkg --print-architecture)" == amd64 ]] || stop "Only amd64 supported"
[[ -s /usr/local/bin/irangate-webpanel && -f /etc/irangate-db.env && -s /etc/irangate/webpanel.env ]] ||
    stop "Existing Master code/DB configuration not found. Do not rerun fresh installer."
[[ -d "$FRONT" && -s /opt/irangate/webpanel/nodes.json && -s /opt/irangate/webpanel/admin.json ]] ||
    stop "Panel configuration missing; restore from original Backup V2 first."
[[ -d /opt/irangate/database/clients ]] || stop "Client database missing: restore Backup V2."
[[ -s /etc/openvpn/easy-rsa/pki/private/ca.key && -s /etc/openvpn/easy-rsa/pki/index.txt ]] ||
    stop "OpenVPN PKI missing: restore original CA from Backup V2. Never generate a new CA."
CLIENTS="$(find /opt/irangate/database/clients -maxdepth 1 -type f -name '*.json' | wc -l)"
[[ "$CLIENTS" -gt 0 ]] || stop "No restored clients found. Confirm Backup V2 restoration before upgrade."
PORT="$(sed -nE 's/^WEBPANEL_PORT="?([0-9]+)"?$/\1/p' /etc/irangate/webpanel.env | tail -1)"
[[ "$PORT" =~ ^[0-9]+$ ]] || stop "Cannot read WEBPANEL_PORT."
healthy() {
    systemctl is-active --quiet irangate-webpanel &&
    curl -fsS --max-time 10 --noproxy '*' "http://127.0.0.1:$PORT/api/health" >/dev/null
}
healthy || stop "Existing panel health failed; inspect restored IP, port and journal before updating."
msg "PANEL_HEALTH=PASS OS=$VERSION_ID CLIENTS=$CLIENTS"
for item in /opt/irangate/webpanel/delegated_admins.json /opt/irangate/traffic/global_usage_totals.json /etc/swanctl/conf.d/irangate-ikev2-users.conf; do
    if [[ -s "$item" ]]; then msg "PRESENT: $item"; else msg "MISSING_OR_EMPTY: $item (cannot recreate private data from GitHub)"; fi
done
for t in irangate-pg-usage-tick irangate-pg-enforcer irangate-pg-sync-clients irangate-node-usage-sync irangate-ikev2-stats; do
    if systemctl cat "$t.timer" >/dev/null 2>&1; then
        msg "TIMER $t: $(systemctl is-active "$t.timer" 2>/dev/null || true)"
    else
        msg "MISSING TIMER UNIT: $t (code can be restored, do not auto-enable accounting)"
    fi
done
if [[ "$MODE" == "--audit" ]]; then
    msg "READ_ONLY_AUDIT=PASS (zero changes)"
    exit 0
fi
[[ "$(df -Pk /root | awk 'END{print $4}')" -ge 3145728 ]] || stop "Need at least 3 GiB free disk."
exec 9>/run/surena-code-upgrade.lock
flock -n 9 || stop "Another code upgrade is running"
TMP="$(mktemp -d /root/surena-verify-release.XXXXXXXX)"
finish() {
    rc=$?
    trap - EXIT
    if [[ "$MODIFIED" == 1 && "$SUCCESS" == 0 ]]; then
        msg "Rolling back code from $BACK"
        systemctl stop irangate-webpanel >/dev/null 2>&1 || true
        if [[ -d "$BACK/frontend" ]]; then
            rsync -a --delete "$BACK/frontend/" "$FRONT/"
        fi
        for p in "$BACK"/bin/*; do
            [[ -f "$p" ]] && cp -a "$p" "/usr/local/bin/$(basename "$p")"
        done
        for p in "$BACK"/sbin/*; do
            [[ -f "$p" ]] && cp -a "$p" "/usr/local/sbin/$(basename "$p")"
        done
        if [[ -f "$BACK/source_dir" ]]; then cp -a "$BACK/source_dir" "$SOURCE_POINTER"; fi
        systemctl start irangate-webpanel || true
        if healthy; then msg "ROLLBACK_PANEL=PASS"; else msg "ROLLBACK_NEEDS_ATTENTION=$BACK"; fi
    fi
    [[ -z "$TMP" ]] || rm -rf "$TMP"
    [[ -z "$BACK" ]] || msg "SAFETY_BACKUP=$BACK"
    exit "$rc"
}
trap finish EXIT
FILE="/root/SURENA-offline-ubuntu${VERSION_ID//./}-amd64.run"
if [[ "$VERSION_ID" == "22.04" ]]; then
    DIGEST="e7b18ba73499b7c8441fa97a8cc61b3a4ab7f872f3f6377d8088c14c4024f472"
    ASSET="660364875"
else
    DIGEST="a9c47fc91b4ebd92c54d741bb731a2dfd5237cad57efe023bb0e38189e7381b5"
    ASSET="660374429"
fi
if [[ ! -s "$FILE" ]]; then
    [[ -s "$AUTH_FILE" && ! -L "$AUTH_FILE" ]] || stop "Private GitHub release missing. Supply SURENA_GITHUB_AUTH_CONFIG."
    msg "Downloading signed GitHub release asset"
    curl -fLsS --retry 2 --max-time 1200 --config "$AUTH_FILE" \
        -H "Accept: application/octet-stream" \
        "https://api.github.com/repos/Net-spoof/SORENA-OVP-PANEL/releases/assets/$ASSET" \
        -o "$TMP/release.run"
    FILE="$TMP/release.run"
fi
[[ "$(sha256sum "$FILE" | awk '{print $1}')" == "$DIGEST" ]] ||
    stop "Release SHA256 mismatch: refused."
msg "OFFLINE_RELEASE_SHA256=PASS"
OFFSET="$(awk '$0=="__IRANGATE_BUNDLE_START__" {print NR+1;exit}' "$FILE")"
[[ "$OFFSET" =~ ^[0-9]+$ ]] || stop "Invalid bundle format"
mkdir "$TMP/source"
tail -n +"$OFFSET" "$FILE" | tar -xzf - -C "$TMP/source"
SOURCE="$TMP/source"
[[ "$(cat "$SOURCE/offline-assets/source-commit")" == "$COMMIT" ]] ||
    stop "Release/commit mismatch"
(cd "$SOURCE" && sha256sum -c offline-assets/SHA256SUMS >/dev/null) ||
    stop "Source files did not pass bundled integrity validation."
python3 -m py_compile "$SOURCE"/deploy/master/scripts/*.py
python3 "$SOURCE/deploy/master/scripts/irangate-pg-enforcer.py" --self-test |
    grep -Fxq "guarded_enforcer_self_test = PASS" || stop "Enforcer tests failed"
python3 "$SOURCE/deploy/master/scripts/irangate-pg-sync-clients.py" --self-test |
    grep -Fxq "pg_sync_guarded_self_test = PASS" || stop "Sync tests failed"
msg "SOURCE_TESTS=PASS"
BACK="/root/surena-pre-github-sync.$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACK/bin" "$BACK/sbin"
cp -a "$FRONT" "$BACK/frontend"
[[ ! -f "$SOURCE_POINTER" ]] || cp -a "$SOURCE_POINTER" "$BACK/source_dir"
for n in irangate-webpanel irangate ovp-panel irangate-routing irangate-pg-enforcer.py irangate-pg-sync-clients.py irangate-pg-usage-tick.py irangate-node-usage-sync.py irangate-local-status-snapshot.py irangate-backup-v2-export.py irangate-backup-v2-import.py irangate-backup-v2-export irangate-backup-v2-import; do
    [[ ! -f "/usr/local/bin/$n" ]] || cp -a "/usr/local/bin/$n" "$BACK/bin/"
done
[[ ! -f /usr/local/sbin/irangate-ikev2-stats-snapshot ]] ||
    cp -a /usr/local/sbin/irangate-ikev2-stats-snapshot "$BACK/sbin/"
python3 - "$BACK/postgresql.dump" <<'PY'
import os,subprocess,sys
from pathlib import Path
from psycopg2.extensions import parse_dsn,make_dsn
dsn=None
for line in Path('/etc/irangate-db.env').read_text().splitlines():
    if line.startswith('IRANGATE_DB_DSN='):
        dsn=parse_dsn(line.split('=',1)[1].strip().strip('"').strip("'"))
        break
if dsn is None: raise RuntimeError('IRANGATE_DB_DSN missing')
env=os.environ.copy()
pwd=dsn.pop('password',None)
if pwd:env['PGPASSWORD']=pwd
subprocess.run(['pg_dump','-Fc','--dbname',make_dsn(**dsn),'-f',sys.argv[1]],env=env,check=True,timeout=360)
assert Path(sys.argv[1]).stat().st_size>0
print('POSTGRES_BACKUP=PASS')
PY
NODES_SHA="$(sha256sum /opt/irangate/webpanel/nodes.json | awk '{print $1}')"
REV_DIR="/opt/surena-releases/$COMMIT"
mkdir -p "$REV_DIR"
rsync -a --exclude='/offline-assets' "$SOURCE/" "$REV_DIR/"
msg "Applying release code only (no database, CA, user JSON or nodes writes)"
MODIFIED=1
systemctl stop irangate-webpanel
install -m 0755 "$SOURCE/offline-assets/irangate-webpanel" /usr/local/bin/irangate-webpanel
install -m 0755 "$SOURCE/offline-assets/irangate-cli" /usr/local/bin/irangate
for p in "$SOURCE"/deploy/master/scripts/*.py; do
    install -m 0755 "$p" "/usr/local/bin/$(basename "$p")"
done
install -m 0755 "$SOURCE/scripts/ovp-panel" /usr/local/bin/ovp-panel
install -m 0755 "$SOURCE/scripts/backup-v2/irangate-backup-v2-export-wrapper" /usr/local/bin/irangate-backup-v2-export
install -m 0755 "$SOURCE/scripts/backup-v2/irangate-backup-v2-import-wrapper" /usr/local/bin/irangate-backup-v2-import
[[ ! -s "$SOURCE/scripts/irangate-routing" ]] ||
    install -m 0755 "$SOURCE/scripts/irangate-routing" /usr/local/bin/irangate-routing
[[ ! -s "$SOURCE/deploy/master/ikev2/irangate-ikev2-stats-snapshot.py" ]] ||
    install -m 0755 "$SOURCE/deploy/master/ikev2/irangate-ikev2-stats-snapshot.py" /usr/local/sbin/irangate-ikev2-stats-snapshot
rsync -a "$SOURCE/webpanel/frontend/" "$FRONT/"
printf '%s\n' "$REV_DIR" > "$SOURCE_POINTER"
chmod 600 "$SOURCE_POINTER"
ln -sfn /usr/local/bin/ovp-panel /usr/local/bin/surena-panel
for p in "$SOURCE"/deploy/master/systemd/*.timer "$SOURCE"/deploy/master/systemd/*.service; do
    target="/etc/systemd/system/$(basename "$p")"
    if [[ ! -e "$target" ]]; then
        install -m 0644 "$p" "$target"
        msg "ADDED_MISSING_UNIT=$(basename "$p") (NOT automatically enabled)"
    fi
done
systemctl daemon-reload
systemctl start irangate-webpanel
GOOD=0
for i in $(seq 1 45); do
    if healthy; then GOOD=1; break; fi
    sleep 1
done
[[ "$GOOD" == 1 ]] || stop "New panel failed health."
[[ "$(find /opt/irangate/database/clients -maxdepth 1 -type f -name '*.json' | wc -l)" == "$CLIENTS" ]] ||
    stop "Client file count unexpectedly changed."
[[ "$(sha256sum /opt/irangate/webpanel/nodes.json | awk '{print $1}')" == "$NODES_SHA" ]] ||
    stop "Node registry changed unexpectedly."
printf '%s\n' "$COMMIT" > /opt/surena-releases/CURRENT
SUCCESS=1
msg "UPGRADE_SUCCESS=YES"
msg "GITHUB_COMMIT=$COMMIT"
msg "QUOTAS_OR_USAGE_RESET=NO"
msg "PKI_CHANGED=NO"
msg "VPN_SERVICES_NOT_RESTARTED=YES"
msg "Now verify migrated IP, node routes, and timer status. Missing private data needs original Backup V2."
