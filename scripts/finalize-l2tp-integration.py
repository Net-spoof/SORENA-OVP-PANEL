#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def replace_once(path: Path, old: str, new: str, marker: str) -> None:
    text = path.read_text(encoding="utf-8")
    if marker in text:
        print(f"already patched: {path} [{marker}]")
        return
    count = text.count(old)
    if count != 1:
        raise SystemExit(
            f"patch target mismatch for {path}: expected 1 occurrence, got {count}: {marker}"
        )
    path.write_text(text.replace(old, new, 1), encoding="utf-8")
    print(f"patched: {path} [{marker}]")


def patch_apply_panel_extras() -> None:
    path = ROOT / "scripts/apply-panel-extras.sh"
    old = '''    bash "$REPO_DIR/scripts/install-ikev2.sh" \\
        "${ike_args[@]}"

    routing_args=()
'''
    new = '''    bash "$REPO_DIR/scripts/install-ikev2.sh" \\
        "${ike_args[@]}"

    # IRANGATE_L2TP_PANEL_EXTRAS_V1
    l2tp_args=()

    [[ "$NO_PACKAGES" -eq 0 ]] ||
        l2tp_args+=(--no-packages)

    bash "$REPO_DIR/scripts/install-l2tp.sh" \\
        "${l2tp_args[@]}"

    bash "$REPO_DIR/scripts/install-l2tp-pg-integration.sh"

    routing_args=()
'''
    replace_once(path, old, new, "IRANGATE_L2TP_PANEL_EXTRAS_V1")


def patch_backup_export() -> None:
    path = ROOT / "deploy/master/scripts/irangate-backup-v2-export.py"

    old = '''        "ikev2_current_stats.json": "[]",
        "ikev2_last_seen.json": "{}",
    }
'''
    new = '''        "ikev2_current_stats.json": "[]",
        "ikev2_last_seen.json": "{}",
        # IRANGATE_L2TP_BACKUP_STATE_V1
        "l2tp_current_stats.json": "[]",
        "l2tp_sessions.json": "[]",
        "l2tp_last_seen.json": "{}",
    }
'''
    replace_once(path, old, new, "IRANGATE_L2TP_BACKUP_STATE_V1")

    old = '''        "/etc/systemd/system/irangate-ikev2-stats.service",
        "/etc/systemd/system/irangate-ikev2-stats.timer",
        "/opt/irangate/routing",
'''
    new = '''        "/etc/systemd/system/irangate-ikev2-stats.service",
        "/etc/systemd/system/irangate-ikev2-stats.timer",
        # IRANGATE_L2TP_BACKUP_EXTRAS_V1
        "/etc/swanctl/conf.d/irangate-l2tp.conf",
        "/etc/sysctl.d/99-irangate-l2tp.conf",
        "/etc/xl2tpd/xl2tpd.conf",
        "/etc/ppp/options.xl2tpd",
        "/opt/irangate/l2tp",
        "/opt/irangate/traffic/l2tp_current_stats.json",
        "/opt/irangate/traffic/l2tp_sessions.json",
        "/opt/irangate/traffic/l2tp_last_seen.json",
        "/usr/local/sbin/irangate-l2tp",
        "/usr/local/sbin/irangate-l2tp-stats-snapshot",
        "/usr/local/sbin/irangate-l2tp-firewall.sh",
        "/etc/ppp/ip-up.d/90-irangate-l2tp",
        "/etc/ppp/ip-down.d/90-irangate-l2tp",
        "/etc/systemd/system/irangate-l2tp-firewall.service",
        "/etc/systemd/system/irangate-l2tp-stats.service",
        "/etc/systemd/system/irangate-l2tp-stats.timer",
        "/usr/local/bin/irangate-pg-l2tp-sync.py",
        "/etc/systemd/system/irangate-pg-l2tp-sync.service",
        "/etc/systemd/system/irangate-pg-l2tp-sync.timer",
        "/opt/irangate/routing",
'''
    replace_once(path, old, new, "IRANGATE_L2TP_BACKUP_EXTRAS_V1")

    old = '''        + "contains=ikev2\\n"
        + "contains=outbound-routing\\n"
'''
    new = '''        + "contains=ikev2\\n"
        + "contains=l2tp\\n"
        + "contains=outbound-routing\\n"
'''
    replace_once(path, old, new, 'contains=l2tp')

    old = '''    sensitive_files = (
        root / "opt/irangate/ikev2/users.tsv",
        root / "etc/swanctl/private/irangate-ca-key.pem",
        root / "etc/swanctl/private/irangate-server-key.pem",
    )
'''
    new = '''    sensitive_files = (
        root / "opt/irangate/ikev2/users.tsv",
        root / "etc/swanctl/private/irangate-ca-key.pem",
        root / "etc/swanctl/private/irangate-server-key.pem",
        root / "opt/irangate/l2tp/users.tsv",
        root / "opt/irangate/l2tp/ipsec-psk",
        root / "opt/irangate/l2tp/stats_state.json",
    )
'''
    replace_once(path, old, new, 'root / "opt/irangate/l2tp/ipsec-psk"')


def patch_backup_import() -> None:
    path = ROOT / "deploy/master/scripts/irangate-backup-v2-import.py"

    old = '''PIPELINE_TIMERS = [
    "irangate-node-usage-sync.timer",
    "irangate-pg-usage-tick.timer",
    "irangate-pg-enforcer.timer",
    "irangate-pg-sync-clients.timer",
]
'''
    new = '''PIPELINE_TIMERS = [
    "irangate-node-usage-sync.timer",
    "irangate-pg-usage-tick.timer",
    "irangate-pg-enforcer.timer",
    "irangate-pg-sync-clients.timer",
    # IRANGATE_L2TP_RESTORE_PIPELINE_V1
    "irangate-pg-l2tp-sync.timer",
    "irangate-l2tp-stats.timer",
    "irangate-ikev2-stats.timer",
]
'''
    replace_once(path, old, new, "IRANGATE_L2TP_RESTORE_PIPELINE_V1")

    old = '''PIPELINE_SERVICES = [
    "irangate-node-usage-sync.service",
    "irangate-pg-usage-tick.service",
    "irangate-pg-enforcer.service",
    "irangate-pg-sync-clients.service",
]
'''
    new = '''PIPELINE_SERVICES = [
    "irangate-node-usage-sync.service",
    "irangate-pg-usage-tick.service",
    "irangate-pg-enforcer.service",
    "irangate-pg-sync-clients.service",
    "irangate-pg-l2tp-sync.service",
    "irangate-l2tp-stats.service",
    "irangate-ikev2-stats.service",
]
'''
    replace_once(path, old, new, '"irangate-pg-l2tp-sync.service"')

    anchor = '''\ndef post_restore_check() -> dict:\n'''
    block = r'''

# IRANGATE_RUNTIME_EXTRAS_RESTORE_V1
def verify_runtime_extras_checksums(root: Path) -> int:
    extras = root / "extras" / "runtime"

    if not extras.is_dir():
        return 0

    checksum_path = extras / "SHA256SUMS"

    if not checksum_path.is_file():
        fail("runtime extras checksum file is missing")

    checked = 0

    for raw in checksum_path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line:
            continue

        try:
            expected, relative = line.split("  ", 1)
        except ValueError:
            fail("invalid runtime extras checksum entry")

        rel = PurePosixPath(relative)
        if rel.is_absolute() or ".." in rel.parts:
            fail("unsafe runtime extras checksum path: " + relative)

        target = extras / relative
        ensure_inside(extras, target)

        if not target.is_file():
            fail("runtime extras checksum target missing: " + relative)

        actual = sha256_file(target)
        if not hmac.compare_digest(actual, expected):
            fail("runtime extras checksum mismatch: " + relative)

        checked += 1

    if checked == 0:
        fail("runtime extras checksum list is empty")

    return checked


def restore_integrated_runtime_extras(root: Path) -> dict:
    extras = root / "extras" / "runtime"

    if not extras.is_dir():
        return {
            "runtime_extras_present": False,
            "runtime_extras_restored": 0,
            "runtime_extras_checksums": 0,
        }

    checked = verify_runtime_extras_checksums(root)

    # Only explicitly allow IranGate-owned paths. Never recursively copy the
    # arbitrary filesystem tree represented by extras/runtime.
    tree_paths = (
        "opt/irangate/ikev2",
        "opt/irangate/l2tp",
        "opt/irangate/routing",
    )

    file_paths = (
        "etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf",
        "etc/swanctl/conf.d/irangate-ikev2-users.conf",
        "etc/swanctl/private/irangate-ca-key.pem",
        "etc/swanctl/private/irangate-server-key.pem",
        "etc/swanctl/x509ca/irangate-ca-cert.pem",
        "etc/swanctl/x509/irangate-server-cert.pem",
        "etc/sysctl.d/99-irangate-ikev2.conf",
        "usr/local/sbin/irangate-ikev2",
        "usr/local/sbin/irangate-ikev2-stats-snapshot",
        "usr/local/sbin/irangate-ikev2-firewall.sh",
        "etc/systemd/system/irangate-ikev2-firewall.service",
        "etc/systemd/system/irangate-ikev2-stats.service",
        "etc/systemd/system/irangate-ikev2-stats.timer",
        "etc/swanctl/conf.d/irangate-l2tp.conf",
        "etc/sysctl.d/99-irangate-l2tp.conf",
        "etc/xl2tpd/xl2tpd.conf",
        "etc/ppp/options.xl2tpd",
        "usr/local/sbin/irangate-l2tp",
        "usr/local/sbin/irangate-l2tp-stats-snapshot",
        "usr/local/sbin/irangate-l2tp-firewall.sh",
        "etc/ppp/ip-up.d/90-irangate-l2tp",
        "etc/ppp/ip-down.d/90-irangate-l2tp",
        "etc/systemd/system/irangate-l2tp-firewall.service",
        "etc/systemd/system/irangate-l2tp-stats.service",
        "etc/systemd/system/irangate-l2tp-stats.timer",
        "usr/local/bin/irangate-pg-l2tp-sync.py",
        "etc/systemd/system/irangate-pg-l2tp-sync.service",
        "etc/systemd/system/irangate-pg-l2tp-sync.timer",
        "etc/sing-box/irangate-routing.json",
        "usr/local/bin/irangate-routing",
        "usr/local/sbin/irangate-openvpn-port-locations",
        "etc/systemd/system/irangate-routing.service",
        "etc/systemd/system/irangate-routing-rules.service",
        "etc/systemd/system/irangate-openvpn-port-locations.service",
        "opt/irangate/panel-proxy.env",
        "etc/systemd/system/irangate-webpanel.service.d/proxy.conf",
    )

    restored: list[str] = []

    for relative in tree_paths:
        source = extras / relative
        if not source.is_dir():
            continue
        destination = Path("/") / relative
        atomic_restore_tree(source, destination)
        restored.append("/" + relative)

    for relative in file_paths:
        source = extras / relative
        if not source.is_file():
            continue
        destination = Path("/") / relative
        atomic_restore_file(source, destination)
        restored.append("/" + relative)

    for sensitive in (
        Path("/opt/irangate/ikev2/users.tsv"),
        Path("/etc/swanctl/private/irangate-ca-key.pem"),
        Path("/etc/swanctl/private/irangate-server-key.pem"),
        Path("/opt/irangate/l2tp/users.tsv"),
        Path("/opt/irangate/l2tp/ipsec-psk"),
        Path("/opt/irangate/l2tp/stats_state.json"),
        Path("/etc/swanctl/conf.d/irangate-l2tp.conf"),
        Path("/etc/ppp/options.xl2tpd"),
    ):
        if sensitive.is_file():
            sensitive.chmod(0o600)

    for executable in (
        Path("/usr/local/sbin/irangate-ikev2"),
        Path("/usr/local/sbin/irangate-ikev2-stats-snapshot"),
        Path("/usr/local/sbin/irangate-ikev2-firewall.sh"),
        Path("/usr/local/sbin/irangate-l2tp"),
        Path("/usr/local/sbin/irangate-l2tp-stats-snapshot"),
        Path("/usr/local/sbin/irangate-l2tp-firewall.sh"),
        Path("/etc/ppp/ip-up.d/90-irangate-l2tp"),
        Path("/etc/ppp/ip-down.d/90-irangate-l2tp"),
        Path("/usr/local/bin/irangate-pg-l2tp-sync.py"),
        Path("/usr/local/bin/irangate-routing"),
        Path("/usr/local/sbin/irangate-openvpn-port-locations"),
    ):
        if executable.is_file():
            executable.chmod(0o755)

    subprocess.run(
        ["systemctl", "daemon-reload"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )

    if command_exists("sysctl"):
        subprocess.run(
            ["sysctl", "--system"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )

    if command_exists("swanctl"):
        result = subprocess.run(
            ["swanctl", "--load-all", "--noprompt"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            log("warning: swanctl reload after extras restore failed: " + result.stdout.strip())

    l2tp_manager = Path("/usr/local/sbin/irangate-l2tp")
    if l2tp_manager.is_file():
        result = subprocess.run(
            [str(l2tp_manager), "reload"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            fail("failed to regenerate restored L2TP credentials: " + result.stdout.strip())

    for unit in (
        "xl2tpd.service",
        "irangate-l2tp-firewall.service",
        "irangate-ikev2-firewall.service",
    ):
        subprocess.run(
            ["systemctl", "try-restart", unit],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )

    return {
        "runtime_extras_present": True,
        "runtime_extras_restored": len(restored),
        "runtime_extras_checksums": checked,
        "runtime_extras_paths": restored,
    }


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None
'''
    replace_once(path, anchor, block + anchor, "IRANGATE_RUNTIME_EXTRAS_RESTORE_V1")

    old = '''    restore_filesystem(
        root
    )

    port_location_result = (
        reconcile_restored_openvpn_port_locations()
    )
'''
    new = '''    restore_filesystem(
        root
    )

    extras_result = restore_integrated_runtime_extras(
        root
    )

    port_location_result = (
        reconcile_restored_openvpn_port_locations()
    )
'''
    replace_once(path, old, new, "extras_result = restore_integrated_runtime_extras")

    old = '''    result = post_restore_check()
    result.update(connection_result)
    result.update(port_location_result)

    return result
'''
    new = '''    result = post_restore_check()
    result.update(connection_result)
    result.update(extras_result)
    result.update(port_location_result)

    return result
'''
    replace_once(path, old, new, "result.update(extras_result)")


def patch_reset_client() -> None:
    path = ROOT / "deploy/master/scripts/irangate-pg-reset-client.py"
    old = '''print("===== capture current baselines before reset =====")
subprocess.run(
    ["/usr/local/bin/irangate-pg-usage-tick.py", "--apply"],
    check=False
)

conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
'''
    new = '''print("===== capture current baselines before reset =====")
subprocess.run(
    ["/usr/local/bin/irangate-pg-usage-tick.py", "--apply"],
    check=False
)

# IRANGATE_L2TP_RESET_BASELINE_V1
# Snapshot and charge any bytes seen immediately before the reset so the
# L2TP cumulative source counter becomes the post-reset baseline rather than
# resurrecting pre-reset traffic on the next PostgreSQL sync.
for command_line in (
    ["/usr/local/sbin/irangate-l2tp-stats-snapshot", "--quiet"],
    ["/usr/local/bin/irangate-pg-l2tp-sync.py"],
):
    if not Path(command_line[0]).exists():
        continue
    try:
        subprocess.run(
            command_line,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=20,
        )
    except Exception as exc:
        print("L2TP BASELINE WARN", command_line[0], exc)

conn = psycopg2.connect(env["IRANGATE_DB_DSN"])
'''
    replace_once(path, old, new, "IRANGATE_L2TP_RESET_BASELINE_V1")


def patch_client_rework() -> None:
    path = ROOT / "webpanel/backend/client_rework.go"

    old = '''\t\t{
\t\t\tfilepath.Join(safetyDir, "users.conf"),
\t\t\t"/etc/swanctl/conf.d/irangate-ikev2-users.conf",
\t\t},
\t}
'''
    new = '''\t\t{
\t\t\tfilepath.Join(safetyDir, "users.conf"),
\t\t\t"/etc/swanctl/conf.d/irangate-ikev2-users.conf",
\t\t},
\t\t// IRANGATE_L2TP_REWORK_ROLLBACK_V1
\t\t{
\t\t\tfilepath.Join(safetyDir, "l2tp-users.tsv"),
\t\t\t"/opt/irangate/l2tp/users.tsv",
\t\t},
\t\t{
\t\t\tfilepath.Join(safetyDir, "chap-secrets"),
\t\t\t"/etc/ppp/chap-secrets",
\t\t},
\t}
'''
    replace_once(path, old, new, "IRANGATE_L2TP_REWORK_ROLLBACK_V1")

    old = '''\tif output, err := exec.Command("swanctl", "--load-creds").CombinedOutput(); err != nil {
\t\terrors = append(errors, strings.TrimSpace(string(output)))
\t}
\tif len(errors) > 0 {
'''
    new = '''\tif output, err := exec.Command("swanctl", "--load-creds").CombinedOutput(); err != nil {
\t\terrors = append(errors, strings.TrimSpace(string(output)))
\t}
\tif _, err := os.Stat("/usr/local/sbin/irangate-l2tp"); err == nil {
\t\tif output, reloadErr := exec.Command("/usr/local/sbin/irangate-l2tp", "reload").CombinedOutput(); reloadErr != nil {
\t\t\terrors = append(errors, "L2TP reload: "+strings.TrimSpace(string(output)))
\t\t}
\t}
\tif len(errors) > 0 {
'''
    replace_once(path, old, new, '"L2TP reload: "+strings.TrimSpace')

    old = '''\tfor source, destination := range map[string]string{
\t\t"/opt/irangate/ikev2/users.tsv":                 filepath.Join(ikeSafety, "users.tsv"),
\t\t"/etc/swanctl/conf.d/irangate-ikev2-users.conf": filepath.Join(ikeSafety, "users.conf"),
\t} {
'''
    new = '''\tfor source, destination := range map[string]string{
\t\t"/opt/irangate/ikev2/users.tsv":                 filepath.Join(ikeSafety, "users.tsv"),
\t\t"/etc/swanctl/conf.d/irangate-ikev2-users.conf": filepath.Join(ikeSafety, "users.conf"),
\t\t"/opt/irangate/l2tp/users.tsv":                  filepath.Join(ikeSafety, "l2tp-users.tsv"),
\t\t"/etc/ppp/chap-secrets":                         filepath.Join(ikeSafety, "chap-secrets"),
\t} {
'''
    replace_once(path, old, new, 'filepath.Join(ikeSafety, "l2tp-users.tsv")')

    old = '''\tnewInfo, err := ensureIKEv2Account(name, true)
\tif err != nil || (oldInfo.Available && oldInfo.Password == newInfo.Password) {
\t\tcause := err
\t\tif cause == nil {
\t\t\tcause = fmt.Errorf("IKEv2 password did not rotate")
\t\t}
\t\trollbackErr := clientReworkRestoreIKEv2(ikeSafety)
\t\treturn newInfo, fmt.Errorf(
\t\t\t"%w; rollback=%v; safety=%s",
\t\t\tcause,
\t\t\trollbackErr,
\t\t\tikeSafety,
\t\t)
\t}
\treturn newInfo, nil
'''
    new = '''\tnewInfo, err := ensureIKEv2Account(name, true)
\tvar cause error
\tif err != nil {
\t\tcause = err
\t} else if oldInfo.Available && oldInfo.Password == newInfo.Password {
\t\tcause = fmt.Errorf("IKEv2 password did not rotate")
\t}

\t// When the L2TP runtime is installed, successful rework is only complete
\t// after the shared IKEv2/L2TP password is verified on both protocols.
\tif cause == nil {
\t\tif _, statErr := os.Stat("/usr/local/sbin/irangate-l2tp"); statErr == nil {
\t\t\tl2tpInfo, l2tpErr := ensureL2TPAccount(name, newInfo.Password, true)
\t\t\tif l2tpErr != nil {
\t\t\t\tcause = fmt.Errorf("L2TP password synchronization failed: %w", l2tpErr)
\t\t\t} else if !l2tpInfo.Available || l2tpInfo.Password != newInfo.Password {
\t\t\t\tcause = fmt.Errorf("L2TP password synchronization verification failed")
\t\t\t}
\t\t}
\t}

\tif cause != nil {
\t\trollbackErr := clientReworkRestoreIKEv2(ikeSafety)
\t\treturn newInfo, fmt.Errorf(
\t\t\t"%w; rollback=%v; safety=%s",
\t\t\tcause,
\t\t\trollbackErr,
\t\t\tikeSafety,
\t\t)
\t}
\treturn newInfo, nil
'''
    replace_once(path, old, new, "L2TP password synchronization verification failed")


def patch_uninstall() -> None:
    path = ROOT / "uninstall.sh"
    old = '''log_info \\
    "Let's Encrypt certificates were intentionally preserved."


echo
echo '========================================'
echo '===== REMOVE SYSTEMD UNIT FILES ====='
'''
    new = '''log_info \\
    "Let's Encrypt certificates were intentionally preserved."


# IRANGATE_L2TP_UNINSTALL_V1
log_info "Removing IranGate-managed L2TP/IPSec runtime..."

if [ -x /usr/local/sbin/irangate-l2tp-firewall.sh ]; then
    /usr/local/sbin/irangate-l2tp-firewall.sh stop \\
        >/dev/null 2>&1 || true
fi

systemctl stop \\
    irangate-l2tp-stats.timer \\
    irangate-l2tp-stats.service \\
    irangate-l2tp-firewall.service \\
    irangate-pg-l2tp-sync.timer \\
    irangate-pg-l2tp-sync.service \\
    >/dev/null 2>&1 || true

# Remove only IranGate's managed chap-secrets block. Preserve unrelated PPP
# credentials that may belong to other software on the same host.
if [ -f /etc/ppp/chap-secrets ]; then
    python3 - <<'IRANGATE_L2TP_CHAP_CLEAN'
from pathlib import Path

path = Path("/etc/ppp/chap-secrets")
begin = "# BEGIN IRANGATE L2TP USERS"
end = "# END IRANGATE L2TP USERS"
lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
out = []
in_managed = False
for line in lines:
    if line.strip() == begin:
        in_managed = True
        continue
    if line.strip() == end:
        in_managed = False
        continue
    if not in_managed:
        out.append(line)
path.write_text("\\n".join(out) + ("\\n" if out else ""), encoding="utf-8")
path.chmod(0o600)
IRANGATE_L2TP_CHAP_CLEAN
fi

# Restore a pre-IranGate xl2tpd configuration when one was preserved by the
# installer. Otherwise remove only the configuration known to be IranGate's.
if [ -f /etc/xl2tpd/xl2tpd.conf.irangate-before-l2tp ]; then
    systemctl stop xl2tpd.service >/dev/null 2>&1 || true
    mv -f \\
        /etc/xl2tpd/xl2tpd.conf.irangate-before-l2tp \\
        /etc/xl2tpd/xl2tpd.conf
elif [ -f /etc/xl2tpd/xl2tpd.conf ] \\
    && grep -qF 'ip range = 10.20.0.10-10.20.0.250' /etc/xl2tpd/xl2tpd.conf
then
    systemctl stop xl2tpd.service >/dev/null 2>&1 || true
    rm -f /etc/xl2tpd/xl2tpd.conf
fi

rm -f \\
    /etc/swanctl/conf.d/irangate-l2tp.conf \\
    /etc/sysctl.d/99-irangate-l2tp.conf \\
    /etc/ppp/options.xl2tpd \\
    /etc/ppp/ip-up.d/90-irangate-l2tp \\
    /etc/ppp/ip-down.d/90-irangate-l2tp \\
    /usr/local/sbin/irangate-l2tp \\
    /usr/local/sbin/irangate-l2tp-stats-snapshot \\
    /usr/local/sbin/irangate-l2tp-firewall.sh \\
    /usr/local/bin/irangate-pg-l2tp-sync.py

rm -rf /run/irangate-l2tp

if command -v swanctl >/dev/null 2>&1; then
    swanctl --load-all --noprompt >/dev/null 2>&1 || true
fi

sysctl --system >/dev/null 2>&1 || true

log_ok "IranGate L2TP/IPSec runtime removed."


echo
echo '========================================'
echo '===== REMOVE SYSTEMD UNIT FILES ====='
'''
    replace_once(path, old, new, "IRANGATE_L2TP_UNINSTALL_V1")


def main() -> None:
    patch_apply_panel_extras()
    patch_backup_export()
    patch_backup_import()
    patch_reset_client()
    patch_client_rework()
    patch_uninstall()


if __name__ == "__main__":
    main()
