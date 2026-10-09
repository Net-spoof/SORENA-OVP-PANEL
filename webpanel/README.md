# SURENA · Web Panel

<p align="center"><img src="frontend/logo/surena.webp" width="112" alt="SURENA"></p>

SURENA is a bilingual (فارسی/English), responsive network control panel for OpenVPN, IKEv2, user/subscription management, local OpenVPN port locations, external nodes and independent outbound routes.

## Installation

- [Master offline installer (FA/EN)](../docs/OFFLINE-RELEASE.md)
- [External Node installer (FA/EN)](../node/README.md)
- [Project overview (FA/EN)](../README.md)

The web panel is installed with the Master bundle. **Do not separately install an outdated webpanel script over a deployed server.**

## Service and files

~~~bash
sudo systemctl status irangate-webpanel --no-pager
sudo journalctl -u irangate-webpanel -n 60 --no-pager
sudo surena-panel
~~~

The existing service unit, environment path `/etc/irangate/webpanel.env`, static root `/opt/irangate/webpanel/frontend` and API routes retain legacy compatibility identifiers; do not rename them on a live installation.

### UI localization / زبان پنل

Use the built-in language switch to toggle **فارسی (RTL)** and **English (LTR)**. The login page, navigation and dashboard share the SURENA logo at `frontend/logo/surena.webp`.

### Security

Restrict panel access, provision trusted HTTPS separately and never share administrator credentials, node tokens, private keys or backup archives.

**Support / پشتیبانی:** [@NET_SPOOF](https://t.me/NET_SPOOF)
