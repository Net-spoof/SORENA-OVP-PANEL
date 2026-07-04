#!/usr/bin/env python3
import json
import argparse
from pathlib import Path
from datetime import datetime
import psycopg2

ENV = Path("/etc/irangate-db.env")
LOCAL = Path("/opt/irangate/traffic/current_stats.json")
NODE = Path("/opt/irangate/traffic/node_current_stats.json")
GLOBAL_MIRROR = Path("/opt/irangate/traffic/global_usage_totals.json")

def load_env():
    d = {}
    for line in ENV.read_text().splitlines():
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            d[k.strip()] = v.strip()
    return d

def load_json(path):
    if not path.exists():
        return []
    try:
        data = json.loads(path.read_text())
        return data if isinstance(data, list) else []
    except Exception:
        return []

def to_int(v):
    try:
        return int(float(v or 0))
    except Exception:
        return 0

def pick(x, *keys):
    for k in keys:
        if k in x and x.get(k) not in (None, ""):
            return x.get(k)
    return None

def name_of(x):
    return pick(x, "client_name", "name", "common_name", "username")

def rx_of(x):
    return to_int(pick(x, "current_received", "bytes_received", "rx", "received"))

def tx_of(x):
    return to_int(pick(x, "current_sent", "bytes_sent", "tx", "sent"))

def total_of(x):
    total = to_int(pick(x, "current_total", "total_bytes", "total", "used_bytes"))
    if total <= 0:
        total = rx_of(x) + tx_of(x)
    return total

def bool_of(v, default=True):
    if v is None:
        return default
    if isinstance(v, bool):
        return v
    return str(v).strip().lower() in ("1", "true", "yes", "y", "connected", "active")

def connected_of(x):
    if "connected" in x:
        return bool_of(x.get("connected"), False)
    if "is_connected" in x:
        return bool_of(x.get("is_connected"), False)
    if "active" in x:
        return bool_of(x.get("active"), False)
    return True

def parse_time(v):
    if not v or str(v).startswith("0001-01-01"):
        return None
    s = str(v).replace("Z", "+00:00")
    try:
        return datetime.fromisoformat(s)
    except Exception:
        return None

def session_of(x):
    parts = []
    for k in ("connected_since", "real_address", "virtual_ip", "virtual_address"):
        v = x.get(k)
        if v:
            parts.append(f"{k}={v}")
    if not parts:
        parts.append("unknown_session")
    return "|".join(parts)

def gb(n):
    return f"{int(n) / 1024 / 1024 / 1024:.3f} GB"

ap = argparse.ArgumentParser()
ap.add_argument("--apply", action="store_true")
ap.add_argument("--verbose", action="store_true")
args = ap.parse_args()

items = []

for x in load_json(LOCAL):
    x["_source_id"] = "local"
    items.append(x)

for x in load_json(NODE):
    x["_source_id"] = str(
        pick(x, "source_id", "node_id", "node_name", "node") or "node"
    )
    items.append(x)

env = load_env()
conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

processed = 0
first_seen = 0
session_reset = 0
counter_reset = 0
delta_count = 0
added_total = 0
active_count = 0

with conn.cursor() as cur:
    cur.execute("DELETE FROM active_sessions WHERE last_seen < now() - interval '90 seconds'")

    # Full snapshot sync: remove old active sessions for sources seen in this tick.
    # This prevents reconnect/port-change stale sessions from creating false connection-limit violations.
    snapshot_sources = sorted(set(["local"]) | {str(x.get("_source_id")) for x in items if x.get("_source_id")})
    for snapshot_source in snapshot_sources:
        cur.execute("DELETE FROM active_sessions WHERE source_id = %s", (snapshot_source,))

    for x in items:
        name = name_of(x)
        if not name:
            continue

        source = str(x["_source_id"])
        rx = rx_of(x)
        tx = tx_of(x)
        total = total_of(x)
        connected = connected_of(x)
        session = session_of(x)
        real_address = pick(x, "real_address", "remote_address")
        virtual_ip = pick(x, "virtual_ip", "virtual_address")
        last_seen = parse_time(pick(x, "last_seen", "updated_at"))
        connected_since = parse_time(pick(x, "connected_since", "last_connection"))

        processed += 1
        if connected:
            active_count += 1

        cur.execute("""
            INSERT INTO clients (client_name, display_name, updated_at)
            VALUES (%s,%s,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name, name))

        cur.execute("""
            INSERT INTO traffic_totals (client_name, rx_bytes, tx_bytes, total_bytes, updated_at)
            VALUES (%s,0,0,0,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name,))

        cur.execute("""
            UPDATE clients
            SET last_seen = COALESCE(%s, last_seen), updated_at = now()
            WHERE client_name = %s
        """, (last_seen, name))

        cur.execute("""
            SELECT session_fingerprint, rx_bytes, tx_bytes, total_bytes
            FROM traffic_sources
            WHERE source_id = %s AND client_name = %s
            FOR UPDATE
        """, (source, name))

        old = cur.fetchone()

        drx = dtx = dtotal = 0
        action = "baseline"
        update_source = True

        def add_usage_delta(action_name, add_rx, add_tx, add_total):
            global delta_count, added_total
            if add_total <= 0:
                return action_name + "_NO_BYTES"

            delta_count += 1
            added_total += add_total

            cur.execute("""
                UPDATE traffic_totals
                SET rx_bytes = rx_bytes + %s,
                    tx_bytes = tx_bytes + %s,
                    total_bytes = total_bytes + %s,
                    updated_at = now()
                WHERE client_name = %s
            """, (max(0, add_rx), max(0, add_tx), max(0, add_total), name))

            return action_name

        if old is None:
            first_seen += 1
            action = "FIRST_BASELINE_ONLY"

        else:
            old_session, old_rx, old_tx, old_total = old

            if old_session != session:
                session_reset += 1
                action = "SESSION_CHANGED_BASELINE_ONLY"

            elif total < old_total:
                counter_reset += 1
                action = "COUNTER_RESET_NEW_BASELINE"

            else:
                drx = max(0, rx - old_rx)
                dtx = max(0, tx - old_tx)
                dtotal = total - old_total

                if dtotal > 0:
                    action = add_usage_delta("ADD_DELTA", drx, dtx, dtotal)
                else:
                    action = "NO_DELTA"

        if not update_source:
            rx, tx, total, session = old_rx, old_tx, old_total, old_session

        cur.execute("""
            INSERT INTO traffic_sources (
                source_id, client_name, session_fingerprint,
                rx_bytes, tx_bytes, total_bytes,
                connected, real_address, virtual_ip, updated_at
            )
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,now())
            ON CONFLICT (source_id, client_name) DO UPDATE SET
                session_fingerprint = EXCLUDED.session_fingerprint,
                rx_bytes = EXCLUDED.rx_bytes,
                tx_bytes = EXCLUDED.tx_bytes,
                total_bytes = EXCLUDED.total_bytes,
                connected = EXCLUDED.connected,
                real_address = EXCLUDED.real_address,
                virtual_ip = EXCLUDED.virtual_ip,
                updated_at = now()
        """, (
            source, name, session, rx, tx, total,
            connected, real_address, virtual_ip
        ))

        if connected:
            cur.execute("""
                INSERT INTO active_sessions (
                    source_id, client_name, session_fingerprint,
                    real_address, virtual_ip, connected_since, last_seen
                )
                VALUES (%s,%s,%s,%s,%s,%s,now())
                ON CONFLICT (source_id, client_name, session_fingerprint) DO UPDATE SET
                    real_address = EXCLUDED.real_address,
                    virtual_ip = EXCLUDED.virtual_ip,
                    connected_since = COALESCE(active_sessions.connected_since, EXCLUDED.connected_since),
                    last_seen = now()
            """, (source, name, session, real_address, virtual_ip, connected_since))
        else:
            cur.execute("""
                DELETE FROM active_sessions
                WHERE source_id = %s AND client_name = %s
            """, (source, name))

        if args.verbose:
            print(action, "source=", source, "name=", name, "delta=", dtotal, "total=", total)

if args.apply:
    conn.commit()

    try:
        mirror = {"last": {}, "totals": {}, "blocked": {}}

        with conn.cursor() as cur:
            cur.execute("""
                SELECT client_name, rx_bytes, tx_bytes, total_bytes
                FROM traffic_totals
                ORDER BY client_name
            """)
            for name, rx, tx, total in cur.fetchall():
                mirror["totals"][name] = {
                    "rx": int(rx or 0),
                    "tx": int(tx or 0),
                    "total": int(total or 0),
                }

            cur.execute("""
                SELECT source_id, client_name, session_fingerprint,
                       rx_bytes, tx_bytes, total_bytes
                FROM traffic_sources
                ORDER BY source_id, client_name
            """)
            for source, name, session, rx, tx, total in cur.fetchall():
                mirror["last"][f"{source}:{name}"] = {
                    "rx": int(rx or 0),
                    "tx": int(tx or 0),
                    "total": int(total or 0),
                    "session": session or "",
                }

            cur.execute("""
                SELECT client_name, reason, details
                FROM quota_blocks
                WHERE blocked = true
                ORDER BY client_name
            """)
            for name, reason, details in cur.fetchall():
                if isinstance(details, dict):
                    item = details
                else:
                    item = {}
                item["reason"] = reason
                mirror["blocked"][name] = item

        tmp = GLOBAL_MIRROR.with_suffix(".json.tmp")
        tmp.write_text(json.dumps(mirror, ensure_ascii=False, indent=2))
        tmp.replace(GLOBAL_MIRROR)

    except Exception as e:
        print("MIRROR_FAIL", e)

else:
    conn.rollback()

conn.close()

print("mode =", "APPLY" if args.apply else "DRY-RUN")
print("processed =", processed)
print("active =", active_count)
print("first_seen =", first_seen)
print("session_reset =", session_reset)
print("counter_reset =", counter_reset)
print("delta_count =", delta_count)
print("added_total_bytes =", added_total)
print("added_total_human =", gb(added_total))
