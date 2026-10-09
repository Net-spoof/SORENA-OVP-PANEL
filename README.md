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

### ۱) نصب Master به‌صورت آفلاین

**پیش‌نیازها:** سرور تازه Ubuntu **22.04 یا 24.04 amd64**، دسترسی root، systemd، فعال‌بودن TUN و فضای کافی. این فایل برای نصب تازه است؛ **روی سرور دارای کاربر، گواهی یا پنل فعال اجرا نکنید**.

مخزن خصوصی است؛ برای دریافت Release، در مرورگر با حساب مجاز وارد GitHub شوید یا روی گوشی/رایانه:

~~~bash
gh auth login -h github.com -p https -w
gh release download --repo Net-spoof/IranGate-OV-Reza-dev \
  --pattern 'SURENA-offline-ubuntu2204-amd64.run*'
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
~~~

برای Ubuntu 24.04، بخش `2204` نام فایل را به `2404` تغییر دهید. **فقط بسته متناسب با سیستم‌عامل را انتخاب کنید.**

از گوشی Termux به سرور:

~~~bash
scp SURENA-offline-ubuntu2204-amd64.run \
    SURENA-offline-ubuntu2204-amd64.run.sha256 \
    root@YOUR_MASTER_IP:/root/
~~~

روی سرور مقصد (حتی اگر اینترنت نداشته باشد):

~~~bash
cd /root
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
sudo bash ./SURENA-offline-ubuntu2204-amd64.run
~~~

نصاب IP عمومی، پروتکل و پورت OpenVPN، پورت پنل، پورت سابسکریپشن و نام ادمین را می‌پرسد؛ پورت اشغال‌شده را بررسی و جایگزین پیشنهاد می‌کند. گواهی‌ها و دیتابیس و سرویس‌های Master، IKEv2، Backup V2 و موتور Routing را راه‌اندازی و بررسی می‌کند.

اطلاعات ورود در یک فایل محرمانه مخصوص root ذخیره می‌شود (رمز را در چت یا لاگ عمومی منتشر نکنید). بعد از نصب:

~~~bash
surena-panel
systemctl is-active irangate-webpanel openvpn@server postgresql nginx strongswan
~~~

اگر **همان نسخه نصاب** وسط کار متوقف شد، پس از رفع علت:

~~~bash
sudo bash ./SURENA-offline-ubuntu2204-amd64.run --resume
~~~

**روی نصب فعال یا نصب قدیمی با نسخه متفاوت، فایل را دوباره اجرا نکنید.**

[📘 راهنمای جامع نصب Master](docs/OFFLINE-RELEASE.md)

### ۲) نصب Foreign Node

نود خارجی با Master فرق دارد. روی **سرور خارجی جداگانه**، با GitHub احرازشده مخزن کامل را بگیرید؛ اجرای فایل تکی `install-node.sh` کافی نیست، چون ابزار VPN Direct Bypass از مسیر `scripts/` نیز نیاز است:

~~~bash
gh auth login -h github.com -p https -w
gh repo clone Net-spoof/IranGate-OV-Reza-dev surena-src -- --branch l2tp-dev
cd surena-src
sudo bash node/install-node.sh
~~~

نصاب Node نام، کد لوکیشن، پورت Agent، توکن مشترک، پورت OpenVPN و IP/پورت نوشته‌شده داخل کانفیگ کاربران را می‌پرسد. خروجی شامل **Agent URL** و Token است.

سپس در Master → **Nodes → Add Node** آدرس Agent، IP، نام، کد کشور و همان Token را ثبت کنید. پورت Agent (پیش‌فرض 8091/TCP) و Control API (5024/TCP) را در فایروال **فقط برای IP مستر** مجاز کنید. توکن را عمومی نکنید. برای مدیریت خود نود:

~~~bash
sudo ovp-node
sudo systemctl status irangate-node-agent irangate-node-control openvpn-server@server --no-pager
~~~

[📘 راهنمای کامل نصب Node](node/README.md)

### ۳) بررسی سلامت و بازیابی

~~~bash
sudo systemctl status irangate-webpanel --no-pager
sudo journalctl -u irangate-webpanel -n 50 --no-pager
sudo surena-panel
~~~

اگر Routing را تنظیم نکرده‌اید، غیرفعال‌بودن موتور مسیریابی طبیعی است. نصب آفلاین Master **به معنی آفلاین‌بودن نصب Node نیست**؛ Node برای نصب بسته‌ها از APT استفاده می‌کند.

---

## English — Installation Guide

**SURENA** is a self-hosted network control panel for OpenVPN, IKEv2/IPsec, users, multi-port virtual locations, external node agents, independent sing-box outbound routing, traffic accounting and Backup V2.

### Install the Master (fully offline on the destination host)

**Requirements:** fresh Ubuntu **22.04 or 24.04 amd64** VPS with root, systemd and TUN. Do not install over an existing deployment, PKI or database.

The repository is private. Authenticate with GitHub CLI on a connected **phone or workstation**, not necessarily on the destination server:

~~~bash
gh auth login -h github.com -p https -w
gh release download -R Net-spoof/IranGate-OV-Reza-dev \
  --pattern 'SURENA-offline-ubuntu2204-amd64.run*'
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
scp SURENA-offline-ubuntu2204-amd64.run \
    SURENA-offline-ubuntu2204-amd64.run.sha256 \
    root@YOUR_MASTER_IP:/root/
~~~

For Ubuntu 24.04, use the `ubuntu2404` files. On the destination server:

~~~bash
cd /root
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
sudo bash ./SURENA-offline-ubuntu2204-amd64.run
~~~

The interactive installer prompts for public IP, OpenVPN protocol/port, panel port, subscription port and admin username. It detects occupied local ports and initializes the Master services, PostgreSQL, IKEv2 and backup/routing tooling.

~~~bash
sudo surena-panel
sudo systemctl is-active irangate-webpanel openvpn@server postgresql nginx strongswan
~~~

For an interrupted run created by this **same installer version only**:

~~~bash
sudo bash ./SURENA-offline-ubuntu2204-amd64.run --resume
~~~

[Complete Master installation details](docs/OFFLINE-RELEASE.md)

### Install a foreign Node

On a separate node server, clone the **whole private repository**, not only the installer script:

~~~bash
gh auth login -h github.com -p https -w
gh repo clone Net-spoof/IranGate-OV-Reza-dev surena-src -- --branch l2tp-dev
cd surena-src
sudo bash node/install-node.sh
~~~

Record the Node Agent URL and secret token shown by the installer. In the Master web UI, open **Nodes → Add Node** and enter the node's name, country code, public IP, Agent URL and shared token. Restrict Agent TCP (8091 by default) and Control TCP 5024 to the Master IP.

~~~bash
sudo ovp-node
sudo systemctl status irangate-node-agent irangate-node-control openvpn-server@server --no-pager
~~~

[Complete Node installation details](node/README.md)

### Security & compatibility

Existing technical service names, APIs, database objects and data directories retain historical identifiers to preserve compatibility. **Never rename them manually on a running deployment.** The removed standalone GOST tunnel-manager is not part of the supported Surena installer. Routing must be deliberately configured and activated. HTTPS, external nodes, DNS and backups require site-specific setup.

---

### Support / پشتیبانی

**Telegram:** [@NET_SPOOF](https://t.me/NET_SPOOF)

**SURENA — Network Control Platform**
