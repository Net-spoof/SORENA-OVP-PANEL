#!/usr/bin/env python3
import json
from pathlib import Path
import psycopg2

ENV = Path("/etc/irangate-db.env")
STATE = Path("/opt/irangate/traffic/global_usage_totals.json")

def load_env():
    d = {}
    for line in ENV.read_text().splitlines():
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            d[k.strip()] = v.strip()
    return d

def to_int(v):
    try:
        return int(float(v or 0))
    except Exception:
        return 0

def session_field(session, key):
    for part in str(session or "").split("|"):
        if part.startswith(key + "="):
            return part.split("=", 1)[1]
    return None

env = load_env()
data = json.loads(STATE.read_text())

totals = data.get("totals", {}) or {}
lasts = data.get("last", {}) or {}
blocked = data.get("blocked", {}) or {}

conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

with conn.cursor() as cur:
    names = set(totals.keys()) | set(blocked.keys())
    for key in lasts.keys():
        if ":" in key:
            names.add(key.split(":", 1)[1])
        else:
            names.add(key)

    for name in sorted(names):
        if not name:
            continue
        cur.execute("""
            INSERT INTO clients (client_name, display_name, updated_at)
            VALUES (%s,%s,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name, name))

    for name, v in totals.items():
        cur.execute("""
            INSERT INTO traffic_totals (client_name, rx_bytes, tx_bytes, total_bytes, updated_at)
            VALUES (%s,%s,%s,%s,now())
            ON CONFLICT (client_name) DO UPDATE SET
                rx_bytes = EXCLUDED.rx_bytes,
                tx_bytes = EXCLUDED.tx_bytes,
                total_bytes = EXCLUDED.total_bytes,
                updated_at = now()
        """, (
            name,
            to_int(v.get("rx")),
            to_int(v.get("tx")),
            to_int(v.get("total")),
        ))

    for key, v in lasts.items():
        if ":" in key:
            source_id, name = key.split(":", 1)
        else:
            source_id, name = "local", key

        session = str(v.get("session") or "")
        cur.execute("""
            INSERT INTO traffic_sources (
                source_id, client_name, session_fingerprint,
                rx_bytes, tx_bytes, total_bytes,
                connected, real_address, virtual_ip, updated_at
            )
            VALUES (%s,%s,%s,%s,%s,%s,false,%s,%s,now())
            ON CONFLICT (source_id, client_name) DO UPDATE SET
                session_fingerprint = EXCLUDED.session_fingerprint,
                rx_bytes = EXCLUDED.rx_bytes,
                tx_bytes = EXCLUDED.tx_bytes,
                total_bytes = EXCLUDED.total_bytes,
                real_address = EXCLUDED.real_address,
                virtual_ip = EXCLUDED.virtual_ip,
                updated_at = now()
        """, (
            source_id,
            name,
            session,
            to_int(v.get("rx")),
            to_int(v.get("tx")),
            to_int(v.get("total")),
            session_field(session, "real_address"),
            session_field(session, "virtual_ip"),
        ))

    for name, v in blocked.items():
        details = v if isinstance(v, dict) else {"value": v}
        reason = details.get("reason") or "traffic_limit"
        cur.execute("""
            INSERT INTO quota_blocks (client_name, blocked, reason, details, updated_at)
            VALUES (%s,true,%s,%s::jsonb,now())
            ON CONFLICT (client_name) DO UPDATE SET
                blocked = true,
                reason = EXCLUDED.reason,
                details = EXCLUDED.details,
                updated_at = now()
        """, (name, reason, json.dumps(details)))

        cur.execute("""
            UPDATE clients
            SET is_blocked = true, block_reason = %s, updated_at = now()
            WHERE client_name = %s
        """, (reason, name))

conn.commit()
conn.close()

print("imported_totals =", len(totals))
print("imported_sources =", len(lasts))
print("imported_blocked =", len(blocked))
