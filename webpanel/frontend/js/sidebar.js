// Sidebar Component for IranGate Web Panel

// IRANGATE_SIDEBAR_LAYOUT_CACHE_BUST_V2
(function refreshSidebarLayoutStylesheet() {
    const links = Array.from(document.querySelectorAll('link[rel="stylesheet"]'));
    const existing = links.find(link => {
        try {
            const url = new URL(link.href, window.location.href);
            return /\/css\/sidebar\.css$/.test(url.pathname);
        } catch (_) {
            return false;
        }
    });

    if (existing) {
        const url = new URL(existing.href, window.location.href);
        url.searchParams.set('v', 'sidebar-i18n-20261007-v3');
        existing.href = url.toString();
        return;
    }

    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = 'css/sidebar.css?v=sidebar-i18n-20261007-v3';
    document.head.appendChild(link);
})();

// Shared UI refresh loader v1
(function loadSharedUIRefresh() {
    if (document.getElementById('irangate-ui-refresh')) return;
    const link = document.createElement('link');
    link.id = 'irangate-ui-refresh';
    link.rel = 'stylesheet';
    link.href = 'css/ui-refresh.css?v=panel-ui-20260911-v1';
    document.head.appendChild(link);
})();

// Authoritative auth-role bootstrap v3
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
})();

// IRANGATE_SIDEBAR_I18N_V3
const IRANGATE_SIDEBAR_I18N = Object.freeze({
    en: Object.freeze({
        resellerPanel: 'Reseller Panel', myPanel: 'My Panel', main: 'Main', dashboard: 'Dashboard',
        analytics: 'Analytics', allCoresClients: 'All Cores Clients', clients: 'Clients',
        openvpnClients: 'OpenVPN Clients', admins: 'Admins', configuration: 'Configuration',
        outbounds: 'Outbounds', routingOutbounds: 'Routing / Outbounds', nodes: 'Nodes',
        serverChecker: 'Server Checker', settings: 'Settings', resources: 'Resources',
        apiDocs: 'API Docs', usage: 'Usage', logout: 'Logout',
        switchToPersian: 'فارسی', switchToEnglish: 'English', languageAria: 'Change sidebar language'
    }),
    fa: Object.freeze({
        resellerPanel: 'پنل نمایندگی', myPanel: 'پنل من', main: 'اصلی', dashboard: 'داشبورد',
        analytics: 'آمار و تحلیل', allCoresClients: 'کاربران همه هسته‌ها', clients: 'کاربران',
        openvpnClients: 'کاربران OpenVPN', admins: 'مدیران', configuration: 'پیکربندی',
        outbounds: 'مسیرهای خروجی', routingOutbounds: 'مسیریابی / خروجی‌ها', nodes: 'نودها',
        serverChecker: 'بررسی سرور', settings: 'تنظیمات', resources: 'منابع',
        apiDocs: 'مستندات API', usage: 'مصرف', logout: 'خروج',
        switchToPersian: 'فارسی', switchToEnglish: 'English', languageAria: 'تغییر زبان منو'
    })
});

function getSidebarLanguage() {
    if (window.IranGateI18n) {
        return window.IranGateI18n.getLanguage();
    }
    const saved = localStorage.getItem('irangate_language') || localStorage.getItem('irangate_sidebar_language');
    return saved === 'fa' ? 'fa' : 'en';
}

function persistSidebarLanguage(language) {
    const normalized = language === 'fa' ? 'fa' : 'en';
    if (window.IranGateI18n) {
        return window.IranGateI18n.setLanguage(normalized, { emit: false });
    }
    localStorage.setItem('irangate_language', normalized);
    localStorage.setItem('irangate_sidebar_language', normalized);
    return normalized;
}

function applySidebarLanguageState(language = getSidebarLanguage()) {
    const normalized = language === 'fa' ? 'fa' : 'en';
    const root = document.documentElement;
    root.lang = normalized === 'fa' ? 'fa' : 'en';
    root.dir = normalized === 'fa' ? 'rtl' : 'ltr';
    root.dataset.irangateLang = normalized;
    root.classList.toggle('irangate-menu-rtl', normalized === 'fa');
    return normalized;
}

// Apply the saved direction before the sidebar is rendered to avoid a left/right flash.
applySidebarLanguageState();

function sidebarTr(key, language = getSidebarLanguage()) {
    const dictionary = IRANGATE_SIDEBAR_I18N[language] || IRANGATE_SIDEBAR_I18N.en;
    return dictionary[key] || IRANGATE_SIDEBAR_I18N.en[key] || key;
}

function createSidebarLanguageButton(language) {
    const isPersian = language === 'fa';
    const label = isPersian ? sidebarTr('switchToEnglish', language) : sidebarTr('switchToPersian', language);
    const shortLabel = isPersian ? 'EN' : 'فا';
    return `
        <button type="button" class="sidebar-language-btn" onclick="toggleSidebarLanguage(event)"
            title="${label}" aria-label="${sidebarTr('languageAria', language)}">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true">
                <circle cx="12" cy="12" r="9" stroke-width="1.8"></circle>
                <path d="M3 12h18M12 3c2.7 2.5 4 5.5 4 9s-1.3 6.5-4 9c-2.7-2.5-4-5.5-4-9s1.3-6.5 4-9Z" stroke-width="1.8"></path>
            </svg>
            <span>${shortLabel}</span>
        </button>
    `;
}

async function toggleSidebarLanguage(event) {
    if (event) { event.preventDefault(); event.stopPropagation(); }

    const next = persistSidebarLanguage(getSidebarLanguage() === 'fa' ? 'en' : 'fa');
    applySidebarLanguageState(next);

    const currentSidebar = document.getElementById('sidebar');
    const wasOpen = Boolean(currentSidebar && currentSidebar.classList.contains('open'));
    const activePage = window.irangateSidebarActivePage ||
        (currentSidebar && currentSidebar.dataset.activePage) ||
        'dashboard';

    await initSidebar(activePage);

    if (wasOpen && window.innerWidth <= 1024) {
        const sidebar = document.getElementById('sidebar');
        const overlay = document.querySelector('.sidebar-overlay');
        if (sidebar) sidebar.classList.add('open');
        if (overlay) overlay.classList.add('active');
    }

    if (window.IranGateI18n) {
        window.IranGateI18n.translateTree(document.body, next);
    }
    window.dispatchEvent(new CustomEvent('irangate:language-changed', {
        detail: { language: next }
    }));
}

window.getIranGateLanguage = getSidebarLanguage;
window.setIranGateLanguage = async function setIranGateLanguage(language) {
    const normalized = persistSidebarLanguage(language);
    applySidebarLanguageState(normalized);
    const currentSidebar = document.getElementById('sidebar');
    const activePage = window.irangateSidebarActivePage ||
        (currentSidebar && currentSidebar.dataset.activePage) ||
        'dashboard';
    await initSidebar(activePage);
    if (window.IranGateI18n) {
        window.IranGateI18n.translateTree(document.body, normalized);
    }
    return normalized;
};


function createSidebar(activePage) {
    const username = localStorage.getItem('username') || 'Admin';
    const role = localStorage.getItem('role') || 'root';
    const language = getSidebarLanguage();
    const direction = language === 'fa' ? 'rtl' : 'ltr';
    const t = (key) => sidebarTr(key, language);
    const languageButton = createSidebarLanguageButton(language);

    if (role === 'delegated_admin') {
        return `
            <button class="sidebar-toggle" onclick="toggleSidebar()">
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/></svg>
            </button>
            <div class="sidebar-overlay" onclick="closeSidebar()"></div>
            <aside class="sidebar" id="sidebar" data-lang="${language}" data-active-page="${activePage}" dir="${direction}">
                <div class="sidebar-header">
                    <div class="sidebar-logo">
                        <div class="sidebar-logo-icon"><img src="logo/net-spoof.svg" alt="NET SPOOF Logo" class="w-10 h-10 rounded-lg"></div>
                        <span class="sidebar-logo-text">NET SPOOF</span>
                        ${languageButton}
                    </div>
                    <div class="sidebar-user">
                        <div class="flex items-center gap-2">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>
                            <span class="font-medium">${username}</span>
                        </div>
                        <div class="sidebar-role-label">${t('resellerPanel')}</div>
                    </div>
                </div>
                <nav class="sidebar-menu">
                    <div class="menu-section">
                        <div class="menu-section-title">${t('myPanel')}</div>
                        <a href="dealer-dashboard.html" class="menu-item ${activePage === 'delegated-dashboard' ? 'active' : ''}" title="${t('dashboard')}">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>
                            <span>${t('dashboard')}</span>
                        </a>
                        <a href="clients.html" class="menu-item ${activePage === 'clients' ? 'active' : ''}" title="${t('clients')}">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
                            <span>${t('clients')}</span>
                        </a>
                        <a href="usage.html" class="menu-item ${activePage === 'usage' ? 'active' : ''}" title="${t('usage')}">
                            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 19V9m4 10V5m4 14v-7m4 7V3m4 16v-4"/></svg>
                            <span>${t('usage')}</span>
                        </a>
                    </div>
                </nav>
                <div class="sidebar-footer"><button onclick="logout()" class="logout-btn"><svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg><span>${t('logout')}</span></button></div>
            </aside>`;
    }

    return `
        <!-- Sidebar Toggle (Mobile) -->
        <button class="sidebar-toggle" onclick="toggleSidebar()">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/>
            </svg>
        </button>

        <!-- Sidebar Overlay (Mobile) -->
        <div class="sidebar-overlay" onclick="closeSidebar()"></div>

        <!-- Sidebar -->
        <aside class="sidebar" id="sidebar" data-lang="${language}" data-active-page="${activePage}" dir="${direction}">
            <!-- Header -->
            <div class="sidebar-header">
                <div class="sidebar-logo">
                    <div class="sidebar-logo-icon">
                        <img src="logo/net-spoof.svg" alt="NET SPOOF Logo" class="w-10 h-10 rounded-lg">
                    </div>
                    <span class="sidebar-logo-text">NET SPOOF</span>
                        ${languageButton}
                </div>
                <div class="sidebar-user">
                    <div class="flex items-center gap-2">
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/>
                        </svg>
                        <span class="font-medium">${username}</span>
                    </div>
                </div>
            </div>

            <!-- Menu -->
            <nav class="sidebar-menu">
                <!-- Main Section -->
                <div class="menu-section">
                    <div class="menu-section-title">${t('main')}</div>
                    
                    <a href="dashboard.html" class="menu-item ${activePage === 'dashboard' ? 'active' : ''}" title="${t('dashboard')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/>
                        </svg>
                        <span>${t('dashboard')}</span>
                    </a>

                    <a href="analytics.html" class="menu-item ${activePage === 'analytics' ? 'active' : ''}" title="${t('analytics')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/>
                        </svg>
                        <span>${t('analytics')}</span>
                    </a>
                </div>

                <!-- All Cores Clients Section -->
                <div class="menu-section">
                    <div class="menu-section-title">${t('allCoresClients')}</div>
                    
                    <a href="clients.html" class="menu-item ${activePage === 'clients' ? 'active' : ''}" title="${t('openvpnClients')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/>
                        </svg>
                        <span>${t('openvpnClients')}</span>
                    </a>

                    <a href="admins.html" class="menu-item ${activePage === 'admins' ? 'active' : ''}" title="${t('admins')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 20h5v-2a4 4 0 00-5.4-3.75M9 20H3v-2a4 4 0 016-3.46M15 7a4 4 0 11-8 0 4 4 0 018 0zm4 3v6m3-3h-6"/>
                        </svg>
                        <span>${t('admins')}</span>
                    </a>
                </div>

                <!-- Configuration Section -->
                <div class="menu-section">
                    <div class="menu-section-title">${t('configuration')}</div>
                    <a href="routing.html" class="menu-item ${activePage === 'routing' ? 'active' : ''}" title="${t('routingOutbounds')}">
                        <svg class="sidebar-menu-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 7h11m0 0l-3-3m3 3l-3 3M20 17H9m0 0l3-3m-3 3l3 3"></path>
                        </svg>
                        <span>${t('outbounds')}</span>
                    </a>

                    <a href="nodes.html" class="menu-item ${activePage === 'nodes' ? 'active' : ''}" title="${t('nodes')}">
                        <svg class="sidebar-menu-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <rect x="3" y="4" width="18" height="6" rx="2" stroke-width="2"></rect>
                            <rect x="3" y="14" width="18" height="6" rx="2" stroke-width="2"></rect>
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 7h.01M7 17h.01"></path>
                        </svg>
                        <span>${t('nodes')}</span>
                    </a>



                    <a href="server-checker.html" class="menu-item ${activePage === 'server-checker' ? 'active' : ''}" title="${t('serverChecker')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>
                        </svg>
                        <span>${t('serverChecker')}</span>
                    </a>

                    <a href="settings.html" class="menu-item ${activePage === 'settings' ? 'active' : ''}" title="${t('settings')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/>
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>
                        </svg>
                        <span>${t('settings')}</span>
                    </a>
                </div>

                <!-- Other Section -->
                <div class="menu-section">
                    <div class="menu-section-title">${t('resources')}</div>
                    
                    <a href="api-docs.html" class="menu-item ${activePage === 'api-docs' ? 'active' : ''}" title="${t('apiDocs')}">
                        <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/>
                        </svg>
                        <span>${t('apiDocs')}</span>
                    </a>
                </div>
            </nav>

            <!-- Footer -->
            <div class="sidebar-footer">
                <button onclick="logout()" class="logout-btn">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/>
                    </svg>
                    <span>${t('logout')}</span>
                </button>
            </div>
        </aside>
    `;
}

// Toggle sidebar (mobile)
function toggleSidebar() {
    const sidebar = document.getElementById('sidebar');
    const overlay = document.querySelector('.sidebar-overlay');
    
    sidebar.classList.toggle('open');
    overlay.classList.toggle('active');
}

function closeSidebar() {
    const sidebar = document.getElementById('sidebar');
    const overlay = document.querySelector('.sidebar-overlay');
    
    sidebar.classList.remove('open');
    overlay.classList.remove('active');
}

// Initialize sidebar only after the backend-authoritative role has resolved.
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

    window.irangateSidebarActivePage = activePage;
    const sidebarHTML = createSidebar(activePage);
    document.body.insertAdjacentHTML('afterbegin', sidebarHTML);

    // Close sidebar when clicking menu item on mobile
    if (window.innerWidth <= 1024) {
        document.querySelectorAll('.menu-item').forEach(item => {
            item.addEventListener('click', closeSidebar);
        });
    }
}

// Show coming soon modal
function showComingSoon(featureName) {
    const modal = document.createElement('div');
    modal.className = 'fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50';
    modal.innerHTML = `
        <div class="bg-white rounded-lg p-8 max-w-md mx-4">
            <div class="text-center">
                <div class="mb-4">
                    <svg class="w-16 h-16 mx-auto text-purple-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/>
                    </svg>
                </div>
                <h3 class="text-xl font-semibold text-gray-800 mb-2">${featureName}</h3>
                <p class="text-gray-600 mb-6 text-2xl">💫 Wait for the surprise!</p>
                <div class="text-left text-sm text-gray-600 mb-6">
                    <p class="mb-2 font-medium">Something amazing is coming:</p>
                    <ul class="list-disc list-inside space-y-1">
                        <li>Automatic tunnel creation based on traffic patterns</li>
                        <li>Intelligent routing rule generation</li>
                        <li>Automated client provisioning</li>
                        <li>Dynamic bandwidth allocation</li>
                    </ul>
                    <p class="mt-3 font-medium text-purple-600">🎁 Stay tuned for the surprise!</p>
                </div>
                <button onclick="this.closest('.fixed').remove()" class="bg-purple-600 hover:bg-purple-700 text-white px-6 py-2 rounded-lg">
                    Got it
                </button>
            </div>
        </div>
    `;
    
    document.body.appendChild(modal);
}

