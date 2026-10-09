# SURENA — Existing Server Operations / مدیریت سرور موجود

[فارسی](#فارسی--مشاهده-تنظیمات-و-بهروزرسانی) · [English](#english--existing-deployment) · [@NET_SPOOF](https://t.me/NET_SPOOF)

## فارسی — مشاهده تنظیمات و به‌روزرسانی

### تشخیص اینکه سرور Master است یا Node

روی سرور فعلی **فقط برای مشاهده**:

```bash
echo "OS:"; . /etc/os-release; echo "$PRETTY_NAME"
for s in irangate-webpanel openvpn@server postgresql irangate-node-agent irangate-node-control openvpn-server@server; do
  printf '%-25s %s\n' "$s" "$(systemctl is-active "$s" 2>/dev/null || true)"
done
for f in /etc/irangate/webpanel.env /etc/irangate-node-agent.env; do
  if [ -s "$f" ]; then echo "FOUND: $f"; fi
done
```

اگر `irangate-webpanel` فعال است، معمولاً پنل Master روی آن سرور اجرا می‌شود. اگر `irangate-node-agent` فعال است، Node روی همان سرور نصب شده است. ممکن است هر دو نقش وجود داشته باشند.

### پنل Master — منوی مشاهده و تغییر

```bash
sudo surena-panel
# در نسخه‌های قبلی:
sudo ovp-panel
```

منوی پنل از جمله این گزینه‌ها را دارد: 1 نمایش اطلاعات، 2 تغییر پورت پنل، 3 تغییر دامنه، 4 مسیر پنل، 5 دامنه سابسکریپشن، 6 پورت سابسکریپشن، 8 پورت OpenVPN، 9 آدرس عمومی، 10 وضعیت سرویس‌ها، 12 لاگ، 14 بکاپ. گزینه 13 فرایند **Git-based source update** را اجرا می‌کند و روی نصب آفلاین بدون `.git` یا بدون بکاپ معتبر، روش ارتقای عمومی نیست.

نمایش تنظیمات عمومی بدون افشای رمزهای ادمین:

```bash
sudo awk -F= '/^(WEBPANEL_PORT|PANEL_DOMAIN|WEBPANEL_BASEPATH|SUBSCRIPTION_PORT|SUBSCRIPTION_PUBLIC_HOST|SUBSCRIPTION_SCHEME)=/ {print}' /etc/irangate/webpanel.env
sudo systemctl status irangate-webpanel --no-pager
sudo journalctl -u irangate-webpanel -n 40 --no-pager
```

تنظیمات محرمانه `JWT_SECRET`، رمز ادمین، DB DSN و توکن‌ها را در چت یا لاگ عمومی ارسال نکنید.

### Node — مدیریت تنظیمات

```bash
sudo ovp-node
```

گزینه‌های Node: 1 وضعیت، 2 پورت Agent، 3 تغییر توکن مشترک، 4 آدرس کانفیگ کاربران، 5 پورت کانفیگ کاربران، 6 پورت واقعی OpenVPN نود، 7 کانفیگ تست و 8 **حذف نود**. گزینه 8 مخرب است.

نمایش امن بدون توکن:

```bash
sudo awk -F= '/^(NODE_NAME|NODE_LOCATION|NODE_AGENT_PORT|NODE_PUBLIC_HOST|NODE_OVPN_PORT|NODE_OVPN_PROTO|NODE_OPENVPN_LISTEN_PORT)=/ {print}' /etc/irangate-node-agent.env
sudo systemctl is-active irangate-node-agent irangate-node-control openvpn-server@server
```

تغییر Agent port یا shared token باید در **Master → Nodes** نیز ثبت شود. پورت Agent به‌صورت پیش‌فرض 8091/TCP و Control 5024/TCP است؛ دسترسی این پورت‌های مدیریتی را به IP مستر محدود کنید.

### به‌روزرسانی سرور موجود — بدون حذف کاربران

**نصاب آفلاین Master `SURENA-offline-*.run` فقط برای نصب تازه است. آن را روی پنل موجود اجرا نکنید.** همچنین `--resume` یک ابزار ارتقا نیست.

اگر فقط می‌خواهید **لوگو، صفحه ورود، منوی مدیریتی و ترجمه‌های SURENA** به‌روزرسانی شوند، روی دستگاهی که دسترسی GitHub دارد سورس تازه را دریافت کرده و یک آرشیو UI بسازید:

```bash
gh auth login -h github.com -p https -w
gh repo clone Net-spoof/IranGate-OV-Reza-dev surena-ui-src -- --branch l2tp-dev
tar -czf surena-ui-update.tar.gz -C surena-ui-src webpanel/frontend scripts/ovp-panel
scp surena-ui-update.tar.gz root@YOUR_SERVER_IP:/root/
```

روی سرور **Master موجود**، ابتدا مسیرهای نصب را بررسی کنید:

```bash
test -d /opt/irangate/webpanel/frontend
test -f /usr/local/bin/ovp-panel
```

در صورت تطبیق مسیرها، UI-only upgrade را با بکاپ اجرا کنید (بدون آپدیت Go/backend، دیتابیس، OpenVPN یا Node):

```bash
sudo bash <<'BASH'
set -Eeuo pipefail
umask 077
STAGE="$(mktemp -d)"
BACK="/root/surena-ui-rollback-$(date +%Y%m%d-%H%M%S)"
FRONT="/opt/irangate/webpanel/frontend"
mkdir -p "$BACK"
tar -xzf /root/surena-ui-update.tar.gz -C "$STAGE"
test -s "$STAGE/webpanel/frontend/logo/surena.webp"
test -f "$STAGE/webpanel/frontend/index.html"
bash -n "$STAGE/scripts/ovp-panel"
test -d "$FRONT"
cp -a "$FRONT" "$BACK/frontend"
cp -a /usr/local/bin/ovp-panel "$BACK/ovp-panel"
cp -a "$STAGE/webpanel/frontend/." "$FRONT/"
install -m 0755 "$STAGE/scripts/ovp-panel" /usr/local/bin/ovp-panel
ln -sfn /usr/local/bin/ovp-panel /usr/local/bin/surena-panel
rm -rf "$STAGE"
printf 'SURENA UI updated; rollback: %s\n' "$BACK"
BASH
```

این دستور سرویس‌ها را ری‌استارت نمی‌کند؛ صفحه مرورگر را با Hard Refresh باز کنید. **برای ارتقای هسته اجرایی و دیتابیس/نود روی سرور فعال، ابتدا نسخه فعلی، نوع نصب، بکاپ و تغییرات سفارشی را بررسی کنید.** این کار نیازمند برنامه ارتقا و Rollback مستقل است.

## English — Existing Deployment

**Identify the deployment:** check the systemd services `irangate-webpanel` (Master) and `irangate-node-agent` (Node).

**Master configuration:** `sudo surena-panel` (legacy alias: `sudo ovp-panel`). Use the manager for panel port/domain/subscription/OpenVPN edits; inspect non-secret fields in `/etc/irangate/webpanel.env`. Avoid displaying JWT secrets, database connection strings and passwords.

**Node configuration:** `sudo ovp-node`. It can show status and edit the Agent port, shared token, advertised client host/port and OpenVPN listen port. Check non-secret fields in `/etc/irangate-node-agent.env`. After changing the Agent port or token, update the pairing settings under **Master → Nodes**.

**Existing-install upgrades:** Fresh offline `.run` installers and the Node installer are **not in-place update mechanisms**. For a branding-only change, the preceding Persian section includes a backed-up, static-files-and-menu-only workflow. Do not update backend binaries, PKI, OpenVPN, IKEv2, databases or production nodes without inspecting current deployment state and preparing a tested rollback.

**Telegram support:** [@NET_SPOOF](https://t.me/NET_SPOOF)
