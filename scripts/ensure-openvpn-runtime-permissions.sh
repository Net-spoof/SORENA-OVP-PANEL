#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

CFG="${IRANGATE_OPENVPN_CONFIG:-/etc/openvpn/server.conf}"
SERVICE="${IRANGATE_OPENVPN_SERVICE:-openvpn@server.service}"
NO_RESTART="${IRANGATE_OPENVPN_NO_RESTART:-0}"

log() {
    echo "[openvpn-permissions] $*"
}

if [ "$(id -u)" -ne 0 ]; then
    log "This command must run as root ❌"
    exit 1
fi

if [ ! -f "$CFG" ]; then
    log "OpenVPN configuration not present yet; skipped."
    exit 0
fi

CONFIG_DIR="$(dirname "$CFG")"

read_config_value() {
    local directive="$1"

    awk -v directive="$directive" '
        /^[[:space:]]*[#;]/ {
            next
        }

        $1 == directive {
            print $2
            exit
        }
    ' "$CFG"
}

resolve_config_path() {
    local value="$1"

    if [ -z "$value" ]; then
        return 0
    fi

    if [[ "$value" = /* ]]; then
        printf '%s\n' "$value"
    else
        printf '%s/%s\n' "$CONFIG_DIR" "$value"
    fi
}

RUN_USER="$(read_config_value user)"
RUN_GROUP="$(read_config_value group)"

RUN_USER="${RUN_USER:-nobody}"
RUN_GROUP="${RUN_GROUP:-nogroup}"

getent passwd "$RUN_USER" >/dev/null || {
    log "OpenVPN runtime user does not exist: $RUN_USER ❌"
    exit 1
}

getent group "$RUN_GROUP" >/dev/null || {
    log "OpenVPN runtime group does not exist: $RUN_GROUP ❌"
    exit 1
}

CCD_RAW="$(read_config_value client-config-dir)"
CONNECT_RAW="$(read_config_value client-connect)"
DISCONNECT_RAW="$(read_config_value client-disconnect)"

CCD="$(resolve_config_path "${CCD_RAW:-ccd}")"
CONNECT_HOOK="$(resolve_config_path "$CONNECT_RAW")"
DISCONNECT_HOOK="$(resolve_config_path "$DISCONNECT_RAW")"

log "Config: $CFG"
log "Runtime identity: $RUN_USER:$RUN_GROUP"
log "CCD: $CCD"
log "Client-connect: ${CONNECT_HOOK:-not configured}"
log "Client-disconnect: ${DISCONNECT_HOOK:-not configured}"

# OpenVPN starts as root, then drops privileges to nobody:nogroup.
# It must retain traversal access to the configuration directory without
# exposing directory listings or private keys.
chown root:root "$CONFIG_DIR"
chmod 0711 "$CONFIG_DIR"

fix_runtime_directory() {
    local directory="$1"

    install -d \
        -o root \
        -g "$RUN_GROUP" \
        -m 0750 \
        "$directory"

    find "$directory" \
        -mindepth 1 \
        -maxdepth 1 \
        -type f \
        -exec chown root:"$RUN_GROUP" {} + \
        -exec chmod 0640 {} +
}

fix_runtime_directory "$CCD"

# The client-connect hook checks these directories after OpenVPN has dropped
# privileges, so they must also be traversable by the runtime group.
for directory in \
    "$CONFIG_DIR/blocked-clients" \
    "$CONFIG_DIR/node-denied-clients"
do
    if [ -e "$directory" ]; then
        fix_runtime_directory "$directory"
    fi
done

fix_hook() {
    local hook="$1"

    [ -n "$hook" ] || return 0

    [ -f "$hook" ] || {
        log "Configured hook does not exist: $hook ❌"
        exit 1
    }

    chown root:"$RUN_GROUP" "$hook"
    chmod 0750 "$hook"

    local parent
    parent="$(dirname "$hook")"

    while [ "$parent" != "/" ]; do
        case "$parent" in
            "$CONFIG_DIR"|"$CONFIG_DIR"/*)
                chmod g+rx "$parent"
                ;;
            /usr/local|/usr/local/*|/opt|/opt/*)
                chmod g+rx "$parent"
                ;;
        esac

        parent="$(dirname "$parent")"
    done
}

fix_hook "$CONNECT_HOOK"
fix_hook "$DISCONNECT_HOOK"

check_as_runtime_user() {
    local mode="$1"
    local path="$2"

    runuser -u "$RUN_USER" -- \
        test "$mode" "$path"
}

check_as_runtime_user -x "$CONFIG_DIR" || {
    log "$RUN_USER cannot traverse $CONFIG_DIR ❌"
    namei -l "$CONFIG_DIR" || true
    exit 1
}

check_as_runtime_user -r "$CCD" || {
    log "$RUN_USER cannot read $CCD ❌"
    namei -l "$CCD" || true
    exit 1
}

check_as_runtime_user -x "$CCD" || {
    log "$RUN_USER cannot traverse $CCD ❌"
    namei -l "$CCD" || true
    exit 1
}

if [ -n "$CONNECT_HOOK" ]; then
    check_as_runtime_user -x "$CONNECT_HOOK" || {
        log "$RUN_USER cannot execute $CONNECT_HOOK ❌"
        namei -l "$CONNECT_HOOK" || true
        exit 1
    }
fi

if [ -n "$DISCONNECT_HOOK" ]; then
    check_as_runtime_user -x "$DISCONNECT_HOOK" || {
        log "$RUN_USER cannot execute $DISCONNECT_HOOK ❌"
        namei -l "$DISCONNECT_HOOK" || true
        exit 1
    }
fi

log "Runtime access validation passed ✅"

if [ "$NO_RESTART" != "1" ] &&
   systemctl is-active --quiet "$SERVICE"
then
    systemctl restart "$SERVICE"
    sleep 2

    systemctl is-active --quiet "$SERVICE" || {
        log "OpenVPN failed after restart ❌"
        systemctl status "$SERVICE" --no-pager -l || true
        exit 1
    }

    log "OpenVPN restarted successfully ✅"
fi

log "OpenVPN runtime permissions ensured ✅"
