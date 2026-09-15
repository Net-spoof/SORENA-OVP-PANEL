#!/usr/bin/env python3
import json
import base64
import hashlib
import hmac
import time
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


env = DB_ENV_DATA
token = build_local_api_token()
clients = api_clients(token)

conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
conn.autocommit = False

with conn.cursor() as cur:
    for c in clients:
        name = c.get("name") or c.get("client_name") or c.get("common_name")
        if not name:
            continue

        limit_bytes = to_int(c.get("traffic_limit_bytes"))
        max_conn = to_int(c.get("connection_limit")) or 1

        rx = to_int(c.get("global_bytes_received") or c.get("bytes_received"))
        tx = to_int(c.get("global_bytes_sent") or c.get("bytes_sent"))
        total = to_int(c.get("global_total_bytes") or c.get("total_bytes") or c.get("used_bytes"))
        if total <= 0:
            total = rx + tx

        expire_at_clean = clean_time(c.get("expires_at"))
        status = status_from_usage(c.get("status"), limit_bytes, total, expire_at_clean, c.get("connected"))

        cur.execute("""
            INSERT INTO clients (
                client_name, display_name, traffic_limit_bytes, expire_at,
                max_connections, is_enabled, subscription_token,
                subscription_url, nodes, status, cipher,
                last_seen, last_connection, created_at, updated_at
            )
            VALUES (
                %s,%s,%s,%s,%s,true,%s,%s,%s::jsonb,%s,%s,%s,%s,
                COALESCE(%s::timestamptz, now()), now()
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
                last_seen = EXCLUDED.last_seen,
                last_connection = EXCLUDED.last_connection,
                updated_at = now()
        """, (
            name, name, limit_bytes, expire_at_clean,
            max_conn, c.get("subscription_token"), c.get("subscription_url"),
            json.dumps(c.get("nodes") or []), status,
            c.get("cipher"), clean_time(c.get("last_seen")),
            clean_time(c.get("last_connection")), clean_time(c.get("created_at"))
        ))

        cur.execute("""
            INSERT INTO traffic_totals (
                client_name, rx_bytes, tx_bytes, total_bytes, updated_at
            )
            VALUES (%s,0,0,0,now())
            ON CONFLICT (client_name) DO NOTHING
        """, (name,))

conn.commit()
conn.close()

print("synced_clients =", len(clients))
