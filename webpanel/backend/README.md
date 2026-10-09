# SURENA Web Panel Backend

This directory contains the Go API serving the SURENA control panel. For installation and user operations, use [Master setup](../../docs/OFFLINE-RELEASE.md) and [Node setup](../../node/README.md).

- Local health endpoint: `GET /api/health`
- Go binary: `/usr/local/bin/irangate-webpanel` (technical compatibility name)
- Service: `irangate-webpanel.service` (compatibility)
- Runtime static content: `/opt/irangate/webpanel/frontend`
- Login/nav asset: `frontend/logo/surena.webp`

**Do not rename** existing API paths, environment variables or database identifiers while migrating a running server.

Support: [@NET_SPOOF](https://t.me/NET_SPOOF).
