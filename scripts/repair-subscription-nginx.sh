#!/usr/bin/env bash
set -Eeuo pipefail

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

echo "IranGate webpanel backend: 127.0.0.1:$WEBPANEL_PORT"

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

echo "Subscription nginx repair complete."
