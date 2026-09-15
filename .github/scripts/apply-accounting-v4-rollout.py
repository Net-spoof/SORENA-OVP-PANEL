#!/usr/bin/env python3
from pathlib import Path

p = Path("deploy/master/scripts/irangate-pg-usage-tick.py")
text = p.read_text(encoding="utf-8")

old = 'ACCOUNTING_V3_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v3.marker")\n'
new = old + 'ACCOUNTING_V4_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v4.marker")\n'
if old not in text or "ACCOUNTING_V4_MARKER" in text:
    raise SystemExit("marker constant source pattern not found or already patched")
text = text.replace(old, new, 1)

marker_anchor = "def self_test_accounting_identity():\n"
marker_func = '''def write_accounting_v4_marker():
    ACCOUNTING_V4_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V4_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(
        json.dumps(
            {
                "version": 4,
                "activated_at": datetime.now(timezone.utc).isoformat(),
                "policy": "ikev2 stable SA identity rollout baseline",
            },
            ensure_ascii=False,
            indent=2,
        ) + "\\n"
    )
    tmp.replace(ACCOUNTING_V4_MARKER)


'''
if marker_anchor not in text:
    raise SystemExit("marker function anchor not found")
text = text.replace(marker_anchor, marker_func + marker_anchor, 1)

old = '''migration_mode = not ACCOUNTING_V2_MARKER.exists()
local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()
processed = 0
'''
new = '''migration_mode = not ACCOUNTING_V2_MARKER.exists()
local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()
ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
processed = 0
'''
if old not in text:
    raise SystemExit("migration mode source pattern not found")
text = text.replace(old, new, 1)

old = '''            if source == "local" and local_session_v3_migration:
                # v2 stored every simultaneous local session in source_id=local.
                # Baseline each live local session exactly once during v3
                # rollout so already-accounted bytes are not charged again.
                migration_baselines += 1
                action = "FIRST_LOCAL_V3_BASELINE_ONLY"
            elif migration_mode and aggregate_total_before > 0:
'''
new = '''            if source == "ikev2" and ike_session_v4_migration:
                # v3 keyed IKEv2 epochs by connected_since. v4 prefers the
                # stable strongSwan SA identity. Baseline active SAs once so
                # changing key formats cannot charge the same bytes twice.
                migration_baselines += 1
                action = "FIRST_IKEV2_V4_BASELINE_ONLY"
            elif source == "local" and local_session_v3_migration:
                # v2 stored every simultaneous local session in source_id=local.
                # Baseline each live local session exactly once during v3
                # rollout so already-accounted bytes are not charged again.
                migration_baselines += 1
                action = "FIRST_LOCAL_V3_BASELINE_ONLY"
            elif migration_mode and aggregate_total_before > 0:
'''
if old not in text:
    raise SystemExit("first-sample migration source pattern not found")
text = text.replace(old, new, 1)

old = '''    if local_session_v3_migration:
        write_accounting_v3_marker()

    try:
'''
new = '''    if local_session_v3_migration:
        write_accounting_v3_marker()

    if ike_session_v4_migration:
        write_accounting_v4_marker()

    try:
'''
if old not in text:
    raise SystemExit("apply marker source pattern not found")
text = text.replace(old, new, 1)

old = 'print("accounting_v2_migration_mode =", migration_mode)\n'
new = (
    old
    + 'print("accounting_v3_local_migration_mode =", local_session_v3_migration)\n'
    + 'print("accounting_v4_ikev2_migration_mode =", ike_session_v4_migration)\n'
)
if old not in text:
    raise SystemExit("diagnostic print source pattern not found")
text = text.replace(old, new, 1)

p.write_text(text, encoding="utf-8")
print("accounting v4 rollout patch applied")
