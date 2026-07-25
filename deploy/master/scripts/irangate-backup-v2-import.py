#!/usr/bin/env python3

from __future__ import annotations

import argparse
import hashlib
import hmac
import http.client
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.parse
import uuid

from datetime import datetime, timezone
from pathlib import Path, PurePosixPath


DB_ENV_FILE = Path("/etc/irangate-db.env")

EXPORTER = Path(
    "/usr/local/bin/irangate-backup-v2-export"
)

BACKUP_ROOT = Path(
    "/opt/irangate/backups/v2"
)

PENDING_NODE_ROOT = (
    BACKUP_ROOT / "pending-node-restores"
)
NODE_INDEX_FORMAT = "irangate-master-node-index-v1"
NODE_METADATA_FORMAT = "irangate-master-node-snapshot-v1"
NODE_SNAPSHOT_FORMAT = "irangate-node-snapshot-v1"
NODE_RESTORE_PATH = "/backup/restore"
NODE_RESTORE_TIMEOUT = 300
NODE_RESTORE_MAX_BYTES = 2 * 1024 * 1024 * 1024


PIPELINE_TIMERS = [
    "irangate-node-usage-sync.timer",
    "irangate-pg-usage-tick.timer",
    "irangate-pg-enforcer.timer",
    "irangate-pg-sync-clients.timer",
]


PIPELINE_SERVICES = [
    "irangate-node-usage-sync.service",
    "irangate-pg-usage-tick.service",
    "irangate-pg-enforcer.service",
    "irangate-pg-sync-clients.service",
]


REQUIRED_COMPONENTS = {
    "postgres",
    "client-json",
    "webpanel-runtime",
    "traffic-state",
    "openvpn-runtime-pki",
}


REQUIRED_FILES = (
    "manifest.json",
    "SHA256SUMS",
    "postgres/irangate.dump",
    "runtime/database/clients",
    "runtime/webpanel/nodes.json",
    "runtime/webpanel/admin.json",
    "runtime/traffic/node_usage_state.json",
    "runtime/traffic/cumulative_totals.json",
    "runtime/traffic/current_stats.json",
    "openvpn/easy-rsa/pki/ca.crt",
    "openvpn/easy-rsa/pki/private/ca.key",
    "openvpn/easy-rsa/pki/index.txt",
    "openvpn/easy-rsa/pki/serial",
    "openvpn/easy-rsa/pki/crl.pem",
)


MAX_MEMBERS = 260000
MAX_TOTAL_EXTRACTED = 4 * 1024 * 1024 * 1024
MAX_SINGLE_FILE = 2 * 1024 * 1024 * 1024


def log(message: str) -> None:
    print(
        f"[import-v2] {message}",
        file=sys.stderr,
        flush=True,
    )


def fail(message: str) -> None:
    raise RuntimeError(message)


def read_env_file(path: Path) -> dict[str, str]:
    result: dict[str, str] = {}

    for raw in path.read_text().splitlines():
        line = raw.strip()

        if (
            not line
            or line.startswith("#")
            or "=" not in line
        ):
            continue

        key, value = line.split("=", 1)

        result[key.strip()] = (
            value.strip()
            .strip('"')
            .strip("'")
        )

    return result


def db_settings() -> dict[str, str]:
    data = read_env_file(
        DB_ENV_FILE
    )

    required = [
        "IRANGATE_DB_HOST",
        "IRANGATE_DB_PORT",
        "IRANGATE_DB_NAME",
        "IRANGATE_DB_USER",
        "IRANGATE_DB_PASSWORD",
    ]

    missing = [
        key
        for key in required
        if not data.get(key)
    ]

    if missing:
        fail(
            "missing database configuration: "
            + ", ".join(missing)
        )

    return data


def command(
    args: list[str],
    *,
    env: dict[str, str] | None = None,
    capture: bool = False,
) -> subprocess.CompletedProcess:

    log(
        "run: "
        + " ".join(args)
    )

    return subprocess.run(
        args,
        env=env,
        stdout=(
            subprocess.PIPE
            if capture
            else None
        ),
        stderr=(
            subprocess.PIPE
            if capture
            else None
        ),
        text=True,
        check=False,
    )


def is_active(unit: str) -> bool:
    result = subprocess.run(
        [
            "systemctl",
            "is-active",
            "--quiet",
            unit,
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )

    return result.returncode == 0


def active_openvpn_services() -> list[str]:
    result = subprocess.run(
        [
            "systemctl",
            "list-units",
            "--type=service",
            "--state=active",
            "--no-legend",
            "--plain",
            "openvpn*.service",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )

    units = []

    for line in result.stdout.splitlines():
        parts = line.split()

        if parts:
            units.append(parts[0])

    return sorted(set(units))


def stop_units(units: list[str]) -> None:
    for unit in units:
        subprocess.run(
            [
                "systemctl",
                "stop",
                unit,
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )


def start_units(units: list[str]) -> None:
    for unit in units:
        subprocess.run(
            [
                "systemctl",
                "start",
                unit,
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()

    with path.open("rb") as handle:
        for chunk in iter(
            lambda: handle.read(
                1024 * 1024
            ),
            b"",
        ):
            digest.update(chunk)

    return digest.hexdigest()


def ensure_inside(
    root: Path,
    target: Path,
) -> None:

    root_resolved = root.resolve()
    target_resolved = target.resolve(
        strict=False
    )

    try:
        target_resolved.relative_to(
            root_resolved
        )
    except ValueError:
        fail(
            f"unsafe archive target: {target}"
        )


def validate_member(
    root: Path,
    member: tarfile.TarInfo,
) -> None:

    name = PurePosixPath(
        member.name
    )

    if name.is_absolute():
        fail(
            "absolute archive path rejected: "
            + member.name
        )

    if ".." in name.parts:
        fail(
            "path traversal rejected: "
            + member.name
        )

    target = root / member.name

    ensure_inside(
        root,
        target,
    )

    if (
        member.ischr()
        or member.isblk()
        or member.isfifo()
    ):
        fail(
            "special archive member rejected: "
            + member.name
        )

    if member.issym():
        link_target = (
            target.parent
            / member.linkname
        )

        ensure_inside(
            root,
            link_target,
        )

    if member.islnk():
        link_target = (
            root
            / member.linkname
        )

        ensure_inside(
            root,
            link_target,
        )


def safe_extract(
    archive_path: Path,
    destination: Path,
) -> None:

    total_size = 0

    with tarfile.open(
        archive_path,
        "r:gz",
    ) as archive:

        members = archive.getmembers()

        if len(members) > MAX_MEMBERS:
            fail(
                "archive contains too many members"
            )

        for member in members:
            validate_member(
                destination,
                member,
            )

            if member.isfile():
                if member.size > MAX_SINGLE_FILE:
                    fail(
                        "archive member too large: "
                        + member.name
                    )

                total_size += member.size

                if total_size > MAX_TOTAL_EXTRACTED:
                    fail(
                        "archive extracted size limit exceeded"
                    )

        archive.extractall(
            destination,
            members=members,
        )



def load_node_records(
    path: Path,
    *,
    allow_missing: bool = False,
) -> list[dict]:
    if not path.is_file():
        if allow_missing:
            return []

        fail(
            "nodes JSON file missing: "
            + str(path)
        )

    data = json.loads(
        path.read_text(encoding="utf-8")
    )

    if isinstance(data, dict):
        nodes = data.get("nodes", [])
    elif isinstance(data, list):
        nodes = data
    else:
        fail("invalid nodes JSON structure")

    if not isinstance(nodes, list):
        fail("invalid nodes list")

    return [
        item
        for item in nodes
        if isinstance(item, dict)
    ]


def nodes_by_id(nodes: list[dict]) -> dict[str, dict]:
    result: dict[str, dict] = {}

    for node in nodes:
        node_id = str(node.get("id") or "").strip()

        if node_id:
            result[node_id] = node

    return result


def safe_node_directory(node_id: str) -> str:
    value = re.sub(
        r"[^A-Za-z0-9_.-]+",
        "_",
        str(node_id or "").strip(),
    ).strip("._-")

    if not value:
        fail(
            "node id cannot be converted to a safe directory"
        )

    return value


def openssl_decrypt_snapshot(
    encrypted: Path,
    passphrase: str,
    output: Path,
) -> None:
    env = os.environ.copy()
    env["IRANGATE_BACKUP_PASSPHRASE"] = (
        passphrase
    )

    result = command(
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
        capture=True,
    )

    if result.returncode != 0:
        fail(
            "node snapshot decryption failed: "
            + result.stderr.strip()
        )

    output.chmod(0o600)


def openssl_encrypt_snapshot(
    raw_archive: Path,
    passphrase: str,
    output: Path,
) -> None:
    env = os.environ.copy()
    env["IRANGATE_BACKUP_PASSPHRASE"] = (
        passphrase
    )

    result = command(
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
        capture=True,
    )

    if result.returncode != 0:
        fail(
            "node snapshot re-encryption failed: "
            + result.stderr.strip()
        )

    output.chmod(0o600)


def validate_node_snapshot_payload(
    encrypted: Path,
    token: str,
) -> dict:
    if len(token) < 16:
        fail(
            "source node token is missing or too short"
        )

    with tempfile.TemporaryDirectory(
        prefix="irangate-node-snapshot-validate-"
    ) as temp_dir:
        temp_root = Path(temp_dir)
        raw_archive = temp_root / "snapshot.tar.gz"
        extracted = temp_root / "extracted"
        extracted.mkdir()

        openssl_decrypt_snapshot(
            encrypted,
            token,
            raw_archive,
        )

        safe_extract(
            raw_archive,
            extracted,
        )

        manifest_path = (
            extracted / "manifest.json"
        )

        if not manifest_path.is_file():
            fail(
                "node snapshot manifest is missing"
            )

        manifest = json.loads(
            manifest_path.read_text(
                encoding="utf-8"
            )
        )

        if manifest.get("format") != NODE_SNAPSHOT_FORMAT:
            fail(
                "unsupported node snapshot format"
            )

        if manifest.get("snapshot_version") != 1:
            fail(
                "unsupported node snapshot version"
            )

        required = (
            "SHA256SUMS",
            "payload/etc/openvpn/easy-rsa/pki/ca.crt",
            "payload/etc/openvpn/easy-rsa/pki/private/ca.key",
            "payload/etc/openvpn/easy-rsa/pki/index.txt",
            "payload/etc/openvpn/easy-rsa/pki/serial",
            "payload/etc/openvpn/server/server.conf",
        )

        for relative in required:
            target = extracted / relative

            if not target.is_file() or target.stat().st_size == 0:
                fail(
                    "required node snapshot file missing: "
                    + relative
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

            raw_path = (
                parts[1]
                .strip()
                .strip('"')
                .strip("'")
            )

            if (
                not raw_path
                or raw_path.lower() in {"none", "[inline]"}
            ):
                continue

            if raw_path.startswith("/etc/openvpn/"):
                relative = "payload" + raw_path
            elif raw_path.startswith("/"):
                fail(
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
                fail(
                    "OpenVPN referenced file missing: "
                    + relative
                )

        checksum_count = verify_checksums(
            extracted
        )

        return {
            "manifest": manifest,
            "checksum_files": checksum_count,
        }


def validate_node_snapshots(
    root: Path,
    manifest: dict,
) -> dict:
    components = set(
        manifest.get("components", [])
    )
    nodes_root = root / "nodes"

    if "node-snapshots" not in components:
        return {
            "present": False,
            "captured": 0,
            "failed": 0,
            "validated": [],
        }

    index_path = nodes_root / "index.json"

    if not index_path.is_file():
        fail(
            "node snapshot index is missing"
        )

    index = json.loads(
        index_path.read_text(encoding="utf-8")
    )

    if index.get("format") != NODE_INDEX_FORMAT:
        fail(
            "unsupported node snapshot index format"
        )

    source_nodes = nodes_by_id(
        load_node_records(
            root
            / "runtime"
            / "webpanel"
            / "nodes.json"
        )
    )

    validated: list[dict] = []
    captured = 0
    failed_count = 0

    for entry in index.get("nodes", []):
        if not isinstance(entry, dict):
            fail(
                "invalid node snapshot index entry"
            )

        status = str(entry.get("status") or "")

        if status == "failed":
            failed_count += 1
            continue

        if status != "captured":
            continue

        node_id = str(entry.get("id") or "").strip()
        directory = str(
            entry.get("directory")
            or safe_node_directory(node_id)
        )

        if directory != safe_node_directory(node_id):
            fail(
                "node snapshot directory mismatch: "
                + node_id
            )

        node_root = nodes_root / directory
        metadata_path = node_root / "metadata.json"
        snapshot_path = (
            node_root / "snapshot.tar.gz.enc"
        )

        if not metadata_path.is_file():
            fail(
                "node snapshot metadata missing: "
                + node_id
            )

        if not snapshot_path.is_file():
            fail(
                "node snapshot archive missing: "
                + node_id
            )

        metadata = json.loads(
            metadata_path.read_text(
                encoding="utf-8"
            )
        )

        if metadata.get("format") != NODE_METADATA_FORMAT:
            fail(
                "unsupported node snapshot metadata: "
                + node_id
            )

        if metadata.get("node_id") != node_id:
            fail(
                "node snapshot metadata id mismatch: "
                + node_id
            )

        expected_sha = str(
            metadata.get("snapshot_sha256") or ""
        ).strip().lower()
        actual_sha = sha256_file(snapshot_path)

        if not expected_sha or not hmac.compare_digest(
            expected_sha,
            actual_sha,
        ):
            fail(
                "node snapshot checksum mismatch: "
                + node_id
            )

        source_node = source_nodes.get(node_id)

        if not source_node:
            fail(
                "source node settings missing: "
                + node_id
            )

        source_token = str(
            source_node.get("token") or ""
        ).strip()

        payload_validation = (
            validate_node_snapshot_payload(
                snapshot_path,
                source_token,
            )
        )

        validated.append(
            {
                "id": node_id,
                "directory": directory,
                "snapshot_sha256": actual_sha,
                "snapshot_size": snapshot_path.stat().st_size,
                "node_checksum_files": (
                    payload_validation[
                        "checksum_files"
                    ]
                ),
                "node_created_at": (
                    payload_validation[
                        "manifest"
                    ].get("created_at")
                ),
            }
        )
        captured += 1

    if index.get("strict") and failed_count:
        fail(
            "strict backup contains failed node snapshots"
        )

    return {
        "present": True,
        "captured": captured,
        "failed": failed_count,
        "validated": validated,
    }


def write_nodes_file(
    path: Path,
    nodes: list[dict],
) -> None:
    current_data = {}

    if path.is_file():
        try:
            current_data = json.loads(
                path.read_text(encoding="utf-8")
            )
        except Exception:
            current_data = {}

    if isinstance(current_data, list):
        payload = nodes
    else:
        payload = dict(current_data)
        payload["nodes"] = nodes

    temp_path = path.with_name(
        path.name + ".tmp-" + uuid.uuid4().hex[:10]
    )
    temp_path.parent.mkdir(
        parents=True,
        exist_ok=True,
    )
    temp_path.write_text(
        json.dumps(
            payload,
            indent=2,
            ensure_ascii=False,
        )
        + "\n",
        encoding="utf-8",
    )
    temp_path.chmod(0o600)
    os.replace(temp_path, path)


def merge_node_connection_overrides(
    overrides: list[dict],
) -> dict:
    nodes_path = Path(
        "/opt/irangate/webpanel/nodes.json"
    )
    restored_nodes = load_node_records(
        nodes_path
    )
    override_map = nodes_by_id(overrides)
    changed: list[str] = []

    connection_keys = (
        "agent_url",
        "control_url",
        "public_ip",
        "token",
        "enabled",
    )

    for node in restored_nodes:
        node_id = str(node.get("id") or "").strip()
        replacement = override_map.get(node_id)

        if not replacement:
            continue

        if bool(node.get("is_local", False)):
            continue

        if bool(replacement.get("is_local", False)):
            continue

        before = dict(node)

        for key in connection_keys:
            if key in replacement:
                node[key] = replacement[key]

        if node != before:
            changed.append(node_id)

    if changed:
        write_nodes_file(
            nodes_path,
            restored_nodes,
        )

    return {
        "preserved_connection_nodes": changed,
    }


def normalized_agent_parts(
    value: str,
) -> tuple[str, str, int, str]:
    raw = str(value or "").strip().rstrip("/")
    parsed = urllib.parse.urlsplit(raw)

    if parsed.scheme not in {"http", "https"}:
        fail(
            "node agent URL must use http or https"
        )

    if not parsed.hostname:
        fail(
            "node agent URL is missing a hostname"
        )

    port = parsed.port

    if port is None:
        port = 443 if parsed.scheme == "https" else 80

    base_path = parsed.path.rstrip("/")
    request_path = base_path + NODE_RESTORE_PATH

    return (
        parsed.scheme,
        parsed.hostname,
        port,
        request_path,
    )


def restore_request_hmac(
    token: str,
    timestamp: int,
    digest: str,
) -> str:
    message = (
        f"POST\n{NODE_RESTORE_PATH}\n"
        f"{timestamp}\n{digest}"
    )

    return hmac.new(
        token.encode("utf-8"),
        message.encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()


def send_node_restore(
    encrypted: Path,
    source_token: str,
    target_node: dict,
) -> dict:
    target_token = str(
        target_node.get("token") or ""
    ).strip()
    agent_url = str(
        target_node.get("agent_url") or ""
    ).strip()

    if len(source_token) < 16:
        fail(
            "source node token is missing or too short"
        )

    if len(target_token) < 16:
        fail(
            "target node token is missing or too short"
        )

    with tempfile.TemporaryDirectory(
        prefix="irangate-node-restore-send-"
    ) as temp_dir:
        temp_root = Path(temp_dir)
        raw_archive = temp_root / "snapshot.tar.gz"
        target_encrypted = (
            temp_root / "snapshot.tar.gz.enc"
        )

        openssl_decrypt_snapshot(
            encrypted,
            source_token,
            raw_archive,
        )
        openssl_encrypt_snapshot(
            raw_archive,
            target_token,
            target_encrypted,
        )

        size = target_encrypted.stat().st_size

        if size <= 0 or size > NODE_RESTORE_MAX_BYTES:
            fail(
                "node restore payload size is invalid"
            )

        digest = sha256_file(target_encrypted)
        timestamp = int(time.time())
        signature = restore_request_hmac(
            target_token,
            timestamp,
            digest,
        )

        scheme, host, port, request_path = (
            normalized_agent_parts(agent_url)
        )

        connection_class = (
            http.client.HTTPSConnection
            if scheme == "https"
            else http.client.HTTPConnection
        )

        connection = connection_class(
            host,
            port,
            timeout=NODE_RESTORE_TIMEOUT,
        )

        try:
            connection.putrequest(
                "POST",
                request_path,
            )
            connection.putheader(
                "Content-Type",
                "application/octet-stream",
            )
            connection.putheader(
                "Content-Length",
                str(size),
            )
            connection.putheader(
                "X-IranGate-Timestamp",
                str(timestamp),
            )
            connection.putheader(
                "X-IranGate-Signature",
                signature,
            )
            connection.putheader(
                "X-IranGate-Content-SHA256",
                digest,
            )
            connection.putheader(
                "User-Agent",
                "IranGate-Master-Restore-v2",
            )
            connection.endheaders()

            with target_encrypted.open("rb") as handle:
                while True:
                    chunk = handle.read(1024 * 1024)

                    if not chunk:
                        break

                    connection.send(chunk)

            response = connection.getresponse()
            response_body = response.read(
                1024 * 1024
            )

        finally:
            connection.close()

        body_text = response_body.decode(
            "utf-8",
            errors="replace",
        ).strip()

        try:
            payload = json.loads(body_text)
        except Exception:
            payload = {
                "success": False,
                "message": body_text,
            }

        if response.status < 200 or response.status >= 300:
            fail(
                "node restore HTTP "
                + str(response.status)
                + ": "
                + str(payload.get("message") or body_text)
            )

        if not payload.get("success"):
            fail(
                "node restore failed: "
                + str(payload.get("message") or payload)
            )

        return payload


def pending_backup_key(root: Path) -> str:
    manifest = json.loads(
        (root / "manifest.json").read_text(
            encoding="utf-8"
        )
    )
    created_at = str(
        manifest.get("created_at") or "unknown"
    )
    safe_created = re.sub(
        r"[^A-Za-z0-9_.-]+",
        "_",
        created_at,
    ).strip("._-")

    return (
        safe_created
        + "-"
        + uuid.uuid4().hex[:8]
    )


def save_pending_node_restore(
    node_root: Path,
    source_node: dict,
    reason: str,
    backup_key: str,
) -> Path:
    node_id = str(
        source_node.get("id") or ""
    ).strip()
    directory = safe_node_directory(node_id)
    destination = (
        PENDING_NODE_ROOT
        / backup_key
        / directory
    )

    if destination.exists():
        shutil.rmtree(destination)

    destination.parent.mkdir(
        parents=True,
        exist_ok=True,
        mode=0o700,
    )
    shutil.copytree(
        node_root,
        destination,
        symlinks=False,
    )

    context = {
        "format": "irangate-pending-node-restore-v1",
        "saved_at": (
            datetime.now(timezone.utc).isoformat()
        ),
        "reason": reason,
        "source_node": source_node,
        "source_token": str(
            source_node.get("token") or ""
        ),
    }

    context_path = destination / "restore-context.json"
    context_path.write_text(
        json.dumps(
            context,
            indent=2,
            ensure_ascii=False,
        )
        + "\n",
        encoding="utf-8",
    )
    context_path.chmod(0o600)

    for item in destination.rglob("*"):
        if item.is_file():
            item.chmod(0o600)

    return destination


def restore_node_snapshots(
    root: Path,
) -> dict:
    nodes_root = root / "nodes"
    index_path = nodes_root / "index.json"

    if not index_path.is_file():
        return {
            "attempted": 0,
            "restored": [],
            "pending": [],
            "failed": [],
        }

    index = json.loads(
        index_path.read_text(encoding="utf-8")
    )
    source_nodes = nodes_by_id(
        load_node_records(
            root
            / "runtime"
            / "webpanel"
            / "nodes.json"
        )
    )
    target_nodes = nodes_by_id(
        load_node_records(
            Path(
                "/opt/irangate/webpanel/nodes.json"
            )
        )
    )
    backup_key = pending_backup_key(root)

    result = {
        "attempted": 0,
        "restored": [],
        "pending": [],
        "failed": [],
    }

    for entry in index.get("nodes", []):
        if not isinstance(entry, dict):
            continue

        if entry.get("status") != "captured":
            continue

        node_id = str(entry.get("id") or "").strip()
        directory = str(
            entry.get("directory")
            or safe_node_directory(node_id)
        )
        node_root = nodes_root / directory
        snapshot = (
            node_root / "snapshot.tar.gz.enc"
        )
        source_node = source_nodes.get(node_id)
        target_node = target_nodes.get(node_id)
        result["attempted"] += 1

        if not source_node:
            reason = "source node settings missing"
            result["failed"].append(
                {"id": node_id, "error": reason}
            )
            continue

        if (
            not target_node
            or bool(target_node.get("is_local", False))
            or str(target_node.get("agent_url") or "")
            in {"", "local"}
        ):
            reason = (
                "matching target node is not configured"
            )
            pending_path = save_pending_node_restore(
                node_root,
                source_node,
                reason,
                backup_key,
            )
            result["pending"].append(
                {
                    "id": node_id,
                    "reason": reason,
                    "path": str(pending_path),
                }
            )
            continue

        try:
            response = send_node_restore(
                snapshot,
                str(source_node.get("token") or ""),
                target_node,
            )
            result["restored"].append(
                {
                    "id": node_id,
                    "agent_url": str(
                        target_node.get("agent_url") or ""
                    ),
                    "response": response,
                }
            )

        except Exception as exc:
            reason = str(exc)
            pending_path = save_pending_node_restore(
                node_root,
                source_node,
                reason,
                backup_key,
            )
            result["pending"].append(
                {
                    "id": node_id,
                    "reason": reason,
                    "path": str(pending_path),
                }
            )

    return result


def restore_pending_nodes() -> dict:
    target_nodes = nodes_by_id(
        load_node_records(
            Path(
                "/opt/irangate/webpanel/nodes.json"
            )
        )
    )
    result = {
        "attempted": 0,
        "restored": [],
        "pending": [],
    }

    if not PENDING_NODE_ROOT.is_dir():
        return result

    contexts = sorted(
        PENDING_NODE_ROOT.glob(
            "*/*/restore-context.json"
        )
    )

    for context_path in contexts:
        node_root = context_path.parent
        context = json.loads(
            context_path.read_text(
                encoding="utf-8"
            )
        )
        source_node = context.get("source_node") or {}
        node_id = str(
            source_node.get("id") or ""
        ).strip()
        source_token = str(
            context.get("source_token") or ""
        )
        target_node = target_nodes.get(node_id)
        snapshot = (
            node_root / "snapshot.tar.gz.enc"
        )
        result["attempted"] += 1

        if not target_node:
            result["pending"].append(
                {
                    "id": node_id,
                    "reason": "target node not configured",
                    "path": str(node_root),
                }
            )
            continue

        try:
            response = send_node_restore(
                snapshot,
                source_token,
                target_node,
            )
            result["restored"].append(
                {
                    "id": node_id,
                    "response": response,
                }
            )
            shutil.rmtree(node_root)

            parent = node_root.parent
            if parent.is_dir() and not any(parent.iterdir()):
                parent.rmdir()

        except Exception as exc:
            result["pending"].append(
                {
                    "id": node_id,
                    "reason": str(exc),
                    "path": str(node_root),
                }
            )

    return result

def validate_manifest(root: Path) -> dict:
    path = root / "manifest.json"

    data = json.loads(
        path.read_text()
    )

    if data.get("format") != "irangate-backup-v2":
        fail(
            "unsupported backup format"
        )

    if data.get("backup_version") != 2:
        fail(
            "unsupported backup version"
        )

    components = set(
        data.get(
            "components",
            [],
        )
    )

    missing = (
        REQUIRED_COMPONENTS
        - components
    )

    if missing:
        fail(
            "missing components: "
            + ", ".join(
                sorted(missing)
            )
        )

    return data


def validate_required_files(
    root: Path,
) -> None:

    for relative in REQUIRED_FILES:
        path = root / relative

        if not path.exists():
            fail(
                "required backup item missing: "
                + relative
            )

        if (
            path.is_file()
            and path.stat().st_size == 0
        ):
            fail(
                "required backup file empty: "
                + relative
            )


def verify_checksums(
    root: Path,
) -> int:

    checksum_path = (
        root
        / "SHA256SUMS"
    )

    checked = 0

    for raw in checksum_path.read_text().splitlines():
        line = raw.strip()

        if not line:
            continue

        try:
            expected, relative = line.split(
                "  ",
                1,
            )
        except ValueError:
            fail(
                "invalid checksum entry"
            )

        rel_path = PurePosixPath(
            relative
        )

        if (
            rel_path.is_absolute()
            or ".." in rel_path.parts
        ):
            fail(
                "unsafe checksum path: "
                + relative
            )

        path = root / relative

        ensure_inside(
            root,
            path,
        )

        if not path.is_file():
            fail(
                "checksum target missing: "
                + relative
            )

        actual = sha256_file(path)

        if actual != expected:
            fail(
                "checksum mismatch: "
                + relative
            )

        checked += 1

    if checked == 0:
        fail(
            "checksum list is empty"
        )

    return checked


def validate_runtime(
    root: Path,
) -> tuple[int, int, int]:

    clients_dir = (
        root
        / "runtime"
        / "database"
        / "clients"
    )

    client_count = 0
    token_count = 0

    for path in sorted(
        clients_dir.glob("*.json")
    ):
        data = json.loads(
            path.read_text()
        )

        client_count += 1

        token = data.get(
            "subscription_token"
        )

        if (
            isinstance(token, str)
            and token.strip()
        ):
            token_count += 1

    nodes_data = json.loads(
        (
            root
            / "runtime"
            / "webpanel"
            / "nodes.json"
        ).read_text()
    )

    if isinstance(nodes_data, dict):
        nodes = nodes_data.get(
            "nodes",
            [],
        )

    elif isinstance(nodes_data, list):
        nodes = nodes_data

    else:
        fail(
            "invalid nodes JSON structure"
        )

    return (
        client_count,
        token_count,
        len(nodes),
    )


def validate_pg_dump(
    root: Path,
) -> int:

    dump_path = (
        root
        / "postgres"
        / "irangate.dump"
    )

    result = command(
        [
            "pg_restore",
            "-l",
            str(dump_path),
        ],
        capture=True,
    )

    if result.returncode != 0:
        fail(
            "pg_restore listing failed: "
            + result.stderr.strip()
        )

    required_tables = [
        "clients",
        "traffic_totals",
        "traffic_sources",
        "active_sessions",
        "quota_blocks",
        "enforcement_events",
        "traffic_totals_audit",
    ]

    for table in required_tables:
        marker = (
            f"TABLE public {table}"
        )

        if marker not in result.stdout:
            fail(
                "required PostgreSQL table missing: "
                + table
            )

    return sum(
        1
        for line in result.stdout.splitlines()
        if (
            line
            and not line.startswith(";")
        )
    )


def validate_extracted(
    root: Path,
) -> dict:

    manifest = validate_manifest(
        root
    )

    validate_required_files(
        root
    )

    checksum_count = verify_checksums(
        root
    )

    (
        client_count,
        token_count,
        node_count,
    ) = validate_runtime(
        root
    )

    pg_objects = validate_pg_dump(
        root
    )

    node_snapshots = validate_node_snapshots(
        root,
        manifest,
    )

    return {
        "success": True,
        "format": manifest.get("format"),
        "backup_version": manifest.get(
            "backup_version"
        ),
        "created_at": manifest.get(
            "created_at"
        ),
        "source_hostname": manifest.get(
            "source_hostname"
        ),
        "source_commit": manifest.get(
            "source_commit"
        ),
        "checksum_files": checksum_count,
        "postgres_objects": pg_objects,
        "client_json_count": client_count,
        "subscription_token_count": token_count,
        "node_count": node_count,
        "node_snapshots_present": (
            node_snapshots["present"]
        ),
        "node_snapshot_count": (
            node_snapshots["captured"]
        ),
        "node_snapshot_failures": (
            node_snapshots["failed"]
        ),
        "node_snapshot_validation": (
            node_snapshots["validated"]
        ),
    }


def extract_and_validate(
    archive_path: Path,
    destination: Path,
) -> dict:

    if not archive_path.is_file():
        fail(
            "archive not found: "
            + str(archive_path)
        )

    if archive_path.stat().st_size == 0:
        fail(
            "archive is empty"
        )

    safe_extract(
        archive_path,
        destination,
    )

    return validate_extracted(
        destination
    )


def create_safety_backup() -> Path:
    if not EXPORTER.is_file():
        fail(
            "Backup v2 exporter not installed"
        )

    log(
        "creating automatic pre-restore safety backup"
    )

    safety_env = os.environ.copy()
    safety_env[
        "IRANGATE_NODE_BACKUP_ALLOW_PARTIAL"
    ] = "1"

    result = command(
        [
            str(EXPORTER),
        ],
        env=safety_env,
        capture=True,
    )

    if result.returncode != 0:
        fail(
            "safety backup failed: "
            + result.stderr.strip()
        )

    path = Path(
        result.stdout.strip()
    )

    if (
        not path.is_file()
        or path.stat().st_size == 0
    ):
        fail(
            "safety backup archive missing"
        )

    return path


def restore_postgres(
    root: Path,
) -> None:

    db = db_settings()

    env = os.environ.copy()

    env["PGPASSWORD"] = (
        db["IRANGATE_DB_PASSWORD"]
    )

    dump_path = (
        root
        / "postgres"
        / "irangate.dump"
    )

    log(
        "restoring PostgreSQL in a single transaction"
    )

    result = command(
        [
            "pg_restore",
            "--clean",
            "--if-exists",
            "--single-transaction",
            "--no-owner",
            "--no-acl",
            "-h",
            db["IRANGATE_DB_HOST"],
            "-p",
            db["IRANGATE_DB_PORT"],
            "-U",
            db["IRANGATE_DB_USER"],
            "-d",
            db["IRANGATE_DB_NAME"],
            str(dump_path),
        ],
        env=env,
        capture=True,
    )

    if result.returncode != 0:
        fail(
            "PostgreSQL restore failed: "
            + result.stderr.strip()
        )


def remove_path(path: Path) -> None:
    if path.is_symlink() or path.is_file():
        path.unlink(
            missing_ok=True
        )

    elif path.is_dir():
        shutil.rmtree(path)


def atomic_restore_tree(
    source: Path,
    destination: Path,
) -> None:

    if not source.is_dir():
        fail(
            "restore source directory missing: "
            + str(source)
        )

    destination.parent.mkdir(
        parents=True,
        exist_ok=True,
    )

    token = uuid.uuid4().hex[:10]

    staged = (
        destination.parent
        / f".{destination.name}.restore-{token}"
    )

    previous = (
        destination.parent
        / f".{destination.name}.previous-{token}"
    )

    remove_path(staged)
    remove_path(previous)

    shutil.copytree(
        source,
        staged,
        symlinks=True,
    )

    if (
        destination.exists()
        or destination.is_symlink()
    ):
        os.rename(
            destination,
            previous,
        )

    try:
        os.rename(
            staged,
            destination,
        )

    except Exception:
        if previous.exists():
            os.rename(
                previous,
                destination,
            )

        raise

    remove_path(previous)


def atomic_restore_file(
    source: Path,
    destination: Path,
) -> None:

    if not source.is_file():
        fail(
            "restore source file missing: "
            + str(source)
        )

    destination.parent.mkdir(
        parents=True,
        exist_ok=True,
    )

    temp_path = (
        destination.parent
        / (
            "."
            + destination.name
            + ".restore-"
            + uuid.uuid4().hex[:10]
        )
    )

    shutil.copy2(
        source,
        temp_path,
    )

    os.replace(
        temp_path,
        destination,
    )


def restore_optional_file(
    source: Path,
    destination: Path,
    *,
    remove_if_missing: bool = False,
) -> None:

    if source.is_file():
        atomic_restore_file(
            source,
            destination,
        )

    elif remove_if_missing:
        remove_path(
            destination
        )


def restore_filesystem(
    root: Path,
) -> None:

    log(
        "restoring client database directory"
    )

    atomic_restore_tree(
        root
        / "runtime"
        / "database"
        / "clients",
        Path(
            "/opt/irangate/database/clients"
        ),
    )


    for name in (
        "settings.json",
        "cron.json",
    ):
        restore_optional_file(
            root
            / "runtime"
            / "database"
            / name,
            Path(
                "/opt/irangate/database"
            )
            / name,
            remove_if_missing=True,
        )


    webpanel_source = (
        root
        / "runtime"
        / "webpanel"
    )

    webpanel_destination = Path(
        "/opt/irangate/webpanel"
    )


    for name in (
        "nodes.json",
        "admin.json",
        "telegram.json",
        "server.json",
        "subscriptions.json",
        "users.json",
        "roles.json",
    ):
        restore_optional_file(
            webpanel_source / name,
            webpanel_destination / name,
            remove_if_missing=True,
        )


    templates_source = (
        webpanel_source
        / "templates"
    )

    templates_destination = (
        webpanel_destination
        / "templates"
    )

    if templates_source.is_dir():
        atomic_restore_tree(
            templates_source,
            templates_destination,
        )
    else:
        remove_path(
            templates_destination
        )


    restore_optional_file(
        root
        / "runtime"
        / "server_config.json",
        Path(
            "/opt/irangate/server_config.json"
        ),
        remove_if_missing=True,
    )


    restore_optional_file(
        root
        / "runtime"
        / "logs"
        / "revocations.json",
        Path(
            "/opt/irangate/logs/revocations.json"
        ),
        remove_if_missing=True,
    )


    generated_source = (
        root
        / "runtime"
        / "generated-clients"
    )

    generated_destination = Path(
        "/opt/irangate/clients"
    )

    if generated_source.is_dir():
        atomic_restore_tree(
            generated_source,
            generated_destination,
        )
    else:
        remove_path(
            generated_destination
        )


    log(
        "restoring traffic state while preserving daily history"
    )

    traffic_source = (
        root
        / "runtime"
        / "traffic"
    )

    traffic_destination = Path(
        "/opt/irangate/traffic"
    )

    traffic_destination.mkdir(
        parents=True,
        exist_ok=True,
    )

    for path in traffic_destination.glob(
        "*.json"
    ):
        path.unlink()

    for source in sorted(
        traffic_source.iterdir()
    ):
        destination = (
            traffic_destination
            / source.name
        )

        if source.is_dir():
            atomic_restore_tree(
                source,
                destination,
            )
        else:
            atomic_restore_file(
                source,
                destination,
            )


    restore_optional_file(
        root
        / "config"
        / "traffic_config.json",
        Path(
            "/etc/irangate/traffic_config.json"
        ),
        remove_if_missing=False,
    )


    log(
        "restoring webpanel.env"
    )

    restore_optional_file(
        root
        / "config"
        / "webpanel.env",
        Path(
            "/etc/irangate/webpanel.env"
        ),
        remove_if_missing=False,
    )

    webpanel_env = Path(
        "/etc/irangate/webpanel.env"
    )

    if webpanel_env.is_file():
        webpanel_env.chmod(0o600)


    log(
        "restoring complete OpenVPN runtime and PKI"
    )

    atomic_restore_tree(
        root
        / "openvpn",
        Path(
            "/etc/openvpn"
        ),
    )

    normalize_restored_openvpn_runtime()


def normalize_restored_openvpn_runtime() -> None:
    log(
        "normalizing restored OpenVPN runtime"
    )

    config = Path(
        "/etc/openvpn/server.conf"
    )

    hook = Path(
        "/etc/openvpn/"
        "irangate-client-connect.sh"
    )

    blocked_dir = Path(
        "/etc/openvpn/blocked-clients"
    )

    node_denied_dir = Path(
        "/etc/openvpn/node-denied-clients"
    )

    if not config.is_file():
        fail(
            "restored OpenVPN server.conf missing"
        )

    blocked_dir.mkdir(
        parents=True,
        exist_ok=True,
    )

    node_denied_dir.mkdir(
        parents=True,
        exist_ok=True,
    )

    hook.write_text(
        '#!/bin/sh\n'
        'CN="$common_name"\n'
        '\n'
        'if [ -z "$CN" ]; then\n'
        '  exit 0\n'
        'fi\n'
        '\n'
        'if [ -f "/etc/openvpn/node-denied-clients/$CN" ]; then\n'
        '  echo "IranGate node access denied: $CN" >&2\n'
        '  exit 1\n'
        'fi\n'
        '\n'
        'if [ -f "/etc/openvpn/blocked-clients/$CN" ]; then\n'
        '  echo "IranGate quota block: $CN" >&2\n'
        '  exit 1\n'
        'fi\n'
        '\n'
        'exit 0\n',
        encoding="utf-8",
    )

    hook.chmod(0o755)

    legacy_directive = (
        "client-connect "
        "/usr/local/bin/"
        "irangate-client-connect-guard"
    )

    current_directive = (
        "client-connect "
        "/etc/openvpn/"
        "irangate-client-connect.sh"
    )

    normalized: list[str] = []

    for raw_line in config.read_text(
        encoding="utf-8"
    ).splitlines():

        stripped = raw_line.strip()

        if stripped in {
            legacy_directive,
            current_directive,
        }:
            continue

        normalized.append(raw_line)

    has_script_security = any(
        line.strip().startswith(
            "script-security "
        )
        for line in normalized
    )

    if not has_script_security:
        normalized.append(
            "script-security 2"
        )

    normalized.append(
        current_directive
    )

    config.write_text(
        "\n".join(normalized)
        + "\n",
        encoding="utf-8",
    )

    legacy_runtime = Path(
        "/usr/local/bin/"
        "irangate-client-connect-guard"
    )

    legacy_runtime.unlink(
        missing_ok=True
    )


def post_restore_check() -> dict:
    db = db_settings()

    env = os.environ.copy()

    env["PGPASSWORD"] = (
        db["IRANGATE_DB_PASSWORD"]
    )

    sql = """
SELECT
    COUNT(*)::text
    || '|'
    || (
        COUNT(*) FILTER (
            WHERE client_name='UNDEF'
        )
    )::text
FROM clients;
"""

    result = command(
        [
            "psql",
            "-P",
            "pager=off",
            "-h",
            db["IRANGATE_DB_HOST"],
            "-p",
            db["IRANGATE_DB_PORT"],
            "-U",
            db["IRANGATE_DB_USER"],
            "-d",
            db["IRANGATE_DB_NAME"],
            "-Atc",
            sql,
        ],
        env=env,
        capture=True,
    )

    if result.returncode != 0:
        fail(
            "post-restore PostgreSQL check failed: "
            + result.stderr.strip()
        )

    line = result.stdout.strip()

    values = line.split("|")

    if len(values) != 2:
        fail(
            "unexpected PostgreSQL check result: "
            + repr(line)
        )

    client_count = int(values[0])
    undef_count = int(values[1])

    nodes_path = Path(
        "/opt/irangate/webpanel/nodes.json"
    )

    nodes_data = json.loads(
        nodes_path.read_text()
    )

    if isinstance(nodes_data, dict):
        node_count = len(
            nodes_data.get(
                "nodes",
                [],
            )
        )

    elif isinstance(nodes_data, list):
        node_count = len(
            nodes_data
        )

    else:
        fail(
            "restored nodes database invalid"
        )

    if not Path(
        "/etc/openvpn/easy-rsa/pki/private/ca.key"
    ).is_file():
        fail(
            "restored OpenVPN CA private key missing"
        )

    return {
        "client_count": client_count,
        "undef_count": undef_count,
        "node_count": node_count,
    }


def rebuild_restored_web_access() -> None:
    log(
        "rebuilding Nginx from restored webpanel.env"
    )

    manager = Path(
        "/usr/local/bin/ovp-panel"
    )

    if not manager.is_file():
        fail(
            "ovp-panel manager not found; "
            "cannot rebuild restored web access"
        )

    result = subprocess.run(
        [
            str(manager),
            "--rebuild-nginx",
        ],
        text=True,
        capture_output=True,
        check=False,
    )

    if result.stdout.strip():
        log(
            result.stdout.strip()
        )

    if result.returncode != 0:
        if result.stderr.strip():
            log(
                result.stderr.strip()
            )

        fail(
            "failed to rebuild Nginx configuration "
            "from restored webpanel.env"
        )


def restore_from_archive_root(
    root: Path,
    node_connection_overrides: list[dict] | None = None,
) -> dict:

    restore_postgres(
        root
    )

    restore_filesystem(
        root
    )

    connection_result = {
        "preserved_connection_nodes": [],
    }

    if node_connection_overrides:
        connection_result = (
            merge_node_connection_overrides(
                node_connection_overrides
            )
        )

    rebuild_restored_web_access()

    result = post_restore_check()
    result.update(connection_result)

    return result


def perform_restore(
    archive_path: Path,
) -> dict:

    safety_archive: Path | None = None

    current_node_targets = load_node_records(
        Path(
            "/opt/irangate/webpanel/nodes.json"
        ),
        allow_missing=True,
    )

    rollback_success = False
    mutation_started = False

    active_timers = [
        unit
        for unit in PIPELINE_TIMERS
        if is_active(unit)
    ]

    active_openvpn = (
        active_openvpn_services()
    )

    with tempfile.TemporaryDirectory(
        prefix="irangate-import-v2-"
    ) as import_temp:

        import_root = Path(
            import_temp
        )

        validation = extract_and_validate(
            archive_path,
            import_root,
        )

        safety_archive = (
            create_safety_backup()
        )

        with tempfile.TemporaryDirectory(
            prefix="irangate-rollback-v2-"
        ) as rollback_temp:

            rollback_root = Path(
                rollback_temp
            )

            try:
                extract_and_validate(
                    safety_archive,
                    rollback_root,
                )
            except Exception as safety_validation_error:
                log(
                    "warning: pre-restore safety backup validation skipped: "
                    + str(safety_validation_error)
                )

            try:
                log(
                    "stopping synchronization pipeline"
                )

                stop_units(
                    PIPELINE_TIMERS
                )

                stop_units(
                    PIPELINE_SERVICES
                )

                log(
                    "stopping active OpenVPN services"
                )

                stop_units(
                    active_openvpn
                )

                mutation_started = True

                restored = restore_from_archive_root(
                    import_root,
                    current_node_targets,
                )

                log(
                    "master restore completed successfully"
                )

                node_restore = restore_node_snapshots(
                    import_root
                )

                result = dict(validation)

                result.update(
                    {
                        "restore_success": True,
                        "rollback_performed": False,
                        "safety_backup": str(
                            safety_archive
                        ),
                        "restored": restored,
                        "node_restore": node_restore,
                        "destination_hostname": (
                            socket.gethostname()
                        ),
                        "completed_at": (
                            datetime.now(
                                timezone.utc
                            ).isoformat()
                        ),
                    }
                )

                return result

            except Exception as restore_error:
                log(
                    "restore failed: "
                    + str(restore_error)
                )

                if mutation_started:
                    log(
                        "starting automatic rollback"
                    )

                    try:
                        restore_from_archive_root(
                            rollback_root
                        )

                        rollback_success = True

                        log(
                            "automatic rollback succeeded"
                        )

                    except Exception as rollback_error:
                        raise RuntimeError(
                            "restore failed: "
                            + str(restore_error)
                            + " | rollback failed: "
                            + str(rollback_error)
                            + " | safety backup: "
                            + str(safety_archive)
                        ) from rollback_error

                raise RuntimeError(
                    "restore failed: "
                    + str(restore_error)
                    + " | rollback_success="
                    + str(rollback_success)
                    + " | safety backup: "
                    + str(safety_archive)
                ) from restore_error

            finally:
                log(
                    "starting previously-active OpenVPN services"
                )

                start_units(
                    active_openvpn
                )

                log(
                    "starting previously-active pipeline timers"
                )

                start_units(
                    active_timers
                )


def validate_only(
    archive_path: Path,
) -> dict:

    with tempfile.TemporaryDirectory(
        prefix="irangate-import-v2-validate-"
    ) as temp_dir:

        root = Path(
            temp_dir
        )

        return extract_and_validate(
            archive_path,
            root,
        )


def main() -> int:
    parser = argparse.ArgumentParser(
        description=(
            "Validate or restore IranGate Backup v2 archives"
        )
    )

    parser.add_argument(
        "archive",
        type=Path,
        nargs="?",
    )

    mode = parser.add_mutually_exclusive_group(
        required=True
    )

    mode.add_argument(
        "--validate-only",
        action="store_true",
    )

    mode.add_argument(
        "--restore",
        action="store_true",
    )

    mode.add_argument(
        "--restore-pending-nodes",
        action="store_true",
    )

    args = parser.parse_args()

    try:
        if args.restore_pending_nodes:
            if args.archive is not None:
                fail(
                    "archive is not accepted with "
                    "--restore-pending-nodes"
                )

            result = restore_pending_nodes()

        else:
            if args.archive is None:
                fail("archive path is required")

            if args.validate_only:
                result = validate_only(
                    args.archive
                )
            else:
                result = perform_restore(
                    args.archive
                )

    except Exception as exc:
        print(
            json.dumps(
                {
                    "success": False,
                    "error": str(exc),
                },
                indent=2,
            )
        )

        return 1

    print(
        json.dumps(
            result,
            indent=2,
        )
    )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
