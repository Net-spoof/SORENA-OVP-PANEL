# SURENA ARM64 post-migration repair

Use on an **already installed/restored ARM64 Master** that was upgraded to
`2e8c0e334237297ad6e2c96cfc9acf1881200c1b`. This is **not** an installer
and must **not** run on the old production server or an AMD64 Master.

The command uses the repository's trusted `irangate-pg-usage-tick.py`
identity functions to insert **one-time session-specific local OpenVPN
baselines** in `traffic_sources` without changing `traffic_totals` or the
clients' quota/expiry. It prevents the first 51GB replay observed after moving
the `open` Master, subject to the live snapshot integrity checks.

### Download (public GitHub; no authentication)

```bash
cd /root
curl -fLsS https://raw.githubusercontent.com/Net-spoof/SORENA-OVP-PANEL/l2tp-dev/scripts/repair-surena-master-migration.sh -o surena-repair.sh
curl -fLsS https://raw.githubusercontent.com/Net-spoof/SORENA-OVP-PANEL/l2tp-dev/scripts/repair/surena-migrated-local-baseline.py -o surena-migrated-local-baseline.py
bash -n surena-repair.sh
python3 -m py_compile surena-migrated-local-baseline.py
python3 surena-migrated-local-baseline.py --self-test
bash surena-repair.sh --audit
# Review output before executing:
bash surena-repair.sh --apply
```

The script only proceeds if: the restored users and PostgreSQL totals exist,
the original OpenVPN CA exists, the pinned ARM64 code is installed, panel health
passes, relevant timers are inactive, the PostgreSQL backup is valid,
and the current OpenVPN snapshot is sufficiently fresh/complete.

`--audit` runs read-only database tests. `--apply` takes a full PostgreSQL
dump plus copies of critical configuration/counter files before any writes.
It refreshes the local status snapshot, establishes the session baseline
in one PostgreSQL transaction, compares historical usage totals and CA hashes,
then reruns the usage engine in dry-run mode. If the new dry-run has any
first-sample additions, counter resets, or more than 64 MiB of incremental
usage, the usage timer remains off.

If checks pass, the **usage timer** is enabled. The **metadata-sync timer**
is enabled only if there are no proposed new clients or quota/expiry/connection
changes. The **Enforcer, node sync and IKEv2 stats timers are deliberately held**
pending operator inspection to avoid unexpected client disconnections or
double-billing from stale node snapshots.

**Important:** A baseline deliberately does not bill bytes already present
in the live first sample, since their prior billing cannot be established
after migration. New post-baseline deltas are billed normally. A successful
run does not imply every VPN node is online; test these separately.

Safety dumps and logs are under `/root/surena-repair-YYYYMMDD-HHMMSS/`.
The script does not automatically restore a full PostgreSQL dump after a
partial failure; if that happens, leave timers off and investigate the
logged stage before any rollback.
