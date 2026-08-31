#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_DIR="$REPO_DIR/deploy/master/scripts"
SYSTEMD_DIR="$REPO_DIR/deploy/master/systemd"

[[ "${EUID:-$(id -u)}" -eq 0 ]] || {
    echo "Must run as root" >&2
    exit 1
}

for path in \
    "$SCRIPT_DIR/irangate-pg-l2tp-sync.py" \
    "$SYSTEMD_DIR/irangate-pg-l2tp-sync.service" \
    "$SYSTEMD_DIR/irangate-pg-l2tp-sync.timer"
do
    [[ -f "$path" ]] || {
        echo "Missing repository asset: $path" >&2
        exit 1
    }
done

install -m 0755 \
    "$SCRIPT_DIR/irangate-pg-l2tp-sync.py" \
    /usr/local/bin/irangate-pg-l2tp-sync.py

install -m 0644 \
    "$SYSTEMD_DIR/irangate-pg-l2tp-sync.service" \
    /etc/systemd/system/irangate-pg-l2tp-sync.service

install -m 0644 \
    "$SYSTEMD_DIR/irangate-pg-l2tp-sync.timer" \
    /etc/systemd/system/irangate-pg-l2tp-sync.timer

systemctl daemon-reload

if [[ -s /etc/irangate-db.env ]] && \
   python3 -c 'import psycopg2' >/dev/null 2>&1
then
    systemctl enable --now irangate-pg-l2tp-sync.timer
    systemctl start irangate-pg-l2tp-sync.service || true
    echo "L2TP PostgreSQL accounting enabled ✅"
else
    systemctl disable --now irangate-pg-l2tp-sync.timer >/dev/null 2>&1 || true
    echo "L2TP runtime installed; PostgreSQL sync not enabled because DB runtime is not ready yet"
fi
