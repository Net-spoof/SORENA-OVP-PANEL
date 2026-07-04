#!/usr/bin/env python3
import sys
import json
import subprocess
import urllib.request
import urllib.parse
from pathlib import Path
import psycopg2

ENV = Path("/etc/irangate-db.env")
NODES_FILE = Path("/opt/irangate/webpanel/nodes.json")
BLOCK_DIR = Path("/etc/openvpn/blocked-clients")

if len(sys.argv) < 2:
    print("usage: irangate-pg-reset-client.py CLIENT_NAME")
    sys.exit(1)

name = sys.argv[1].strip()

def load_env():
    d = {}
    for line in ENV.read_text().splitlines():
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            d[k.strip()] = v.strip()
    return d

def load_nodes():
    try:
        data = json.loads(NODES_FILE.read_text())
    except Exception:
        return []

    if isinstance(data, list):
        return data

    if isinstance(data, dict):
        return data.get("nodes", [])

    return []

def local_unblock(client):
    try:
        (BLOCK_DIR / client).unlink()
        print("LOCAL UNBLOCK OK", client)
    except FileNotFoundError:
        print("LOCAL UNBLOCK OK no-file", client)

def node_unblock(client):
    for node in load_nodes():
        if node.get("is_local"):
            continue

        agent = node.get("agent_url", "")
        parsed = urllib.parse.urlparse(agent)

        if not parsed.scheme or not parsed.hostname:
            continue

        url = f"{parsed.scheme}://{parsed.hostname}:5024/unblock-client?name={urllib.parse.quote(client)}"
        token = node.get("token", "")

        req = urllib.request.Request(url, headers={
            "Authorization": "Bearer " + token,
        })

        node_name = node.get("id") or node.get("name") or "node"

        try:
            urllib.request.urlopen(req, timeout=5).read()
            print("NODE UNBLOCK OK", node_name, client)
        except Exception as e:
            print("NODE UNBLOCK FAIL", node_name, client, e)

env = load_env()

print("===== capture current baselines before reset =====")
subprocess.run(
    ["/usr/local/bin/irangate-pg-usage-tick.py", "--apply"],
    check=False
)

conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

with conn.cursor() as cur:
    cur.execute("""
        SELECT c.client_name, c.traffic_limit_bytes, COALESCE(t.total_bytes,0), c.is_blocked, c.block_reason
        FROM clients c
        LEFT JOIN traffic_totals t ON t.client_name = c.client_name
        WHERE c.client_name = %s
    """, (name,))
    before = cur.fetchone()
    print("BEFORE =", before)

    cur.execute("""
        INSERT INTO clients (client_name, display_name, updated_at)
        VALUES (%s,%s,now())
        ON CONFLICT (client_name) DO NOTHING
    """, (name, name))

    cur.execute("""
        INSERT INTO traffic_totals (client_name, rx_bytes, tx_bytes, total_bytes, updated_at)
        VALUES (%s,0,0,0,now())
        ON CONFLICT (client_name) DO UPDATE SET
            rx_bytes = 0,
            tx_bytes = 0,
            total_bytes = 0,
            updated_at = now()
    """, (name,))

    cur.execute("""
        UPDATE clients
        SET is_blocked = false,
            block_reason = NULL,
            status = 'inactive',
            updated_at = now()
        WHERE client_name = %s
    """, (name,))

    cur.execute("""
        INSERT INTO quota_blocks (client_name, blocked, reason, details, updated_at)
        VALUES (%s,false,NULL,%s::jsonb,now())
        ON CONFLICT (client_name) DO UPDATE SET
            blocked = false,
            reason = NULL,
            details = EXCLUDED.details,
            updated_at = now()
    """, (name, json.dumps({"manual_reset": True, "client_name": name})))

    cur.execute("""
        INSERT INTO enforcement_events (client_name, event_type, reason, details)
        VALUES (%s,'manual_reset','traffic_reset',%s::jsonb)
    """, (name, json.dumps({"manual_reset": True, "client_name": name})))

    cur.execute("""
        SELECT c.client_name, c.traffic_limit_bytes, COALESCE(t.total_bytes,0), c.is_blocked, c.block_reason
        FROM clients c
        LEFT JOIN traffic_totals t ON t.client_name = c.client_name
        WHERE c.client_name = %s
    """, (name,))
    after = cur.fetchone()
    print("AFTER =", after)

conn.commit()
conn.close()

local_unblock(name)
node_unblock(name)

print("===== refresh mirror after reset =====")
subprocess.run(
    ["/usr/local/bin/irangate-pg-usage-tick.py", "--apply"],
    check=False
)

print("RESET DONE", name)
