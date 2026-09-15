// Delegated-admin portal navigation.
// Root/admin navigation is intentionally left untouched.
(function () {
    'use strict';

    const isClientsPage = /(?:^|\/)clients\.html$/.test(window.location.pathname);

    // The master/local OpenVPN location is mandatory for every client. Keep it
    // selected in the DOM for create/edit flows, but do not expose a redundant
    // checkbox to the operator. Remote locations remain user-selectable.
    function enforceImplicitMasterLocation(root = document) {
        const selectors = [
            '#clientNodesBox input.client-node-checkbox[value="local"]',
            '#editClientNodesBox input.edit-client-node-checkbox[value="local"]'
        ];

        selectors.forEach((selector) => {
            const inputs = [];

            if (root && root.matches && root.matches(selector)) {
                inputs.push(root);
            }
            if (root && root.querySelectorAll) {
                inputs.push(...root.querySelectorAll(selector));
            }

            inputs.forEach((input) => {
                input.checked = true;
                input.disabled = true;

                const label = input.closest('label');
                if (label) {
                    label.hidden = true;
                    label.style.display = 'none';
                    label.setAttribute('aria-hidden', 'true');
                    label.dataset.implicitMasterLocation = 'true';
                }
            });
        });
    }

    // The panel currently has a single/default OpenVPN server configuration for
    // client creation. Select it automatically and remove the redundant selector
    // from the UI. The legacy inline submit handler can continue reading
    // #clientInbound, so backend behaviour stays unchanged.
    function chooseImplicitServerConfiguration(select) {
        if (!select) return false;

        const options = Array.from(select.options || []).filter(
            option => option && option.value && !option.disabled
        );

        if (!options.length) return false;

        let selected = options.find((option) => {
            try {
                const config = option.dataset && option.dataset.config
                    ? JSON.parse(option.dataset.config)
                    : null;
                return String(config && config.status || '').toLowerCase() === 'active';
            } catch (_) {
                return false;
            }
        });

        if (!selected) {
            selected = options[0];
        }

        if (select.value !== selected.value) {
            select.value = selected.value;
            select.dispatchEvent(new Event('change', { bubbles: true }));
        }

        return Boolean(select.value);
    }

    function enforceImplicitServerConfiguration(root = document) {
        let select = null;

        if (root && root.matches && root.matches('#clientInbound')) {
            select = root;
        } else if (root && root.querySelector) {
            select = root.querySelector('#clientInbound');
        }

        if (!select) {
            select = document.getElementById('clientInbound');
        }

        if (!select) return;

        chooseImplicitServerConfiguration(select);

        const wrapper = select.closest('.mb-6') || select.parentElement;
        if (wrapper) {
            wrapper.hidden = true;
            wrapper.style.display = 'none';
            wrapper.setAttribute('aria-hidden', 'true');
            wrapper.dataset.implicitServerConfiguration = 'true';
        }
    }

    function initImplicitClientDefaults() {
        enforceImplicitMasterLocation(document);
        enforceImplicitServerConfiguration(document);

        if (!document.body) return;

        const clientDefaultsObserver = new MutationObserver((mutations) => {
            let inboundChanged = false;
            let clientNodesChanged = false;

            mutations.forEach((mutation) => {
                if (
                    mutation.target &&
                    mutation.target.id === 'clientInbound'
                ) {
                    inboundChanged = true;
                }

                mutation.addedNodes.forEach((node) => {
                    if (!node || node.nodeType !== 1) return;

                    if (
                        node.matches?.('#clientInbound, #clientInbound option') ||
                        node.querySelector?.('#clientInbound')
                    ) {
                        inboundChanged = true;
                    }

                    if (
                        node.matches?.('.client-node-checkbox, .edit-client-node-checkbox') ||
                        node.querySelector?.('.client-node-checkbox, .edit-client-node-checkbox')
                    ) {
                        clientNodesChanged = true;
                    }
                });
            });

            if (inboundChanged) {
                enforceImplicitServerConfiguration(document);
            }

            if (clientNodesChanged) {
                enforceImplicitMasterLocation(document);
            }
        });

        clientDefaultsObserver.observe(document.body, {
            childList: true,
            subtree: true
        });

        // One extra pass after inline page scripts finish wiring the form.
        setTimeout(() => {
            enforceImplicitMasterLocation(document);
            enforceImplicitServerConfiguration(document);
        }, 0);

        window.addEventListener('beforeunload', () => {
            clientDefaultsObserver.disconnect();
        }, { once: true });
    }

    if (isClientsPage) {
        if (document.readyState === 'loading') {
            document.addEventListener('DOMContentLoaded', initImplicitClientDefaults, { once: true });
        } else {
            initImplicitClientDefaults();
        }
    }

    if (typeof window.createSidebar !== 'function') {
        console.error('IranGate delegated portal: createSidebar is unavailable');
        return;
    }

    const isDelegated = (localStorage.getItem('role') || 'root') === 'delegated_admin';
    const rootCreateSidebar = window.createSidebar;

    // The orphan-certificate controls are root-only. The legacy clients page checks
    // them automatically, so suppress that request for delegated admins and hide
    // the corresponding card without granting any additional backend access.
    if (isDelegated && typeof window.fetch === 'function') {
        const nativeFetch = window.fetch.bind(window);
        window.fetch = function delegatedSafeFetch(input, init) {
            try {
                const raw = typeof input === 'string' ? input : input && input.url;
                const method = String((init && init.method) || 'GET').toUpperCase();
                const url = new URL(raw, window.location.origin);
                if (method === 'GET' && url.pathname === '/api/clients/orphaned') {
                    return Promise.resolve(new Response(JSON.stringify({
                        success: true,
                        message: 'Root-only check skipped for delegated admin',
                        data: { count: 0 }
                    }), {
                        status: 200,
                        headers: { 'Content-Type': 'application/json' }
                    }));
                }
            } catch (_) {
                // Fall through to the real request.
            }
            return nativeFetch(input, init);
        };

        document.addEventListener('DOMContentLoaded', function hideRootOnlyClientTools() {
            const orphanedList = document.getElementById('orphanedList');
            const orphanedSection = orphanedList ? orphanedList.closest('.mt-8') : null;
            if (orphanedSection) orphanedSection.classList.add('hidden');
        });
    }

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
                            <img src="logo/net-spoof.svg" alt="NET SPOOF Logo" class="w-10 h-10 rounded-lg">
                        </div>
                        <span class="sidebar-logo-text">NET SPOOF</span>
                    </div>
                    <div class="sidebar-user">
                        <div class="flex items-center gap-2">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/>
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
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 013-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/>
                        </svg>
                        <span>Logout</span>
                    </button>
                </div>
            </aside>`;
    };
})();
