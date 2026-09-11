from pathlib import Path
import re

ROOT = Path('webpanel/frontend')
VERSION = 'panel-ui-20260911-v1'
changed = 0

for page in ROOT.glob('*.html'):
    text = page.read_text(encoding='utf-8')
    new = re.sub(r'js/sidebar\.js(?:\?v=[^"\']*)?', f'js/sidebar.js?v={VERSION}', text)
    if new != text:
        page.write_text(new, encoding='utf-8')
        changed += 1

if changed == 0:
    raise SystemExit('no sidebar references changed')

for page in ROOT.glob('*.html'):
    text = page.read_text(encoding='utf-8')
    if 'js/sidebar.js' in text and f'js/sidebar.js?v={VERSION}' not in text:
        raise SystemExit(f'stale sidebar reference: {page}')

print(f'updated {changed} HTML files')
