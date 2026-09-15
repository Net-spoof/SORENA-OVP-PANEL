#!/usr/bin/env python3
from pathlib import Path

p = Path("deploy/master/scripts/irangate-pg-usage-tick.py")
text = p.read_text(encoding="utf-8")

old = '''local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()
ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
ike_v4_legacy_sources_present = False
'''
new = '''local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()
local_v3_legacy_source_present = False
ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
ike_v4_legacy_sources_present = False
'''
if old not in text:
    raise SystemExit("v3 migration variable anchor not found")
text = text.replace(old, new, 1)

old = '''with conn.cursor() as cur:
    if ike_session_v4_migration:
'''
new = '''with conn.cursor() as cur:
    if local_session_v3_migration:
        cur.execute("""
            SELECT EXISTS (
                SELECT 1
                FROM traffic_sources
                WHERE source_id = 'local'
            )
        """)
        row = cur.fetchone()
        local_v3_legacy_source_present = bool(row and row[0])

    if ike_session_v4_migration:
'''
if old not in text:
    raise SystemExit("v3 cursor migration anchor not found")
text = text.replace(old, new, 1)

old = 'elif source == "local" and local_session_v3_migration:'
new = 'elif source == "local" and local_session_v3_migration and local_v3_legacy_source_present:'
if old not in text:
    raise SystemExit("local v3 baseline condition not found")
text = text.replace(old, new, 1)

old = 'print("accounting_v3_local_migration_mode =", local_session_v3_migration)\n'
new = old + 'print("accounting_v3_legacy_local_source =", local_v3_legacy_source_present)\n'
if old not in text:
    raise SystemExit("v3 diagnostics anchor not found")
text = text.replace(old, new, 1)

p.write_text(text, encoding="utf-8")
print("accounting v3 migration guard refined")
