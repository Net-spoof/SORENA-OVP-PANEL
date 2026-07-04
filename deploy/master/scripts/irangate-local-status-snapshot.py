#!/usr/bin/env python3
import json
import os
import subprocess
from pathlib import Path
from datetime import datetime, timezone

OUT = Path("/opt/irangate/traffic/current_stats.json")
STATUS_PATHS = [
    "/etc/openvpn/openvpn-status.log",
    "/etc/openvpn/openvpn-status-server.log",
    "/etc/openvpn/openvpn-status-server-tcp-443.log",
]

def now_iso():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")

def to_int(x):
    try:
        return int(str(x).strip())
    except Exception:
        return 0

def parse_status_text(text):
    clients = []
    routes = {}

    lines = [x.strip() for x in text.splitlines() if x.strip()]

    in_clients = False
    in_routes = False

    for line in lines:
        if line.startswith("Common Name,Real Address,Bytes Received,Bytes Sent,Connected Since"):
            in_clients = True
            in_routes = False
            continue

        if line == "ROUTING TABLE":
            in_clients = False
            in_routes = False
            continue

        if line.startswith("Virtual Address,Common Name,Real Address,Last Ref"):
            in_routes = True
            in_clients = False
            continue

        if line == "GLOBAL STATS" or line == "END":
            in_clients = False
            in_routes = False
            continue

        if in_clients:
            parts = line.split(",")
            if len(parts) < 5:
                continue

            name = parts[0].strip()
            real = parts[1].strip()
            rx = to_int(parts[2])
            tx = to_int(parts[3])
            connected_since = parts[4].strip()

            if not name or name == "UNDEF":
                continue

            clients.append({
                "client_name": name,
                "current_received": rx,
                "current_sent": tx,
                "current_total": rx + tx,
                "last_seen": now_iso(),
                "connected": True,
                "real_address": real,
                "virtual_ip": "",
                "connected_since": connected_since,
            })

        elif in_routes:
            parts = line.split(",")
            if len(parts) < 3:
                continue

            virtual_ip = parts[0].strip()
            name = parts[1].strip()
            real = parts[2].strip()
            routes[(name, real)] = virtual_ip

    for c in clients:
        c["virtual_ip"] = routes.get((c["client_name"], c["real_address"]), "")

    return clients

def read_status_file():
    for path in STATUS_PATHS:
        try:
            text = Path(path).read_text(errors="ignore")
        except Exception:
            continue

        clients = parse_status_text(text)
        if clients:
            return clients

    return []

def read_management():
    cmd = "{ sleep 0.2; echo status; sleep 0.2; echo quit; } | nc -w 3 127.0.0.1 7505"
    try:
        r = subprocess.run(
            ["bash", "-lc", cmd],
            capture_output=True,
            text=True,
            timeout=5,
        )
    except Exception:
        return []

    return parse_status_text((r.stdout or "") + "\n" + (r.stderr or ""))

def main():
    clients = read_status_file()

    if not clients:
        clients = read_management()

    OUT.parent.mkdir(parents=True, exist_ok=True)

    tmp = OUT.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(clients, ensure_ascii=False, indent=2))
    os.replace(tmp, OUT)

    print("local_clients =", len(clients))
    for c in clients:
        print(c["client_name"], c["current_total"], c["real_address"], c["virtual_ip"])

if __name__ == "__main__":
    main()
