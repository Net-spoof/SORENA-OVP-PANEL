#!/usr/bin/env bash
# SURENA ARM64 migrated Master repair: no install, restore, quota reset or CA replacement.
set -Eeuo pipefail
umask 077
export PATH="/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"
MODE="${1:---audit}"
[[ "$MODE" == --audit || "$MODE" == --apply ]] || { echo "Usage: $0 --audit|--apply"; exit 2; }
[[ "$EUID" == 0 ]] || { echo "Run as root"; exit 1; }
REV="2e8c0e334237297ad6e2c96cfc9acf1881200c1b"
SRC="/opt/surena-releases/$REV"
HELPER="${SURENA_REPAIR_BASELINE:-$(dirname "$(readlink -f "$0")")/surena-migrated-local-baseline.py}"
LOG="/root/surena-repair-$(date +%Y%m%d-%H%M%S)"
mkdir -m 0700 -p "$LOG"
log() { printf '[SURENA REPAIR] %s\n' "$*"; }
die() { log "STOP: $*" >&2; exit 1; }
for c in python3 pg_dump pg_restore psql runuser systemctl flock sha256sum stat awk timeout curl; do
    command -v "$c" >/dev/null || die "Missing command: $c"
done
[[ "$(uname -m)" == aarch64 ]] || die "This repair is for migrated ARM64 Master"
[[ -s /etc/irangate-db.env && -s /usr/local/bin/irangate-webpanel ]] ||
    die "PostgreSQL env or Master binary missing"
[[ -s /opt/irangate/webpanel/nodes.json && -d /opt/irangate/database/clients ]] ||
    die "Restored clients/nodes missing: inspect Backup V2"
[[ -s /etc/openvpn/easy-rsa/pki/private/ca.key ]] ||
    die "Original OpenVPN CA missing; do not generate replacements"
[[ -r "$HELPER" && -s /usr/local/bin/irangate-pg-usage-tick.py ]] ||
    die "Baseline helper or installed accounting engine missing"
[[ -s /opt/surena-releases/CURRENT ]] && [[ "$(cat /opt/surena-releases/CURRENT)" == "$REV" ]] ||
    die "Installed ARM64 GitHub release mismatch"
[[ -d "$SRC/deploy/master/systemd" && -f "$SRC/scripts/install-backup-v2-extras-wrappers.sh" ]] ||
    die "Staged GitHub source missing"
PORT="$(sed -nE 's/^WEBPANEL_PORT="?([0-9]+)"?$/\1/p' /etc/irangate/webpanel.env | tail -1)"
[[ "$PORT" =~ ^[0-9]+$ ]] || die "Missing WEBPANEL_PORT"
systemctl is-active --quiet irangate-webpanel || die "Panel inactive"
curl --noproxy '*' -fsS --max-time 10 "http://127.0.0.1:$PORT/api/health" >/dev/null ||
    die "Panel local health endpoint failed"

for name in irangate-pg-usage-tick irangate-pg-enforcer irangate-pg-sync-clients irangate-node-usage-sync; do
    if systemctl is-active --quiet "$name.timer" ||
       systemctl is-active --quiet "$name.service"; then
        die "$name already active; simultaneous repairs are unsafe"
    fi
done
exec 9>/run/surena-accounting-repair.lock
flock -n 9 || die "Another accounting repair in progress"
cd /tmp
db_state() {
    runuser -u postgres -- psql -XAt -v ON_ERROR_STOP=1 -d irangate \
        -c "SELECT (SELECT COUNT(*) FROM clients),
                   (SELECT COUNT(*) FROM traffic_totals),
                   (SELECT COUNT(*) FROM traffic_sources),
                   (SELECT COALESCE(SUM(total_bytes),0) FROM traffic_totals);" | tail -1
}
BEFORE="$(db_state)"
IFS='|' read -r N_C N_T N_S N_B <<<"$BEFORE"
[[ "$N_C" -gt 0 && "$N_C" == "$N_T" && "$N_S" -gt 0 ]] ||
    die "Historical PostgreSQL data incomplete"
log "BEFORE=$BEFORE MODE=$MODE"
check() {
    local label="$1" status=0
    shift
    timeout 120 "$@" >"$LOG/$label.log" 2>&1 || status=$?
    if [[ "$status" != 0 ]]; then
        tail -n 16 "$LOG/$label.log" >&2
        die "$label failed (exit $status), see $LOG"
    fi
    log "$label=PASS"
}
num() {
    awk -F' = ' -v k="$1" '$1==k {print $2}' "$2" | tail -1
}
check SYNC_DRY python3 /usr/local/bin/irangate-pg-sync-clients.py
grep -Fxq 'database_modified = NO' "$LOG/SYNC_DRY.log" || die "Sync was not read-only"
check USAGE_DRY python3 /usr/local/bin/irangate-pg-usage-tick.py
grep -Fxq 'mode = DRY-RUN' "$LOG/USAGE_DRY.log" || die "Usage dry-run not confirmed"
check ENFORCER_DRY python3 /usr/local/bin/irangate-pg-enforcer.py --guarded-dry-run
grep -Fxq 'mode = GUARDED-DRY-RUN' "$LOG/ENFORCER_DRY.log" || die "Enforcer dry-run not confirmed"
check BASELINE_AUDIT python3 "$HELPER" --audit

for k in api_clients pg_clients pg_only_preserved new_clients quota_changes expiry_changes connection_changes; do
    log "SYNC_$k=$(num "$k" "$LOG/SYNC_DRY.log")"
done
for k in first_seen first_sample_added counter_reset added_total_bytes; do
    log "USAGE_$k=$(num "$k" "$LOG/USAGE_DRY.log")"
done
for k in guarded_eligible_blocks guarded_eligible_unblocks guarded_skipped; do
    log "ENFORCER_$k=$(num "$k" "$LOG/ENFORCER_DRY.log")"
done
log "LOCAL_BASELINES_NEEDED=$(num NEW_LOCAL_STREAM_BASELINES "$LOG/BASELINE_AUDIT.log")"
if [[ "$MODE" == --audit ]]; then
    log "AUDIT_PASS: no database changes or timer activation"
    log "LOG_DIR=$LOG"
    exit 0
fi

[[ "$(df -Pk /root | awk 'END{print $4}')" -gt 2097152 ]] ||
    die "Less than 2 GiB free; cannot create safety backups"
BACK="$LOG/safety"
mkdir -m 0700 "$BACK"
runuser -u postgres -- pg_dump -Fc -d irangate >"$BACK/irangate.dump" ||
    die "Could not backup PostgreSQL"
[[ -s "$BACK/irangate.dump" ]] || die "Empty PostgreSQL dump"
pg_restore -l "$BACK/irangate.dump" >"$BACK/pg_manifest.txt" ||
    die "PostgreSQL backup invalid"
cp -a /etc/irangate-db.env "$BACK/"
cp -a /opt/irangate/webpanel/nodes.json "$BACK/"
cp -a /etc/openvpn/easy-rsa/pki/ca.crt "$BACK/"
for p in /opt/irangate/traffic/*marker* /opt/irangate/traffic/*stats.json /opt/irangate/traffic/node_usage_state.json; do
    [[ ! -f "$p" ]] || cp -a "$p" "$BACK/$(basename "$p")"
done
CA_SHA="$(sha256sum /etc/openvpn/easy-rsa/pki/private/ca.key | awk '{print $1}')"
log "SAFETY_BACKUP=PASS $BACK"

check REFRESH_LOCAL python3 /usr/local/bin/irangate-local-status-snapshot.py
check BASELINE_PRECHECK python3 "$HELPER" --audit
log "LIVE_LOCAL_BASELINES=$(num NEW_LOCAL_STREAM_BASELINES "$LOG/BASELINE_PRECHECK.log")"
check BASELINE_APPLY python3 "$HELPER" --apply
grep -Fxq 'BASELINE_COMMIT = PASS' "$LOG/BASELINE_APPLY.log" ||
    die "Baseline transaction not confirmed"
check USAGE_POST_BASELINE python3 /usr/local/bin/irangate-pg-usage-tick.py
FIRST="$(num first_sample_added "$LOG/USAGE_POST_BASELINE.log")"
RESET="$(num counter_reset "$LOG/USAGE_POST_BASELINE.log")"
DELTA="$(num added_total_bytes "$LOG/USAGE_POST_BASELINE.log")"
[[ "$FIRST" =~ ^[0-9]+$ && "$RESET" =~ ^[0-9]+$ && "$DELTA" =~ ^[0-9]+$ ]] ||
    die "Incomplete post-baseline metrics"
[[ "$FIRST" == 0 && "$RESET" == 0 && "$DELTA" -le 67108864 ]] ||
    die "Possible replay remains: first=$FIRST reset=$RESET delta=$DELTA. Timer kept off"
AFTER="$(db_state)"
IFS='|' read -r A_C A_T A_S A_B <<<"$AFTER"
[[ "$A_C" == "$N_C" && "$A_T" == "$N_T" && "$A_S" -ge "$N_S" && "$A_B" == "$N_B" ]] ||
    die "Historical usage/clients changed unexpectedly. Timers remain off"
[[ "$(sha256sum /etc/openvpn/easy-rsa/pki/private/ca.key | awk '{print $1}')" == "$CA_SHA" ]] ||
    die "OpenVPN CA checksum changed"
log "HISTORY_AND_CA_INTACT=PASS"
log "POST_BASELINE_POTENTIAL_DELTA_BYTES=$DELTA"

bash "$SRC/scripts/install-backup-v2-extras-wrappers.sh" >"$LOG/BACKUP_RUNTIME.log"
ln -sfn /usr/local/bin/ovp-panel /usr/local/bin/surena-panel
for p in "$SRC"/deploy/master/systemd/*.timer "$SRC"/deploy/master/systemd/*.service; do
    t="/etc/systemd/system/$(basename "$p")"
    if [[ ! -e "$t" ]]; then
        install -m 0644 "$p" "$t"
        log "RESTORED_UNIT=$(basename "$p")"
    fi
done
systemctl daemon-reload

systemctl enable --now irangate-pg-usage-tick.timer ||
    die "Usage timer activation failed"
systemctl is-active --quiet irangate-pg-usage-tick.timer ||
    die "Usage timer not active"
log "USAGE_TIMER=ACTIVE"

NEW="$(num new_clients "$LOG/SYNC_DRY.log")"
QUOTA="$(num quota_changes "$LOG/SYNC_DRY.log")"
EXPIRY="$(num expiry_changes "$LOG/SYNC_DRY.log")"
CONN="$(num connection_changes "$LOG/SYNC_DRY.log")"
if [[ "$NEW" == 0 && "$QUOTA" == 0 && "$EXPIRY" == 0 && "$CONN" == 0 ]]; then
    systemctl enable --now irangate-pg-sync-clients.timer
    log "SYNC_TIMER=ACTIVE"
else
    log "SYNC_TIMER=HELD (proposed new=$NEW quota=$QUOTA expiry=$EXPIRY connection=$CONN)"
fi
log "ENFORCER_TIMER=HELD (some previously eligible blocks/skips need review)"
log "NODE_SYNC_TIMER=HELD (stale remote node snapshot must be verified)"
log "IKEV2_TIMER=HELD (verify restored strongSwan runtime first)"
log "REPAIR_SAFE_STAGE=COMPLETE"
log "DB_AFTER_BASELINE=$AFTER"
log "LOG_DIR=$LOG"
log "NO_QUOTA_OR_HISTORICAL_USAGE_RESET=YES"
