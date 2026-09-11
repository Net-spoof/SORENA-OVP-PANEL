#!/usr/bin/env python3
from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text(encoding="utf-8")
    count = text.count(old)
    if count != 1:
        raise SystemExit(
            f"{path}: expected exactly one match, found {count}: {old[:120]!r}"
        )
    p.write_text(text.replace(old, new, 1), encoding="utf-8")


# Backend routing/auth integration.
api = "webpanel/backend/api.go"
replace_once(
    api,
    "go StartBlockedClientRemoteSync(60)\n\tgo StartAsyncCleanup()",
    "go StartBlockedClientRemoteSync(60)\n\tgo StartDelegatedAdminMonitor(15)\n\tgo StartAsyncCleanup()",
)
replace_once(
    api,
    'r.HandleFunc("/api/login", loginHandler).Methods("POST")',
    'r.HandleFunc("/api/login", delegatedLoginHandler).Methods("POST")',
)
replace_once(
    api,
    'api.Use(authMiddleware)\n\tapi.HandleFunc("/clients", getClientsWithNodeStatsHandler).Methods("GET")\n\tapi.HandleFunc("/clients", createClientHandler).Methods("POST")',
    '''api.Use(delegatedAuthMiddleware)
	api.HandleFunc("/me", delegatedMeHandler).Methods("GET")
	api.HandleFunc("/delegated-admins", listDelegatedAdminsHandler).Methods("GET")
	api.HandleFunc("/delegated-admins", createDelegatedAdminHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/add-traffic", addDelegatedAdminTrafficHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/add-time", addDelegatedAdminTimeHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/password", resetDelegatedAdminPasswordHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/disable", disableDelegatedAdminHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/enable", enableDelegatedAdminHandler).Methods("POST")
	api.HandleFunc("/clients", delegatedGetClientsHandler).Methods("GET")
	api.HandleFunc("/clients", delegatedCreateClientHandler).Methods("POST")''',
)
replace_once(
    api,
    'api.HandleFunc("/nodes", getNodesHandler).Methods("GET")',
    'api.HandleFunc("/nodes", delegatedNodesHandler).Methods("GET")',
)

# Login role persistence and role-specific landing page.
index = "webpanel/frontend/index.html"
replace_once(
    index,
    "localStorage.setItem('expires_at', data.data.expires_at);\n                    window.location.href = 'dashboard.html';",
    "localStorage.setItem('expires_at', data.data.expires_at);\n                    localStorage.setItem('role', data.data.role || 'root');\n                    window.location.href = data.data.role === 'delegated_admin' ? 'clients.html' : 'dashboard.html';",
)
replace_once(
    index,
    "if (localStorage.getItem('token')) {\n            window.location.href = 'dashboard.html';\n        }",
    "if (localStorage.getItem('token')) {\n            window.location.href = localStorage.getItem('role') === 'delegated_admin' ? 'clients.html' : 'dashboard.html';\n        }",
)

# Frontend hard guard: delegated admins cannot browse other pages even by URL.
apijs = "webpanel/frontend/js/api.js"
replace_once(
    apijs,
    """function checkAuth() {
    const token = localStorage.getItem('token');
    if (!token) {
        window.location.href = 'index.html';
        return false;
    }
    return true;
}""",
    """function checkAuth() {
    const token = localStorage.getItem('token');
    if (!token) {
        window.location.href = 'index.html';
        return false;
    }

    const role = localStorage.getItem('role') || 'root';
    const currentPage = (window.location.pathname.split('/').pop() || '').split('?')[0];
    if (role === 'delegated_admin' && currentPage !== 'clients.html') {
        window.location.replace('clients.html');
        return false;
    }

    return true;
}""",
)

# Sidebar: delegated admins get one menu only; root gets Admins management tab.
sidebar = "webpanel/frontend/js/sidebar.js"
delegated_sidebar = r'''    const role = localStorage.getItem('role') || 'root';

    if (role === 'delegated_admin') {
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
                    <div class="sidebar-user"><div class="flex items-center gap-2"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg><span class="font-medium">${username}</span></div></div>
                </div>
                <nav class="sidebar-menu">
                    <div class="menu-section">
                        <div class="menu-section-title">Clients</div>
                        <a href="clients.html" class="menu-item ${activePage === 'clients' ? 'active' : ''}" title="OpenVPN Clients">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
                            <span>OpenVPN Clients</span>
                        </a>
                    </div>
                </nav>
                <div class="sidebar-footer"><button onclick="logout()" class="logout-btn"><svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg><span>Logout</span></button></div>
            </aside>`;
    }
'''
replace_once(
    sidebar,
    "    const username = localStorage.getItem('username') || 'Admin';\n    \n    return `",
    "    const username = localStorage.getItem('username') || 'Admin';\n" + delegated_sidebar + "\n    return `",
)

clients_anchor = '''                    <a href="clients.html" class="menu-item ${activePage === 'clients' ? 'active' : ''}" title="OpenVPN Clients">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/>
                        </svg>
                        <span>OpenVPN Clients</span>
                    </a>'''
admins_anchor = clients_anchor + '''

                    <a href="admins.html" class="menu-item ${activePage === 'admins' ? 'active' : ''}" title="Admins">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 20h5v-2a4 4 0 00-5.4-3.75M9 20H3v-2a4 4 0 016-3.46M15 7a4 4 0 11-8 0 4 4 0 018 0zm4 3v6m3-3h-6"/>
                        </svg>
                        <span>Admins</span>
                    </a>'''
replace_once(sidebar, clients_anchor, admins_anchor)

# Backup v2 must preserve delegated credentials, quotas and accounting snapshots.
for backup in [
    "deploy/master/scripts/irangate-backup-v2-export.py",
    "deploy/master/scripts/irangate-backup-v2-import.py",
]:
    replace_once(
        backup,
        '"nodes.json",\n                "admin.json",\n                "telegram.json",',
        '"nodes.json",\n                "admin.json",\n                "delegated_admins.json",\n                "telegram.json",',
    )

print("Delegated admin integration patch applied successfully")
