#!/usr/bin/env python3
import json
import argparse
import hashlib
import time
from pathlib import Path
from datetime import datetime, timezone
from irangate_local_snapshot import read_snapshot, session_key, valid_session, SCHEMA_VERSION, COUNTER_KIND

ENV = Path("/etc/irangate-db.env")
LOCAL = Path("/opt/irangate/traffic/openvpn_session_stats.json")
NODE = Path("/opt/irangate/traffic/node_current_stats.json")
IKEV2 = Path("/opt/irangate/traffic/ikev2_current_stats.json")
GLOBAL_MIRROR = Path("/opt/irangate/traffic/global_usage_totals.json")
ACCOUNTING_V2_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v2.marker")
ACCOUNTING_V3_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v3.marker")
ACCOUNTING_V4_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v4.marker")
ACCOUNTING_V5_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v5.marker")
ACCOUNTING_V6_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v6.marker")


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


def load_fresh_node_json(path, max_age_seconds=90):
    """Do not bill an old node snapshot or treat its sessions as connected."""
    try:
        age = time.time() - path.stat().st_mtime
    except OSError:
        return []
    if age < -30 or age > max_age_seconds:
        print("NODE_SNAPSHOT_SKIPPED_STALE age_seconds=", round(age))
        return []
    return load_json(path)


def v5_should_baseline_local(is_migration, source, historical_local, existing_total):
    """Baseline every currently observed local connection on the v5 cutover.

    Existing OpenVPN session counters can reflect unbilled bytes, already-billed
    bytes, a manually reset account, or the same bytes under a former source ID.
    These possibilities cannot be distinguished reliably on first observation.
    In a live migration, skip the entire first sample for ALL local sessions,
    establish durable per-session baselines, and charge only subsequent deltas.
    The historical aggregate is never modified. Newly created sessions after
    the v5 marker is written follow the usual first-sample policy.
    """
    return bool(is_migration and source == "local")


def write_accounting_v5_marker():
    ACCOUNTING_V5_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V5_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(json.dumps({
        "version": 5,
        "activated_at": datetime.now(timezone.utc).isoformat(),
        "policy": "baseline existing local clients after source-key migration",
    }, ensure_ascii=False, indent=2) + "\n")
    tmp.replace(ACCOUNTING_V5_MARKER)


def write_accounting_v6_marker():
    ACCOUNTING_V6_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V6_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(json.dumps({
        "version": 6,
        "activated_at": datetime.now(timezone.utc).isoformat(),
        "policy": "strict v6 OpenVPN sessions; baseline unknown epochs; preserve historical totals",
    }, ensure_ascii=False, indent=2) + "\n")
    tmp.replace(ACCOUNTING_V6_MARKER)


def local_delta(old, rx, tx, migration=False):
    """Never bill first sightings, replays or counter resets as historical traffic."""
    if migration:
        return 0, 0, 0, "LOCAL_V6_MIGRATION_BASELINE_ONLY"
    if old is None:
        return 0, 0, 0, "LOCAL_FIRST_SAMPLE_BASELINE_ONLY"
    old_rx, old_tx = int(old[1] or 0), int(old[2] or 0)
    if rx < old_rx or tx < old_tx:
        return 0, 0, 0, "LOCAL_COUNTER_RESET_BASELINE_ONLY"
    drx, dtx = rx - old_rx, tx - old_tx
    return drx, dtx, drx + dtx, "LOCAL_ADD_DELTA" if drx + dtx else "LOCAL_NO_DELTA"


def to_int(v):
    try:
        return int(float(v or 0))
    except Exception:
        return 0


def pick(x, *keys):
    for k in keys:
        if k in x and x.get(k) not in (None, ""):
            return x.get(k)
    return None


def name_of(x):
    return pick(x, "client_name", "name", "common_name", "username")


def rx_of(x):
    return to_int(pick(x, "current_received", "bytes_received", "rx", "received"))


def tx_of(x):
    return to_int(pick(x, "current_sent", "bytes_sent", "tx", "sent"))


def total_of(x):
    total = to_int(pick(x, "current_total", "total_bytes", "total", "used_bytes"))
    if total <= 0:
        total = rx_of(x) + tx_of(x)
    return total


def bool_of(v, default=True):
    if v is None:
        return default
    if isinstance(v, bool):
        return v
    return str(v).strip().lower() in ("1", "true", "yes", "y", "connected", "active")


def connected_of(x):
    if "connected" in x:
        return bool_of(x.get("connected"), False)
    if "is_connected" in x:
        return bool_of(x.get("is_connected"), False)
    if "active" in x:
        return bool_of(x.get("active"), False)
    return True


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
    for k in (
        "session_id",
        "ike_id",
        "connected_since",
        "real_address",
        "virtual_ip",
        "virtual_address",
    ):
        v = x.get(k)
        if v:
            parts.append(f"{k}={v}")
    if not parts:
        parts.append("unknown_session")
    return "|".join(parts)


def peer_ip(address):
    """Normalize a remote peer address without its ephemeral NAT port."""
    from ipaddress import ip_address
    raw = str(address or "").strip()
    if not raw:
        return ""
    if raw.startswith("[") and "]" in raw:
        return raw[1:raw.index("]")]
    try:
        return str(ip_address(raw))
    except ValueError:
        pass
    host, sep, port = raw.rpartition(":")
    if sep and port.isdigit():
        return host
    return raw


def likely_replayed_local_counter(new_total, previous_total):
    """Guard a first large sample for the same client from the same peer IP."""
    return (
        new_total >= 1_000_000_000
        and previous_total >= 1_000_000_000
        and previous_total * 75 <= new_total * 100
        and new_total * 100 <= previous_total * 150
    )


def local_replay_baseline(cur, client_name, real_address, total):
    """Find a likely already-charged legacy snapshot; never rewrite history."""
    ip = peer_ip(real_address)
    if not ip or total < 1_000_000_000:
        return None
    cur.execute("""
        SELECT total_bytes, real_address
        FROM traffic_sources
        WHERE client_name = %s AND source_id LIKE 'local@%%'
          AND real_address IS NOT NULL
          AND updated_at > now() - interval '30 days'
        ORDER BY updated_at DESC LIMIT 1000
    """, (client_name,))
    candidates = [
        int(old_total or 0)
        for old_total, old_peer in cur.fetchall()
        if peer_ip(old_peer) == ip
        and likely_replayed_local_counter(total, int(old_total or 0))
    ]
    return max(candidates) if candidates else None


def counter_mode(source):
    source = str(source or "").strip().lower()
    # The separate, schema-validated OpenVPN file contains session counters.
    # IKEv2 is per-SA. Remote node snapshots are cumulative per node/CN.
    return "session" if source in ("local", "ikev2") else "cumulative"


def counter_epoch_key(source, x, session):
    """Return a durable accounting stream key for one counter epoch."""
    source = str(source or "").strip()
    if counter_mode(source) != "session":
        # Node counters are cumulative per node/client, so reconnects must keep
        # using the same durable source row.
        return source

    if source.lower() == "local":
        # Only the validated session-v6 envelope can yield a local stream id.
        return session_key(x)

    connected_since = str(x.get("connected_since") or "").strip()
    real_address = str(
        x.get("real_address") or x.get("remote_address") or ""
    ).strip()

    if source.lower() == "ikev2":
        # One IKE SA is one counter epoch. Prefer the strongSwan IKE id,
        # which is stable for the life of the SA and separates simultaneous
        # SAs even when connected_since timestamps fall in the same second.
        stable_session_id = str(
            x.get("session_id") or x.get("ike_id") or ""
        ).strip()
        seed = stable_session_id or connected_since or session or "unknown_session"
    else:
        # A NAT port is not a session identity. OpenVPN v2/v3 Client ID and
        # Connected Since identify a stable per-daemon connection; the status
        # file also scopes IDs across different local OpenVPN listeners.
        # For v1/missing time, fall back to public IP + VPN IP, never port.
        status_file = str(x.get("status_file") or "")
        vpn_ip = str(x.get("virtual_ip") or x.get("virtual_address") or "")
        cid = str(x.get("client_id") or x.get("peer_id") or "").strip()
        if connected_since:
            parts = ("local", status_file, connected_since,
                     cid or peer_ip(real_address), vpn_ip)
        else:
            parts = ("missing-connected-since", status_file,
                     peer_ip(real_address), vpn_ip)
        seed = "\x1f".join(parts) if any(parts) else session

    digest = hashlib.sha256(seed.encode("utf-8", "replace")).hexdigest()[:24]
    return f"{source}@{digest}"

def gb(n):
    return f"{int(n) / 1_000_000_000:.3f} GB"


def write_accounting_v2_marker():
    ACCOUNTING_V2_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V2_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(
        json.dumps(
            {
                "version": 2,
                "activated_at": datetime.now(timezone.utc).isoformat(),
                "policy": "local+node cumulative; ikev2 session epochs",
            },
            ensure_ascii=False,
            indent=2,
        ) + "\n"
    )
    tmp.replace(ACCOUNTING_V2_MARKER)



def write_accounting_v3_marker():
    ACCOUNTING_V3_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V3_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(
        json.dumps(
            {
                "version": 3,
                "activated_at": datetime.now(timezone.utc).isoformat(),
                "policy": "local+ikev2 session epochs; node cumulative; exact client identity",
            },
            ensure_ascii=False,
            indent=2,
        ) + "\n"
    )
    tmp.replace(ACCOUNTING_V3_MARKER)


def write_accounting_v4_marker():
    ACCOUNTING_V4_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V4_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(
        json.dumps(
            {
                "version": 4,
                "activated_at": datetime.now(timezone.utc).isoformat(),
                "policy": "ikev2 stable SA identity rollout baseline",
            },
            ensure_ascii=False,
            indent=2,
        ) + "\n"
    )
    tmp.replace(ACCOUNTING_V4_MARKER)


def self_test_accounting_identity():
    # Distinct OpenVPN Client IDs represent concurrent sessions, even with
    # identical peer IP and connection time. A NAT port alone does not.
    local_a = {
        "schema_version": SCHEMA_VERSION,
        "counter_kind": COUNTER_KIND,
        "client_name": "test",
        "connected": True,
        "current_received": 100,
        "current_sent": 25,
        "current_total": 125,
        "connected_since": "2026-09-15 10:00:00",
        "real_address": "1.2.3.4:10001",
        "client_id": "101",
        "status_file": "/etc/openvpn/openvpn-status.log",
    }
    local_b = dict(local_a, real_address="1.2.3.4:10002", client_id="102")
    assert counter_mode("local") == "session"
    assert counter_mode("ikev2") == "session"
    assert counter_mode("turkey") == "cumulative"
    key_a = counter_epoch_key("local", local_a, "a")
    key_b = counter_epoch_key("local", local_b, "b")
    assert key_a != key_b, (key_a, key_b)
    assert key_a == counter_epoch_key("local", dict(local_a), "replay")
    # Same OpenVPN session with a rebound NAT port retains one ledger key.
    assert key_a == counter_epoch_key(
        "local", dict(local_a, real_address="1.2.3.4:33333"), "port-rebind"
    )
    assert counter_epoch_key("turkey", local_a, "a") == "turkey"

    ike_a = {
        "ike_id": "101",
        "session_id": "ike:101",
        "connected_since": "2026-09-15T10:00:00Z",
    }
    ike_b = {
        "ike_id": "102",
        "session_id": "ike:102",
        "connected_since": "2026-09-15T10:00:00Z",
    }
    ike_key_a = counter_epoch_key("ikev2", ike_a, "same-fallback")
    ike_key_b = counter_epoch_key("ikev2", ike_b, "same-fallback")
    assert ike_key_a != ike_key_b, (ike_key_a, ike_key_b)
    assert ike_key_a == counter_epoch_key("ikev2", dict(ike_a), "replay")
    no_time_1 = dict(local_a, connected_since="")
    no_time_2 = dict(local_a, connected_since="", real_address="1.2.3.4:54321")
    assert not valid_session(no_time_1) and not valid_session(no_time_2)
    try:
        counter_epoch_key("local", no_time_1, "a")
        assert False, "missing timestamp must be quarantined"
    except ValueError:
        pass
    with_time_1 = dict(local_a, real_address="1.2.3.4:12345", virtual_ip="10.8.0.7",
                       connected_since="2026-10-07T11:00:00Z", client_id="10",
                       status_file="/etc/openvpn/status-a.log")
    with_time_2 = dict(with_time_1, real_address="1.2.3.4:55555")
    assert counter_epoch_key("local", with_time_1, "a") == counter_epoch_key("local", with_time_2, "b")
    with_time_3 = dict(with_time_1, client_id="11")
    assert counter_epoch_key("local", with_time_1, "a") != counter_epoch_key("local", with_time_3, "b")
    import tempfile
    assert peer_ip("[2001:db8::1]:54321") == "2001:db8::1"
    assert local_delta(None, 69_790_000_000, 0)[:3] == (0, 0, 0)
    assert local_delta(("s", 69_790_000_000, 0, 69_790_000_000),
                       69_790_000_000, 0)[:3] == (0, 0, 0)
    assert local_delta(("s", 69_790_000_000, 0, 69_790_000_000),
                       69_790_000_100, 20)[:3] == (100, 20, 120)
    assert local_delta(("s", 69_790_000_000, 0, 69_790_000_000),
                       20, 0)[:3] == (0, 0, 0)
    assert local_delta(("s", 1, 2, 3), 9_000_000_000, 2, True)[:3] == (0, 0, 0)
    with tempfile.TemporaryDirectory() as d:
        v6_path = Path(d) / "openvpn_session_stats.json"
        display_data = [{"client_name": "test", "current_total": 69_790_000_000}]
        v6_path.write_text(json.dumps(display_data))
        assert read_snapshot(v6_path)[0] is False
        envelope = {
            "schema_version": SCHEMA_VERSION, "counter_kind": COUNTER_KIND,
            "captured_at": "2026-10-30T10:00:00Z", "sessions": [local_a],
        }
        v6_path.write_text(json.dumps(envelope))
        ok, rows, bad = read_snapshot(v6_path)
        assert ok and len(rows) == 1 and bad == 0
        v6_path.write_text(json.dumps(envelope))
        assert read_snapshot(v6_path)[1] == rows  # tick replay/restart is idempotent
        envelope["sessions"][0].pop("connected_since")
        v6_path.write_text(json.dumps(envelope))
        assert read_snapshot(v6_path) == (True, [], 1)
    assert likely_replayed_local_counter(11_100_000_000, 11_090_000_000)
    assert not likely_replayed_local_counter(30_000, 11_090_000_000)
    assert not likely_replayed_local_counter(50_000_000_000, 11_090_000_000)
    assert v5_should_baseline_local(True, "local", True, 11_000_000_000)
    assert not v5_should_baseline_local(False, "local", True, 11_000_000_000)
    assert v5_should_baseline_local(True, "local", False, 11_000_000_000)
    assert not v5_should_baseline_local(True, "ikev2", True, 11_000_000_000)
    assert v5_should_baseline_local(True, "local", True, 0)
    assert v5_should_baseline_local(True, "local", False, 0)
    assert not v5_should_baseline_local(False, "local", False, 0)
    import os
    import tempfile
    with tempfile.TemporaryDirectory() as dirname:
        p = Path(dirname) / "nodes.json"
        p.write_text('[{"client_name": "test"}]')
        then = time.time()
        os.utime(p, (then - 604800, then - 604800))
        assert load_fresh_node_json(p) == []
        os.utime(p, (then, then))
        assert len(load_fresh_node_json(p)) == 1
    print("accounting_identity_self_test = PASS")

ap = argparse.ArgumentParser()
ap.add_argument("--apply", action="store_true")
ap.add_argument("--verbose", action="store_true")
ap.add_argument("--self-test", action="store_true")
args = ap.parse_args()

if args.self_test:
    self_test_accounting_identity()
    raise SystemExit(0)

import psycopg2  # production-only dependency; self-test needs no live DB

items = []

local_snapshot_valid, local_rows, local_quarantined = read_snapshot(LOCAL)
if not local_snapshot_valid:
    print("ACCOUNTING_WARNING local_session_v6_snapshot_missing_stale_or_invalid: local billing disabled")
if local_quarantined:
    print("ACCOUNTING_WARNING local_sessions_quarantined=", local_quarantined)
for x in local_rows:
    x["_source_id"] = "local"
    items.append(x)

for x in load_fresh_node_json(NODE):
    x["_source_id"] = str(
        pick(x, "source_id", "node_id", "node_name", "node") or "node"
    )
    items.append(x)

for x in load_json(IKEV2):
    x["_source_id"] = str(pick(x, "source_id") or "ikev2")
    items.append(x)

env = load_env()
conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

migration_mode = not ACCOUNTING_V2_MARKER.exists()
local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()
local_v3_legacy_source_present = False
ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
local_v5_migration = not ACCOUNTING_V5_MARKER.exists()
local_v6_migration = not ACCOUNTING_V6_MARKER.exists()
ike_v4_legacy_sources_present = False
processed = 0
first_seen = 0
first_sample_added = 0
migration_baselines = 0
session_reset = 0
counter_reset = 0
delta_count = 0
added_total = 0
local_added_total = 0
active_count = 0

with conn.cursor() as cur:
    if local_session_v3_migration:
        cur.execute("""
            SELECT EXISTS (
                SELECT 1
                FROM traffic_sources
                WHERE source_id = 'local'
            )
        """)
        row = cur.fetchone()
        local_v3_legacy_source_present = bool(row and row[0])

    if ike_session_v4_migration:
        cur.execute("""
            SELECT EXISTS (
                SELECT 1
                FROM traffic_sources
                WHERE source_id LIKE 'ikev2@%'
            )
        """)
        row = cur.fetchone()
        ike_v4_legacy_sources_present = bool(row and row[0])

    cur.execute("DELETE FROM active_sessions WHERE last_seen < now() - interval '90 seconds'")

    # Full snapshot sync: remove old active sessions for sources seen in this tick.
    # This prevents reconnect/port-change stale sessions from creating false
    # connection-limit violations. Accounting source ids are intentionally
    # separate from these runtime source ids for IKEv2 session epochs.
    snapshot_sources = sorted(
        set(["local"])
        | {str(x.get("_source_id")) for x in items if x.get("_source_id")}
    )
    for snapshot_source in snapshot_sources:
        cur.execute(
            "DELETE FROM active_sessions WHERE source_id = %s",
            (snapshot_source,),
        )

    # Session-scoped IKEv2 counter rows are durable for restart/replay
    # idempotency, but old epochs do not need to live forever.
    cur.execute("""
        DELETE FROM traffic_sources
        WHERE (source_id LIKE 'ikev2@%' OR source_id LIKE 'local@%')
          AND updated_at < now() - interval '30 days'
    """)

    for x in items:
        raw_name = name_of(x)
        name = str(raw_name or "").strip()

        if not name or name.upper() == "UNDEF":
            if args.verbose:
                print(
                    "SKIP_INVALID_CLIENT",
                    "source=",
                    x.get("_source_id"),
                    "name=",
                    repr(name),
                )
            continue

        source = str(x["_source_id"])
        mode = counter_mode(source)
        rx = max(0, rx_of(x))
        tx = max(0, tx_of(x))
        total = max(0, total_of(x))
        connected = connected_of(x)
        session = session_of(x)
        accounting_source = counter_epoch_key(source, x, session)
        if source == "local":
            # NAT port changes must not create duplicate active sessions.
            session = accounting_source
        real_address = pick(x, "real_address", "remote_address")
        virtual_ip = pick(x, "virtual_ip", "virtual_address")
        last_seen = parse_time(pick(x, "last_seen", "updated_at"))
        connected_since = parse_time(pick(x, "connected_since", "last_connection"))

        processed += 1
        if connected:
            active_count += 1

        cur.execute("""
            INSERT INTO clients (client_name, display_name, updated_at)
            VALUES (%s,%s,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name, name))

        cur.execute("""
            INSERT INTO traffic_totals (client_name, rx_bytes, tx_bytes, total_bytes, updated_at)
            VALUES (%s,0,0,0,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name,))

        cur.execute("""
            UPDATE clients
            SET last_seen = COALESCE(%s, last_seen), updated_at = now()
            WHERE client_name = %s
        """, (last_seen, name))

        cur.execute("""
            SELECT rx_bytes, tx_bytes, total_bytes
            FROM traffic_totals
            WHERE client_name = %s
        """, (name,))
        aggregate_row = cur.fetchone() or (0, 0, 0)
        aggregate_total_before = int(aggregate_row[2] or 0)

        cur.execute("""
            SELECT session_fingerprint, rx_bytes, tx_bytes, total_bytes
            FROM traffic_sources
            WHERE source_id = %s AND client_name = %s
            FOR UPDATE
        """, (accounting_source, name))

        old = cur.fetchone()

        drx = dtx = dtotal = 0
        action = "baseline"

        def add_usage_delta(action_name, add_rx, add_tx, add_total):
            global delta_count, added_total, local_added_total
            if add_total <= 0:
                return action_name + "_NO_BYTES"

            delta_count += 1
            added_total += add_total
            if source == "local":
                local_added_total += add_total

            cur.execute("""
                UPDATE traffic_totals
                SET rx_bytes = rx_bytes + %s,
                    tx_bytes = tx_bytes + %s,
                    total_bytes = total_bytes + %s,
                    updated_at = now()
                WHERE client_name = %s
            """, (
                max(0, add_rx),
                max(0, add_tx),
                max(0, add_total),
                name,
            ))

            return action_name

        if source == "local":
            if old is None:
                first_seen += 1
            drx, dtx, dtotal, action = local_delta(
                old, rx, tx, migration=local_v6_migration
            )
            if local_v6_migration:
                migration_baselines += 1
            if action == "LOCAL_COUNTER_RESET_BASELINE_ONLY":
                counter_reset += 1
            if dtotal:
                action = add_usage_delta(action, drx, dtx, dtotal)

        elif old is None:
            first_seen += 1
            local_v5_replay = False
            if source == "local" and local_v5_migration:
                # A formerly billed local session obtains a new stream ID
                # when upgrading from the old port-based epoch identity.
                # Do not charge the same full counter at the migration boundary.
                cur.execute("""
                    SELECT EXISTS (
                        SELECT 1 FROM traffic_sources
                        WHERE client_name = %s
                          AND (source_id LIKE 'local@%%' OR source_id = 'local')
                          AND source_id <> %s
                    )
                """, (name, accounting_source))
                local_v5_replay = bool(cur.fetchone()[0])

            # At this one-time v5 deployment, ALL active local streams are
            # ambiguous: old source identity changes, prior quota resets, and
            # long-paused timers can make their first counters replay traffic.
            # Baseline every new local stream now; charge subsequent deltas.
            # Post-migration sessions follow normal accounting policy.
            if source == "ikev2" and ike_session_v4_migration and ike_v4_legacy_sources_present:
                # v3 keyed IKEv2 epochs by connected_since. v4 prefers the
                # stable strongSwan SA identity. Baseline active SAs once so
                # changing key formats cannot charge the same bytes twice.
                migration_baselines += 1
                action = "FIRST_IKEV2_V4_BASELINE_ONLY"
            elif source == "local" and local_session_v3_migration and local_v3_legacy_source_present:
                # v2 stored every simultaneous local session in source_id=local.
                # Baseline each live local session exactly once during v3
                # rollout so already-accounted bytes are not charged again.
                migration_baselines += 1
                action = "FIRST_LOCAL_V3_BASELINE_ONLY"
            elif v5_should_baseline_local(
                local_v5_migration, source, local_v5_replay, aggregate_total_before
            ):
                migration_baselines += 1
                action = "FIRST_LOCAL_V5_BASELINE_ONLY"
            elif migration_mode and aggregate_total_before > 0:
                migration_baselines += 1
                action = "FIRST_MIGRATION_BASELINE_ONLY"
            else:
                prior = (
                    local_replay_baseline(cur, name, real_address, total)
                    if source == "local" else None
                )
                if prior is not None:
                    # Existing totals are historical records and stay intact.
                    # Charge only the unambiguously increasing portion.
                    dtotal = max(0, total - prior)
                    if total > 0:
                        drx = (rx * dtotal) // total
                        dtx = dtotal - drx
                    action = add_usage_delta(
                        "SUSPICIOUS_LOCAL_REPLAY_DELTA_ONLY", drx, dtx, dtotal
                    )
                    print(
                        "ACCOUNTING_WARNING likely_duplicate_local_snapshot",
                        "name=", name, "prior=", prior, "current=", total,
                        "charged=", dtotal,
                    )
                else:
                    drx = rx
                    dtx = tx
                    dtotal = total
                    action = add_usage_delta(
                        "FIRST_SAMPLE_ADD", drx, dtx, dtotal,
                    )
                    if dtotal > 0:
                        first_sample_added += 1

        else:
            old_session, old_rx, old_tx, old_total = old

            if old_session != session:
                session_reset += 1

            # local and node inputs are cumulative counters. IKEv2 counters are
            # per-SA, but each SA has its own accounting_source epoch key, so the
            # same monotonic/reset math is safe for every persisted stream.
            if total >= old_total:
                drx = max(0, rx - old_rx)
                dtx = max(0, tx - old_tx)
                dtotal = total - old_total

                if dtotal > 0:
                    action = add_usage_delta(
                        "ADD_DELTA",
                        drx,
                        dtx,
                        dtotal,
                    )
                else:
                    action = "NO_DELTA"
            else:
                # Counter decreased inside the same durable source stream.
                # Treat this as a new counter epoch and charge the new epoch's
                # current value once. The persisted source row makes replay and
                # service restart idempotent on the next tick.
                counter_reset += 1
                drx = rx
                dtx = tx
                dtotal = total
                action = add_usage_delta(
                    "COUNTER_RESET_ADD_NEW_EPOCH",
                    drx,
                    dtx,
                    dtotal,
                )

        cur.execute("""
            INSERT INTO traffic_sources (
                source_id, client_name, session_fingerprint,
                rx_bytes, tx_bytes, total_bytes,
                connected, real_address, virtual_ip, updated_at
            )
            VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,now())
            ON CONFLICT (source_id, client_name) DO UPDATE SET
                session_fingerprint = EXCLUDED.session_fingerprint,
                rx_bytes = EXCLUDED.rx_bytes,
                tx_bytes = EXCLUDED.tx_bytes,
                total_bytes = EXCLUDED.total_bytes,
                connected = EXCLUDED.connected,
                real_address = EXCLUDED.real_address,
                virtual_ip = EXCLUDED.virtual_ip,
                updated_at = now()
        """, (
            accounting_source, name, session, rx, tx, total,
            connected, real_address, virtual_ip,
        ))

        if connected:
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
            """, (
                source,
                name,
                session,
                real_address,
                virtual_ip,
                connected_since,
            ))
        else:
            cur.execute("""
                DELETE FROM active_sessions
                WHERE source_id = %s AND client_name = %s
            """, (source, name))

        if args.verbose:
            print(
                action,
                "source=",
                source,
                "accounting_source=",
                accounting_source,
                "mode=",
                mode,
                "name=",
                name,
                "delta=",
                dtotal,
                "total=",
                total,
            )

# Accounting epochs remain durable, but a long-gone session is not connected.
# Never delete its already-booked usage or its deduplication ledger here.
with conn.cursor() as cur:
    cur.execute("""
        UPDATE traffic_sources SET connected = false
        WHERE connected = true
          AND (source_id LIKE 'local@%' OR source_id LIKE 'ikev2@%')
          AND updated_at < now() - interval '90 seconds'
    """)

if args.apply:
    # Strict fail-closed migration: first v5 activation on a populated Master
    # must NOT increase anyone's historical usage, even if a node/SA suddenly
    # reports a fresh counter between dry-run and the actual commit.
    # The whole transaction is rolled back before any DB write is committed.
    if local_v6_migration and local_added_total != 0:
        conn.rollback()
        conn.close()
        raise SystemExit(
            "V6_MIGRATION_REFUSED_LOCAL_DELTA bytes=" + str(local_added_total)
        )
    if local_v5_migration and added_total != 0:
        conn.rollback()
        conn.close()
        raise SystemExit(
            "V5_MIGRATION_REFUSED_NONZERO_DELTA bytes=" + str(added_total)
        )
    conn.commit()

    if migration_mode:
        write_accounting_v2_marker()

    if local_session_v3_migration:
        write_accounting_v3_marker()

    if ike_session_v4_migration:
        write_accounting_v4_marker()

    if local_v5_migration and any(x.get("_source_id") == "local" for x in items):
        write_accounting_v5_marker()
    if local_v6_migration and local_snapshot_valid:
        write_accounting_v6_marker()

    try:
        mirror = {"last": {}, "totals": {}, "blocked": {}}

        with conn.cursor() as cur:
            cur.execute("""
                SELECT client_name, rx_bytes, tx_bytes, total_bytes
                FROM traffic_totals
                ORDER BY client_name
            """)
            for name, rx, tx, total in cur.fetchall():
                mirror["totals"][name] = {
                    "rx": int(rx or 0),
                    "tx": int(tx or 0),
                    "total": int(total or 0),
                }

            cur.execute("""
                SELECT source_id, client_name, session_fingerprint,
                       rx_bytes, tx_bytes, total_bytes
                FROM traffic_sources
                ORDER BY source_id, client_name
            """)
            for source, name, session, rx, tx, total in cur.fetchall():
                mirror["last"][f"{source}:{name}"] = {
                    "rx": int(rx or 0),
                    "tx": int(tx or 0),
                    "total": int(total or 0),
                    "session": session or "",
                }

            cur.execute("""
                SELECT client_name, reason, details
                FROM quota_blocks
                WHERE blocked = true
                ORDER BY client_name
            """)
            for name, reason, details in cur.fetchall():
                if isinstance(details, dict):
                    item = details
                else:
                    item = {}
                item["reason"] = reason
                mirror["blocked"][name] = item

        tmp = GLOBAL_MIRROR.with_suffix(".json.tmp")
        tmp.write_text(json.dumps(mirror, ensure_ascii=False, indent=2))
        tmp.replace(GLOBAL_MIRROR)

    except Exception as e:
        print("MIRROR_FAIL", e)

else:
    conn.rollback()

conn.close()

print("mode =", "APPLY" if args.apply else "DRY-RUN")
print("accounting_v2_migration_mode =", migration_mode)
print("accounting_v3_local_migration_mode =", local_session_v3_migration)
print("accounting_v3_legacy_local_source =", local_v3_legacy_source_present)
print("accounting_v4_ikev2_migration_mode =", ike_session_v4_migration)
print("accounting_v5_local_migration_mode =", local_v5_migration)
print("accounting_v6_local_migration_mode =", local_v6_migration)
print("accounting_v6_valid_local_snapshot =", local_snapshot_valid)
print("accounting_v6_local_added_bytes =", local_added_total)
print("accounting_v4_legacy_ikev2_sources =", ike_v4_legacy_sources_present)
print("processed =", processed)
print("active =", active_count)
print("first_seen =", first_seen)
print("first_sample_added =", first_sample_added)
print("migration_baselines =", migration_baselines)
print("session_fingerprint_changes =", session_reset)
print("counter_reset =", counter_reset)
print("delta_count =", delta_count)
print("added_total_bytes =", added_total)
print("added_total_human =", gb(added_total))
