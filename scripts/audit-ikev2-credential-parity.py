#!/usr/bin/env python3
"""Read-only IKEv2 TSV/StrongSwan credential parity check.

Does NOT recreate absent secrets, reset passwords, or touch PostgreSQL.
A missing client password cannot be reconstructed from the public panel metadata.
"""
from __future__ import annotations

import argparse
import csv
import hmac
import re
import sys
from pathlib import Path


def read_users(path: Path) -> tuple[dict[str, tuple[str, str]], list[str]]:
    users: dict[str, tuple[str, str]] = {}
    errors: list[str] = []
    if not path.is_file():
        return users, ["TSV_MISSING"]
    with path.open(newline="", encoding="utf-8") as f:
        for number, row in enumerate(csv.reader(f, delimiter="\t"), 1):
            if not row:
                continue
            if len(row) != 4:
                errors.append(f"TSV_INVALID_ROW line={number}")
                continue
            user, password, _created, status = row
            if not re.fullmatch(r"[A-Za-z0-9._-]{3,64}", user):
                errors.append(f"TSV_INVALID_ID line={number}")
                continue
            if user in users:
                errors.append(f"TSV_DUPLICATE user={user}")
            if status not in {"active", "blocked"}:
                errors.append(f"TSV_INVALID_STATUS user={user}")
            if not password:
                errors.append(f"TSV_EMPTY_PASSWORD user={user}")
            users[user] = (password, status)
    return users, errors


def read_eap(path: Path) -> tuple[dict[str, str], list[str]]:
    secrets: dict[str, str] = {}
    errors: list[str] = []
    if not path.is_file():
        return secrets, ["CONF_MISSING"]
    data = path.read_text(encoding="utf-8")
    # The generated format consists of simple, flat eap-* blocks.
    for block in re.finditer(r"(?m)^\s*eap-[\w.-]+\s*\{([^{}]*)\}", data):
        body = block.group(1)
        name = re.search(r'(?m)^\s*id\s*=\s*"?([^"\s]+)"?\s*$', body)
        secret = re.search(r'(?m)^\s*secret\s*=\s*"([^"]*)"\s*$', body)
        if not name or not secret:
            errors.append("CONF_INCOMPLETE_EAP_BLOCK")
            continue
        user = name.group(1)
        if user in secrets:
            errors.append(f"CONF_DUPLICATE user={user}")
        secrets[user] = secret.group(1)
    if not re.search(r"(?m)^\s*secrets\s*\{", data):
        errors.append("CONF_NO_SECRETS_BLOCK")
    return secrets, errors


def audit(tsv: Path, conf: Path, selected: set[str] | None = None) -> tuple[list[str], int, int]:
    users, errors = read_users(tsv)
    loaded, conf_errors = read_eap(conf)
    errors.extend(conf_errors)
    for user, (password, status) in sorted(users.items()):
        if selected is not None and user not in selected:
            continue
        if status == "active":
            if user not in loaded:
                errors.append(f"ACTIVE_MISSING_EAP user={user}")
            elif not hmac.compare_digest(password, loaded[user]):
                errors.append(f"SECRET_MISMATCH user={user}")
        elif user in loaded:
            errors.append(f"BLOCKED_HAS_EAP user={user}")
    for user in sorted(set(loaded) - set(users)):
        if selected is None or user in selected:
            errors.append(f"CONF_ORPHAN user={user}")
    if selected:
        for user in sorted(selected - set(users)):
            errors.append(f"USER_MISSING_IN_TSV user={user}")
    return errors, len(users), len(loaded)


def self_test() -> None:
    import tempfile
    with tempfile.TemporaryDirectory() as folder:
        tsv = Path(folder) / "users.tsv"
        conf = Path(folder) / "users.conf"
        tsv.write_text("alpha\tpassword123\t2026-10-01T00:00:00Z\tactive\n"
                       "beta\tpassword999\t2026-10-01T00:00:00Z\tblocked\n")
        conf.write_text('secrets {\n eap-alpha {\n id = "alpha"\n secret = "password123"\n }\n}\n')
        issues, count, eap_count = audit(tsv, conf)
        assert not issues and (count, eap_count) == (2, 1), issues
        conf.write_text('secrets {\n eap-alpha {\n id = "alpha"\n secret = "wrongpass"\n }\n}\n')
        issues, *_ = audit(tsv, conf)
        assert any(x.startswith("SECRET_MISMATCH ") for x in issues), issues
        conf.write_text("secrets {\n}\n")
        issues, *_ = audit(tsv, conf)
        assert any(x.startswith("ACTIVE_MISSING_EAP ") for x in issues), issues
    print("IKEV2_AUDIT_SELF_TEST=PASS")


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--tsv", type=Path,
                   default=Path("/opt/irangate/ikev2/users.tsv"))
    p.add_argument("--conf", type=Path,
                   default=Path("/etc/swanctl/conf.d/irangate-ikev2-users.conf"))
    p.add_argument("--user", action="append",
                   help="Check only one account (repeatable)")
    p.add_argument("--strict", action="store_true",
                   help="Exit nonzero if any discrepancies are detected")
    p.add_argument("--self-test", action="store_true")
    args = p.parse_args()
    if args.self_test:
        self_test()
        return 0
    selected = set(args.user) if args.user else None
    try:
        errors, total, eap_count = audit(args.tsv, args.conf, selected)
    except (OSError, UnicodeError) as e:
        print(f"ERROR: audit read failed: {type(e).__name__}", file=sys.stderr)
        return 2
    print(f"TSV_ACCOUNTS={total} EAP_SECRETS={eap_count} DISCREPANCIES={len(errors)}")
    for issue in errors:
        print(issue)
    print("READ_ONLY=YES PASSWORDS_PRINTED=NO")
    return 1 if args.strict and errors else 0


if __name__ == "__main__":
    sys.exit(main())
