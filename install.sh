#!/bin/bash
set -euo pipefail

# Allow OpenVPN to bind privileged UDP/TCP ports such as 443.
# Without this systemd drop-in, OpenVPN may fail with:
# "Socket bind failed ... Permission denied (errno=13)" on ports below 1024.
ensure_openvpn_privileged_port_capability() {
    mkdir -p /etc/systemd/system/openvpn@server.service.d

    cat > /etc/systemd/system/openvpn@server.service.d/cap-net-bind.conf <<'EOF'
[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_SETGID CAP_SETUID CAP_DAC_OVERRIDE
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
EOF

    systemctl daemon-reload
}


# Install a lightweight OpenVPN connection guard.
# It enforces per-client connection_limit from:
# /opt/irangate/database/clients/<client>.json
install_connection_guard() {
    log_info "Installing IranGate connection limit guard..."

    apt-get install -y python3 >/dev/null 2>&1 || true

    cat > /usr/local/bin/irangate-connection-guard <<'PYGUARD'
#!/usr/bin/env python3
import argparse
import json
import os
import socket
import time
from pathlib import Path

BASE = Path("/opt/irangate")
CLIENTS_DIR = BASE / "database" / "clients"
TRAFFIC_DIR = BASE / "traffic"
LOG = Path("/var/log/irangate-connection-guard.log")
BASELINES_PATH = TRAFFIC_DIR / "guard_session_baselines.json"

MGMT_HOST = os.environ.get("IRANGATE_MGMT_HOST", "127.0.0.1")
MGMT_PORT = int(os.environ.get("IRANGATE_MGMT_PORT", "7505"))
INTERVAL = float(os.environ.get("IRANGATE_GUARD_INTERVAL", "20"))

def log(msg):
    try:
        LOG.parent.mkdir(parents=True, exist_ok=True)
        with LOG.open("a") as f:
            f.write(time.strftime("%Y-%m-%d %H:%M:%S ") + msg + "\n")
    except Exception:
        pass

def read_json(path, default=None):
    try:
        return json.loads(Path(path).read_text())
    except Exception:
        return default

def write_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, indent=2, ensure_ascii=False))
    os.replace(tmp, path)

def mgmt_cmd(cmd, timeout=3):
    sock = socket.create_connection((MGMT_HOST, MGMT_PORT), timeout=timeout)
    try:
        try:
            sock.settimeout(0.5)
            sock.recv(4096)
        except Exception:
            pass

        sock.settimeout(timeout)
        sock.sendall((cmd.strip() + "\n").encode())

        chunks = []
        while True:
            try:
                data = sock.recv(8192)
            except socket.timeout:
                break
            if not data:
                break

            chunks.append(data.decode(errors="ignore"))
            text = "".join(chunks)

            if "\nEND" in text or "SUCCESS:" in text or "ERROR:" in text:
                break

        return "".join(chunks)
    finally:
        try:
            sock.close()
        except Exception:
            pass

def get_sessions():
    out = mgmt_cmd("status 2")
    sessions = {}

    for line in out.splitlines():
        if not line.startswith("CLIENT_LIST,"):
            continue

        parts = line.split(",")
        if len(parts) < 9:
            continue

        cn = parts[1]
        real = parts[2]

        try:
            received = int(parts[5] or 0)
            sent = int(parts[6] or 0)
        except Exception:
            received = 0
            sent = 0

        try:
            connected_ts = int(parts[8] or 0)
        except Exception:
            connected_ts = 0

        sessions.setdefault(cn, []).append({
            "real": real,
            "received": received,
            "sent": sent,
            "total": received + sent,
            "connected_ts": connected_ts,
        })

    return sessions

def usage_maps():
    current = {}
    current_split = {}

    cur = read_json(TRAFFIC_DIR / "current_stats.json", [])
    if isinstance(cur, list):
        for rec in cur:
            if not isinstance(rec, dict):
                continue

            cn = rec.get("client_name")
            if not cn:
                continue

            recv = int(rec.get("current_received") or 0)
            sent = int(rec.get("current_sent") or 0)
            total = int(rec.get("current_total") or 0)
            if total <= 0:
                total = recv + sent

            if total > current.get(cn, 0):
                current[cn] = total
                current_split[cn] = (recv, sent)

    cumulative = {}
    cumulative_split = {}

    cum = read_json(TRAFFIC_DIR / "cumulative_totals.json", [])
    if isinstance(cum, list):
        for rec in cum:
            if not isinstance(rec, dict):
                continue

            cn = rec.get("client_name")
            if not cn:
                continue

            recv = int(rec.get("total_received") or 0)
            sent = int(rec.get("total_sent") or 0)
            total = recv + sent

            if total > cumulative.get(cn, 0):
                cumulative[cn] = total
                cumulative_split[cn] = (recv, sent)

    return current, current_split, cumulative, cumulative_split


def as_int(v):
    try:
        return int(v or 0)
    except Exception:
        return 0

def session_key(cn, sess):
    return f"{cn}|{sess.get('real', '')}|{sess.get('connected_ts', 0)}"

def calculate_enforced_usage(cn, client, cn_sessions, cumulative):
    # Stable usage must come ONLY from client.json.
    # current_stats/cumulative_totals can be stale or inflated after reconnect/reactivation.
    json_used = as_int(client.get("traffic_used_bytes"))
    json_bytes = as_int(client.get("bytes_sent")) + as_int(client.get("bytes_received"))

    stored_used = max(json_used, json_bytes)

    if not cn_sessions:
        return stored_used

    baselines = read_json(BASELINES_PATH, {})
    if not isinstance(baselines, dict):
        baselines = {}

    changed = False
    alive_keys = set()
    session_delta = 0

    for sess in cn_sessions:
        key = session_key(cn, sess)
        alive_keys.add(key)

        live_total = as_int(sess.get("total"))
        base = baselines.get(key)

        if not isinstance(base, dict):
            base = {
                "base_used": stored_used,
                "base_session_total": live_total,
                "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            }
            baselines[key] = base
            changed = True

        base_used = as_int(base.get("base_used"))
        base_session_total = as_int(base.get("base_session_total"))

        delta = live_total - base_session_total
        if delta < 0:
            delta = 0

        session_delta += delta

    # Remove old baselines for this client after reconnects/disconnects.
    prefix = f"{cn}|"
    for key in list(baselines.keys()):
        if key.startswith(prefix) and key not in alive_keys:
            baselines.pop(key, None)
            changed = True

    if changed:
        write_json(BASELINES_PATH, baselines)

    baseline_used = stored_used
    for key in alive_keys:
        base = baselines.get(key, {})
        baseline_used = max(baseline_used, as_int(base.get("base_used")))

    return max(stored_used, baseline_used + session_delta)


def client_path(cn):
    return CLIENTS_DIR / f"{cn}.json"

def load_client(cn):
    data = read_json(client_path(cn), {})
    if isinstance(data, dict):
        return data
    return {}

def sync_traffic_files(cn, used, recv, sent, limit, connected=False):
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    quota = round((used / limit) * 100, 2) if limit > 0 else 0

    current_path = TRAFFIC_DIR / "current_stats.json"
    current = read_json(current_path, [])
    if isinstance(current, list):
        found = False
        for rec in current:
            if isinstance(rec, dict) and rec.get("client_name") == cn:
                rec["current_received"] = recv
                rec["current_sent"] = sent
                rec["current_total"] = used
                rec["daily_usage"] = used
                rec["monthly_usage"] = used
                rec["quota_used_percent"] = quota
                rec["connected"] = connected
                rec["last_seen"] = now
                found = True

        if found:
            write_json(current_path, current)

    cumulative_path = TRAFFIC_DIR / "cumulative_totals.json"
    cumulative = read_json(cumulative_path, [])
    if isinstance(cumulative, list):
        found = False
        for rec in cumulative:
            if isinstance(rec, dict) and rec.get("client_name") == cn:
                rec["total_received"] = recv
                rec["total_sent"] = sent
                rec["last_session_received"] = recv
                rec["last_session_sent"] = sent
                rec["last_update"] = now
                found = True

        if found:
            write_json(cumulative_path, cumulative)

def best_split(cn, used, client, sessions, current_split, cumulative_split):
    if cn in current_split:
        recv, sent = current_split[cn]
    elif cn in cumulative_split:
        recv, sent = cumulative_split[cn]
    else:
        best_live = None
        for sess in sessions.get(cn, []):
            if best_live is None or sess.get("total", 0) > best_live.get("total", 0):
                best_live = sess

        if best_live:
            recv = int(best_live.get("received") or 0)
            sent = int(best_live.get("sent") or 0)
        else:
            recv = int(client.get("bytes_received") or client.get("download") or 0)
            sent = int(client.get("bytes_sent") or client.get("upload") or 0)

    total = recv + sent
    if used > 0 and total != used:
        if total > 0:
            sent = int(used * sent / total)
            recv = used - sent
        else:
            sent = int(used * 0.8)
            recv = used - sent

    return int(recv), int(sent)

def kill_real(real, reason):
    if not real:
        return
    resp = mgmt_cmd(f"kill {real}")
    log(f"killed session {real} reason={reason} response={resp.strip()}")

def kill_client_sessions(cn, sessions, reason):
    for sess in sessions.get(cn, []):
        kill_real(sess.get("real"), reason)

def mark_exceeded(cn, client, used, recv, sent, limit):
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    try:
        used = int(used or 0)
    except Exception:
        used = 0

    try:
        limit = int(limit or 0)
    except Exception:
        limit = 0

    try:
        recv = int(recv or 0)
    except Exception:
        recv = 0

    try:
        sent = int(sent or 0)
    except Exception:
        sent = 0

    # Critical rule:
    # When quota is exceeded, freeze displayed usage at the quota limit.
    # Do NOT write inflated current_stats/session values into client.json.
    if limit > 0:
        used = limit

    # Prefer existing stable split if the incoming split is empty.
    if recv + sent <= 0:
        recv = int(client.get("bytes_received") or client.get("download") or 0)
        sent = int(client.get("bytes_sent") or client.get("upload") or 0)

    total = recv + sent

    # Scale upload/download to exactly match frozen used bytes.
    if used > 0 and total != used:
        if total > 0:
            sent = int(used * sent / total)
            recv = used - sent
        else:
            sent = int(used * 0.32)
            recv = used - sent

    client["active"] = False
    client["status"] = "traffic_limit_exceeded"
    client["traffic_used_bytes"] = int(used)
    client["bytes_received"] = int(recv)
    client["bytes_sent"] = int(sent)
    client["download"] = int(recv)
    client["upload"] = int(sent)
    client["data_used_mb"] = int(used // 1000000)
    client["traffic_exceeded_at"] = now
    client["last_update"] = now

    write_json(client_path(cn), client)
    sync_traffic_files(cn, used, recv, sent, limit, connected=False)



def mark_progress(cn, client, used, recv, sent, limit):
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    try:
        used = int(used or 0)
    except Exception:
        used = 0

    try:
        limit = int(limit or 0)
    except Exception:
        limit = 0

    try:
        recv = int(recv or 0)
    except Exception:
        recv = 0

    try:
        sent = int(sent or 0)
    except Exception:
        sent = 0

    old_used = int(client.get("traffic_used_bytes") or 0)
    old_recv = int(client.get("bytes_received") or client.get("download") or 0)
    old_sent = int(client.get("bytes_sent") or client.get("upload") or 0)

    # Never decrease stored usage.
    if used < old_used:
        used = old_used

    # Do not let progress write above limit. mark_exceeded handles exact limit lock.
    if limit > 0 and used > limit:
        used = limit

    if used <= 0:
        return

    if recv + sent <= 0:
        recv = old_recv
        sent = old_sent

    total = recv + sent

    if used > 0 and total != used:
        if total > 0:
            sent = int(used * sent / total)
            recv = used - sent
        else:
            sent = old_sent
            recv = old_recv

    client["traffic_used_bytes"] = int(used)
    client["bytes_received"] = int(recv)
    client["bytes_sent"] = int(sent)
    client["download"] = int(recv)
    client["upload"] = int(sent)
    client["data_used_mb"] = int(used // 1000000)
    client["active"] = True
    client["status"] = "active"
    client["last_update"] = now

    write_json(client_path(cn), client)
    sync_traffic_files(cn, used, recv, sent, limit, connected=True)


def enforce_client(cn, sessions, maps):
    current, current_split, cumulative, cumulative_split = maps
    client = load_client(cn)
    if not client:
        return

    status = str(client.get("status") or "").lower()
    active = client.get("active", True)

    if active is False or "traffic_limit" in status or "exceeded" in status:
        if sessions.get(cn):
            kill_client_sessions(cn, sessions, "inactive")
        return

    try:
        limit = int(client.get("traffic_limit_bytes") or 0)
    except Exception:
        limit = 0

    try:
        json_used = int(client.get("traffic_used_bytes") or 0)
    except Exception:
        json_used = 0

    used = calculate_enforced_usage(cn, client, sessions.get(cn, []), cumulative)

    if limit > 0 and used >= limit:
        recv, sent = best_split(cn, used, client, sessions, {}, {})
        log(f"traffic limit exceeded client={cn} used={used} limit={limit} sessions={len(sessions.get(cn, []))}")
        kill_client_sessions(cn, sessions, "traffic_limit")
        mark_exceeded(cn, client, used, recv, sent, limit)
        return

    # Persist live progress while the client is connected.
    # This makes "left" decrease in the panel before quota is fully exhausted.
    if sessions.get(cn) and used > int(client.get("traffic_used_bytes") or 0):
        recv, sent = best_split(cn, used, client, sessions, {}, {})
        mark_progress(cn, client, used, recv, sent, limit)

    try:
        conn_limit = int(client.get("connection_limit") or 1)
    except Exception:
        conn_limit = 1

    if conn_limit > 0 and len(sessions.get(cn, [])) > conn_limit:
        sorted_sessions = sorted(
            sessions.get(cn, []),
            key=lambda x: int(x.get("connected_ts") or 0),
            reverse=True
        )
        to_kill = sorted_sessions[conn_limit:]
        log(f"client={cn} active={len(sessions.get(cn, []))} limit={conn_limit} killing={len(to_kill)}")
        for sess in to_kill:
            kill_real(sess.get("real"), "connection_limit")

def enforce_once(cn=None):
    sessions = get_sessions()
    maps = usage_maps()

    if cn:
        enforce_client(cn, sessions, maps)
        return

    for active_cn in list(sessions.keys()):
        enforce_client(active_cn, sessions, maps)

def kick_client(cn):
    sessions = get_sessions()
    kill_client_sessions(cn, sessions, "admin_edit_reset_or_kick")

def loop():
    log(f"IranGate optimized unified guard started host={MGMT_HOST} port={MGMT_PORT} interval={INTERVAL}")
    while True:
        try:
            enforce_once()
        except Exception as exc:
            log(f"ERROR {exc}")
        time.sleep(INTERVAL)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--once", help="Enforce only one client immediately")
    parser.add_argument("--kick", help="Kick all active sessions for one client")
    args = parser.parse_args()

    if args.kick:
        kick_client(args.kick)
        return

    if args.once:
        enforce_once(args.once)
        return

    loop()

if __name__ == "__main__":
    main()
PYGUARD

    chmod +x /usr/local/bin/irangate-connection-guard

    cat > /usr/local/bin/irangate-client-connect-guard <<'PYCLIENTCONNECT'
#!/usr/bin/env python3
import json
import os
import sys
import time
from pathlib import Path

CLIENTS_DIR = Path("/opt/irangate/database/clients")
LOG_FILE = Path("/var/log/irangate-client-connect-guard.log")

def log(msg):
    try:
        LOG_FILE.parent.mkdir(parents=True, exist_ok=True)
        with LOG_FILE.open("a") as f:
            f.write(time.strftime("%Y-%m-%d %H:%M:%S ") + msg + "\n")
    except Exception:
        pass

def read_json(path):
    try:
        return json.loads(path.read_text())
    except Exception:
        return None

cn = os.environ.get("common_name") or ""
cn = os.path.basename(cn.strip())

if not cn:
    log("REJECT no common_name")
    sys.exit(1)

client_file = CLIENTS_DIR / f"{cn}.json"
data = read_json(client_file)

# If client JSON is missing, allow to avoid breaking old/manual clients unexpectedly.
if not isinstance(data, dict):
    log(f"ALLOW {cn} no_json")
    sys.exit(0)

status = str(data.get("status") or "").lower()
active = data.get("active", True)

try:
    used = int(data.get("traffic_used_bytes") or 0)
except Exception:
    used = 0

try:
    limit = int(data.get("traffic_limit_bytes") or 0)
except Exception:
    limit = 0

if used <= 0:
    try:
        used = int(data.get("bytes_sent") or 0) + int(data.get("bytes_received") or 0)
    except Exception:
        used = 0

if active is False:
    log(f"REJECT {cn} inactive status={status} used={used} limit={limit}")
    sys.exit(1)

if status == "traffic_limit_exceeded" or "traffic_limit" in status or "exceeded" in status:
    log(f"REJECT {cn} status={status} used={used} limit={limit}")
    sys.exit(1)

if limit > 0 and used >= limit:
    log(f"REJECT {cn} quota used={used} limit={limit}")
    sys.exit(1)

log(f"ALLOW {cn} active={active} status={status} used={used} limit={limit}")
sys.exit(0)
PYCLIENTCONNECT

    chmod +x /usr/local/bin/irangate-client-connect-guard

    if [ -f /etc/openvpn/server.conf ]; then
        grep -q '^script-security 2' /etc/openvpn/server.conf || echo 'script-security 2' >> /etc/openvpn/server.conf
        grep -q '^client-connect /usr/local/bin/irangate-client-connect-guard' /etc/openvpn/server.conf || echo 'client-connect /usr/local/bin/irangate-client-connect-guard' >> /etc/openvpn/server.conf
    fi


    cat > /etc/systemd/system/irangate-connection-guard.service <<'EOF'
[Unit]
Description=IranGate OpenVPN Connection Limit Guard
After=network-online.target openvpn@server.service
Wants=openvpn@server.service

[Service]
Type=simple
Environment=IRANGATE_DATA_DIR=/opt/irangate
Environment=OPENVPN_MANAGEMENT_HOST=127.0.0.1
Environment=OPENVPN_MANAGEMENT_PORT=7505
Environment=IRANGATE_GUARD_INTERVAL=20
ExecStart=/usr/local/bin/irangate-connection-guard
Restart=always
RestartSec=3
User=root

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable irangate-connection-guard >/dev/null 2>&1 || true
}

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="/var/log/irangate-install.log"
SERVER_IP=""
PROTOCOL="udp"
PORT=1194
DNS="2"
CLIENT_NAME="client"
WEBPANEL_PORT=8080
ADMIN_USER="admin"
ADMIN_PASS="adminadmin"
WEB_PATH=""
display_banner() {
    clear
    echo -e "${CYAN}"
    echo "╔══════════════════════════════════════════════════════════════════════════════╗"
    echo "║                                                                              ║"
    echo "║    ██╗██████╗  █████╗ ███╗   ██╗ ██████╗  █████╗ ████████╗███████╗           ║"
    echo "║    ██║██╔══██╗██╔══██╗████╗  ██║██╔════╝ ██╔══██╗╚══██╔══╝██╔════╝           ║"
    echo "║    ██║██████╔╝███████║██╔██╗ ██║██║  ███╗███████║   ██║   █████╗             ║"
    echo "║    ██║██╔══██╗██╔══██║██║╚██╗██║██║   ██║██╔══██║   ██║   ██╔══╝             ║"
    echo "║    ██║██║  ██║██║  ██║██║ ╚████║╚██████╔╝██║  ██║   ██║   ███████╗           ║"
    echo "║    ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═══╝ ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚══════╝           ║"
    echo "║                                                                              ║"
    echo "║    ╔═══════════════════════════════════════════════════════════════════╗     ║"
    echo -e "║    ║     ${MAGENTA}🚀 IranGate Multi-Protocol Gateway Management System 🚀${CYAN}      ║      ║"
    echo "║    ╚═══════════════════════════════════════════════════════════════════╝     ║"
    echo "║                                                                              ║"
    echo "╚══════════════════════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    echo ""
    echo -e "${YELLOW}Version: 3.0.0${NC}"
    echo -e "${YELLOW}This installer will set up:${NC}"
    echo -e "  ${GREEN}✓${NC} OpenVPN Server Core"
    echo -e "  ${GREEN}✓${NC} IranGate CLI"
    echo -e "  ${GREEN}✓${NC} IranGate Web Panel"
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}📦 Project's Git:${NC} ${GREEN}https://github.com/AmiriDev-ORG/IranGate-OV${NC}"
    echo -e "${BLUE}📢 Telegram Channel:${NC} ${GREEN}@IranGate_Official${NC}"
    echo -e "${BLUE}👤 Project Author:${NC} ${GREEN}@AmiriDev_ORG${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo ""
}
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') - [INFO] $1" >> "$LOG_FILE"
}
log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') - [WARN] $1" >> "$LOG_FILE"
}
log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') - [ERROR] $1" >> "$LOG_FILE"
}
log_step() {
    echo -e "${BLUE}[STEP]${NC} $1"
    echo "$(date '+%Y-%m-%d %H:%M:%S') - [STEP] $1" >> "$LOG_FILE"
}
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "This installer needs to be run with superuser privileges."
        log_error "Please run: sudo bash install.sh"
        exit 1
    fi
}
check_system() {
    if [[ ! -f /etc/os-release ]]; then
        log_error "Could not determine OS type"
        exit 1
    fi
    source /etc/os-release
    if grep -qs "ubuntu" /etc/os-release; then
        os="ubuntu"
        os_version=$(grep 'VERSION_ID' /etc/os-release | cut -d '"' -f 2 | tr -d '.')
        group_name="nogroup"
    elif [[ -e /etc/debian_version ]]; then
        os="debian"
        os_version=$(grep -oE '[0-9]+' /etc/debian_version | head -1)
        group_name="nogroup"
    elif [[ -e /etc/almalinux-release || -e /etc/rocky-release || -e /etc/centos-release ]]; then
        os="centos"
        os_version=$(grep -shoE '[0-9]+' /etc/almalinux-release /etc/rocky-release /etc/centos-release | head -1)
        group_name="nobody"
    elif [[ -e /etc/fedora-release ]]; then
        os="fedora"
        os_version=$(grep -oE '[0-9]+' /etc/fedora-release | head -1)
        group_name="nobody"
    else
        log_error "This installer seems to be running on an unsupported distribution."
        log_error "Supported distros are Ubuntu, Debian, AlmaLinux, Rocky Linux, CentOS and Fedora."
        exit 1
    fi
    if [[ "$os" == "ubuntu" && "$os_version" -lt 2204 ]]; then
        log_error "Ubuntu 22.04 or higher is required"
        exit 1
    fi
    if [[ "$os" == "debian" && "$os_version" -lt 11 ]]; then
        log_error "Debian 11 or higher is required"
        exit 1
    fi
    if [[ "$os" == "centos" && "$os_version" -lt 9 ]]; then
        log_error "CentOS/Rocky/AlmaLinux 9 or higher is required"
        exit 1
    fi
    log_info "Detected OS: $os $os_version"
}
check_tun() {
    if [[ ! -e /dev/net/tun ]] || ! ( exec 7<>/dev/net/tun ) 2>/dev/null; then
        log_error "The system does not have the TUN device available."
        log_error "TUN needs to be enabled before running this installer."
        exit 1
    fi
}
prompt_server_ip() {
    echo ""
    log_step "Server IP/Domain Configuration"
    echo ""
    if [[ $(ip -4 addr | grep inet | grep -vEc '127(\.[0-9]{1,3}){3}') -eq 1 ]]; then
        DETECTED_IP=$(ip -4 addr | grep inet | grep -vE '127(\.[0-9]{1,3}){3}' | cut -d '/' -f 1 | grep -oE '[0-9]{1,3}(\.[0-9]{1,3}){3}')
    else
        DETECTED_IP=$(ip -4 addr | grep inet | grep -vE '127(\.[0-9]{1,3}){3}' | cut -d '/' -f 1 | grep -oE '[0-9]{1,3}(\.[0-9]{1,3}){3}' | head -1)
    fi
    echo -e "${YELLOW}Enter your server IP address or domain name:${NC}"
    echo -e "${YELLOW}This will be used in client configuration files.${NC}"
    echo -e "${YELLOW}Detected IP: ${CYAN}$DETECTED_IP${NC}"
    echo ""
    read -p "Server IP or Domain [$DETECTED_IP]: " SERVER_IP
    if [[ -z "$SERVER_IP" ]]; then
        SERVER_IP="$DETECTED_IP"
    fi
    if [[ -z "$SERVER_IP" ]]; then
        log_error "Server IP cannot be empty"
        exit 1
    fi
    log_info "Server IP/Domain set to: $SERVER_IP"
    mkdir -p /opt/irangate
    cat > /opt/irangate/server_config.json << EOF
{
  "server_ip": "$SERVER_IP",
  "server_domain": "",
  "auto_detect_ip": false,
  "last_updated": "$(date -Iseconds)",
  "detected_ips": {
    "primary": "$SERVER_IP",
    "public": "",
    "local": "$DETECTED_IP"
  },
  "settings": {
    "use_domain": false,
    "fallback_to_ip": true,
    "update_interval": 300
  }
}
EOF
    chmod 644 /opt/irangate/server_config.json
    log_info "Server configuration saved to /opt/irangate/server_config.json"
}
prompt_openvpn_config() {
    echo ""
    log_step "OpenVPN Configuration"
    echo ""
    echo -e "${YELLOW}Which protocol should OpenVPN use?${NC}"
    echo "   1) UDP (recommended)"
    echo "   2) TCP"
    read -p "Protocol [1]: " PROTOCOL
    until [[ -z "$PROTOCOL" || "$PROTOCOL" =~ ^[12]$ ]]; do
        echo "$PROTOCOL: invalid selection."
        read -p "Protocol [1]: " PROTOCOL
    done
    case "$PROTOCOL" in
        1|"") PROTOCOL="udp" ;;
        2) PROTOCOL="tcp" ;;
    esac
    echo ""
    echo -e "${YELLOW}What port should OpenVPN listen on?${NC}"
    read -p "Port [1194]: " PORT
    until [[ -z "$PORT" || "$PORT" =~ ^[0-9]+$ && "$PORT" -le 65535 ]]; do
        echo "$PORT: invalid port."
        read -p "Port [1194]: " PORT
    done
    [[ -z "$PORT" ]] && PORT="1194"
    echo ""
    echo -e "${YELLOW}Select a DNS server for the clients:${NC}"
    echo "   1) Google (8.8.8.8, 8.8.4.4)"
    echo "   2) Cloudflare (1.1.1.1, 1.0.0.1)"
    echo "   3) OpenDNS (208.67.222.222)"
    echo "   4) Quad9 (9.9.9.9)"
    echo "   5) System resolvers"
    read -p "DNS server [2]: " DNS
    until [[ -z "$DNS" || "$DNS" =~ ^[1-5]$ ]]; do
        echo "$DNS: invalid selection."
        read -p "DNS server [2]: " DNS
    done
    [[ -z "$DNS" ]] && DNS="2"
    echo ""
    echo -e "${YELLOW}Enter a name for the first client:${NC}"
    read -p "Name [client]: " CLIENT_NAME
    [[ -z "$CLIENT_NAME" ]] && CLIENT_NAME="client"
    log_info "OpenVPN configuration complete"
    log_info "  Protocol: $PROTOCOL"
    log_info "  Port: $PORT"
    log_info "  DNS: $DNS"
    log_info "  Client name: $CLIENT_NAME"
}
prompt_webpanel_config() {
    echo ""
    log_step "Web Panel Configuration"
    echo ""
    echo -e "${YELLOW}Enter the web path where the panel should be installed:${NC}"
    echo -e "${YELLOW}(e.g., 'panel' or 'admin' or leave empty for root)${NC}"
    echo -e "${YELLOW}This is used to bypass censorship scanning systems.${NC}"
    read -p "Web Path (press Enter for root '/'): " WEB_PATH
    WEB_PATH=$(echo "$WEB_PATH" | tr -d '[:space:]')
    if [[ -z "$WEB_PATH" ]]; then
        WEB_PATH=""
    else
        WEB_PATH=$(echo "$WEB_PATH" | sed 's|^/||' | sed 's|/$||')
    fi
    log_info "Web Path set to: /$WEB_PATH"
}
create_irangate_directories() {
    log_info "Creating IranGate directory structure..."
    mkdir -p /opt/irangate/database
    mkdir -p /opt/irangate/database/clients
    mkdir -p /opt/irangate/database/backups
    mkdir -p /opt/irangate/database/history
    mkdir -p /opt/irangate/clients
    mkdir -p /opt/irangate/webpanel/frontend
    mkdir -p /opt/irangate/webpanel/devices
    mkdir -p /opt/irangate/webpanel/backups
    mkdir -p /opt/irangate/webpanel/templates
    mkdir -p /opt/irangate/traffic
    mkdir -p /var/log/irangate
    mkdir -p /var/log/openvpn
    mkdir -p /etc/irangate
    mkdir -p /etc/openvpn/server
    mkdir -p /etc/openvpn/client
    mkdir -p /etc/openvpn/client-templates
    chmod 700 /opt/irangate
    chmod 700 /etc/irangate
    chmod 755 /opt/irangate/clients
    chmod 755 /var/log/irangate
    chmod 755 /var/log/openvpn
    chmod 755 /etc/openvpn/client
    log_info "Directory structure created successfully"
}
install_dependencies() {
    log_info "Installing system dependencies..."
    if [[ "$os" = "debian" || "$os" = "ubuntu" ]]; then
        if ! apt-get update -qq; then
            log_error "Failed to update package list"
            exit 1
        fi
        if ! apt-get install -y -qq \
            wget curl openvpn easy-rsa openssl ca-certificates \
            iptables jq sqlite3 net-tools git build-essential \
            procps python3 python3-pip python3-venv python3-dev \
            nginx certbot python3-certbot-nginx htop nano vim tree supervisor rsync cron logrotate; then
            log_error "Failed to install system dependencies"
            exit 1
        fi
        # Verify critical packages
        if ! command -v openvpn &> /dev/null; then
            log_error "OpenVPN installation failed"
            exit 1
        fi
        if [[ ! -d "/usr/share/easy-rsa" ]]; then
            log_error "easy-rsa installation failed"
            exit 1
        fi
    elif [[ "$os" = "centos" ]]; then
        if ! dnf install -y epel-release; then
            log_error "Failed to install EPEL repository"
            exit 1
        fi
        if ! dnf install -y openvpn easy-rsa openssl ca-certificates tar \
            iptables jq sqlite net-tools git gcc make procps-ng \
            python3 python3-pip python3-devel nginx certbot; then
            log_error "Failed to install system dependencies"
            exit 1
        fi
        # Verify critical packages
        if ! command -v openvpn &> /dev/null; then
            log_error "OpenVPN installation failed"
            exit 1
        fi
        if [[ ! -d "/usr/share/easy-rsa/3" ]]; then
            log_error "easy-rsa installation failed"
            exit 1
        fi
    elif [[ "$os" = "fedora" ]]; then
        if ! dnf install -y openvpn easy-rsa openssl ca-certificates tar \
            iptables jq sqlite net-tools git gcc make procps-ng \
            python3 python3-pip python3-devel nginx certbot; then
            log_error "Failed to install system dependencies"
            exit 1
        fi
        # Verify critical packages
        if ! command -v openvpn &> /dev/null; then
            log_error "OpenVPN installation failed"
            exit 1
        fi
        if [[ ! -d "/usr/share/easy-rsa/3" ]]; then
            log_error "easy-rsa installation failed"
            exit 1
        fi
    fi
    log_info "System dependencies installed successfully"
    if [[ -f "$SCRIPT_DIR/pkg/ai/requirements.txt" ]]; then
        log_info "Installing additional Python dependencies..."
        pip3 install --user -r "$SCRIPT_DIR/pkg/ai/requirements.txt" 2>&1 | grep -v "WARNING" || log_warn "Additional dependencies installation had warnings (optional)"
    fi
}
install_golang() {
    if command -v go &> /dev/null; then
        log_info "Go is already installed: $(go version | cut -d ' ' -f 3)"
        export PATH=$PATH:/usr/local/go/bin
        return 0
    fi
    log_info "Installing Go..."
    GO_VERSION="1.21.5"
    GO_ARCH="amd64"
    wget -q -O /tmp/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz \
        https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz
    tar -C /usr/local -xzf /tmp/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz
    rm /tmp/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    if command -v go &> /dev/null; then
        log_info "Go installed successfully: $(go version | cut -d ' ' -f 3)"
    else
        log_error "Failed to install Go"
        exit 1
    fi
}
validate_pki() {
    local pki_dir="/etc/openvpn/easy-rsa"
    local missing_files=()
    
    local required_files=(
        "$pki_dir/pki/ca.crt"
        "$pki_dir/pki/issued/server.crt"
        "$pki_dir/pki/private/server.key"
        "$pki_dir/pki/dh.pem"
        "$pki_dir/pki/tc.key"
    )
    
    for file in "${required_files[@]}"; do
        if [[ ! -f "$file" ]]; then
            missing_files+=("$file")
        fi
    done
    
    if [[ ${#missing_files[@]} -eq 0 ]]; then
        return 0
    else
        log_warn "PKI validation failed. Missing files:"
        for file in "${missing_files[@]}"; do
            log_warn "  - $file"
        done
        return 1
    fi
}

setup_pki() {
    local pki_dir="/etc/openvpn/easy-rsa"
    local backup_dir="/opt/irangate/database/backups/pki-$(date +%Y%m%d-%H%M%S)"
    
    # Check if easy-rsa is installed
    if [[ ! -d "/usr/share/easy-rsa" && ! -d "/usr/share/easy-rsa/3" ]]; then
        log_error "easy-rsa is not installed. Please install it first."
        exit 1
    fi
    
    # If PKI directory exists, validate it first
    if [[ -d "$pki_dir" ]]; then
        log_info "Existing PKI directory found, validating..."
        if validate_pki; then
            log_info "PKI validation successful, using existing PKI"
            # Create symlink even for existing PKI
            if [[ -f "$pki_dir/easyrsa" && ! -f "/usr/local/bin/easyrsa" ]]; then
                ln -sf "$pki_dir/easyrsa" /usr/local/bin/easyrsa
                log_info "easyrsa symlink created at /usr/local/bin/easyrsa"
            fi
            return 0
        else
            log_warn "PKI is incomplete or corrupted. Creating backup and rebuilding..."
            mkdir -p "$backup_dir"
            cp -r "$pki_dir" "$backup_dir/" 2>/dev/null || true
            log_info "Backup created at: $backup_dir"
            rm -rf "$pki_dir"
        fi
    fi
    
    # Setup Easy-RSA
    log_info "Setting up Easy-RSA PKI..."
    mkdir -p "$pki_dir"
    
    if [[ "$os" = "debian" || "$os" = "ubuntu" ]]; then
        if [[ -d "/usr/share/easy-rsa" ]]; then
            cp -r /usr/share/easy-rsa/* "$pki_dir/" 2>/dev/null || {
                log_error "Failed to copy easy-rsa files"
                exit 1
            }
        else
            log_error "easy-rsa not found at /usr/share/easy-rsa"
            exit 1
        fi
    elif [[ "$os" = "centos" || "$os" = "fedora" ]]; then
        if [[ -d "/usr/share/easy-rsa/3" ]]; then
            cp -r /usr/share/easy-rsa/3/* "$pki_dir/" 2>/dev/null || {
                log_error "Failed to copy easy-rsa files"
                exit 1
            }
        else
            log_error "easy-rsa not found at /usr/share/easy-rsa/3"
            exit 1
        fi
    fi
    
    cd "$pki_dir" || {
        log_error "Failed to change directory to $pki_dir"
        exit 1
    }
    
    # Initialize PKI
    log_info "Initializing PKI..."
    if ! ./easyrsa init-pki << EOF
EOF
    then
        log_error "Failed to initialize PKI"
        exit 1
    fi
    
    # Build CA
    log_info "Building Certificate Authority..."
    if ! ./easyrsa --batch build-ca nopass; then
        log_error "Failed to build CA certificate"
        exit 1
    fi
    
    # Generate DH parameters
    log_info "Generating DH parameters (this may take a few minutes)..."
    if ! ./easyrsa gen-dh; then
        log_error "Failed to generate DH parameters"
        exit 1
    fi
    
    # Build server certificate
    log_info "Building server certificate..."
    if ! ./easyrsa --batch build-server-full server nopass; then
        log_error "Failed to build server certificate"
        exit 1
    fi
    
    # Generate TLS crypt key
    log_info "Generating TLS crypt key..."
    if ! openvpn --genkey --secret "$pki_dir/pki/tc.key"; then
        log_error "Failed to generate TLS crypt key"
        exit 1
    fi
    
    # Validate PKI after setup
    if ! validate_pki; then
        log_error "PKI setup completed but validation failed"
        exit 1
    fi
    
    # Create symlink for easyrsa command so web panel can find it
    log_info "Creating easyrsa symlink for easy access..."
    if [[ -f "$pki_dir/easyrsa" && ! -f "/usr/local/bin/easyrsa" ]]; then
        ln -sf "$pki_dir/easyrsa" /usr/local/bin/easyrsa
        log_info "easyrsa symlink created at /usr/local/bin/easyrsa"
    elif [[ -f "/usr/local/bin/easyrsa" ]]; then
        log_info "easyrsa already exists at /usr/local/bin/easyrsa"
    fi
    
    log_info "PKI setup completed successfully"
}

install_openvpn_core() {
    log_step "Step 1/5: Installing OpenVPN Server Core..."
    
    if ! command -v openvpn &> /dev/null; then
        log_error "OpenVPN is not installed"
        exit 1
    fi
    
    mkdir -p /etc/openvpn/server
    mkdir -p /etc/openvpn/client
    mkdir -p /etc/openvpn/ccd
    mkdir -p /etc/openvpn/logs
    
    setup_pki
    case "$DNS" in
        1) DNS1="8.8.8.8"; DNS2="8.8.4.4" ;;
        2) DNS1="1.1.1.1"; DNS2="1.0.0.1" ;;
        3) DNS1="208.67.222.222"; DNS2="208.67.220.220" ;;
        4) DNS1="9.9.9.9"; DNS2="149.112.112.112" ;;
        5)
            DNS_SERVERS=$(grep -E "^nameserver" /etc/resolv.conf | awk '{print $2}' | head -2)
            DNS1=$(echo "$DNS_SERVERS" | head -1)
            DNS2=$(echo "$DNS_SERVERS" | tail -1)
            [[ -z "$DNS2" ]] && DNS2="8.8.8.8"
            ;;
    esac
    log_info "Creating OpenVPN server configuration..."
    
    if [[ "$PROTOCOL" == "udp" ]]; then
        cat > /etc/openvpn/server.conf << EOF
port $PORT
proto $PROTOCOL
dev tun
ca /etc/openvpn/easy-rsa/pki/ca.crt
cert /etc/openvpn/easy-rsa/pki/issued/server.crt
key /etc/openvpn/easy-rsa/pki/private/server.key
dh /etc/openvpn/easy-rsa/pki/dh.pem
tls-crypt /etc/openvpn/easy-rsa/pki/tc.key
server 10.8.0.0 255.255.255.0
ifconfig-pool-persist /etc/openvpn/ipp.txt
push "redirect-gateway def1 bypass-dhcp"
push "dhcp-option DNS $DNS1"
push "dhcp-option DNS $DNS2"
client-config-dir /etc/openvpn/ccd
duplicate-cn
management 127.0.0.1 7505
keepalive 10 120
cipher AES-256-GCM
auth SHA256
user nobody
group $group_name
persist-key
persist-tun
status /etc/openvpn/openvpn-status.log
log-append /etc/openvpn/logs/openvpn.log
verb 3
explicit-exit-notify 1
EOF
    else
        cat > /etc/openvpn/server.conf << EOF
port $PORT
proto $PROTOCOL
dev tun
ca /etc/openvpn/easy-rsa/pki/ca.crt
cert /etc/openvpn/easy-rsa/pki/issued/server.crt
key /etc/openvpn/easy-rsa/pki/private/server.key
dh /etc/openvpn/easy-rsa/pki/dh.pem
tls-crypt /etc/openvpn/easy-rsa/pki/tc.key
server 10.8.0.0 255.255.255.0
ifconfig-pool-persist /etc/openvpn/ipp.txt
push "redirect-gateway def1 bypass-dhcp"
push "dhcp-option DNS $DNS1"
push "dhcp-option DNS $DNS2"
client-config-dir /etc/openvpn/ccd
duplicate-cn
management 127.0.0.1 7505
keepalive 10 120
cipher AES-256-GCM
auth SHA256
user nobody
group $group_name
persist-key
persist-tun
status /etc/openvpn/openvpn-status.log
log-append /etc/openvpn/logs/openvpn.log
verb 3
EOF
    fi
    log_info "Validating PKI certificates..."
    if ! validate_pki; then
        log_error "PKI validation failed. Cannot proceed with OpenVPN setup."
        exit 1
    fi
    log_info "All PKI certificates validated successfully"
    
    log_info "Testing OpenVPN configuration..."
    if openvpn --config /etc/openvpn/server.conf --test-crypto > /tmp/openvpn-test.log 2>&1; then
        log_info "OpenVPN configuration test passed"
    else
        log_error "OpenVPN configuration test failed!"
        log_error "Configuration errors:"
        cat /tmp/openvpn-test.log | tail -20 || true
        log_warn "Attempting to continue anyway..."
    fi
    
    log_info "Setting certificate file permissions..."
    chmod 644 /etc/openvpn/easy-rsa/pki/ca.crt 2>/dev/null || true
    chmod 644 /etc/openvpn/easy-rsa/pki/issued/server.crt 2>/dev/null || true
    chmod 600 /etc/openvpn/easy-rsa/pki/private/server.key 2>/dev/null || true
    chmod 644 /etc/openvpn/easy-rsa/pki/dh.pem 2>/dev/null || true
    chmod 600 /etc/openvpn/easy-rsa/pki/tc.key 2>/dev/null || true
    chmod o+x /etc/openvpn/easy-rsa/ 2>/dev/null || true
    log_info "Configuring IP forwarding..."
    echo 'net.ipv4.ip_forward=1' | tee /etc/sysctl.d/99-openvpn-forward.conf > /dev/null
    if sysctl -p /etc/sysctl.d/99-openvpn-forward.conf > /dev/null 2>&1 || sysctl -p > /dev/null 2>&1; then
        if ! sysctl -n net.ipv4.ip_forward | grep -q "1"; then
            log_error "Failed to enable IP forwarding"
            exit 1
        fi
        log_info "IP forwarding verified: $(sysctl -n net.ipv4.ip_forward)"
    else
        log_error "Failed to apply IP forwarding configuration"
        exit 1
    fi
    if command -v getenforce &> /dev/null && [[ "$(getenforce)" == "Enforcing" ]]; then
        log_info "Configuring SELinux for OpenVPN..."
        if command -v setsebool &> /dev/null; then
            setsebool -P domain_can_mmap_files 1 2>/dev/null || true
        fi
        if command -v semanage &> /dev/null; then
            semanage port -a -t openvpn_port_t -p $PROTOCOL $PORT 2>/dev/null || \
            semanage port -m -t openvpn_port_t -p $PROTOCOL $PORT 2>/dev/null || true
        fi
        log_info "SELinux configuration completed"
    fi
    log_info "Configuring firewall rules..."
    NAT_INTERFACE=$(ip route | grep default | awk '{print $5}' | head -1)
    if [[ -z "$NAT_INTERFACE" ]]; then
        NAT_INTERFACE=$(ip route show | grep -E "^default" | awk '{print $5}' | head -1)
    fi
    cat > /etc/systemd/system/openvpn-iptables.service << EOF
[Unit]
After=network-online.target
Wants=network-online.target
[Service]
Type=oneshot
ExecStart=/usr/sbin/iptables -t nat -A POSTROUTING -s 10.8.0.0/24 ! -d 10.8.0.0/24 -o $NAT_INTERFACE -j MASQUERADE
ExecStart=/usr/sbin/iptables -I INPUT -p $PROTOCOL --dport $PORT -j ACCEPT
ExecStart=/usr/sbin/iptables -I FORWARD -s 10.8.0.0/24 -j ACCEPT
ExecStart=/usr/sbin/iptables -I FORWARD -m state --state RELATED,ESTABLISHED -j ACCEPT
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable openvpn-iptables.service
    if ! systemctl start openvpn-iptables.service; then
        log_error "Failed to start openvpn-iptables service"
        exit 1
    fi
    sleep 2
    if ! systemctl is-active --quiet openvpn-iptables.service; then
        log_error "openvpn-iptables service is not running"
        systemctl status openvpn-iptables.service --no-pager -l
        exit 1
    fi
    log_info "IPTables service started successfully"
    
    log_info "Testing OpenVPN configuration..."
    if timeout 5 openvpn --config /etc/openvpn/server.conf --test-crypto > /tmp/openvpn-test.log 2>&1; then
        log_info "OpenVPN configuration test passed"
    else
        local test_status=$?
        log_warn "OpenVPN --test-crypto completed with status: $test_status"
        if grep -q "Options error" /tmp/openvpn-test.log; then
            log_error "OpenVPN configuration has errors:"
            cat /tmp/openvpn-test.log | grep -i "error" | head -10
            exit 1
        else
            log_info "Configuration syntax appears valid"
        fi
    fi
    
    cat > /etc/systemd/system/openvpn@server.service << EOF
[Unit]
Description=OpenVPN connection to %i
Documentation=man:openvpn(8)
After=network-online.target
Wants=network-online.target
[Service]
Type=notify
PrivateTmp=true
WorkingDirectory=/etc/openvpn
ExecStart=/usr/sbin/openvpn --config /etc/openvpn/server.conf
CapabilityBoundingSet=CAP_IPC_LOCK CAP_NET_ADMIN CAP_NET_RAW CAP_SETGID CAP_SETUID CAP_SYS_CHROOT CAP_DAC_OVERRIDE
LimitNPROC=10
DeviceAllow=/dev/null rw
DeviceAllow=/dev/net/tun rw
ProtectSystem=true
ProtectHome=true
KillMode=process
RestartSec=5s
Restart=on-failure
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    
    systemctl stop openvpn@server 2>/dev/null || true
    ensure_openvpn_privileged_port_capability
    install_connection_guard
    systemctl enable openvpn@server
    log_info "Starting OpenVPN service..."
    if ! systemctl start openvpn@server; then
        log_error "Failed to start openvpn@server service"
        log_warn "Checking service status..."
        systemctl status openvpn@server.service --no-pager -l || true
        
        log_warn "Checking OpenVPN logs..."
        if [[ -f "/etc/openvpn/logs/openvpn.log" ]]; then
            log_warn "OpenVPN log file contents:"
            tail -30 /etc/openvpn/logs/openvpn.log || true
        fi
        
        log_warn "Checking journalctl logs..."
        journalctl -u openvpn@server.service --no-pager -n 30 || true
        
        log_warn "Checking for common configuration issues..."
        if ! grep -q "^ca " /etc/openvpn/server.conf; then
            log_error "Missing 'ca' directive in server.conf"
        fi
        if ! grep -q "^cert " /etc/openvpn/server.conf; then
            log_error "Missing 'cert' directive in server.conf"
        fi
        if ! grep -q "^key " /etc/openvpn/server.conf; then
            log_error "Missing 'key' directive in server.conf"
        fi
        
        log_warn "OpenVPN service failed to start. Continuing installation, but please check the logs manually."
        log_warn "You can try starting manually with: openvpn --config /etc/openvpn/server.conf"
    else
        sleep 3
        if ! systemctl is-active --quiet openvpn@server; then
            log_warn "OpenVPN service started but is not currently active"
            systemctl status openvpn@server.service --no-pager -l || true
            if [[ -f "/etc/openvpn/logs/openvpn.log" ]]; then
                log_warn "Recent OpenVPN log entries:"
                tail -20 /etc/openvpn/logs/openvpn.log || true
            fi
        else
            log_info "OpenVPN service is running successfully"
        fi
    fi
    log_info "Creating first client: $CLIENT_NAME"
    cd /etc/openvpn/easy-rsa || {
        log_error "Failed to change directory to /etc/openvpn/easy-rsa"
        exit 1
    }
    if [[ ! -f "pki/issued/${CLIENT_NAME}.crt" ]]; then
        log_info "Generating client certificate for $CLIENT_NAME..."
        if ! ./easyrsa --batch build-client-full "$CLIENT_NAME" nopass; then
            log_error "Failed to generate client certificate"
            exit 1
        fi
        # Verify client certificate was created
        if [[ ! -f "pki/issued/${CLIENT_NAME}.crt" ]]; then
            log_error "Client certificate file was not created"
            exit 1
        fi
        if [[ ! -f "pki/private/${CLIENT_NAME}.key" ]]; then
            log_error "Client key file was not created"
            exit 1
        fi
        # Read certificate files with error checking
        local ca_crt=$(cat pki/ca.crt 2>/dev/null)
        local client_crt=$(cat pki/issued/${CLIENT_NAME}.crt 2>/dev/null)
        local client_key=$(cat pki/private/${CLIENT_NAME}.key 2>/dev/null)
        local tc_key=$(cat pki/tc.key 2>/dev/null)
        
        if [[ -z "$ca_crt" ]]; then
            log_error "Failed to read CA certificate"
            exit 1
        fi
        if [[ -z "$client_crt" ]]; then
            log_error "Failed to read client certificate"
            exit 1
        fi
        if [[ -z "$client_key" ]]; then
            log_error "Failed to read client key"
            exit 1
        fi
        if [[ -z "$tc_key" ]]; then
            log_error "Failed to read TLS crypt key"
            exit 1
        fi
        
        cat > /opt/irangate/clients/${CLIENT_NAME}.ovpn << CLIENT_EOF
client
dev tun
proto $PROTOCOL
remote $SERVER_IP $PORT
resolv-retry infinite
nobind
persist-key
persist-tun
remote-cert-tls server
cipher AES-256-GCM
auth SHA256
verb 3
<ca>
$ca_crt
</ca>
<cert>
$client_crt
</cert>
<key>
$client_key
</key>
<tls-crypt>
$tc_key
</tls-crypt>
CLIENT_EOF
        
        if [[ ! -f "/opt/irangate/clients/${CLIENT_NAME}.ovpn" ]]; then
            log_error "Failed to create client configuration file"
            exit 1
        fi
        
        chmod 644 /opt/irangate/clients/${CLIENT_NAME}.ovpn
        log_info "Client configuration saved to: /opt/irangate/clients/${CLIENT_NAME}.ovpn"
    else
        log_info "Client certificate for $CLIENT_NAME already exists, skipping..."
    fi
    cat > /opt/irangate/database/settings.json << EOF
{
  "server_ip": "$SERVER_IP",
  "server_port": $PORT,
  "protocol": "$PROTOCOL",
  "install_date": "$(date -Iseconds)",
  "version": "3.0.0",
  "backup_path": "/opt/irangate/database/backups/",
  "client_dir": "/opt/irangate/clients"
}
EOF
    chmod 600 /opt/irangate/database/settings.json
    if [[ ! -f /etc/irangate/traffic_config.json ]]; then
        cat > /etc/irangate/traffic_config.json << EOF
{
  "data_dir": "/opt/irangate/traffic",
  "collection_interval_seconds": 10,
  "retention_days": 90,
  "status_log_path": "/etc/openvpn/openvpn-status.log",
  "enable_quota_tracking": true,
  "alert_threshold_percent": 80.0,
  "rollup_interval_hours": 1,
  "batch_size": 100,
  "max_concurrent_writes": 10
}
EOF
        chmod 644 /etc/irangate/traffic_config.json
        log_info "Traffic config initialized"
    fi
    touch /etc/openvpn/openvpn-status.log
    chmod 644 /etc/openvpn/openvpn-status.log
    chown nobody:$group_name /etc/openvpn/openvpn-status.log
    log_info "OpenVPN Server Core installed and configured successfully"
}
install_irangate_cli() {
    log_step "Step 2/5: Installing IranGate CLI..."
    cd "$SCRIPT_DIR" || {
        log_error "Failed to change directory to $SCRIPT_DIR"
        exit 1
    }
    export PATH=$PATH:/usr/local/go/bin
    
    # Check if go.mod exists
    if [[ ! -f "go.mod" ]]; then
        log_error "go.mod not found in $SCRIPT_DIR"
        log_error "Please ensure you're running the installer from the correct directory"
        exit 1
    fi
    
    # Verify Go is working
    if ! command -v go &> /dev/null; then
        log_error "Go is not in PATH. Trying to source /etc/profile..."
        source /etc/profile 2>/dev/null || true
        export PATH=$PATH:/usr/local/go/bin
        if ! command -v go &> /dev/null; then
            log_error "Go is still not available. Please check Go installation."
            exit 1
        fi
    fi
    
    log_info "Using Go: $(go version)"
    
    # Set Go proxy if needed (for environments with restricted internet)
    export GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
    export GOSUMDB=${GOSUMDB:-sum.golang.org}
    
    log_info "Go environment: GOPROXY=$GOPROXY, GOSUMDB=$GOSUMDB"
    
    log_info "Downloading Go dependencies..."
    log_info "Go version: $(go version 2>&1 || echo 'unknown')"
    log_info "GOPROXY: $GOPROXY"
    
    # Try downloading with better error handling
    log_info "Running go mod download..."
    local download_output
    download_output=$(go mod download 2>&1)
    local download_status=$?
    
    if [[ $download_status -eq 0 ]]; then
        log_info "Go dependencies downloaded successfully"
        # Check for warnings in output
        if echo "$download_output" | grep -qi "WARNING\|ERROR"; then
            log_warn "Some warnings during dependency download:"
            echo "$download_output" | grep -i "WARNING\|ERROR" | head -10 || true
        fi
    else
        log_warn "go mod download returned non-zero status, checking output..."
        log_info "Download output:"
        echo "$download_output" | grep -v "^go:" || echo "$download_output"
        
        # Check for common issues
        log_info "Checking network connectivity to Go proxy..."
        if ! curl -s --connect-timeout 5 https://proxy.golang.org > /dev/null 2>&1; then
            log_warn "Cannot reach Go proxy. Trying direct mode..."
            export GOPROXY=direct
            log_info "Retrying with GOPROXY=direct..."
            download_output=$(go mod download 2>&1)
            download_status=$?
        fi
        
        # Try go mod tidy to fix issues
        log_info "Running go mod tidy to fix dependency issues..."
        go mod tidy 2>&1 | grep -v "^go:" || true
        
        # Try download again after tidy
        log_info "Retrying go mod download after tidy..."
        download_output=$(go mod download 2>&1)
        download_status=$?
        
        if [[ $download_status -ne 0 ]]; then
            log_error "Failed to download Go dependencies after retries"
            log_error "Last output:"
            echo "$download_output" | grep -v "^go:" || echo "$download_output"
            log_error "Please check:"
            log_error "  1. Internet connectivity"
            log_error "  2. Go proxy accessibility"
            log_error "  3. go.mod file integrity"
            exit 1
        else
            log_info "Go dependencies resolved after retry"
        fi
    fi
    # Check if main.go exists
    if [[ ! -f "$SCRIPT_DIR/main.go" ]]; then
        log_error "main.go not found in $SCRIPT_DIR"
        exit 1
    fi
    
    log_info "Building IranGate CLI..."
    if go build -o /usr/local/bin/irangate ./main.go 2>&1; then
        chmod +x /usr/local/bin/irangate
        if [[ ! -f /usr/local/bin/irangate ]]; then
            log_error "irangate binary was not created at /usr/local/bin/irangate"
            exit 1
        fi
        log_info "IranGate CLI installed successfully at /usr/local/bin/irangate"
        # Verify the binary works
        if /usr/local/bin/irangate version &> /dev/null; then
            log_info "CLI verification successful"
            /usr/local/bin/irangate version
        else
            log_warn "irangate binary exists but version command failed"
        fi
        # Ensure /usr/local/bin is in PATH for current session
        if [[ ":$PATH:" != *":/usr/local/bin:"* ]]; then
            export PATH="$PATH:/usr/local/bin"
            log_info "Added /usr/local/bin to PATH for current session"
        fi
    else
        log_error "Failed to build IranGate CLI"
        log_error "Please check Go installation and source code"
        exit 1
    fi
}
install_webpanel() {
    log_step "Step 3/5: Installing IranGate Web Panel..."
    if [[ -d "$SCRIPT_DIR/webpanel/backend" ]]; then
        log_info "Building Web Panel backend..."
        cd "$SCRIPT_DIR/webpanel/backend" || {
            log_error "Failed to change directory to $SCRIPT_DIR/webpanel/backend"
            exit 1
        }
        export PATH=$PATH:/usr/local/go/bin
        
        # Check if go.mod exists
        if [[ ! -f "go.mod" ]]; then
            log_warn "go.mod not found, initializing..."
            go mod init irangate-webpanel || log_warn "go mod init failed (may already exist)"
        fi
        
        log_info "Downloading Go dependencies..."
        go mod download 2>&1 | grep -v "^go:" || true
        go mod tidy
        
        log_info "Building webpanel binary..."
        if go build -o /usr/local/bin/irangate-webpanel . 2>&1; then
            chmod +x /usr/local/bin/irangate-webpanel
            log_info "Web Panel backend built successfully"
        else
            log_error "Failed to build Web Panel backend"
            exit 1
        fi
    else
        log_error "Web Panel backend directory not found"
        exit 1
    fi
    if [[ -d "$SCRIPT_DIR/webpanel/frontend" ]]; then
        log_info "Copying frontend files..."
        cp -r "$SCRIPT_DIR/webpanel/frontend"/* /opt/irangate/webpanel/frontend/ 2>/dev/null || true
    fi
    log_info "Creating Web Panel environment configuration..."
    cat > /etc/irangate/webpanel.env << EOF
WEBPANEL_PORT=$WEBPANEL_PORT
WEBPANEL_BASEPATH=/$WEB_PATH
WEBPANEL_STATIC_DIR=/opt/irangate/webpanel/frontend
WEBPANEL_ADMIN_USER=$ADMIN_USER
WEBPANEL_ADMIN_PASS=$ADMIN_PASS
WEBPANEL_ADMIN_EMAIL=admin@irangate.local
EOF
    chmod 600 /etc/irangate/webpanel.env
    log_info "Creating Web Panel systemd service..."
    cat > /etc/systemd/system/irangate-webpanel.service << EOF
[Unit]
Description=IranGate Web Panel
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
EnvironmentFile=/etc/irangate/webpanel.env
ExecStart=/usr/local/bin/irangate-webpanel
User=root
Restart=on-failure
RestartSec=3
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable irangate-webpanel
    if ! systemctl start irangate-webpanel; then
        log_error "Failed to start irangate-webpanel service"
        log_warn "Checking service status..."
        systemctl status irangate-webpanel.service --no-pager -l || true
        log_warn "Checking logs..."
        journalctl -u irangate-webpanel.service --no-pager -n 20 || true
        log_warn "Web Panel service failed to start. Continuing installation, but please check the logs manually."
    else
        sleep 3
        if ! systemctl is-active --quiet irangate-webpanel; then
            log_warn "Web Panel service started but is not currently active"
            systemctl status irangate-webpanel.service --no-pager -l || true
        else
            log_info "Web Panel service is running successfully"
        fi
    fi
    if [[ ! -f /etc/irangate/traffic_config.json ]]; then
        cat > /etc/irangate/traffic_config.json << EOF
{
  "data_dir": "/opt/irangate/traffic",
  "collection_interval_seconds": 10,
  "retention_days": 90,
  "status_log_path": "/etc/openvpn/openvpn-status.log",
  "enable_quota_tracking": true,
  "alert_threshold_percent": 80.0,
  "rollup_interval_hours": 1,
  "batch_size": 100,
  "max_concurrent_writes": 10
}
EOF
        chmod 644 /etc/irangate/traffic_config.json
    fi
    log_info "IranGate Web Panel installed successfully"
}
test_services() {
    log_step "Step 4/5: Testing all services..."
    echo ""
    local all_ok=true
    echo -e "${CYAN}Testing OpenVPN service...${NC}"
    if systemctl is-active --quiet openvpn@server; then
        echo -e "${GREEN}✓${NC} OpenVPN: ${GREEN}ACTIVE${NC}"
    else
        echo -e "${RED}✗${NC} OpenVPN: ${RED}INACTIVE${NC}"
        all_ok=false
    fi
    echo -e "${CYAN}Testing IranGate CLI...${NC}"
    if /usr/local/bin/irangate version &> /dev/null; then
        echo -e "${GREEN}✓${NC} IranGate CLI: ${GREEN}WORKING${NC}"
        /usr/local/bin/irangate version | head -1
    else
        echo -e "${RED}✗${NC} IranGate CLI: ${RED}NOT WORKING${NC}"
        all_ok=false
    fi
    echo -e "${CYAN}Testing Web Panel service...${NC}"
    if systemctl is-active --quiet irangate-webpanel; then
        echo -e "${GREEN}✓${NC} Web Panel: ${GREEN}ACTIVE${NC}"
        if curl -s -f -o /dev/null http://127.0.0.1:$WEBPANEL_PORT/; then
            echo -e "${GREEN}✓${NC} Web Panel HTTP: ${GREEN}RESPONDING${NC}"
        else
            echo -e "${YELLOW}⚠${NC} Web Panel HTTP: ${YELLOW}NOT RESPONDING (may need a moment)${NC}"
        fi
    else
        echo -e "${RED}✗${NC} Web Panel: ${RED}INACTIVE${NC}"
        all_ok=false
    fi
    if command -v nginx &> /dev/null; then
        echo -e "${CYAN}Testing Nginx...${NC}"
        if systemctl is-active --quiet nginx; then
            echo -e "${GREEN}✓${NC} Nginx: ${GREEN}ACTIVE${NC}"
        else
            echo -e "${YELLOW}⚠${NC} Nginx: ${YELLOW}INACTIVE (optional)${NC}"
        fi
    fi
    echo ""
    if [[ "$all_ok" == true ]]; then
        log_info "All core services are running properly!"
        return 0
    else
        log_warn "Some services are not running. Please check the logs."
        return 1
    fi
}
show_final_summary() {
    log_step "Step 5/5: Installation Complete!"
    echo ""
    echo -e "${GREEN}╔══════════════════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║                    ${CYAN}🎉 Installation Successful! 🎉${GREEN}                    ║${NC}"
    echo -e "${GREEN}╚══════════════════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    local server_ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    server_ip=${server_ip:-127.0.0.1}
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                         📋 Installation Summary${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "${YELLOW}OpenVPN Server:${NC}"
    echo -e "  ${GREEN}✓${NC} Server IP/Domain: ${CYAN}$SERVER_IP${NC}"
    echo -e "  ${GREEN}✓${NC} Protocol: ${CYAN}$PROTOCOL${NC}"
    echo -e "  ${GREEN}✓${NC} Port: ${CYAN}$PORT${NC}"
    echo -e "  ${GREEN}✓${NC} Status: $(systemctl is-active openvpn@server)"
    echo ""
    echo -e "${YELLOW}Web Panel:${NC}"
    echo -e "  ${GREEN}✓${NC} URL: ${CYAN}http://$server_ip:$WEBPANEL_PORT/$WEB_PATH/${NC}"
    echo -e "  ${GREEN}✓${NC} Admin Username: ${CYAN}$ADMIN_USER${NC}"
    echo -e "  ${GREEN}✓${NC} Admin Password: ${CYAN}$ADMIN_PASS${NC}"
    echo -e "  ${GREEN}✓${NC} Status: $(systemctl is-active irangate-webpanel)"
    echo ""
    echo -e "${YELLOW}First Client:${NC}"
    echo -e "  ${GREEN}✓${NC} Name: ${CYAN}$CLIENT_NAME${NC}"
    echo -e "  ${GREEN}✓${NC} Config File: ${CYAN}/opt/irangate/clients/${CLIENT_NAME}.ovpn${NC}"
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "${MAGENTA}🚀 Quick Access Commands:${NC}"
    echo ""
    echo -e "  ${YELLOW}1.${NC} Access CLI Menu:"
    echo -e "     ${GREEN}irangate${NC} ${YELLOW}or${NC} ${GREEN}/usr/local/bin/irangate${NC}"
    echo ""
    echo -e "  ${YELLOW}2.${NC} Check CLI version:"
    echo -e "     ${GREEN}irangate version${NC} ${YELLOW}or${NC} ${GREEN}/usr/local/bin/irangate version${NC}"
    echo -e "     ${YELLOW}Note:${NC} If 'command not found', use full path or run: ${GREEN}export PATH=\$PATH:/usr/local/bin${NC}"
    echo ""
    echo -e "  ${YELLOW}3.${NC} Manage clients:"
    echo -e "     ${GREEN}irangate client list${NC}"
    echo ""
    echo -e "  ${YELLOW}4.${NC} Check service status:"
    echo -e "     ${GREEN}systemctl status irangate-webpanel${NC}"
    echo -e "     ${GREEN}systemctl status openvpn@server${NC}"
    echo ""
    echo -e "  ${YELLOW}5.${NC} View logs:"
    echo -e "     ${GREEN}journalctl -u irangate-webpanel -f${NC}"
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "${GREEN}Installation log saved to: /var/log/irangate-install.log${NC}"
    echo ""
    echo -e "${GREEN}Thank you for using IranGate VPN Management System!${NC}"
    echo -e "${GREEN}For support and documentation, visit: https://github.com/amiridev-org/irangate-ov${NC}"
    echo ""
}
main() {
    display_banner
    touch "$LOG_FILE"
    chmod 644 "$LOG_FILE"
    log_info "=== IranGate Complete Installation Started ==="
    log_info "Log file: $LOG_FILE"
    echo ""
    check_root
    check_system
    check_tun
    prompt_server_ip
    prompt_openvpn_config
    prompt_webpanel_config
    echo ""
    log_info "Starting installation process..."
    echo ""
    sleep 2
    create_irangate_directories
    install_dependencies
    install_golang
    install_openvpn_core
    install_irangate_cli
    install_webpanel
    test_services
    show_final_summary
    log_info "=== Installation Completed ==="
}
main "$@"
