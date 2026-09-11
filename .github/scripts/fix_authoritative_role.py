from pathlib import Path
import re
import subprocess

ROOT = Path('webpanel/frontend')
VERSION = 'delegated-portal-20260911-v3'

sidebar = ROOT / 'js/sidebar.js'
text = sidebar.read_text(encoding='utf-8')

# Replace the localStorage-only delegated route guard with a backend-authoritative role bootstrap.
start = text.index('// Delegated route guard v2')
end = text.index('\n\nfunction createSidebar', start)
bootstrap = r'''// Authoritative auth-role bootstrap v3
(function bootstrapAuthoritativeRole() {
    const token = localStorage.getItem('token');
    const cachedRole = localStorage.getItem('role') || 'root';

    // Hide protected pages briefly while /api/me resolves so a stale cached role
    // cannot render root navigation for a delegated admin.
    if (token) {
        const style = document.createElement('style');
        style.id = 'irangate-auth-guard-style';
        style.textContent = 'html.irangate-auth-resolving body{visibility:hidden!important}';
        document.head.appendChild(style);
        document.documentElement.classList.add('irangate-auth-resolving');
    }

    const allowedDelegatedPages = new Set(['dealer-dashboard.html', 'clients.html', 'usage.html']);

    window.irangateAuthState = (async () => {
        if (!token) {
            document.documentElement.classList.remove('irangate-auth-resolving');
            return { role: cachedRole, redirected: false };
        }

        try {
            const response = await fetch('/api/me', {
                method: 'GET',
                headers: { 'Authorization': `Bearer ${token}` },
                cache: 'no-store'
            });

            if (response.status === 401) {
                localStorage.clear();
                window.location.replace('index.html');
                return { role: null, redirected: true };
            }

            if (!response.ok) {
                throw new Error(`Role check failed with HTTP ${response.status}`);
            }

            const payload = await response.json();
            if (!payload || payload.success !== true || !payload.data || !payload.data.role) {
                throw new Error('Role check returned an invalid payload');
            }

            const role = String(payload.data.role);
            const username = payload.data.username ? String(payload.data.username) : '';
            localStorage.setItem('role', role);
            if (username) localStorage.setItem('username', username);

            const page = (window.location.pathname.split('/').pop() || '').split('?')[0];
            if (role === 'delegated_admin' && !allowedDelegatedPages.has(page)) {
                window.location.replace('dealer-dashboard.html');
                return { role, redirected: true };
            }

            return { role, redirected: false };
        } catch (error) {
            console.error('IranGate authoritative role check failed:', error);
            // Fail closed for a session that is already known to be delegated.
            const page = (window.location.pathname.split('/').pop() || '').split('?')[0];
            if (cachedRole === 'delegated_admin' && !allowedDelegatedPages.has(page)) {
                window.location.replace('dealer-dashboard.html');
                return { role: cachedRole, redirected: true };
            }
            return { role: cachedRole, redirected: false, error: true };
        } finally {
            document.documentElement.classList.remove('irangate-auth-resolving');
        }
    })();
})();'''
text = text[:start] + bootstrap + text[end:]

old_init = '''// Initialize sidebar
function initSidebar(activePage) {
    const sidebarHTML = createSidebar(activePage);
    document.body.insertAdjacentHTML('afterbegin', sidebarHTML);
    
    // Close sidebar when clicking menu item on mobile
    if (window.innerWidth <= 1024) {
        document.querySelectorAll('.menu-item').forEach(item => {
            item.addEventListener('click', closeSidebar);
        });
    }
}
'''
new_init = '''// Initialize sidebar only after the backend-authoritative role has resolved.
async function initSidebar(activePage) {
    const authState = window.irangateAuthState
        ? await window.irangateAuthState
        : { role: localStorage.getItem('role') || 'root', redirected: false };

    if (authState && authState.redirected) return;

    // Remove an already-rendered stale sidebar if a page script called init twice.
    const oldSidebar = document.getElementById('sidebar');
    if (oldSidebar) oldSidebar.remove();
    const oldToggle = document.querySelector('.sidebar-toggle');
    if (oldToggle) oldToggle.remove();
    const oldOverlay = document.querySelector('.sidebar-overlay');
    if (oldOverlay) oldOverlay.remove();

    const sidebarHTML = createSidebar(activePage);
    document.body.insertAdjacentHTML('afterbegin', sidebarHTML);

    // Close sidebar when clicking menu item on mobile
    if (window.innerWidth <= 1024) {
        document.querySelectorAll('.menu-item').forEach(item => {
            item.addEventListener('click', closeSidebar);
        });
    }
}
'''
if text.count(old_init) != 1:
    raise SystemExit(f'initSidebar block count={text.count(old_init)}')
text = text.replace(old_init, new_init, 1)
sidebar.write_text(text, encoding='utf-8')

# Bust sidebar cache on every HTML page so browsers cannot retain the old localStorage-only gate.
for page in ROOT.glob('*.html'):
    html = page.read_text(encoding='utf-8')
    html = re.sub(r'js/sidebar\.js(?:\?v=[^\"\']*)?', f'js/sidebar.js?v={VERSION}', html)
    page.write_text(html, encoding='utf-8')

# Validation.
final = sidebar.read_text(encoding='utf-8')
for token in (
    'Authoritative auth-role bootstrap v3',
    "fetch('/api/me'",
    "localStorage.setItem('role', role)",
    "allowedDelegatedPages.has(page)",
    'async function initSidebar(activePage)',
    'await window.irangateAuthState',
):
    if token not in final:
        raise SystemExit(f'missing token: {token}')

for page in ROOT.glob('*.html'):
    html = page.read_text(encoding='utf-8')
    if 'js/sidebar.js' in html and f'js/sidebar.js?v={VERSION}' not in html:
        raise SystemExit(f'{page}: stale sidebar cache key')

subprocess.run(['node', '--check', str(sidebar)], check=True)
print('Authoritative delegated role gate v3 validated')
