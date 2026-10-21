#!/usr/bin/env bash
# Restore preflight integration test: a split-brain IKEv2 backup must be
# rejected before the core importer performs any write.
set -Eeuo pipefail
umask 077
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/stage/extras/runtime/opt/irangate/ikev2" \
    "$TMP/stage/extras/runtime/etc/swanctl/conf.d"

cat > "$TMP/bin/mock-core" <<'CORE'
#!/usr/bin/env bash
set -e
if [[ "$1" == "--restore" ]]; then
    touch "$MOCK_RESTORE_MARK"
fi
echo '{"success": true}'
CORE
chmod 0700 "$TMP/bin/mock-core"
export MOCK_RESTORE_MARK="$TMP/core-restore-called"
python3 - "$REPO" "$TMP" <<'PY'
from pathlib import Path
import sys
repo, tmp = map(Path, sys.argv[1:])
wrapper = (repo / "scripts/backup-v2/irangate-backup-v2-import-wrapper").read_text()
assert wrapper.count('BASE="/usr/local/bin/irangate-backup-v2-import.py"') == 1
assert wrapper.count('GUARD="/usr/local/sbin/irangate-ikev2-backup-guard"') == 1
wrapper = wrapper.replace('BASE="/usr/local/bin/irangate-backup-v2-import.py"',
                          f'BASE="{tmp}/bin/mock-core"')
wrapper = wrapper.replace('GUARD="/usr/local/sbin/irangate-ikev2-backup-guard"',
                          f'GUARD="{repo}/scripts/backup-v2/ikev2-credential-guard.py"')
(tmp / "bin/wrapper").write_text(wrapper)
PY
chmod 0700 "$TMP/bin/wrapper"
ROOT="$TMP/stage/extras/runtime"
printf 'connections {}\n' > "$ROOT/etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf"
printf 'alpha\tpassword123\t2026-10-01\tactive\n' > "$ROOT/opt/irangate/ikev2/users.tsv"
printf 'secrets {\n eap-alpha {\n id = "alpha"\n secret = "wrongpassword"\n }\n}\n' \
    > "$ROOT/etc/swanctl/conf.d/irangate-ikev2-users.conf"
tar -czf "$TMP/bad.tar.gz" -C "$TMP/stage" extras
if "$TMP/bin/wrapper" --restore "$TMP/bad.tar.gz" >"$TMP/result.log" 2>&1; then
    echo "FAIL: divergent EAP password accepted"
    exit 1
fi
test ! -e "$MOCK_RESTORE_MARK" || {
    echo "FAIL: restore core called before credential guard"
    exit 1
}

# New backups carry only the authoritative TSV, never the EAP duplicate.
rm "$ROOT/etc/swanctl/conf.d/irangate-ikev2-users.conf"
tar -czf "$TMP/good.tar.gz" -C "$TMP/stage" extras
"$TMP/bin/wrapper" --validate-only "$TMP/good.tar.gz" >"$TMP/good.log"
test ! -e "$MOCK_RESTORE_MARK"
echo "IKEV2_BACKUP_IMPORT_PREFLIGHT_TEST=PASS"
