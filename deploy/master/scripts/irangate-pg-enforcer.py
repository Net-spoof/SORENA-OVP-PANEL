#!/usr/bin/env python3
from pathlib import Path
import argparse
import json
import time
import subprocess
import urllib.request
import urllib.parse
import psycopg2

ENV = Path("/etc/irangate-db.env")
NODES_FILE = Path("/opt/irangate/webpanel/nodes.json")
BLOCK_DIR = Path("/etc/openvpn/blocked-clients")

def load_env():
    d = {}
    for line in ENV.read_text().splitlines():
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            d[k.strip()] = v.strip()
    return d

def gb(n):
    try:
        return f"{int(n) / 1024 / 1024 / 1024:.2f} GB"
    except Exception:
        return "0 GB"

def load_json(path, default):
    try:
        return json.loads(path.read_text())
    except Exception:
        return default

def get_nodes():
    data = load_json(NODES_FILE, [])
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        return data.get("nodes", [])
    return []

def node_url(node, path, name):
    agent = node.get("agent_url", "")
    parsed = urllib.parse.urlparse(agent)
    if not parsed.scheme or not parsed.hostname:
        return ""
    return f"{parsed.scheme}://{parsed.hostname}:5024{path}?name={urllib.parse.quote(name)}"

def call_node(node, path, name):
    url = node_url(node, path, name)
    if not url:
        return False, "empty_url"

    token = node.get("token", "")
    req = urllib.request.Request(url, headers={
        "Authorization": "Bearer " + token,
    })

    node_name = node.get("id") or node.get("name") or "node"

    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            r.read()
        print(f"NODE OK {node_name}: {path} {name}")
        return True, "ok"
    except Exception as e:
        print(f"NODE FAIL {node_name}: {path} {name}: {e}")
        return False, str(e)

def local_block(name):
    BLOCK_DIR.mkdir(parents=True, exist_ok=True)
    (BLOCK_DIR / name).write_text("blocked\n")

    subprocess.run(
        f'printf "kill {name}\\nquit\\n" | nc -w 2 127.0.0.1 7505',
        shell=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )

    print(f"LOCAL BLOCK {name}")
    return True



def find_node_for_source(source_id):
    sid = str(source_id or "").strip().lower()
    if not sid:
        return None

    for node in get_nodes():
        vals = [
            node.get("id"),
            node.get("name"),
            node.get("node_id"),
            node.get("node_name"),
        ]

        for v in vals:
            if str(v or "").strip().lower() == sid:
                return node

    return None

def node_temp_kick_session(source_id, name):
    node = find_node_for_source(source_id)
    if not node:
        msg = f"NODE NOT FOUND source={source_id}"
        print(msg)
        return False, msg

    node_name = node.get("id") or node.get("name") or source_id

    ok1, msg1 = call_node(node, "/block-client", name)
    time.sleep(2)
    ok2, msg2 = call_node(node, "/unblock-client", name)

    ok = bool(ok1 and ok2)
    msg = f"node={node_name} block=({ok1},{msg1}) unblock=({ok2},{msg2})"
    print(f"NODE TEMP KICK {name}: {msg}")

    return ok, msg

def local_kill_session(real_address):
    if not real_address:
        return False

    cmd = f'{{ sleep 0.3; echo "kill {real_address}"; sleep 0.3; echo "quit"; }} | nc -w 3 127.0.0.1 7505'

    r = subprocess.run(
        cmd,
        shell=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )

    out = (r.stdout or "") + (r.stderr or "")
    print(f"LOCAL KILL SESSION {real_address}")

    if out.strip():
        print(out.strip()[:500])

    return True



def ikev2_cmd(*args):
    cmd = ["/usr/local/sbin/irangate-ikev2"] + list(args)

    try:
        r = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=12,
        )
    except Exception as e:
        msg = f"IKEV2 CMD EXCEPTION {' '.join(cmd)}: {e}"
        print(msg)
        return False, msg

    out = ((r.stdout or "") + (r.stderr or "")).strip()
    if out:
        print(out[:1000])

    return r.returncode == 0, out[:1000]


def ikev2_block(name):
    ok, msg = ikev2_cmd("block", name)
    print(f"IKEV2 BLOCK {name}: {ok}")
    return ok, msg


def ikev2_unblock(name):
    ok, msg = ikev2_cmd("unblock", name)
    print(f"IKEV2 UNBLOCK {name}: {ok}")
    return ok, msg


def ikev2_terminate(name):
    ok, msg = ikev2_cmd("terminate", name)
    print(f"IKEV2 TERMINATE {name}: {ok}")
    return ok, msg


def local_unblock(name):
    try:
        (BLOCK_DIR / name).unlink()
    except FileNotFoundError:
        pass

    print(f"LOCAL UNBLOCK {name}")
    return True

def unblock_everywhere(name):
    results = {"local": None, "ikev2": None, "nodes": []}

    try:
        results["local"] = local_unblock(name)
    except Exception as e:
        print(f"LOCAL UNBLOCK FAIL {name}: {e}")
        results["local"] = False

    try:
        ok, msg = ikev2_unblock(name)
        results["ikev2"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"IKEV2 UNBLOCK FAIL {name}: {e}")
        results["ikev2"] = {"ok": False, "message": str(e)}

    for node in get_nodes():
        if node.get("is_local"):
            continue

        ok, msg = call_node(node, "/unblock-client", name)
        results["nodes"].append({
            "node": node.get("id") or node.get("name"),
            "ok": ok,
            "message": msg,
        })

    return results

def block_everywhere(name):
    results = {"local": None, "ikev2": None, "nodes": []}

    try:
        results["local"] = local_block(name)
    except Exception as e:
        print(f"LOCAL BLOCK FAIL {name}: {e}")
        results["local"] = False

    try:
        ok, msg = ikev2_block(name)
        results["ikev2"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"IKEV2 BLOCK FAIL {name}: {e}")
        results["ikev2"] = {"ok": False, "message": str(e)}

    for node in get_nodes():
        if node.get("is_local"):
            continue

        ok, msg = call_node(node, "/block-client", name)
        results["nodes"].append({
            "node": node.get("id") or node.get("name"),
            "ok": ok,
            "message": msg,
        })

    return results

ap = argparse.ArgumentParser()
ap.add_argument("--apply", action="store_true")
args = ap.parse_args()

mode = "APPLY_DB_ONLY" if args.apply else "DRY-RUN"
print("mode =", mode)

env = load_env()
conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

blocked_now = 0
unblocked_now = 0
events_now = 0

with conn.cursor() as cur:
    print()
    print("===== traffic / expiry candidates =====")

    cur.execute("""
        SELECT client_name, used_bytes, traffic_limit_bytes, expire_at, is_blocked, reason
        FROM enforcement_candidates
        WHERE reason IS NOT NULL
        ORDER BY used_bytes DESC
    """)

    rows = cur.fetchall()
    print("count =", len(rows))

    for name, used, limitb, expire_at, is_blocked, reason in rows:
        action = "SKIP_ALREADY_BLOCKED" if is_blocked else "WOULD_BLOCK"
        # IRANGATE_SKIP_ALREADY_BLOCKED_RUNTIME_V1
        # Already-blocked clients must not be killed or reconciled repeatedly.
        print(f"{action} name={name} reason={reason} used={gb(used)} limit={gb(limitb)} expire_at={expire_at}")

        if args.apply and not is_blocked:
            details = {
                "used_bytes": int(used or 0),
                "traffic_limit_bytes": int(limitb or 0),
                "expire_at": str(expire_at) if expire_at else None,
                "reason": reason,
            }

            details["block_results"] = block_everywhere(name)

            cur.execute("""
                UPDATE clients
                SET is_blocked = true,
                    block_reason = %s,
                    updated_at = now()
                WHERE client_name = %s
            """, (reason, name))

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
                INSERT INTO enforcement_events (client_name, event_type, reason, details)
                VALUES (%s,'db_block',%s,%s::jsonb)
            """, (name, reason, json.dumps(details)))

            blocked_now += 1
            events_now += 1

    print()
    print("===== unblock candidates =====")

    cur.execute("""
        SELECT
            c.client_name,
            COALESCE(t.total_bytes,0) AS used_bytes,
            c.traffic_limit_bytes,
            c.expire_at,
            c.block_reason,
            qb.reason AS quota_reason
        FROM clients c
        LEFT JOIN traffic_totals t ON t.client_name = c.client_name
        LEFT JOIN quota_blocks qb ON qb.client_name = c.client_name
        WHERE c.is_enabled = true
          AND (c.is_blocked = true OR qb.blocked = true)
          AND NOT (
              (c.expire_at IS NOT NULL AND c.expire_at <= now())
              OR
              (c.traffic_limit_bytes > 0 AND COALESCE(t.total_bytes,0) >= c.traffic_limit_bytes)
          )
        ORDER BY c.client_name
    """)

    unblock_rows = cur.fetchall()
    print("count =", len(unblock_rows))

    for name, used, limitb, expire_at, block_reason, quota_reason in unblock_rows:
        print(f"WOULD_UNBLOCK name={name} used={gb(used)} limit={gb(limitb)} expire_at={expire_at}")

        if args.apply:
            details = {
                "used_bytes": int(used or 0),
                "traffic_limit_bytes": int(limitb or 0),
                "expire_at": str(expire_at) if expire_at else None,
                "previous_block_reason": block_reason,
                "previous_quota_reason": quota_reason,
            }

            details["unblock_results"] = unblock_everywhere(name)

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
            """, (name, json.dumps(details)))

            cur.execute("""
                INSERT INTO enforcement_events (client_name, event_type, reason, details)
                VALUES (%s,'db_unblock','quota_cleared',%s::jsonb)
            """, (name, json.dumps(details)))

            unblocked_now += 1
            events_now += 1

    print()
    print("===== connection limit violations =====")

    cur.execute("""
        SELECT
            c.client_name,
            c.max_connections,
            count(s.*) AS active_connections,
            string_agg(
                s.source_id || ':' || COALESCE(s.real_address,''),
                ' | '
                ORDER BY s.connected_since NULLS LAST, s.last_seen
            ) AS sessions
        FROM clients c
        JOIN active_sessions s ON s.client_name = c.client_name
        WHERE c.is_enabled = true
        GROUP BY c.client_name, c.max_connections
        HAVING count(s.*) > c.max_connections
        ORDER BY active_connections DESC
    """)

    conn_rows = cur.fetchall()
    print("count =", len(conn_rows))

    for name, max_conn, active_conn, sessions in conn_rows:
        extra = int(active_conn) - int(max_conn)
        print(f"WOULD_KILL_OLD_SESSIONS name={name} active={active_conn} limit={max_conn} extra={extra} sessions={sessions}")

        if args.apply:
            cur.execute("""
                SELECT source_id, client_name, session_fingerprint,
                       real_address, virtual_ip, connected_since, last_seen
                FROM active_sessions
                WHERE client_name = %s
                ORDER BY connected_since NULLS FIRST, last_seen ASC
                LIMIT %s
            """, (name, extra))

            old_sessions = cur.fetchall()

            killed = []

            for source_id, client_name, session_fp, real_address, virtual_ip, connected_since, last_seen in old_sessions:
                result = {
                    "source_id": source_id,
                    "real_address": real_address,
                    "virtual_ip": virtual_ip,
                    "session_fingerprint": session_fp,
                    "connected_since": str(connected_since) if connected_since else None,
                    "last_seen": str(last_seen) if last_seen else None,
                    "action": None,
                    "ok": False,
                }

                if source_id == "local":
                    ok = local_kill_session(real_address)
                    result["action"] = "local_kill_session"
                    result["ok"] = bool(ok)

                    cur.execute("""
                        DELETE FROM active_sessions
                        WHERE source_id = %s
                          AND client_name = %s
                          AND session_fingerprint = %s
                    """, (source_id, client_name, session_fp))

                elif str(source_id) == "ikev2":
                    ok, msg = ikev2_terminate(client_name)
                    result["action"] = "ikev2_terminate"
                    result["ok"] = bool(ok)
                    result["message"] = msg

                    if ok:
                        cur.execute("""
                            DELETE FROM active_sessions
                            WHERE source_id = %s
                              AND client_name = %s
                              AND session_fingerprint = %s
                        """, (source_id, client_name, session_fp))

                else:
                    ok, msg = node_temp_kick_session(source_id, name)
                    result["action"] = "node_temp_block_unblock"
                    result["ok"] = bool(ok)
                    result["message"] = msg

                    if ok:
                        cur.execute("""
                            DELETE FROM active_sessions
                            WHERE source_id = %s
                              AND client_name = %s
                              AND session_fingerprint = %s
                        """, (source_id, client_name, session_fp))

                killed.append(result)

            details = {
                "active_connections": int(active_conn),
                "max_connections": int(max_conn),
                "extra": extra,
                "sessions": sessions,
                "killed": killed,
            }

            cur.execute("""
                INSERT INTO enforcement_events (client_name, event_type, reason, details)
                VALUES (%s,'connection_kill','connection_limit',%s::jsonb)
            """, (name, json.dumps(details)))

            events_now += 1

if args.apply:
    conn.commit()
else:
    conn.rollback()

conn.close()

print()
print("blocked_now =", blocked_now)
print("unblocked_now =", unblocked_now)
print("events_now =", events_now)
