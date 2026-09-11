// Delegated-admin portal navigation.
// Root/admin navigation is intentionally left untouched.
(function () {
    'use strict';

    if (typeof window.createSidebar !== 'function') {
        console.error('IranGate delegated portal: createSidebar is unavailable');
        return;
    }

    const rootCreateSidebar = window.createSidebar;

    window.createSidebar = function createDelegatedAwareSidebar(activePage) {
        const role = localStorage.getItem('role') || 'root';
        if (role !== 'delegated_admin') {
            return rootCreateSidebar(activePage);
        }

        const username = localStorage.getItem('username') || 'Admin';
        return `
            <button class="sidebar-toggle" onclick="toggleSidebar()">
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/>
                </svg>
            </button>
            <div class="sidebar-overlay" onclick="closeSidebar()"></div>
            <aside class="sidebar" id="sidebar">
                <div class="sidebar-header">
                    <div class="sidebar-logo">
                        <div class="sidebar-logo-icon">
                            <img src="logo/logo.ico" alt="IranGate Logo" class="w-10 h-10 rounded-lg">
                        </div>
                        <span class="sidebar-logo-text">IranGate</span>
                    </div>
                    <div class="sidebar-user">
                        <div class="flex items-center gap-2">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/>
                            </svg>
                            <span class="font-medium">${username}</span>
                        </div>
                        <div class="mt-1 text-xs opacity-70">Delegated Admin</div>
                    </div>
                </div>

                <nav class="sidebar-menu">
                    <div class="menu-section">
                        <div class="menu-section-title">My Panel</div>

                        <a href="dealer-dashboard.html"
                           class="menu-item ${activePage === 'delegated-dashboard' ? 'active' : ''}"
                           title="Dashboard">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/>
                            </svg>
                            <span>Dashboard</span>
                        </a>

                        <a href="clients.html"
                           class="menu-item ${activePage === 'clients' ? 'active' : ''}"
                           title="Clients">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/>
                            </svg>
                            <span>Clients</span>
                        </a>

                        <a href="usage.html"
                           class="menu-item ${activePage === 'usage' ? 'active' : ''}"
                           title="Usage">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 19V9m4 10V5m4 14v-7m4 7V3m4 16v-4"/>
                            </svg>
                            <span>Usage</span>
                        </a>
                    </div>
                </nav>

                <div class="sidebar-footer">
                    <button onclick="logout()" class="logout-btn">
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/>
                        </svg>
                        <span>Logout</span>
                    </button>
                </div>
            </aside>`;
    };
})();
