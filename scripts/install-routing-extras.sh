#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

NO_DOWNLOAD=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-download)
            NO_DOWNLOAD=1
            shift
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 2
            ;;
    esac
done

[[ "${EUID:-$(id -u)}" -eq 0 ]] || {
    echo "Must run as root" >&2
    exit 1
}

log() {
    echo "[routing-install] $*"
}

install -d -m 0700 \
    /opt/irangate/routing

install -d -m 0755 \
    /etc/sing-box

install -m 0755 \
    "$REPO_DIR/scripts/irangate-routing" \
    /usr/local/bin/irangate-routing

WAS_ACTIVE=0

if systemctl is-active --quiet irangate-routing.service; then
    WAS_ACTIVE=1
fi

if ! command -v sing-box >/dev/null 2>&1; then
    if [[ -x "$REPO_DIR/offline-assets/sing-box" ]]; then
        install -m 0755 \
            "$REPO_DIR/offline-assets/sing-box" \
            /usr/local/bin/sing-box

        log "Installed bundled sing-box"
    elif [[ "$NO_DOWNLOAD" -eq 0 ]]; then
        command -v curl >/dev/null 2>&1 || {
            echo "curl is required to install sing-box" >&2
            exit 1
        }

        installer="$(mktemp)"

        curl -fsSL \
            https://sing-box.app/install.sh \
            -o "$installer"

        if [[ -n "${SING_BOX_VERSION:-}" ]]; then
            sh "$installer" \
                --version "$SING_BOX_VERSION"
        else
            sh "$installer"
        fi

        rm -f "$installer"

        log "Installed sing-box using the official installer"
    else
        log "sing-box is missing and download is disabled"
        exit 0
    fi
fi

command -v sing-box >/dev/null 2>&1 || {
    echo "sing-box installation failed" >&2
    exit 1
}

sing-box version | head -5

systemctl disable --now sing-box.service \
    >/dev/null 2>&1 || true

/usr/local/bin/irangate-routing init

/usr/local/bin/irangate-routing \
    generate-engine \
    --write \
    --check

if [[ "$WAS_ACTIVE" -eq 1 ]]; then
    systemctl restart irangate-routing.service
fi

log "Routing runtime installed ✅"
