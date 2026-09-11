from pathlib import Path
import re
import subprocess

ROOT = Path('webpanel/frontend')
VERSION = 'delegated-portal-20260911-v2'

# 1) Make sidebar.js itself authoritative for delegated navigation and route gating.
p = ROOT / 'js/sidebar.js'
text = p.read_text(encoding='utf-8')

# Insert an early page guard once, before createSidebar.
guard_marker = '// Delegated route guard v2'
if guard_marker not in text:
    anchor = 'function createSidebar(activePage) {'
    guard = '''// Delegated route guard v2\n(function enforceDelegatedRoute() {\n    const role = localStorage.getItem('role') || 'root';\n    const token = localStorage.getItem('token');\n    if (role !== 'delegated_admin' || !token) return;\n\n    const page = (window.location.pathname.split('/').pop() || '').split('?')[0];\n    const allowed = new Set(['dealer-dashboard.html', 'clients.html', 'usage.html']);\n    if (!allowed.has(page)) {\n        window.location.replace('dealer-dashboard.html');\n    }\n})();\n\n'''
    if anchor not in text:
        raise SystemExit('sidebar createSidebar anchor not found')
    text = text.replace(anchor, guard + anchor, 1)

start_marker = "    if (role === 'delegated_admin') {"
start = text.index(start_marker)
end = text.index("\n\n    return `", start)
new_block = '''    if (role === 'delegated_admin') {
        return `
            <button class="sidebar-toggle" onclick="toggleSidebar()">
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/></svg>
            </button>
            <div class="sidebar-overlay" onclick="closeSidebar()"></div>
            <aside class="sidebar" id="sidebar">
                <div class="sidebar-header">
                    <div class="sidebar-logo">
                        <div class="sidebar-logo-icon"><img src="logo/logo.ico" alt="IranGate Logo" class="w-10 h-10 rounded-lg"></div>
                        <span class="sidebar-logo-text">IranGate</span>
                    </div>
                    <div class="sidebar-user">
                        <div class="flex items-center gap-2">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>
                            <span class="font-medium">${username}</span>
                        </div>
                        <div class="mt-1 text-xs opacity-70">Delegated Admin</div>
                    </div>
                </div>
                <nav class="sidebar-menu">
                    <div class="menu-section">
                        <div class="menu-section-title">My Panel</div>
                        <a href="dealer-dashboard.html" class="menu-item ${activePage === 'delegated-dashboard' ? 'active' : ''}" title="Dashboard">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>
                            <span>Dashboard</span>
                        </a>
                        <a href="clients.html" class="menu-item ${activePage === 'clients' ? 'active' : ''}" title="Clients">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
                            <span>Clients</span>
                        </a>
                        <a href="usage.html" class="menu-item ${activePage === 'usage' ? 'active' : ''}" title="Usage">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 19V9m4 10V5m4 14v-7m4 7V3m4 16v-4"/></svg>
                            <span>Usage</span>
                        </a>
                    </div>
                </nav>
                <div class="sidebar-footer"><button onclick="logout()" class="logout-btn"><svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg><span>Logout</span></button></div>
            </aside>`;
    }'''
text = text[:start] + new_block + text[end:]
p.write_text(text, encoding='utf-8')

# 2) Cache-bust shared auth/sidebar scripts on every top-level HTML page.
# Also tell browsers not to keep HTML documents stale after this release.
for page in ROOT.glob('*.html'):
    html = page.read_text(encoding='utf-8')
    html = re.sub(r'js/sidebar\.js(?:\?v=[^\"\']*)?', f'js/sidebar.js?v={VERSION}', html)
    html = re.sub(r'js/api\.js(?:\?v=[^\"\']*)?', f'js/api.js?v={VERSION}', html)
    if 'http-equiv="Cache-Control"' not in html:
        needle = '<meta name="viewport" content="width=device-width, initial-scale=1.0">'
        if needle in html:
            html = html.replace(needle, needle + '\n    <meta http-equiv="Cache-Control" content="no-cache, no-store, must-revalidate">\n    <meta http-equiv="Pragma" content="no-cache">\n    <meta http-equiv="Expires" content="0">', 1)
    page.write_text(html, encoding='utf-8')

# 3) Keep API-side client route guard as a second frontend safety layer.
api = ROOT / 'js/api.js'
api_text = api.read_text(encoding='utf-8')
if "allowedPages = new Set(['dealer-dashboard.html', 'clients.html', 'usage.html'])" not in api_text:
    raise SystemExit('api delegated allowedPages guard missing')

# Validation.
sidebar = (ROOT / 'js/sidebar.js').read_text(encoding='utf-8')
for token in ('Delegated route guard v2', 'dealer-dashboard.html', 'clients.html', 'usage.html'):
    if token not in sidebar:
        raise SystemExit(f'sidebar token missing: {token}')

for page in ROOT.glob('*.html'):
    html = page.read_text(encoding='utf-8')
    if 'js/sidebar.js' in html and f'js/sidebar.js?v={VERSION}' not in html:
        raise SystemExit(f'{page}: stale sidebar ref')
    if 'js/api.js' in html and f'js/api.js?v={VERSION}' not in html:
        raise SystemExit(f'{page}: stale api ref')

subprocess.run(['node', '--check', str(ROOT / 'js/sidebar.js')], check=True)
subprocess.run(['node', '--check', str(ROOT / 'js/api.js')], check=True)
print('Delegated frontend gate v2 validated')
