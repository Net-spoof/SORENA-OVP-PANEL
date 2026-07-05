#!/usr/bin/env bash

set -Eeuo pipefail

# IranGate safe uninstaller.
#
# Design goals:
# - Remove IranGate-managed runtime and data.
# - Remove only IranGate PostgreSQL database/role.
# - Remove only IranGate Nginx configuration.
# - Never flush global iptables tables.
# - Never disable UFW or Fail2ban.
# - Never remove Nginx/PostgreSQL packages.
# - Preserve Let's Encrypt certificates by default.
# - Preserve source code by default.
# - Create an external final Backup v2 before destructive removal.
#
# Usage:
#   ./uninstall.sh
#   ./uninstall.sh --dry-run
#   ./uninstall.sh --yes
#   ./uninstall.sh --yes --skip-backup
#   ./uninstall.sh --yes --remove-ufw-rules
#   ./uninstall.sh --yes --purge-openvpn-packages


RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'


AUTO_YES=0
DRY_RUN=0
SKIP_BACKUP=0
REMOVE_UFW_RULES=0
PURGE_OPENVPN_PACKAGES=0


log_info() {
    echo -e "${CYAN}[INFO]${NC} $*"
}


log_ok() {
    echo -e "${GREEN}[OK]${NC} $*"
}


log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}


log_error() {
    echo -e "${RED}[ERROR]${NC} $*" >&2
}


die() {
    log_error "$*"
    exit 1
}


usage() {
    cat <<'EOF'
IranGate Safe Uninstaller

Usage:
  ./uninstall.sh [options]

Options:
  --yes
      Skip interactive confirmation.

  --dry-run
      Show detected resources and planned actions.
      Nothing is removed.

  --skip-backup
      Do not create a final external Backup v2.

  --remove-ufw-rules
      Remove detected IranGate port allow rules from UFW.
      Disabled by default because firewall rules may be shared.

  --purge-openvpn-packages
      Purge only OpenVPN and Easy-RSA packages after cleanup.
      Nginx and PostgreSQL are never purged.

  -h, --help
      Show this help.
EOF
}


while [ "$#" -gt 0 ]; do
    case "$1" in

        --yes)
            AUTO_YES=1
            ;;

        --dry-run)
            DRY_RUN=1
            ;;

        --skip-backup)
            SKIP_BACKUP=1
            ;;

        --remove-ufw-rules)
            REMOVE_UFW_RULES=1
            ;;

        --purge-openvpn-packages)
            PURGE_OPENVPN_PACKAGES=1
            ;;

        -h|--help)
            usage
            exit 0
            ;;

        *)
            die "Unknown argument: $1"
            ;;
    esac

    shift
done


if [ "$EUID" -ne 0 ]; then
    die "This script must be run as root."
fi


on_error() {
    local rc=$?
    local line="${1:-unknown}"

    echo
    log_error "Uninstaller stopped at line ${line}, exit code ${rc}."
    log_error "Review the output before retrying."

    exit "$rc"
}


trap 'on_error "$LINENO"' ERR


read_env_value() {
    local key="$1"
    local file="$2"
    local line=""
    local value=""

    [ -f "$file" ] || return 0

    line="$(
        grep -m1 -E "^${key}=" "$file" \
        2>/dev/null \
        || true
    )"

    [ -n "$line" ] || return 0

    value="${line#*=}"
    value="${value%$'\r'}"

    if [ "${#value}" -ge 2 ]; then

        if [[ "$value" == \"*\" ]]; then
            value="${value:1:${#value}-2}"

        elif [[ "$value" == \'*\' ]]; then
            value="${value:1:${#value}-2}"
        fi

    fi

    printf '%s' "$value"
}


remove_rule_all() {
    local table="$1"
    local chain="$2"

    shift 2

    local removed=0
    local guard=0

    while true; do

        guard=$((guard + 1))

        if [ "$guard" -gt 100 ]; then
            log_warn "iptables cleanup guard reached for ${table}/${chain}"
            break
        fi

        if [ "$table" = "filter" ]; then

            if ! iptables -C "$chain" "$@" \
                >/dev/null 2>&1
            then
                break
            fi

            iptables -D "$chain" "$@"

        else

            if ! iptables -t "$table" \
                -C "$chain" "$@" \
                >/dev/null 2>&1
            then
                break
            fi

            iptables -t "$table" \
                -D "$chain" "$@"
        fi

        removed=$((removed + 1))
    done

    log_info \
        "Removed ${removed} matching iptables rule(s) from ${table}/${chain}"
}


remove_ufw_rule() {
    local rule="$1"

    [ -n "$rule" ] || return 0

    if ! command -v ufw >/dev/null 2>&1; then
        return 0
    fi

    if ! ufw status \
        2>/dev/null \
        | grep -q '^Status: active'
    then
        return 0
    fi

    ufw --force delete allow "$rule" \
        >/dev/null 2>&1 \
        || true

    log_info "Requested UFW removal for: $rule"
}


DB_ENV='/etc/irangate-db.env'
WEB_ENV='/etc/irangate/webpanel.env'

DB_NAME="$(
    read_env_value \
        IRANGATE_DB_NAME \
        "$DB_ENV"
)"

DB_USER="$(
    read_env_value \
        IRANGATE_DB_USER \
        "$DB_ENV"
)"

WEBPANEL_PORT="$(
    read_env_value \
        WEBPANEL_PORT \
        "$WEB_ENV"
)"

SUBSCRIPTION_PORT="$(
    read_env_value \
        SUBSCRIPTION_PORT \
        "$WEB_ENV"
)"

PANEL_DOMAIN="$(
    read_env_value \
        PANEL_DOMAIN \
        "$WEB_ENV"
)"



# LEGACY_SUBSCRIPTION_PORT_FALLBACK
if [ -z "$SUBSCRIPTION_PORT" ] \
    && [ -f /etc/nginx/conf.d/irangate-subscription.conf ]
then

    SUBSCRIPTION_PORT="$(
        awk '
            $1 == "listen" {
                value = $2
                gsub(/;/, "", value)
                sub(/^.*:/, "", value)
                gsub(/[^0-9]/, "", value)

                if (value != "") {
                    print value
                    exit
                }
            }
        ' \
        /etc/nginx/conf.d/irangate-subscription.conf \
        2>/dev/null \
        || true
    )"

fi

OPENVPN_CONFIG=''

if [ -f /etc/openvpn/server.conf ]; then

    OPENVPN_CONFIG='/etc/openvpn/server.conf'

elif [ -f /etc/openvpn/server/server.conf ]; then

    OPENVPN_CONFIG='/etc/openvpn/server/server.conf'

fi


OPENVPN_PORT=''
OPENVPN_PROTO=''
VPN_CIDR=''


if [ -n "$OPENVPN_CONFIG" ]; then

    OPENVPN_PORT="$(
        awk '
            $1 == "port" {
                print $2
                exit
            }
        ' "$OPENVPN_CONFIG"
    )"

    OPENVPN_PROTO="$(
        awk '
            $1 == "proto" {
                print $2
                exit
            }
        ' "$OPENVPN_CONFIG"
    )"

    OPENVPN_PROTO="${OPENVPN_PROTO%%[46]}"

    VPN_NET="$(
        awk '
            $1 == "server" {
                print $2
                exit
            }
        ' "$OPENVPN_CONFIG"
    )"

    VPN_MASK="$(
        awk '
            $1 == "server" {
                print $3
                exit
            }
        ' "$OPENVPN_CONFIG"
    )"

    if [ -n "${VPN_NET:-}" ] \
        && [ -n "${VPN_MASK:-}" ] \
        && command -v python3 >/dev/null 2>&1
    then

        VPN_CIDR="$(
            python3 -c "import ipaddress,sys; print(ipaddress.ip_network(sys.argv[1]+chr(47)+sys.argv[2], strict=False))" "$VPN_NET" "$VPN_MASK" 2>/dev/null || true
        )"

    fi
fi


EXT_IF="$(
    ip route get 1.1.1.1 \
    2>/dev/null \
    | awk '
        {
            for (i = 1; i <= NF; i++) {
                if ($i == "dev") {
                    print $(i + 1)
                    exit
                }
            }
        }
    ' \
    || true
)"


mapfile -t DISCOVERED_UNITS < <(
    systemctl list-unit-files \
        --no-legend \
        --no-pager \
        2>/dev/null \
    | awk '
        $1 ~ /^irangate-.*\.(service|timer)$/ {
            print $1
        }
    '
)


UNITS=(
    "${DISCOVERED_UNITS[@]}"
    "openvpn-iptables.service"
    "openvpn@server.service"
    "openvpn-server@server.service"
)


mapfile -t UNITS < <(
    printf '%s\n' "${UNITS[@]}" \
    | sed '/^$/d' \
    | sort -u
)


echo
echo '========================================'
echo '===== IRANGATE UNINSTALL PLAN ====='
echo '========================================'

echo "Mode: $(
    if [ "$DRY_RUN" -eq 1 ]; then
        echo 'DRY-RUN'
    else
        echo 'DESTRUCTIVE'
    fi
)"

echo
echo 'Detected systemd units:'

printf '  %s\n' "${UNITS[@]}"


echo
echo 'Detected runtime:'
echo "  OpenVPN config: ${OPENVPN_CONFIG:-none}"
echo "  OpenVPN port: ${OPENVPN_PORT:-unknown}"
echo "  OpenVPN proto: ${OPENVPN_PROTO:-unknown}"
echo "  VPN CIDR: ${VPN_CIDR:-unknown}"
echo "  External interface: ${EXT_IF:-unknown}"

echo
echo 'Detected Web access:'
echo "  WebPanel port: ${WEBPANEL_PORT:-unknown}"
echo "  Subscription port: ${SUBSCRIPTION_PORT:-unknown}"
echo "  Panel domain: ${PANEL_DOMAIN:-none}"

echo
echo 'Detected PostgreSQL objects:'
echo "  Database: ${DB_NAME:-unknown}"
echo "  Role: ${DB_USER:-unknown}"

echo
echo 'Preserved intentionally:'
echo '  - Source repository'
echo '  - Nginx package and unrelated configs'
echo '  - PostgreSQL package/service and unrelated databases'
echo '  - UFW and Fail2ban services'
echo '  - Let'\''s Encrypt certificates'
echo '  - Docker containers'
echo '  - External backups under /root'


if [ "$DRY_RUN" -eq 1 ]; then

    echo
    log_ok "DRY-RUN complete. Nothing was removed."
    exit 0

fi


if [ "$AUTO_YES" -ne 1 ]; then

    echo
    echo -e "${RED}WARNING:${NC}"
    echo 'This will remove IranGate runtime, OpenVPN PKI, clients,'
    echo 'IranGate PostgreSQL database/role, and IranGate web runtime.'
    echo

    read -r -p \
        'Type REMOVE-IRANGATE to continue: ' \
        confirm

    if [ "$confirm" != 'REMOVE-IRANGATE' ]; then
        log_warn "Uninstallation cancelled."
        exit 0
    fi

fi


STAMP="$(
    date +%Y%m%d-%H%M%S
)"

SAFETY_DIR="/root/irangate-uninstall-safety-${STAMP}"

mkdir -p "$SAFETY_DIR"

chmod 700 "$SAFETY_DIR"


log_info \
    "Creating pre-removal safety metadata in ${SAFETY_DIR}"


if [ -f "$DB_ENV" ]; then
    cp -a \
        "$DB_ENV" \
        "$SAFETY_DIR/irangate-db.env"
fi


if [ -f "$WEB_ENV" ]; then
    cp -a \
        "$WEB_ENV" \
        "$SAFETY_DIR/webpanel.env"
fi


mkdir -p \
    "$SAFETY_DIR/nginx" \
    "$SAFETY_DIR/systemd"


find /etc/nginx \
    -maxdepth 3 \
    -type f \
    -name 'irangate*.conf' \
    -exec cp -a {} "$SAFETY_DIR/nginx/" \; \
    2>/dev/null \
    || true


find /etc/systemd/system \
    -maxdepth 1 \
    -type f \
    \( \
        -name 'irangate-*.service' \
        -o -name 'irangate-*.timer' \
        -o -name 'openvpn-iptables.service' \
        -o -name 'openvpn@server.service' \
    \) \
    -exec cp -a {} "$SAFETY_DIR/systemd/" \; \
    2>/dev/null \
    || true


crontab -l \
    > "$SAFETY_DIR/root-crontab.txt" \
    2>/dev/null \
    || true


if [ "$SKIP_BACKUP" -eq 0 ]; then

    if [ ! -x \
        /usr/local/bin/irangate-backup-v2-export
    ]; then
        die \
            "Backup v2 exporter not found. Use --skip-backup only when an external verified backup already exists."
    fi

    log_info \
        "Creating final Backup v2 before removal..."


    /usr/local/bin/irangate-backup-v2-export \
        > "$SAFETY_DIR/backup-export.log" \
        2>&1


    LATEST_BACKUP="$(
        find \
            /opt/irangate/backups/v2 \
            -maxdepth 1 \
            -type f \
            -name 'irangate-backup-v2-*.tar.gz' \
            -printf '%T@ %p\n' \
            2>/dev/null \
        | sort -nr \
        | head -n1 \
        | cut -d' ' -f2-
    )"


    [ -n "$LATEST_BACKUP" ] \
        || die "Backup exporter completed but no archive was found."


    [ -s "$LATEST_BACKUP" ] \
        || die "Latest Backup v2 archive is empty."


    FINAL_BACKUP="$SAFETY_DIR/final-backup-v2.tar.gz"


    cp -a \
        "$LATEST_BACKUP" \
        "$FINAL_BACKUP"


    chmod 600 "$FINAL_BACKUP"


    gzip -t "$FINAL_BACKUP"


    sha256sum \
        "$FINAL_BACKUP" \
        > "$SAFETY_DIR/final-backup-v2.sha256"


    log_ok \
        "External final backup created: $FINAL_BACKUP"

else

    log_warn \
        "Final Backup v2 creation skipped by request."

fi


echo
echo '========================================'
echo '===== STOP SERVICES AND TIMERS ====='
echo '========================================'


for unit in "${UNITS[@]}"; do

    case "$unit" in
        *.timer)
            systemctl stop "$unit" \
                >/dev/null 2>&1 \
                || true

            systemctl disable "$unit" \
                >/dev/null 2>&1 \
                || true
            ;;
    esac

done


for unit in "${UNITS[@]}"; do

    case "$unit" in
        *.service)
            systemctl stop "$unit" \
                >/dev/null 2>&1 \
                || true

            systemctl disable "$unit" \
                >/dev/null 2>&1 \
                || true
            ;;
    esac

done


log_ok \
    "IranGate services and timers stopped."


echo
echo '========================================'
echo '===== CLEAN IRANGATE IPTABLES RULES ====='
echo '========================================'


if command -v iptables >/dev/null 2>&1 \
    && [ -n "$VPN_CIDR" ]
then

    remove_rule_all \
        filter \
        FORWARD \
        -s "$VPN_CIDR" \
        -j ACCEPT


    if [ -n "$EXT_IF" ]; then

        remove_rule_all \
            nat \
            POSTROUTING \
            -s "$VPN_CIDR" \
            ! -d "$VPN_CIDR" \
            -o "$EXT_IF" \
            -j MASQUERADE

    fi


    if command -v netfilter-persistent \
        >/dev/null 2>&1
    then
        netfilter-persistent save \
            >/dev/null 2>&1 \
            || true
    fi

else

    log_warn \
        "VPN CIDR was not detected; no global firewall flush will be attempted."

fi


if [ "$REMOVE_UFW_RULES" -eq 1 ]; then

    log_info \
        "Removing detected IranGate UFW allow rules..."


    if [ -n "$OPENVPN_PORT" ] \
        && [ -n "$OPENVPN_PROTO" ]
    then
        remove_ufw_rule \
            "${OPENVPN_PORT}/${OPENVPN_PROTO}"
    fi


    if [ -n "$WEBPANEL_PORT" ]; then
        remove_ufw_rule \
            "${WEBPANEL_PORT}/tcp"
    fi


    if [ -n "$SUBSCRIPTION_PORT" ]; then
        remove_ufw_rule \
            "${SUBSCRIPTION_PORT}/tcp"
    fi


    if [ -n "$PANEL_DOMAIN" ]; then
        remove_ufw_rule '80/tcp'
        remove_ufw_rule '443/tcp'
    fi

else

    log_info \
        "UFW rules preserved. Use --remove-ufw-rules for explicit removal."

fi


echo
echo '========================================'
echo '===== REMOVE IRANGATE POSTGRES OBJECTS ====='
echo '========================================'


if [ -n "$DB_NAME" ]; then

    case "$DB_NAME" in
        postgres|template0|template1)
            die \
                "Refusing to remove protected PostgreSQL database: $DB_NAME"
            ;;
    esac


    if command -v runuser >/dev/null 2>&1 \
        && id postgres >/dev/null 2>&1
    then

        runuser \
            -u postgres \
            -- dropdb \
            --if-exists \
            --force \
            "$DB_NAME"


        log_ok \
            "PostgreSQL database removed: $DB_NAME"

    else

        die \
            "postgres OS user or runuser command not available."

    fi

else

    log_warn \
        "IranGate database name not detected; database removal skipped."

fi


if [ -n "$DB_USER" ]; then

    if [ "$DB_USER" = 'postgres' ]; then
        die \
            "Refusing to remove protected PostgreSQL role: postgres"
    fi


    runuser \
        -u postgres \
        -- dropuser \
        --if-exists \
        "$DB_USER"


    log_ok \
        "PostgreSQL role removed: $DB_USER"

else

    log_warn \
        "IranGate database role not detected; role removal skipped."

fi


echo
echo '========================================'
echo '===== REMOVE NGINX IRANGATE CONFIG ====='
echo '========================================'


rm -f \
    /etc/nginx/conf.d/irangate-*.conf \
    /etc/nginx/sites-enabled/irangate \
    /etc/nginx/sites-available/irangate


if command -v nginx >/dev/null 2>&1; then

    if nginx -t; then

        systemctl reload nginx \
            >/dev/null 2>&1 \
            || true

        log_ok \
            "Nginx configuration validated and reloaded."

    else

        log_warn \
            "Nginx config test failed after IranGate config removal; Nginx was not reloaded."

    fi

fi


log_info \
    "Let's Encrypt certificates were intentionally preserved."


echo
echo '========================================'
echo '===== REMOVE SYSTEMD UNIT FILES ====='
echo '========================================'


find /etc/systemd/system \
    -maxdepth 1 \
    -type f \
    \( \
        -name 'irangate-*.service' \
        -o -name 'irangate-*.timer' \
    \) \
    -delete


rm -f \
    /etc/systemd/system/openvpn-iptables.service \
    /etc/systemd/system/openvpn@server.service


rm -rf \
    /etc/systemd/system/openvpn@server.service.d


find /etc/systemd/system \
    -type l \
    \( \
        -name 'irangate-*.service' \
        -o -name 'irangate-*.timer' \
        -o -name 'openvpn-iptables.service' \
        -o -name 'openvpn@server.service' \
        -o -name 'openvpn-server@server.service' \
    \) \
    -delete \
    2>/dev/null \
    || true


systemctl daemon-reload

systemctl reset-failed \
    >/dev/null 2>&1 \
    || true


log_ok \
    "IranGate systemd units removed."


echo
echo '========================================'
echo '===== REMOVE PROJECT CRON ENTRIES ====='
echo '========================================'


CRON_OLD="$(
    mktemp
)"

CRON_NEW="$(
    mktemp
)"


if crontab -l \
    > "$CRON_OLD" \
    2>/dev/null
then

    grep -vE \
        '/usr/local/bin/irangate|/opt/irangate' \
        "$CRON_OLD" \
        > "$CRON_NEW" \
        || true


    if [ -s "$CRON_NEW" ]; then
        crontab "$CRON_NEW"
    else
        crontab -r \
            2>/dev/null \
            || true
    fi

fi


rm -f \
    "$CRON_OLD" \
    "$CRON_NEW"


rm -f \
    /etc/cron.d/irangate*


echo
echo '========================================'
echo '===== REMOVE IRANGATE FILES ====='
echo '========================================'


if [ -L /usr/local/bin/easyrsa ]; then

    EASYRSA_TARGET="$(
        readlink -f \
            /usr/local/bin/easyrsa \
            2>/dev/null \
            || true
    )"

    case "$EASYRSA_TARGET" in
        /etc/openvpn/easy-rsa/*)
            rm -f \
                /usr/local/bin/easyrsa
            ;;
    esac

fi


find /usr/local/bin \
    -maxdepth 1 \
    \( \
        -type f \
        -o -type l \
    \) \
    -name 'irangate*' \
    -delete


rm -f \
    /usr/bin/irangate


rm -rf \
    /opt/irangate \
    /opt/irangate-node \
    /etc/irangate \
    /var/log/irangate \
    /root/.irangate


rm -f \
    /etc/irangate-db.env \
    /etc/logrotate.d/irangate \
    /etc/logrotate.d/irangate-webpanel \
    /etc/logrotate.d/irangate-* \
    /etc/sysctl.d/99-openvpn.conf


rm -rf \
    /etc/openvpn/blocked-clients \
    /etc/openvpn/node-denied-clients \
    /etc/openvpn/client \
    /etc/openvpn/clients \
    /etc/openvpn/ccd \
    /etc/openvpn/easy-rsa \
    /etc/openvpn/logs \
    /etc/openvpn/server


rm -f \
    /etc/openvpn/server.conf \
    /etc/openvpn/crl.pem \
    /etc/openvpn/ipp.txt \
    /etc/openvpn/openvpn-status.log \
    /etc/openvpn/irangate-client-connect.sh \
    /etc/openvpn/update-resolv-conf


log_ok \
    "IranGate runtime and OpenVPN project data removed."


if [ "$PURGE_OPENVPN_PACKAGES" -eq 1 ]; then

    echo
    log_warn \
        "Purging OpenVPN and Easy-RSA packages..."

    apt-get remove \
        --purge \
        -y \
        openvpn \
        easy-rsa

    apt-get autoremove \
        -y

    log_ok \
        "OpenVPN and Easy-RSA packages purged."

else

    log_info \
        "OpenVPN/Easy-RSA packages preserved."

fi


echo
echo '========================================'
echo '===== FINAL VERIFICATION ====='
echo '========================================'


FAILED=0


if [ -e /opt/irangate ]; then
    log_error \
        "/opt/irangate still exists."
    FAILED=1
fi


if [ -e /etc/irangate ]; then
    log_error \
        "/etc/irangate still exists."
    FAILED=1
fi


if find /usr/local/bin \
    -maxdepth 1 \
    -name 'irangate*' \
    -print \
    2>/dev/null \
    | grep -q .
then
    log_error \
        "IranGate binaries still exist under /usr/local/bin."
    FAILED=1
fi


if systemctl list-unit-files \
    --no-legend \
    --no-pager \
    2>/dev/null \
    | awk '{print $1}' \
    | grep -qE '^irangate-.*\.(service|timer)$'
then
    log_error \
        "IranGate systemd units still exist."
    FAILED=1
fi


if [ -n "$DB_NAME" ] \
    && runuser \
        -u postgres \
        -- psql \
        -Atqc 'SELECT datname FROM pg_database;' \
        2>/dev/null \
        | grep -Fxq "$DB_NAME"
then
    log_error \
        "IranGate PostgreSQL database still exists: $DB_NAME"
    FAILED=1
fi


if [ -n "$DB_USER" ] \
    && runuser \
        -u postgres \
        -- psql \
        -Atqc 'SELECT rolname FROM pg_roles;' \
        2>/dev/null \
        | grep -Fxq "$DB_USER"
then
    log_error \
        "IranGate PostgreSQL role still exists: $DB_USER"
    FAILED=1
fi


if [ "$FAILED" -ne 0 ]; then
    die \
        "Uninstall completed with residual IranGate resources."
fi


echo
echo '========================================'
echo '===== IRANGATE UNINSTALL COMPLETE ✅ ====='
echo '========================================'

echo "Safety directory: $SAFETY_DIR"

echo
echo 'Preserved:'
echo '  ✅ Source repository'
echo '  ✅ Nginx package'
echo '  ✅ PostgreSQL service/package'
echo '  ✅ Unrelated PostgreSQL databases'
echo '  ✅ UFW service'
echo '  ✅ Fail2ban service'
echo '  ✅ Let'\''s Encrypt certificates'
echo '  ✅ Docker containers'
echo '  ✅ External backups'

echo
log_ok \
    "IranGate removal finished successfully."
