from pathlib import Path

html = Path('webpanel/frontend/clients.html')
css = Path('webpanel/frontend/css/clients-ui.css')
s = html.read_text(encoding='utf-8')


def replace_once(old: str, new: str, label: str) -> None:
    global s
    count = s.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected 1 match, found {count}')
    s = s.replace(old, new, 1)


replace_once(
    'css/clients-ui.css?v=clients-ui-20260911-v1',
    'css/clients-ui.css?v=clients-ui-20260911-v2',
    'cache key',
)
replace_once(
    '<div class="max-w-7xl mx-auto px-4 py-8">',
    '<div class="clients-shell max-w-7xl mx-auto px-4 py-8">',
    'shell',
)
replace_once(
    '<div class="flex justify-between items-center mb-8">',
    '<div class="clients-page-header flex justify-between items-center mb-8">',
    'page header',
)

summary = '''
        <!-- Workspace Summary -->
        <div class="clients-summary-grid" aria-label="Client summary">
            <div class="client-summary-card client-summary-total">
                <div class="client-summary-icon">ALL</div>
                <div><span>Total clients</span><strong id="clientStatTotal">0</strong></div>
            </div>
            <div class="client-summary-card client-summary-online">
                <div class="client-summary-icon">ON</div>
                <div><span>Online now</span><strong id="clientStatOnline">0</strong></div>
            </div>
            <div class="client-summary-card client-summary-ready">
                <div class="client-summary-icon">OK</div>
                <div><span>Ready / offline</span><strong id="clientStatReady">0</strong></div>
            </div>
            <div class="client-summary-card client-summary-ended">
                <div class="client-summary-icon">!</div>
                <div><span>Ended / blocked</span><strong id="clientStatEnded">0</strong></div>
            </div>
        </div>

'''
marker = '        <!-- Search & Filter -->\n'
if s.count(marker) != 1:
    raise SystemExit(f'summary marker count={s.count(marker)}')
s = s.replace(marker, summary + marker, 1)

replace_once(
    '<div class="bg-white rounded-lg shadow p-4 mb-6">',
    '<div class="clients-toolbar bg-white rounded-lg shadow p-4 mb-6">',
    'toolbar',
)
replace_once(
    '<div class="bg-white rounded-lg shadow overflow-hidden">',
    '<div class="clients-table-card bg-white rounded-lg shadow overflow-hidden">',
    'table card',
)
replace_once(
    '<div class="mt-8">\n            <div class="bg-yellow-50 border-l-4 border-yellow-400 p-6 rounded-lg">',
    '<div class="clients-security-card mt-8">\n            <div class="bg-yellow-50 border-l-4 border-yellow-400 p-6 rounded-lg">',
    'security card',
)

expires_th = '''                            <th class="px-6 py-4 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                Expires
                            </th>
'''
plan_th = expires_th + '''                            <th class="px-6 py-4 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                                Plan / Remaining
                            </th>
'''
replace_once(expires_th, plan_th, 'plan column')
replace_once('                                Status\n', '                                Access\n', 'access header')

# Human-readable filter labels while preserving current values and filtering logic.
s = s.replace('<option value="active">Active</option>', '<option value="active">Online</option>')
s = s.replace('<option value="inactive">Inactive</option>', '<option value="inactive">Ready / Offline</option>')
s = s.replace('<option value="deactive">Deactive</option>', '<option value="deactive">Ended / Blocked</option>')

old_status = '''        function getClientStatusClass(client) {
            if (isClientVolumeFinished(client)) return 'bg-red-100 text-red-800';
            return client.active ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-800';
        }

        function getClientStatusDotClass(client) {
            if (isClientVolumeFinished(client)) return 'bg-red-500';
            return client.active ? 'bg-green-400' : 'bg-gray-400';
        }

        function getClientStatusText(client) {
            if (isClientVolumeFinished(client)) return 'Deactive';
            return client.active ? 'Active' : 'Inactive';
        }
'''
new_status = '''        function getClientStatusClass(client) {
            if (isClientDeactiveByStatus(client)) return 'bg-red-100 text-red-800';
            if (client.active === false) return 'bg-gray-100 text-gray-800';
            return isClientOnline(client)
                ? 'bg-green-100 text-green-800'
                : 'bg-blue-100 text-blue-800';
        }

        function getClientStatusDotClass(client) {
            if (isClientDeactiveByStatus(client)) return 'bg-red-500';
            if (client.active === false) return 'bg-gray-400';
            return isClientOnline(client) ? 'bg-green-400' : 'bg-blue-400';
        }

        function getClientStatusText(client) {
            if (isClientDeactiveByStatus(client)) return 'Ended';
            if (client.active === false) return 'Disabled';
            return isClientOnline(client) ? 'Online' : 'Ready';
        }
'''
replace_once(old_status, new_status, 'status helpers')

render_marker = '        function renderClients(list) {\n'
summary_js = '''        function updateClientWorkspaceSummary() {
            const source = Array.isArray(allClients) ? allClients : [];
            let online = 0;
            let ready = 0;
            let ended = 0;

            for (const client of source) {
                const state = getClientFilterStatus(client);
                if (state === 'active') online += 1;
                else if (state === 'deactive') ended += 1;
                else ready += 1;
            }

            const values = {
                clientStatTotal: source.length,
                clientStatOnline: online,
                clientStatReady: ready,
                clientStatEnded: ended
            };

            for (const [id, value] of Object.entries(values)) {
                const node = document.getElementById(id);
                if (node) node.textContent = String(value);
            }

            const roleNode = document.getElementById('clientWorkspaceRole');
            if (roleNode) {
                const delegated = (localStorage.getItem('role') || 'root') === 'delegated_admin';
                roleNode.textContent = delegated ? 'Reseller workspace' : 'Root workspace';
            }
        }

'''
if s.count(render_marker) != 1:
    raise SystemExit(f'render marker count={s.count(render_marker)}')
s = s.replace(render_marker, summary_js + render_marker, 1)
replace_once(
    '''        function renderClients(list) {
            clientsLastRenderedList = Array.isArray(list) ? list : [];
''',
    '''        function renderClients(list) {
            updateClientWorkspaceSummary();
            clientsLastRenderedList = Array.isArray(list) ? list : [];
''',
    'render summary call',
)

replace_once(
    '<p class="text-gray-600 mt-2">Manage your VPN clients</p>',
    '<p class="text-gray-600 mt-2">Manage access, quota, validity and client configs from one workspace.</p>\n                <span id="clientWorkspaceRole" class="client-workspace-role">Workspace</span>',
    'role chip',
)

html.write_text(s, encoding='utf-8')

c = css.read_text(encoding='utf-8')
marker_css = '/* NET SPOOF client workspace v2 */'
if marker_css not in c:
    c += r'''

/* NET SPOOF client workspace v2 */
html, body { min-height: 100%; }

body {
  background: var(--primary-bg, #0a192f) !important;
}

.main-content {
  min-height: 100vh;
  background:
    radial-gradient(circle at 18% 0%, color-mix(in srgb, var(--accent-color, #64ffda) 6%, transparent), transparent 30rem),
    radial-gradient(circle at 96% 10%, color-mix(in srgb, var(--accent-secondary, #00d4ff) 5%, transparent), transparent 32rem),
    var(--primary-bg, #0a192f) !important;
}

.clients-shell {
  width: 100%;
  max-width: 1500px !important;
  padding: 2rem 1.4rem 3rem !important;
}

.clients-page-header {
  gap: 1rem;
  margin-bottom: 1.35rem !important;
  padding: .15rem .1rem;
}

.clients-page-header h1 {
  margin: 0;
  color: var(--text-primary, #e6f1ff) !important;
  letter-spacing: -.035em;
}

.clients-page-header > button {
  min-height: 46px;
  padding-inline: 1.15rem !important;
  border: 1px solid color-mix(in srgb, var(--accent-color, #64ffda) 58%, transparent) !important;
  border-radius: 12px !important;
  box-shadow: 0 10px 28px color-mix(in srgb, var(--accent-secondary, #00d4ff) 12%, transparent);
}

.client-workspace-role {
  display: inline-flex;
  align-items: center;
  margin-top: .65rem;
  padding: .28rem .58rem;
  border: 1px solid color-mix(in srgb, var(--accent-color, #64ffda) 24%, transparent);
  border-radius: 999px;
  color: color-mix(in srgb, var(--accent-color, #64ffda) 78%, var(--text-primary, #e6f1ff));
  background: color-mix(in srgb, var(--accent-color, #64ffda) 6%, transparent);
  font-size: .68rem;
  font-weight: 800;
  letter-spacing: .055em;
  text-transform: uppercase;
}

.clients-summary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: .85rem;
  margin-bottom: 1rem;
}

.client-summary-card {
  position: relative;
  display: flex;
  align-items: center;
  gap: .8rem;
  min-height: 86px;
  padding: 1rem 1.05rem;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--border-color, #1e3a5f) 90%, transparent);
  border-radius: 15px;
  background: color-mix(in srgb, var(--card-bg, #112240) 95%, #020617);
  box-shadow: 0 12px 34px rgba(2,8,23,.18);
}

.client-summary-card::after {
  content: '';
  position: absolute;
  inset: auto -30px -42px auto;
  width: 105px;
  height: 105px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent-color, #64ffda) 5%, transparent);
}

.client-summary-icon {
  position: relative;
  z-index: 1;
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  border-radius: 11px;
  border: 1px solid color-mix(in srgb, var(--accent-color, #64ffda) 22%, transparent);
  background: color-mix(in srgb, var(--accent-color, #64ffda) 8%, transparent);
  color: var(--accent-color, #64ffda);
  font-size: .66rem;
  font-weight: 900;
  letter-spacing: .03em;
}

.client-summary-card > div:last-child {
  position: relative;
  z-index: 1;
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: .24rem;
}

.client-summary-card span {
  color: var(--text-secondary, #8892b0);
  font-size: .72rem;
  font-weight: 700;
}

.client-summary-card strong {
  color: var(--text-primary, #e6f1ff);
  font-size: 1.45rem;
  line-height: 1;
  letter-spacing: -.03em;
}

.client-summary-online strong { color: #34d399; }
.client-summary-ready strong { color: #38bdf8; }
.client-summary-ended strong { color: #fb7185; }

.clients-toolbar {
  margin-bottom: 1rem !important;
  padding: .8rem !important;
  border: 1px solid color-mix(in srgb, var(--border-color, #1e3a5f) 92%, transparent) !important;
  border-radius: 14px !important;
  background: color-mix(in srgb, var(--secondary-bg, #112240) 94%, #020617) !important;
  box-shadow: 0 12px 32px rgba(2,8,23,.16) !important;
}

.clients-toolbar input,
.clients-toolbar select {
  min-height: 44px;
  border-radius: 10px !important;
}

.clients-toolbar input {
  padding-left: 2.7rem !important;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='18' height='18' viewBox='0 0 24 24' fill='none' stroke='%238892b0' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Ccircle cx='11' cy='11' r='8'/%3E%3Cpath d='m21 21-4.3-4.3'/%3E%3C/svg%3E") !important;
  background-repeat: no-repeat !important;
  background-position: .85rem center !important;
}

.clients-toolbar button {
  min-height: 44px;
  padding-inline: 1rem !important;
  border: 1px solid color-mix(in srgb, var(--border-color, #1e3a5f) 95%, transparent) !important;
  border-radius: 10px !important;
  background: color-mix(in srgb, var(--tertiary-bg, #1a2f4f) 72%, transparent) !important;
  color: var(--text-primary, #e6f1ff) !important;
}

.clients-table-card {
  border: 1px solid color-mix(in srgb, var(--border-color, #1e3a5f) 92%, transparent) !important;
  border-radius: 16px !important;
  background: color-mix(in srgb, var(--card-bg, #112240) 96%, #020617) !important;
  box-shadow: 0 18px 48px rgba(2,8,23,.2) !important;
}

.clients-table-card > .overflow-x-auto {
  border-radius: inherit;
}

.clients-table-card table {
  min-width: 1240px;
  border-collapse: separate;
  border-spacing: 0;
}

.clients-table-card thead {
  background: color-mix(in srgb, var(--tertiary-bg, #1a2f4f) 88%, #020617) !important;
}

.clients-table-card th {
  padding: .88rem 1rem !important;
  color: color-mix(in srgb, var(--text-secondary, #8892b0) 92%, white) !important;
  font-size: .67rem !important;
  font-weight: 800 !important;
  letter-spacing: .075em !important;
}

.clients-table-card td {
  padding: .9rem 1rem !important;
  border-top: 1px solid color-mix(in srgb, var(--border-color, #1e3a5f) 54%, transparent);
}

.clients-table-card tbody tr {
  background: transparent !important;
  transition: background-color .14s ease, box-shadow .14s ease;
}

.clients-table-card tbody tr:hover {
  background: color-mix(in srgb, var(--accent-color, #64ffda) 4%, transparent) !important;
  box-shadow: inset 3px 0 0 color-mix(in srgb, var(--accent-color, #64ffda) 72%, transparent);
}

.clients-table-card th:nth-child(1),
.clients-table-card td:nth-child(1) { min-width: 190px; }
.clients-table-card th:nth-child(2),
.clients-table-card td:nth-child(2) { min-width: 150px; }
.clients-table-card th:nth-child(3),
.clients-table-card td:nth-child(3) { min-width: 150px; }
.clients-table-card th:nth-child(4),
.clients-table-card td:nth-child(4) { min-width: 155px; }
.clients-table-card th:nth-child(5),
.clients-table-card td:nth-child(5) { min-width: 145px; }
.clients-table-card th:nth-child(6),
.clients-table-card td:nth-child(6) { min-width: 120px; }
.clients-table-card th:nth-child(7),
.clients-table-card td:nth-child(7) { min-width: 360px; }

.clients-table-card td:nth-child(1) .text-sm.font-medium {
  color: var(--text-primary, #e6f1ff) !important;
  font-weight: 800 !important;
  letter-spacing: -.01em;
}

.clients-table-card td:nth-child(5) span.inline-flex {
  min-width: 84px;
  justify-content: center;
  border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
  padding: .36rem .62rem !important;
}

.clients-table-card td:nth-child(7) > div {
  display: flex !important;
  flex-wrap: wrap !important;
  justify-content: flex-end !important;
  gap: .35rem !important;
}

.clients-table-card td:nth-child(7) button {
  min-height: 31px;
  border: 1px solid color-mix(in srgb, currentColor 28%, transparent);
  border-radius: 8px !important;
  background: color-mix(in srgb, currentColor 4%, transparent);
}

.clients-table-card td:nth-child(7) button:hover {
  transform: translateY(-1px);
  background: color-mix(in srgb, currentColor 9%, transparent) !important;
}

#clientsPagination {
  margin-top: .85rem !important;
  padding-bottom: .3rem;
}

.clients-security-card > div {
  border: 1px solid color-mix(in srgb, #f59e0b 35%, var(--border-color, #1e3a5f)) !important;
  border-left-width: 3px !important;
  border-radius: 14px !important;
  background: color-mix(in srgb, #f59e0b 5%, var(--secondary-bg, #112240)) !important;
  box-shadow: 0 12px 32px rgba(2,8,23,.16);
}

.clients-security-card h3 { color: #fbbf24 !important; }
.clients-security-card p { color: var(--text-secondary, #8892b0) !important; }
.clients-security-card button { border-radius: 10px !important; }

@media (max-width: 1180px) {
  .clients-summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

@media (max-width: 760px) {
  .clients-shell { padding: 1rem .7rem 2rem !important; }
  .clients-page-header {
    align-items: stretch !important;
    flex-direction: column;
  }
  .clients-page-header > button { width: 100%; justify-content: center; }
  .clients-summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .55rem; }
  .client-summary-card { min-height: 74px; padding: .75rem; }
  .client-summary-icon { width: 32px; height: 32px; flex-basis: 32px; }
  .client-summary-card strong { font-size: 1.2rem; }
  .clients-toolbar > div { gap: .6rem !important; }
  .clients-toolbar select,
  .clients-toolbar button { width: 100%; }
  .clients-security-card > div { padding: 1rem !important; }
}

@media (max-width: 420px) {
  .clients-summary-grid { grid-template-columns: 1fr; }
}
'''

css.write_text(c, encoding='utf-8')
