<div align="center">
  <img src="webpanel/frontend/logo/surena.webp" alt="SURENA logo" width="150">

  <h1>SURENA</h1>
  <p><strong>OpenVPN &amp; IKEv2 Network Control Platform</strong></p>
  <p>Centralized VPN management · Multi-location nodes · Traffic control · Subscription management</p>

  <p>
    <img alt="Ubuntu" src="https://img.shields.io/badge/Ubuntu-22.04%20%7C%2024.04-E95420?logo=ubuntu&logoColor=white">
    <img alt="OpenVPN" src="https://img.shields.io/badge/VPN-OpenVPN%20%7C%20IKEv2-2875D0">
    <a href="MIT-LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-2ea44f"></a>
  </p>

  <p>
    <a href="#persian">راهنمای فارسی</a> ·
    <a href="#english">English documentation</a> ·
    <a href="https://t.me/NET_SPOOF">Support / پشتیبانی</a>
  </p>
</div>

---

<a id="persian"></a>

## 🇮🇷 سورنا پنل | راهنمای فارسی

**SURENA** یک پلتفرم تحت وب برای مدیریت متمرکز سرویس‌های VPN، کاربران، نودهای خارجی و مسیرهای خروجی است. مستر، مدیریت کاربران و سرویس‌ها را بر عهده دارد و نودها می‌توانند به‌عنوان سرورهای جداگانه به آن متصل شوند.

### ✨ امکانات اصلی

| قابلیت | توضیحات |
| --- | --- |
| داشبورد مدیریتی | نمایش وضعیت سرویس‌ها، کاربران و اطلاعات مصرف |
| مدیریت کاربران | ساخت و مدیریت کاربران، حجم مصرفی، محدودیت‌ها و وضعیت دسترسی |
| OpenVPN و IKEv2/IPsec | راه‌اندازی و مدیریت سرویس‌های پشتیبانی‌شده |
| مدیریت نودها | افزودن و مدیریت نودهای خارجی از پنل مرکزی |
| لوکیشن‌های چندپورتی | تعریف ورودی‌های OpenVPN با نام و پورت‌های جداگانه |
| Outbound Routing | پیکربندی مسیرهای خروجی با sing-box و حالت‌های Single و Failover |
| لینک سابسکریپشن | دسترسی کاربران به کانفیگ‌های سرویس از طریق لینک اشتراک |
| گزارش و مصرف | مشاهده گزارش‌ها، اتصال‌ها و آمار ترافیک |
| Backup V2 | تهیه و بازیابی نسخه پشتیبان از داده‌ها و تنظیمات پشتیبانی‌شده |
| رابط کاربری | داشبورد وب با قابلیت انتخاب فارسی/انگلیسی و چیدمان راست‌به‌چپ |

> **نکته معماری:** لوکیشن‌های پورت‌دار به‌تنهایی به معنی داشتن IP خروجی متفاوت نیستند. کشور و IP خروجی به سرور و مسیر واقعی ترافیک بستگی دارد. تنظیم مسیرهای خروجی باید به‌صورت جداگانه انجام شود.

### 🖥️ نصب آنلاین پنل مرکزی (Master)

**پیش‌نیازها:** سرور تازه با Ubuntu **22.04 یا 24.04**، معماری **amd64**، دسترسی `root` یا `sudo`، اینترنت، `systemd` و دستگاه `TUN` فعال.

دستورات زیر را در ترمینال **سرور Master** اجرا کنید:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash install.sh
```

نصاب، تنظیمات اصلی را به‌صورت تعاملی دریافت می‌کند:

| تنظیم | مقدار پیش‌فرض / توضیح |
| --- | --- |
| IP یا دامنه سرور | آدرس مورد استفاده در کانفیگ کاربران |
| پروتکل OpenVPN | UDP |
| پورت OpenVPN | `1194` |
| DNS کاربران | قابل انتخاب در نصب |
| پورت پنل وب | `8080/TCP` |
| پورت سابسکریپشن | `8090/TCP` |
| دامنه و مسیر پنل | اختیاری |
| حساب مدیریت | نام کاربری و رمز انتخابی شما |

**پس از نصب:** آدرس دقیق ورود به پنل در خروجی نصاب نمایش داده می‌شود. اگر دامنه، مسیر و پورت پیش‌فرض را تغییر نداده باشید:

```text
http://SERVER_IP:8080/
```

برای باز کردن منوی مدیریت مستر:

```bash
sudo surena-panel
```

از این منو می‌توانید اطلاعات پنل، دامنه، پورت‌ها، تنظیمات مرتبط با سرویس‌ها و بخش‌های مدیریتی را بررسی و مدیریت کنید.

### 🌍 نصب آنلاین نود خارجی (Node)

Node روی **یک سرور جداگانه** نصب می‌شود. دستور زیر را در ترمینال **سرور Node** اجرا کنید:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

نصاب نود اطلاعات زیر را دریافت می‌کند:

| تنظیم | توضیح |
| --- | --- |
| Node Name | نام نمایشی نود در پنل |
| Location Code | کد لوکیشن مانند `DE`، `TR` یا `FI` |
| Agent API Port | پیش‌فرض `8091/TCP` |
| Shared Secret | کلید محرمانه ارتباط نود با مستر |
| OpenVPN Listen Port | پیش‌فرض `1194/UDP` |
| Client Config Host/Port | آدرس و پورتی که در کانفیگ کاربران نوشته می‌شود |

**اتصال نود به مستر**

1. در پایان نصب، مقادیر **Agent URL** و **Shared Secret** را نگه دارید.
2. در پنل مرکزی به **Nodes → Add Node** بروید.
3. نام، کد لوکیشن، IP نود، آدرس Agent و کلید مشترک را ثبت کنید.
4. ارتباط مستر با نود را بررسی کنید.

پورت **Control API** به‌صورت پیش‌فرض `5024/TCP` است. دسترسی به پورت‌های مدیریتی Node را به IP مستر محدود کنید. اگر کانفیگ کاربران از یک تونل ورودی عبور می‌کند، Host/Port درج‌شده در کانفیگ را متناسب با همان ورودی تنظیم کنید.

برای مدیریت نود در ترمینال:

```bash
sudo Serena-node
```

از منوی Node می‌توانید وضعیت سرویس، پورت Agent، کلید ارتباطی، آدرس کانفیگ کاربران، پورت OpenVPN و ساخت کانفیگ آزمایشی را مدیریت کنید.

[راهنمای تخصصی نصب و مدیریت Node](node/README.md)

### 🔎 بررسی سرویس‌ها و عیب‌یابی

روی Master:

```bash
sudo surena-panel
sudo systemctl status irangate-webpanel --no-pager
sudo journalctl -u irangate-webpanel -n 50 --no-pager
```

روی Node:

```bash
sudo Serena-node
sudo systemctl status irangate-node-agent irangate-node-control openvpn-server@server --no-pager
```

نام‌های سرویس در `systemd` شناسه‌های فنی نصب هستند؛ دستورات اصلی مدیریت سورنا همان `surena-panel` و `Serena-node` هستند.

### 🔐 نکات مهم استقرار

- نصاب‌های Master و Node برای **نصب تازه** طراحی شده‌اند؛ برای ارتقای سرور فعال، نصاب کامل را دوباره اجرا نکنید.
- پیش از تغییرات روی سرور عملیاتی، از دیتابیس، کاربران، کانفیگ‌ها و گواهی‌ها نسخه پشتیبان معتبر تهیه کنید.
- یک رمز قوی برای مدیر پنل انتخاب کنید و دسترسی مدیریتی را محدود نگه دارید.
- برای استفاده عمومی از پنل، DNS و HTTPS معتبر را تنظیم کنید؛ HTTP اولیه به معنی فعال‌بودن TLS نیست.
- پورت‌های لازم را بر اساس تنظیمات انتخابی خود در فایروال مجاز کنید؛ Agent و Control نود را برای کل اینترنت باز نگذارید.
- تغییر Shared Secret یا پورت Agent در Node باید در تنظیمات همان نود در Master نیز اعمال شود.

### 📚 مستندات تکمیلی

- [راهنمای نصب آنلاین Master و Node](docs/installation.md)
- [راهنمای کامل Node](node/README.md)
- [راهنمای عملیات و ارتقای سرور موجود](docs/EXISTING-SERVER-OPERATIONS.md)
- [نصب آفلاین Master (روش جایگزین)](docs/OFFLINE-RELEASE.md)

---

<a id="english"></a>

## 🌐 SURENA | English Documentation

**SURENA** is a self-hosted control platform for managing VPN services, user accounts, remote nodes, and outbound traffic policies. A central **Master** handles administration, while independently deployed **Nodes** extend service locations and connectivity.

### ✨ Features

| Feature | Description |
| --- | --- |
| Administration dashboard | View service health, user information, and traffic statistics |
| User management | Create and manage accounts, traffic quotas, limits, and access |
| OpenVPN & IKEv2/IPsec | Provision and manage supported VPN services |
| Remote node management | Connect and administer external servers from the Master |
| Multi-port locations | Configure distinct OpenVPN listeners and location labels |
| Outbound routing | Configure sing-box outbounds using Single and Failover modes |
| Subscription links | Deliver service configuration updates to users |
| Monitoring and logs | Review connections, usage, and operational activity |
| Backup V2 | Export and restore supported configuration and account data |
| Web interface | Persian/English language switching with RTL support |

> **Network design:** A named, port-based location is not necessarily a separate exit IP. The actual egress address depends on your configured routing and infrastructure.

### 🖥️ Online Master Installation

**Requirements:** a **fresh Ubuntu 22.04 or 24.04 (amd64)** server with `root` or `sudo`, Internet connectivity, `systemd`, and an enabled `TUN` device.

Run on the **Master server**:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash install.sh
```

The interactive installer will prompt for:

| Setting | Default / notes |
| --- | --- |
| Server IP or domain | Used when generating client profiles |
| OpenVPN protocol | UDP |
| OpenVPN port | `1194` |
| Client DNS | Selectable during installation |
| Web panel port | `8080/TCP` |
| Subscription port | `8090/TCP` |
| Panel domain and web path | Optional |
| Administrator account | Choose your username and a strong password |

The installer displays the panel URL when setup completes. With the default port and no custom domain/path:

```text
http://SERVER_IP:8080/
```

Open the Master management menu:

```bash
sudo surena-panel
```

Use the management menu for panel information, domains, ports, and supported service settings.

### 🌍 Online Node Installation

Deploy each Node on a **separate server**. Run the following commands on the **Node server**:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

Node installation parameters:

| Setting | Description |
| --- | --- |
| Node Name | Display name shown in the Master |
| Location Code | Location identifier, such as `DE`, `TR`, or `FI` |
| Agent API Port | Default `8091/TCP` |
| Shared Secret | Authentication key shared with the Master |
| OpenVPN Listen Port | Default `1194/UDP` |
| Client Config Host/Port | Address and port advertised in client profiles |

**Pair the Node with the Master**

1. Save the **Agent URL** and **Shared Secret** shown after installation.
2. Open **Nodes → Add Node** in the Master dashboard.
3. Enter the Node name, location code, public IP, Agent URL, and shared key.
4. Verify communication between the Master and Node.

The Control API uses `5024/TCP` by default. Restrict the Node's administrative ports to the Master IP. For deployments using an ingress tunnel, set the advertised client endpoint to the appropriate tunnel address and port.

To manage the Node:

```bash
sudo Serena-node
```

The Node manager provides service status, Agent port and secret configuration, advertised client endpoint settings, OpenVPN listener management, and test-profile creation.

[Detailed Node setup guide](node/README.md)

### 🔎 Service Checks and Troubleshooting

On the Master:

```bash
sudo surena-panel
sudo systemctl status irangate-webpanel --no-pager
sudo journalctl -u irangate-webpanel -n 50 --no-pager
```

On a Node:

```bash
sudo Serena-node
sudo systemctl status irangate-node-agent irangate-node-control openvpn-server@server --no-pager
```

The `systemd` unit identifiers are internal service names. The standard management commands are `surena-panel` for the Master and `Serena-node` for Nodes.

### 🔐 Deployment and Security

- Use the Master and Node installers for **fresh deployments**; do not rerun them as upgrades on production servers.
- Keep verified backups of accounts, certificates, configuration, and databases before changes.
- Set a strong administrator password and restrict access to management endpoints.
- Configure valid DNS and HTTPS for public access; initial HTTP access does not imply TLS is enabled.
- Open only required firewall ports. Limit Node Agent and Control API access to the Master.
- Update the Master pairing configuration whenever a Node's shared secret or Agent port changes.

### 📚 Documentation

- [Online installation reference](docs/installation.md)
- [Detailed Node guide](node/README.md)
- [Existing server operations and safe updates](docs/EXISTING-SERVER-OPERATIONS.md)
- [Offline Master installation (alternative)](docs/OFFLINE-RELEASE.md)

---

<div align="center">
  <p><strong>SURENA — Network Control Platform</strong></p>
  <p><a href="https://t.me/NET_SPOOF">Telegram Support · @NET_SPOOF</a> · <a href="MIT-LICENSE">MIT License</a></p>
</div>
