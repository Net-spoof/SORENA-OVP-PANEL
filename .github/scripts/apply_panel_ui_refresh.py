from pathlib import Path
import re

ROOT = Path('webpanel/frontend')
SIDEBAR = ROOT / 'js/sidebar.js'
INDEX = ROOT / 'index.html'
VERSION = 'panel-ui-20260911-v1'
CHANNEL_URL = 'https://t.me/irangate_official'

text = SIDEBAR.read_text(encoding='utf-8')

# Load the shared UI polish stylesheet from the common sidebar bootstrap so every
# authenticated panel page gets the same visual layer without duplicating HTML edits.
marker = '// Shared UI refresh loader v1'
if marker not in text:
    anchor = '// Authoritative auth-role bootstrap v3'
    if anchor not in text:
        raise SystemExit('authoritative role bootstrap anchor not found')
    loader = f'''// Shared UI refresh loader v1\n(function loadSharedUIRefresh() {{\n    if (document.getElementById('irangate-ui-refresh')) return;\n    const link = document.createElement('link');\n    link.id = 'irangate-ui-refresh';\n    link.rel = 'stylesheet';\n    link.href = 'css/ui-refresh.css?v={VERSION}';\n    document.head.appendChild(link);\n}})();\n\n'''
    text = text.replace(anchor, loader + anchor, 1)

# Add the NET SPOOF channel pill to the delegated sidebar header.
delegated_logo = '''                        <span class="sidebar-logo-text">IranGate</span>\n                    </div>\n                    <div class="sidebar-user">'''
if delegated_logo in text:
    delegated_replacement = f'''                        <span class="sidebar-logo-text">IranGate</span>\n                        <a href="{CHANNEL_URL}" target="_blank" rel="noopener noreferrer" class="sidebar-channel-link" title="NET SPOOF">\n                            <svg fill="currentColor" viewBox="0 0 24 24" aria-hidden="true"><path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.028-1.627 4.476-1.635z"/></svg>\n                            <span class="sidebar-channel-label">NET SPOOF</span>\n                        </a>\n                    </div>\n                    <div class="sidebar-user">'''
    text = text.replace(delegated_logo, delegated_replacement, 1)
elif 'class="sidebar-channel-link" title="NET SPOOF"' not in text:
    raise SystemExit('delegated logo anchor not found')

# Upgrade the existing root Telegram icon into the same readable NET SPOOF pill.
root_pattern = re.compile(
    r'<a href="https://t\.me/irangate_official" target="_blank" class="sidebar-telegram-link" title="Join Our Telegram Channel">\s*'
    r'(<svg.*?</svg>)\s*</a>',
    re.S,
)
root_match = root_pattern.search(text)
if root_match:
    svg = root_match.group(1)
    replacement = (
        f'<a href="{CHANNEL_URL}" target="_blank" rel="noopener noreferrer" class="sidebar-channel-link" title="NET SPOOF">\n'
        f'                        {svg}\n'
        '                        <span class="sidebar-channel-label">NET SPOOF</span>\n'
        '                    </a>'
    )
    text = text[:root_match.start()] + replacement + text[root_match.end():]
elif text.count('class="sidebar-channel-link"') < 2:
    raise SystemExit('root Telegram link anchor not found')

# Make the delegated role caption consistent with the refreshed sidebar styling.
text = text.replace(
    '<div class="mt-1 text-xs opacity-70">Delegated Admin</div>',
    '<div class="sidebar-role-label">Reseller Panel</div>',
    1,
)

SIDEBAR.write_text(text, encoding='utf-8')

# Login page does not load sidebar.js, so include the shared stylesheet explicitly.
index = INDEX.read_text(encoding='utf-8')
ui_link = f'    <link rel="stylesheet" href="css/ui-refresh.css?v={VERSION}">'
if 'css/ui-refresh.css' not in index:
    anchor = '    <link rel="stylesheet" href="css/dark-theme.css">'
    if anchor not in index:
        raise SystemExit('login stylesheet anchor not found')
    index = index.replace(anchor, anchor + '\n' + ui_link, 1)
INDEX.write_text(index, encoding='utf-8')

# Validation.
final_sidebar = SIDEBAR.read_text(encoding='utf-8')
assert marker in final_sidebar
assert f'css/ui-refresh.css?v={VERSION}' in final_sidebar
assert final_sidebar.count('NET SPOOF') >= 4
assert final_sidebar.count('class="sidebar-channel-link"') >= 2
assert 'Authoritative auth-role bootstrap v3' in final_sidebar
assert "allowedDelegatedPages = new Set(['dealer-dashboard.html', 'clients.html', 'usage.html'])" in final_sidebar
assert f'css/ui-refresh.css?v={VERSION}' in INDEX.read_text(encoding='utf-8')
print('Panel UI refresh patch validated')
