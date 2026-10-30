# OpenVPN local accounting v6: migration safety

The panel's `/opt/irangate/traffic/current_stats.json` is exclusively a
**display lifetime-total** JSON list (`schema_version: 6`,
`counter_kind: panel_cumulative_display`). It is NEVER billable.

The status snapshot writes only
`/opt/irangate/traffic/openvpn_session_stats.json`, with a typed v6 envelope:

```json
{"schema_version":6,"counter_kind":"openvpn_session","captured_at":"2026-10-30T12:00:00Z","sessions":[]}
```

The PostgreSQL tick and session-sync code refuse legacy JSON, stale files,
invalid counters and unverified local connection identities. Session rows need
a stable status-file identifier, valid connection start, remote IP, and a
Client ID/Peer ID or virtual IP. Legacy v1 sessions lacking BOTH IDs and a
virtual address are quarantined rather than charged. Review warning logs and
switch those listeners to status-version 2/3 if needed.

## Migration procedure for an existing populated Master

1. Back up PostgreSQL `traffic_totals`, `traffic_sources`,
   `active_sessions`, the markers and both old/new JSON files.
2. Run the repository safe upgrade preflight pinned to the chosen commit.
   It runs producer/consumer self-tests before changing live files.
3. Deploy the code as one maintenance change. Restart the webpanel;
   ensure both local producer and PG tick are installed together.
4. The first **trusted** v6 snapshot baselines every local stream, without
   incrementing or clearing historical `traffic_totals`. The successful
   commit then creates `pg_usage_accounting_v6.marker`.
5. Verify the next tick reports zero duplicate first-sample charges and
   subsequent ticks charge only positive observed byte deltas.
6. If the producer or contract fails, local accounting fails CLOSED; inspect
   `journalctl -u irangate-pg-usage-tick.service`.

A never-seen session and an unexpected counter reset also baseline only.
This intentionally excludes the first partial sample to prevent replaying
historical usage (including on reboot or NAT port rebinding). Do not zero,
import, or overwrite historical PostgreSQL usage during this migration.
The source files already installed on a server must be updated separately:
a GitHub commit alone does not update a running VPN server.
