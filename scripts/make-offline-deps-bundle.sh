#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-$REPO_DIR/offline-bundles}"
mkdir -p "$OUT_DIR"

OS_ID="$(. /etc/os-release && echo "${ID}")"
OS_VER="$(. /etc/os-release && echo "${VERSION_ID}")"
ARCH="$(dpkg --print-architecture)"
STAMP="$(date +%Y%m%d-%H%M%S)"

NAME="irangate-deps-offline-${OS_ID}${OS_VER}-${ARCH}-${STAMP}"
WORK="/tmp/${NAME}"
DEB_DIR="$WORK/debs"

PACKAGES=(
  ca-certificates
  curl
  wget
  rsync
  openssl
  gnupg
  lsb-release
  jq
  tar
  gzip
  unzip
  xz-utils
  cron
  logrotate
  iproute2
  iptables
  nftables
  net-tools
  tcpdump
  nginx
  postgresql
  postgresql-client
  postgresql-contrib
  openvpn
  easy-rsa
  python3
  python3-venv
  python3-pip
  python3-psycopg2
)

log() { echo "[deps-bundle] $*"; }

rm -rf "$WORK"
mkdir -p "$DEB_DIR"

log "OS=${OS_ID} ${OS_VER} ARCH=${ARCH}"
log "Updating apt metadata"
apt-get update

log "Resolving dependencies"
RESOLVED="$WORK/packages.txt"

{
  printf '%s\n' "${PACKAGES[@]}"

  apt-cache depends --recurse --no-recommends --no-suggests \
    --no-conflicts --no-breaks --no-replaces --no-enhances \
    "${PACKAGES[@]}" 2>/dev/null \
    | awk '
        /^[a-zA-Z0-9][a-zA-Z0-9+.-]+$/ {print $1}
        /^[[:space:]]*Depends:/ {
          gsub(/[<>]/, "", $2)
          print $2
        }
      '
} | sed 's/:.*//' | grep -E '^[a-zA-Z0-9][a-zA-Z0-9+.-]+$' | sort -u > "$RESOLVED"

log "Package count: $(wc -l < "$RESOLVED")"

log "Downloading .deb files"
cd "$DEB_DIR"

while read -r pkg; do
  [ -n "$pkg" ] || continue
  apt-get download "$pkg" >/dev/null 2>>"$WORK/download-errors.log" || true
done < "$RESOLVED"

cd "$WORK"

cat > install-offline-deps.sh <<'INSTALL'
#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

echo "===== OFFLINE DEPS INSTALL ====="

if ! ls debs/*.deb >/dev/null 2>&1; then
  echo "No deb files found ❌"
  exit 1
fi

echo "Installing local .deb packages..."
dpkg -i debs/*.deb || true

echo
echo "Fixing dependency configuration without internet..."
apt-get -o Dir::Cache::archives="$DIR/debs" \
  -o Apt::Get::Download-Only=false \
  -o Acquire::Retries=0 \
  -f install -y || true

echo
echo "Second pass..."
dpkg -i debs/*.deb || true

echo
echo "===== CHECK COMMANDS ====="
for c in nginx psql openvpn easyrsa curl rsync python3 jq iptables nft; do
  command -v "$c" >/dev/null 2>&1 && echo "$c ✅" || echo "$c ❌"
done

echo
echo "Offline dependency install completed ✅"
INSTALL

chmod +x install-offline-deps.sh

cat > manifest.txt <<EOF
IranGate Offline Dependencies Bundle
OS: ${OS_ID}
Version: ${OS_VER}
Arch: ${ARCH}
Created: $(date -Is)

Base packages:
$(printf ' - %s\n' "${PACKAGES[@]}")

Resolved package count: $(wc -l < "$RESOLVED")
Deb file count: $(find "$DEB_DIR" -type f -name '*.deb' | wc -l)
EOF

ARCHIVE="$OUT_DIR/${NAME}.tar.gz"

log "Creating archive: $ARCHIVE"
tar -C /tmp -czf "$ARCHIVE" "$NAME"

cd "$OUT_DIR"
sha256sum "$(basename "$ARCHIVE")" > "$(basename "$ARCHIVE").sha256"

rm -rf "$WORK"

echo
echo "Bundle: $ARCHIVE"
echo "SHA256: ${ARCHIVE}.sha256"
