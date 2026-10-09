# NET SPOOF

**NET SPOOF** is a self-hosted VPN management panel for OpenVPN, IKEv2, multi-port local VPN locations, outbound routing, traffic accounting and automated backups.

The panel includes a web interface, a command-line manager, PostgreSQL accounting, StrongSwan/IKEv2 support, a sing-box routing engine, and client/subscription management.

## Features

- OpenVPN UDP endpoints, local port-based locations, country flags, and independent client configuration downloads.
- IKEv2/IPsec with StrongSwan and client synchronization.
- Per-location outbound routing and single/failover strategies with sing-box.
- Client/traffic/usage administration through a web panel.
- Backup V2 export/import and maintenance timers.
- Local offline installation artifacts for Ubuntu 22.04 and 24.04, amd64.

**Routing note:** Installing the routing engine does not activate traffic interception automatically. Configure a supported outbound and explicitly apply routing in the panel. A displayed country flag represents a location profile, not evidence that traffic exits in that country.

## Offline installation

Download the correct file from [Releases](../../releases/latest), together with its `.sha256` companion.

For Ubuntu 22.04 amd64:

```bash
cd /root
sha256sum -c NET-SPOOF-offline-ubuntu2204-amd64.run.sha256
sudo bash ./NET-SPOOF-offline-ubuntu2204-amd64.run
```

For Ubuntu 24.04 amd64, substitute `ubuntu2404`.

The installer asks for the public server IP, OpenVPN protocol/port, webpanel port, subscription port, and administrator username. It detects locally occupied service ports and proposes alternatives. Administration credentials are generated securely and saved in a root-only local file.

Only on an interrupted installation created by the **same installer release**:

```bash
sudo bash ./NET-SPOOF-offline-ubuntu2204-amd64.run --resume
```

Do not run a fresh installer over an existing deployed server, and do not use `--resume` across unrelated builds.

See [Complete offline installation guide](docs/OFFLINE-RELEASE.md).

## Administration

After installation:

```bash
sudo netspoof-panel
netspoof version
```

The historical CLI executable names are preserved for compatibility with existing integrations.

In the web panel, use **Nodes → Local OpenVPN Port Locations** to create virtual locations and **Outbounds** to choose their exit routes. The interface also supports client/subscription configuration downloads for enabled local locations.

## Documentation

- [Configuration](docs/configuration.md)
- [Installation](docs/installation.md)
- [CLI guide](docs/cli-guide.md)
- [Traffic](docs/TRAFFIC_QUICKSTART.md)
- [Troubleshooting](docs/troubleshooting.md)

## Safety and compatibility

Existing service units, API paths, backup metadata, persistent file paths, and database objects deliberately retain their established compatibility identifiers. Rebranding the visible product does not require destructive database or PKI migration. Do not manually rename systemd units or data directories on a live deployment.

NET SPOOF · VPN Management
