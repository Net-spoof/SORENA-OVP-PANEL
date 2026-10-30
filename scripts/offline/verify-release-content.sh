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

# Offline installers must carry verified v6 session accounting, not the
# historical panel-display counters or OpenVPN status-v1 fallback.
require_file deploy/master/scripts/irangate_local_snapshot.py
require_file deploy/master/scripts/irangate-local-status-snapshot.py
require_file deploy/master/scripts/irangate-pg-usage-tick.py
require_file deploy/master/scripts/irangate-pg-sync-sessions.py
require_file deploy/master/systemd/irangate-pg-usage-tick.service
require_file scripts/repair/surena-migrated-local-baseline.py
require_text deploy/master/scripts/irangate_local_snapshot.py 'COUNTER_KIND = "openvpn_session"'
require_text deploy/master/scripts/irangate_local_snapshot.py 'openvpn_session_stats.json'
require_text deploy/master/scripts/irangate-local-status-snapshot.py "echo 'status 3'"
require_text deploy/master/scripts/irangate-local-status-snapshot.py 'choose_snapshot(read_status_files())'
require_text deploy/master/scripts/irangate-pg-usage-tick.py 'from irangate_local_snapshot import read_snapshot, session_key, valid_session'
require_text deploy/master/scripts/irangate-pg-usage-tick.py 'ACCOUNTING_V6_MARKER'
require_text deploy/master/scripts/irangate-pg-sync-sessions.py 'from irangate_local_snapshot import'
require_text deploy/master/systemd/irangate-pg-usage-tick.service 'irangate-local-status-snapshot.py && /usr/local/bin/irangate-pg-usage-tick.py --apply'
require_text scripts/repair/surena-migrated-local-baseline.py 'openvpn_session_stats.json'
require_text scripts/repair/surena-migrated-local-baseline.py 'mod.session_key'
require_text install.sh 'for script in "$deploy_dir"/scripts/*.py; do'

python3 -m py_compile \
  "$ROOT/deploy/master/scripts/irangate_local_snapshot.py" \
  "$ROOT/deploy/master/scripts/irangate-local-status-snapshot.py" \
  "$ROOT/deploy/master/scripts/irangate-pg-usage-tick.py" \
  "$ROOT/deploy/master/scripts/irangate-pg-sync-sessions.py" \
  "$ROOT/scripts/repair/surena-migrated-local-baseline.py"

bash -n "$ROOT/scripts/apply-panel-extras.sh"
bash -n "$ROOT/scripts/offline/install-master-from-bundle.sh"
if command -v node >/dev/null 2>&1; then
  node --check "$ROOT/webpanel/frontend/js/delegated-portal.js"
  node --check "$ROOT/webpanel/frontend/js/api.js"
fi

echo "[offline-verify] Full source verified: UI, Outbounds V2, UDP ports, PKI and strict OpenVPN accounting v6 ✅"
