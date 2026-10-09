#!/usr/bin/env bash
readonly IRANGATE_NODE_SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
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
  apt install -y openvpn easy-rsa curl jq python3 python3-pip iptables netcat-openbsd openssl util-linux
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

script-security 2
client-connect /etc/openvpn/irangate-client-connect.sh
management 127.0.0.1 7505
EOF
}

setup_network() {
  cat > /etc/sysctl.d/99-irangate-openvpn.conf <<'EOF'
net.ipv4.ip_forward=1
EOF

  sysctl --system >/dev/null

  cat > /usr/local/bin/irangate-node-nat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-start}"

VPN_NET="10.88.0.0/24"

WAN_IF="$(
  ip route get 1.1.1.1 |
  awk '{
    for (i=1; i<=NF; i++) {
      if ($i == "dev") {
        print $(i+1)
        exit
      }
    }
  }'
)"

if [ -z "$WAN_IF" ]; then
  echo "Unable to detect default network interface" >&2
  exit 1
fi

case "$ACTION" in
  start)

    sysctl -w net.ipv4.ip_forward=1 >/dev/null

    # NAT: VPN clients -> Internet
    iptables -t nat       -C POSTROUTING       -s "$VPN_NET"       -o "$WAN_IF"       -j MASQUERADE       2>/dev/null     || iptables -t nat       -A POSTROUTING       -s "$VPN_NET"       -o "$WAN_IF"       -j MASQUERADE

    # Forward VPN traffic toward Internet
    iptables       -C FORWARD       -s "$VPN_NET"       -o "$WAN_IF"       -j ACCEPT       2>/dev/null     || iptables       -I FORWARD 1       -s "$VPN_NET"       -o "$WAN_IF"       -j ACCEPT

    # Allow return traffic to VPN clients
    iptables       -C FORWARD       -i "$WAN_IF"       -d "$VPN_NET"       -m conntrack       --ctstate ESTABLISHED,RELATED       -j ACCEPT       2>/dev/null     || iptables       -I FORWARD 2       -i "$WAN_IF"       -d "$VPN_NET"       -m conntrack       --ctstate ESTABLISHED,RELATED       -j ACCEPT
    ;;

  stop)

    iptables       -D FORWARD       -s "$VPN_NET"       -o "$WAN_IF"       -j ACCEPT       2>/dev/null     || true

    iptables       -D FORWARD       -i "$WAN_IF"       -d "$VPN_NET"       -m conntrack       --ctstate ESTABLISHED,RELATED       -j ACCEPT       2>/dev/null     || true

    iptables -t nat       -D POSTROUTING       -s "$VPN_NET"       -o "$WAN_IF"       -j MASQUERADE       2>/dev/null     || true
    ;;

  *)
    echo "Usage: $0 start|stop" >&2
    exit 1
    ;;
esac
EOF

  chmod +x /usr/local/bin/irangate-node-nat

  cat > /etc/systemd/system/irangate-node-nat.service <<'EOF'
[Unit]
Description=IranGate OpenVPN NAT
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/irangate-node-nat start
ExecStop=/usr/local/bin/irangate-node-nat stop

[Install]
WantedBy=multi-user.target
EOF
}

write_client_tool() {
  cat > /usr/local/bin/irangate-node-client <<'CLIENT_TOOL_EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ACTION="${1:-}"
NAME="${2:-}"

case "$ACTION" in
  create|rotate) ;;
  *) echo "Usage: irangate-node-client create|rotate CLIENT_NAME" >&2; exit 1 ;;
esac

[ -n "$NAME" ] || { echo "Client name is required" >&2; exit 1; }
SAFE_NAME="$(printf '%s' "$NAME" | tr -cd 'A-Za-z0-9_.@+-')"
[ "$SAFE_NAME" = "$NAME" ] || { echo "Invalid client name" >&2; exit 1; }

mkdir -p /run
exec 9>/run/irangate-node-pki.lock
flock -w 150 9 || { echo "Timed out waiting for PKI lock" >&2; exit 1; }

set -a
. /etc/irangate-node-agent.env
set +a

EASYRSA_DIR="/etc/openvpn/easy-rsa"
PKI_DIR="$EASYRSA_DIR/pki"
OUT_DIR="/opt/irangate-node-agent/clients"
OVPN_FILE="$OUT_DIR/$SAFE_NAME.ovpn"
SERVER_CONF="/etc/openvpn/server/server.conf"
CRL_RUNTIME="/etc/openvpn/server/crl.pem"
SERVICE="openvpn-server@server.service"
SAFETY_ROOT="/opt/irangate-node-agent/rework-safety"
SAFETY_DIR=""
ROLLBACK_NEEDED=0
CRL_WAS_PRESENT=0

mkdir -p "$OUT_DIR" "$SAFETY_ROOT"
cd "$EASYRSA_DIR"

reload_openvpn() {
  systemctl is-active --quiet "$SERVICE" || {
    echo "OpenVPN node service is not active" >&2
    return 1
  }
}

restore_rotate() {
  [ "$ROLLBACK_NEEDED" -eq 1 ] || return 0
  [ -n "$SAFETY_DIR" ] || return 0
  rm -rf "$PKI_DIR"
  cp -a "$SAFETY_DIR/pki" "$PKI_DIR"
  cp -a "$SAFETY_DIR/client.ovpn" "$OVPN_FILE"
  cp -a "$SAFETY_DIR/server.conf" "$SERVER_CONF"
  if [ "$CRL_WAS_PRESENT" -eq 1 ] && [ -f "$SAFETY_DIR/runtime-crl.pem" ]; then
    cp -a "$SAFETY_DIR/runtime-crl.pem" "$CRL_RUNTIME"
  else
    rm -f "$CRL_RUNTIME"
  fi
  reload_openvpn >/dev/null 2>&1 || true
}

finish() {
  rc=$?
  trap - EXIT
  if [ "$ROLLBACK_NEEDED" -eq 1 ]; then
    restore_rotate
  fi
  exit "$rc"
}
trap finish EXIT

write_profile() {
  local template="${1:-}"
  if [ -n "$template" ] && [ -f "$template" ]; then
    python3 - "$template" "$OVPN_FILE" \
      "$PKI_DIR/issued/$SAFE_NAME.crt" \
      "$PKI_DIR/private/$SAFE_NAME.key" <<'PY_PROFILE'
import re
import sys
from pathlib import Path

source = Path(sys.argv[1])
destination = Path(sys.argv[2])
cert_path = Path(sys.argv[3])
key_path = Path(sys.argv[4])
profile = source.read_text(encoding="utf-8")
cert_raw = cert_path.read_text(encoding="utf-8")
key = key_path.read_text(encoding="utf-8").strip()
marker = "-----BEGIN CERTIFICATE-----"
index = cert_raw.find(marker)
if index < 0:
    raise SystemExit("certificate PEM marker missing")
cert = cert_raw[index:].strip()
for tag, value in (("cert", cert), ("key", key)):
    pattern = re.compile(rf"<{tag}>.*?</{tag}>", re.DOTALL)
    if not pattern.search(profile):
        raise SystemExit(f"<{tag}> block missing")
    profile = pattern.sub(f"<{tag}>\n{value}\n</{tag}>", profile, count=1)
temporary = destination.with_suffix(destination.suffix + ".tmp")
temporary.write_text(profile, encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(destination)
PY_PROFILE
    return
  fi

  cat >"$OVPN_FILE" <<OVPN
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
$(awk '/BEGIN CERTIFICATE/{flag=1} flag {print}' "$PKI_DIR/issued/$SAFE_NAME.crt")
</cert>
<key>
$(cat "$PKI_DIR/private/$SAFE_NAME.key")
</key>
<tls-crypt>
$(cat /etc/openvpn/server/ta.key)
</tls-crypt>
OVPN
  chmod 0600 "$OVPN_FILE"
}

create_client() {
  if [ ! -f "$PKI_DIR/issued/$SAFE_NAME.crt" ]; then
    EASYRSA_REQ_CN="$SAFE_NAME" ./easyrsa --batch gen-req "$SAFE_NAME" nopass >/dev/null 2>&1
    ./easyrsa --batch sign-req client "$SAFE_NAME" >/dev/null 2>&1
  fi
  write_profile ""
  printf '%s\n' "$OVPN_FILE"
}

rotate_client() {
  [ -f "$PKI_DIR/issued/$SAFE_NAME.crt" ] || { echo "Existing certificate missing" >&2; return 1; }
  [ -f "$OVPN_FILE" ] || { echo "Existing profile missing" >&2; return 1; }

  SAFETY_DIR="$SAFETY_ROOT/$SAFE_NAME-$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$SAFETY_DIR"
  cp -a "$PKI_DIR" "$SAFETY_DIR/pki"
  cp -a "$OVPN_FILE" "$SAFETY_DIR/client.ovpn"
  cp -a "$SERVER_CONF" "$SAFETY_DIR/server.conf"
  if [ -f "$CRL_RUNTIME" ]; then
    CRL_WAS_PRESENT=1
    cp -a "$CRL_RUNTIME" "$SAFETY_DIR/runtime-crl.pem"
  fi
  ROLLBACK_NEEDED=1

  old_serial="$(openssl x509 -in "$PKI_DIR/issued/$SAFE_NAME.crt" -noout -serial | sed 's/^serial=//')"

  revoke_output=""
  if ! revoke_output="$(EASYRSA_BATCH=1 ./easyrsa --batch revoke "$SAFE_NAME" 2>&1)"; then
    case "${revoke_output,,}" in
      *"already revoked"*|*"not found"*) ;;
      *) echo "$revoke_output" >&2; return 1 ;;
    esac
  fi

  EASYRSA_BATCH=1 ./easyrsa --batch gen-crl >/dev/null 2>&1
  cp -a "$PKI_DIR/crl.pem" "$CRL_RUNTIME"
  chmod 0644 "$CRL_RUNTIME"

  if ! grep -qE '^[[:space:]]*crl-verify[[:space:]]+' "$SERVER_CONF"; then
    printf '\ncrl-verify %s\n' "$CRL_RUNTIME" >>"$SERVER_CONF"
  elif ! grep -qE "^[[:space:]]*crl-verify[[:space:]]+$CRL_RUNTIME([[:space:]]|$)" "$SERVER_CONF"; then
    echo "Unexpected crl-verify directive" >&2
    return 1
  fi

  rm -f \
    "$PKI_DIR/issued/$SAFE_NAME.crt" \
    "$PKI_DIR/private/$SAFE_NAME.key" \
    "$PKI_DIR/reqs/$SAFE_NAME.req"

  EASYRSA_REQ_CN="$SAFE_NAME" ./easyrsa --batch gen-req "$SAFE_NAME" nopass >/dev/null 2>&1
  ./easyrsa --batch sign-req client "$SAFE_NAME" >/dev/null 2>&1
  write_profile "$SAFETY_DIR/client.ovpn"

  new_serial="$(openssl x509 -in "$PKI_DIR/issued/$SAFE_NAME.crt" -noout -serial | sed 's/^serial=//')"
  [ -n "$new_serial" ] && [ "$new_serial" != "$old_serial" ] || { echo "Certificate serial did not rotate" >&2; return 1; }

  printf 'kill %s\nquit\n' "$SAFE_NAME" | nc -w 2 127.0.0.1 7505 >/dev/null 2>&1 || true
  reload_openvpn

  ROLLBACK_NEEDED=0
  printf '%s\n' "$OVPN_FILE"
  printf 'OLD_SERIAL=%s\nNEW_SERIAL=%s\nSAFETY_DIR=%s\n' "$old_serial" "$new_serial" "$SAFETY_DIR" >&2
}

case "$ACTION" in
  create) create_client ;;
  rotate) rotate_client ;;
esac
CLIENT_TOOL_EOF

  chmod +x /usr/local/bin/irangate-node-client
}

write_client_connect_guard() {
  mkdir -p \
    /etc/openvpn/blocked-clients \
    /etc/openvpn/node-denied-clients

  cat > /etc/openvpn/irangate-client-connect.sh <<'CONNECT_GUARD_EOF'
#!/bin/sh
CN="$common_name"

if [ -z "$CN" ]; then
  exit 0
fi

if [ -f "/etc/openvpn/node-denied-clients/$CN" ]; then
  echo "IranGate node access denied: $CN" >&2
  exit 1
fi

if [ -f "/etc/openvpn/blocked-clients/$CN" ]; then
  echo "IranGate quota block: $CN" >&2
  exit 1
fi

exit 0
CONNECT_GUARD_EOF

  chmod +x /etc/openvpn/irangate-client-connect.sh
}


write_backup_helper() {
  cat > /usr/local/sbin/irangate-node-backup-v1 <<'NODE_BACKUP_HELPER_EOF'
#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

FORMAT = "irangate-node-snapshot-v1"
LOCK_PATH = Path("/run/irangate-node-pki.lock")
OUTPUT_DIR = Path("/run/irangate-node-backup")

SOURCES = (
    (Path("/etc/openvpn/easy-rsa/pki"), Path("payload/etc/openvpn/easy-rsa/pki")),
    (Path("/etc/openvpn/server"), Path("payload/etc/openvpn/server")),
    (Path("/etc/openvpn/blocked-clients"), Path("payload/etc/openvpn/blocked-clients")),
    (Path("/etc/openvpn/node-denied-clients"), Path("payload/etc/openvpn/node-denied-clients")),
    (Path("/etc/openvpn/ccd"), Path("payload/etc/openvpn/ccd")),
    (Path("/etc/openvpn/irangate-client-connect.sh"), Path("payload/etc/openvpn/irangate-client-connect.sh")),
    (Path("/opt/irangate-node-agent/clients"), Path("payload/opt/irangate-node-agent/clients")),
)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def copy_item(source: Path, destination: Path) -> bool:
    if not source.exists() and not source.is_symlink():
        return False
    destination.parent.mkdir(parents=True, exist_ok=True)
    if source.is_dir() and not source.is_symlink():
        shutil.copytree(source, destination, symlinks=True)
    elif source.is_symlink():
        os.symlink(os.readlink(source), destination)
    else:
        shutil.copy2(source, destination)
    return True


def count_files(path: Path, pattern: str = "*") -> int:
    if not path.is_dir():
        return 0
    return sum(1 for item in path.glob(pattern) if item.is_file())


def write_checksums(stage: Path) -> None:
    checksum_path = stage / "SHA256SUMS"
    files = sorted(
        (
            item
            for item in stage.rglob("*")
            if item.is_file() and not item.is_symlink() and item != checksum_path
        ),
        key=lambda item: item.relative_to(stage).as_posix(),
    )
    with checksum_path.open("w", encoding="utf-8") as handle:
        for item in files:
            relative = item.relative_to(stage).as_posix()
            handle.write(f"{sha256_file(item)}  {relative}\n")
    checksum_path.chmod(0o600)


def create_archive(stage: Path, output: Path) -> None:
    with tarfile.open(output, "w:gz", format=tarfile.PAX_FORMAT) as archive:
        for child in sorted(stage.iterdir(), key=lambda item: item.name):
            archive.add(child, arcname=child.name, recursive=True)
    output.chmod(0o600)


def main() -> int:
    passphrase = os.environ.get("IRANGATE_BACKUP_PASSPHRASE", "")
    if len(passphrase) < 16:
        raise RuntimeError("backup passphrase is missing or too short")

    OUTPUT_DIR.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(OUTPUT_DIR, 0o700)
    LOCK_PATH.parent.mkdir(parents=True, exist_ok=True)

    with LOCK_PATH.open("a+") as lock_handle:
        import fcntl

        fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)

        with tempfile.TemporaryDirectory(prefix="irangate-node-snapshot-") as temp_dir:
            temp_root = Path(temp_dir)
            stage = temp_root / "snapshot"
            stage.mkdir(parents=True)

            included = []
            for source, relative in SOURCES:
                if copy_item(source, stage / relative):
                    included.append(str(source))

            manifest = {
                "format": FORMAT,
                "snapshot_version": 1,
                "created_at": datetime.now(timezone.utc).isoformat(),
                "source_hostname": socket.gethostname(),
                "node_name": os.environ.get("NODE_NAME", ""),
                "node_location": os.environ.get("NODE_LOCATION", ""),
                "node_public_host": os.environ.get("NODE_PUBLIC_HOST", ""),
                "node_ovpn_port": os.environ.get("NODE_OVPN_PORT", ""),
                "node_ovpn_proto": os.environ.get("NODE_OVPN_PROTO", ""),
                "included_paths": included,
                "counts": {
                    "generated_clients": count_files(Path("/opt/irangate-node-agent/clients"), "*.ovpn"),
                    "issued_certificates": count_files(Path("/etc/openvpn/easy-rsa/pki/issued"), "*.crt"),
                    "private_keys": count_files(Path("/etc/openvpn/easy-rsa/pki/private"), "*.key"),
                    "blocked_clients": count_files(Path("/etc/openvpn/blocked-clients")),
                    "node_denied_clients": count_files(Path("/etc/openvpn/node-denied-clients")),
                },
                "secrets": {
                    "node_environment_included": False,
                    "pki_private_material_included": True,
                    "transport_encrypted": True,
                },
            }
            (stage / "manifest.json").write_text(
                json.dumps(manifest, indent=2, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
            (stage / "manifest.json").chmod(0o600)
            write_checksums(stage)

            raw_archive = temp_root / "snapshot.tar.gz"
            create_archive(stage, raw_archive)

            output = OUTPUT_DIR / (
                "node-snapshot-"
                + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
                + "-"
                + os.urandom(4).hex()
                + ".tar.gz.enc"
            )

            env = os.environ.copy()
            env["IRANGATE_BACKUP_PASSPHRASE"] = passphrase
            result = subprocess.run(
                [
                    "openssl",
                    "enc",
                    "-aes-256-cbc",
                    "-salt",
                    "-pbkdf2",
                    "-iter",
                    "200000",
                    "-md",
                    "sha256",
                    "-pass",
                    "env:IRANGATE_BACKUP_PASSPHRASE",
                    "-in",
                    str(raw_archive),
                    "-out",
                    str(output),
                ],
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                raise RuntimeError("snapshot encryption failed: " + result.stderr.strip())
            output.chmod(0o600)
            print(output)

    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"irangate-node-backup-v1: {exc}", file=sys.stderr)
        raise SystemExit(1)
NODE_BACKUP_HELPER_EOF

  chmod 0755 /usr/local/sbin/irangate-node-backup-v1
}

write_restore_helper() {
  cat > /usr/local/sbin/irangate-node-restore-v1 <<'NODE_RESTORE_HELPER_EOF'
#!/usr/bin/env python3
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path, PurePosixPath

LOCK_PATH = Path("/run/irangate-node-pki.lock")
SAFETY_ROOT = Path("/opt/irangate-node-agent/restore-safety")
BACKUP_HELPER = Path("/usr/local/sbin/irangate-node-backup-v1")
SNAPSHOT_FORMAT = "irangate-node-snapshot-v1"
OPENVPN_SERVICE = "openvpn-server@server.service"
MAX_MEMBERS = 50000
MAX_EXTRACTED = 4 * 1024 * 1024 * 1024
MAX_SINGLE_FILE = 2 * 1024 * 1024 * 1024

DIRECTORY_MAPPINGS = (
    (
        Path("payload/etc/openvpn/easy-rsa/pki"),
        Path("/etc/openvpn/easy-rsa/pki"),
    ),
    (
        Path("payload/etc/openvpn/server"),
        Path("/etc/openvpn/server"),
    ),
    (
        Path("payload/etc/openvpn/blocked-clients"),
        Path("/etc/openvpn/blocked-clients"),
    ),
    (
        Path("payload/etc/openvpn/node-denied-clients"),
        Path("/etc/openvpn/node-denied-clients"),
    ),
    (
        Path("payload/etc/openvpn/ccd"),
        Path("/etc/openvpn/ccd"),
    ),
    (
        Path("payload/opt/irangate-node-agent/clients"),
        Path("/opt/irangate-node-agent/clients"),
    ),
)

FILE_MAPPINGS = (
    (
        Path("payload/etc/openvpn/irangate-client-connect.sh"),
        Path("/etc/openvpn/irangate-client-connect.sh"),
    ),
)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def ensure_inside(root: Path, target: Path) -> None:
    try:
        target.resolve(strict=False).relative_to(root.resolve())
    except ValueError as exc:
        raise RuntimeError(f"unsafe archive target: {target}") from exc


def safe_extract(archive_path: Path, destination: Path) -> None:
    total = 0

    with tarfile.open(archive_path, "r:gz") as archive:
        members = archive.getmembers()

        if len(members) > MAX_MEMBERS:
            raise RuntimeError("snapshot contains too many members")

        for member in members:
            name = PurePosixPath(member.name)

            if name.is_absolute() or ".." in name.parts:
                raise RuntimeError(
                    "unsafe snapshot path: " + member.name
                )

            target = destination / member.name
            ensure_inside(destination, target)

            if member.ischr() or member.isblk() or member.isfifo():
                raise RuntimeError(
                    "special snapshot member rejected: " + member.name
                )

            if member.issym():
                ensure_inside(target.parent, target.parent / member.linkname)

            if member.islnk():
                ensure_inside(destination, destination / member.linkname)

            if member.isfile():
                if member.size > MAX_SINGLE_FILE:
                    raise RuntimeError(
                        "snapshot member too large: " + member.name
                    )

                total += member.size

                if total > MAX_EXTRACTED:
                    raise RuntimeError(
                        "snapshot extracted size limit exceeded"
                    )

        archive.extractall(destination, members=members)


def verify_checksums(root: Path) -> int:
    checksum_path = root / "SHA256SUMS"

    if not checksum_path.is_file():
        raise RuntimeError("snapshot checksum list is missing")

    checked = 0

    for raw in checksum_path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()

        if not line:
            continue

        digest, separator, relative = line.partition("  ")

        if not separator or len(digest) != 64:
            raise RuntimeError("invalid snapshot checksum entry")

        relative_path = PurePosixPath(relative)

        if relative_path.is_absolute() or ".." in relative_path.parts:
            raise RuntimeError("unsafe snapshot checksum path")

        target = root / relative
        ensure_inside(root, target)

        if not target.is_file():
            raise RuntimeError(
                "snapshot checksum target missing: " + relative
            )

        if sha256_file(target) != digest.lower():
            raise RuntimeError(
                "snapshot checksum mismatch: " + relative
            )

        checked += 1

    if checked == 0:
        raise RuntimeError("snapshot checksum list is empty")

    return checked


def decrypt_snapshot(encrypted: Path, passphrase: str, output: Path) -> None:
    env = os.environ.copy()
    env["IRANGATE_BACKUP_PASSPHRASE"] = passphrase

    result = subprocess.run(
        [
            "openssl",
            "enc",
            "-d",
            "-aes-256-cbc",
            "-pbkdf2",
            "-iter",
            "200000",
            "-md",
            "sha256",
            "-pass",
            "env:IRANGATE_BACKUP_PASSPHRASE",
            "-in",
            str(encrypted),
            "-out",
            str(output),
        ],
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )

    if result.returncode != 0:
        raise RuntimeError(
            "snapshot decryption failed: " + result.stderr.strip()
        )

    output.chmod(0o600)


def validate_snapshot(encrypted: Path, passphrase: str, work: Path) -> tuple[Path, dict, int]:
    raw = work / "snapshot.tar.gz"
    extracted = work / "extracted"
    extracted.mkdir(parents=True)

    decrypt_snapshot(encrypted, passphrase, raw)
    safe_extract(raw, extracted)

    manifest_path = extracted / "manifest.json"

    if not manifest_path.is_file():
        raise RuntimeError("snapshot manifest is missing")

    manifest = json.loads(
        manifest_path.read_text(encoding="utf-8")
    )

    if manifest.get("format") != SNAPSHOT_FORMAT:
        raise RuntimeError("unsupported snapshot format")

    if manifest.get("snapshot_version") != 1:
        raise RuntimeError("unsupported snapshot version")

    required = (
        "payload/etc/openvpn/easy-rsa/pki/ca.crt",
        "payload/etc/openvpn/easy-rsa/pki/private/ca.key",
        "payload/etc/openvpn/easy-rsa/pki/index.txt",
        "payload/etc/openvpn/easy-rsa/pki/serial",
        "payload/etc/openvpn/server/server.conf",
    )

    for relative in required:
        target = extracted / relative

        if not target.is_file() or target.stat().st_size == 0:
            raise RuntimeError(
                "required snapshot file missing: " + relative
            )

    server_conf = (
        extracted
        / "payload/etc/openvpn/server/server.conf"
    )

    referenced_directives = {
        "ca",
        "cert",
        "key",
        "dh",
        "tls-auth",
        "tls-crypt",
        "tls-crypt-v2",
        "crl-verify",
    }

    for raw_line in server_conf.read_text(
        encoding="utf-8",
        errors="replace",
    ).splitlines():
        line = raw_line.strip()

        if not line or line.startswith(("#", ";")):
            continue

        parts = line.split()

        if len(parts) < 2:
            continue

        directive = parts[0].lower()

        if directive not in referenced_directives:
            continue

        raw_path = parts[1].strip("\"'")

        if not raw_path or raw_path.lower() in {"none", "[inline]"}:
            continue

        if raw_path.startswith("/etc/openvpn/"):
            relative = "payload" + raw_path
        elif raw_path.startswith("/"):
            raise RuntimeError(
                "unsupported external OpenVPN file reference: "
                + raw_path
            )
        else:
            relative = (
                "payload/etc/openvpn/server/"
                + raw_path
            )

        target = extracted / relative

        if not target.is_file() or target.stat().st_size == 0:
            raise RuntimeError(
                "OpenVPN referenced file missing: "
                + relative
            )

    checksum_count = verify_checksums(extracted)

    return extracted, manifest, checksum_count


def copy_current_to_safety(safety: Path) -> None:
    for relative, destination in DIRECTORY_MAPPINGS:
        target = safety / relative

        if destination.is_dir():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copytree(destination, target, symlinks=True)

    for relative, destination in FILE_MAPPINGS:
        target = safety / relative

        if destination.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(destination, target)


def replace_path(source: Path, destination: Path, is_directory: bool) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    staged = destination.parent / (
        "." + destination.name + ".restore-" + os.urandom(5).hex()
    )

    if staged.exists() or staged.is_symlink():
        if staged.is_dir() and not staged.is_symlink():
            shutil.rmtree(staged)
        else:
            staged.unlink()

    if source.exists():
        if is_directory:
            shutil.copytree(source, staged, symlinks=True)
        else:
            shutil.copy2(source, staged)
    elif is_directory:
        staged.mkdir(parents=True)
        staged.chmod(0o755)
    else:
        if destination.exists() or destination.is_symlink():
            if destination.is_dir() and not destination.is_symlink():
                shutil.rmtree(destination)
            else:
                destination.unlink()
        return

    if destination.exists() or destination.is_symlink():
        if destination.is_dir() and not destination.is_symlink():
            shutil.rmtree(destination)
        else:
            destination.unlink()

    os.replace(staged, destination)


def apply_payload(extracted: Path) -> None:
    for relative, destination in DIRECTORY_MAPPINGS:
        replace_path(
            extracted / relative,
            destination,
            True,
        )

    for relative, destination in FILE_MAPPINGS:
        replace_path(
            extracted / relative,
            destination,
            False,
        )

    Path("/etc/openvpn/blocked-clients").mkdir(
        parents=True,
        exist_ok=True,
    )
    Path("/etc/openvpn/node-denied-clients").mkdir(
        parents=True,
        exist_ok=True,
    )
    Path("/etc/openvpn/ccd").mkdir(
        parents=True,
        exist_ok=True,
    )
    Path("/opt/irangate-node-agent/clients").mkdir(
        parents=True,
        exist_ok=True,
    )

    guard = Path("/etc/openvpn/irangate-client-connect.sh")
    if guard.is_file():
        guard.chmod(0o755)


def service_active() -> bool:
    return subprocess.run(
        ["systemctl", "is-active", "--quiet", OPENVPN_SERVICE],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode == 0


def restart_openvpn() -> None:
    result = subprocess.run(
        ["systemctl", "restart", OPENVPN_SERVICE],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )

    if result.returncode != 0 or not service_active():
        raise RuntimeError(
            "OpenVPN restart failed: " + result.stderr.strip()
        )


def create_persistent_safety(passphrase: str) -> Path:
    env = os.environ.copy()
    env["IRANGATE_BACKUP_PASSPHRASE"] = passphrase

    result = subprocess.run(
        [str(BACKUP_HELPER)],
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )

    if result.returncode != 0:
        raise RuntimeError(
            "pre-restore node safety backup failed: "
            + result.stderr.strip()
        )

    source = Path(result.stdout.strip().splitlines()[-1])

    if not source.is_file():
        raise RuntimeError("node safety snapshot is missing")

    SAFETY_ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    destination = SAFETY_ROOT / (
        "pre-restore-"
        + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
        + "-"
        + os.urandom(4).hex()
        + ".tar.gz.enc"
    )
    shutil.copy2(source, destination)
    destination.chmod(0o600)
    source.unlink(missing_ok=True)

    return destination


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("archive", type=Path)
    parser.add_argument("--validate-only", action="store_true")
    args = parser.parse_args()

    passphrase = os.environ.get("IRANGATE_BACKUP_PASSPHRASE", "")

    if len(passphrase) < 16:
        raise RuntimeError("restore passphrase is missing or too short")

    if not args.archive.is_file() or args.archive.stat().st_size == 0:
        raise RuntimeError("restore archive is missing or empty")

    with tempfile.TemporaryDirectory(
        prefix="irangate-node-restore-v1-"
    ) as temp_dir:
        work = Path(temp_dir)
        extracted, manifest, checksum_count = validate_snapshot(
            args.archive,
            passphrase,
            work / "incoming",
        )

        if args.validate_only:
            print(
                json.dumps(
                    {
                        "success": True,
                        "validated_only": True,
                        "format": manifest.get("format"),
                        "created_at": manifest.get("created_at"),
                        "checksum_files": checksum_count,
                        "counts": manifest.get("counts", {}),
                    },
                    ensure_ascii=False,
                )
            )
            return 0

        safety_archive = create_persistent_safety(passphrase)

        LOCK_PATH.parent.mkdir(parents=True, exist_ok=True)

        with LOCK_PATH.open("a+") as lock_handle:
            fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)
            was_active = service_active()

            if was_active:
                subprocess.run(
                    ["systemctl", "stop", OPENVPN_SERVICE],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    check=False,
                )

            try:
                apply_payload(extracted)
                restart_openvpn()

            except Exception as restore_error:
                rollback_root = work / "rollback"
                rollback_extracted, _, _ = validate_snapshot(
                    safety_archive,
                    passphrase,
                    rollback_root,
                )
                apply_payload(rollback_extracted)

                if was_active:
                    restart_openvpn()

                raise RuntimeError(
                    "node restore failed and rollback was applied: "
                    + str(restore_error)
                ) from restore_error

        print(
            json.dumps(
                {
                    "success": True,
                    "restored": True,
                    "created_at": manifest.get("created_at"),
                    "counts": manifest.get("counts", {}),
                    "checksum_files": checksum_count,
                    "safety_backup": str(safety_archive),
                    "openvpn_active": service_active(),
                },
                ensure_ascii=False,
            )
        )

    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(
            json.dumps(
                {
                    "success": False,
                    "error": str(exc),
                },
                ensure_ascii=False,
            ),
            file=sys.stderr,
        )
        raise SystemExit(1)
NODE_RESTORE_HELPER_EOF

  chmod 0755 /usr/local/sbin/irangate-node-restore-v1
}

write_agent() {
  cat > "$AGENT_DIR/agent.py" <<'AGENT_PY_EOF'
#!/usr/bin/env python3
import hashlib, hmac, json, os, re, subprocess, time
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

TOKEN = os.environ.get("NODE_TOKEN", "")
NODE_PUBLIC_HOST = os.environ.get("NODE_PUBLIC_HOST", "")
NODE_OVPN_PORT = os.environ.get("NODE_OVPN_PORT", "1194")
NODE_OVPN_PROTO = os.environ.get("NODE_OVPN_PROTO", "udp")
NODE_LOCATION = os.environ.get("NODE_LOCATION", "NODE")
NODE_NAME = os.environ.get("NODE_NAME", "SURENA Node")
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


# ===== IRANGATE_NODE_BACKUP_EXPORT_V1 =====
BACKUP_EXPORT_PATH = "/backup/export"
BACKUP_HELPER = "/usr/local/sbin/irangate-node-backup-v1"
BACKUP_MAX_CLOCK_SKEW = 300


def irangate_backup_hmac(message):
    return hmac.new(
        TOKEN.encode("utf-8"),
        message.encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()


def irangate_backup_request_ok(handler, method, path):
    if not TOKEN or len(TOKEN) < 16:
        return False

    raw_timestamp = handler.headers.get("X-IranGate-Timestamp", "").strip()
    signature = handler.headers.get("X-IranGate-Signature", "").strip().lower()

    try:
        timestamp = int(raw_timestamp)
    except Exception:
        return False

    if abs(int(time.time()) - timestamp) > BACKUP_MAX_CLOCK_SKEW:
        return False

    expected = irangate_backup_hmac(f"{method}\n{path}\n{timestamp}\n")
    return hmac.compare_digest(signature, expected)


def irangate_file_sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def irangate_send_binary_file(handler, path, filename):
    size = os.path.getsize(path)
    digest = irangate_file_sha256(path)
    response_timestamp = int(time.time())
    response_signature = irangate_backup_hmac(
        f"RESPONSE\n{BACKUP_EXPORT_PATH}\n{response_timestamp}\n{digest}"
    )

    handler.send_response(200)
    handler.send_header("Content-Type", "application/octet-stream")
    handler.send_header("Content-Disposition", f'attachment; filename="{filename}"')
    handler.send_header("Content-Length", str(size))
    handler.send_header("X-IranGate-Content-SHA256", digest)
    handler.send_header("X-IranGate-Response-Timestamp", str(response_timestamp))
    handler.send_header("X-IranGate-Response-Signature", response_signature)
    handler.end_headers()

    with open(path, "rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                break
            handler.wfile.write(chunk)


def irangate_handle_backup_export(handler):
    if not irangate_backup_request_ok(handler, "GET", BACKUP_EXPORT_PATH):
        return send_json(
            handler,
            401,
            {"success": False, "message": "Unauthorized backup request"},
        )

    env = os.environ.copy()
    env["IRANGATE_BACKUP_PASSPHRASE"] = TOKEN

    result = subprocess.run(
        [BACKUP_HELPER],
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=180,
        check=False,
    )

    if result.returncode != 0:
        return send_json(
            handler,
            500,
            {
                "success": False,
                "message": "Node snapshot export failed",
                "error": result.stderr[-2000:],
            },
        )

    output = result.stdout.strip().splitlines()
    if not output:
        return send_json(
            handler,
            500,
            {"success": False, "message": "Node snapshot path missing"},
        )

    snapshot_path = output[-1].strip()
    allowed_root = os.path.realpath("/run/irangate-node-backup")
    resolved = os.path.realpath(snapshot_path)

    if not resolved.startswith(allowed_root + os.sep) or not os.path.isfile(resolved):
        return send_json(
            handler,
            500,
            {"success": False, "message": "Invalid node snapshot path"},
        )

    try:
        return irangate_send_binary_file(
            handler,
            resolved,
            os.path.basename(resolved),
        )
    finally:
        try:
            os.unlink(resolved)
        except FileNotFoundError:
            pass
# ===== IRANGATE_NODE_BACKUP_RESTORE_V1 =====
BACKUP_RESTORE_PATH = "/backup/restore"
BACKUP_RESTORE_HELPER = "/usr/local/sbin/irangate-node-restore-v1"
BACKUP_RESTORE_MAX_BYTES = 2 * 1024 * 1024 * 1024


def irangate_backup_restore_request_ok(handler, digest):
    if not TOKEN or len(TOKEN) < 16:
        return False

    raw_timestamp = handler.headers.get("X-IranGate-Timestamp", "").strip()
    signature = handler.headers.get("X-IranGate-Signature", "").strip().lower()

    try:
        timestamp = int(raw_timestamp)
    except Exception:
        return False

    if abs(int(time.time()) - timestamp) > BACKUP_MAX_CLOCK_SKEW:
        return False

    expected = irangate_backup_hmac(
        f"POST\n{BACKUP_RESTORE_PATH}\n{timestamp}\n{digest}"
    )
    return hmac.compare_digest(signature, expected)


def irangate_handle_backup_restore(handler):
    expected_digest = handler.headers.get(
        "X-IranGate-Content-SHA256",
        "",
    ).strip().lower()

    if not re.fullmatch(r"[0-9a-f]{64}", expected_digest):
        return send_json(
            handler,
            400,
            {"success": False, "message": "Invalid restore checksum"},
        )

    if not irangate_backup_restore_request_ok(handler, expected_digest):
        return send_json(
            handler,
            401,
            {"success": False, "message": "Unauthorized restore request"},
        )

    try:
        content_length = int(
            handler.headers.get("Content-Length", "0") or "0"
        )
    except Exception:
        content_length = 0

    if content_length <= 0 or content_length > BACKUP_RESTORE_MAX_BYTES:
        return send_json(
            handler,
            413,
            {"success": False, "message": "Restore payload size is invalid"},
        )

    restore_root = Path("/run/irangate-node-restore")
    restore_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    upload_path = restore_root / (
        "restore-" + str(int(time.time())) + "-" + os.urandom(5).hex() + ".enc"
    )
    digest = hashlib.sha256()
    remaining = content_length

    try:
        with upload_path.open("wb") as handle:
            while remaining > 0:
                chunk = handler.rfile.read(min(1024 * 1024, remaining))

                if not chunk:
                    raise RuntimeError("Restore upload ended early")

                handle.write(chunk)
                digest.update(chunk)
                remaining -= len(chunk)

        upload_path.chmod(0o600)
        actual_digest = digest.hexdigest()

        if not hmac.compare_digest(actual_digest, expected_digest):
            return send_json(
                handler,
                400,
                {"success": False, "message": "Restore payload checksum mismatch"},
            )

        env = os.environ.copy()
        env["IRANGATE_BACKUP_PASSPHRASE"] = TOKEN
        command = [BACKUP_RESTORE_HELPER, str(upload_path)]

        if handler.headers.get("X-IranGate-Restore-Mode", "").strip() == "validate":
            command.append("--validate-only")

        result = subprocess.run(
            command,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=600,
            check=False,
        )

        output_text = result.stdout.strip()
        error_text = result.stderr.strip()

        try:
            payload = json.loads(output_text or error_text)
        except Exception:
            payload = {
                "success": result.returncode == 0,
                "message": output_text or error_text,
            }

        if result.returncode != 0:
            return send_json(handler, 500, payload)

        return send_json(handler, 200, payload)

    except Exception as exc:
        return send_json(
            handler,
            500,
            {"success": False, "message": str(exc)},
        )

    finally:
        upload_path.unlink(missing_ok=True)
# ===== /IRANGATE_NODE_BACKUP_RESTORE_V1 =====
# ===== /IRANGATE_NODE_BACKUP_EXPORT_V1 =====


# ===== IranGate node usage endpoint patch =====
def irangate_send_json(handler, code, payload):
    import json
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    handler.send_response(code)
    handler.send_header("Content-Type", "application/json; charset=utf-8")
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)

def irangate_auth_ok(handler):
    import os
    token = os.environ.get("NODE_TOKEN", "").strip()
    if not token:
        return True
    auth = handler.headers.get("Authorization", "").strip()
    return auth == ("Bearer " + token)

def irangate_parse_status_file():
    import os, time
    path = os.environ.get("OPENVPN_STATUS_LOG_PATH", "/etc/openvpn/openvpn-status.log")
    clients = []

    if not os.path.exists(path):
        return clients

    with open(path, "r", encoding="utf-8", errors="ignore") as f:
        for line in f:
            line = line.strip()
            if not line.startswith("CLIENT_LIST,"):
                continue

            parts = line.split(",")
            if len(parts) < 8:
                continue

            def to_int(x):
                try:
                    return int(x)
                except Exception:
                    return 0

            common_name = parts[1]
            real_address = parts[2]
            virtual_address = parts[3]
            # OpenVPN status v3 columns:
            # 0 CLIENT_LIST
            # 1 Common Name
            # 2 Real Address
            # 3 Virtual Address
            # 4 Virtual IPv6 Address
            # 5 Bytes Received
            # 6 Bytes Sent
            # 7 Connected Since
            # 8 Connected Since time_t
            # 9 Username
            # 10 Client ID
            # 11 Peer ID
            bytes_received = to_int(parts[5]) if len(parts) > 5 else 0
            bytes_sent = to_int(parts[6]) if len(parts) > 6 else 0

            clients.append({
                "common_name": common_name,
                "client_name": common_name,
                "real_address": real_address,
                "virtual_address": virtual_address,
                "virtual_ip": virtual_address,
                "bytes_received": bytes_received,
                "bytes_sent": bytes_sent,
                "total_bytes": bytes_received + bytes_sent,
                "connected_since": parts[7] if len(parts) > 7 else "",
                "connected_since_time_t": to_int(parts[8]) if len(parts) > 8 else 0,
                "username": parts[9] if len(parts) > 9 else "",
                "last_updated": int(time.time()),
                "status": "connected",
            })

    return clients

def irangate_handle_usage(handler):
    import os, time

    if not irangate_auth_ok(handler):
        return irangate_send_json(handler, 401, {
            "success": False,
            "message": "Unauthorized",
        })

    clients = irangate_parse_status_file()

    return irangate_send_json(handler, 200, {
        "success": True,
        "message": "OpenVPN usage retrieved",
        "node": os.environ.get("NODE_NAME", ""),
        "location": os.environ.get("NODE_LOCATION", ""),
        "timestamp": int(time.time()),
        "clients": clients,
        "data": clients,
    })
# ===== /IranGate node usage endpoint patch =====

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
        request_path = urlparse(self.path).path

        if request_path == BACKUP_EXPORT_PATH:
            return irangate_handle_backup_export(self)

        if self.path.split("?")[0] in ("/usage", "/clients", "/openvpn/status"):
            return irangate_handle_usage(self)

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

        if path == "/capabilities":
            return send_json(self, 200, {
                "success": True,
                "client_rework": True,
                "client_rework_version": 3,
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
        path = urlparse(self.path).path

        if path == BACKUP_RESTORE_PATH:
            return irangate_handle_backup_restore(self)

        if not self.auth_ok():
            return send_json(self, 401, {"success": False, "message": "Unauthorized"})

        if path in ("/clients", "/clients/rework"):
            data = self.read_json()
            name = safe_name(data.get("name"))
            if not name:
                return send_json(self, 400, {"success": False, "message": "Invalid client name"})

            r = subprocess.run(
                [
                    "/usr/local/bin/irangate-node-client",
                    ("rotate" if path == "/clients/rework" else "create"),
                    name,
                ],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE
            )

            if r.returncode != 0:
                return send_json(self, 500, {
                    "success": False,
                    "message": (
                        "Client rework failed"
                        if path == "/clients/rework"
                        else "Client create failed"
                    ),
                    "stderr": r.stderr[-2000:]
                })

            return send_json(self, 200, {
                "success": True,
                "message": (
                    "Client credentials rotated"
                    if path == "/clients/rework"
                    else "Client created"
                ),
                "name": name,
                "location": NODE_LOCATION,
                "config_path": (r.stdout.strip().splitlines()[-1] if r.stdout.strip() else ""),
                "download_path": f"/clients/{name}/download"
            })

        return send_json(self, 404, {"success": False, "message": "Not found"})

if __name__ == "__main__":
    CLIENT_DIR.mkdir(parents=True, exist_ok=True)
    ThreadingHTTPServer(("0.0.0.0", NODE_AGENT_PORT), Handler).serve_forever()
AGENT_PY_EOF

  chmod +x "$AGENT_DIR/agent.py"
}

write_control_server() {
  cat > "$AGENT_DIR/node_control_server.py" <<'CONTROL_PY_EOF'
#!/usr/bin/env python3
import os, json, re, subprocess
from pathlib import Path
from urllib.parse import urlparse, parse_qs
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOKEN = os.environ.get("NODE_TOKEN", "").strip()
PORT = int(os.environ.get("NODE_CONTROL_PORT", "5024"))
BLOCK_DIR = Path("/etc/openvpn/blocked-clients")
BLOCK_DIR.mkdir(parents=True, exist_ok=True)

DENY_DIR = Path("/etc/openvpn/node-denied-clients")
DENY_DIR.mkdir(parents=True, exist_ok=True)

def send_json(h, code, obj):
    b = json.dumps(obj, ensure_ascii=False).encode()
    h.send_response(code)
    h.send_header("Content-Type", "application/json; charset=utf-8")
    h.send_header("Content-Length", str(len(b)))
    h.end_headers()
    h.wfile.write(b)

def auth_ok(h):
    if not TOKEN:
        return True
    return h.headers.get("Authorization", "").strip() == "Bearer " + TOKEN

def clean_name(name):
    name = (name or "").strip()
    if not re.match(r"^[A-Za-z0-9_.@+-]{1,128}$", name):
        return ""
    return name

def kill_client(name):
    cmd = f'printf "kill {name}\\nquit\\n" | nc -w 2 127.0.0.1 7505'
    subprocess.run(cmd, shell=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

class H(BaseHTTPRequestHandler):
    def log_message(self, *args):
        return

    def do_GET(self):
        if not auth_ok(self):
            return send_json(self, 401, {"success": False, "message": "Unauthorized"})

        u = urlparse(self.path)
        q = parse_qs(u.query)
        name = clean_name((q.get("name") or [""])[0])

        if u.path == "/health":
            return send_json(self, 200, {"success": True, "status": "ok"})

        if not name:
            return send_json(self, 400, {"success": False, "message": "Invalid name"})

        if u.path == "/deny-node-client":
            (DENY_DIR / name).write_text("denied\n")
            kill_client(name)

            return send_json(
                self,
                200,
                {
                    "success": True,
                    "message": "node access denied",
                    "name": name,
                    "denied": True,
                },
            )

        if u.path == "/allow-node-client":
            try:
                (DENY_DIR / name).unlink()
            except FileNotFoundError:
                pass

            return send_json(
                self,
                200,
                {
                    "success": True,
                    "message": "node access allowed",
                    "name": name,
                    "denied": False,
                },
            )

        if u.path == "/node-client-access":
            denied = (DENY_DIR / name).exists()

            return send_json(
                self,
                200,
                {
                    "success": True,
                    "name": name,
                    "denied": denied,
                },
            )

        if u.path == "/block-client":
            (BLOCK_DIR / name).write_text("blocked\n")
            kill_client(name)
            return send_json(self, 200, {"success": True, "message": "blocked", "name": name})

        if u.path == "/unblock-client":
            try:
                (BLOCK_DIR / name).unlink()
            except FileNotFoundError:
                pass
            return send_json(self, 200, {"success": True, "message": "unblocked", "name": name})

        return send_json(self, 404, {"success": False, "message": "Not found"})

ThreadingHTTPServer(("0.0.0.0", PORT), H).serve_forever()
CONTROL_PY_EOF

  chmod +x "$AGENT_DIR/node_control_server.py"
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

  cat > /etc/systemd/system/irangate-node-control.service <<'EOF'
[Unit]
Description=IranGate Node Control API
After=network.target openvpn-server@server.service
Wants=openvpn-server@server.service

[Service]
Type=simple
EnvironmentFile=/etc/irangate-node-agent.env
Environment=NODE_CONTROL_PORT=5024
WorkingDirectory=/opt/irangate-node-agent
ExecStart=/usr/bin/python3 /opt/irangate-node-agent/node_control_server.py
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

write_menu() {
  cat > /usr/local/bin/ovp-node <<'OVP_NODE_EOF'
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

restart_node_apis() {
  systemctl restart irangate-node-agent.service
  systemctl restart irangate-node-control.service
}

show_status() {
  load_env
  echo "Node Name: ${NODE_NAME:-}"
  echo "Location: ${NODE_LOCATION:-}"
  echo "Agent URL: http://$(hostname -I | awk '{print $1}'):${NODE_AGENT_PORT:-8091}"
  local token="${NODE_TOKEN:-}"
  local masked_token="<not set>"

  if [ -n "$token" ]; then
    if [ "${#token}" -le 8 ]; then
      masked_token="****"
    else
      masked_token="${token:0:4}****${token: -4}"
    fi
  fi

  echo "Token: $masked_token"
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
  restart_node_apis
  echo "Token changed and both Node APIs restarted"
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
  echo "This removes SURENA node-agent and OpenVPN node files."
  read -rp "Type REMOVE to continue: " x
  [ "$x" != "REMOVE" ] && echo "Canceled" && return

  systemctl disable --now irangate-node-agent.service 2>/dev/null || true
  systemctl disable --now irangate-node-control.service 2>/dev/null || true
  systemctl disable --now openvpn-server@server.service 2>/dev/null || true
  systemctl disable --now irangate-node-nat.service 2>/dev/null || true

  rm -f /etc/systemd/system/irangate-node-agent.service
  rm -f /etc/systemd/system/irangate-node-control.service
  rm -f /etc/systemd/system/irangate-node-nat.service
  rm -f /usr/local/bin/irangate-node-client
  rm -f /usr/local/sbin/irangate-node-backup-v1
  rm -f /usr/local/sbin/irangate-node-restore-v1
  rm -rf /run/irangate-node-restore
  rm -rf /opt/irangate-node-agent/restore-safety
  rm -f /run/irangate-node-pki.lock
  rm -rf /run/irangate-node-backup
  systemctl disable --now irangate-vpn-direct-bypass.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/irangate-vpn-direct-bypass.service
  rm -f /etc/default/irangate-vpn-direct-bypass
  rm -f /usr/local/sbin/irangate-vpn-direct-bypass
  rm -f /etc/systemd/system/irangate-warp-gemini-rules.service.d/90-vpn-direct-bypass.conf
  rm -f /usr/local/bin/irangate-node-nat
  rm -f /usr/local/bin/ovp-node
  rm -f /etc/openvpn/irangate-client-connect.sh
  rm -rf /etc/openvpn/blocked-clients
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
  echo "SURENA OVPN Node"
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
OVP_NODE_EOF

  chmod +x /usr/local/bin/ovp-node
}

start_services() {
  systemctl daemon-reload

  systemctl enable irangate-node-nat.service
  systemctl restart irangate-node-nat.service

  systemctl enable openvpn-server@server.service
  systemctl restart openvpn-server@server.service

  systemctl enable irangate-node-agent.service
  systemctl enable irangate-node-control.service

  systemctl restart irangate-node-agent.service
  systemctl restart irangate-node-control.service
}


install_vpn_direct_bypass() {
  # IRANGATE_VPN_DIRECT_BYPASS_NODE_LOCAL_FIRST_V2
  local script_dir
  local repo_root
  local local_installer
  local raw_base
  local installer_url
  local temp_installer

  script_dir="$IRANGATE_NODE_SCRIPT_DIR"

  repo_root="$(
    cd -- "$script_dir/.." &&
    pwd
  )"

  local_installer="$repo_root/scripts/install-vpn-direct-bypass.sh"

  if [ -f "$local_installer" ]; then
    bash -n "$local_installer"
    bash "$local_installer" node
    return 0
  fi

  echo "Missing scripts/install-vpn-direct-bypass.sh." >&2
  echo "Install SURENA Node from a complete authenticated repository clone, not a standalone script." >&2
  return 1
}

main() {
  need_root

  PUBLIC_IP="$(detect_ip)"
  echo "SURENA OpenVPN Node Installer"
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
  install_vpn_direct_bypass
  write_client_tool
  write_client_connect_guard
  write_backup_helper
  write_restore_helper
  write_agent
  write_control_server
  write_systemd
  write_menu
  start_services

  echo
  echo "DONE"
  echo "Agent URL for panel: http://$PUBLIC_IP:$NODE_AGENT_PORT"
  echo "Control URL for Master: http://$PUBLIC_IP:5024"
  echo "Token: $NODE_TOKEN"
  echo "Public IP: $PUBLIC_IP"
  echo "Config remote: $NODE_PUBLIC_HOST:$NODE_OVPN_PORT/$NODE_OVPN_PROTO"
  echo
  echo "Manage node with: ovp-node"
}

main "$@"
