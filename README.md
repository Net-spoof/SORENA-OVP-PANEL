<!-- REZA-FORK-QUICKSTART-START -->

# IranGate-OV Reza Fork 🚀

**OpenVPN Gateway Management System with Master/Node Architecture, Web Panel, Traffic Management and Multi-Tunnel GOST UDP Manager**

---

## 🚀 Quick Installation

### Master Server Installation

Run the following commands on the **Master server**:

```bash
git clone -b CyrusTheGreat --single-branch https://github.com/Reza-lgif/IranGate-OV-Reza.git
cd IranGate-OV-Reza
chmod +x install.sh uninstall.sh
sudo ./install.sh
```

After installation, the Master server can be managed using IranGate CLI and Web Panel.

---

## 🌐 GOST UDP Tunnel Manager

IranGate-OV Reza Fork includes a multi-tunnel GOST UDP manager designed for forwarding OpenVPN UDP traffic between an Iran server and one or more foreign OpenVPN servers.

The tunnel manager supports:

- Multiple simultaneous UDP tunnels
- One Iran server connected to multiple foreign servers
- Independent ports for every tunnel
- Tunnel details and status monitoring
- Container health checks
- UDP listener checks
- Firewall packet counters
- Tunnel restart
- Tunnel deletion
- Live traffic monitoring

### Tunnel Manager Command

Run the following command on **both Iran and foreign servers**:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/scripts/irangate-gost-tunnel-manager.sh)
```

The main menu will appear:

```text
============================================================
        IranGate GOST UDP Tunnel Manager
============================================================

1) Create tunnel
2) Tunnels
3) System status
4) Install manager command
0) Exit
```

---

## 🇮🇷 Iran-Side Tunnel Setup

Run the tunnel manager on the Iran server:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/scripts/irangate-gost-tunnel-manager.sh)
```

Then select:

```text
1) Create tunnel
1) Iran server
```

The installer asks for:

```text
Tunnel name
User-facing UDP port
Foreign server IP or hostname
Foreign GOST UDP port
UDP session TTL
```

Example:

```text
Tunnel Name: Germany
User Port: 443
Foreign IP: 91.107.158.214
Foreign GOST Port: 8080
TTL: 300
```

This creates the following path:

```text
OpenVPN Client
       │
       │ UDP 443
       ▼
Iran Server
       │
       │ GOST Native UDP Forward
       ▼
Foreign Server
       │ UDP 8080
       ▼
Foreign-side GOST Tunnel
```

For multiple foreign servers, create additional tunnels using different user-facing ports.

Example:

```text
Germany:
Iran:443/UDP
→ Germany:8080/UDP

Finland:
Iran:444/UDP
→ Finland:8080/UDP

Netherlands:
Iran:445/UDP
→ Netherlands:8080/UDP
```

---

## 🌍 External OpenVPN Node Installation

Run the following commands on each external OpenVPN Node:

```bash
wget -4 -O install-node.sh https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/node/install-node.sh
chmod +x install-node.sh
bash install-node.sh
```

After installation, manage the Node with:

```bash
ovp-node
```

The Node installer asks for:

```text
Node name
Location code
Agent API port
Shared token
OpenVPN listen port
OVPN remote IP/domain
OVPN remote port
```

### Important Tunnel Configuration

When using an Iran tunnel, the generated OpenVPN configuration must point to the **Iran server IP and Iran tunnel port**, not directly to the foreign server.

Example:

```text
Iran Server IP: 79.175.133.27
Iran Tunnel Port: 443
```

The generated client configuration should contain:

```text
proto udp
remote 79.175.133.27 443
```

---

## 🌍 Foreign-Side Tunnel Setup

After installing OpenVPN on the foreign Master or Node server, run:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/scripts/irangate-gost-tunnel-manager.sh)
```

Then select:

```text
1) Create tunnel
2) Foreign server
```

The installer asks for:

```text
Tunnel name
GOST UDP listen port
Local OpenVPN host
Local OpenVPN UDP port
UDP session TTL
Allowed Iran IPv4 address
```

Example:

```text
Tunnel Name: Germany
GOST Port: 8080
Local OpenVPN Host: 127.0.0.1
OpenVPN Port: 443
TTL: 300
Allowed Iran IP: 79.175.133.27
```

The resulting architecture is:

```text
OpenVPN Client
       │
       │ UDP 443
       ▼
79.175.133.27:443
Iran Server
       │
       │ GOST UDP Tunnel
       ▼
91.107.158.214:8080
Foreign Server
       │
       │ Local UDP Forward
       ▼
127.0.0.1:443
       │
       ▼
OpenVPN Server
```

---

## 🔀 Complete Tunnel Example

Example environment:

```text
Iran Server IP:
79.175.133.27

Foreign Server IP:
91.107.158.214

Iran User Port:
443/UDP

Foreign GOST Port:
8080/UDP

Foreign OpenVPN Port:
443/UDP
```

Complete traffic path:

```text
User
  │
  │ OpenVPN UDP
  ▼
79.175.133.27:443
  │
  │ GOST UDP Forward
  ▼
91.107.158.214:8080
  │
  │ GOST Local UDP Forward
  ▼
127.0.0.1:443
  │
  ▼
OpenVPN Server
```

Client configuration:

```text
proto udp
remote 79.175.133.27 443
```

---

## 🛠️ Tunnel Management

Open the manager:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/scripts/irangate-gost-tunnel-manager.sh)
```

Select:

```text
2) Tunnels
```

Available actions:

```text
1) View tunnel details
2) Check tunnel health
3) Restart tunnel
4) Delete tunnel
5) Live traffic monitor
0) Back
```

### View Tunnel Details

Displays:

```text
Tunnel ID
Tunnel name
Server role
Creation time
Container name
Container status
Restart count
UDP TTL
Listen port
Destination IP
Destination port
```

### Check Tunnel Health

The health checker verifies:

```text
Docker container status
UDP listener status
Foreign host resolution
Route availability
ICMP reachability
Local OpenVPN listener
OpenVPN systemd status
iptables packet counters
Recent GOST logs
```

### Live Traffic Monitor

Live traffic monitoring uses `tcpdump` to display packets passing through the selected tunnel.

Press:

```text
Ctrl + C
```

to stop monitoring.

---

## 📦 Install Tunnel Manager as a System Command

From the main tunnel menu, select:

```text
4) Install manager command
```

After installation, the manager can be opened with:

```bash
irangate-gost
```

This avoids downloading the script again for every operation.

---

## 🗑️ Safe Panel Uninstallation

### Preview Removal Operations

Run a dry-run before deleting anything:

```bash
sudo ./uninstall.sh --dry-run
```

### Complete Safe Removal

```bash
sudo ./uninstall.sh --yes
```

The safe uninstaller creates a final **Backup v2** before removal.

It preserves:

```text
Source Repository
External backups stored under /root
Let's Encrypt certificates
PostgreSQL service
Unrelated PostgreSQL databases
Nginx
UFW
Fail2ban
Unrelated Docker containers
```

<!-- REZA-FORK-QUICKSTART-END -->

---

# IranGate-OV 🚀

**Professional OpenVPN Gateway Management System with AI Assistant, Web Panel and Multi-Node Support**

---

## Overview

**IranGate-OV** is an OpenVPN gateway management system providing CLI management, a modern Web Panel, traffic control, client management, monitoring and Master/Node architecture.

This fork additionally includes an integrated multi-tunnel GOST UDP management system for connecting Iran gateways to external OpenVPN servers.

### Project Information

```text
Version: 3.0.0
Platform: Linux
Recommended: Ubuntu 22.04+
Architecture: Master / Node
Tunnel Support: GOST Native UDP Forwarding
```

---

## Key Features

- ✅ CLI-based OpenVPN management
- ✅ Modern Web Panel
- ✅ Master/Node architecture
- ✅ Multiple external OpenVPN nodes
- ✅ GOST UDP tunnel manager
- ✅ Multiple simultaneous Iran-to-Foreign tunnels
- ✅ Client management
- ✅ Traffic quota tracking
- ✅ Real-time traffic monitoring
- ✅ Backup and recovery
- ✅ AI monitoring capabilities
- ✅ Telegram alerts
- ✅ UDP and TCP OpenVPN support
- ✅ systemd service management
- ✅ Docker-based tunnel isolation
- ✅ Tunnel health monitoring

---

## Installation Summary

### Master

```bash
git clone -b CyrusTheGreat --single-branch https://github.com/Reza-lgif/IranGate-OV-Reza.git
cd IranGate-OV-Reza
chmod +x install.sh uninstall.sh
sudo ./install.sh
```

### External Node

```bash
wget -4 -O install-node.sh https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/node/install-node.sh
chmod +x install-node.sh
bash install-node.sh
```

### GOST Tunnel Manager

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/scripts/irangate-gost-tunnel-manager.sh)
```

---

## Project Structure

```text
IranGate-OV-Reza/
├── main.go
├── pkg/
├── webpanel/
├── modules/
├── node/
├── scripts/
│   └── irangate-gost-tunnel-manager.sh
├── clients/
├── database/
├── logs/
├── templates/
├── install.sh
├── uninstall.sh
└── README.md
```

---

## Quick Workflow

### 1. Install Master Server

```text
Install IranGate Master
↓
Configure Web Panel
↓
Configure Iran-side GOST tunnel
```

### 2. Install External Node

```text
Install OpenVPN Node
↓
Configure OpenVPN port
↓
Configure Foreign-side GOST tunnel
```

### 3. Create Client

```text
Create OpenVPN client
↓
Set Iran server IP as remote address
↓
Set Iran GOST user port
↓
Download .ovpn file
↓
Connect
```

---

## Client Configuration Example

For an Iran gateway:

```text
Iran IP: 79.175.133.27
Tunnel Port: 443
Protocol: UDP
```

Client configuration:

```text
proto udp
remote 79.175.133.27 443
```

The user connects to the Iran gateway while the tunnel forwards OpenVPN UDP traffic to the selected foreign OpenVPN server.

---

## Traffic Monitoring

IranGate provides traffic monitoring and tunnel-level diagnostics.

OpenVPN monitoring includes:

```text
Connected clients
Upload traffic
Download traffic
Quota tracking
Connection status
Traffic history
```

GOST Tunnel Manager monitoring includes:

```text
Container state
UDP listener state
Packet counters
Tunnel logs
Route checks
Live packet monitoring
```

---

## Tech Stack

```text
Backend:
Go

Web Panel:
HTML5
CSS3
JavaScript
WebSocket

Database:
JSON
SQLite

Infrastructure:
OpenVPN
Easy-RSA
systemd
iptables
Docker
GOST

Monitoring:
Python
Telegram Bot API
AI Monitoring
```

---

## Contribution

1. Fork the repository.
2. Create a new branch.
3. Make your changes.
4. Commit the changes.
5. Open a Pull Request.

---

## License

MIT License

Copyright (c) 2025 Amirreza Radsar - AmiriDev

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
