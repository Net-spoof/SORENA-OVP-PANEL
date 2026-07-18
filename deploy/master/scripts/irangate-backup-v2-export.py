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

from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path


BACKUP_ROOT = Path("/opt/irangate/backups/v2")
DB_ENV_FILE = Path("/etc/irangate-db.env")
REPO_ROOT = Path("/root/IranGate-OV-Reza-sub-final")


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


def copy_traffic_state(stage: Path) -> bool:
    src = Path("/opt/irangate/traffic")
    dst = stage / "runtime" / "traffic"
    dst.mkdir(parents=True, exist_ok=True)

    copied = False

    if src.exists():
        shutil.copytree(src, dst, dirs_exist_ok=True)
        copied = True

    required_defaults = {
        "cumulative_totals.json": "{}",
        "current_stats.json": "{}",
        "node_usage_state.json": "{}",
        "node_current_stats.json": "{}",
        "global_usage_totals.json": "{}",
        "usage_state.json": "{}",
        "sessions_state.json": "{}",
    }

    for name, default in required_defaults.items():
        target = dst / name
        if not target.exists():
            target.write_text(default)
            copied = True

    return copied


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
        },
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

            if copy_optional(
                Path("/etc/openvpn"),
                stage / "openvpn",
            ):
                components.append(
                    "openvpn-runtime-pki"
                )

        write_manifest(
            stage,
            components,
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
