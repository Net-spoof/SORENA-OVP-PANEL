#!/usr/bin/env python3
"""Cumulative node accounting per OpenVPN session, published per node/client.

The legacy state['totals'] is preserved to avoid resetting existing quotas.
A node fetch failure does not advance a counter or erase existing totals.
"""
import json
import hashlib
import os
import sys
import time
import urllib.request
from collections import defaultdict
from pathlib import Path

NODES_FILE = Path("/opt/irangate/webpanel/nodes.json")
TRAFFIC_DIR = Path("/opt/irangate/traffic")
OUT_FILE = TRAFFIC_DIR / "node_current_stats.json"
STATE_FILE = TRAFFIC_DIR / "node_usage_state.json"
NODE_CUTOVER_MARKER = TRAFFIC_DIR / "node_usage_session_v2.marker"


def load_json(path, default):
    try:
        return json.loads(path.read_text())
    except (OSError, ValueError):
        return default


def save_json(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2))
    os.replace(tmp, path)


def nodes_list(data):
    if isinstance(data, list):
        return data
    return data.get("nodes", []) if isinstance(data, dict) else []


def fetch_usage(node):
    url = node.get("agent_url", "").rstrip("/") + "/usage"
    token = node.get("token", "")
    req = urllib.request.Request(
        url, headers={"Authorization": "Bearer " + token}
    )
    with urllib.request.urlopen(req, timeout=8) as r:
        payload = json.loads(r.read().decode("utf-8", "replace"))
    if not payload.get("success"):
        raise RuntimeError("node did not confirm success")
    clients = payload.get("clients")
    if clients is None:
        clients = payload.get("data")
    if not isinstance(clients, list):
        raise ValueError("node clients is not a list")
    return clients


def nonnegative(v):
    try:
        return max(0, int(v or 0))
    except (TypeError, ValueError):
        return 0


def peer_ip(address):
    from ipaddress import ip_address
    raw = str(address or "").strip()
    if raw.startswith("[") and "]" in raw:
        return raw[1:raw.index("]")]
    try:
        return str(ip_address(raw))
    except ValueError:
        head, sep, port = raw.rpartition(":")
        return head if sep and port.isdigit() else raw


def stable_session_id(row):
    # A proper OpenVPN status-v2 epoch is a strong reconnect discriminator.
    since = str(row.get("connected_since_time_t") or row.get("connected_since") or "").strip()
    remote = str(row.get("real_address") or "").strip()
    vpn = str(row.get("virtual_ip") or row.get("virtual_address") or "").strip()
    if not since or since == "0":
        # Without an epoch, a NAT port change is not a new session.
        seed = "unknown-epoch|" + peer_ip(remote) + "|" + vpn
    else:
        seed = since + "|" + peer_ip(remote) + "|" + vpn
    return hashlib.sha256(seed.encode("utf-8")).hexdigest()[:24]


def likely_replayed_counter(current, previous):
    return (current >= 1_000_000_000 and previous >= 1_000_000_000
            and previous * 75 <= current * 100
            and current * 100 <= previous * 150)


def process_node_clients(state, node_id, node_name, clients, now_iso, now_ts):
    """Mutates only passed in-memory state; caller publishes after successful fetch."""
    last = state.setdefault("last", {})
    totals = state.setdefault("totals", {})
    sessions = state.setdefault("sessions", {})
    grouped = defaultdict(list)
    for c in clients:
        if not isinstance(c, dict):
            continue
        name = str(c.get("client_name") or c.get("common_name") or "").strip()
        if not name or name.upper() == "UNDEF":
            continue
        grouped[name].append(c)

    output = []
    for name, rows in grouped.items():
        key = f"{node_id}:{name}"
        aggregate = dict(totals.get(key) or {"rx": 0, "tx": 0})
        legacy_last = last.get(key)
        prior_sessions = {
            k: v for k, v in sessions.items()
            if k.startswith(key + ":") and isinstance(v, dict)
        }
        fresh_legacy_cutover = legacy_last is not None and not prior_sessions
        active_rx = active_tx = 0
        delta_rx = delta_tx = 0
        for row in rows:
            rx = nonnegative(row.get("bytes_received"))
            tx = nonnegative(row.get("bytes_sent"))
            active_rx += rx
            active_tx += tx
            session_key = key + ":" + stable_session_id(row)
            previous = sessions.get(session_key)
            if isinstance(previous, dict):
                old_rx = nonnegative(previous.get("rx"))
                old_tx = nonnegative(previous.get("tx"))
                # For an unchanged session, reset means a new counter epoch.
                drx = rx - old_rx if rx >= old_rx else rx
                dtx = tx - old_tx if tx >= old_tx else tx
            elif fresh_legacy_cutover:
                # Legacy tracking used one snapshot per node/client; for a
                # single active session recover its delta. For concurrent
                # sessions there is no reliable mapping: baseline once.
                if len(rows) == 1:
                    old_rx = nonnegative(legacy_last.get("rx"))
                    old_tx = nonnegative(legacy_last.get("tx"))
                    drx = rx - old_rx if rx >= old_rx else rx
                    dtx = tx - old_tx if tx >= old_tx else tx
                else:
                    drx = dtx = 0
                    print("NODE_ACCOUNTING_WARNING concurrent_legacy_cutover", key)
            else:
                # Reconnect with changed NAT port and a suspiciously similar
                # large value: charge only the incremental bytes, not the
                # cumulative reading again.
                peer = peer_ip(row.get("real_address"))
                prior = [
                    v for v in prior_sessions.values()
                    if peer_ip(v.get("real_address")) == peer and peer
                    and likely_replayed_counter(
                        rx + tx,
                        nonnegative(v.get("rx")) + nonnegative(v.get("tx"))
                    )
                ]
                if prior:
                    prev = max(prior, key=lambda v: nonnegative(v.get("rx")) + nonnegative(v.get("tx")))
                    total_delta = max(0, rx + tx - nonnegative(prev.get("rx")) - nonnegative(prev.get("tx")))
                    drx = (rx * total_delta // (rx + tx)) if rx + tx else 0
                    dtx = total_delta - drx
                    print("NODE_ACCOUNTING_WARNING possible_replay", key,
                          "charged=", total_delta)
                else:
                    drx, dtx = rx, tx
            delta_rx += max(0, drx)
            delta_tx += max(0, dtx)
            sessions[session_key] = {
                "rx": rx, "tx": tx, "real_address": row.get("real_address") or "",
                "last_seen": now_ts,
            }

        aggregate["rx"] = nonnegative(aggregate.get("rx")) + delta_rx
        aggregate["tx"] = nonnegative(aggregate.get("tx")) + delta_tx
        last[key] = {"rx": active_rx, "tx": active_tx}
        totals[key] = aggregate
        rx_sum, tx_sum = nonnegative(aggregate["rx"]), nonnegative(aggregate["tx"])
        combined = rx_sum + tx_sum
        first = rows[0]
        output.append({
            "name": name, "client_name": name, "common_name": name,
            "node_id": node_id, "node_name": node_name,
            "bytes_received": rx_sum, "bytes_sent": tx_sum,
            "total_bytes": combined,
            "current_received": rx_sum, "current_sent": tx_sum,
            "current_total": combined,
            "daily_usage": combined, "monthly_usage": combined,
            "data_used_mb": round(combined / 1_000_000, 2),
            "upload": rx_sum, "download": tx_sum, "used_bytes": combined,
            "used": combined, "usage": combined, "traffic": combined,
            "used_traffic": combined, "connected": True,
            "is_connected": True, "active": True, "status": "active",
            "real_address": first.get("real_address", ""),
            "virtual_ip": first.get("virtual_ip") or first.get("virtual_address", ""),
            "virtual_address": first.get("virtual_address", ""),
            "connected_since": first.get("connected_since", ""),
            "last_seen": now_iso, "last_connection": now_iso,
            "last_updated": now_iso, "last_updated_ts": now_ts,
        })
        print(node_id, name, "+", delta_rx + delta_tx, "total=", combined)
    return output


def baseline_node_clients(state, node_id, clients, now_ts):
    """Baseline ALL active node sessions without charging historical counters.

    Never mutate state['totals'] here; it is a durable running aggregate
    and must be kept byte-for-byte across the cutover.
    """
    last = state.setdefault("last", {})
    sessions = state.setdefault("sessions", {})
    # Preserve historical session ledgers for disconnected peers. Dropping
    # them would make a later reconnect look new and could replay old traffic.
    # Only refresh baselines for sessions observed in this cutover snapshot.
    grouped = defaultdict(list)
    for row in clients:
        if not isinstance(row, dict):
            continue
        name = str(row.get("client_name") or row.get("common_name") or "").strip()
        if name and name.upper() != "UNDEF":
            grouped[name].append(row)
    for name, rows in grouped.items():
        key = node_id + ":" + name
        rx_sum = tx_sum = 0
        for row in rows:
            rx, tx = nonnegative(row.get("bytes_received")), nonnegative(row.get("bytes_sent"))
            rx_sum += rx
            tx_sum += tx
            sessions[key + ":" + stable_session_id(row)] = {
                "rx": rx, "tx": tx,
                "real_address": row.get("real_address") or "",
                "last_seen": now_ts,
            }
        last[key] = {"rx": rx_sum, "tx": tx_sum}


def self_test():
    now = "2026-10-15T10:00:00Z"
    state = {"last": {}, "totals": {}, "sessions": {}}
    def row(sent, port, since="1000", ip="1.2.3.4", vip="10.8.0.2"):
        return {"common_name": "alice", "bytes_received": 0,
                "bytes_sent": sent, "connected_since_time_t": since,
                "real_address": ip + ":" + str(port), "virtual_ip": vip}
    a = process_node_clients(state, "tr", "Turkey", [row(11_100_000_000, 10)], now, 1)
    assert a[0]["total_bytes"] == 11_100_000_000
    b = process_node_clients(state, "tr", "Turkey", [row(11_120_000_000, 11)], now, 2)
    assert b[0]["total_bytes"] == 11_120_000_000  # same-peer large replay
    c = process_node_clients(state, "tr", "Turkey", [row(30, 12, since="1001")], now, 3)
    assert c[0]["total_bytes"] == 11_120_000_030
    d = process_node_clients(state, "tr", "Turkey", [row(30, 12, since="1001"), row(40, 13, since="1002", vip="10.8.0.3")], now, 4)
    assert d[0]["total_bytes"] == 11_120_000_070
    assert process_node_clients(state, "tr", "Turkey", [row(30, 12, since="1001"), row(40, 13, since="1002", vip="10.8.0.3")], now, 5)[0]["total_bytes"] == 11_120_000_070
    legacy = {"last": {"fi:alice": {"rx": 0, "tx": 11_000_000_000}},
              "totals": {"fi:alice": {"rx": 0, "tx": 15_000_000_000}}}
    z = process_node_clients(legacy, "fi", "Finland", [row(11_100_000_000, 22)], now, 6)
    assert z[0]["total_bytes"] == 15_100_000_000
    assert stable_session_id(row(5, 22, since="")) == stable_session_id(row(5, 99, since=""))
    assert stable_session_id(row(5, 22, since="1000")) == stable_session_id(row(5, 99, since="1000"))
    cutover_state = {"last": {"fi:alice": {"rx": 0, "tx": 300}},
                     "totals": {"fi:alice": {"rx": 500, "tx": 900}},
                     "sessions": {}}
    original_total = dict(cutover_state["totals"]["fi:alice"])
    baseline_node_clients(cutover_state, "fi", [row(700, 12)], 100)
    assert cutover_state["totals"]["fi:alice"] == original_total
    out = process_node_clients(cutover_state, "fi", "Finland",
                               [row(710, 13)], now, 101)
    assert out[0]["total_bytes"] == 1410
    assert cutover_state["totals"]["fi:alice"] == {"rx": 500, "tx": 910}
    print("node_usage_sync_self_test = PASS")


def main(baseline=False):
    if baseline and NODE_CUTOVER_MARKER.exists():
        raise RuntimeError("Node session baseline already initialized; refusing second cutover")
    nodes = nodes_list(load_json(NODES_FILE, []))
    state = load_json(STATE_FILE, {"last": {}, "totals": {}, "sessions": {}})
    if not isinstance(state, dict):
        raise ValueError("invalid node state")
    for field in ("last", "totals", "sessions"):
        if not isinstance(state.get(field, {}), dict):
            raise ValueError("invalid node state field " + field)
    out = []
    now_iso = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    now_ts = int(time.time())
    results = []
    candidates = 0
    for node in nodes:
        if node.get("is_local") or not node.get("enabled", True):
            continue
        if not node.get("agent_url") or not node.get("token"):
            continue
        candidates += 1
        node_id = str(node.get("id") or node.get("name") or node.get("agent_url"))
        try:
            clients = fetch_usage(node)
        except Exception as e:
            if baseline:
                raise RuntimeError("Baseline refused because node " + node_id
                                   + " did not provide a valid usage response") from e
            print(node_id, "fetch failed:", type(e).__name__, e)
            continue
        results.append((node_id, node.get("name", node_id), clients))
    if baseline and (candidates == 0 or len(results) != candidates):
        raise RuntimeError("Baseline refused: incomplete node inventory")

    if baseline:
        for node_id, _node_name, clients in results:
            baseline_node_clients(state, node_id, clients, now_ts)

    for node_id, node_name, clients in results:
        out.extend(process_node_clients(state, node_id, node_name,
                                        clients, now_iso, now_ts))
    # Retain inactive session ledgers only 30 days, never wipe cumulative totals.
    sessions = state.get("sessions", {})
    if isinstance(sessions, dict):
        for key, value in list(sessions.items()):
            if now_ts - nonnegative(value.get("last_seen")) > 30 * 86400:
                del sessions[key]
    save_json(STATE_FILE, state)
    save_json(OUT_FILE, out)
    if baseline:
        NODE_CUTOVER_MARKER.write_text(
            json.dumps({"version": 2, "timestamp": now_iso,
                        "nodes": [name for name, _, _ in results],
                        "policy": "live session counters baseline; preserve prior totals"},
                       ensure_ascii=False, indent=2) + "\n"
        )
        print("NODE_BASELINE_COMPLETE", "nodes=", len(results),
              "live_sessions=", sum(len(rows) for _, _, rows in results),
              "retained_totals=", len(state.get("totals", {})))


if __name__ == "__main__":
    if "--self-test" in sys.argv:
        self_test()
    else:
        main(baseline="--baseline" in sys.argv)
