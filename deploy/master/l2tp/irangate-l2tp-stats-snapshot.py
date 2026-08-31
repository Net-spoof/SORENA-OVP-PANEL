#!/usr/bin/env python3
from __future__ import annotations

import argparse
import fcntl
import json
import os
import re
import tempfile
from datetime import datetime, timezone
from pathlib import Path

BASE = Path("/opt/irangate/l2tp")
TRAFFIC = Path("/opt/irangate/traffic")
SESSION_DIR = Path("/run/irangate-l2tp/sessions")
STATE_FILE = BASE / "stats_state.json"
CURRENT_FILE = TRAFFIC / "l2tp_current_stats.json"
SESSIONS_FILE = TRAFFIC / "l2tp_sessions.json"
LAST_SEEN_FILE = TRAFFIC / "l2tp_last_seen.json"
LOCK_FILE = Path("/run/irangate-l2tp/stats.lock")

USER_RE = re.compile(r"^[A-Za-z0-9._-]{3,64}$")
IFACE_RE = re.compile(r"^ppp[0-9]+$")
SESSION_RE = re.compile(r"^l2tp:[A-Za-z0-9._-]{3,64}:[A-Za-z0-9._-]+:[0-9]+$")


def now_iso() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def load_json(path: Path, default):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return default


def atomic_json(path: Path, data, mode: int = 0o600) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=path.name + ".", suffix=".tmp", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(data, handle, ensure_ascii=False, indent=2, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp_name, mode)
        os.replace(temp_name, path)
    finally:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass


def read_counter(interface: str, name: str) -> int:
    path = Path("/sys/class/net") / interface / "statistics" / name
    try:
        value = int(path.read_text().strip())
        return max(value, 0)
    except Exception:
        return 0


def pid_alive(pid: int) -> bool:
    return pid > 1 and Path(f"/proc/{pid}").exists()


def valid_session_row(row: dict) -> bool:
    user = str(row.get("client_name") or "").strip()
    interface = str(row.get("interface") or "").strip()
    fingerprint = str(row.get("session_fingerprint") or "").strip()
    try:
        pid = int(row.get("pid") or 0)
    except Exception:
        return False
    return (
        bool(USER_RE.fullmatch(user))
        and bool(IFACE_RE.fullmatch(interface))
        and bool(SESSION_RE.fullmatch(fingerprint))
        and pid_alive(pid)
        and (Path("/sys/class/net") / interface).is_dir()
    )


def collect_active_sessions() -> list[dict]:
    rows: list[dict] = []
    if not SESSION_DIR.is_dir():
        return rows

    for path in sorted(SESSION_DIR.glob("*.json")):
        row = load_json(path, {})
        if not isinstance(row, dict) or not valid_session_row(row):
            # Stale hook files must not hold a connection slot forever.
            try:
                path.unlink()
            except Exception:
                pass
            continue
        rows.append(row)
    return rows


def normalize_state(raw) -> dict:
    if not isinstance(raw, dict):
        raw = {}
    users = raw.get("users")
    sessions = raw.get("sessions")
    if not isinstance(users, dict):
        users = {}
    if not isinstance(sessions, dict):
        sessions = {}
    return {"users": users, "sessions": sessions}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--quiet", action="store_true")
    args = parser.parse_args()

    BASE.mkdir(parents=True, exist_ok=True, mode=0o700)
    TRAFFIC.mkdir(parents=True, exist_ok=True, mode=0o755)
    SESSION_DIR.mkdir(parents=True, exist_ok=True, mode=0o700)
    LOCK_FILE.parent.mkdir(parents=True, exist_ok=True, mode=0o700)

    with LOCK_FILE.open("a+") as lock_handle:
        fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)

        state = normalize_state(load_json(STATE_FILE, {}))
        user_state: dict = state["users"]
        previous_sessions: dict = state["sessions"]
        current_sessions: dict = {}
        active_rows = collect_active_sessions()
        observed_at = now_iso()

        for row in active_rows:
            user = str(row["client_name"]).strip()
            interface = str(row["interface"]).strip()
            fingerprint = str(row["session_fingerprint"]).strip()
            rx = read_counter(interface, "rx_bytes")
            tx = read_counter(interface, "tx_bytes")

            previous = previous_sessions.get(fingerprint, {})
            if not isinstance(previous, dict):
                previous = {}

            if previous:
                old_rx = max(int(previous.get("rx") or 0), 0)
                old_tx = max(int(previous.get("tx") or 0), 0)
                delta_rx = rx - old_rx if rx >= old_rx else rx
                delta_tx = tx - old_tx if tx >= old_tx else tx
            else:
                # A new PPP interface starts its counters at zero. Count bytes
                # already transferred before the first snapshot rather than
                # discarding them as a baseline.
                delta_rx = rx
                delta_tx = tx

            user_row = user_state.get(user, {})
            if not isinstance(user_row, dict):
                user_row = {}
            cumulative_rx = max(int(user_row.get("rx") or 0), 0) + max(delta_rx, 0)
            cumulative_tx = max(int(user_row.get("tx") or 0), 0) + max(delta_tx, 0)
            user_state[user] = {
                "rx": cumulative_rx,
                "tx": cumulative_tx,
                "last_seen": observed_at,
            }

            current_sessions[fingerprint] = {
                "user": user,
                "interface": interface,
                "pid": int(row.get("pid") or 0),
                "rx": rx,
                "tx": tx,
                "connected_since": str(row.get("connected_since") or observed_at),
                "virtual_ip": str(row.get("virtual_ip") or ""),
            }

        state = {"users": user_state, "sessions": current_sessions}
        atomic_json(STATE_FILE, state)

        active_by_user: dict[str, int] = {}
        session_output: list[dict] = []
        for row in active_rows:
            user = str(row["client_name"]).strip()
            active_by_user[user] = active_by_user.get(user, 0) + 1
            session_output.append({
                "source_id": "l2tp",
                "client_name": user,
                "session_fingerprint": str(row["session_fingerprint"]),
                "connected": True,
                "connected_since": str(row.get("connected_since") or observed_at),
                "last_seen": observed_at,
                "real_address": str(row.get("real_address") or ""),
                "virtual_ip": str(row.get("virtual_ip") or ""),
                "interface": str(row.get("interface") or ""),
                "pid": int(row.get("pid") or 0),
            })

        aggregate: list[dict] = []
        last_seen_map: dict[str, dict] = {}
        for user in sorted(user_state):
            row = user_state[user]
            if not isinstance(row, dict):
                continue
            rx = max(int(row.get("rx") or 0), 0)
            tx = max(int(row.get("tx") or 0), 0)
            last_seen = str(row.get("last_seen") or "")
            aggregate.append({
                "source_id": "l2tp",
                "client_name": user,
                "current_received": rx,
                "current_sent": tx,
                "current_total": rx + tx,
                # Active sessions are published separately in l2tp_sessions.json
                # so several simultaneous PPP sessions can be counted while
                # traffic_sources still has exactly one cumulative row/user.
                "connected": False,
                "last_seen": last_seen,
                "active_sessions": active_by_user.get(user, 0),
            })
            if last_seen:
                last_seen_map[user] = {"last_seen": last_seen}

        atomic_json(CURRENT_FILE, aggregate, 0o644)
        atomic_json(SESSIONS_FILE, session_output, 0o644)
        atomic_json(LAST_SEEN_FILE, last_seen_map, 0o644)

        if not args.quiet:
            print(
                f"L2TP snapshot users={len(aggregate)} "
                f"active_sessions={len(session_output)}"
            )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
