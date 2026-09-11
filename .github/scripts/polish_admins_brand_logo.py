from pathlib import Path
import re

ROOT = Path('webpanel/frontend')
SIDEBAR_VERSION = 'panel-ui-20260911-v4'
PORTAL_VERSION = 'delegated-portal-20260911-v3'
ADMINS_UI_VERSION = 'admins-ui-20260911-v1'

# Brand logo in both sidebar renderers.
for path in [ROOT / 'js/sidebar.js', ROOT / 'js/delegated-portal.js']:
    text = path.read_text(encoding='utf-8')
    text = text.replace('logo/logo.ico', 'logo/net-spoof.svg')
    text = text.replace('alt="IranGate Logo"', 'alt="NET SPOOF Logo"')
    path.write_text(text, encoding='utf-8')

# Login branding uses the same uploaded logo.
index = ROOT / 'index.html'
text = index.read_text(encoding='utf-8')
text = text.replace('<title>IranGate - Login</title>', '<title>NET SPOOF - Login</title>')
text = re.sub(
    r'''<div class="inline-block p-3 bg-gradient-to-r from-purple-500 to-indigo-600 rounded-full mb-4">\s*<svg.*?</svg>\s*</div>\s*<h1 class="text-3xl font-bold text-gray-800">IranGate</h1>\s*<p class="text-gray-600 mt-2">OpenVPN Management Panel</p>''',
    '''<div class="inline-flex items-center justify-center rounded-2xl bg-white/95 p-2 shadow-lg mb-4">\n                <img src="logo/net-spoof.svg" alt="NET SPOOF Logo" class="h-20 w-20 rounded-xl object-cover">\n            </div>\n            <h1 class="text-3xl font-bold text-gray-800">NET SPOOF</h1>\n            <p class="text-gray-600 mt-2">OpenVPN Management Panel</p>''',
    text,
    count=1,
    flags=re.S,
)
index.write_text(text, encoding='utf-8')

# Admin page visual polish + fast initial load.
admins_path = ROOT / 'admins.html'
text = admins_path.read_text(encoding='utf-8')
text = text.replace('<title>Admins - IranGate</title>', '<title>Admins - NET SPOOF</title>')
if 'css/admins-ui.css' not in text:
    text = text.replace(
        '<link rel="stylesheet" href="css/theme-overrides.css">',
        '<link rel="stylesheet" href="css/theme-overrides.css">\n    <link rel="stylesheet" href="css/admins-ui.css?v=' + ADMINS_UI_VERSION + '">',
        1,
    )
text = text.replace(
    '<div class="max-w-7xl mx-auto px-4 sm:px-6 py-6 lg:py-8">',
    '<div class="admins-shell max-w-7xl mx-auto px-4 sm:px-6 py-6 lg:py-8">',
    1,
)
text = text.replace(
    '<div class="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4 mb-7">',
    '<div class="admins-hero flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4 mb-7">',
    1,
)
text = text.replace(
    '<h1 class="text-3xl font-bold text-gray-900">Delegated Admins</h1>\n                    <p class="text-gray-500 mt-2">Manage reseller access, traffic pools, validity and owned users from one place.</p>',
    '<h1 class="text-3xl font-bold text-gray-900">Reseller Admins</h1>\n                    <p class="text-gray-500 mt-2">Manage reseller accounts, quotas, validity and owned users from one fast workspace.</p>',
    1,
)
text = text.replace(
    'class="soft-ring rounded-2xl bg-white p-5 shadow-sm"',
    'class="admin-stat-card soft-ring rounded-2xl bg-white p-5 shadow-sm"',
)
text = text.replace(
    '<section class="soft-ring rounded-2xl bg-white shadow-sm overflow-hidden">',
    '<section class="admin-list-card soft-ring rounded-2xl bg-white shadow-sm overflow-hidden">',
    1,
)
text = text.replace(
    '<div class="flex flex-col md:flex-row md:items-center md:justify-between gap-3 border-b border-gray-100 px-5 py-4">',
    '<div class="admin-list-toolbar flex flex-col md:flex-row md:items-center md:justify-between gap-3 border-b border-gray-100 px-5 py-4">',
    1,
)
text = text.replace(
    '<p class="text-xs text-gray-500 mt-1">Click an admin to inspect its users, usage and connection state.</p>',
    '<p class="text-xs text-gray-500 mt-1">The list loads immediately; detailed client data is fetched only when you open an admin.</p>\n                        <span id="adminsUpdated" class="admins-updated mt-2">Loading accounts…</span>',
    1,
)
text = text.replace(
    'let allClients = [];',
    "let allClients = [];\n        let clientsLoaded = false;\n        let clientsLoadingPromise = null;",
    1,
)

load_pattern = re.compile(
    r'''        async function loadAdmins\(force = false\) \{.*?\n        \}\n\n        function renderAdmins\(\) \{''',
    re.S,
)
load_replacement = '''        async function loadAdmins(force = false) {
            const table = document.getElementById('adminsTable');
            table.setAttribute('aria-busy', 'true');
            table.innerHTML = Array.from({length: 3}, () => `<tr><td colspan="6" class="px-5 py-4"><div class="grid grid-cols-6 gap-4 items-center"><div class="admin-skeleton h-5 col-span-1"></div><div class="admin-skeleton h-5"></div><div class="admin-skeleton h-5"></div><div class="admin-skeleton h-5 col-span-2"></div><div class="admin-skeleton h-5"></div></div></td></tr>`).join('');
            try {
                if (force) {
                    if (api.invalidateCache) api.invalidateCache('/clients');
                    allClients = [];
                    clientsLoaded = false;
                    clientsLoadingPromise = null;
                }
                // Fast path: /delegated-admins already includes client_count and quota totals.
                // Do not block the page on the much heavier root /clients endpoint.
                const adminResult = await apiCall('/delegated-admins');
                admins = Array.isArray(adminResult.data) ? adminResult.data : [];
                renderAdmins();
                const updated = document.getElementById('adminsUpdated');
                if (updated) updated.textContent = `Updated ${new Date().toLocaleTimeString()}`;
            } catch (error) {
                table.innerHTML = `<tr><td colspan="6" class="px-5 py-10 text-center text-red-500">${escapeHtml(error.message)}</td></tr>`;
                showToast(error.message, 'error');
            } finally {
                table.removeAttribute('aria-busy');
            }
        }

        function renderAdmins() {'''
text, count = load_pattern.subn(load_replacement, text, count=1)
if count != 1:
    raise SystemExit(f'loadAdmins patch count={count}')

owned_marker = '''        function getOwnedClients(username) {
            const target = String(username || '').toLowerCase();
            return allClients.filter(client => String(client.owner_admin || '').toLowerCase() === target);
        }
'''
if owned_marker not in text:
    raise SystemExit('getOwnedClients marker not found')
ensure_block = '''        async function ensureClientsLoaded() {
            if (clientsLoaded) return allClients;
            if (clientsLoadingPromise) return clientsLoadingPromise;

            clientsLoadingPromise = (async () => {
                const result = await api.getClients();
                if (!result?.success || !Array.isArray(result.data)) {
                    throw new Error(result?.message || 'Failed to load client details');
                }
                allClients = result.data;
                clientsLoaded = true;
                return allClients;
            })();

            try {
                return await clientsLoadingPromise;
            } finally {
                clientsLoadingPromise = null;
            }
        }

'''
text = text.replace(owned_marker, ensure_block + owned_marker, 1)

text = text.replace('        function openAdminDetails(encoded) {', '        async function openAdminDetails(encoded) {', 1)
open_anchor = '''            const admin = admins.find(a => String(a.username).toLowerCase() === username.toLowerCase());
            if (!admin) return;
            const clients = getOwnedClients(username);'''
open_replacement = '''            const admin = admins.find(a => String(a.username).toLowerCase() === username.toLowerCase());
            if (!admin) return;

            const modal = document.getElementById('detailsModal');
            document.getElementById('detailsTitle').textContent = `${username} · Admin Details`;
            document.getElementById('detailsContent').innerHTML = `<div class="py-14 text-center"><div class="mx-auto mb-4 h-10 w-10 animate-spin rounded-full border-4 border-gray-200 border-t-purple-500"></div><div class="font-semibold text-gray-700">Loading client details…</div><div class="mt-1 text-xs text-gray-400">The admin list stays fast; full client data loads only here.</div></div>`;
            modal.classList.remove('hidden');
            modal.classList.add('flex');

            try {
                await ensureClientsLoaded();
            } catch (error) {
                document.getElementById('detailsContent').innerHTML = `<div class="rounded-xl border border-red-200 bg-red-50 p-5 text-red-700">${escapeHtml(error.message)}</div>`;
                return;
            }

            const clients = getOwnedClients(username);'''
if open_anchor not in text:
    raise SystemExit('openAdminDetails anchor not found')
text = text.replace(open_anchor, open_replacement, 1)

admins_path.write_text(text, encoding='utf-8')

# Force new shared assets everywhere they are referenced.
for page in ROOT.glob('*.html'):
    text = page.read_text(encoding='utf-8')
    text = re.sub(r'js/sidebar\.js(?:\?v=[^"\']*)?', f'js/sidebar.js?v={SIDEBAR_VERSION}', text)
    text = re.sub(r'js/delegated-portal\.js(?:\?v=[^"\']*)?', f'js/delegated-portal.js?v={PORTAL_VERSION}', text)
    page.write_text(text, encoding='utf-8')

# Assertions.
sidebar = (ROOT / 'js/sidebar.js').read_text(encoding='utf-8')
portal = (ROOT / 'js/delegated-portal.js').read_text(encoding='utf-8')
admins = admins_path.read_text(encoding='utf-8')
index = (ROOT / 'index.html').read_text(encoding='utf-8')
assert 'logo/net-spoof.svg' in sidebar and 'logo/logo.ico' not in sidebar
assert 'logo/net-spoof.svg' in portal and 'logo/logo.ico' not in portal
assert 'logo/net-spoof.svg' in index and '>NET SPOOF</h1>' in index
assert 'const adminResult = await apiCall(\'/delegated-admins\');' in admins
load_section = admins.split('async function loadAdmins', 1)[1].split('function renderAdmins', 1)[0]
assert 'api.getClients()' not in load_section
assert 'async function ensureClientsLoaded()' in admins
assert 'async function openAdminDetails(encoded)' in admins
assert f'css/admins-ui.css?v={ADMINS_UI_VERSION}' in admins
for page in ROOT.glob('*.html'):
    t = page.read_text(encoding='utf-8')
    if 'js/sidebar.js' in t:
        assert f'js/sidebar.js?v={SIDEBAR_VERSION}' in t, page
    if 'js/delegated-portal.js' in t:
        assert f'js/delegated-portal.js?v={PORTAL_VERSION}' in t, page
print('NET SPOOF logo + fast admins UI patch verified')
