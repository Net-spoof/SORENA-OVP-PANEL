# IranGate fully-offline release installer

The downloadable **.run GitHub Release asset** is a self-extracting snapshot of the
entire current IranGate-OV-Reza-dev source plus its compiled Linux executables,
a local Ubuntu APT package repository including dependency closure, sing-box,
and Python AI wheels. The destination machine makes **zero internet requests**
during installation.

## Supported targets

- Fresh **Ubuntu 22.04 LTS (amd64)**: `IranGate-OV-offline-ubuntu2204-amd64.run`
- Fresh **Ubuntu 24.04 LTS (amd64)**: `IranGate-OV-offline-ubuntu2404-amd64.run`
- Root + systemd + working TUN interface + enough disk space
- Choose the package matching the **exact OS release**. The installer aborts on
  a mismatch to avoid mixing incompatible .deb versions.

## Transfer and install (one command)

Download the matching `.run` (and optional `.sha256`) from the GitHub Release
on a networked machine and transfer it by SCP, SFTP, USB or another offline
channel. Then run the single file on the destination:

```bash
sudo bash ./IranGate-OV-offline-ubuntu2404-amd64.run
```

Change `2404` to `2204` for Ubuntu 22.04.

If available, verify file integrity against the **GitHub Release** checksum
before installing:

```bash
sha256sum -c IranGate-OV-offline-ubuntu2404-amd64.run.sha256
```

### Optional IP, credentials and ports

Environment variables are optional. **No interactive prompts are required.**
If you don't provide an administrator password, a unique random password
is generated and securely saved to
`/root/irangate-offline-initial-credentials.txt` (mode 0600).

```bash
sudo IRANGATE_SERVER_IP=192.0.2.25 IRANGATE_PANEL_PORT=8614 \
  IRANGATE_SUBSCRIPTION_PORT=2098 \
  bash ./IranGate-OV-offline-ubuntu2404-amd64.run
```

Do not put passwords directly in shell history on shared machines.
The installer defaults to **HTTP via the IP address** (no Let's Encrypt
enrollment or international DNS required). Terminate TLS at a trusted local
reverse proxy or pre-provision TLS certificates separately.

## What's bundled

- Full repository source files, frontend, tools, master/agent source, templates,
  Backup v2 export/import and systemd units
- Compiled `irangate-webpanel` and `irangate` CLI (Go compiler not required
  on the destination)
- Ubuntu offline APT repository: OpenVPN / EasyRSA, PostgreSQL,
  psycopg2, Nginx, strongSwan (IKEv2), routing and maintenance dependencies
- Bundled official sing-box Linux amd64 executable
- Python dependency wheels for optional AI functionality

The master offline installer initializes OpenVPN PKI and service,
PostgreSQL schema and traffic/enforcement timers, webpanel, subscription proxy,
routing, IKEv2, maintenance services and backup wrappers.

## Important safety/limitations

1. **Fresh installation only.** It refuses to overwrite an existing IranGate
   or OpenVPN PKI. Never run it on a populated production master.
2. No user database, admin credentials, node tokens, certificates or private
   backup archives are embedded in the public release. Restore your own
   Backup v2 archive from a secure location after installation.
3. Foreign nodes, public domains and public TLS certificates still require
   site-specific configuration. A global-trust certificate cannot be obtained
   from Let's Encrypt without reachability.
4. This installer prepares the **master panel**, not an already-joined foreign
   node. Agent node installation remains a separate role and requires a node
   token / endpoint.
5. CI builds for both Ubuntu versions MUST pass the Go build/test and local
   APT dependency simulation before the GitHub Release is published.
6. Installing kernel networking packages may require reboot on some VPS
   configurations, and a TUN-disabled host cannot be fixed by packaging.

## Build and publication

See `.github/workflows/irangate-offline-release.yml`. The GitHub Actions
workflow builds each OS package on an internet-connected hosted runner,
checks completeness of its local dependency repository using APT simulation,
generates release checksums and publishes both `.run` artifacts under
GitHub **Releases** only after both jobs pass.

**Do not claim a working offline Release until a workflow success and the
corresponding release assets have been verified.** Source commits alone are
not a downloadable tested release.
