# SURENA — Installation / نصب سورنا

<p align="center"><img src="../webpanel/frontend/logo/surena.webp" width="104" alt="SURENA"></p>

## فارسی — نصب آنلاین

روش مستقیم برای نصب **پنل مرکزی (Master)** روی VPS تازه Ubuntu 22.04/24.04 amd64:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash install.sh
```

نصاب IP/دامنه، پورت OpenVPN، پورت پنل (8080 پیش‌فرض)، پورت سابسکریپشن (8090 پیش‌فرض) و اطلاعات ادمین را می‌پرسد. **رمز قوی و غیرپیش‌فرض انتخاب کنید.** این روش برای نصب تازه است، نه ارتقای مستر فعال.

نصب **Node** روی سرور جداگانه:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

پس از نصب نود، `sudo Serena-node` را برای مدیریت اجرا کنید. Agent URL و توکن ایجادشده را در **Master → Nodes → Add Node** وارد کنید. فقط IP مستر باید به Agent و Control دسترسی داشته باشد.

- [راهنمای کامل نصب و اتصال نود](../node/README.md)
- [نصب آفلاین مستر (جایگزین اختیاری)](OFFLINE-RELEASE.md)
- [ارتقا و مدیریت امن سرورهای موجود](EXISTING-SERVER-OPERATIONS.md)

## English — Online Installation

Install the **Master** on a fresh Ubuntu 22.04/24.04 amd64 VPS:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash install.sh
```

The installer asks for the IP/domain, OpenVPN settings, panel port (8080 default), subscription port (8090 default), and admin credentials. **Choose a strong, non-default password.** This is a fresh install, not a production upgrade.

Install a **Node** on a separate server:

```bash
sudo apt-get update
sudo apt-get install -y git ca-certificates
git clone --branch l2tp-dev --single-branch https://github.com/Net-spoof/SORENA-OVP-PANEL.git
cd SORENA-OVP-PANEL
sudo bash node/install-node.sh
```

Manage it with `sudo Serena-node`. Register the output Agent URL and secret token under **Master → Nodes → Add Node**. Restrict administrative Node ports to the Master IP.

- [Detailed Node setup and pairing](../node/README.md)
- [Optional offline Master installer](OFFLINE-RELEASE.md)
- [Safe operations on existing servers](EXISTING-SERVER-OPERATIONS.md)

**Support / پشتیبانی:** [@NET_SPOOF](https://t.me/NET_SPOOF)
