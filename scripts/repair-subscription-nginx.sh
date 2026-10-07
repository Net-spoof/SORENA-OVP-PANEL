#!/usr/bin/env bash
set -Eeuo pipefail

# IRANGATE_SUBSCRIPTION_NGINX_REPAIR_V2
# repair stale IranGate subscription
# Repair stale IranGate subscription reverse-proxy upstreams.
# The authoritative backend port is read from /etc/irangate/webpanel.env.
# Only nginx locations that proxy /sub/ to a local TCP port are changed.

ENV_FILE="${IRANGATE_WEBPANEL_ENV:-/etc/irangate/webpanel.env}"
STAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP_ROOT="/root/irangate-subscription-nginx-backup-${STAMP}"
PY_RESULT="/tmp/irangate-subscription-nginx-repair-$$.txt"

cleanup() {
    rm -f "$PY_RESULT"
}
trap cleanup EXIT

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
    echo "ERROR: run as root" >&2
    exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
    echo "ERROR: webpanel env not found: $ENV_FILE" >&2
    exit 1
fi

WEBPANEL_PORT="$(
    awk -F= '
        /^WEBPANEL_PORT=/ {
            sub(/^[^=]*=/, "")
            gsub(/^[[:space:]]+|[[:space:]]+$/, "")
            print
            exit
        }
    ' "$ENV_FILE"
)"

if [[ ! "$WEBPANEL_PORT" =~ ^[0-9]+$ ]] ||
   (( WEBPANEL_PORT < 1 || WEBPANEL_PORT > 65535 )); then
    echo "ERROR: invalid WEBPANEL_PORT in $ENV_FILE: ${WEBPANEL_PORT:-<empty>}" >&2
    exit 1
fi

echo "IRANGATE_SUBSCRIPTION_NGINX_REPAIR_V2"
echo "IranGate webpanel backend: 127.0.0.1:$WEBPANEL_PORT"

SUBSCRIPTION_PORT="$(
    awk -F= '
        /^SUBSCRIPTION_PORT=/ {
            sub(/^[^=]*=/, "")
            gsub(/^[[:space:]]+|[[:space:]]+$/, "")
            print
            exit
        }
    ' "$ENV_FILE"
)"

if [[ ! "$SUBSCRIPTION_PORT" =~ ^[0-9]+$ ]] ||
   (( SUBSCRIPTION_PORT < 1 || SUBSCRIPTION_PORT > 65535 )); then
    SUBSCRIPTION_PORT="$(
        nginx -T 2>/dev/null |
        awk '
            /configuration file .*irangate-subscription\.conf:/ { in_file=1; next }
            /^# configuration file / && in_file { exit }
            in_file && /listen[[:space:]]+[0-9]+[[:space:]]+ssl;/ {
                for (i=1; i<=NF; i++) {
                    if ($i ~ /^[0-9]+$/) {
                        print $i
                        exit
                    }
                }
            }
        '
    )"
fi

echo "IranGate subscription port: ${SUBSCRIPTION_PORT:-unknown}"

# Confirm the backend is reachable before touching nginx. Any HTTP response is
# acceptable here; connection failure is not.
BACKEND_CODE="$(
    curl -sS         -o /dev/null         -w '%{http_code}'         --max-time 5         "http://127.0.0.1:${WEBPANEL_PORT}/"         2>/dev/null || true
)"

if [[ -z "$BACKEND_CODE" || "$BACKEND_CODE" == "000" ]]; then
    echo "ERROR: webpanel backend is not reachable on 127.0.0.1:$WEBPANEL_PORT" >&2
    exit 1
fi

echo "Backend HTTP: $BACKEND_CODE"

mkdir -p "$BACKUP_ROOT"

export WEBPANEL_PORT BACKUP_ROOT PY_RESULT

python3 - <<'PY'
import os
import re
import shutil
from pathlib import Path

backend_port = os.environ["WEBPANEL_PORT"]
backup_root = Path(os.environ["BACKUP_ROOT"])
result_path = Path(os.environ["PY_RESULT"])

roots = [
    Path("/etc/nginx/conf.d"),
    Path("/etc/nginx/sites-enabled"),
]

# Only rewrite a local HTTP upstream that is explicitly a /sub/ target.
proxy_re = re.compile(
    r'proxy_pass\s+http://(?:127\.0\.0\.1|localhost):(?P<port>\d+)/sub/\s*;'
)

changed = []
seen_realpaths = set()

for root in roots:
    if not root.exists():
        continue

    for entry in sorted(root.glob("*.conf")) if root.name == "conf.d" else sorted(root.iterdir()):
        try:
            real = entry.resolve(strict=True)
        except (FileNotFoundError, RuntimeError):
            continue

        if not real.is_file() or real in seen_realpaths:
            continue
        seen_realpaths.add(real)

        try:
            text = real.read_text()
        except UnicodeDecodeError:
            continue

        if "/sub/" not in text or "proxy_pass" not in text:
            continue

        replacements = []

        def repl(match):
            old_port = match.group("port")
            if old_port == backend_port:
                return match.group(0)
            replacements.append((old_port, backend_port))
            return f"proxy_pass http://127.0.0.1:{backend_port}/sub/;"

        new_text = proxy_re.sub(repl, text)

        if new_text == text:
            continue

        # Preserve an absolute-path mirror under the backup root.
        relative = Path(str(real).lstrip("/"))
        backup = backup_root / relative
        backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(real, backup)

        real.write_text(new_text)
        changed.append((real, backup, replacements))

with result_path.open("w") as out:
    for real, backup, replacements in changed:
        out.write(f"{real}\t{backup}\n")
        for old_port, new_port in replacements:
            print(f"FIXED {real}: 127.0.0.1:{old_port}/sub/ -> 127.0.0.1:{new_port}/sub/")

if not changed:
    print("No stale local /sub/ nginx upstreams found; configuration already matches the webpanel port.")
else:
    print(f"Changed nginx files: {len(changed)}")
PY

if [[ -s "$PY_RESULT" ]]; then
    echo
    echo "Testing nginx configuration..."

    if ! nginx -t; then
        echo "ERROR: nginx -t failed; restoring changed files." >&2

        while IFS=$'\t' read -r target backup; do
            [[ -n "$target" && -f "$backup" ]] || continue
            cp -a "$backup" "$target"
        done < "$PY_RESULT"

        nginx -t || true
        exit 1
    fi

    systemctl reload nginx
    echo "nginx reloaded successfully."
    echo "Backup: $BACKUP_ROOT"
else
    rmdir "$BACKUP_ROOT" 2>/dev/null || true
fi

echo
echo "Current subscription proxy_pass directives:"
LIVE_SUB_LINES="$(
    nginx -T 2>/dev/null |
        awk '
            /location[^\n]*\/sub\// { in_sub=1; depth=0 }
            in_sub {
                print
                opens=gsub(/\{/, "{")
                closes=gsub(/\}/, "}")
                depth += opens - closes
                if (depth <= 0 && closes > 0) in_sub=0
            }
        ' |
        grep -E 'location|proxy_pass' || true
)"
printf '%s\n' "$LIVE_SUB_LINES"

EXPECTED_PROXY="proxy_pass http://127.0.0.1:$WEBPANEL_PORT/sub/;"
if ! grep -Fq "$EXPECTED_PROXY" <<<"$LIVE_SUB_LINES"; then
    echo "ERROR: live nginx config does not contain expected subscription upstream:" >&2
    echo "  $EXPECTED_PROXY" >&2
    exit 1
fi

echo
echo "Testing subscription route directly on backend..."
DIRECT_SUB_CODE="$(
    curl -sS         -o /dev/null         -w '%{http_code}'         --max-time 5         "http://127.0.0.1:${WEBPANEL_PORT}/sub/__invalid_test_token__"         2>/dev/null || true
)"
echo "Direct subscription HTTP: $DIRECT_SUB_CODE"

if [[ -z "$DIRECT_SUB_CODE" || "$DIRECT_SUB_CODE" == "000" || "$DIRECT_SUB_CODE" == "502" ]]; then
    echo "ERROR: direct backend subscription route is unhealthy" >&2
    exit 1
fi

if [[ "$SUBSCRIPTION_PORT" =~ ^[0-9]+$ ]]; then
    echo
    echo "Testing nginx subscription listener on port $SUBSCRIPTION_PORT..."

    ERROR_LOG="/var/log/nginx/error.log"
    BEFORE_LINES=0
    if [[ -f "$ERROR_LOG" ]]; then
        BEFORE_LINES="$(wc -l < "$ERROR_LOG" 2>/dev/null || echo 0)"
    fi

    PUBLIC_SUB_CODE="$(
        curl -k -sS             --resolve "subo.konkuriran.sbs:${SUBSCRIPTION_PORT}:127.0.0.1"             -o /dev/null             -w '%{http_code}'             --max-time 8             "https://subo.konkuriran.sbs:${SUBSCRIPTION_PORT}/sub/__invalid_test_token__"             2>/dev/null || true
    )"

    echo "Nginx subscription HTTP: $PUBLIC_SUB_CODE"

    if [[ -z "$PUBLIC_SUB_CODE" || "$PUBLIC_SUB_CODE" == "000" || "$PUBLIC_SUB_CODE" == "502" ]]; then
        echo "ERROR: nginx subscription listener is still unhealthy" >&2
        if [[ -f "$ERROR_LOG" ]]; then
            echo
            echo "New nginx error log lines:"
            tail -n "+$((BEFORE_LINES + 1))" "$ERROR_LOG" 2>/dev/null | tail -80 || true
        fi
        exit 1
    fi
fi

echo "Subscription nginx repair complete and verified."
