#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

OUT_DIR="${OUT_DIR:-$REPO_DIR/offline-bundles}"
REQUIRE_WEBPANEL_BINARY="${REQUIRE_WEBPANEL_BINARY:-1}"

mkdir -p "$OUT_DIR"

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || date +%s)"
BRANCH="$(git branch --show-current 2>/dev/null || echo unknown)"
NAME="irangate-panel-offline-${BRANCH}-${COMMIT}-$(date +%Y%m%d-%H%M%S)"
WORK="/tmp/${NAME}"

log() { echo "[bundle] $*"; }
warn() { echo "[bundle][WARN] $*" >&2; }
fail() { echo "[bundle][ERROR] $*" >&2; exit 1; }

go_candidates() {
  {
    type -P -a go 2>/dev/null || true
    printf '%s\n' \
      /usr/local/go/bin/go \
      /opt/go/bin/go \
      /usr/bin/go \
      /snap/bin/go
  } | awk 'NF && !seen[$0]++'
}

try_build_webpanel() {
  local out="$1"
  local go_bin ver

  while IFS= read -r go_bin; do
    [[ -x "$go_bin" ]] || continue

    ver="$("$go_bin" version 2>/dev/null || true)"
    log "Trying webpanel build with ${ver:-$go_bin}"

    rm -f "$out"

    if (
      cd "$REPO_DIR/webpanel/backend"
      "$go_bin" build -o "$out" .
    ); then
      chmod 0755 "$out"
      log "Built webpanel binary: $out"
      return 0
    fi

    warn "Webpanel build failed with ${ver:-$go_bin}"
  done < <(go_candidates)

  return 1
}

copy_existing_webpanel() {
  local out="$1"
  local src

  for src in \
    "${OFFLINE_WEBPANEL_BIN:-}" \
    /usr/local/bin/irangate-webpanel \
    /opt/irangate/bin/irangate-webpanel \
    /tmp/irangate-webpanel
  do
    [[ -n "$src" ]] || continue
    [[ -x "$src" ]] || continue

    log "Using existing webpanel binary: $src"
    install -m 0755 "$src" "$out"
    return 0
  done

  return 1
}

rm -rf "$WORK"
mkdir -p "$WORK"

log "Exporting repo source from ${BRANCH}@${COMMIT}"
git archive --format=tar HEAD | tar -x -C "$WORK"

mkdir -p "$WORK/offline-assets"

WEBPANEL_OUT="$WORK/offline-assets/irangate-webpanel"

if ! try_build_webpanel "$WEBPANEL_OUT"; then
  warn "Could not build webpanel from source"
  if ! copy_existing_webpanel "$WEBPANEL_OUT"; then
    if [[ "$REQUIRE_WEBPANEL_BINARY" == "1" ]]; then
      fail "No usable webpanel binary found. Install newer Go or set OFFLINE_WEBPANEL_BIN=/path/to/irangate-webpanel"
    fi
    warn "Creating source-only bundle without webpanel binary"
  fi
fi

cat > "$WORK/offline-assets/manifest.txt" <<EOF
IranGate Offline Assets
Branch: ${BRANCH}
Commit: ${COMMIT}
Created: $(date -Is)
Webpanel binary included: $([[ -x "$WEBPANEL_OUT" ]] && echo yes || echo no)
EOF

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

Important:
- This bundle installs/updates source, webpanel binary, frontend, ovp-panel, routing CLI, and backup wrappers.
- It does not install OS packages from apt when internet is unavailable.
- For a fully fresh offline server, nginx/postgresql/openvpn/curl/rsync must already exist or be installed from a separate dependency bundle.
EOF

ARCHIVE="$OUT_DIR/${NAME}.tar.gz"

log "Creating archive: $ARCHIVE"
tar -C /tmp -czf "$ARCHIVE" "$NAME"

sha256sum "$ARCHIVE" > "${ARCHIVE}.sha256"

rm -rf "$WORK"

echo
echo "Bundle: $ARCHIVE"
echo "SHA256: ${ARCHIVE}.sha256"
