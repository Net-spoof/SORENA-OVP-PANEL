#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="/etc/irangate-node-agent.env"
AGENT_DIR="/opt/irangate-node-agent"
CLIENT_DIR="$AGENT_DIR/clients"
EASYRSA_DIR="/etc/openvpn/easy-rsa"
SERVER_DIR="/etc/openvpn/server"

need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "Run as root"
    exit 1
  fi
}

detect_ip() {
  curl -4 -sS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}'
}

ask() {
  local prompt="$1"
  local def="$2"
  local ans
  read -rp "$prompt [$def]: " ans
  echo "${ans:-$def}"
}

install_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt update
  apt install -y openvpn easy-rsa curl jq python3 python3-pip iptables
}

write_env() {
  cat > "$ENV_FILE" <<EOF
NODE_TOKEN=$NODE_TOKEN
NODE_PUBLIC_HOST=$NODE_PUBLIC_HOST
NODE_OVPN_PORT=$NODE_OVPN_PORT
NODE_OVPN_PROTO=$NODE_OVPN_PROTO
NODE_LOCATION=$NODE_LOCATION
NODE_NAME=$NODE_NAME
NODE_AGENT_PORT=$NODE_AGENT_PORT
NODE_OPENVPN_LISTEN_PORT=$NODE_OPENVPN_LISTEN_PORT
EOF
}

setup_openvpn() {
  mkdir -p "$SERVER_DIR" "$CLIENT_DIR" "$AGENT_DIR/logs"

  if [ ! -d "$EASYRSA_DIR" ]; then
    rm -rf "$EASYRSA_DIR"
    make-cadir "$EASYRSA_DIR"
  fi

  cd "$EASYRSA_DIR"

  if [ ! -d pki ]; then
    ./easyrsa --batch init-pki
  fi

  if [ ! -f pki/ca.crt ]; then
    EASYRSA_REQ_CN="IranGate-Node-CA" ./easyrsa --batch build-ca nopass
  fi

  if [ ! -f pki/issued/server.crt ]; then
    ./easyrsa --batch gen-req server nopass
    ./easyrsa --batch sign-req server server
  fi

  if [ ! -f pki/dh.pem ]; then
    ./easyrsa --batch gen-dh
  fi

  if [ ! -f "$SERVER_DIR/ta.key" ]; then
    openvpn --genkey secret "$SERVER_DIR/ta.key"
  fi

  cp pki/ca.crt "$SERVER_DIR/ca.crt"
  cp pki/issued/server.crt "$SERVER_DIR/server.crt"
  cp pki/private/server.key "$SERVER_DIR/server.key"
  cp pki/dh.pem "$SERVER_DIR/dh.pem"

  cat > "$SERVER_DIR/server.conf" <<EOF
port $NODE_OPENVPN_LISTEN_PORT
proto udp
dev tun

ca $SERVER_DIR/ca.crt
cert $SERVER_DIR/server.crt
key $SERVER_DIR/server.key
dh $SERVER_DIR/dh.pem
tls-crypt $SERVER_DIR/ta.key

topology subnet
server 10.88.0.0 255.255.255.0
ifconfig-pool-persist $SERVER_DIR/ipp.txt

push "redirect-gateway def1 bypass-dhcp"
push "dhcp-option DNS 1.1.1.1"
push "dhcp-option DNS 8.8.8.8"

keepalive 10 120
cipher AES-256-GCM
data-ciphers AES-256-GCM:AES-128-GCM:CHACHA20-POLY1305
auth SHA256

user nobody
group nogroup
persist-key
persist-tun

status /etc/openvpn/openvpn-status.log
log-append /var/log/openvpn.log
verb 3
explicit-exit-notify 1
EOF
}

setup_network() {
  cat > /etc/sysctl.d/99-irangate-openvpn.conf <<'EOF'
net.ipv4.ip_forward=1
EOF
  sysctl --system >/dev/null

  cat > /etc/systemd/system/irangate-node-nat.service <<'EOF'
[Unit]
Description=IranGate OpenVPN NAT
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/bash -lc 'IFACE=$(ip route get 1.1.1.1 | awk "{print \$5; exit}"); iptables -t nat -C POSTROUTING -s 10.88.0.0/24 -o "$IFACE" -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s 10.88.0.0/24 -o "$IFACE" -j MASQUERADE'
ExecStop=/bin/bash -lc 'IFACE=$(ip route get 1.1.1.1 | awk "{print \$5; exit}"); iptables -t nat -D POSTROUTING -s 10.88.0.0/24 -o "$IFACE" -j MASQUERADE 2>/dev/null || true'

[Install]
WantedBy=multi-user.target
EOF
}

write_client_tool() {
  cat > /usr/local/bin/irangate-node-client <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-}"
NAME="${2:-}"

if [ -z "$ACTION" ] || [ -z "$NAME" ]; then
  echo "Usage: irangate-node-client create CLIENT_NAME"
  exit 1
fi

SAFE_NAME="$(echo "$NAME" | tr -cd 'A-Za-z0-9_.-')"
if [ -z "$SAFE_NAME" ]; then
  echo "Invalid client name"
  exit 1
fi

set -a
. /etc/irangate-node-agent.env
set +a

EASYRSA_DIR="/etc/openvpn/easy-rsa"
OUT_DIR="/opt/irangate-node-agent/clients"
OVPN_FILE="$OUT_DIR/$SAFE_NAME.ovpn"

mkdir -p "$OUT_DIR"
cd "$EASYRSA_DIR"

case "$ACTION" in
  create)
    if [ ! -f "pki/issued/$SAFE_NAME.crt" ]; then
      EASYRSA_REQ_CN="$SAFE_NAME" ./easyrsa --batch gen-req "$SAFE_NAME" nopass >/dev/null 2>&1
      ./easyrsa --batch sign-req client "$SAFE_NAME" >/dev/null 2>&1
    fi

    cat > "$OVPN_FILE" <<OVPN
client
dev tun
proto ${NODE_OVPN_PROTO}
remote ${NODE_PUBLIC_HOST} ${NODE_OVPN_PORT}
resolv-retry infinite
nobind
persist-key
persist-tun
remote-cert-tls server
cipher AES-256-GCM
data-ciphers AES-256-GCM:AES-128-GCM:CHACHA20-POLY1305
auth SHA256
verb 3

<ca>
$(cat /etc/openvpn/server/ca.crt)
</ca>
<cert>
$(awk '/BEGIN CERTIFICATE/{flag=1} flag {print}' "pki/issued/$SAFE_NAME.crt")
</cert>
<key>
$(cat "pki/private/$SAFE_NAME.key")
</key>
<tls-crypt>
$(cat /etc/openvpn/server/ta.key)
</tls-crypt>
OVPN

    chmod 600 "$OVPN_FILE"
    echo "$OVPN_FILE"
    ;;
  *)
    echo "Unknown action: $ACTION"
    exit 1
    ;;
esac
EOF

  chmod +x /usr/local/bin/irangate-node-client
}

write_agent() {
  cat > "$AGENT_DIR/agent.py" <<'PY'
#!/usr/bin/env python3
import json, os, re, subprocess
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

TOKEN = os.environ.get("NODE_TOKEN", "")
NODE_PUBLIC_HOST = os.environ.get("NODE_PUBLIC_HOST", "")
NODE_OVPN_PORT = os.environ.get("NODE_OVPN_PORT", "1194")
NODE_OVPN_PROTO = os.environ.get("NODE_OVPN_PROTO", "udp")
NODE_LOCATION = os.environ.get("NODE_LOCATION", "NODE")
NODE_NAME = os.environ.get("NODE_NAME", "IranGate Node")
NODE_AGENT_PORT = int(os.environ.get("NODE_AGENT_PORT", "8091"))
CLIENT_DIR = Path("/opt/irangate-node-agent/clients")

def safe_name(name):
    return re.sub(r"[^A-Za-z0-9_.-]", "", str(name or ""))

def send_json(h, code, payload):
    body = json.dumps(payload, ensure_ascii=False).encode()
    h.send_response(code)
    h.send_header("Content-Type", "application/json; charset=utf-8")
    h.send_header("Content-Length", str(len(body)))
    h.end_headers()
    h.wfile.write(body)

def send_file(h, code, content, filename):
    body = content.encode()
    h.send_response(code)
    h.send_header("Content-Type", "application/x-openvpn-profile")
    h.send_header("Content-Disposition", f'attachment; filename="{filename}"')
    h.send_header("Content-Length", str(len(body)))
    h.end_headers()
    h.wfile.write(body)

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def auth_ok(self):
        return (not TOKEN) or self.headers.get("Authorization", "") == f"Bearer {TOKEN}"

    def read_json(self):
        n = int(self.headers.get("Content-Length", "0") or 0)
        if n <= 0:
            return {}
        return json.loads(self.rfile.read(n).decode() or "{}")

    def do_GET(self):
        if not self.auth_ok():
            return send_json(self, 401, {"success": False, "message": "Unauthorized"})

        path = urlparse(self.path).path

        if path == "/status":
            return send_json(self, 200, {
                "success": True,
                "node": NODE_NAME,
                "location": NODE_LOCATION,
                "public_host": NODE_PUBLIC_HOST,
                "ovpn_port": NODE_OVPN_PORT,
                "ovpn_proto": NODE_OVPN_PROTO,
                "agent_port": NODE_AGENT_PORT,
                "status": "ok"
            })

        m = re.match(r"^/clients/([^/]+)/download$", path)
        if m:
            name = safe_name(m.group(1))
            f = CLIENT_DIR / f"{name}.ovpn"
            if not name or not f.exists():
                return send_json(self, 404, {"success": False, "message": "Client config not found"})
            return send_file(self, 200, f.read_text(), f"{name}-{NODE_LOCATION}.ovpn")

        return send_json(self, 404, {"success": False, "message": "Not found"})

    def do_POST(self):
        if not self.auth_ok():
            return send_json(self, 401, {"success": False, "message": "Unauthorized"})

        path = urlparse(self.path).path

        if path == "/clients":
            data = self.read_json()
            name = safe_name(data.get("name"))
            if not name:
                return send_json(self, 400, {"success": False, "message": "Invalid client name"})

            r = subprocess.run(
                ["/usr/local/bin/irangate-node-client", "create", name],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE
            )

            if r.returncode != 0:
                return send_json(self, 500, {
                    "success": False,
                    "message": "Client create failed",
                    "stderr": r.stderr[-2000:]
                })

            return send_json(self, 200, {
                "success": True,
                "message": "Client created",
                "name": name,
                "location": NODE_LOCATION,
                "config_path": (r.stdout.strip().splitlines()[-1] if r.stdout.strip() else ""),
                "download_path": f"/clients/{name}/download"
            })

        return send_json(self, 404, {"success": False, "message": "Not found"})

if __name__ == "__main__":
    CLIENT_DIR.mkdir(parents=True, exist_ok=True)
    ThreadingHTTPServer(("0.0.0.0", NODE_AGENT_PORT), Handler).serve_forever()
PY

  chmod +x "$AGENT_DIR/agent.py"
}

write_systemd() {
  cat > /etc/systemd/system/irangate-node-agent.service <<'EOF'
[Unit]
Description=IranGate Node Agent
After=network.target openvpn-server@server.service
Wants=openvpn-server@server.service

[Service]
Type=simple
EnvironmentFile=/etc/irangate-node-agent.env
WorkingDirectory=/opt/irangate-node-agent
ExecStart=/usr/bin/python3 /opt/irangate-node-agent/agent.py
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

write_menu() {
  cat > /usr/local/bin/ovp-node <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="/etc/irangate-node-agent.env"

load_env() {
  if [ -f "$ENV_FILE" ]; then
    set -a
    . "$ENV_FILE"
    set +a
  fi
}

set_var() {
  local key="$1"
  local val="$2"
  if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
    sed -i "s|^${key}=.*|${key}=${val}|" "$ENV_FILE"
  else
    echo "${key}=${val}" >> "$ENV_FILE"
  fi
}

restart_agent() {
  systemctl restart irangate-node-agent.service
}

show_status() {
  load_env
  echo "Node Name: ${NODE_NAME:-}"
  echo "Location: ${NODE_LOCATION:-}"
  echo "Agent URL: http://$(hostname -I | awk '{print $1}'):${NODE_AGENT_PORT:-8091}"
  echo "Token: ${NODE_TOKEN:-}"
  echo "OVPN remote in configs: ${NODE_PUBLIC_HOST:-}:${NODE_OVPN_PORT:-1194}/${NODE_OVPN_PROTO:-udp}"
  echo "OpenVPN listen port: ${NODE_OPENVPN_LISTEN_PORT:-1194}"
  echo
  systemctl --no-pager -l status irangate-node-agent.service | head -12 || true
  systemctl --no-pager -l status openvpn-server@server.service | head -12 || true
  ss -lntup | grep ":${NODE_AGENT_PORT:-8091}" || true
  ss -lunp | grep ":${NODE_OPENVPN_LISTEN_PORT:-1194}" || true
}

change_agent_port() {
  load_env
  read -rp "New Agent port [${NODE_AGENT_PORT:-8091}]: " p
  p="${p:-${NODE_AGENT_PORT:-8091}}"
  set_var NODE_AGENT_PORT "$p"
  restart_agent
  echo "Agent port changed to $p"
}

change_token() {
  load_env
  read -rp "New shared token: " t
  [ -z "$t" ] && echo "Empty token rejected" && return
  set_var NODE_TOKEN "$t"
  restart_agent
  echo "Token changed"
}

change_remote_host() {
  load_env
  read -rp "New IP/Domain inside ovpn configs [${NODE_PUBLIC_HOST:-}]: " h
  h="${h:-${NODE_PUBLIC_HOST:-}}"
  [ -z "$h" ] && echo "Empty host rejected" && return
  set_var NODE_PUBLIC_HOST "$h"
  restart_agent
  echo "Remote host changed to $h"
}

change_remote_port() {
  load_env
  read -rp "New port inside ovpn configs [${NODE_OVPN_PORT:-1194}]: " p
  p="${p:-${NODE_OVPN_PORT:-1194}}"
  set_var NODE_OVPN_PORT "$p"
  restart_agent
  echo "Remote port changed to $p"
}

change_openvpn_listen_port() {
  load_env
  read -rp "New OpenVPN listen port on this node [${NODE_OPENVPN_LISTEN_PORT:-1194}]: " p
  p="${p:-${NODE_OPENVPN_LISTEN_PORT:-1194}}"
  sed -i "s/^port .*/port $p/" /etc/openvpn/server/server.conf
  set_var NODE_OPENVPN_LISTEN_PORT "$p"
  systemctl restart openvpn-server@server.service
  echo "OpenVPN listen port changed to $p"
}

make_test_config() {
  local name="test-$(date +%s)"
  local f
  f="$(irangate-node-client create "$name")"
  echo "$f"
  grep '^remote ' "$f"
}

uninstall_node() {
  echo "This removes IranGate node-agent and OpenVPN node files."
  read -rp "Type REMOVE to continue: " x
  [ "$x" != "REMOVE" ] && echo "Canceled" && return

  systemctl disable --now irangate-node-agent.service 2>/dev/null || true
  systemctl disable --now openvpn-server@server.service 2>/dev/null || true
  systemctl disable --now irangate-node-nat.service 2>/dev/null || true

  rm -f /etc/systemd/system/irangate-node-agent.service
  rm -f /etc/systemd/system/irangate-node-nat.service
  rm -f /usr/local/bin/irangate-node-client
  rm -f /usr/local/bin/ovp-node
  rm -f /etc/irangate-node-agent.env
  rm -rf /opt/irangate-node-agent

  read -rp "Also delete /etc/openvpn/easy-rsa and /etc/openvpn/server? type DELETE-OVPN: " y
  if [ "$y" = "DELETE-OVPN" ]; then
    rm -rf /etc/openvpn/easy-rsa /etc/openvpn/server
  fi

  systemctl daemon-reload
  echo "Node removed"
}

while true; do
  echo
  echo "IranGate OVP Node"
  echo "1) Status"
  echo "2) Change Agent port"
  echo "3) Change shared Token"
  echo "4) Change IP/Domain inside ovpn config"
  echo "5) Change Port inside ovpn config"
  echo "6) Change OpenVPN listen port on node"
  echo "7) Create test ovpn config"
  echo "8) Remove node completely"
  echo "0) Exit"
  read -rp "Choice: " c

  case "$c" in
    1) show_status ;;
    2) change_agent_port ;;
    3) change_token ;;
    4) change_remote_host ;;
    5) change_remote_port ;;
    6) change_openvpn_listen_port ;;
    7) make_test_config ;;
    8) uninstall_node; exit 0 ;;
    0) exit 0 ;;
    *) echo "Invalid choice" ;;
  esac
done
EOF

  chmod +x /usr/local/bin/ovp-node
}

start_services() {
  systemctl daemon-reload
  systemctl enable --now irangate-node-nat.service
  systemctl enable --now openvpn-server@server.service
  systemctl enable --now irangate-node-agent.service
}

main() {
  need_root

  PUBLIC_IP="$(detect_ip)"
  echo "IranGate OpenVPN Node Installer"
  echo

  NODE_NAME="$(ask 'Node display name' 'Turkey')"
  NODE_LOCATION="$(ask 'Location code' 'TR')"
  NODE_AGENT_PORT="$(ask 'Agent API port' '8091')"

  DEFAULT_TOKEN="$(openssl rand -hex 16 2>/dev/null || date +%s%N)"
  NODE_TOKEN="$(ask 'Shared token between Master and Node' "$DEFAULT_TOKEN")"

  NODE_OPENVPN_LISTEN_PORT="$(ask 'OpenVPN listen port on this node' '1194')"
  NODE_PUBLIC_HOST="$(ask 'IP/Domain written inside ovpn config, use Iran tunnel IP if you have tunnel' "$PUBLIC_IP")"
  NODE_OVPN_PORT="$(ask 'Port written inside ovpn config, use Iran tunnel port if you have tunnel' "$NODE_OPENVPN_LISTEN_PORT")"
  NODE_OVPN_PROTO="$(ask 'OpenVPN protocol' 'udp')"

  install_packages
  write_env
  setup_openvpn
  setup_network
  write_client_tool
  write_agent
  write_systemd
  write_menu
  start_services

  echo
  echo "DONE"
  echo "Agent URL for panel: http://$PUBLIC_IP:$NODE_AGENT_PORT"
  echo "Token: $NODE_TOKEN"
  echo "Public IP: $PUBLIC_IP"
  echo "Config remote: $NODE_PUBLIC_HOST:$NODE_OVPN_PORT/$NODE_OVPN_PROTO"
  echo
  echo "Manage node with: ovp-node"
}

main "$@"
