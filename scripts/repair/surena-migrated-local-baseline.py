#!/usr/bin/env python3
"""One-time migrated-master baseline; NEVER changes traffic_totals or quotas.

Uses the live accounting engine's own identity/counter functions (via AST),
so its stream keys exactly match the next production usage tick.
"""
from __future__ import annotations

import argparse
import ast
import hashlib
import json
import os
import sys
import time
from pathlib import Path

TICK = Path("/usr/local/bin/irangate-pg-usage-tick.py")
SNAPSHOT = Path("/opt/irangate/traffic/current_stats.json")
DB_ENV = Path("/etc/irangate-db.env")
NAMES = ("to_int", "pick", "name_of", "rx_of", "tx_of", "total_of",
         "connected_of", "bool_of", "session_of", "peer_ip",
         "counter_mode", "counter_epoch_key")


def tick_functions(path: Path) -> dict:
    source = path.read_text()
    parsed = ast.parse(source, filename=str(path))
    selected = [n for n in parsed.body
                if isinstance(n, ast.FunctionDef) and n.name in NAMES]
    found = {n.name for n in selected}
    if found != set(NAMES):
        raise RuntimeError("Accounting functions have changed; refuse to guess: "
                           + ", ".join(sorted(set(NAMES)-found)))
    env = {"hashlib": hashlib}
    exec(compile(ast.Module(body=selected, type_ignores=[]),
                 str(path), "exec"), env)
    return env


def dsn_from_env(path: Path) -> str:
    for line in path.read_text().splitlines():
        if line.startswith("IRANGATE_DB_DSN="):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    raise RuntimeError("Database DSN missing")


def candidates(raw: list, fn: dict) -> list:
    if len(raw) > 1000:
        raise RuntimeError("Too many local snapshot rows; abort")
    result = []
    seen = set()
    for row in raw:
        if not isinstance(row, dict):
            raise RuntimeError("Malformed local OpenVPN snapshot")
        name = str(fn["name_of"](row) or "").strip()
        if not name or name.upper() == "UNDEF" or not fn["connected_of"](row):
            continue
        rx = max(0, fn["rx_of"](row))
        tx = max(0, fn["tx_of"](row))
        total = max(0, fn["total_of"](row))
        source = fn["counter_epoch_key"]("local", row, fn["session_of"](row))
        if not source.startswith("local@") or len(source) != len("local@") + 24:
            raise RuntimeError("Unexpected local session identity")
        if (source, name) in seen:
            raise RuntimeError("Duplicate live stream in snapshot")
        seen.add((source, name))
        result.append((source, name, fn["session_of"](row), rx, tx, total,
                       row.get("real_address"), row.get("virtual_ip")))
    return result


def self_test() -> None:
    source = Path(__file__).resolve().parents[2] / "deploy/master/scripts/irangate-pg-usage-tick.py"
    if not source.is_file():
        source = TICK
    fn = tick_functions(source)
    example = {"client_name": "test-subscriber", "connected": True,
               "client_id": "7", "connected_since": "2026-10-21T02:00:00Z",
               "real_address": "192.0.2.10:12000", "virtual_ip": "10.8.0.5",
               "current_received": 2000, "current_sent": 3000,
               "current_total": 5000}
    records = candidates([example], fn)
    assert len(records) == 1
    assert records[0][0] == fn["counter_epoch_key"](
        "local", example, fn["session_of"](example))
    assert records[0][2] == fn["session_of"](example)
    assert records[0][5] == 5000
    print("surena_migrated_baseline_self_test = PASS")


def main() -> None:
    p = argparse.ArgumentParser()
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("--audit", action="store_true")
    g.add_argument("--apply", action="store_true")
    args = p.parse_args()

    import psycopg2
    fns = tick_functions(TICK)
    source_bytes = SNAPSHOT.read_bytes()
    stat_before = SNAPSHOT.stat()
    age = time.time() - stat_before.st_mtime
    if args.apply and (age < -30 or age > 120):
        raise RuntimeError(f"Local snapshot is stale ({age:.0f}s); refresh before cutover")
    rows = candidates(json.loads(source_bytes), fns)
    conn = psycopg2.connect(dsn_from_env(DB_ENV), connect_timeout=12)
    conn.autocommit = False
    planned = []
    historic_before = None
    try:
        with conn.cursor() as cur:
            if args.audit:
                cur.execute("SET TRANSACTION READ ONLY")
            else:
                cur.execute("SELECT pg_try_advisory_xact_lock(740015,1405)")
                if not cur.fetchone()[0]:
                    raise RuntimeError("Concurrent accounting metadata sync detected")
            cur.execute("SELECT COUNT(*),COALESCE(SUM(total_bytes),0) FROM traffic_totals")
            total_rows, historic_before = cur.fetchone()
            if total_rows < 1:
                raise RuntimeError("Historical traffic ledger is empty")
            for source, name, session, rx, tx, total, address, vpn_ip in rows:
                cur.execute(
                    "SELECT 1 FROM traffic_sources WHERE source_id=%s AND client_name=%s",
                    (source, name))
                if cur.fetchone():
                    continue
                cur.execute(
                    "SELECT COUNT(*) FROM traffic_sources WHERE client_name=%s",
                    (name,))
                previous = int(cur.fetchone()[0])
                cur.execute(
                    "SELECT t.total_bytes FROM traffic_totals t "
                    "JOIN clients c USING(client_name) WHERE c.client_name=%s",
                    (name,))
                old_total = cur.fetchone()
                if not old_total or previous == 0:
                    raise RuntimeError("A new stream lacks historical identity; "
                                       "refuse to baseline an unknown account")
                planned.append((source, name, session, rx, tx, total, address, vpn_ip))

            print("CUTOVER_MODE =", "APPLY" if args.apply else "AUDIT")
            print("LOCAL_SNAPSHOT_AGE_SECONDS =", round(age))
            print("LOCAL_ACTIVE_STREAMS =", len(rows))
            print("NEW_LOCAL_STREAM_BASELINES =", len(planned))
            print("LOCAL_BASELINE_BYTES_NOT_CHARGED =",
                  sum(r[5] for r in planned))
            print("HISTORICAL_TOTAL_BYTES =", int(historic_before))

            if args.apply:
                if SNAPSHOT.stat().st_mtime_ns != stat_before.st_mtime_ns or \
                        SNAPSHOT.read_bytes() != source_bytes:
                    raise RuntimeError("Local snapshot changed during preparation")
                for source, name, session, rx, tx, total, address, vpn_ip in planned:
                    cur.execute("""
                        INSERT INTO traffic_sources (
                          source_id, client_name, session_fingerprint,
                          rx_bytes, tx_bytes, total_bytes, connected,
                          real_address, virtual_ip, updated_at
                        ) VALUES (%s,%s,%s,%s,%s,%s,true,%s,%s,now())
                        ON CONFLICT (source_id,client_name) DO NOTHING
                        RETURNING source_id
                    """, (source, name, session,
                          rx, tx, total, address, vpn_ip))
                    if cur.fetchone() is None:
                        raise RuntimeError("Concurrent source insert during baseline")
                cur.execute("SELECT COUNT(*),COALESCE(SUM(total_bytes),0) FROM traffic_totals")
                count_after, historic_after = cur.fetchone()
                if count_after != total_rows or historic_after != historic_before:
                    raise RuntimeError("Traffic totals changed during baseline")
                conn.commit()
                print("BASELINE_COMMIT = PASS")
                print("TRAFFIC_TOTALS_MUTATED_BY_BASELINE = NO")
            else:
                conn.rollback()
                print("DATABASE_COMMIT = NO")
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


if __name__ == "__main__":
    try:
        if "--self-test" in sys.argv:
            self_test()
        else:
            main()
    except Exception as error:
        print("BASELINE_STOP =", str(error), file=sys.stderr)
        raise SystemExit(1)
