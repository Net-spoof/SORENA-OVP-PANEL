#!/usr/bin/env python3
from pathlib import Path


def replace_once(text, old, new, label):
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected 1 match, got {count}")
    return text.replace(old, new, 1)


def go_func_span(text, name):
    start = text.find(f"func {name}(")
    if start < 0:
        raise SystemExit(f"Go function {name} not found")
    opening = text.find("{", start)
    if opening < 0:
        raise SystemExit(f"Go function {name}: opening brace not found")
    depth = 0
    in_string = False
    in_raw = False
    escape = False
    i = opening
    while i < len(text):
        ch = text[i]
        if in_raw:
            if ch == "`":
                in_raw = False
        elif in_string:
            if escape:
                escape = False
            elif ch == "\\":
                escape = True
            elif ch == '"':
                in_string = False
        else:
            if ch == "`":
                in_raw = True
            elif ch == '"':
                in_string = True
            elif ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    return start, i + 1
        i += 1
    raise SystemExit(f"Go function {name}: closing brace not found")


def find_matching_brace(text, opening):
    depth = 0
    in_string = False
    in_raw = False
    escape = False
    i = opening
    while i < len(text):
        ch = text[i]
        if in_raw:
            if ch == "`":
                in_raw = False
        elif in_string:
            if escape:
                escape = False
            elif ch == "\\":
                escape = True
            elif ch == '"':
                in_string = False
        else:
            if ch == "`":
                in_raw = True
            elif ch == '"':
                in_string = True
            elif ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    return i
        i += 1
    raise SystemExit("matching brace not found")


# ---------------------------------------------------------------------------
# PostgreSQL accounting v3
# ---------------------------------------------------------------------------
p = Path("deploy/master/scripts/irangate-pg-usage-tick.py")
text = p.read_text()

if "ACCOUNTING_V3_MARKER" not in text:
    text = replace_once(
        text,
        'ACCOUNTING_V2_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v2.marker")\n',
        'ACCOUNTING_V2_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v2.marker")\n'
        'ACCOUNTING_V3_MARKER = Path("/opt/irangate/traffic/pg_usage_accounting_v3.marker")\n',
        "accounting v3 marker",
    )

old_mode = '''def counter_mode(source):
    source = str(source or "").strip().lower()
    return "session" if source == "ikev2" else "cumulative"
'''
new_mode = '''def counter_mode(source):
    source = str(source or "").strip().lower()
    # current_stats.json contains one row per local OpenVPN session.
    # IKEv2 is per-SA. Remote node snapshots are cumulative per node/CN.
    return "session" if source in ("local", "ikev2") else "cumulative"
'''
if old_mode in text:
    text = text.replace(old_mode, new_mode, 1)
elif new_mode not in text:
    raise SystemExit("counter_mode shape is unexpected")

start = text.find("def counter_epoch_key(")
if start < 0:
    raise SystemExit("counter_epoch_key missing")
end = text.find("\ndef ", start + 1)
if end < 0:
    raise SystemExit("counter_epoch_key end missing")
new_epoch = '''def counter_epoch_key(source, x, session):
    """Return a durable accounting stream key for one counter epoch."""
    source = str(source or "").strip()
    if counter_mode(source) != "session":
        # Node counters are cumulative per node/client, so reconnects must keep
        # using the same durable source row.
        return source

    connected_since = str(x.get("connected_since") or "").strip()
    real_address = str(
        x.get("real_address") or x.get("remote_address") or ""
    ).strip()

    if source.lower() == "ikev2":
        # One IKE SA is a counter epoch. connected_since is stable even if a
        # MOBIKE/NAT address changes while the SA remains alive.
        seed = connected_since or session or "unknown_session"
    else:
        # OpenVPN has one row per live session. connected_since + peer address
        # separates simultaneous sessions of the same CN and remains stable on
        # replay of the same status snapshot.
        parts = (connected_since, real_address)
        seed = "\\x1f".join(parts) if any(parts) else session

    digest = hashlib.sha256(seed.encode("utf-8", "replace")).hexdigest()[:24]
    return f"{source}@{digest}"
'''
text = text[:start] + new_epoch + text[end:]

if "def write_accounting_v3_marker():" not in text:
    anchor = "\nap = argparse.ArgumentParser()\n"
    block = '''

def write_accounting_v3_marker():
    ACCOUNTING_V3_MARKER.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTING_V3_MARKER.with_suffix(".marker.tmp")
    tmp.write_text(
        json.dumps(
            {
                "version": 3,
                "activated_at": datetime.now(timezone.utc).isoformat(),
                "policy": "local+ikev2 session epochs; node cumulative; exact client identity",
            },
            ensure_ascii=False,
            indent=2,
        ) + "\\n"
    )
    tmp.replace(ACCOUNTING_V3_MARKER)


def self_test_accounting_identity():
    local_a = {
        "connected_since": "2026-09-15 10:00:00",
        "real_address": "1.2.3.4:10001",
    }
    local_b = {
        "connected_since": "2026-09-15 10:00:00",
        "real_address": "1.2.3.4:10002",
    }
    assert counter_mode("local") == "session"
    assert counter_mode("ikev2") == "session"
    assert counter_mode("turkey") == "cumulative"
    key_a = counter_epoch_key("local", local_a, "a")
    key_b = counter_epoch_key("local", local_b, "b")
    assert key_a != key_b, (key_a, key_b)
    assert key_a == counter_epoch_key("local", dict(local_a), "replay")
    assert counter_epoch_key("turkey", local_a, "a") == "turkey"
    print("accounting_identity_self_test = PASS")
'''
    if anchor not in text:
        raise SystemExit("argparse anchor missing")
    text = text.replace(anchor, block + anchor, 1)

if 'ap.add_argument("--self-test", action="store_true")' not in text:
    text = replace_once(
        text,
        'ap.add_argument("--verbose", action="store_true")\nargs = ap.parse_args()\n',
        'ap.add_argument("--verbose", action="store_true")\n'
        'ap.add_argument("--self-test", action="store_true")\n'
        'args = ap.parse_args()\n\n'
        'if args.self_test:\n'
        '    self_test_accounting_identity()\n'
        '    raise SystemExit(0)\n',
        "self test argparse",
    )

if "local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()" not in text:
    text = replace_once(
        text,
        "migration_mode = not ACCOUNTING_V2_MARKER.exists()\n",
        "migration_mode = not ACCOUNTING_V2_MARKER.exists()\n"
        "local_session_v3_migration = not ACCOUNTING_V3_MARKER.exists()\n",
        "v3 migration flag",
    )

text = text.replace(
    "WHERE source_id LIKE 'ikev2@%'\n          AND updated_at < now() - interval '30 days'",
    "WHERE (source_id LIKE 'ikev2@%' OR source_id LIKE 'local@%')\n"
    "          AND updated_at < now() - interval '30 days'",
)

old_first = '''            if migration_mode and aggregate_total_before > 0:
                migration_baselines += 1
                action = "FIRST_MIGRATION_BASELINE_ONLY"
            else:
'''
new_first = '''            if source == "local" and local_session_v3_migration:
                # v2 stored every simultaneous local session in source_id=local.
                # Baseline each live local session exactly once during v3
                # rollout so already-accounted bytes are not charged again.
                migration_baselines += 1
                action = "FIRST_LOCAL_V3_BASELINE_ONLY"
            elif migration_mode and aggregate_total_before > 0:
                migration_baselines += 1
                action = "FIRST_MIGRATION_BASELINE_ONLY"
            else:
'''
if old_first in text:
    text = text.replace(old_first, new_first, 1)
elif new_first not in text:
    raise SystemExit("first sample migration block is unexpected")

if "if local_session_v3_migration:\n        write_accounting_v3_marker()" not in text:
    text = replace_once(
        text,
        "    if migration_mode:\n        write_accounting_v2_marker()\n",
        "    if migration_mode:\n        write_accounting_v2_marker()\n\n"
        "    if local_session_v3_migration:\n"
        "        write_accounting_v3_marker()\n",
        "v3 marker activation",
    )

p.write_text(text)


# ---------------------------------------------------------------------------
# Exact client identity for delegated-admin accounting
# ---------------------------------------------------------------------------
p = Path("webpanel/backend/delegated_admins.go")
text = p.read_text()
old = '''\t\tkey := strings.ToLower(strings.TrimSpace(rawName))
\t\tif key != "" {
\t\t\tresult[key] += total
\t\t}
'''
new = '''\t\tkey := strings.TrimSpace(rawName)
\t\tif key != "" {
\t\t\t// Existing client identity is exact/case-sensitive. Never merge
\t\t\t// meeee and Meeee into one delegated-admin quota bucket.
\t\t\tresult[key] = total
\t\t}
'''
if old in text:
    text = text.replace(old, new, 1)
elif new not in text:
    raise SystemExit("delegatedGlobalUsageMap block is unexpected")

old = '\t\tkey := strings.ToLower(clientName)\n\t\tcurrent := global[key]\n'
new = '\t\tkey := strings.TrimSpace(clientName)\n\t\tcurrent := global[key]\n'
if old in text:
    text = text.replace(old, new, 1)
elif new not in text:
    raise SystemExit("delegated snapshot lookup is unexpected")

text = text.replace(
    "stampDelegatedClientOwner(strings.TrimSpace(request.Name), principal.Username)",
    "stampDelegatedClientOwner(canonicalClientNameForCreate(request.Name), principal.Username)",
)
p.write_text(text)


# ---------------------------------------------------------------------------
# Shared exact usage helper and client-create collision check
# ---------------------------------------------------------------------------
Path("webpanel/backend/client_name_identity.go").write_text('''package main

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "sync"
)

// New client names are canonical lowercase. Existing clients retain their
// exact historical casing until an operator explicitly migrates them.
var clientCreateIdentityMu sync.Mutex

func canonicalClientNameForCreate(name string) string {
    return strings.ToLower(strings.TrimSpace(name))
}

func findCaseInsensitiveClientCollision(name string) (string, bool, error) {
    wanted := canonicalClientNameForCreate(name)
    if wanted == "" {
        return "", false, nil
    }

    dir := filepath.Join(DataBaseDir, "database", "clients")
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return "", false, nil
        }
        return "", false, fmt.Errorf("read clients directory: %w", err)
    }

    for _, entry := range entries {
        if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
            continue
        }
        existing := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
        if strings.EqualFold(strings.TrimSpace(existing), wanted) {
            return existing, true, nil
        }
    }
    return "", false, nil
}

func exactUsageInt64(v interface{}) int64 {
    switch x := v.(type) {
    case float64:
        return int64(x)
    case float32:
        return int64(x)
    case int:
        return int64(x)
    case int64:
        return x
    case json.Number:
        n, _ := x.Int64()
        return n
    case string:
        n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
        return n
    default:
        return 0
    }
}

func exactGlobalUsage(clientName string) (int64, int64, int64, bool) {
    name := strings.TrimSpace(clientName)
    if name == "" {
        return 0, 0, 0, false
    }

    path := filepath.Join(DataBaseDir, "traffic", "global_usage_totals.json")
    payload, err := os.ReadFile(path)
    if err != nil {
        return 0, 0, 0, false
    }

    var root map[string]interface{}
    if json.Unmarshal(payload, &root) != nil {
        return 0, 0, 0, false
    }
    totals, ok := root["totals"].(map[string]interface{})
    if !ok {
        return 0, 0, 0, false
    }

    raw, exists := totals[name]
    if !exists {
        return 0, 0, 0, false
    }
    row, ok := raw.(map[string]interface{})
    if !ok {
        return 0, 0, 0, false
    }

    rx := exactUsageInt64(row["rx"])
    tx := exactUsageInt64(row["tx"])
    total := exactUsageInt64(row["total"])
    if total <= 0 {
        total = rx + tx
    }
    if rx < 0 {
        rx = 0
    }
    if tx < 0 {
        tx = 0
    }
    if total < 0 {
        total = 0
    }
    return rx, tx, total, true
}
''')


# ---------------------------------------------------------------------------
# Web API: exact usage + canonical lowercase new names + HTTP 409 collision
# ---------------------------------------------------------------------------
p = Path("webpanel/backend/api.go")
text = p.read_text()
text = text.replace(
    "globalUsageTotals[strings.ToLower(strings.TrimSpace(name))]",
    "globalUsageTotals[strings.TrimSpace(name)]",
)

s, e = go_func_span(text, "subGlobalUsageTotal")
text = text[:s] + '''func subGlobalUsageTotal(clientName string) int64 {
\t_, _, total, ok := exactGlobalUsage(clientName)
\tif !ok {
\t\treturn 0
\t}
\treturn total
}''' + text[e:]

s, e = go_func_span(text, "createClientHandler")
fn = text[s:e]
if "IRANGATE_CASE_SAFE_CREATE_V1" not in fn:
    needle = 'if err := json.NewDecoder(r.Body).Decode(&req); err != nil {'
    d = fn.find(needle)
    if d < 0:
        raise SystemExit("createClientHandler decode block missing")
    opening = fn.find("{", d)
    closing = find_matching_brace(fn, opening)
    guard = '''

\t// IRANGATE_CASE_SAFE_CREATE_V1
\treq.Name = canonicalClientNameForCreate(req.Name)
\tclientCreateIdentityMu.Lock()
\tdefer clientCreateIdentityMu.Unlock()

\tif existing, collision, err := findCaseInsensitiveClientCollision(req.Name); err != nil {
\t\tsendError(w, "Failed to validate client-name uniqueness", http.StatusInternalServerError)
\t\treturn
\t} else if collision {
\t\tsendError(
\t\t\tw,
\t\t\tfmt.Sprintf("Client name conflicts with existing client %q", existing),
\t\t\thttp.StatusConflict,
\t\t)
\t\treturn
\t}
'''
    fn = fn[: closing + 1] + guard + fn[closing + 1 :]
    text = text[:s] + fn + text[e:]
p.write_text(text)


# ---------------------------------------------------------------------------
# Enforcer: exact bucket only, no EqualFold fallback
# ---------------------------------------------------------------------------
p = Path("webpanel/backend/client_lifecycle.go")
text = p.read_text()
s, e = go_func_span(text, "fastEnforceGlobalUsage")
text = text[:s] + '''func fastEnforceGlobalUsage(clientName string) int64 {
\t_, _, total, ok := exactGlobalUsage(clientName)
\tif ok {
\t\treturn total
\t}

\tdatabase := NewTrafficDatabase()
\ttotals := database.loadCumulativeTotalsMap()
\tname := strings.TrimSpace(clientName)
\tif total, exists := totals[name]; exists {
\t\treturn total.TotalReceived +
\t\t\ttotal.TotalSent +
\t\t\ttotal.LastSessionReceived +
\t\t\ttotal.LastSessionSent
\t}

\treturn 0
}''' + text[e:]
p.write_text(text)


# ---------------------------------------------------------------------------
# Subscription usage uses the same exact identity helper
# ---------------------------------------------------------------------------
p = Path("webpanel/backend/subscription_global_usage_patch.go")
text = p.read_text()
s, e = go_func_span(text, "loadSubGlobalUsage")
text = text[:s] + '''func loadSubGlobalUsage(clientName string) (float64, float64, float64, bool) {
\trx, tx, total, ok := exactGlobalUsage(clientName)
\tif !ok {
\t\treturn 0, 0, 0, false
\t}
\treturn float64(rx), float64(tx), float64(total), true
}''' + text[e:]
text = text.replace('\t"os"\n', "")
p.write_text(text)


# ---------------------------------------------------------------------------
# Core CLI: lowercase all NEW client names, reject case-insensitive duplicate
# ---------------------------------------------------------------------------
p = Path("pkg/client/client.go")
text = p.read_text()
fn_start = text.find("func (m *Manager) CreateClient(name string) (*database.Client, error) {")
if fn_start < 0:
    raise SystemExit("Manager.CreateClient missing")
window = text[fn_start : fn_start + 2500]
if "name = strings.ToLower(strings.TrimSpace(name))" not in window:
    opening = text.find("{", fn_start)
    text = text[: opening + 1] + "\n\tname = strings.ToLower(strings.TrimSpace(name))" + text[opening + 1 :]

old_dup = '''\t_, err := m.GetClient(name)
\tif err == nil {
\t\treturn nil, fmt.Errorf("client %s already exists", name)
\t}
'''
new_dup = '''\texistingClients, err := m.db.GetClients()
\tif err != nil {
\t\treturn nil, fmt.Errorf("failed to validate client-name uniqueness: %v", err)
\t}
\tfor _, existing := range existingClients {
\t\tif strings.EqualFold(strings.TrimSpace(existing.Name), name) {
\t\t\treturn nil, fmt.Errorf("client name conflicts with existing client %s", existing.Name)
\t\t}
\t}
'''
if old_dup in text:
    text = text.replace(old_dup, new_dup, 1)
elif new_dup not in text:
    raise SystemExit("CreateClient duplicate block unexpected")
p.write_text(text)


# ---------------------------------------------------------------------------
# PostgreSQL: safe conditional unique index for clean databases
# ---------------------------------------------------------------------------
p = Path("deploy/master/postgres/schema.sql")
text = p.read_text()
if "IRANGATE_CASE_INSENSITIVE_CLIENT_UNIQUENESS_V1" not in text:
    block = '''

-- IRANGATE_CASE_INSENSITIVE_CLIENT_UNIQUENESS_V1
-- New clients are canonical lowercase. Existing case-colliding rows must be
-- resolved manually before the protective expression index can be created.
DO $$
BEGIN
    IF EXISTS (
        SELECT lower(client_name)
        FROM public.clients
        GROUP BY lower(client_name)
        HAVING count(*) > 1
    ) THEN
        RAISE NOTICE 'Skipping clients_client_name_lower_uidx: existing case-insensitive collisions must be resolved first';
    ELSE
        CREATE UNIQUE INDEX IF NOT EXISTS clients_client_name_lower_uidx
            ON public.clients ((lower(client_name)));
    END IF;
END
$$;
'''
    text = replace_once(
        text,
        "\n--\n-- PostgreSQL database dump complete\n--\n",
        block + "\n--\n-- PostgreSQL database dump complete\n--\n",
        "schema uniqueness insertion",
    )
p.write_text(text)


# ---------------------------------------------------------------------------
# Regression tests
# ---------------------------------------------------------------------------
Path("webpanel/backend/client_name_identity_test.go").write_text('''package main

import (
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func withIdentityTestDataDir(t *testing.T) string {
    t.Helper()
    old := DataBaseDir
    dir := t.TempDir()
    DataBaseDir = dir
    t.Cleanup(func() { DataBaseDir = old })
    if err := os.MkdirAll(filepath.Join(dir, "database", "clients"), 0755); err != nil {
        t.Fatal(err)
    }
    if err := os.MkdirAll(filepath.Join(dir, "traffic"), 0755); err != nil {
        t.Fatal(err)
    }
    return dir
}

func TestExactGlobalUsagePreservesClientCase(t *testing.T) {
    dir := withIdentityTestDataDir(t)
    payload := `{"totals":{"meeee":{"total":100},"Meeee":{"total":250}}}`
    if err := os.WriteFile(filepath.Join(dir, "traffic", "global_usage_totals.json"), []byte(payload), 0600); err != nil {
        t.Fatal(err)
    }

    _, _, lower, ok := exactGlobalUsage("meeee")
    if !ok || lower != 100 {
        t.Fatalf("lower bucket = %d ok=%v", lower, ok)
    }
    _, _, upper, ok := exactGlobalUsage("Meeee")
    if !ok || upper != 250 {
        t.Fatalf("upper bucket = %d ok=%v", upper, ok)
    }
    if _, _, _, ok := exactGlobalUsage("MEEEE"); ok {
        t.Fatal("unexpected case-insensitive usage match")
    }
}

func TestCreateClientRejectsCaseInsensitiveCollision(t *testing.T) {
    dir := withIdentityTestDataDir(t)
    existing := filepath.Join(dir, "database", "clients", "Meeee.json")
    if err := os.WriteFile(existing, []byte(`{"name":"Meeee"}`), 0600); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"name":"MEEEE"}`))
    rec := httptest.NewRecorder()
    createClientHandler(rec, req)

    if rec.Code != http.StatusConflict {
        t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
    }
}

func TestCanonicalNewClientNameIsLowercase(t *testing.T) {
    if got := canonicalClientNameForCreate("  AbC_123  "); got != "abc_123" {
        t.Fatalf("canonical=%q", got)
    }
}
''')

print("accounting v3 + case-safe identity patch completed")
