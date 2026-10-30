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
from irangate_local_snapshot import SESSION_FILE, make_payload, read_snapshot

OUT = SESSION_FILE
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
    # OpenVPN Management defaults to status v1, which omits per-session
    # identity in some deployments. Force v3 (TSV) for billable counters.
    cmd = "{ sleep 0.2; echo 'status 3'; sleep 0.2; echo quit; } | nc -w 3 127.0.0.1 7505"
    try:
        result = subprocess.run(
            ["bash", "-lc", cmd], capture_output=True, text=True, timeout=5
        )
    except (OSError, subprocess.TimeoutExpired):
        return []
    # Parse only the protocol response, never error text.
    rows = parse_status_text(result.stdout or "")
    for row in rows:
        row["status_file"] = "management:7505"
    return rows


def choose_snapshot(file_rows, management_reader=read_management, captured_at=None):
    """Prefer verified local sessions; consult status 3 if v1 was quarantined.

    The v1 parser may return rows even though NONE is safe to account.
    Never promote those rows by relaxing the v6 identity requirements.
    Prefer Management only if it supplies strictly more valid sessions;
    otherwise retain the already verified file rows.
    """
    captured_at = captured_at or now_iso()
    file_payload, file_rejected = make_payload(file_rows, captured_at)
    file_valid = len(file_payload["sessions"])

    if file_valid and file_rejected == 0:
        return file_payload, file_rejected, "status_files"

    management_rows = management_reader()
    management_payload, management_rejected = make_payload(
        management_rows, captured_at
    )
    management_valid = len(management_payload["sessions"])
    if management_valid > file_valid:
        return management_payload, management_rejected, "management_status3"
    return file_payload, file_rejected, "status_files"


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

    # Reproduce the live regression: status v1 parses TEN connected clients,
    # but none has Client ID or Virtual Address; status 3 has both.
    v1_many = "OpenVPN CLIENT LIST\n" + (
        "Common Name,Real Address,Bytes Received,Bytes Sent,Connected Since\n"
    ) + "".join(
        f"user_{i},198.51.100.{i}:4567,1000,200,Wed Oct  7 11:00:00 2026\n"
        for i in range(1, 11)
    ) + "ROUTING TABLE\n"
    v3_many = (
        "TITLE\tOpenVPN 2.7\n"
        "HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\n"
    ) + "".join(
        f"CLIENT_LIST\tuser_{i}\t198.51.100.{i}:4567\t10.8.0.{i}\t\t1000\t200\tignored\t1791367200\tuser_{i}\t{i}\t{i}\n"
        for i in range(1, 11)
    ) + "END\n"
    file_v1_rows = parse_status_text(v1_many, stamp)
    management_v3_rows = parse_status_text(v3_many, stamp)
    assert len(file_v1_rows) == len(management_v3_rows) == 10
    for row in file_v1_rows:
        row["status_file"] = "/etc/openvpn/openvpn-status.log"
    for row in management_v3_rows:
        row["status_file"] = "management:7505"

    rejected_v1_payload, rejected_count = make_payload(file_v1_rows, stamp)
    assert len(rejected_v1_payload["sessions"]) == 0 and rejected_count == 10

    from unittest.mock import patch
    fake_response = subprocess.CompletedProcess(
        args=["nc"], returncode=0, stdout=v3_many, stderr=""
    )
    with patch("subprocess.run", return_value=fake_response) as run_mock:
        mgmt_rows = read_management()
        assert "status 3" in run_mock.call_args.args[0][2]
        assert len(mgmt_rows) == 10
        assert all(row["status_file"] == "management:7505" for row in mgmt_rows)
    payload, quarantined, origin = choose_snapshot(
        file_v1_rows, lambda: mgmt_rows, stamp
    )
    assert (len(payload["sessions"]), quarantined, origin) == (
        10, 0, "management_status3"
    )
    assert all(
        row["counter_kind"] == "openvpn_session"
        and row["schema_version"] == 6
        and row["virtual_ip"] and row["client_id"]
        for row in payload["sessions"]
    )

    # Management unavailable: fail closed rather than billing v1 history.
    empty_payload, invalid, origin = choose_snapshot(
        file_v1_rows, lambda: [], stamp
    )
    assert len(empty_payload["sessions"]) == 0 and invalid == 10
    assert origin == "status_files"

    # Already valid v2/v3: never require Management availability.
    v2_row = dict(b[0], status_file="/etc/openvpn/openvpn-status.log")
    direct, invalid, origin = choose_snapshot(
        [v2_row], lambda: (_ for _ in ()).throw(
            AssertionError("unnecessary management query")
        ), stamp
    )
    assert len(direct["sessions"]) == 1 and invalid == 0
    assert origin == "status_files"

    # A mixed snapshot must not silently drop valid live users if status 3
    # can provide their complete verified records.
    mixed, invalid, origin = choose_snapshot(
        [v2_row, *file_v1_rows], lambda: mgmt_rows, stamp
    )
    assert len(mixed["sessions"]) == 10 and invalid == 0
    assert origin == "management_status3"
    import tempfile
    with tempfile.TemporaryDirectory() as d:
        path = Path(d) / "sessions.json"
        sample = dict(b[0], status_file="/etc/openvpn/openvpn-status.log")
        payload, rejected = make_payload([sample], stamp)
        assert rejected == 0
        path.write_text(json.dumps(payload))
        trusted, rows, rejected = read_snapshot(path)
        assert trusted and len(rows) == 1 and rejected == 0
        # A panel's lifetime totals must NEVER be accepted as session bytes.
        path.write_text(json.dumps([{"client_name": "alice",
                                     "current_total": 69790000000}]))
        assert read_snapshot(path)[0] is False
        payload["sessions"][0].pop("connected_since")
        path.write_text(json.dumps(payload))
        assert read_snapshot(path) == (True, [], 1)
    print("local_status_snapshot_self_test = PASS")


def main():
    payload, quarantined, origin = choose_snapshot(read_status_files())
    OUT.parent.mkdir(parents=True, exist_ok=True)
    tmp = OUT.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(payload, ensure_ascii=False, indent=2))
    os.replace(tmp, OUT)
    print("local_clients =", len(payload["sessions"]),
          "quarantined_unverifiable =", quarantined,
          "source =", origin)
    if origin == "status_files" and quarantined:
        print("LOCAL_ACCOUNTING_WARNING invalid status-file session identities; "
              "management status 3 unavailable or did not improve validity")
    for c in payload["sessions"]:
        # Avoid printing the user's public real_address in daemon logs.
        print(c["client_name"], c["current_total"],
              "has_connected_since=", bool(c["connected_since"]))


if __name__ == "__main__":
    if "--self-test" in sys.argv:
        self_test()
    else:
        main()
