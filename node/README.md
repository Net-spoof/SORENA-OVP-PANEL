<div align="center">
  <img src="../webpanel/frontend/logo/surena.webp" alt="SURENA Node" width="115">
  <h1>SURENA Node</h1>
  <p><strong>Remote OpenVPN Node Agent &amp; Control API</strong></p>
  <p><a href="#persian">فارسی</a> · <a href="#english">English</a> · <a href="../README.md">Main README</a></p>
</div>

---

<a id="persian"></a>

## 🇮🇷 راهنمای فارسی نصب Node

Node سرور جداگانه‌ای است که با پنل مرکزی **SURENA Master** ارتباط برقرار می‌کند و برای ارائه سرویس از لوکیشن‌های مختلف استفاده می‌شود. برای نصب Master به [صفحه اصلی پروژه](../README.md) مراجعه کنید.

### پیش‌نیازها

- سرور تازه و مستقل با دسترسی `root` یا `sudo`
- سیستم‌عامل لینوکسی دارای `systemd` و دستگاه `TUN`
- اینترنت برای دریافت کد پروژه و بسته‌های موردنیاز
- پورت قابل دسترس برای OpenVPN؛ پیش‌فرض `1194/UDP`
- ارتباط امن از Master به Agent و Control API

### نصب آنلاین Node

روی **سرور خارجی Node**:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

**مهم:** نصب را از سورس کامل انجام دهید؛ نصاب به فایل‌های کمکی داخل پوشه `scripts/` وابسته است. روی سرور دارای کاربران، PKI یا OpenVPN فعال، بدون برنامه مهاجرت و بکاپ نصب تازه انجام ندهید.

### تنظیمات مرحله نصب

| گزینه | توضیح |
| --- | --- |
| Node display name | نام نمایشی، مانند `Germany 1` |
| Location code | کد کشور، مانند `DE`، `FI`، `TR` |
| Agent API port | درگاه مدیریتی؛ پیش‌فرض `8091/TCP` |
| Shared secret | کلید اختصاصی احراز ارتباط با Master |
| OpenVPN listen port | پورت واقعی شنود نود؛ پیش‌فرض `1194/UDP` |
| IP/Domain inside config | آدرس مقصدی که کاربران در فایل `.ovpn` دریافت می‌کنند |
| Port inside config | پورت مقصد درج‌شده در فایل `.ovpn` |
| OpenVPN protocol | مقدار پروتکل کانفیگ؛ مقدار معمول `udp` |

**توجه:** آدرس و پورت داخل کانفیگ کاربران ممکن است با IP و پورت واقعی نود متفاوت باشد. به‌عنوان مثال، اگر کاربران ابتدا به یک سرور ورودی یا تونل متصل می‌شوند، آدرس درج‌شده در کانفیگ باید مربوط به همان ورودی باشد.

### اتصال Node به Master

پس از پایان نصب، خروجی شامل **Agent URL**، **Control URL**، کلید مشترک و مقصد کانفیگ کاربران است.

1. پنل مرکزی را باز کنید و وارد **Nodes → Add Node** شوید.
2. نام Node، کد کشور و IP واقعی سرور را وارد کنید.
3. آدرس **Agent URL** و کلید مشترک تولیدشده هنگام نصب را ثبت کنید.
4. وضعیت نود و مسیر اتصال کاربران را بررسی کنید.

| سرویس | پورت پیش‌فرض | محدوده دسترسی |
| --- | --- | --- |
| OpenVPN | `1194/UDP` | متناسب با مسیر اتصال کاربران |
| Node Agent | `8091/TCP` | فقط Master |
| Node Control | `5024/TCP` | فقط Master |

پورت‌های مدیریتی را در فایروال به IP مستر محدود کنید. برای ارتباط مدیریتی روی اینترنت عمومی، از تونل امن یا TLS مناسب استفاده کنید.

### مدیریت Node

```bash
sudo Serena-node
```

این منو امکانات زیر را ارائه می‌دهد:

1. نمایش وضعیت سرویس‌ها
2. تغییر پورت Agent
3. تغییر Shared Secret
4. تغییر IP یا دامنه درج‌شده در کانفیگ‌ها
5. تغییر پورت مقصد کانفیگ‌ها
6. تغییر پورت واقعی OpenVPN
7. ساخت کانفیگ آزمایشی
8. حذف Node (عملیات مخرب)

پس از تغییر پورت Agent یا کلید مشترک، مشخصات Node را در پنل Master هم به‌روزرسانی کنید.

### بررسی سرویس و لاگ‌ها

```bash
sudo systemctl status irangate-node-agent irangate-node-control openvpn-server@server --no-pager
sudo journalctl -u irangate-node-agent -n 50 --no-pager
sudo ss -tulnp
```

در صورت عدم اتصال، ابتدا وضعیت سرویس‌ها، فایروال سرور و محدودیت‌های شبکه بین مستر و نود را بررسی کنید.

### پشتیبان‌گیری و تغییرات

بخش Node از Snapshot برای پشتیبان‌گیری و بازیابی داده‌های پشتیبانی‌شده استفاده می‌کند. پیش از Restore، حذف Node یا تغییرات اساسی سرویس‌ها، بکاپ معتبر و برنامه بازیابی تهیه کنید. راهنمای تکمیلی: [عملیات روی سرورهای موجود](../docs/EXISTING-SERVER-OPERATIONS.md).

---

<a id="english"></a>

## 🌐 English Node Setup

A **SURENA Node** is a separate server managed through the central **SURENA Master**. Nodes allow you to extend your network with external VPN service locations. For Master installation, see the [main project README](../README.md).

### Requirements

- A fresh, separate Linux VPS with `root` or `sudo`
- `systemd` and an enabled `TUN` device
- Internet access for source files and package installation
- A reachable OpenVPN listener (default `1194/UDP`)
- A secure, restricted communication path from the Master to Node APIs

### Online Node Installation

Run on the **Node server**:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

Clone the **complete repository**. The Node installer requires helper files under `scripts/`. Do not run a fresh installation over existing VPN certificates, user profiles, or a production OpenVPN server without a migration and backup plan.

### Installation Parameters

| Parameter | Description |
| --- | --- |
| Node display name | Friendly name, e.g., `Germany 1` |
| Location code | Country/location identifier (`DE`, `FI`, `TR`) |
| Agent API port | Management API; default `8091/TCP` |
| Shared secret | Authentication key shared with the Master |
| OpenVPN listen port | Actual Node listener; default `1194/UDP` |
| IP/Domain inside config | Host advertised in generated `.ovpn` profiles |
| Port inside config | Port advertised in generated `.ovpn` profiles |
| OpenVPN protocol | Client profile setting, normally `udp` |

The **advertised client endpoint** and **actual Node listener** may be different. For traffic entering through a tunnel or ingress gateway, use the appropriate ingress host and port in the client profiles.

### Connect the Node to the Master

The installer prints the **Agent URL**, **Control URL**, shared secret, and advertised profile endpoint.

1. Open **Nodes → Add Node** in the Master dashboard.
2. Provide the Node name, location code, and public IP.
3. Enter the **Agent URL** and installation-generated shared secret.
4. Check Node connectivity and verify the client traffic path.

| Service | Default port | Recommended access |
| --- | --- | --- |
| OpenVPN | `1194/UDP` | According to your client ingress design |
| Node Agent | `8091/TCP` | Master only |
| Node Control | `5024/TCP` | Master only |

Restrict administrative API ports to the Master's IP address. Use an appropriate secure tunnel or TLS when administrative traffic crosses public networks.

### Node Management

```bash
sudo Serena-node
```

The interactive menu provides service status, Agent port settings, shared secret changes, advertised host/port configuration, OpenVPN listener updates, test-profile generation, and Node removal. **Node removal is destructive.**

Changes to the Agent port or shared secret must also be reflected in the Master dashboard.

### Diagnostics

```bash
sudo systemctl status irangate-node-agent irangate-node-control openvpn-server@server --no-pager
sudo journalctl -u irangate-node-agent -n 50 --no-pager
sudo ss -tulnp
```

When connectivity fails, check service status, firewall restrictions, and network reachability between the Master and Node.

### Backups and Existing Deployments

Node snapshot workflows support export and restore of relevant service state. Keep verified backups before restoration, removal, or network changes. See the [existing-server operations guide](../docs/EXISTING-SERVER-OPERATIONS.md).

---

<div align="center">
  <a href="../README.md">SURENA Home</a> · <a href="https://t.me/NET_SPOOF">Support @NET_SPOOF</a>
</div>
