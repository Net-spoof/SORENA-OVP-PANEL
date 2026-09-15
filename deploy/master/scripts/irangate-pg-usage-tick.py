#!/usr/bin/env python3
import json
import argparse
import hashlib
from pathlib import Path
from datetime import datetime, timezone
import psycopg2

ENV = Path("/etc/irangate-db.env")
LOCAL = Path("/opt/irangate/traffic/current_stats.json")
NODE = Path("/opt/irangate/traffic/node_current_stats.json")
IKEV2 = Path("/opt/irangate/traffic/ikev2_current_stats.json")
GLOBAL_MIRROR = Path("/opt/irangate/traffic/global_usage_totals.json")
ACCOUNTING_V2_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v2.marker")
ACCOUNTING_V3_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v3.marker")
ACCOUNTING_V4_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v4.marker")


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


def counter_mode(source):
    source = str(source or "").strip().lower()
    # current_stats.json contains one row per local OpenVPN session.
    # IKEv2 is per-SA. Remote node snapshots are cumulative per node/CN.
    return "session" if source in ("local", "ikev2") else "cumulative"


def counter_epoch_key(source, x, session):
    """Return a durable accounting stream key for one counter epoch."""
    source = str(source or "").strip()
    if counter_mode(source) != "session":
        # Node counters are cumulative per node/client, so reconnects must keep
        # using the same durable source row.
        return source

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
        # OpenVPN has one row per live session. connected_since + peer address
        # separates simultaneous sessions of the same CN and remains stable on
        # replay of the same status snapshot.
        parts = (connected_since, real_address)
        seed = "\x1f".join(parts) if any(parts) else session

    digest = hashlib.sha256(seed.encode("utf-8", "replace")).hexdigest()[:24]
    return f"{source}@{digest}"

def gb(n):
    return f"{int(n) / 1024 / 1024 / 1024:.3f} GB"


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
    local_a = {
        "connected_since": "2026-09-15 10:00:00",
        "real_address": "1.2.3.4:10001",
    }
    local_b = {
        "connected_since": "2026-09-15 10:00:00",
        "real_address": "1.2.3.4:10002",
    }
    assert counter_mode("local") == "session"
    assert counter_mode("ikev2") == "session"
    assert counter_mode("turkey") == "cumulative"
    key_a = counter_epoch_key("local", local_a, "a")
    key_b = counter_epoch_key("local", local_b, "b")
    assert key_a != key_b, (key_a, key_b)
    assert key_a == counter_epoch_key("local", dict(local_a), "replay")
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
    print("accounting_identity_self_test = PASS")

ap = argparse.ArgumentParser()
ap.add_argument("--apply", action="store_true")
ap.add_argument("--verbose", action="store_true")
ap.add_argument("--self-test", action="store_true")
args = ap.parse_args()

if args.self_test:
    self_test_accounting_identity()
    raise SystemExit(0)

items = []

for x in load_json(LOCAL):
    x["_source_id"] = "local"
    items.append(x)

for x in load_json(NODE):
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
ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
processed = 0
first_seen = 0
first_sample_added = 0
migration_baselines = 0
session_reset = 0
counter_reset = 0
delta_count = 0
added_total = 0
active_count = 0

with conn.cursor() as cur:
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
            global delta_count, added_total
            if add_total <= 0:
                return action_name + "_NO_BYTES"

            delta_count += 1
            added_total += add_total

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

        if old is None:
            first_seen += 1

            # Rollout safety: an upgraded database may already contain an
            # imported aggregate total while having no per-source ledger row.
            # Baseline only that ambiguous first observation. Fresh clients
            # (aggregate=0) and all future sources after the marker is created
            # charge their first sample exactly once.
            if source == "ikev2" and ike_session_v4_migration:
                # v3 keyed IKEv2 epochs by connected_since. v4 prefers the
                # stable strongSwan SA identity. Baseline active SAs once so
                # changing key formats cannot charge the same bytes twice.
                migration_baselines += 1
                action = "FIRST_IKEV2_V4_BASELINE_ONLY"
            elif source == "local" and local_session_v3_migration:
                # v2 stored every simultaneous local session in source_id=local.
                # Baseline each live local session exactly once during v3
                # rollout so already-accounted bytes are not charged again.
                migration_baselines += 1
                action = "FIRST_LOCAL_V3_BASELINE_ONLY"
            elif migration_mode and aggregate_total_before > 0:
                migration_baselines += 1
                action = "FIRST_MIGRATION_BASELINE_ONLY"
            else:
                drx = rx
                dtx = tx
                dtotal = total
                action = add_usage_delta(
                    "FIRST_SAMPLE_ADD",
                    drx,
                    dtx,
                    dtotal,
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

if args.apply:
    conn.commit()

    if migration_mode:
        write_accounting_v2_marker()

    if local_session_v3_migration:
        write_accounting_v3_marker()

    if ike_session_v4_migration:
        write_accounting_v4_marker()

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
print("accounting_v4_ikev2_migration_mode =", ike_session_v4_migration)
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
