#!/usr/bin/env python3
import json, time, urllib.request
from pathlib import Path

NODES_FILE = Path("/opt/irangate/webpanel/nodes.json")
TRAFFIC_DIR = Path("/opt/irangate/traffic")
OUT_FILE = TRAFFIC_DIR / "node_current_stats.json"
STATE_FILE = TRAFFIC_DIR / "node_usage_state.json"

TRAFFIC_DIR.mkdir(parents=True, exist_ok=True)

def load_json(path, default):
    try:
        return json.loads(path.read_text())
    except Exception:
        return default

def save_json(path, data):
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2))
    tmp.replace(path)

def nodes_list(data):
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        return data.get("nodes", [])
    return []

def fetch_usage(node):
    url = node.get("agent_url", "").rstrip("/") + "/usage"
    token = node.get("token", "")
    req = urllib.request.Request(url, headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req, timeout=8) as r:
        payload = json.loads(r.read().decode("utf-8", "ignore"))
    if not payload.get("success"):
        return []
    return payload.get("clients") or payload.get("data") or []

nodes = nodes_list(load_json(NODES_FILE, []))
state = load_json(STATE_FILE, {"last": {}, "totals": {}})
state.setdefault("last", {})
state.setdefault("totals", {})

# Remove invalid synthetic client state left by older parsers.
for section in ("last", "totals"):
    bucket = state.get(section)

    if not isinstance(bucket, dict):
        continue

    for key in list(bucket):
        client_name = str(key).rsplit(":", 1)[-1].strip()

        if not client_name or client_name.upper() == "UNDEF":
            del bucket[key]

out = []
now_iso = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
now_ts = int(time.time())

for node in nodes:
    if node.get("is_local"):
        continue
    if not node.get("enabled", True):
        continue
    if not node.get("agent_url") or not node.get("token"):
        continue

    node_id = node.get("id") or node.get("name") or node.get("agent_url")
    node_name = node.get("name", node_id)

    try:
        clients = fetch_usage(node)
    except Exception as e:
        print(f"{node_id}: fetch failed: {e}")
        continue

    for c in clients:
        name = str(
            c.get("client_name")
            or c.get("common_name")
            or ""
        ).strip()

        if not name or name.upper() == "UNDEF":
            print(f"{node_id}: skip invalid client name={name!r}")
            continue

        rx = int(c.get("bytes_received") or 0)
        tx = int(c.get("bytes_sent") or 0)
        key = f"{node_id}:{name}"

        if key not in state["last"] or key not in state["totals"]:
            drx, dtx = rx, tx
            total = {"rx": rx, "tx": tx}
        else:
            last = state["last"].get(key, {"rx": 0, "tx": 0})
            total = state["totals"].get(key, {"rx": 0, "tx": 0})

            drx = rx - int(last.get("rx", 0))
            dtx = tx - int(last.get("tx", 0))

            if drx < 0:
                drx = rx
            if dtx < 0:
                dtx = tx

            total["rx"] = int(total.get("rx", 0)) + drx
            total["tx"] = int(total.get("tx", 0)) + dtx

        state["last"][key] = {"rx": rx, "tx": tx}
        state["totals"][key] = total

        crx = int(total["rx"])
        ctx = int(total["tx"])
        ctotal = crx + ctx
        mb = round(ctotal / 1024 / 1024, 2)

        out.append({
            "name": name,
            "client_name": name,
            "common_name": name,
            "node_id": node_id,
            "node_name": node_name,

            "bytes_received": crx,
            "bytes_sent": ctx,
            "total_bytes": ctotal,
            "current_received": crx,
            "current_sent": ctx,
            "current_total": ctotal,
            "daily_usage": ctotal,
            "monthly_usage": ctotal,
            "data_used_mb": mb,

            "upload": crx,
            "download": ctx,
            "used_bytes": ctotal,
            "used": ctotal,
            "usage": ctotal,
            "traffic": ctotal,
            "used_traffic": ctotal,

            "connected": True,
            "is_connected": True,
            "active": True,
            "status": "active",

            "real_address": c.get("real_address", ""),
            "virtual_ip": c.get("virtual_ip") or c.get("virtual_address", ""),
            "virtual_address": c.get("virtual_address", ""),
            "connected_since": c.get("connected_since", ""),
            "last_seen": now_iso,
            "last_connection": now_iso,
            "last_updated": now_iso,
            "last_updated_ts": now_ts,
        })

        print(f"{node_id}/{name}: +{drx+dtx} bytes, total={ctotal}")

save_json(STATE_FILE, state)
save_json(OUT_FILE, out)
