<div align="center">
  <img src="../webpanel/frontend/logo/surena.webp" width="112" alt="SURENA">
  <h1>SURENA · Master Offline Installer</h1>
  <strong>نصب آفلاین مستر · Complete Master Installation</strong>
  <p><a href="#فارسی--نصب-آفلاین-مستر">فارسی</a> · <a href="#english--fully-offline-master-setup">English</a> · <a href="https://t.me/NET_SPOOF">@NET_SPOOF</a></p>
</div>

---

<div dir="rtl" align="right">

## فارسی — نصب آفلاین مستر

### نیازمندی‌ها و محدوده نصب

- VPS تازه **Ubuntu 22.04 یا 24.04، معماری amd64**
- دسترسی root، systemd، دستگاه TUN و فضای دیسک کافی
- فایل نصب `.run` و فایل متناظر `.sha256`
- نصب فقط روی مستر **تازه** انجام شود. اسکریپت از جایگزینی اطلاعات، PKI یا دیتابیس قبلی خودداری می‌کند.
- بسته **فقط Master** را پوشش می‌دهد؛ نصب Node خارجی دستور جداگانه دارد: [Node README](../node/README.md).

**معماری:** اسکریپت ساخت Release در GitHub Actions با اینترنت اجرا و مخزن محلی APT، Wheelهای Python، باینری پنل، CLI و Sing-box را داخل فایل یک‌تکه قرار می‌دهد. روی VPS مقصد، نصب بسته‌ها از مخزن محلی فایل انجام می‌شود.

### دریافت فایل از Release عمومی

[**دانلود آخرین نسخه آفلاین تأییدشده SURENA**](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest)

| سیستم | فایل نصب | SHA256 |
| --- | --- | --- |
| Ubuntu 22.04 / AMD64 | [دانلود](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2204-amd64.run) | [دریافت هش](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2204-amd64.run.sha256) |
| Ubuntu 24.04 / AMD64 | [دانلود](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2404-amd64.run) | [دریافت هش](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2404-amd64.run.sha256) |

از هر Release فقط فایل نصب و هش متناظر همان نسخه را دانلود کنید. آخرین نسخه تنها پس از ساخته‌شدن هر دو فایل منتشر می‌شود؛ نسخه‌های قدیمی حذف نمی‌شوند.



از [صفحه Releases](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases) یا با GitHub CLI روی گوشی/رایانه متصل به اینترنت دریافت کنید:

<div dir="ltr" align="left">

~~~bash
gh release download -R Net-spoof/SORENA-OVP-PANEL \
  --pattern 'SURENA-offline-ubuntu2204-amd64.run*'
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
~~~

</div>

اگر مقصد Ubuntu 24.04 است، `ubuntu2204` را با `ubuntu2404` عوض کنید.

برای ارسال از Termux:

<div dir="ltr" align="left">

~~~bash
scp SURENA-offline-ubuntu2204-amd64.run \
    SURENA-offline-ubuntu2204-amd64.run.sha256 \
    root@YOUR_MASTER_IP:/root/
~~~

</div>

### نصب با یک دستور روی سرور مقصد

<div dir="ltr" align="left">

~~~bash
cd /root
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
sudo bash ./SURENA-offline-ubuntu2204-amd64.run
~~~

</div>

**در نصب تعاملی** این موارد پرسیده می‌شود: Public IPv4، پروتکل OpenVPN (UDP/TCP)، پورت OpenVPN، پورت پنل، پورت سابسکریپشن و نام کاربری ادمین. نصاب تداخل پورت‌های TCP/UDP با سرویس‌های فعلی را بررسی و پورت آزاد پیشنهاد می‌کند. یک رمز قوی می‌سازد و آن را در فایل root-only ذخیره می‌کند.

**سرویس‌های ایجادشده:** OpenVPN، پنل وب، PostgreSQL و تایمرهای مصرف، StrongSwan/IKEv2، Sing-box/مسیریابی، Backup V2 و خدمات نگهداری. موتور مسیریابی نصب و اعتبارسنجی می‌شود اما بدون تعریف Outbound به‌صورت خودکار ترافیک را تغییر مسیر نمی‌دهد.

### مشاهده نتیجه نصب

<div dir="ltr" align="left">

~~~bash
sudo surena-panel
sudo systemctl is-active irangate-webpanel openvpn@server postgresql nginx strongswan
sudo journalctl -u irangate-webpanel -n 35 --no-pager
~~~

</div>

برای نمونه، اگر پورت انتخابی پنل 18614 باشد:

<div dir="ltr" align="left">

~~~bash
curl --noproxy '*' -fsS http://127.0.0.1:18614/api/health
~~~

</div>

اطلاعات ورود در فایل `/root/irangate-offline-initial-credentials.txt` نگهداری می‌شود. **رمز را در چت نفرستید و فایل را عمومی نکنید.**

### رفع توقف نصب

اگر فقط همین نسخه در اواسط نصب قطع شده است، بعد از شناسایی و رفع خطا:

<div dir="ltr" align="left">

~~~bash
sudo bash ./SURENA-offline-ubuntu2204-amd64.run --resume
~~~

</div>

Checkpointهای نصب در `/var/lib/irangate-offline/` نگهداری می‌شوند. برای **نصب‌های قدیمی یا سرور از پیش نصب‌شده** از این گزینه استفاده نکنید. نصب را با حذف PKI، دیتابیس یا سرویس‌های فعال از ابتدا تکرار نکنید.

### امنیت

- پورت‌های UDP موردنیاز OpenVPN و IKEv2 را متناسب با سرویس واقعی تنظیم کنید؛ پورت پنل را به IPهای مدیریتی محدود کنید.
- استفاده از HTTP در نصب اولیه به معنی HTTPS نیست؛ دامنه و TLS عمومی نیاز به تنظیم مستقل دارند.
- فایل‌های Backup V2، Tokenها و کلیدهای خصوصی را فقط در محل امن و با دسترسی محدود نگه دارید.
- قبل از تغییر نسخه روی محیط عملیاتی، بکاپ و تست Rollback لازم است.

---

</div>

## English — Fully Offline Master Setup

### Scope and prerequisites

The self-extracting `.run` release installs a **fresh** Master on **Ubuntu 22.04/24.04 amd64** with root privileges, systemd, TUN and sufficient disk space. It is **not** an upgrade/restore script for existing installations.

The build pipeline downloads dependencies on an online GitHub Actions runner. The destination server installs from an **embedded file-based APT repository**, prebuilt executables and a bundled wheelhouse.

Foreign nodes use the [separate Node installer](../node/README.md), which is not offline.

### Download from public Releases

[**Verified Latest SURENA offline release**](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest)

- Ubuntu 22.04 / AMD64: [Installer](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2204-amd64.run) · [SHA256](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2204-amd64.run.sha256)
- Ubuntu 24.04 / AMD64: [Installer](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2404-amd64.run) · [SHA256](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases/latest/download/SURENA-offline-ubuntu2404-amd64.run.sha256)

Only promote a release after both installers are built and checked. Older per-commit versions stay in the archive.



~~~bash
gh release download -R Net-spoof/SORENA-OVP-PANEL \
  --pattern 'SURENA-offline-ubuntu2204-amd64.run*'
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256

scp SURENA-offline-ubuntu2204-amd64.run \
    SURENA-offline-ubuntu2204-amd64.run.sha256 \
    root@YOUR_MASTER_IP:/root/
~~~

Replace `ubuntu2204` with `ubuntu2404` for Ubuntu 24.04. These assets are published under the [public Releases page](https://github.com/Net-spoof/SORENA-OVP-PANEL/releases); GitHub authentication is not required to browse or download public assets.

### Install on the fresh Master

~~~bash
cd /root
sha256sum -c SURENA-offline-ubuntu2204-amd64.run.sha256
sudo bash ./SURENA-offline-ubuntu2204-amd64.run
~~~

The installer prompts for public IP, OpenVPN protocol/port, panel port, subscription port and admin username. Occupied ports are checked before service provisioning. It installs OpenVPN, PostgreSQL, panel, IKEv2, backup tooling and sing-box routing. Outbound traffic interception stays disabled until explicitly configured.

### Verify

~~~bash
sudo surena-panel
sudo systemctl is-active irangate-webpanel openvpn@server postgresql nginx strongswan
curl --noproxy '*' -fsS http://127.0.0.1:PANEL_PORT/api/health
~~~

Substitute your actual numeric panel port. Administrator credentials are in `/root/irangate-offline-initial-credentials.txt` (root-only); never share them.

### Resume an interrupted install

~~~bash
sudo bash ./SURENA-offline-ubuntu2204-amd64.run --resume
~~~

**Only** run resume for state created by the same released installer; do not use this as an updater for an existing deployment. Preserve PKI and databases, diagnose the original error and maintain verified backups.

### Limits

The offline installer cannot supply your site's DNS, public TLS, remote Node secrets or historical user state. A successful installer health check does not replace a real client connection test.

---

**Telegram support:** [@NET_SPOOF](https://t.me/NET_SPOOF)
