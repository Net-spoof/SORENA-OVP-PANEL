#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

OUT_DIR="${OUT_DIR:-$REPO_DIR/offline-bundles}"
mkdir -p "$OUT_DIR"

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || date +%s)"
BRANCH="$(git branch --show-current 2>/dev/null || echo unknown)"
NAME="irangate-panel-offline-${BRANCH}-${COMMIT}-$(date +%Y%m%d-%H%M%S)"
WORK="/tmp/${NAME}"

log() { echo "[bundle] $*"; }

find_go() {
  if command -v go >/dev/null 2>&1; then command -v go; return 0; fi
  if [[ -x /usr/local/go/bin/go ]]; then echo /usr/local/go/bin/go; return 0; fi
  if [[ -x /usr/bin/go ]]; then echo /usr/bin/go; return 0; fi
  if [[ -x /snap/bin/go ]]; then echo /snap/bin/go; return 0; fi
  return 1
}

rm -rf "$WORK"
mkdir -p "$WORK"

log "Exporting repo source"
git archive --format=tar HEAD | tar -x -C "$WORK"

mkdir -p "$WORK/offline-assets"

if go_bin="$(find_go)"; then
  log "Building webpanel with $("$go_bin" version)"
  (
    cd "$REPO_DIR/webpanel/backend"
    "$go_bin" mod tidy
    "$go_bin" build -o "$WORK/offline-assets/irangate-webpanel" .
  )
  chmod +x "$WORK/offline-assets/irangate-webpanel"
else
  log "Go not found; bundle will rely on target server Go"
fi

cat > "$WORK/OFFLINE-INSTALL-README.txt" <<EOF
IranGate Offline Panel Bundle
Branch: ${BRANCH}
Commit: ${COMMIT}
Created: $(date -Is)

Install/update on target server:
  tar -xzf ${NAME}.tar.gz
  cd ${NAME}
  sudo bash scripts/offline-install-panel.sh

Optional env overrides:
  WEBPANEL_PORT=8614 SUBSCRIPTION_PORT=2098 PANEL_DOMAIN=example.com sudo -E bash scripts/offline-install-panel.sh

Notes:
- This bundle installs/updates the web panel, frontend, ovp-panel, routing CLI, backup wrappers.
- It does not install OS packages from apt when internet is unavailable.
- For fully fresh offline machines, preinstall nginx/postgresql/openvpn or build a separate OS dependency bundle.
EOF

ARCHIVE="$OUT_DIR/${NAME}.tar.gz"

log "Creating archive: $ARCHIVE"
tar -C /tmp -czf "$ARCHIVE" "$NAME"

sha256sum "$ARCHIVE" > "${ARCHIVE}.sha256"

rm -rf "$WORK"

echo
echo "Bundle: $ARCHIVE"
echo "SHA256: ${ARCHIVE}.sha256"
