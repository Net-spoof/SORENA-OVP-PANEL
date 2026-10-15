#!/usr/bin/env python3
"""Read active OpenVPN sessions in status v1/v2/v3 without losing session age.

This file writes *snapshots*, not totals. PostgreSQL owns durable accounting.
Unknown/malformed lines fail closed instead of fabricating connection times.
"""
import csv
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

OUT = Path("/opt/irangate/traffic/current_stats.json")
STATUS_PATHS = [
    "/etc/openvpn/openvpn-status.log",
    "/etc/openvpn/openvpn-status-server.log",
    "/etc/openvpn/openvpn-status-server-tcp-443.log",
]


def now_iso():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def to_int(value):
    try:
        return max(0, int(str(value).strip()))
    except (TypeError, ValueError):
        return 0


def normalized_row(header, values, captured):
    if len(values) < len(header):
        return None
    item = dict(zip(header, values))
    name = (item.get("Common Name") or item.get("Username") or "").strip()
    if not name or name.upper() == "UNDEF":
        return None
    real = (item.get("Real Address") or "").strip()
    if not real:
        return None
    rx = to_int(item.get("Bytes Received"))
    tx = to_int(item.get("Bytes Sent"))
    # OpenVPN v2/v3 offers an unambiguous Unix epoch; v1 offers a text value.
    epoch = to_int(item.get("Connected Since (time_t)"))
    if epoch > 0:
        connected_since = datetime.fromtimestamp(
            epoch, timezone.utc
        ).isoformat().replace("+00:00", "Z")
    else:
        connected_since = (item.get("Connected Since") or "").strip()
    return {
        "client_name": name,
        "current_received": rx,
        "current_sent": tx,
        "current_total": rx + tx,
        "last_seen": captured,
        "connected": True,
        "real_address": real,
        "virtual_ip": (item.get("Virtual Address") or "").strip(),
        "client_id": (item.get("Client ID") or "").strip(),
        "peer_id": (item.get("Peer ID") or "").strip(),
        "connected_since": connected_since,
    }


def parse_status_text(text, captured=None):
    captured = captured or now_iso()
    result = []
    header = None
    v1_rows = False
    # v1 and v2 are comma-separated; v3 is tab-separated.
    for raw in text.splitlines():
        raw = raw.strip()
        if not raw:
            continue
        delimiter = "\t" if "\t" in raw and (
            raw.startswith(("HEADER\t", "CLIENT_LIST\t", "TITLE\t",
                            "TIME\t", "ROUTING_TABLE\t"))
        ) else ","
        try:
            columns = next(csv.reader([raw], delimiter=delimiter))
        except csv.Error:
            continue
        if not columns:
            continue
        if columns[0] == "HEADER" and len(columns) > 2 and columns[1] == "CLIENT_LIST":
            header, v1_rows = columns[2:], False
            continue
        if columns[0] == "CLIENT_LIST" and header is not None:
            row = normalized_row(header, columns[1:], captured)
            if row:
                result.append(row)
            continue
        if columns[:2] == ["Common Name", "Real Address"]:
            header, v1_rows = columns, True
            continue
        if columns[0] in ("ROUTING TABLE", "GLOBAL STATS", "END",
                          "Virtual Address", "HEADER"):
            if v1_rows:
                v1_rows = False
            continue
        if v1_rows and header is not None:
            row = normalized_row(header, columns, captured)
            if row:
                result.append(row)
    return result


def read_status_files():
    result = []
    seen_files = set()
    seen_sessions = set()
    for path in STATUS_PATHS:
        p = Path(path)
        try:
            canonical = str(p.resolve())
            if canonical in seen_files:
                continue
            seen_files.add(canonical)
            if time.time() - p.stat().st_mtime > 180:
                # A dead listener can leave a believable but stale status
                # file. Never charge it as a newly active session.
                continue
            text = p.read_text(errors="replace")
        except OSError:
            continue
        for client in parse_status_text(text):
            key = (
                client["client_name"], client["real_address"],
                client["connected_since"], client["virtual_ip"],
            )
            if key not in seen_sessions:
                client["status_file"] = canonical
                result.append(client)
                seen_sessions.add(key)
    return result


def read_management():
    cmd = "{ sleep 0.2; echo status; sleep 0.2; echo quit; } | nc -w 3 127.0.0.1 7505"
    try:
        result = subprocess.run(
            ["bash", "-lc", cmd], capture_output=True, text=True, timeout=5
        )
    except (OSError, subprocess.TimeoutExpired):
        return []
    return parse_status_text((result.stdout or "") + "\n" + (result.stderr or ""))


def self_test():
    stamp = "2026-10-15T11:00:00Z"
    v1 = """OpenVPN CLIENT LIST
Common Name,Real Address,Bytes Received,Bytes Sent,Connected Since
alice,1.2.3.4:1234,11000000000,250,Wed Oct  7 11:00:00 2026
ROUTING TABLE
"""
    v2 = """TITLE,OpenVPN 2.7
HEADER,CLIENT_LIST,Common Name,Real Address,Virtual Address,Virtual IPv6 Address,Bytes Received,Bytes Sent,Connected Since,Connected Since (time_t),Username,Client ID,Peer ID
CLIENT_LIST,alice,1.2.3.4:5555,10.8.0.2,,11000000050,320,ignored,1791367200,alice,1,1
END
"""
    v3 = v2.replace(",", "\t")
    a = parse_status_text(v1, stamp)
    b = parse_status_text(v2, stamp)
    c = parse_status_text(v3, stamp)
    assert len(a) == len(b) == len(c) == 1
    assert a[0]["connected_since"].startswith("Wed Oct")
    assert b[0]["connected_since"] and b[0]["connected_since"] == c[0]["connected_since"]
    assert b[0]["current_total"] == 11000000370
    assert b[0]["client_id"] == "1"
    assert a[0]["current_total"] == 11000000250
    assert parse_status_text("bad status\nEND", stamp) == []
    print("local_status_snapshot_self_test = PASS")


def main():
    clients = read_status_files()
    if not clients:
        clients = read_management()
    OUT.parent.mkdir(parents=True, exist_ok=True)
    tmp = OUT.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(clients, ensure_ascii=False, indent=2))
    os.replace(tmp, OUT)
    print("local_clients =", len(clients))
    for c in clients:
        # Avoid printing the user's public real_address in daemon logs.
        print(c["client_name"], c["current_total"],
              "has_connected_since=", bool(c["connected_since"]))


if __name__ == "__main__":
    if "--self-test" in sys.argv:
        self_test()
    else:
        main()
