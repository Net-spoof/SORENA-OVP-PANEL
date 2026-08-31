#!/usr/bin/env python3
from __future__ import annotations

import json
from pathlib import Path

import psycopg2

ENV = Path("/etc/irangate-db.env")
CURRENT = Path("/opt/irangate/traffic/l2tp_current_stats.json")
SESSIONS = Path("/opt/irangate/traffic/l2tp_sessions.json")
SOURCE_ID = "l2tp"


def load_env() -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in ENV.read_text(encoding="utf-8").splitlines():
        raw = raw.strip()
        if not raw or raw.startswith("#") or "=" not in raw:
            continue
        key, value = raw.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def load_array(path: Path) -> list[dict]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return []
    if not isinstance(data, list):
        return []
    return [item for item in data if isinstance(item, dict)]


def non_negative_int(value) -> int:
    try:
        return max(int(value or 0), 0)
    except Exception:
        return 0


def text(value) -> str:
    return str(value or "").strip()


def counter_delta(current: int, previous: int) -> int:
    # Reconnects are already converted to cumulative per-user counters by the
    # snapshotter. If its state file is ever reset/recovered and a counter
    # moves backwards, treat the current value as a fresh monotonic baseline
    # instead of producing a negative quota adjustment.
    if current >= previous:
        return current - previous
    return current


def main() -> int:
    env = load_env()
    dsn = env.get("IRANGATE_DB_DSN", "")
    if not dsn:
        raise SystemExit("IRANGATE_DB_DSN missing")

    aggregate = load_array(CURRENT)
    sessions = load_array(SESSIONS)

    conn = psycopg2.connect(dsn, application_name="irangate-pg-l2tp-sync")
    conn.autocommit = False

    charged_total = 0
    accepted_users = 0
    accepted_sessions = 0

    try:
        with conn.cursor() as cur:
            cur.execute("SELECT pg_advisory_xact_lock(hashtext('irangate-l2tp-sync'))")

            cur.execute("SELECT client_name FROM clients")
            known_clients = {str(row[0]) for row in cur.fetchall()}

            for item in aggregate:
                name = text(item.get("client_name"))
                if not name or name not in known_clients:
                    continue

                rx = non_negative_int(item.get("current_received"))
                tx = non_negative_int(item.get("current_sent"))
                total = non_negative_int(item.get("current_total"))
                if total <= 0:
                    total = rx + tx

                cur.execute(
                    """
                    SELECT rx_bytes, tx_bytes, total_bytes
                    FROM traffic_sources
                    WHERE source_id = %s AND client_name = %s
                    FOR UPDATE
                    """,
                    (SOURCE_ID, name),
                )
                old = cur.fetchone()
                old_rx, old_tx, old_total = (0, 0, 0) if old is None else map(int, old)

                delta_rx = counter_delta(rx, old_rx)
                delta_tx = counter_delta(tx, old_tx)
                delta_total = counter_delta(total, old_total)

                # Prefer the explicit cumulative total. If it is absent or
                # inconsistent, never charge less than the sum of RX/TX delta.
                component_delta = delta_rx + delta_tx
                if delta_total < component_delta:
                    delta_total = component_delta

                cur.execute(
                    """
                    INSERT INTO traffic_sources (
                        source_id, client_name, session_fingerprint,
                        rx_bytes, tx_bytes, total_bytes, connected,
                        real_address, virtual_ip, updated_at
                    )
                    VALUES (%s,%s,'',%s,%s,%s,false,NULL,NULL,now())
                    ON CONFLICT (source_id, client_name) DO UPDATE SET
                        session_fingerprint = '',
                        rx_bytes = EXCLUDED.rx_bytes,
                        tx_bytes = EXCLUDED.tx_bytes,
                        total_bytes = EXCLUDED.total_bytes,
                        connected = false,
                        real_address = NULL,
                        virtual_ip = NULL,
                        updated_at = now()
                    """,
                    (SOURCE_ID, name, rx, tx, total),
                )

                cur.execute(
                    """
                    INSERT INTO traffic_totals (
                        client_name, rx_bytes, tx_bytes, total_bytes, updated_at
                    )
                    VALUES (%s,%s,%s,%s,now())
                    ON CONFLICT (client_name) DO UPDATE SET
                        rx_bytes = traffic_totals.rx_bytes + EXCLUDED.rx_bytes,
                        tx_bytes = traffic_totals.tx_bytes + EXCLUDED.tx_bytes,
                        total_bytes = traffic_totals.total_bytes + EXCLUDED.total_bytes,
                        updated_at = now()
                    """,
                    (name, delta_rx, delta_tx, delta_total),
                )

                charged_total += delta_total
                accepted_users += 1

            # A dedicated session snapshot is used because traffic_sources has
            # a (source_id, client_name) primary key while one L2TP username may
            # legitimately have multiple concurrent PPP sessions.
            cur.execute(
                "DELETE FROM active_sessions WHERE source_id = %s",
                (SOURCE_ID,),
            )

            active_names: set[str] = set()
            for item in sessions:
                name = text(item.get("client_name"))
                fingerprint = text(item.get("session_fingerprint"))
                if (
                    not name
                    or name not in known_clients
                    or not fingerprint.startswith(f"l2tp:{name}:")
                ):
                    continue

                cur.execute(
                    """
                    INSERT INTO active_sessions (
                        source_id, client_name, session_fingerprint,
                        real_address, virtual_ip, connected_since, last_seen
                    )
                    VALUES (%s,%s,%s,%s,%s,%s,now())
                    ON CONFLICT (source_id, client_name, session_fingerprint)
                    DO UPDATE SET
                        real_address = EXCLUDED.real_address,
                        virtual_ip = EXCLUDED.virtual_ip,
                        connected_since = COALESCE(
                            active_sessions.connected_since,
                            EXCLUDED.connected_since
                        ),
                        last_seen = now()
                    """,
                    (
                        SOURCE_ID,
                        name,
                        fingerprint,
                        text(item.get("real_address")) or None,
                        text(item.get("virtual_ip")) or None,
                        text(item.get("connected_since")) or None,
                    ),
                )
                active_names.add(name)
                accepted_sessions += 1

            for name in active_names:
                cur.execute(
                    """
                    UPDATE clients
                    SET last_seen = now(),
                        last_connection = now(),
                        status = CASE
                            WHEN is_enabled AND NOT is_blocked THEN 'active'
                            ELSE status
                        END,
                        updated_at = now()
                    WHERE client_name = %s
                    """,
                    (name,),
                )

        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()

    print(
        f"L2TP PG sync users={accepted_users} "
        f"sessions={accepted_sessions} charged_bytes={charged_total}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
