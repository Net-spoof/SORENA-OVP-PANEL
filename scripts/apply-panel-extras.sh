#!/usr/bin/env bash
set -Eeuo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RESTART=0
SKIP_BUILD=0
NO_PACKAGES=0
NO_SING_BOX_DOWNLOAD=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --restart)
            RESTART=1
            shift
            ;;
        --skip-build)
            SKIP_BUILD=1
            shift
            ;;
        --no-packages)
            NO_PACKAGES=1
            shift
            ;;
        --no-sing-box-download)
            NO_SING_BOX_DOWNLOAD=1
            shift
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 2
            ;;
    esac
done

log() {
    echo "[panel-extras] $*"
}

find_go() {
    for path in \
        "$(command -v go 2>/dev/null || true)" \
        /usr/local/go/bin/go \
        /usr/bin/go \
        /snap/bin/go
    do
        [[ -n "$path" && -x "$path" ]] && {
            echo "$path"
            return
        }
    done

    return 1
}

build_backend() {
    if [[ "$SKIP_BUILD" -eq 1 ]]; then
        log "Web panel backend build skipped"
        return 0
    fi

    local go_bin
    go_bin="$(find_go)" || {
        echo "Go compiler not found" >&2
        exit 1
    }

    local output
    output="$(mktemp)"

    (
        cd "$REPO_DIR/webpanel/backend"
        "$go_bin" build -trimpath -o "$output" .
    )

    test -s "$output"

    install -m 0755 \
        "$output" \
        /usr/local/bin/irangate-webpanel

    rm -f "$output"

    log "Web panel backend deployed"
}

install_master_runtimes() {
    local file

    for file in "$REPO_DIR"/deploy/master/scripts/*.py; do
        [[ -f "$file" ]] || continue

        install -m 0755 \
            "$file" \
            "/usr/local/bin/$(basename "$file")"
    done

    if [[ -f "$REPO_DIR/scripts/ovp-panel" ]]; then
        install -m 0755 \
            "$REPO_DIR/scripts/ovp-panel" \
            /usr/local/bin/ovp-panel
    fi
}

deploy_frontend() {
    local src="$REPO_DIR/webpanel/frontend"
    local dst

    [[ -d "$src" ]] || return

    for dst in \
        /opt/irangate/webpanel/frontend \
        /var/www/html
    do
        [[ -d "$dst" ]] || continue

        mkdir -p "$dst/js"

        find "$src" \
            -maxdepth 1 \
            -type f \
            -print0 |
        xargs -0 -r -I{} \
            install -m 0644 "{}" "$dst/"

        if [[ -d "$src/js" ]]; then
            find "$src/js" \
                -maxdepth 1 \
                -type f \
                -print0 |
            xargs -0 -r -I{} \
                install -m 0644 "{}" "$dst/js/"
        fi
    done
}

main() {
    cd "$REPO_DIR"

    build_backend
    install_master_runtimes

    bash "$REPO_DIR/scripts/install-backup-v2-extras-wrappers.sh"

    ike_args=()

    [[ "$NO_PACKAGES" -eq 0 ]] ||
        ike_args+=(--no-packages)

    bash "$REPO_DIR/scripts/install-ikev2.sh" \
        "${ike_args[@]}"

    routing_args=()

    [[ "$NO_SING_BOX_DOWNLOAD" -eq 0 ]] ||
        routing_args+=(--no-download)

    bash "$REPO_DIR/scripts/install-routing-extras.sh" \
        "${routing_args[@]}"

    deploy_frontend

    systemctl daemon-reload

    if [[ "$RESTART" -eq 1 ]]; then
        systemctl restart irangate-webpanel
    fi


# IRANGATE_ROUTING_IKEV2_INSTALL_V1

if [[ -f "$REPO_DIR/scripts/irangate-routing" ]]; then
  log "Installing IranGate routing runtime"

  install \
    -o root \
    -g root \
    -m 0755 \
    "$REPO_DIR/scripts/irangate-routing" \
    /usr/local/bin/irangate-routing

  if command -v sing-box >/dev/null 2>&1; then
    /usr/local/bin/irangate-routing install-service
  else
    warn "sing-box is not installed; routing service installation skipped"
  fi
fi

if [[ -f "$REPO_DIR/scripts/ensure-ikev2-runtime.sh" ]]; then
  log "Installing IranGate IKEv2 runtime validator"

  install \
    -o root \
    -g root \
    -m 0755 \
    "$REPO_DIR/scripts/ensure-ikev2-runtime.sh" \
    /usr/local/sbin/irangate-ensure-ikev2-runtime

  /usr/local/sbin/irangate-ensure-ikev2-runtime
fi


# IRANGATE_OPENVPN_PERMISSIONS_INSTALL_V1

if [[ -f "$REPO_DIR/scripts/ensure-openvpn-runtime-permissions.sh" ]]; then
  log "Installing OpenVPN runtime permission validator"

  install \
    -o root \
    -g root \
    -m 0755 \
    "$REPO_DIR/scripts/ensure-openvpn-runtime-permissions.sh" \
    /usr/local/sbin/irangate-ensure-openvpn-permissions

  /usr/local/sbin/irangate-ensure-openvpn-permissions
fi

log "All panel extras applied ✅"
}

main "$@"
