#!/usr/bin/env bash
# Verify the current SURENA client UI, Outbounds V2, local UDP port locations and PKI fixes are present
# in a source tree, including the tree extracted from a self-extracting .run.
# No network calls or changes to the target server.
set -Eeuo pipefail

ROOT="${1:-.}"
ROOT="$(cd "$ROOT" && pwd -P)"

require_file() {
  local path="$1"
  [[ -s "$ROOT/$path" ]] || {
    echo "[offline-verify][ERROR] Required source missing: $path" >&2
    exit 1
  }
}
require_text() {
  local path="$1"
  local text="$2"
  require_file "$path"
  grep -Fq -- "$text" "$ROOT/$path" || {
    echo "[offline-verify][ERROR] Missing required integration in: $path" >&2
    exit 1
  }
}

require_file webpanel/frontend/clients.html
require_file webpanel/frontend/routing.html
require_file webpanel/frontend/nodes.html
require_file webpanel/frontend/css/routing-panel-v2.css
require_file webpanel/backend/openvpn_port_locations.go
require_file webpanel/backend/routing_api.go
require_file webpanel/frontend/css/clients-panel-v3.css
require_file webpanel/frontend/css/clients-ui.css
require_file webpanel/frontend/css/control-ui.css
require_file webpanel/frontend/js/delegated-portal.js
require_file webpanel/frontend/js/api.js
require_file scripts/apply-panel-extras.sh
require_file scripts/offline/install-master-from-bundle.sh
require_file pkg/cmd/root.go
require_file webpanel/backend/api.go

# Correct Sanaei-inspired responsive layout + no dependence on cached CSS.
require_text webpanel/frontend/clients.html 'css/clients-panel-v3.css?v=sanaei-20261025-v1'
require_text webpanel/frontend/clients.html 'function renderClientsMobile(clients)'
require_text webpanel/frontend/clients.html 'function renderClientsOriginal(clients)'
require_text webpanel/frontend/clients.html 'function openClientActions(client)'
require_text webpanel/frontend/clients.html "runClientAction('more',"
require_text webpanel/frontend/clients.html 'id="editClientForm"'
require_text webpanel/frontend/clients.html 'id="clientSecurityToolbar"'
require_text webpanel/frontend/css/clients-panel-v3.css '#clientsMobileList{display:grid!important'
require_text webpanel/frontend/css/clients-panel-v3.css '#clientQuickActionsModal:not(.hidden)'
require_text webpanel/frontend/css/clients-panel-v3.css '#editClientForm'

# Outbounds V2: retain the 3-tab workspace and real local UDP location
# creation, per-port outbound picker, and the backend's existing contracts.
require_text webpanel/frontend/routing.html 'css/routing-panel-v2.css?v=outbounds-v2-20261025-02'
require_text webpanel/frontend/routing.html 'id="routingV2PaneOutbounds"'
require_text webpanel/frontend/routing.html 'id="routingV2PaneRouting"'
require_text webpanel/frontend/routing.html 'id="routingV2PaneAdvanced"'
require_text webpanel/frontend/routing.html 'id="routingV2PortForm"'
require_text webpanel/frontend/routing.html 'id="routingV2RouteChoices"'
require_text webpanel/frontend/routing.html 'window.createRoutingV2PortLocation'
require_text webpanel/frontend/routing.html 'window.renderRoutingV2PortChoices'
require_text webpanel/frontend/routing.html "apiRequest('/openvpn-port-locations',"
require_text webpanel/frontend/routing.html 'location_id: profile.locationId'
require_text webpanel/frontend/routing.html 'window.activateChecked = async function ()'
require_text webpanel/frontend/nodes.html 'id="port-location-management"'
require_text webpanel/frontend/css/routing-panel-v2.css '.routing-v2-port-form'
require_text webpanel/frontend/css/routing-panel-v2.css '#routingV2RouteChoices'
require_text webpanel/backend/openvpn_port_locations.go 'func createOpenVPNPortLocationHandler('
require_text webpanel/backend/openvpn_port_locations.go 'applyOpenVPNPortLocationsRuntime()'
require_text webpanel/backend/routing_api.go 'func bindRoutingHandler('

# Include the fix for the real-world bug: routine online updates must
# install CSS as well as HTML and JS. Fresh offline installs sync whole tree.
require_text scripts/apply-panel-extras.sh 'for asset_dir in js css; do'
require_text scripts/offline/install-master-from-bundle.sh 'rsync -a "$SCRIPT_DIR/webpanel/frontend/" /opt/irangate/webpanel/frontend/'

# Preserve fixes for clean PKI scan JSON and authoritative traffic reset.
require_text pkg/cmd/root.go 'utils.GetLogger().SetOutput(os.Stderr)'
require_text webpanel/backend/api.go 'Invalid orphaned certificate scan output'
require_text webpanel/backend/api.go 'cmd.Output()'
require_text webpanel/frontend/clients.html 'traffic_used_bytes_authoritative'
require_text webpanel/backend/api.go 'traffic_used_bytes_authoritative'

bash -n "$ROOT/scripts/apply-panel-extras.sh"
bash -n "$ROOT/scripts/offline/install-master-from-bundle.sh"
if command -v node >/dev/null 2>&1; then
  node --check "$ROOT/webpanel/frontend/js/delegated-portal.js"
  node --check "$ROOT/webpanel/frontend/js/api.js"
fi

echo "[offline-verify] SURENA full source: Clients UI, Outbounds V2, local UDP ports, PKI and traffic fixes present ✅"
