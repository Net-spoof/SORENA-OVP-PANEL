<div align="center">
  <img src="../webpanel/frontend/logo/surena.webp" width="112" alt="SURENA">
  <h1>SURENA · Node Agent</h1>
  <p>OpenVPN Foreign Node · Agent & Control APIs · Remote Client Operations</p>
  <a href="#فارسی--نصب-نود">فارسی</a> · <a href="#english--node-setup">English</a> ·
  <a href="https://t.me/NET_SPOOF">Telegram @NET_SPOOF</a>
</div>

---

## فارسی — نصب نود

این راهنما مخصوص **سرور خارجی Node** است؛ پنل مرکزی یا Master را با [راهنمای اصلی](../README.md) نصب کنید.

### پیش‌نیازها

- سرور لینوکسی جداگانه با دسترسی root، systemd، TUN، دسترسی اینترنت برای APT و دسترسی به مخزن خصوصی GitHub
- پورت UDP مربوط به OpenVPN (پیش‌فرض 1194) و امکان برقراری ارتباط Master با Agent و Control
- توکن اختصاصی و قوی برای ارتباط Master و Node

**مهم:** این اسکریپت نصب Node برای سرور موجودِ دارای کاربران یا گواهی‌های VPN طراحی نشده است؛ قبل از اجرا از داده‌های مهم بکاپ بگیرید. نصب Node، برخلاف بسته آفلاین Master، از APT و شبکه استفاده می‌کند.

### نصب با سورس کامل مخزن

چون مخزن خصوصی است، GitHub CLI را از قبل نصب و با حساب مجاز احراز هویت کنید، یا مخزن را با SSH key مجاز clone کنید:

~~~bash
gh auth login -h github.com -p https -w
gh repo clone Net-spoof/IranGate-OV-Reza-dev surena-src -- --branch l2tp-dev
cd surena-src
sudo bash node/install-node.sh
~~~

**فقط فایل تکی `node/install-node.sh` را دانلود و اجرا نکنید.** نصب به فایل `scripts/install-vpn-direct-bypass.sh` داخل همین مخزن وابسته است.

مقادیر موردنیاز:

| سؤال نصاب Node | نمونه | توضیح |
| --- | --- | --- |
| Node display name | Germany 1 | نام نمایشی در Master |
| Location code | DE | کد کشور |
| Agent API port | 8091 | پورت TCP Agent |
| Shared token | خودکار | توکن مشترک؛ در Master عیناً درج شود |
| OpenVPN listen port | 1194 | پورت واقعی دریافت UDP روی Node |
| IP/Domain inside ovpn config | Public Node IP | اگر از تونل ورودی استفاده می‌کنید، آدرس سمت ورود ایران را قرار دهید |
| Port inside ovpn config | 1194 | در صورت استفاده از تونل، پورت سمت ورود ایران |
| OpenVPN protocol | udp | پروتکل کانفیگ |

> نکته: OpenVPN listen port با Port inside ovpn config لزوماً یکسان نیستند؛ اولی پورت واقعی نود و دومی مقصدی است که داخل کانفیگ کلاینت نوشته می‌شود.

پس از نصب در خروجی، **Agent URL، Control URL و Token** نمایش داده می‌شود. Token را منتشر نکنید.

### اتصال نود به مستر

در مرورگر وارد Master شوید: **Nodes → Add Node** و نام، کد کشور، IP نود، **Agent URL** و **Shared Token** را وارد کنید. آدرس Agent معمولاً به شکل `http://NODE_IP:8091` است؛ سرویس Control به‌صورت پیش‌فرض روی `TCP 5024` اجرا می‌شود.

در فایروال سرور Node، پورت‌های Agent و Control را فقط از **IP مستر** مجاز کنید. از بازگذاشتن سرویس‌های HTTP مدیریتی برای همه اینترنت خودداری کنید؛ اگر ارتباط از شبکه عمومی عبور می‌کند TLS یا یک تونل امن در نظر بگیرید.

### مدیریت و عیب‌یابی

~~~bash
sudo surena-node
sudo systemctl is-active openvpn-server@server irangate-node-agent irangate-node-control
sudo systemctl status irangate-node-agent --no-pager
sudo journalctl -u irangate-node-agent -n 50 --no-pager
sudo ss -tulnp
~~~

از `surena-node` می‌توان وضعیت را دید، پورت Agent یا OpenVPN، توکن مشترک یا IP/پورت مقصد داخل کانفیگ کلاینت را تغییر داد و یک کانفیگ آزمایشی ایجاد کرد. **تغییر توکن یا پورت Agent باید در تنظیمات نود داخل Master هم اعمال شود.**

### بکاپ و بازیابی Node

Node دارای Snapshot Export/Restore رمزگذاری‌شده برای هماهنگی با بکاپ Master است. متادیتای بکاپ شامل گواهی‌ها، کانفیگ‌ها و وضعیت بلاک‌کردن کاربران می‌شود، ولی فایل محیطی حاوی Token وارد Snapshot نمی‌شود. عملیات Restore واقعی روی سرویس اثر می‌گذارد؛ قبل از اجرا بکاپ معتبر و پنجره نگهداری داشته باشید.

---

## English — Node Setup

This is the installer for a separate **foreign OpenVPN Node**, not the Master panel. Follow the [Master instructions](../README.md) for the management server.

**Requirements:** a Linux VPS with root, systemd, TUN, Internet access for APT, and authenticated access to the private repository.

### Install from the complete repository

~~~bash
gh auth login -h github.com -p https -w
gh repo clone Net-spoof/IranGate-OV-Reza-dev surena-src -- --branch l2tp-dev
cd surena-src
sudo bash node/install-node.sh
~~~

Do not download and execute `install-node.sh` by itself. It expects the local helper at `scripts/install-vpn-direct-bypass.sh`. This Node setup is **online** and changes OpenVPN/systemd/network settings.

### Interactive settings

| Prompt | Purpose |
| --- | --- |
| Node display name | Friendly name displayed in the Master |
| Location code | Country/location identifier (e.g. DE, TR, FI) |
| Agent API port | TCP listener (8091 default) |
| Shared token | Authentication secret shared with the Master |
| OpenVPN listen port | Actual UDP listener on the Node |
| IP/Domain inside ovpn config | Address embedded into client configs; may be an ingress tunnel endpoint |
| Port inside ovpn config | Port embedded into client configs |
| OpenVPN protocol | Normally UDP |

The installer prints the **Agent URL**, **Control URL** (TCP 5024 default), **token**, and profile endpoint information. Keep the token private.

### Pair with the Master

In the Master web UI go to **Nodes → Add Node** and supply the display name, location, public IP, Agent URL and exact shared token. Restrict Node Agent/Control ports so **only the Master** can reach them. Administrative HTTP must not be freely exposed on the public Internet.

### Operations

~~~bash
sudo surena-node
sudo systemctl is-active openvpn-server@server irangate-node-agent irangate-node-control
sudo journalctl -u irangate-node-agent -n 50 --no-pager
~~~

Menu options include status, Agent port, shared token, advertised config host/port, OpenVPN listener, a test configuration, and node removal. Coordinate Agent port or token changes on both ends.

**Available management aliases:** `surena-node`, `Serena-node`, `serena-node`, and legacy `ovp-node`.

**Support / پشتیبانی:** [Telegram @NET_SPOOF](https://t.me/NET_SPOOF)
