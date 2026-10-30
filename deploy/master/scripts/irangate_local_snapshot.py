#!/usr/bin/env python3
"""Strict v6 contract for *OpenVPN session* counters, never panel totals.

A missing/stale/legacy/cumulative file is not a billable local snapshot.
"""
import hashlib
import ipaddress
import json
import time
from datetime import datetime
from pathlib import Path

SCHEMA_VERSION = 6
COUNTER_KIND = "openvpn_session"
SESSION_FILE = Path("/opt/irangate/traffic/openvpn_session_stats.json")


def peer_ip(value):
    raw = str(value or "").strip()
    if raw.startswith("[") and "]" in raw:
        raw = raw[1:raw.index("]")]
    else:
        try:
            ipaddress.ip_address(raw)
        except ValueError:
            host, separator, port = raw.rpartition(":")
            if separator and port.isdigit():
                raw = host
    try:
        return str(ipaddress.ip_address(raw))
    except ValueError:
        return ""


def has_valid_connection_time(value):
    raw = str(value or "").strip()
    if not raw:
        return False
    try:
        parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
        return parsed.year >= 2000
    except ValueError:
        pass
    try:
        return datetime.strptime(raw, "%a %b %d %H:%M:%S %Y").year >= 2000
    except ValueError:
        return False


def valid_session(row):
    if not isinstance(row, dict):
        return False
    if row.get("schema_version") != SCHEMA_VERSION or row.get("counter_kind") != COUNTER_KIND:
        return False
    name = str(row.get("client_name") or "").strip()
    if not name or name.upper() == "UNDEF" or row.get("connected") is not True:
        return False
    if not str(row.get("status_file") or "").strip():
        return False
    if not has_valid_connection_time(row.get("connected_since")):
        return False
    remote = peer_ip(row.get("real_address"))
    if not remote:
        return False
    client_id = str(row.get("client_id") or row.get("peer_id") or "").strip()
    virtual_ip = str(row.get("virtual_ip") or "").strip()
    # A legacy v1 row without a per-connection id AND VPN address is ambiguous.
    if not client_id and not virtual_ip:
        return False
    for key in ("current_received", "current_sent", "current_total"):
        value = row.get(key)
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            return False
    return row["current_total"] == row["current_received"] + row["current_sent"]


def session_key(row):
    if not valid_session(row):
        raise ValueError("Unverified OpenVPN session identity; refusing billing")
    remote = peer_ip(row["real_address"])
    client_id = str(row.get("client_id") or row.get("peer_id") or "").strip()
    seed = "\x1f".join((
        "local",
        str(row["status_file"]).strip(),
        str(row["connected_since"]).strip(),
        client_id or remote,
        str(row.get("virtual_ip") or "").strip(),
    ))
    return "local@" + hashlib.sha256(seed.encode("utf-8", "replace")).hexdigest()[:24]


def make_payload(rows, captured_at):
    accepted = []
    quarantined = 0
    for item in rows:
        row = dict(item, schema_version=SCHEMA_VERSION, counter_kind=COUNTER_KIND)
        if valid_session(row):
            accepted.append(row)
        else:
            quarantined += 1
    return {
        "schema_version": SCHEMA_VERSION,
        "counter_kind": COUNTER_KIND,
        "captured_at": captured_at,
        "sessions": accepted,
    }, quarantined


def read_snapshot(path=SESSION_FILE, max_age_seconds=90):
    """Return (trusted_envelope, valid_rows, quarantined_rows). Fail closed."""
    try:
        age = time.time() - path.stat().st_mtime
        if age < -30 or age > max_age_seconds:
            return False, [], 0
        payload = json.loads(path.read_text())
    except (OSError, ValueError, UnicodeError):
        return False, [], 0
    if not isinstance(payload, dict):
        return False, [], 0
    if (payload.get("schema_version") != SCHEMA_VERSION
            or payload.get("counter_kind") != COUNTER_KIND
            or not isinstance(payload.get("sessions"), list)):
        return False, [], 0
    seen = set()
    accepted = []
    quarantined = 0
    for row in payload["sessions"]:
        if not valid_session(row):
            quarantined += 1
            continue
        key = (row["client_name"], session_key(row))
        if key in seen:
            quarantined += 1
            continue
        seen.add(key)
        accepted.append(row)
    return True, accepted, quarantined
