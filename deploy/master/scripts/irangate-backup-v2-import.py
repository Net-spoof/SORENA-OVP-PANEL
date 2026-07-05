#!/usr/bin/env python3

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import tarfile
import tempfile

from pathlib import Path, PurePosixPath


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
    "runtime/webpanel/nodes.json",
    "runtime/traffic/node_usage_state.json",
    "runtime/traffic/cumulative_totals.json",
    "runtime/traffic/current_stats.json",
    "openvpn/easy-rsa/pki/ca.crt",
    "openvpn/easy-rsa/pki/private/ca.key",
    "openvpn/easy-rsa/pki/index.txt",
    "openvpn/easy-rsa/pki/serial",
    "openvpn/easy-rsa/pki/crl.pem",
)


def fail(message: str) -> None:
    raise RuntimeError(message)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()

    with path.open("rb") as handle:
        for chunk in iter(
            lambda: handle.read(1024 * 1024),
            b"",
        ):
            digest.update(chunk)

    return digest.hexdigest()


def ensure_inside(
    root: Path,
    target: Path,
) -> None:

    root_resolved = root.resolve()
    target_resolved = target.resolve()

    try:
        target_resolved.relative_to(root_resolved)
    except ValueError:
        fail(
            f"unsafe archive target: {target}"
        )


def validate_member(
    root: Path,
    member: tarfile.TarInfo,
) -> None:

    name = PurePosixPath(member.name)

    if name.is_absolute():
        fail(
            f"absolute archive path rejected: {member.name}"
        )

    if ".." in name.parts:
        fail(
            f"path traversal rejected: {member.name}"
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
            f"special archive member rejected: {member.name}"
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

    with tarfile.open(
        archive_path,
        "r:gz",
    ) as archive:

        members = archive.getmembers()

        for member in members:
            validate_member(
                destination,
                member,
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
            "missing backup components: "
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

        if not path.is_file():
            fail(
                f"required file missing: {relative}"
            )

        if path.stat().st_size == 0:
            fail(
                f"required file empty: {relative}"
            )


def verify_checksums(
    root: Path,
) -> int:

    checksum_file = (
        root
        / "SHA256SUMS"
    )

    checked = 0

    for raw_line in checksum_file.read_text().splitlines():
        line = raw_line.strip()

        if not line:
            continue

        try:
            expected, relative = line.split(
                "  ",
                1,
            )
        except ValueError:
            fail(
                f"invalid checksum line: {line}"
            )

        relative_path = PurePosixPath(
            relative
        )

        if (
            relative_path.is_absolute()
            or ".." in relative_path.parts
        ):
            fail(
                f"unsafe checksum path: {relative}"
            )

        path = root / relative

        ensure_inside(
            root,
            path,
        )

        if not path.is_file():
            fail(
                f"checksum target missing: {relative}"
            )

        actual = sha256_file(path)

        if actual != expected:
            fail(
                f"checksum mismatch: {relative}"
            )

        checked += 1

    if checked == 0:
        fail(
            "checksum list is empty"
        )

    return checked


def validate_json_runtime(
    root: Path,
) -> tuple[int, int, int]:

    clients_dir = (
        root
        / "runtime"
        / "database"
        / "clients"
    )

    if not clients_dir.is_dir():
        fail(
            "client JSON directory missing"
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

    nodes_path = (
        root
        / "runtime"
        / "webpanel"
        / "nodes.json"
    )

    nodes_data = json.loads(
        nodes_path.read_text()
    )

    if isinstance(
        nodes_data,
        dict,
    ):
        nodes = nodes_data.get(
            "nodes",
            [],
        )
    elif isinstance(
        nodes_data,
        list,
    ):
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

    result = subprocess.run(
        [
            "pg_restore",
            "-l",
            str(dump_path),
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )

    if result.returncode != 0:
        fail(
            "pg_restore listing failed: "
            + result.stderr.strip()
        )

    required_tables = (
        "clients",
        "traffic_totals",
        "traffic_sources",
        "active_sessions",
        "quota_blocks",
        "enforcement_events",
        "traffic_totals_audit",
    )

    for table in required_tables:
        marker = (
            f"TABLE public {table}"
        )

        if marker not in result.stdout:
            fail(
                f"required PostgreSQL table missing: {table}"
            )

    objects = sum(
        1
        for line in result.stdout.splitlines()
        if line
        and not line.startswith(";")
    )

    return objects


def validate_archive(
    archive_path: Path,
) -> dict:

    if not archive_path.is_file():
        fail(
            f"archive not found: {archive_path}"
        )

    if archive_path.stat().st_size == 0:
        fail(
            "archive is empty"
        )

    with tempfile.TemporaryDirectory(
        prefix="irangate-import-v2-validate-"
    ) as temp_dir:

        root = Path(temp_dir)

        safe_extract(
            archive_path,
            root,
        )

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
        ) = validate_json_runtime(
            root
        )

        pg_objects = validate_pg_dump(
            root
        )

        result = {
            "success": True,
            "format": manifest.get(
                "format"
            ),
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

        return result


def main() -> int:
    parser = argparse.ArgumentParser()

    parser.add_argument(
        "archive",
        type=Path,
    )

    parser.add_argument(
        "--validate-only",
        action="store_true",
        default=False,
    )

    args = parser.parse_args()

    try:
        result = validate_archive(
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

    if not args.validate_only:
        print(
            "restore mode is not enabled yet",
            file=sys.stderr,
        )

        return 2

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
