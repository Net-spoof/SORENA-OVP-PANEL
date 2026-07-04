#!/usr/bin/env python3
import json
from pathlib import Path
from datetime import datetime, timezone
import psycopg2

ENV = Path("/etc/irangate-db.env")
LOCAL = Path("/opt/irangate/traffic/current_stats.json")
NODE = Path("/opt/irangate/traffic/node_current_stats.json")

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

def name_of(x):
    return x.get("name") or x.get("client_name") or x.get("common_name")

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
        parts.append("real_address=" + str(x.get("real_address") or "unknown"))
    return "|".join(parts)

items = []

for x in load_json(LOCAL):
    if x.get("connected") is True:
        x["_source_id"] = "local"
        items.append(x)

for x in load_json(NODE):
    if x.get("connected") is True or x.get("is_connected") is True or x.get("active") is True:
        x["_source_id"] = str(x.get("node_id") or "node")
        items.append(x)

env = load_env()
conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

with conn.cursor() as cur:
    cur.execute("DELETE FROM active_sessions WHERE last_seen < now() - interval '2 minutes'")

    for x in items:
        name = name_of(x)
        if not name:
            continue

        source = x["_source_id"]
        session = session_of(x)
        real_address = x.get("real_address")
        virtual_ip = x.get("virtual_ip") or x.get("virtual_address")
        connected_since = parse_time(x.get("connected_since") or x.get("last_connection"))

        cur.execute("""
            INSERT INTO clients (client_name, display_name, updated_at)
            VALUES (%s,%s,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name, name))

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

conn.commit()
conn.close()

print("synced_active_sessions =", len(items))
