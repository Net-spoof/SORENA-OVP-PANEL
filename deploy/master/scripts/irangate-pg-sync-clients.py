#!/usr/bin/env python3
import json
import base64
import hashlib
import hmac
import time
import argparse
import re
from decimal import Decimal, InvalidOperation
import urllib.error
import urllib.request
from pathlib import Path
import psycopg2
from datetime import datetime, timezone

DB_ENV = Path("/etc/irangate-db.env")
WEBPANEL_ENV = Path("/etc/irangate/webpanel.env")


def env_load(path):
    data = {}

    for line in Path(path).read_text().splitlines():
        line = line.strip()

        if not line or line.startswith("#") or "=" not in line:
            continue

        key, value = line.split("=", 1)
        data[key.strip()] = value.strip().strip('"').strip("'")

    return data


DB_ENV_DATA = env_load(DB_ENV)
WEBPANEL_ENV_DATA = env_load(WEBPANEL_ENV)

WEBPANEL_PORT = WEBPANEL_ENV_DATA.get("WEBPANEL_PORT", "8080")
BASE = f"http://127.0.0.1:{WEBPANEL_PORT}"
IRANGATE_DATA_DIR = Path(
    WEBPANEL_ENV_DATA.get("IRANGATE_DATA_DIR", "/opt/irangate").strip()
    or "/opt/irangate"
)
ROOT_ADMIN_JSON = IRANGATE_DATA_DIR / "webpanel" / "admin.json"


def b64url(data):
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def canonical_root_username():
    env_username = WEBPANEL_ENV_DATA.get(
        "WEBPANEL_ADMIN_USER",
        "",
    ).strip()

    try:
        admin_data = json.loads(ROOT_ADMIN_JSON.read_text())
    except Exception as exc:
        raise RuntimeError(
            f"failed to read canonical root admin from {ROOT_ADMIN_JSON}: {exc}"
        ) from exc

    username = str(admin_data.get("username") or "").strip()
    if not username:
        raise RuntimeError(
            f"canonical root admin username is missing in {ROOT_ADMIN_JSON}"
        )

    if env_username and env_username != username:
        print(
            "WARNING root admin username mismatch:",
            f"WEBPANEL_ADMIN_USER={env_username!r}",
            f"canonical_admin={username!r};",
            "using canonical admin.json value",
        )

    return username


def build_local_api_token():
    secret = WEBPANEL_ENV_DATA.get("JWT_SECRET", "").strip()
    username = canonical_root_username()

    if not secret:
        raise RuntimeError(
            "JWT_SECRET is missing from /etc/irangate/webpanel.env"
        )

    now = int(time.time())

    header = {
        "alg": "HS256",
        "typ": "JWT",
    }

    payload = {
        "username": username,
        "iat": now,
        "exp": now + 300,
    }

    header_part = b64url(
        json.dumps(
            header,
            separators=(",", ":"),
        ).encode()
    )

    payload_part = b64url(
        json.dumps(
            payload,
            separators=(",", ":"),
        ).encode()
    )

    signing_input = (
        header_part + "." + payload_part
    ).encode()

    signature = hmac.new(
        secret.encode(),
        signing_input,
        hashlib.sha256,
    ).digest()

    return (
        header_part
        + "."
        + payload_part
        + "."
        + b64url(signature)
    )


def api_clients(token):
    req = urllib.request.Request(BASE + "/api/clients")
    req.add_header("Authorization", "Bearer " + token)
    req.add_header("Cache-Control", "no-cache")

    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            raw = response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        body = exc.read(4096).decode("utf-8", "replace").strip()
        raise RuntimeError(
            f"GET /api/clients failed: HTTP {exc.code}: {body[:1000]}"
        ) from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(
            f"GET /api/clients failed: {exc.reason}"
        ) from exc

    try:
        data = json.loads(raw)
    except Exception as exc:
        raise RuntimeError(
            f"GET /api/clients returned invalid JSON: {raw[:500]!r}"
        ) from exc

    if isinstance(data, list):
        return data

    return data.get("clients") or data.get("data") or []


def to_int(v):
    try:
        if v in (None, ""):
            return 0
        return int(float(v))
    except Exception:
        return 0


def clean_time(v):
    if not v:
        return None
    s = str(v)
    if s.startswith("0001-01-01"):
        return None
    return s


def truthy(v):
    if isinstance(v, bool):
        return v
    if v is None:
        return False
    return str(v).strip().lower() in ("1", "true", "yes", "on", "active", "connected")


def status_from_usage(raw_status, limit_bytes, total, expire_at, connected=False):
    exp = clean_time(expire_at)
    if exp:
        try:
            ss = str(exp).replace("Z", "+00:00")
            dt = datetime.fromisoformat(ss)
            if dt.tzinfo is None:
                dt = dt.replace(tzinfo=timezone.utc)
            if dt <= datetime.now(timezone.utc):
                return "expired"
        except Exception:
            pass

    if limit_bytes > 0 and total >= limit_bytes:
        return "traffic_limit_exceeded"

    return "active" if truthy(connected) else "inactive"



def safe_count(value, field, allow_zero=True):
    if isinstance(value, bool) or value is None or value == "":
        raise ValueError(field + " missing")
    try:
        number = Decimal(str(value))
        if not number.is_finite() or number != number.to_integral_value():
            raise ValueError("not an integer")
        result = int(number)
    except (InvalidOperation, TypeError, ValueError, OverflowError) as exc:
        raise ValueError(field + " invalid") from exc
    if result < 0 or (not allow_zero and result < 1):
        raise ValueError(field + " out of range")
    return result


def normal_expiry(value):
    if value is None or value == "" or str(value).startswith("0001-01-01"):
        return None
    try:
        dt = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except (TypeError, ValueError) as exc:
        raise ValueError("invalid expires_at") from exc
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def new_client_json(name):
    """Read canonical local metadata for an API client not yet in PostgreSQL."""
    if not re.fullmatch(r"[A-Za-z0-9_.@+-]{1,128}", name):
        raise RuntimeError("SYNC FAIL-CLOSED: unsafe new client identifier")
    path = IRANGATE_DATA_DIR / "database" / "clients" / (name + ".json")
    try:
        data = json.loads(path.read_text())
    except (OSError, ValueError) as exc:
        raise RuntimeError(
            "SYNC FAIL-CLOSED: new client missing canonical metadata: " + name
        ) from exc
    if not isinstance(data, dict) or str(data.get("name") or "") != name:
        raise RuntimeError(
            "SYNC FAIL-CLOSED: canonical client metadata mismatch: " + name
        )
    return data


def safe_api_items(raw, existing, canonical_loader=new_client_json):
    if not isinstance(raw, list):
        raise RuntimeError("SYNC FAIL-CLOSED: API client list missing or invalid")
    # An empty API is valid on a genuinely empty fresh installation only.
    # With existing users, the count floor below refuses truncated snapshots.
    seen = set()
    result = []
    for row in raw:
        if not isinstance(row, dict):
            raise RuntimeError("SYNC FAIL-CLOSED: non-object API record")
        name = row.get("name") or row.get("client_name") or row.get("common_name")
        name = str(name or "").strip()
        if not name or name in seen:
            raise RuntimeError("SYNC FAIL-CLOSED: missing or duplicate client name")
        seen.add(name)
        # SURENA's Go API can omit zero-valued quota/expiry metadata:
        # persistCreatedClientMetadata only writes traffic_limit_bytes when >0.
        # Such an omission is safe ONLY if the existing DB already has the
        # equivalent default value. Otherwise fail closed (no silent reset).
        old = existing.get(name)
        canonical = None
        if old is None and any(
            field not in row
            for field in ("traffic_limit_bytes", "connection_limit", "expires_at")
        ):
            canonical = canonical_loader(name)

        if "traffic_limit_bytes" in row:
            quota = safe_count(row["traffic_limit_bytes"], name + ".quota")
        elif old is not None and int(old[0] or 0) == 0:
            quota = 0
        elif old is None and canonical is not None:
            # The Go client's json:"traffic_limit_bytes,omitempty" tag
            # removes a zero quota from both API and canonical JSON.
            if "traffic_limit_bytes" not in canonical:
                quota = 0
            else:
                raise RuntimeError("SYNC FAIL-CLOSED: omitted quota conflicts with disk for " + name)
        else:
            raise RuntimeError("SYNC FAIL-CLOSED: ambiguous missing quota for " + name)

        if "connection_limit" in row:
            maxconn = max(1, safe_count(row["connection_limit"], name + ".max_connections"))
        elif old is not None and int(old[1] or 1) == 1:
            maxconn = 1
        elif old is None and canonical is not None and "connection_limit" not in canonical:
            maxconn = 1
        else:
            raise RuntimeError("SYNC FAIL-CLOSED: ambiguous missing connection limit for " + name)

        if "expires_at" in row:
            expiry = normal_expiry(row["expires_at"])
        elif old is not None and normal_expiry(old[2]) is None:
            expiry = None
        elif old is None and canonical is not None and "expires_at" not in canonical:
            expiry = None
        else:
            raise RuntimeError("SYNC FAIL-CLOSED: ambiguous missing expiry for " + name)

        result.append((name, row, quota, maxconn, expiry))
    return result


def min_api_client_count(existing_count):
    return max(1, (existing_count * 3) // 4) if existing_count else 0


def retain_optional_metadata(row, old):
    """old is r[1:] for SELECT quota, max_conn, expiry, token, url, nodes, cipher, ..."""
    old_token = old[3] if old else None
    old_url = old[4] if old else None
    old_nodes = old[5] if old else None
    old_cipher = old[6] if old else None
    token = row.get("subscription_token") or old_token
    url = row.get("subscription_url") or old_url
    nodes = row.get("nodes")
    if not isinstance(nodes, (dict, list)) or (not nodes and old_nodes):
        nodes = old_nodes if old_nodes is not None else []
    cipher = row.get("cipher") or old_cipher
    return token, url, nodes, cipher


def self_test():
    assert safe_count("1000000000000", "quota") == 1000000000000
    for v in (None, "", "-1", "nan", "inf", "100.5", True):
        try:
            safe_count(v, "quota")
            raise AssertionError("invalid quota accepted")
        except ValueError:
            pass
    assert safe_count(0, "quota") == 0
    assert normal_expiry(None) is None
    assert normal_expiry("0001-01-01T00:00:00Z") is None
    assert normal_expiry("2026-10-15T12:00:00Z").year == 2026
    # Validate omission handling separately with 50 unique legitimate records.
    zero_rows = [{"name": "u" + str(i), "connection_limit": 1,
                  "expires_at": None} for i in range(50)]
    zero_db = {r["name"]: (0, 1, None) for r in zero_rows}
    assert len(safe_api_items(zero_rows, zero_db)) == 50
    assert safe_api_items([], {}) == []
    assert safe_api_items([], {"old": (0, 1, None)}) == []
    assert min_api_client_count(0) == 0
    assert min_api_client_count(1) == 1
    assert min_api_client_count(475) == 356
    assert min_api_client_count(100) == 75
    first_client = [{"name": "fresh", "expires_at": None}]
    assert safe_api_items(
        first_client, {},
        canonical_loader=lambda name: {"name": name}
    )[0][2:4] == (0, 1)
    try:
        safe_api_items(
            first_client, {},
            canonical_loader=lambda name: {
                "name": name, "traffic_limit_bytes": 10_000_000
            }
        )
    except RuntimeError:
        pass
    else:
        raise AssertionError("missing quota masked a real disk limit")
    # Each test must actually OMIT the corresponding API field. Otherwise
    # its explicit API value should validly override an older DB value.
    for field, changed in (
        ("traffic_limit_bytes", (10, 1, None)),
        ("connection_limit", (0, 2, None)),
        ("expires_at", (0, 1, datetime(2026, 10, 20, tzinfo=timezone.utc))),
    ):
        bad_rows = [dict(r) for r in zero_rows]
        bad_rows[0].pop(field, None)
        bad_db = dict(zero_db)
        bad_db["u0"] = changed
        try:
            safe_api_items(bad_rows, bad_db)
        except RuntimeError:
            pass
        else:
            raise AssertionError("ambiguous API omission was accepted: " + field)

    # Regression: API explicitly setting a field may intentionally change
    # the old database value; omission alone must never do that.
    changed_db = dict(zero_db)
    changed_db["u0"] = (10, 2, datetime(2026, 10, 20, tzinfo=timezone.utc))
    explicit_rows = [dict(r) for r in zero_rows]
    explicit_rows[0].update({
        "traffic_limit_bytes": 25,
        "connection_limit": 1,
        "expires_at": None,
    })
    assert safe_api_items(explicit_rows, changed_db)[0][2:4] == (25, 1)

    # Regression: optional database columns follow exact SELECT field order.
    sample_old = (0, 1, None, "prior-token", "prior-url",
                  ["finland"], "AES-256-GCM", None, None, None)
    retained = retain_optional_metadata({}, sample_old)
    assert retained == ("prior-token", "prior-url",
                        ["finland"], "AES-256-GCM")
    assert retain_optional_metadata({
        "subscription_token": "new-token",
        "subscription_url": "new-url",
        "nodes": ["azerbaijan"],
        "cipher": "AES-128-GCM",
    }, sample_old) == (
        "new-token", "new-url", ["azerbaijan"], "AES-128-GCM"
    )
    print("pg_sync_guarded_self_test = PASS")


def main(apply=False):
    # Acquire a consistent API snapshot before starting any DB updates.
    api_snapshot = api_clients(build_local_api_token())
    env = DB_ENV_DATA
    conn = psycopg2.connect(env["IRANGATE_DB_DSN"], connect_timeout=10)
    conn.autocommit = False
    try:
        with conn.cursor() as cur:
            if apply:
                # Serialize manual invocation with the systemd one-shot service.
                cur.execute("SELECT pg_try_advisory_xact_lock(740015, 1405)")
                if not cur.fetchone()[0]:
                    raise RuntimeError("SYNC FAIL-CLOSED: concurrent metadata sync")
            cur.execute("""
                SELECT client_name, traffic_limit_bytes, max_connections,
                       expire_at, subscription_token, subscription_url,
                       nodes, cipher, last_seen, last_connection, created_at
                FROM clients
            """)
            existing = {r[0]: r[1:] for r in cur.fetchall()}
            records = safe_api_items(api_snapshot, existing)
            cur.execute("SELECT COALESCE(SUM(total_bytes),0) FROM traffic_totals")
            historic_usage_before = int(cur.fetchone()[0])
            # A partial API response must never make historical clients vanish
            # from sync consideration. There are legitimately PG-only accounts.
            api_floor = min_api_client_count(len(existing))
            if len(records) < api_floor:
                raise RuntimeError(
                    "SYNC FAIL-CLOSED: API returned "
                    + str(len(records)) + " clients; expected >="
                    + str(api_floor)
                )

            changed_limits = 0
            changed_dates = 0
            changed_connections = 0
            new_accounts = 0
            preserved_pg_only = len(existing) - sum(
                1 for name, *_ in records if name in existing
            )
            processed = []
            for name, row, quota, maxconn, expiry in records:
                old = existing.get(name)
                if old is None:
                    new_accounts += 1
                else:
                    old_quota, old_max, old_exp, *_ = old
                    changed_limits += int(quota != int(old_quota or 0))
                    changed_connections += int(maxconn != int(old_max or 1))
                    norm_old_exp = normal_expiry(old_exp)
                    changed_dates += int(
                        (expiry is None) != (norm_old_exp is None)
                        or (
                            expiry is not None and norm_old_exp is not None
                            and abs((expiry - norm_old_exp).total_seconds()) > 60
                        )
                    )

                # Missing optional fields must not wipe subscription credentials
                # or node assignments that already exist in PostgreSQL.
                token, url, nodes, cipher = retain_optional_metadata(row, old)

                total = to_int(row.get("global_total_bytes"))
                if total <= 0:
                    total = to_int(row.get("total_bytes"))
                if total <= 0:
                    total = to_int(row.get("used_bytes"))
                if total <= 0:
                    total = to_int(row.get("bytes_received")) + to_int(
                        row.get("bytes_sent")
                    )
                status = status_from_usage(
                    row.get("status"), quota, total, expiry,
                    row.get("connected"),
                )

                last_seen = clean_time(row.get("last_seen"))
                last_connection = clean_time(row.get("last_connection"))
                created_at = clean_time(row.get("created_at"))
                processed.append((
                    name, quota, expiry, maxconn, token, url,
                    json.dumps(nodes), status, cipher,
                    last_seen, last_connection, created_at,
                ))

            changed_total = (
                changed_limits + changed_dates + changed_connections + new_accounts
            )
            # A bulk update across almost the whole user-base is more likely
            # a broken API response than a legitimate renewal operation.
            change_ceiling = max(10, len(records) // 20)
            if changed_total > change_ceiling:
                raise RuntimeError(
                    "SYNC FAIL-CLOSED: metadata changes "
                    + str(changed_total) + " exceed max "
                    + str(change_ceiling)
                )
            print("mode =", "APPLY" if apply else "DRY-RUN")
            print("api_clients =", len(records))
            print("pg_clients =", len(existing))
            print("pg_only_preserved =", preserved_pg_only)
            print("new_clients =", new_accounts)
            print("quota_changes =", changed_limits)
            print("expiry_changes =", changed_dates)
            print("connection_changes =", changed_connections)
            print("change_ceiling =", change_ceiling)

            if apply:
                for rec in processed:
                    cur.execute("""
                        INSERT INTO clients (
                            client_name, display_name, traffic_limit_bytes,
                            expire_at, max_connections, is_enabled,
                            subscription_token, subscription_url, nodes,
                            status, cipher, last_seen, last_connection,
                            created_at, updated_at
                        )
                        VALUES (
                            %s,%s,%s,%s,%s,true,%s,%s,%s::jsonb,%s,%s,%s,%s,
                            COALESCE(%s::timestamptz,now()),now()
                        )
                        ON CONFLICT (client_name) DO UPDATE SET
                            display_name = EXCLUDED.display_name,
                            traffic_limit_bytes = EXCLUDED.traffic_limit_bytes,
                            expire_at = EXCLUDED.expire_at,
                            max_connections = EXCLUDED.max_connections,
                            subscription_token = EXCLUDED.subscription_token,
                            subscription_url = EXCLUDED.subscription_url,
                            nodes = EXCLUDED.nodes,
                            status = EXCLUDED.status,
                            cipher = EXCLUDED.cipher,
                            last_seen = COALESCE(EXCLUDED.last_seen,clients.last_seen),
                            last_connection = COALESCE(
                                EXCLUDED.last_connection,clients.last_connection
                            ),
                            updated_at = now()
                    """, (rec[0], rec[0], rec[1], rec[2], rec[3],
                          rec[4], rec[5], rec[6], rec[7], rec[8],
                          rec[9], rec[10], rec[11]))
                    cur.execute("""
                        INSERT INTO traffic_totals(
                            client_name,rx_bytes,tx_bytes,total_bytes,updated_at
                        ) VALUES(%s,0,0,0,now())
                        ON CONFLICT (client_name) DO NOTHING
                    """, (rec[0],))

                # Absolute usage is owned exclusively by accounting ticks.
                cur.execute("SELECT COALESCE(SUM(total_bytes),0) FROM traffic_totals")
                historic_usage_after = int(cur.fetchone()[0])
                # The OpenVPN accounting tick updates traffic_totals concurrently.
                # A strictly increasing aggregate is expected; the metadata
                # sync SQL only inserts zero-valued rows for new accounts.
                # A decrease is never expected and must fail closed.
                if historic_usage_after < historic_usage_before:
                    raise RuntimeError("SYNC FAIL-CLOSED: historical traffic total decreased")
                print("historical_usage_non_decreasing = PASS")
                conn.commit()
                print("synced_clients =", len(processed))
            else:
                conn.rollback()
                print("database_modified = NO")
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--apply", action="store_true")
    mode.add_argument("--self-test", action="store_true")
    args = ap.parse_args()
    if args.self_test:
        self_test()
    else:
        main(apply=args.apply)
