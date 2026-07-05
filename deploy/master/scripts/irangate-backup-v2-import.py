#!/usr/bin/env python3

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
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


MAX_MEMBERS = 50000
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

    result = command(
        [
            str(EXPORTER),
        ],
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
        "destination webpanel.env preserved intentionally"
    )


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


def restore_from_archive_root(
    root: Path,
) -> dict:

    restore_postgres(
        root
    )

    restore_filesystem(
        root
    )

    return post_restore_check()


def perform_restore(
    archive_path: Path,
) -> dict:

    safety_archive: Path | None = None

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

            extract_and_validate(
                safety_archive,
                rollback_root,
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
                    import_root
                )

                log(
                    "restore completed successfully"
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

    args = parser.parse_args()

    try:
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
