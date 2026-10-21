#!/usr/bin/env python3
"""Fail-closed IKEv2 credential backup guard (no passwords in output).

The canonical credential store is /opt/irangate/ikev2/users.tsv.
The swanctl eap-... secrets file is generated runtime state, not a second
password database. A backup must never silently replace missing credentials.
"""
from __future__ import annotations

import argparse
import csv
import hmac
import re
import tempfile
from pathlib import Path


TSV = "opt/irangate/ikev2/users.tsv"
CONF = "etc/swanctl/conf.d/irangate-ikev2-users.conf"
CONNECTION = "etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf"


class CredentialError(Exception):
    pass


def read_users(path: Path) -> dict[str, tuple[str, str]]:
    users: dict[str, tuple[str, str]] = {}
    try:
        with path.open(encoding="utf-8", newline="") as stream:
            for row in csv.reader(stream, delimiter="\t"):
                if not row:
                    continue
                if len(row) != 4:
                    raise CredentialError("TSV_MALFORMED")
                name, password, _created, status = row
                if (not re.fullmatch(r"[A-Za-z0-9._-]{3,64}", name)
                        or not password or '"' in password
                        or "\r" in password or "\n" in password
                        or status not in {"active", "blocked"}):
                    raise CredentialError("TSV_INVALID_ACCOUNT")
                if name in users:
                    raise CredentialError("TSV_DUPLICATE_ACCOUNT")
                users[name] = (password, status)
    except (OSError, UnicodeError) as exc:
        raise CredentialError("TSV_UNREADABLE") from exc
    return users


def read_secrets(path: Path) -> dict[str, str]:
    try:
        data = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise CredentialError("EAP_UNREADABLE") from exc
    if not re.search(r"(?m)^\s*secrets\s*\{", data):
        raise CredentialError("EAP_WRAPPER_MISSING")
    blocks = re.findall(r"(?m)^\s*eap-[\w.-]+\s*\{([^{}]*)\}", data)
    # Refuse even partially malformed credential blocks.
    if len(blocks) != len(re.findall(r"(?m)^\s*eap-[\w.-]+\s*\{", data)):
        raise CredentialError("EAP_MALFORMED")
    secrets: dict[str, str] = {}
    for block in blocks:
        name = re.search(r'(?m)^\s*id\s*=\s*"([^"\s]+)"\s*$', block)
        password = re.search(r'(?m)^\s*secret\s*=\s*"([^"]*)"\s*$', block)
        if not name or not password or name.group(1) in secrets:
            raise CredentialError("EAP_INVALID_OR_DUPLICATE")
        secrets[name.group(1)] = password.group(1)
    return secrets


def verify(root: Path, *, live: bool) -> tuple[int, int]:
    conn = root / CONNECTION
    tsv = root / TSV
    conf = root / CONF
    if not conn.is_file() and not tsv.is_file() and not conf.is_file():
        return 0, 0  # Deployment without IKEv2.
    if not conn.is_file():
        raise CredentialError("IKE_CONNECTION_MISSING")
    if not tsv.is_file():
        raise CredentialError("CANONICAL_TSV_MISSING")
    users = read_users(tsv)
    active = {name: password for name, (password, status) in users.items()
              if status == "active"}
    # The derived file is deliberately omitted from *new* archives.
    # Historical archives containing both copies must agree exactly.
    if live and not conf.is_file():
        raise CredentialError("LIVE_EAP_MISSING")
    if conf.is_file():
        eap = read_secrets(conf)
        if set(active) != set(eap):
            raise CredentialError("EAP_MEMBERSHIP_MISMATCH")
        for name, password in active.items():
            if not hmac.compare_digest(password, eap[name]):
                raise CredentialError("EAP_PASSWORD_MISMATCH")
    return len(users), len(active)


def self_test() -> None:
    with tempfile.TemporaryDirectory() as folder:
        root = Path(folder)
        for path in (TSV, CONF, CONNECTION):
            (root / path).parent.mkdir(parents=True, exist_ok=True)
        (root / CONNECTION).write_text("connections {}\n")
        (root / TSV).write_text(
            "alpha\tpassword123\t2026-10-01\tactive\n"
            "beta\tpassword456\t2026-10-01\tblocked\n")
        (root / CONF).write_text(
            'secrets {\n eap-alpha {\n id = "alpha"\n'
            ' secret = "password123"\n }\n}\n')
        assert verify(root, live=True) == (2, 1)
        (root / CONF).unlink()
        assert verify(root, live=False) == (2, 1)
        try:
            verify(root, live=True)
            raise AssertionError("missing live EAP accepted")
        except CredentialError:
            pass
        (root / CONF).write_text(
            'secrets {\n eap-alpha {\n id = "alpha"\n'
            ' secret = "wrongpassword"\n }\n}\n')
        try:
            verify(root, live=False)
            raise AssertionError("split-brain credentials accepted")
        except CredentialError:
            pass
        (root / TSV).unlink()
        try:
            verify(root, live=False)
            raise AssertionError("missing canonical source accepted")
        except CredentialError:
            pass
    print("IKEV2_BACKUP_GUARD_SELF_TEST=PASS")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--live", action="store_true")
    group.add_argument("--snapshot", type=Path)
    group.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    try:
        count, active = verify(
            Path("/") if args.live else args.snapshot,
            live=args.live,
        )
    except CredentialError as exc:
        print(f"IKEV2_BACKUP_GUARD=FAIL reason={exc}")
        return 1
    print(f"IKEV2_BACKUP_GUARD=PASS users={count} active={active} passwords_printed=NO")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
