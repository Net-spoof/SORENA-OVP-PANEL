# IranGate OpenVPN Node Installer

Install this on each external OpenVPN node server.

## Install

```bash
wget -O install-node.sh https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/node/install-node.sh
chmod +x install-node.sh
bash install-node.sh
```

## Manage

After install, run:

```bash
ovp-node
```

Menu options:

1. Status
2. Change Agent port
3. Change shared Token
4. Change IP/Domain inside ovpn config
5. Change Port inside ovpn config
6. Change OpenVPN listen port on node
7. Create test ovpn config
8. Remove node completely

If you use an Iran tunnel, set the ovpn config IP/domain and port to the Iran tunnel IP and tunnel port.
