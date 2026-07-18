#!/usr/bin/env bash
set -euo pipefail

RESTART=0
if [[ "${1:-}" == "--restart" ]]; then
  RESTART=1
fi

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

log() { echo "[panel-extras] $*"; }
warn() { echo "[panel-extras][WARN] $*" >&2; }

find_go() {
  if command -v go >/dev/null 2>&1; then command -v go; return 0; fi
  if [[ -x /usr/local/go/bin/go ]]; then echo /usr/local/go/bin/go; return 0; fi
  if [[ -x /usr/bin/go ]]; then echo /usr/bin/go; return 0; fi
  if [[ -x /snap/bin/go ]]; then echo /snap/bin/go; return 0; fi
  return 1
}

cache_bust_sidebar() {
  local dir="$1"
  [[ -d "$dir" ]] || return 0

  local version
  version="$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || date +%s)-$(date +%s)"

  find "$dir" -maxdepth 1 -type f -name "*.html" -print0 2>/dev/null \
    | xargs -0 -r sed -i -E \
      "s#js/sidebar\.js(\?v=[A-Za-z0-9._-]+)?#js/sidebar.js?v=${version}#g"
}

deploy_frontend() {
  local src="$REPO_DIR/webpanel/frontend"
  [[ -d "$src" ]] || { warn "frontend source not found: $src"; return 0; }

  for dst in /opt/irangate/webpanel/frontend /var/www/html; do
    if [[ -d "$dst" ]]; then
      log "Deploying frontend to $dst"
      mkdir -p "$dst/js"

      # Copy all top-level frontend assets if present.
      find "$src" -maxdepth 1 -type f -print0 \
        | xargs -0 -r -I{} install -m 0644 "{}" "$dst/"

      if [[ -d "$src/js" ]]; then
        find "$src/js" -maxdepth 1 -type f -print0 \
          | xargs -0 -r -I{} install -m 0644 "{}" "$dst/js/"
      fi

      cache_bust_sidebar "$dst"
    fi
  done
}

build_backend() {
  local backend="$REPO_DIR/webpanel/backend"
  [[ -d "$backend" ]] || { warn "backend source not found: $backend"; return 0; }

  local go_bin
  if ! go_bin="$(find_go)"; then
    warn "Go not found; skipping backend build"
    return 0
  fi

  log "Using Go: $("$go_bin" version)"
  cd "$backend"

  "$go_bin" mod tidy
  "$go_bin" build -o /tmp/irangate-webpanel .

  install -m 0755 /tmp/irangate-webpanel /usr/local/bin/irangate-webpanel
  cd "$REPO_DIR"
}

install_cli_and_wrappers() {
  if [[ -f "$REPO_DIR/scripts/irangate-routing" ]]; then
    install -m 0755 "$REPO_DIR/scripts/irangate-routing" /usr/local/bin/irangate-routing
    log "Installed irangate-routing"
  fi

  if [[ -x "$REPO_DIR/scripts/install-backup-v2-extras-wrappers.sh" ]]; then
    bash "$REPO_DIR/scripts/install-backup-v2-extras-wrappers.sh" || warn "backup wrapper installer had warnings"
  fi
}

main() {
  log "Applying panel extras from $REPO_DIR"

  build_backend
  install_cli_and_wrappers
  deploy_frontend

  systemctl daemon-reload || true

  if [[ "$RESTART" == "1" ]]; then
    systemctl restart irangate-webpanel || warn "failed to restart irangate-webpanel"
  fi

  log "Done"
}

main "$@"
