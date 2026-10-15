#!/usr/bin/env bash
# In-place node accounting cutover: preserve historical quotas and totals.
set -Eeuo pipefail
umask 077
REV="$1"
AUTH="$SURENA_GITHUB_AUTH_CONFIG"
[[ "$EUID" -eq 0 && "$REV" =~ ^[a-f0-9]{40}$ && -f "$AUTH" ]] || exit 2
SRC=/usr/local/bin/irangate-node-usage-sync.py
STATE=/opt/irangate/traffic/node_usage_state.json
OUT=/opt/irangate/traffic/node_current_stats.json
MARKER=/opt/irangate/traffic/node_usage_session_v2.marker
PGTIMER=irangate-pg-usage-tick.timer
NODETIMER=irangate-node-usage-sync.timer
[[ -s "$SRC" && -s "$STATE" && -s "$OUT" && ! -e "$MARKER" ]] ||
  { echo "STOP: missing files or node migration already completed"; exit 2; }
systemctl is-active --quiet irangate-webpanel || exit 2
systemctl is-active --quiet "$PGTIMER" || { echo "STOP: PG timer must be active"; exit 2; }
systemctl is-active --quiet irangate-pg-enforcer.timer &&
  { echo "STOP: enforcer must remain inactive during migration"; exit 2; }
[[ -s /etc/irangate-db.env ]] || { echo "STOP: missing database configuration"; exit 2; }
if systemctl is-active --quiet "$NODETIMER" ||
   systemctl is-active --quiet irangate-node-usage-sync.service; then
  echo "STOP: node sync already running"; exit 2
fi
exec 9>/run/surena-node-cutover.lock
flock -n 9 || exit 2
WORK="$(mktemp -d /root/surena-node-stage.XXXXXXXX)"
BACK="/root/surena-node-rollback.$(date -u +%Y%m%d-%H%M%S)"
mkdir -m 700 "$BACK"
PAUSED=0
MUTATED=0
SEEDED=0
cleanup() {
  local rc=$?
  trap - EXIT
  if [[ "$MUTATED" -eq 1 && "$SEEDED" -eq 0 ]]; then
    echo "RESTORING PREVIOUS NODE SCRIPT AND STATE"
    cp -a "$BACK/original.py" "$SRC"
    cp -a "$BACK/old_state.json" "$STATE"
    cp -a "$BACK/old_output.json" "$OUT"
    rm -f "$MARKER"
  fi
  if [[ "$PAUSED" -eq 1 ]]; then
    systemctl start "$PGTIMER" ||
      echo "CRITICAL: restart PG accounting timer manually"
  fi
  rm -rf "$WORK"
  echo "BACKUP=$BACK"
  exit "$rc"
}
trap cleanup EXIT
echo "===== FETCH AND SELF TEST ====="
curl -fLsS --config "$AUTH" -H "Accept: application/vnd.github.raw+json" \
  "https://api.github.com/repos/Net-spoof/SORENA-OVP-PANEL/contents/deploy/master/scripts/irangate-node-usage-sync.py?ref=$REV" \
  -o "$WORK/node.py"
python3 -m py_compile "$WORK/node.py"
python3 "$WORK/node.py" --self-test
echo "===== BACKUP SCRIPT, NODE STATE, POSTGRES ====="
cp -a "$SRC" "$BACK/original.py"
cp -a "$STATE" "$BACK/old_state.json"
cp -a "$OUT" "$BACK/old_output.json"
python3 - "$BACK/postgresql.dump" <<'PY'
import os,sys,subprocess
from pathlib import Path
from psycopg2.extensions import parse_dsn,make_dsn
env={}
for line in Path("/etc/irangate-db.env").read_text().splitlines():
    if "=" in line and not line.lstrip().startswith("#"):
        k,v=line.split("=",1); env[k.strip()]=v.strip()
d=parse_dsn(env["IRANGATE_DB_DSN"])
e=os.environ.copy()
p=d.pop("password",None)
if p: e["PGPASSWORD"]=p
subprocess.run(["pg_dump","-Fc","--dbname",make_dsn(**d),
                "-f",sys.argv[1]],env=e,check=True,timeout=300)
assert Path(sys.argv[1]).stat().st_size > 0
print("POSTGRES_BACKUP=PASS")
PY
echo "===== PAUSE LOCAL ACCOUNTING TEMPORARILY ====="
systemctl stop "$PGTIMER"
PAUSED=1
for i in $(seq 1 30); do
  systemctl is-active --quiet irangate-pg-usage-tick.service || break
  sleep 1
done
if systemctl is-active --quiet irangate-pg-usage-tick.service; then
  echo "STOP: existing PG tick still running"; exit 1
fi
echo "===== BASELINE NODE SESSIONS ====="
install -m 0755 "$WORK/node.py" "$SRC"
MUTATED=1
python3 "$SRC" --baseline
[[ -s "$MARKER" ]] || { echo "STOP: missing node baseline marker"; exit 1; }
python3 - "$BACK/old_state.json" "$STATE" "$OUT" <<'PY'
import json,sys
from pathlib import Path
old,new,rows=(json.loads(Path(x).read_text()) for x in sys.argv[1:])
assert isinstance(old,dict) and isinstance(new,dict)
old_totals=old.get("totals",{})
new_totals=new.get("totals",{})
assert isinstance(old_totals,dict) and isinstance(new_totals,dict)
for key, value in old_totals.items():
    assert key in new_totals, "HISTORIC NODE ROW DELETED: "+key
    assert new_totals[key] == value, "HISTORIC NODE TOTAL MODIFIED: "+key
new_keys=set(new_totals)-set(old_totals)
for key in new_keys:
    value=new_totals[key]
    assert isinstance(value,dict), "INVALID NEW NODE ROW: "+key
    assert int(value.get("rx",-1)) == 0 and int(value.get("tx",-1)) == 0, (
        "UNEXPECTED NEW NODE TRAFFIC: "+key
    )
print("PRESERVED_HISTORIC_NODE_ROWS=",len(old_totals))
print("NEW_ZERO_ONLY_NODE_ROWS=",len(new_keys))
assert isinstance(rows,list) and len(rows)>0, "No baseline output"
for r in rows:
    assert r.get("node_id") and r.get("client_name")
    assert min(int(r.get("current_received",0)),
               int(r.get("current_sent",0)),
               int(r.get("current_total",0)))>=0
print("PRESERVED_NODE_TOTALS=PASS")
print("NODE_CLIENT_ROWS=",len(rows))
PY
echo "===== BASELINE PG NODE LEDGERS WITHOUT CHARGING USERS ====="
python3 - "$OUT" <<'PY'
import json,sys
from pathlib import Path
import psycopg2
env={}
for line in Path("/etc/irangate-db.env").read_text().splitlines():
    if "=" in line and not line.lstrip().startswith("#"):
        k,v=line.split("=",1); env[k.strip()]=v.strip()
rows=json.loads(Path(sys.argv[1]).read_text())
assert rows, "empty snapshot"
db=psycopg2.connect(env["IRANGATE_DB_DSN"],connect_timeout=10)
db.autocommit=False
try:
    with db.cursor() as cur:
        cur.execute("SELECT COALESCE(sum(total_bytes),0) FROM traffic_totals")
        aggregate_before=int(cur.fetchone()[0])
        seen=set()
        for r in rows:
            src=str(r.get("node_id") or "").strip()
            name=str(r.get("client_name") or "").strip()
            if not src or not name or (src,name) in seen:
                raise RuntimeError("invalid duplicate/missing node identity")
            seen.add((src,name))
            cur.execute("SELECT EXISTS (SELECT 1 FROM clients WHERE client_name=%s)",(name,))
            if not cur.fetchone()[0]:
                raise RuntimeError("missing master DB client: "+name)
            rx=int(r.get("current_received",0))
            tx=int(r.get("current_sent",0))
            total=int(r.get("current_total",0))
            if min(rx,tx,total)<0 or total!=rx+tx:
                raise RuntimeError("invalid node cumulative total for "+name)
            fp="connected_since="+str(r.get("connected_since") or "")+"|real_address="+str(r.get("real_address") or "")+"|virtual_ip="+str(r.get("virtual_ip") or "")
            cur.execute("""
                INSERT INTO traffic_sources
                  (source_id,client_name,session_fingerprint,
                   rx_bytes,tx_bytes,total_bytes,connected,
                   real_address,virtual_ip,updated_at)
                VALUES (%s,%s,%s,%s,%s,%s,true,%s,%s,now())
                ON CONFLICT (source_id,client_name) DO UPDATE SET
                  session_fingerprint=EXCLUDED.session_fingerprint,
                  rx_bytes=EXCLUDED.rx_bytes,
                  tx_bytes=EXCLUDED.tx_bytes,
                  total_bytes=EXCLUDED.total_bytes,
                  connected=true,
                  real_address=EXCLUDED.real_address,
                  virtual_ip=EXCLUDED.virtual_ip,
                  updated_at=now()
            """,(src,name,fp,rx,tx,total,r.get("real_address"),r.get("virtual_ip")))
        cur.execute("SELECT COALESCE(sum(total_bytes),0) FROM traffic_totals")
        aggregate_after=int(cur.fetchone()[0])
        if aggregate_after != aggregate_before:
            raise RuntimeError("Traffic totals changed during ledger seeding; rolling back")
    db.commit()
    print("PG_NODE_LEDGERS_BASELINED=",len(rows))
    print("PG_TRAFFIC_TOTALS_MODIFIED=NO")
finally:
    db.close()
PY
SEEDED=1
echo "===== ACTIVATE NODE TIMER AND RESUME PG TIMER ====="
systemctl start "$NODETIMER"
systemctl start "$PGTIMER"
PAUSED=0
systemctl is-active --quiet "$NODETIMER"
systemctl is-active --quiet "$PGTIMER"
systemctl is-active --quiet irangate-webpanel
echo "SURENA_NODE_CUTOVER=SUCCESS"
echo "NODE_TIMER=ACTIVE"
echo "PG_TIMER=ACTIVE"
echo "ENFORCER=UNCHANGED"
echo "BACKUP=$BACK"
