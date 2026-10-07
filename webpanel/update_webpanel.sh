#!/bin/bash
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
echo "🔄 Updating IranGate Web Panel..."
SOURCE_DIR="/root/ov/irangate/webpanel/frontend"
DEST_DIR="/opt/irangate/webpanel/frontend"
if [ ! -d "$SOURCE_DIR" ]; then
    echo "❌ Source directory not found: $SOURCE_DIR"
    exit 1
fi
if [ ! -d "$DEST_DIR" ]; then
    echo "❌ Destination directory not found: $DEST_DIR"
    echo "Please install the webpanel first"
    exit 1
fi
echo "📁 Copying updated files..."
cp -r "$SOURCE_DIR"/* "$DEST_DIR/"
chown -R root:root "$DEST_DIR"
chmod -R 644 "$DEST_DIR"
find "$DEST_DIR" -type d -exec chmod 755 {} \;
WRAPPER_INSTALLER="$SCRIPT_DIR/../scripts/install-backup-v2-extras-wrappers.sh"
if [ -x "$WRAPPER_INSTALLER" ]; then
    echo "🧰 Installing Backup v2 wrappers..."
    bash "$WRAPPER_INSTALLER"
else
    echo "⚠️ Backup v2 wrapper installer not found: $WRAPPER_INSTALLER"
fi

echo "🔄 Restarting webpanel service..."
systemctl restart irangate-webpanel
if systemctl is-active --quiet irangate-webpanel; then
    echo "✅ Webpanel updated and restarted successfully!"

    REPAIR_SCRIPT="$SCRIPT_DIR/../scripts/repair-subscription-nginx.sh"
    if [ -f "$REPAIR_SCRIPT" ]; then
        echo "🔧 Verifying subscription reverse proxy..."
        bash "$REPAIR_SCRIPT"
    else
        echo "⚠️ Subscription nginx repair script not found: $REPAIR_SCRIPT"
    fi

    WEBPANEL_PORT="$(
        awk -F= '/^WEBPANEL_PORT=/{print $2; exit}' /etc/irangate/webpanel.env 2>/dev/null || true
    )"
    WEBPANEL_PORT="${WEBPANEL_PORT:-444}"
    echo "🌐 Access your webpanel at: http://$(hostname -I | awk '{print $1}'):${WEBPANEL_PORT}/"
else
    echo "❌ Failed to restart webpanel service"
    systemctl status irangate-webpanel --no-pager
    exit 1
fi
echo "🎉 Update complete!"