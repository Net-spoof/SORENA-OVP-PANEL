#!/usr/bin/env python3
import json
import os
import re
import subprocess
from pathlib import Path
from datetime import datetime, timezone, timedelta

OUT = Path("/opt/irangate/traffic/ikev2_current_stats.json")
STATE = Path("/opt/irangate/ikev2/stats_state.json")
HISTORY = Path("/opt/irangate/traffic/ikev2_last_seen.json")

def now():
    return datetime.now(timezone.utc)

def iso(dt):
    return dt.isoformat().replace("+00:00", "Z")

def load_state():
    try:
        data = json.loads(STATE.read_text())
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}

def save_state(state):
    STATE.parent.mkdir(parents=True, exist_ok=True)
    tmp = STATE.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(state, ensure_ascii=False, indent=2))
    os.replace(tmp, STATE)
    os.chmod(STATE, 0o600)


def load_history():
    try:
        data = json.loads(HISTORY.read_text())
    except Exception:
        return {}

    result = {}

    if isinstance(data, dict):
        iterable = data.values()
    elif isinstance(data, list):
        iterable = data
    else:
        iterable = []

    for row in iterable:
        if not isinstance(row, dict):
            continue

        user = str(
            row.get("client_name")
            or row.get("name")
            or row.get("username")
            or ""
        ).strip()

        if user:
            result[user] = row

    return result


def save_history(history):
    HISTORY.parent.mkdir(parents=True, exist_ok=True)

    rows = [
        row
        for row in history.values()
        if isinstance(row, dict)
    ]

    rows.sort(
        key=lambda row: str(
            row.get("client_name") or ""
        ).lower()
    )

    tmp = HISTORY.with_suffix(".json.tmp")
    tmp.write_text(
        json.dumps(
            rows,
            ensure_ascii=False,
            indent=2,
        ) + "\n"
    )

    os.replace(tmp, HISTORY)
    os.chmod(HISTORY, 0o600)


def run_swanctl():
    try:
        r = subprocess.run(
            ["swanctl", "--list-sas"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=8,
        )
        return r.stdout or ""
    except Exception:
        return ""

def parse_bytes(line):
    m = re.search(r",\s*([0-9]+)\s+bytes", line)
    return int(m.group(1)) if m else 0

def parse():
    state = load_state()
    history = load_history()
    sessions_state = state.get("sessions")
    if not isinstance(sessions_state, dict):
        sessions_state = {}

    text = run_swanctl()
    items = []
    active_keys = set()
    cur = None

    def flush():
        nonlocal cur
        if not cur:
            return

        user = cur.get("user")
        ike_id = cur.get("ike_id")
        if not user or not ike_id:
            cur = None
            return

        rx = int(cur.get("rx") or 0)
        tx = int(cur.get("tx") or 0)
        total = rx + tx
        real = cur.get("real_address") or ""
        vip = cur.get("virtual_ip") or ""
        n = now()

        key = f"{ike_id}:{user}"
        active_keys.add(key)

        old = sessions_state.get(key)
        if not isinstance(old, dict):
            old = {}

        established_seconds = cur.get("established_seconds")
        if old.get("connected_since"):
            connected_since = old["connected_since"]
        elif isinstance(established_seconds, int) and established_seconds >= 0:
            connected_since = iso(n - timedelta(seconds=established_seconds))
        else:
            connected_since = iso(n)

        sessions_state[key] = {
            "user": user,
            "ike_id": ike_id,
            "real_address": real,
            "virtual_ip": vip,
            "connected_since": connected_since,
            "last_total": total,
            "last_seen": iso(n),
        }

        history[user] = {
            "source_id": "ikev2-history",
            "client_name": user,
            "last_seen": iso(n),
            "connected": False,
        }

        items.append({
            "source_id": "ikev2",
            "ike_id": ike_id,
            "session_id": f"ike:{ike_id}",
            "client_name": user,
            "current_received": rx,
            "current_sent": tx,
            "current_total": total,
            "last_seen": iso(n),
            "connected": True,
            "real_address": real,
            "virtual_ip": vip,
            "connected_since": connected_since,
        })

        cur = None

    for raw in text.splitlines():
        line = raw.rstrip()

        if re.match(r"^\S.*#\d+,\s+ESTABLISHED,\s+IKEv2", line):
            flush()
            m = re.search(r"#(\d+),", line)
            cur = {"ike_id": m.group(1) if m else ""}
            continue

        if cur is None:
            continue

        if " EAP: '" in line:
            m = re.search(r"@\s*([^\[\s]+)\[(\d+)\].*EAP:\s*'([^']+)'\s*\[([^\]]+)\]", line)
            if m:
                cur["real_address"] = f"{m.group(1)}:{m.group(2)}"
                cur["user"] = m.group(3)
                cur["virtual_ip"] = m.group(4)

        m = re.search(r"established\s+(\d+)s\s+ago", line)
        if m:
            cur["established_seconds"] = int(m.group(1))

        if re.match(r"^\s+in\s+", line):
            cur["rx"] = parse_bytes(line)

        if re.match(r"^\s+out\s+", line):
            cur["tx"] = parse_bytes(line)

    flush()

    for key in list(sessions_state.keys()):
        if key not in active_keys:
            sessions_state.pop(key, None)

    state["sessions"] = sessions_state
    save_state(state)
    save_history(history)

    return items

def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    data = parse()
    tmp = OUT.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2))
    os.replace(tmp, OUT)

    print("ikev2_clients =", len(data))
    for x in data:
        print(x["client_name"], x["current_total"], x["real_address"], x["virtual_ip"], x["connected_since"])

if __name__ == "__main__":
    main()
