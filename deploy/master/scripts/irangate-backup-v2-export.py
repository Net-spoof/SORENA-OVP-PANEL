#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import hmac
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
import urllib.error
import urllib.parse
import urllib.request

from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path


BACKUP_ROOT = Path("/opt/irangate/backups/v2")
DB_ENV_FILE = Path("/etc/irangate-db.env")
REPO_ROOT = Path("/root/IranGate-OV-Reza-sub-final")

NODE_EXPORT_PATH = "/backup/export"
NODE_EXPORT_TIMEOUT = 240
NODE_EXPORT_MAX_BYTES = 2 * 1024 * 1024 * 1024
NODE_EXPORT_RESPONSE_SKEW = 600
NODE_INDEX_FORMAT = "irangate-master-node-index-v1"
NODE_METADATA_FORMAT = "irangate-master-node-snapshot-v1"


TIMER_UNITS = [
    "irangate-node-usage-sync.timer",
    "irangate-pg-usage-tick.timer",
    "irangate-pg-enforcer.timer",
    "irangate-pg-sync-clients.timer",
]


SERVICE_UNITS = [
    "irangate-node-usage-sync.service",
    "irangate-pg-usage-tick.service",
    "irangate-pg-enforcer.service",
    "irangate-pg-sync-clients.service",
]


def log(message: str) -> None:
    print(
        f"[backup-v2] {message}",
        file=sys.stderr,
        flush=True,
    )


def read_env_file(path: Path) -> dict[str, str]:
    data: dict[str, str] = {}

    for raw_line in path.read_text().splitlines():
        line = raw_line.strip()

        if not line or line.startswith("#") or "=" not in line:
            continue

        key, value = line.split("=", 1)

        data[key.strip()] = (
            value.strip()
            .strip('"')
            .strip("'")
        )

    return data


def systemctl_is_active(unit: str) -> bool:
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


@contextmanager
def paused_sync_pipeline():
    active_timers = [
        unit
        for unit in TIMER_UNITS
        if systemctl_is_active(unit)
    ]

    log("pausing sync/enforcement timers")

    try:
        for unit in active_timers:
            subprocess.run(
                [
                    "systemctl",
                    "stop",
                    unit,
                ],
                check=True,
            )

        for unit in SERVICE_UNITS:
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

        yield

    finally:
        log("resuming sync/enforcement timers")

        for unit in active_timers:
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


def copy_optional(
    src: Path,
    dst: Path,
) -> bool:

    if not src.exists() and not src.is_symlink():
        return False

    dst.parent.mkdir(
        parents=True,
        exist_ok=True,
    )

    if src.is_dir() and not src.is_symlink():
        shutil.copytree(
            src,
            dst,
            symlinks=True,
            dirs_exist_ok=True,
        )

    else:
        if dst.exists() or dst.is_symlink():
            dst.unlink()

        if src.is_symlink():
            os.symlink(
                os.readlink(src),
                dst,
            )
        else:
            shutil.copy2(
                src,
                dst,
            )

    return True


# IRANGATE_BACKUP_SINGLE_PASS_V1
# IRANGATE_BACKUP_EXCLUDE_OPENVPN_LOGS_V2
def copy_openvpn_state(stage: Path) -> bool:
    src = Path("/etc/openvpn")
    dst = stage / "openvpn"

    if not src.is_dir():
        return False

    def ignore_transient(
        directory,
        names,
    ):
        directory_path = Path(directory)
        ignored = set()

        for name in names:
            candidate = directory_path / name

            try:
                relative = candidate.relative_to(src)
            except ValueError:
                continue

            parts = relative.parts
            lowered = name.lower()

            # Runtime logs/status are not required for restore.
            if parts and parts[0] == "logs":
                ignored.add(name)
                continue

            if (
                lowered == "openvpn-status.log"
                or lowered.endswith(".log")
                or ".log." in lowered
                or lowered.endswith(".pid")
                or lowered.endswith(".tmp")
            ):
                ignored.add(name)

        return ignored

    shutil.copytree(
        src,
        dst,
        symlinks=True,
        dirs_exist_ok=True,
        ignore=ignore_transient,
    )

    log(
        "excluded OpenVPN logs/status files from backup"
    )

    return True


def copy_traffic_state(stage: Path) -> bool:
    src = Path("/opt/irangate/traffic")
    dst = stage / "runtime" / "traffic"
    dst.mkdir(parents=True, exist_ok=True)

    # Sufficient state to resume accounting/enforcement.
    # Daily, minute and monthly history/cache trees are excluded.
    state_defaults = {
        "cumulative_totals.json": "{}",
        "current_stats.json": "[]",
        "node_usage_state.json": "{}",
        "node_current_stats.json": "{}",
        "global_usage_totals.json": "{}",
        "usage_state.json": "{}",
        "sessions_state.json": "{}",
        # IRANGATE_OPENVPN_PORT_LOCATION_BACKUP_STAGE6B
        "openvpn_multiport_state.json": "{}",
        "ikev2_current_stats.json": "[]",
        "ikev2_last_seen.json": "{}",
    }

    copied = False

    for name, default in state_defaults.items():
        source = src / name
        target = dst / name

        if source.is_file() and not source.is_symlink():
            shutil.copy2(source, target)
            copied = True
        else:
            target.write_text(
                default,
                encoding="utf-8",
            )
            copied = True

    if src.is_dir():
        excluded_entries = sum(
            1
            for path in src.iterdir()
            if path.name not in state_defaults
        )

        if excluded_entries:
            log(
                "excluded nonessential traffic history/cache "
                f"entries: {excluded_entries}"
            )

    return copied


def write_extras_checksums(root: Path) -> None:
    checksum_file = root / "SHA256SUMS"

    files = sorted(
        (
            path
            for path in root.rglob("*")
            if (
                path != checksum_file
                and path.is_file()
                and not path.is_symlink()
            )
        ),
        key=lambda path: (
            path.relative_to(root).as_posix()
        ),
    )

    with checksum_file.open(
        "w",
        encoding="utf-8",
    ) as handle:
        for path in files:
            relative = (
                "./"
                + path.relative_to(root).as_posix()
            )

            handle.write(
                f"{sha256_file(path)}  {relative}\n"
            )

    checksum_file.chmod(0o600)


def copy_runtime_extras(stage: Path) -> int:
    root = stage / "extras" / "runtime"
    root.mkdir(
        parents=True,
        exist_ok=True,
        mode=0o700,
    )

    sources = (
        "/etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf",
        "/etc/swanctl/conf.d/irangate-ikev2-users.conf",
        "/etc/swanctl/private/irangate-ca-key.pem",
        "/etc/swanctl/private/irangate-server-key.pem",
        "/etc/swanctl/x509ca/irangate-ca-cert.pem",
        "/etc/swanctl/x509/irangate-server-cert.pem",
        "/etc/sysctl.d/99-irangate-ikev2.conf",
        "/opt/irangate/ikev2",
        "/opt/irangate/traffic/ikev2_current_stats.json",
        "/opt/irangate/traffic/ikev2_last_seen.json",
        "/opt/irangate/traffic/global_usage_totals.json",
        "/opt/irangate/webpanel/server.json",
        "/usr/local/sbin/irangate-ikev2",
        "/usr/local/sbin/irangate-ikev2-stats-snapshot",
        "/usr/local/sbin/irangate-ikev2-firewall.sh",
        "/etc/systemd/system/irangate-ikev2-firewall.service",
        "/etc/systemd/system/irangate-ikev2-stats.service",
        "/etc/systemd/system/irangate-ikev2-stats.timer",
        "/opt/irangate/routing",
        "/etc/sing-box/irangate-routing.json",
        "/usr/local/bin/irangate-routing",
        "/usr/local/sbin/irangate-openvpn-port-locations",
        "/etc/systemd/system/irangate-routing.service",
        "/etc/systemd/system/irangate-routing-rules.service",
        "/etc/systemd/system/irangate-openvpn-port-locations.service",
        "/opt/irangate/panel-proxy.env",
        "/etc/systemd/system/irangate-webpanel.service.d/proxy.conf",
    )

    copied = 0

    for raw_source in sources:
        source = Path(raw_source)
        destination = (
            root
            / raw_source.lstrip("/")
        )

        if copy_optional(source, destination):
            copied += 1

    manifest = root / "MANIFEST.txt"
    manifest.write_text(
        "created_at="
        + datetime.now(timezone.utc).strftime(
            "%Y-%m-%dT%H:%M:%SZ"
        )
        + "\n"
        + "hostname="
        + socket.gethostname()
        + "\n"
        + "contains=ikev2\n"
        + "contains=outbound-routing\n"
        + "contains=openvpn-port-locations\n"
        + "contains=panel-proxy\n"
        + "integrity=sha256\n",
        encoding="utf-8",
    )
    manifest.chmod(0o600)

    sensitive_files = (
        root / "opt/irangate/ikev2/users.tsv",
        root / "etc/swanctl/private/irangate-ca-key.pem",
        root / "etc/swanctl/private/irangate-server-key.pem",
    )

    for path in sensitive_files:
        if path.is_file():
            path.chmod(0o600)

    write_extras_checksums(root)

    log(
        "integrated runtime extras into single-pass archive "
        f"({copied} source items)"
    )

    return copied



def load_node_records(path: Path) -> list[dict]:
    data = json.loads(
        path.read_text(encoding="utf-8")
    )

    if isinstance(data, dict):
        nodes = data.get("nodes", [])
    elif isinstance(data, list):
        nodes = data
    else:
        raise RuntimeError(
            "invalid nodes JSON structure"
        )

    if not isinstance(nodes, list):
        raise RuntimeError(
            "invalid nodes list"
        )

    result: list[dict] = []

    for item in nodes:
        if isinstance(item, dict):
            result.append(item)

    return result


def safe_node_directory(node_id: str) -> str:
    value = re.sub(
        r"[^A-Za-z0-9_.-]+",
        "_",
        str(node_id or "").strip(),
    ).strip("._-")

    if not value:
        raise RuntimeError(
            "node id cannot be converted to a safe directory"
        )

    return value


def node_hmac(
    token: str,
    message: str,
) -> str:
    return hmac.new(
        token.encode("utf-8"),
        message.encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()


def normalized_agent_url(value: str) -> str:
    raw = str(value or "").strip().rstrip("/")
    parsed = urllib.parse.urlsplit(raw)

    if parsed.scheme not in {"http", "https"}:
        raise RuntimeError(
            "node agent URL must use http or https"
        )

    if not parsed.hostname:
        raise RuntimeError(
            "node agent URL is missing a hostname"
        )

    if parsed.query or parsed.fragment:
        raise RuntimeError(
            "node agent URL cannot contain query or fragment"
        )

    base_path = parsed.path.rstrip("/")

    return urllib.parse.urlunsplit(
        (
            parsed.scheme,
            parsed.netloc,
            base_path,
            "",
            "",
        )
    )


def download_node_snapshot(
    node: dict,
    destination: Path,
) -> dict:
    node_id = str(node.get("id") or "").strip()
    token = str(node.get("token") or "").strip()
    agent_url = normalized_agent_url(
        str(node.get("agent_url") or "")
    )

    if not node_id:
        raise RuntimeError("node id is missing")

    if len(token) < 16:
        raise RuntimeError(
            f"node {node_id} token is missing or too short"
        )

    request_timestamp = int(time.time())
    request_signature = node_hmac(
        token,
        f"GET\n{NODE_EXPORT_PATH}\n"
        f"{request_timestamp}\n",
    )

    request = urllib.request.Request(
        agent_url + NODE_EXPORT_PATH,
        method="GET",
        headers={
            "X-IranGate-Timestamp": str(
                request_timestamp
            ),
            "X-IranGate-Signature": (
                request_signature
            ),
            "User-Agent": (
                "IranGate-Master-Backup-v2"
            ),
        },
    )

    temp_path = destination.with_name(
        destination.name + ".part"
    )

    temp_path.parent.mkdir(
        parents=True,
        exist_ok=True,
        mode=0o700,
    )

    digest = hashlib.sha256()
    total = 0

    try:
        with urllib.request.urlopen(
            request,
            timeout=NODE_EXPORT_TIMEOUT,
        ) as response:
            if response.status != 200:
                raise RuntimeError(
                    "node export returned HTTP "
                    + str(response.status)
                )

            expected_sha = str(
                response.headers.get(
                    "X-IranGate-Content-SHA256",
                    "",
                )
            ).strip().lower()

            response_timestamp_raw = str(
                response.headers.get(
                    "X-IranGate-Response-Timestamp",
                    "",
                )
            ).strip()

            response_signature = str(
                response.headers.get(
                    "X-IranGate-Response-Signature",
                    "",
                )
            ).strip().lower()

            try:
                response_timestamp = int(
                    response_timestamp_raw
                )
            except Exception as exc:
                raise RuntimeError(
                    "node export response timestamp is invalid"
                ) from exc

            if (
                abs(int(time.time()) - response_timestamp)
                > NODE_EXPORT_RESPONSE_SKEW
            ):
                raise RuntimeError(
                    "node export response timestamp is stale"
                )

            with temp_path.open("wb") as handle:
                while True:
                    chunk = response.read(
                        1024 * 1024
                    )

                    if not chunk:
                        break

                    total += len(chunk)

                    if total > NODE_EXPORT_MAX_BYTES:
                        raise RuntimeError(
                            "node snapshot exceeds maximum size"
                        )

                    digest.update(chunk)
                    handle.write(chunk)

        actual_sha = digest.hexdigest()

        if not expected_sha:
            raise RuntimeError(
                "node export response checksum is missing"
            )

        if not hmac.compare_digest(
            actual_sha,
            expected_sha,
        ):
            raise RuntimeError(
                "node export response checksum mismatch"
            )

        expected_response_signature = node_hmac(
            token,
            f"RESPONSE\n{NODE_EXPORT_PATH}\n"
            f"{response_timestamp}\n{actual_sha}",
        )

        if not hmac.compare_digest(
            response_signature,
            expected_response_signature,
        ):
            raise RuntimeError(
                "node export response HMAC mismatch"
            )

        if total == 0:
            raise RuntimeError(
                "node export returned an empty snapshot"
            )

        temp_path.chmod(0o600)
        temp_path.replace(destination)
        destination.chmod(0o600)

        return {
            "snapshot_sha256": actual_sha,
            "snapshot_size": total,
            "response_timestamp": (
                response_timestamp
            ),
        }

    except urllib.error.HTTPError as exc:
        body = exc.read(2048).decode(
            "utf-8",
            errors="replace",
        ).strip()

        raise RuntimeError(
            f"node export HTTP {exc.code}: {body}"
        ) from exc

    except urllib.error.URLError as exc:
        raise RuntimeError(
            "node export network failure: "
            + str(exc.reason)
        ) from exc

    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass


def export_remote_node_snapshots(
    stage: Path,
) -> dict:
    nodes_path = Path(
        "/opt/irangate/webpanel/nodes.json"
    )
    nodes = load_node_records(nodes_path)
    nodes_root = stage / "nodes"
    nodes_root.mkdir(
        parents=True,
        exist_ok=True,
        mode=0o700,
    )

    allow_partial = (
        os.environ.get(
            "IRANGATE_NODE_BACKUP_ALLOW_PARTIAL",
            "0",
        ).strip()
        == "1"
    )

    index: dict = {
        "format": NODE_INDEX_FORMAT,
        "index_version": 1,
        "created_at": (
            datetime.now(timezone.utc).isoformat()
        ),
        "strict": not allow_partial,
        "nodes": [],
    }

    failures: list[str] = []
    captured = 0
    remote_enabled = 0
    seen_directories: set[str] = set()

    for node in nodes:
        node_id = str(node.get("id") or "").strip()
        entry = {
            "id": node_id,
            "name": str(node.get("name") or ""),
            "location": str(
                node.get("location") or ""
            ),
            "enabled": bool(
                node.get("enabled", True)
            ),
            "is_local": bool(
                node.get("is_local", False)
            ),
            "public_ip": str(
                node.get("public_ip") or ""
            ),
            "agent_url": str(
                node.get("agent_url") or ""
            ),
        }

        if not node_id:
            entry["status"] = "failed"
            entry["error"] = "node id is missing"
            failures.append("unnamed node: node id missing")
            index["nodes"].append(entry)
            continue

        if not entry["enabled"]:
            entry["status"] = "skipped-disabled"
            index["nodes"].append(entry)
            continue

        if (
            entry["is_local"]
            or entry["agent_url"] == "local"
        ):
            entry["status"] = (
                "local-covered-by-master"
            )
            index["nodes"].append(entry)
            continue

        remote_enabled += 1

        try:
            directory = safe_node_directory(node_id)

            if directory in seen_directories:
                raise RuntimeError(
                    "duplicate safe node directory: "
                    + directory
                )

            seen_directories.add(directory)
            node_root = nodes_root / directory
            node_root.mkdir(
                parents=True,
                exist_ok=False,
                mode=0o700,
            )

            snapshot_path = (
                node_root
                / "snapshot.tar.gz.enc"
            )

            transfer = download_node_snapshot(
                node,
                snapshot_path,
            )

            metadata = {
                "format": NODE_METADATA_FORMAT,
                "metadata_version": 1,
                "node_id": node_id,
                "node_directory": directory,
                "node_name": entry["name"],
                "node_location": entry["location"],
                "source_agent_url": (
                    entry["agent_url"]
                ),
                "source_public_ip": (
                    entry["public_ip"]
                ),
                "captured_at": (
                    datetime.now(timezone.utc)
                    .isoformat()
                ),
                **transfer,
                "token_included": False,
                "snapshot_encrypted": True,
            }

            metadata_path = (
                node_root / "metadata.json"
            )
            metadata_path.write_text(
                json.dumps(
                    metadata,
                    indent=2,
                    ensure_ascii=False,
                )
                + "\n",
                encoding="utf-8",
            )
            metadata_path.chmod(0o600)

            entry.update(
                {
                    "status": "captured",
                    "directory": directory,
                    **transfer,
                }
            )
            captured += 1

            log(
                "captured node snapshot: "
                + node_id
                + " ("
                + str(transfer["snapshot_size"])
                + " bytes)"
            )

        except Exception as exc:
            entry["status"] = "failed"
            entry["error"] = str(exc)
            failures.append(
                node_id + ": " + str(exc)
            )

        index["nodes"].append(entry)

    index["summary"] = {
        "configured": len(nodes),
        "remote_enabled": remote_enabled,
        "captured": captured,
        "failed": len(failures),
    }

    index_path = nodes_root / "index.json"
    index_path.write_text(
        json.dumps(
            index,
            indent=2,
            ensure_ascii=False,
        )
        + "\n",
        encoding="utf-8",
    )
    index_path.chmod(0o600)

    if failures and not allow_partial:
        raise RuntimeError(
            "remote node snapshot backup failed: "
            + " | ".join(failures)
        )

    if failures:
        log(
            "warning: partial node snapshot backup: "
            + " | ".join(failures)
        )

    return index["summary"]

def run_pg_dump(stage: Path) -> None:
    db = read_env_file(DB_ENV_FILE)

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
        if not db.get(key)
    ]

    if missing:
        raise RuntimeError(
            "missing database settings: "
            + ", ".join(missing)
        )

    dump_path = (
        stage
        / "postgres"
        / "irangate.dump"
    )

    dump_path.parent.mkdir(
        parents=True,
        exist_ok=True,
    )

    env = os.environ.copy()

    env["PGPASSWORD"] = (
        db["IRANGATE_DB_PASSWORD"]
    )

    command = [
        "pg_dump",
        "-Fc",
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
        "-f",
        str(dump_path),
    ]

    log(
        "creating consistent PostgreSQL "
        "custom-format dump"
    )

    result = subprocess.run(
        command,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )

    if result.returncode != 0:
        raise RuntimeError(
            "pg_dump failed: "
            + result.stderr.strip()
        )

    if (
        not dump_path.is_file()
        or dump_path.stat().st_size == 0
    ):
        raise RuntimeError(
            "pg_dump produced an empty dump"
        )


def current_git_commit() -> str:
    result = subprocess.run(
        [
            "git",
            "-C",
            str(REPO_ROOT),
            "rev-parse",
            "HEAD",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )

    value = result.stdout.strip()

    if result.returncode == 0 and value:
        return value

    return "unknown"


def write_manifest(
    stage: Path,
    components: list[str],
    node_snapshot_summary: dict | None = None,
) -> None:

    manifest = {
        "format": "irangate-backup-v2",
        "backup_version": 2,
        "schema_version": "postgres-v1",
        "created_at": (
            datetime.now(timezone.utc)
            .isoformat()
        ),
        "source_hostname": socket.gethostname(),
        "source_commit": current_git_commit(),
        "components": sorted(components),
        "migration_notes": {
            "postgres_dump_format": "custom",
            "database_credentials_included": False,
            "traffic_daily_history_included": False,
            "subscription_tokens_preserved": True,
            "openvpn_private_key_material_included": True,
            "remote_node_snapshots_included": (
                "node-snapshots" in components
            ),
        },
        "node_snapshot_summary": (
            node_snapshot_summary or {}
        ),
    }

    path = stage / "manifest.json"

    path.write_text(
        json.dumps(
            manifest,
            indent=2,
            ensure_ascii=False,
        )
        + "\n"
    )

    path.chmod(0o600)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()

    with path.open("rb") as handle:
        for chunk in iter(
            lambda: handle.read(1024 * 1024),
            b"",
        ):
            digest.update(chunk)

    return digest.hexdigest()


def write_checksums(stage: Path) -> None:
    checksum_file = stage / "SHA256SUMS"

    files: list[Path] = []

    for path in stage.rglob("*"):
        if path == checksum_file:
            continue

        if (
            path.is_file()
            and not path.is_symlink()
        ):
            files.append(path)

    files.sort(
        key=lambda path: (
            path.relative_to(stage).as_posix()
        )
    )

    with checksum_file.open("w") as handle:
        for path in files:
            rel = (
                path.relative_to(stage)
                .as_posix()
            )

            handle.write(
                f"{sha256_file(path)}  {rel}\n"
            )

    checksum_file.chmod(0o600)


def create_archive(
    stage: Path,
    archive_path: Path,
) -> None:

    archive_path.parent.mkdir(
        parents=True,
        exist_ok=True,
        mode=0o700,
    )

    with tarfile.open(
        archive_path,
        "w:gz",
        format=tarfile.PAX_FORMAT,
        compresslevel=3,
    ) as archive:

        for child in sorted(
            stage.iterdir(),
            key=lambda path: path.name,
        ):
            archive.add(
                child,
                arcname=child.name,
                recursive=True,
            )

    archive_path.chmod(0o600)



def ensure_runtime_state_files() -> None:
    defaults = [
        (
            Path(
                "/opt/irangate/webpanel/nodes.json"
            ),
            {
                "nodes": [],
            },
            0o600,
        ),
        (
            Path(
                "/opt/irangate/traffic/"
                "cumulative_totals.json"
            ),
            [],
            0o644,
        ),
    ]

    for path, default_value, mode in defaults:
        path.parent.mkdir(
            parents=True,
            exist_ok=True,
        )

        if (
            not path.is_file()
            or path.stat().st_size == 0
        ):
            path.write_text(
                json.dumps(
                    default_value,
                    indent=2,
                    ensure_ascii=False,
                )
                + "\n",
                encoding="utf-8",
            )

            path.chmod(mode)

        try:
            with path.open(
                "r",
                encoding="utf-8",
            ) as handle:
                json.load(handle)

        except Exception as exc:
            raise RuntimeError(
                "invalid runtime JSON state: "
                + str(path)
                + ": "
                + str(exc)
            ) from exc



def main() -> int:
    ensure_runtime_state_files()
    BACKUP_ROOT.mkdir(
        parents=True,
        exist_ok=True,
        mode=0o700,
    )

    os.chmod(
        BACKUP_ROOT,
        0o700,
    )

    timestamp = (
        datetime.now(timezone.utc)
        .strftime("%Y%m%dT%H%M%SZ")
    )

    archive_path = (
        BACKUP_ROOT
        / f"irangate-backup-v2-{timestamp}.tar.gz"
    )

    components: list[str] = []

    with tempfile.TemporaryDirectory(
        prefix="irangate-backup-v2-"
    ) as temp_dir:

        stage = (
            Path(temp_dir)
            / "payload"
        )

        stage.mkdir(
            parents=True,
            exist_ok=True,
        )

        with paused_sync_pipeline():

            run_pg_dump(stage)

            components.append(
                "postgres"
            )

            if copy_optional(
                Path(
                    "/opt/irangate/database/clients"
                ),
                (
                    stage
                    / "runtime"
                    / "database"
                    / "clients"
                ),
            ):
                components.append(
                    "client-json"
                )

            for name in (
                "settings.json",
                "cron.json",
            ):
                copy_optional(
                    (
                        Path(
                            "/opt/irangate/database"
                        )
                        / name
                    ),
                    (
                        stage
                        / "runtime"
                        / "database"
                        / name
                    ),
                )

            webpanel_dst = (
                stage
                / "runtime"
                / "webpanel"
            )

            for name in (
                "nodes.json",
                "admin.json",
                "delegated_admins.json",
                "telegram.json",
                "server.json",
                "subscriptions.json",
                "users.json",
                "roles.json",
            ):
                copy_optional(
                    (
                        Path(
                            "/opt/irangate/webpanel"
                        )
                        / name
                    ),
                    webpanel_dst / name,
                )

            copy_optional(
                Path(
                    "/opt/irangate/webpanel/templates"
                ),
                webpanel_dst / "templates",
            )

            components.append(
                "webpanel-runtime"
            )

            copy_optional(
                Path(
                    "/opt/irangate/server_config.json"
                ),
                (
                    stage
                    / "runtime"
                    / "server_config.json"
                ),
            )

            copy_optional(
                Path(
                    "/opt/irangate/logs/revocations.json"
                ),
                (
                    stage
                    / "runtime"
                    / "logs"
                    / "revocations.json"
                ),
            )

            copy_optional(
                Path(
                    "/opt/irangate/clients"
                ),
                (
                    stage
                    / "runtime"
                    / "generated-clients"
                ),
            )

            if copy_traffic_state(stage):
                components.append(
                    "traffic-state"
                )

            copy_optional(
                Path(
                    "/etc/irangate/traffic_config.json"
                ),
                (
                    stage
                    / "config"
                    / "traffic_config.json"
                ),
            )

            copy_optional(
                Path(
                    "/etc/irangate/webpanel.env"
                ),
                (
                    stage
                    / "config"
                    / "webpanel.env"
                ),
            )

            components.append(
                "panel-config"
            )

            if copy_openvpn_state(stage):
                components.append(
                    "openvpn-runtime-pki"
                )

            node_snapshot_summary = (
                export_remote_node_snapshots(stage)
            )
            components.append(
                "node-snapshots"
            )

            copy_runtime_extras(stage)
            components.append(
                "runtime-extras"
            )

        write_manifest(
            stage,
            components,
            node_snapshot_summary,
        )

        write_checksums(stage)

        create_archive(
            stage,
            archive_path,
        )

    print(str(archive_path))

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
