#!/usr/bin/env python3
from pathlib import Path

path = Path('.github/scripts/integrate_delegated_admin.py')
text = path.read_text(encoding='utf-8')
old = '''# Backup v2 must preserve delegated credentials, quotas and accounting snapshots.
for backup in [
    "deploy/master/scripts/irangate-backup-v2-export.py",
    "deploy/master/scripts/irangate-backup-v2-import.py",
]:
    replace_once(
        backup,
        '\"nodes.json\",\\n                \"admin.json\",\\n                \"telegram.json\",',
        '\"nodes.json\",\\n                \"admin.json\",\\n                \"delegated_admins.json\",\\n                \"telegram.json\",',
    )
'''
new = '''# Backup v2 must preserve delegated credentials, quotas and accounting snapshots.
replace_once(
    "deploy/master/scripts/irangate-backup-v2-export.py",
    '\"nodes.json\",\\n                \"admin.json\",\\n                \"telegram.json\",',
    '\"nodes.json\",\\n                \"admin.json\",\\n                \"delegated_admins.json\",\\n                \"telegram.json\",',
)
replace_once(
    "deploy/master/scripts/irangate-backup-v2-import.py",
    '\"nodes.json\",\\n        \"admin.json\",\\n        \"telegram.json\",',
    '\"nodes.json\",\\n        \"admin.json\",\\n        \"delegated_admins.json\",\\n        \"telegram.json\",',
)
'''
if text.count(old) != 1:
    raise SystemExit(f'integration script matcher block count={text.count(old)}')
path.write_text(text.replace(old, new, 1), encoding='utf-8')
print('Fixed delegated integration backup matcher')
