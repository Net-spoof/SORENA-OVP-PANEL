from pathlib import Path
import re

ROOT = Path('webpanel/frontend')
SIDEBAR = ROOT / 'js/sidebar.js'
VERSION = 'panel-ui-20260911-v2'

text = SIDEBAR.read_text(encoding='utf-8')

old_brand = '<span class="sidebar-logo-text">IranGate</span>'
brand_count = text.count(old_brand)
if brand_count != 2:
    raise SystemExit(f'expected 2 IranGate sidebar brand labels, found {brand_count}')
text = text.replace(old_brand, '<span class="sidebar-logo-text">NET SPOOF</span>')

# Remove the extra NET SPOOF channel badge that was added from the earlier misunderstanding.
pattern = re.compile(
    r'\n\s*<a href="https://t\.me/irangate_official"[^>]*class="sidebar-channel-link"[^>]*>.*?</a>',
    re.S,
)
text, removed = pattern.subn('', text)
if removed != 2:
    raise SystemExit(f'expected 2 extra channel badge blocks, removed {removed}')

SIDEBAR.write_text(text, encoding='utf-8')

changed_pages = 0
for page in ROOT.glob('*.html'):
    source = page.read_text(encoding='utf-8')
    updated = re.sub(
        r'js/sidebar\.js(?:\?v=[^"\']*)?',
        f'js/sidebar.js?v={VERSION}',
        source,
    )
    if updated != source:
        page.write_text(updated, encoding='utf-8')
        changed_pages += 1

if changed_pages == 0:
    raise SystemExit('no sidebar cache references changed')

final = SIDEBAR.read_text(encoding='utf-8')
if final.count('<span class="sidebar-logo-text">NET SPOOF</span>') != 2:
    raise SystemExit('NET SPOOF brand verification failed')
if 'sidebar-channel-link' in final:
    raise SystemExit('extra channel badge still exists')
if '<span class="sidebar-logo-text">IranGate</span>' in final:
    raise SystemExit('old IranGate sidebar brand still exists')

for page in ROOT.glob('*.html'):
    source = page.read_text(encoding='utf-8')
    if 'js/sidebar.js' in source and f'js/sidebar.js?v={VERSION}' not in source:
        raise SystemExit(f'stale sidebar reference: {page}')

print(f'brand fixed; cache-busted {changed_pages} pages')
