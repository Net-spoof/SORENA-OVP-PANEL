# IranGate OpenVPN Node Installer

Install this script on each external IranGate OpenVPN node.

## Install

    wget -O install-node.sh https://raw.githubusercontent.com/Reza-lgif/IranGate-OV-Reza/CyrusTheGreat/node/install-node.sh
    chmod +x install-node.sh
    bash install-node.sh

The installer configures:

- OpenVPN server
- Node Agent API
- OpenVPN usage API
- Node Control API
- block/unblock enforcement
- immediate session termination through the OpenVPN management interface
- persistent client-connect blocking
- IPv4 forwarding and NAT
- client profile creation and download
- encrypted node snapshot export for Master backups
- PKI locking while clients or snapshots are created
- ovp-node management CLI

## Node Agent API

Authenticated endpoints include:

- /status
- /usage
- /clients
- /openvpn/status
- client profile creation
- client profile download
- `GET /backup/export` using timestamped HMAC authentication

The Agent port is selected during installation.


## Node snapshot export

The Agent can export an encrypted snapshot for the Master backup system.
The snapshot includes OpenVPN PKI material, generated client profiles,
server files, persistent block markers, node deny markers, CCD files, and
the client-connect guard. The node environment file and shared token are
not copied into the snapshot.

`GET /backup/export` requires `X-IranGate-Timestamp` and
`X-IranGate-Signature` headers. The signature is an HMAC-SHA256 derived from
the shared Node token. Snapshot creation and client certificate generation
share `/run/irangate-node-pki.lock` so Easy-RSA state is captured consistently.
The response is encrypted with AES-256-CBC/PBKDF2 and includes SHA256 and
response-HMAC headers for Master-side verification.

## Node Control API

The Node Control service listens on TCP port 5024 by default.

Authenticated endpoints:

- /health
- /block-client?name=CLIENT
- /unblock-client?name=CLIENT

The Control API and Agent API use the same shared Node token.

Blocking a client performs two actions:

1. Creates a persistent block marker under /etc/openvpn/blocked-clients.
2. Terminates the active OpenVPN session through the local management interface.

The OpenVPN client-connect hook prevents blocked clients from reconnecting.

## Manage

Run:

    ovp-node

Menu options:

1. Status
2. Change Agent port
3. Change shared Token
4. Change IP/Domain inside ovpn config
5. Change Port inside ovpn config
6. Change OpenVPN listen port on node
7. Create test ovpn config
8. Remove node completely

When using an Iran-to-external tunnel, set the IP/domain and port written into generated .ovpn profiles to the Iran-side tunnel endpoint.
