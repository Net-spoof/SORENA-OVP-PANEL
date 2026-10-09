<div align="center">
  <img src="webpanel/frontend/logo/surena.webp" width="160" alt="SURENA emblem">
  <h1>SURENA</h1>
  <strong>Network Control Platform</strong>
  <p>OpenVPN · IKEv2/IPsec · Multi-location · Outbound Routing · Node Management</p>
  <p>
    <a href="#فارسی--راهنمای-نصب">فارسی</a> ·
    <a href="#english--installation-guide">English</a> ·
    <a href="https://t.me/NET_SPOOF">Telegram Support · @NET_SPOOF</a>
  </p>
</div>

---

## فارسی — راهنمای نصب

**سورنا** پنل مدیریت متمرکز OpenVPN و IKEv2 با قابلیت تعریف کاربران، لوکیشن‌های مجازی روی پورت‌های مستقل، نودهای خارجی، مسیریابی با sing-box، آمار مصرف و Backup V2 است.

### امکانات

| بخش | کاربرد |
| --- | --- |
| Master Panel | مدیریت کاربران، مدیران، سابسکریپشن و آمار |
| OpenVPN / IKEv2 | ساخت و کنترل سرویس‌های VPN |
| Local Port Locations | ساخت چند پورت OpenVPN با نام، پرچم و کانفیگ مستقل |
| Foreign Nodes | اتصال سرورهای خارجی به Master از طریق Node Agent |
| Outbounds | انتخاب مسیر خروجی مستقل برای ورودی‌ها با حالت Single و Failover |
| Backup V2 | خروجی گرفتن و بازیابی تنظیمات و اطلاعات |

> **توجه:** لوکیشن پورت‌دار، VPS خارجی جدا نیست؛ IP سرور ورودی یکسان می‌ماند و کشور خروجی وابسته به Outbound واقعی است. مسیریابی تا زمانی که عمداً تنظیم و فعال نشده باشد غیرفعال می‌ماند.

### ۱) نصب آنلاین Master / پنل مرکزی

**پیش‌نیازها:** سرور **تازه** Ubuntu 22.04 یا 24.04 (amd64)، دسترسی root یا sudo، اینترنت برای دانلود وابستگی‌ها، systemd و TUN فعال. این نصب برای **سرور تازه** است؛ روی مستر دارای کاربران، دیتابیس، گواهی یا VPN فعال اجرا نکنید. برای ارتقای سرور موجود از [راهنمای عملیات امن](docs/EXISTING-SERVER-OPERATIONS.md) استفاده کنید.

روی **سرور Master** اجرا کنید؛ مخزن عمومی است و به GitHub Token یا `gh auth login` نیاز ندارد:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash install.sh
```

نصاب به‌صورت تعاملی IP/دامنه، پروتکل و پورت OpenVPN (پیش‌فرض 1194)، DNS، پورت پنل (پیش‌فرض **8080**)، پورت سابسکریپشن (پیش‌فرض **8090**)، دامنه اختیاری، مسیر وب و اطلاعات ادمین را می‌پرسد. **حتماً رمز ادمین قوی و غیرپیش‌فرض وارد کنید**؛ رمز پیش‌فرضِ نمایش‌داده‌شده در نصاب برای استفاده عمومی امن نیست. HTTPS با دامنه به DNS معتبر و راه‌اندازی صحیح TLS وابسته است.

پس از نصب، آدرس پنل در خروجی نمایش داده می‌شود. اگر دامنه و مسیر وب اختصاصی تعیین نکرده‌اید، آدرس معمول:

```text
http://MASTER_IP:8080/
```

برای مدیریت و بررسی سرویس‌ها:

```bash
sudo surena-panel
sudo systemctl is-active irangate-webpanel openvpn@server postgresql nginx
sudo journalctl -u irangate-webpanel -n 50 --no-pager
```

**توجه:** این دستورها نصب تازه‌اند، نه دستور آپدیت. پورت‌های انتخابی را مطابق فایروال/شبکه خود باز کنید؛ پنل مدیریتی را ترجیحاً پشت HTTPS و کنترل دسترسی قرار دهید.

### ۲) نصب آنلاین Foreign Node / نود خارجی

نود را روی **سرور جداگانه** اجرا کنید، نه روی سرور Master. باید مخزن را **کامل** Clone کنید، زیرا نصاب نود به فایل‌های پوشه `scripts/` هم نیاز دارد.

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

نصاب نام نود، کد کشور، پورت Agent (پیش‌فرض **8091/TCP**)، توکن مشترک، پورت واقعی OpenVPN و IP/پورت درج‌شده در کانفیگ کلاینت را می‌پرسد. آدرس کانفیگ می‌تواند با IP واقعی نود متفاوت باشد، مثلاً زمانی که اتصال از تونل ایران انجام می‌شود.

بعد از نصب، مقدار **Agent URL** و **Token** خروجی را در پنل Master از مسیر **Nodes → Add Node** ثبت کنید. پورت‌های Agent (**8091/TCP** یا مقدار انتخابی) و Control (**5024/TCP**) را فقط برای IP مستر مجاز کنید؛ توکن را عمومی نکنید.

مدیریت نود و بررسی وضعیت:

```bash
sudo Serena-node
sudo systemctl is-active openvpn-server@server irangate-node-agent irangate-node-control
```

دستورهای `surena-node` و `serena-node` نیز پشتیبانی می‌شوند. [📘 راهنمای کامل نصب و اتصال نود](node/README.md).

### ۳) نصب آفلاین Master (اختیاری)

اگر سرور مقصد به اینترنت دسترسی ندارد، نسخه بسته‌بندی‌شده آفلاین Master برای Ubuntu 22.04/24.04 amd64 در [Releases](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases) ارائه می‌شود. دستورهای دانلود، کنترل SHA256، انتقال از گوشی/رایانه و نصب در **[راهنمای کامل نصب آفلاین](docs/OFFLINE-RELEASE.md)** قرار دارند. نصب Node همچنان آنلاین است.

### ۴) بررسی سلامت و ارتقا

```bash
sudo systemctl status irangate-webpanel --no-pager
sudo journalctl -u irangate-webpanel -n 50 --no-pager
```

برای تغییرات روی نصب موجود، به‌جای اجرای مجدد نصاب کامل، از [🛠️ راهنمای ارتقای امن و مدیریت Master/Node](docs/EXISTING-SERVER-OPERATIONS.md) استفاده کنید. مسیریابی Outbound تا زمان تنظیم و فعال‌سازی عمدی اعمال نمی‌شود.

---

## English — Installation Guide

**SURENA** is a self-hosted network control panel for OpenVPN, IKEv2/IPsec, user management, multi-port virtual locations, external Node agents, optional sing-box outbound routing, traffic accounting, and Backup V2.

### 1) Online Master / Panel Installation

**Requirements:** a **fresh** Ubuntu 22.04 or 24.04 (amd64) VPS, root/sudo, Internet access for dependencies, systemd, and an enabled TUN device. **Do not run this fresh installer on an existing deployment** containing users, PKI, databases or active VPN services. Use the [existing-server operations guide](docs/EXISTING-SERVER-OPERATIONS.md) for upgrades.

Run on the **Master VPS**. The repository is public; GitHub authentication is not required:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash install.sh
```

The interactive installer asks for the server IP/domain, OpenVPN protocol and port (**1194** default), client DNS, web panel port (**8080** default), subscription port (**8090** default), optional domain, web path, and admin credentials. **Set a strong, non-default admin password**; the suggested default is unsafe for Internet-facing installations. Domain HTTPS requires working DNS and TLS configuration.

The installer displays the resulting URL; with no custom domain/path, the typical address is `http://MASTER_IP:8080/`.

Check services and open the management menu:

```bash
sudo surena-panel
sudo systemctl is-active irangate-webpanel openvpn@server postgresql nginx
sudo journalctl -u irangate-webpanel -n 50 --no-pager
```

Allow your chosen ports through the firewall. Prefer HTTPS and restricted access for the management interface. These commands are for **fresh installation, not in-place updates**.

### 2) Online Foreign Node Installation

Use a **separate server** for each Node; do not install it over the Master. Clone the **whole repository**, because the Node installer uses helpers from `scripts/`.

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

The wizard prompts for the Node name and country code, Agent API port (**8091/TCP** default), shared secret token, real OpenVPN listener, and the host/port embedded in client profiles. The profile destination can differ from the Node's IP when traffic enters through a tunnel.

After installation, copy the **Agent URL** and **Token** to **Master → Nodes → Add Node**. Restrict the Agent port (**8091/TCP**, or your selection) and Control API (**5024/TCP**) so **only the Master IP** can reach them; never publish the token.

```bash
sudo Serena-node
sudo systemctl is-active openvpn-server@server irangate-node-agent irangate-node-control
```

The `surena-node` and `serena-node` spellings also work. See the [full Node setup and pairing guide](node/README.md).

### 3) Offline Master Installer (optional)

For a destination VPS without Internet access, prebuilt offline Master assets for Ubuntu 22.04/24.04 amd64 can be downloaded from [Releases](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases). See the [offline release guide](docs/OFFLINE-RELEASE.md) for matching OS packages, SHA256 verification, upload and installation. The separate Node installer still requires Internet access.

### 4) Operations and security

For existing servers, follow the [safe Master/Node update guide](docs/EXISTING-SERVER-OPERATIONS.md) rather than rerunning `install.sh`. Some internal service, database and filesystem identifiers retain historical names for backward compatibility; **do not rename them on live deployments**. Outbound routing is not enabled until explicitly configured, and public TLS/DNS and Node secrets require site-specific setup.

---

### Support / پشتیبانی

**Telegram:** [@NET_SPOOF](https://t.me/NET_SPOOF)

**SURENA — Network Control Platform**
