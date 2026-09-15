#!/usr/bin/env python3
from pathlib import Path

p = Path("deploy/master/scripts/irangate-pg-usage-tick.py")
text = p.read_text(encoding="utf-8")

old = '''ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
processed = 0
'''
new = '''ike_session_v4_migration = not ACCOUNTING_V4_MARKER.exists()
ike_v4_legacy_sources_present = False
processed = 0
'''
if old not in text:
    raise SystemExit("v4 migration variable anchor not found")
text = text.replace(old, new, 1)

old = '''with conn.cursor() as cur:
    cur.execute("DELETE FROM active_sessions WHERE last_seen < now() - interval '90 seconds'")
'''
new = '''with conn.cursor() as cur:
    if ike_session_v4_migration:
        cur.execute("""
            SELECT EXISTS (
                SELECT 1
                FROM traffic_sources
                WHERE source_id LIKE 'ikev2@%'
            )
        """)
        row = cur.fetchone()
        ike_v4_legacy_sources_present = bool(row and row[0])

    cur.execute("DELETE FROM active_sessions WHERE last_seen < now() - interval '90 seconds'")
'''
if old not in text:
    raise SystemExit("cursor migration anchor not found")
text = text.replace(old, new, 1)

old = 'if source == "ikev2" and ike_session_v4_migration:'
new = 'if source == "ikev2" and ike_session_v4_migration and ike_v4_legacy_sources_present:'
if old not in text:
    raise SystemExit("IKEv2 v4 baseline condition not found")
text = text.replace(old, new, 1)

old = 'print("accounting_v4_ikev2_migration_mode =", ike_session_v4_migration)\n'
new = old + 'print("accounting_v4_legacy_ikev2_sources =", ike_v4_legacy_sources_present)\n'
if old not in text:
    raise SystemExit("v4 diagnostics anchor not found")
text = text.replace(old, new, 1)

p.write_text(text, encoding="utf-8")
print("accounting v4 migration guard refined")
