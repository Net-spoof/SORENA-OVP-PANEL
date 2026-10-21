#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="$REPO/deploy/master/ikev2/irangate-ikev2"
AUDIT="$REPO/scripts/audit-ikev2-credential-parity.py"

bash -n "$SOURCE"
python3 "$AUDIT" --self-test

TEMP="$(mktemp -d)"
trap 'rm -rf "$TEMP"' EXIT
mkdir -p "$TEMP/ikev2" "$TEMP/swan" "$TEMP/bin"

# Only replace paths in the disposable test copy.
python3 - "$SOURCE" "$TEMP" <<'PY'
from pathlib import Path
import sys
source, root = Path(sys.argv[1]), Path(sys.argv[2])
content = source.read_text()
a = 'BASE_DIR="/opt/irangate/ikev2"'
b = 'USERS_CONF="/etc/swanctl/conf.d/irangate-ikev2-users.conf"'
assert content.count(a) == 1 and content.count(b) == 1
content = content.replace(a, f'BASE_DIR="{root}/ikev2"')
content = content.replace(b, f'USERS_CONF="{root}/swan/users.conf"')
(root / 'manager').write_text(content)
PY
chmod 700 "$TEMP/manager"
cat > "$TEMP/bin/swanctl" <<'SH'
#!/usr/bin/env bash
if [[ "${MOCK_SWANCTL_FAIL:-0}" == "1" ]]; then
  exit 2
fi
exit 0
SH
chmod 700 "$TEMP/bin/swanctl"
export PATH="$TEMP/bin:$PATH"
MANAGER="$TEMP/manager"
TSV="$TEMP/ikev2/users.tsv"
CONF="$TEMP/swan/users.conf"

"$MANAGER" add alpha password123 >/dev/null
python3 "$AUDIT" --tsv "$TSV" --conf "$CONF" --strict >/dev/null
created_before="$(awk -F '\t' '$1=="alpha"{print $3}' "$TSV")"
"$MANAGER" rotate alpha >/dev/null
created_after="$(awk -F '\t' '$1=="alpha"{print $3}' "$TSV")"
[[ "$created_before" == "$created_after" ]] || { echo "FAILED creation-date preservation"; exit 1; }
python3 "$AUDIT" --tsv "$TSV" --conf "$CONF" --strict >/dev/null

"$MANAGER" block alpha >/dev/null
python3 "$AUDIT" --tsv "$TSV" --conf "$CONF" --strict >/dev/null
"$MANAGER" unblock alpha >/dev/null
python3 "$AUDIT" --tsv "$TSV" --conf "$CONF" --strict >/dev/null

before="$(sha256sum "$TSV" "$CONF" | awk '{print $1}' | tr '\n' ' ')"
if MOCK_SWANCTL_FAIL=1 "$MANAGER" add beta password456 >/dev/null 2>&1; then
  echo "FAILED: reload error unexpectedly accepted"
  exit 1
fi
after="$(sha256sum "$TSV" "$CONF" | awk '{print $1}' | tr '\n' ' ')"
[[ "$before" == "$after" ]] || { echo "FAILED: reload failure did not roll back"; exit 1; }
python3 "$AUDIT" --tsv "$TSV" --conf "$CONF" --strict >/dev/null

"$MANAGER" add beta password456 >/dev/null &
a=$!
"$MANAGER" add gamma password789 >/dev/null &
b=$!
wait "$a"
wait "$b"
python3 "$AUDIT" --tsv "$TSV" --conf "$CONF" --strict >/dev/null
[[ "$(wc -l < "$TSV")" == "3" ]] || { echo "FAILED: concurrent users lost"; exit 1; }

echo "IKEV2_CREDENTIAL_INTEGRITY_TEST=PASS"
