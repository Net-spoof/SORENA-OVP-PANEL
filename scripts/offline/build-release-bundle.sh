#!/usr/bin/env bash
# Run ONLY on an internet-connected GitHub Actions Ubuntu runner.
# Produces a self-extracting .run with a complete local APT repository.
set -Eeuo pipefail
umask 022
[[ "${CI:-}" == "true" ]] || { echo "This builder must run in CI" >&2; exit 1; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$ROOT"
. /etc/os-release
[[ "$ID" == "ubuntu" && ( "$VERSION_ID" == "22.04" || "$VERSION_ID" == "24.04" ) ]] ||
    { echo "Unsupported build runner OS" >&2; exit 1; }
[[ "$(dpkg --print-architecture)" == "amd64" ]] || exit 1

DISTRO="$VERSION_ID"
OUT="${OUT_DIR:-$RUNNER_TEMP/offline-release}"
WORK="$(mktemp -d "$RUNNER_TEMP/irangate-offline.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
PAYLOAD="$WORK/payload"
mkdir -p "$OUT" "$PAYLOAD/offline-assets/apt/pool" "$PAYLOAD/offline-assets/wheelhouse"

echo "[builder] Building Go binaries"
export CGO_ENABLED=0
( cd "$ROOT/webpanel/backend" &&
  go test -run '^(TestTelegramBackup|TestBackupImport)' -count=1 ./... &&
  go build -trimpath -o "$PAYLOAD/offline-assets/irangate-webpanel" . )
( cd "$ROOT" && go build -trimpath -o "$PAYLOAD/offline-assets/irangate-cli" ./main.go )
chmod 755 "$PAYLOAD/offline-assets/irangate-webpanel" "$PAYLOAD/offline-assets/irangate-cli"
"$PAYLOAD/offline-assets/irangate-cli" version >/dev/null
go version -m "$PAYLOAD/offline-assets/irangate-webpanel" | head -5

echo "[builder] Resolving Ubuntu deb dependencies"
cat > "$PAYLOAD/offline-assets/apt-required.txt" <<'APT'
wget
curl
openvpn
easy-rsa
openssl
ca-certificates
iptables
nftables
jq
sqlite3
net-tools
git
build-essential
procps
python3
python3-pip
python3-venv
python3-dev
python3-psycopg2
python3-requests
python3-psutil
nginx
certbot
python3-certbot-nginx
htop
nano
vim
tree
supervisor
rsync
cron
logrotate
postgresql
postgresql-contrib
strongswan
strongswan-pki
strongswan-swanctl
charon-systemd
libcharon-extra-plugins
libstrongswan-extra-plugins
iproute2
iputils-ping
netcat-openbsd
util-linux
socat
APT

mapfile -t PKGS < "$PAYLOAD/offline-assets/apt-required.txt"

# Resolve dependency *alternatives* via the actual APT solver with a
# blank dpkg status database, so packages preinstalled on GitHub runners
# are still included for a truly fresh offline Ubuntu installation.
: > "$WORK/empty-status"
LC_ALL=C apt-get -s --no-install-recommends \
    -o "Dir::State::status=$WORK/empty-status" \
    install "${PKGS[@]}" > "$WORK/apt-solved.txt"

mapfile -t SOLVED < <(
    sed -n -E 's/^Inst ([^ ]+) \(([^ ]+) .*/\1=\2/p' "$WORK/apt-solved.txt" |
    sort -u
)
(( ${#SOLVED[@]} >= 40 )) || {
    echo "[builder] APT solver returned an incomplete package set" >&2
    exit 1
}
printf '%s\n' "${SOLVED[@]}" > "$WORK/apt-exact-versions.txt"

cd "$PAYLOAD/offline-assets/apt/pool"
downloaded=0
while IFS= read -r package_version; do
    if ! apt-get download "$package_version" \
         >/dev/null 2>>"$WORK/apt-download-warnings.txt"; then
        echo "[builder] Failed to download resolved dependency: $package_version" >&2
        exit 1
    fi
    downloaded=$((downloaded + 1))
done < "$WORK/apt-exact-versions.txt"
(( downloaded >= 40 )) || { echo "APT pool incomplete" >&2; exit 1; }
cd "$PAYLOAD/offline-assets/apt"
dpkg-scanpackages -m pool /dev/null > Packages
gzip -9 -c Packages > Packages.gz
(( $(find pool -name '*.deb' | wc -l) >= 40 )) || exit 1

# Validate all declared top-level dependencies can be selected without any
# network-based repository. This runs against an empty dpkg package status to
# detect gaps, not merely against the builder's already-installed packages.
mkdir -p "$WORK/apttest/lists/partial"
printf 'deb [trusted=yes] file:%s ./\n' "$PAYLOAD/offline-assets/apt" > "$WORK/apttest/local.list"
: > "$WORK/apttest/empty-status"
sudo apt-get -o "Dir::Etc::sourcelist=$WORK/apttest/local.list" \
        -o "Dir::Etc::sourceparts=-" \
        -o "Dir::State::lists=$WORK/apttest/lists" \
        -o "Dir::State::status=$WORK/apttest/empty-status" \
        -o "Acquire::Languages=none" update
sudo apt-get -s --no-install-recommends \
        -o "Dir::Etc::sourcelist=$WORK/apttest/local.list" \
        -o "Dir::Etc::sourceparts=-" \
        -o "Dir::State::lists=$WORK/apttest/lists" \
        -o "Dir::State::status=$WORK/apttest/empty-status" \
        install "${PKGS[@]}" > "$WORK/apt-offline-simulation.txt"
echo "[builder] Offline APT dependency simulation passed"

echo "[builder] Downloading optional AI Python wheels"
python3 -m pip download --only-binary=:all: \
    -r "$ROOT/pkg/ai/requirements.txt" \
    -d "$PAYLOAD/offline-assets/wheelhouse" --disable-pip-version-check
[[ -n "$(find "$PAYLOAD/offline-assets/wheelhouse" -type f -name '*.whl' -print -quit)" ]] ||
    { echo "Python wheelhouse missing" >&2; exit 1; }

echo "[builder] Downloading official sing-box linux-amd64 executable"
export GH_TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
SING_VERSION="$(gh release view --repo SagerNet/sing-box --json tagName --jq .tagName)"
SING_ARCHIVE="$(gh release view "$SING_VERSION" --repo SagerNet/sing-box --json assets --jq '.assets[].name' |
    grep -E -- '-linux-amd64\.tar\.gz$' | head -1)"
[[ -n "$SING_ARCHIVE" ]] || { echo "Missing sing-box linux-amd64 release" >&2; exit 1; }
mkdir -p "$WORK/singbox"
gh release download "$SING_VERSION" -R SagerNet/sing-box -p "$SING_ARCHIVE" -D "$WORK/singbox"
tar -xzf "$WORK/singbox/$SING_ARCHIVE" -C "$WORK/singbox"
SING_BIN="$(find "$WORK/singbox" -type f -name sing-box -print -quit)"
[[ -n "$SING_BIN" ]] || { echo "sing-box extracted binary missing" >&2; exit 1; }
install -m 0755 "$SING_BIN" "$PAYLOAD/offline-assets/sing-box"
"$PAYLOAD/offline-assets/sing-box" version | head -3
printf '%s\n' "$SING_VERSION" > "$PAYLOAD/offline-assets/sing-box-version"

echo "[builder] Copying full project source and master/node assets"
rsync -a --exclude=/.git --exclude=/offline-bundles \
    --exclude='__pycache__' --exclude='*.pyc' \
    "$ROOT/" "$PAYLOAD/"
printf '%s\n' "$DISTRO" > "$PAYLOAD/offline-assets/ubuntu-version"
printf '%s\n' "$(git rev-parse HEAD)" > "$PAYLOAD/offline-assets/source-commit"

# Files are checksummed and verified by the offline installer before mutation.
(
  cd "$PAYLOAD"
  find . -type f ! -path './offline-assets/SHA256SUMS' -print0 |
    sort -z | xargs -0 sha256sum > offline-assets/SHA256SUMS
)

NAME="IranGate-OV-offline-ubuntu${DISTRO//./}-amd64"
ASSET="$OUT/$NAME.run"
cat > "$ASSET" <<'SELF'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$(id -u)" == 0 ]] || { echo "Run with sudo/root" >&2; exit 1; }
SELF_FILE="$(readlink -f "$0")"
TMP="$(mktemp -d /tmp/irangate-offline-release.XXXXXX)"
# _apt needs traversal permission to read file:// packages from this
# otherwise-private temporary extraction directory.
chmod 755 "$TMP"
trap 'rm -rf "$TMP"' EXIT
MARKER='__IRANGATE_BUNDLE_START__'
OFFSET="$(awk -v marker="$MARKER" '$0 == marker { print NR+1; exit }' "$SELF_FILE")"
[[ "$OFFSET" =~ ^[0-9]+$ ]] || { echo "Corrupt self-extracting release" >&2; exit 1; }
tail -n +"$OFFSET" "$SELF_FILE" | tar -xzf - -C "$TMP"
bash "$TMP/scripts/offline/install-master-from-bundle.sh" "$TMP"
exit 0
__IRANGATE_BUNDLE_START__
SELF
tar -czf - -C "$PAYLOAD" . >> "$ASSET"
chmod 0755 "$ASSET"
(
    cd "$OUT"
    sha256sum "$(basename "$ASSET")" > "$(basename "$ASSET").sha256"
)
echo "[builder] Offline release ready: $ASSET"
du -h "$ASSET"
[[ $(stat -c '%s' "$ASSET") -lt 2000000000 ]] || {
    echo "Release file exceeds GitHub's 2GB asset limit" >&2
    exit 1
}
