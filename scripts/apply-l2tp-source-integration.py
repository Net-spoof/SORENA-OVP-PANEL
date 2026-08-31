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


def patch_ikev2_manager() -> None:
    path = ROOT / "deploy/master/ikev2/irangate-ikev2"

    old = '''gen_pass() {
  openssl rand -base64 24 | tr -d '=+/' | cut -c1-22
}

render_users_conf() {'''
    new = '''gen_pass() {
  openssl rand -base64 24 | tr -d '=+/' | cut -c1-22
}

# IRANGATE_L2TP_SHARED_CREDENTIAL_SYNC_V1
l2tp_sync() {
  if [ ! -x /usr/local/sbin/irangate-l2tp ]; then
    return 0
  fi

  if ! /usr/local/sbin/irangate-l2tp "$@" >/dev/null 2>&1; then
    echo "WARNING: L2TP sync failed: $*" >&2
    return 1
  fi

  return 0
}

render_users_conf() {'''
    replace_once(path, old, new, "IRANGATE_L2TP_SHARED_CREDENTIAL_SYNC_V1")

    old = '''  if [ "$status" = "blocked" ]; then
    terminate_user "$user" >/dev/null 2>&1 || true
  fi

  echo "IKEv2 user status changed ✅ user=$user old=$old_status new=$status"
}'''
    new = '''  if [ "$status" = "blocked" ]; then
    terminate_user "$user" >/dev/null 2>&1 || true
    l2tp_sync block "$user" || true
  else
    l2tp_sync unblock "$user" || true
  fi

  echo "IKEv2 user status changed ✅ user=$user old=$old_status new=$status"
}'''
    replace_once(path, old, new, "l2tp_sync block \"$user\"")

    old = '''  render_users_conf
  reload_swanctl

  echo "IKEv2 user created ✅"
  echo
  show_config "$user"
}'''
    new = '''  render_users_conf
  reload_swanctl

  # Keep IKEv2 and L2TP on exactly the same username/password.
  l2tp_sync add "$user" "$pass" || true

  echo "IKEv2 user created ✅"
  echo
  show_config "$user"
}'''
    replace_once(path, old, new, "l2tp_sync add \"$user\" \"$pass\"")

    old = '''  render_users_conf
  reload_swanctl

  echo "IKEv2 user deleted ✅ $user"
}'''
    new = '''  render_users_conf
  reload_swanctl
  l2tp_sync del "$user" || true

  echo "IKEv2 user deleted ✅ $user"
}'''
    replace_once(path, old, new, "l2tp_sync del \"$user\"")


def patch_enforcer() -> None:
    path = ROOT / "deploy/master/scripts/irangate-pg-enforcer.py"

    old = '''def ikev2_terminate(name):
    ok, msg = ikev2_cmd("terminate", name)
    print(f"IKEV2 TERMINATE {name}: {ok}")
    return ok, msg


def local_unblock(name):'''
    new = '''def ikev2_terminate(name):
    ok, msg = ikev2_cmd("terminate", name)
    print(f"IKEV2 TERMINATE {name}: {ok}")
    return ok, msg


# IRANGATE_L2TP_ENFORCER_V1
def l2tp_cmd(*args):
    cmd = ["/usr/local/sbin/irangate-l2tp"] + list(args)

    if not Path(cmd[0]).exists():
        return False, "L2TP runtime not installed"

    try:
        r = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=12,
        )
    except Exception as e:
        msg = f"L2TP CMD EXCEPTION {' '.join(cmd)}: {e}"
        print(msg)
        return False, msg

    out = ((r.stdout or "") + (r.stderr or "")).strip()
    if out:
        print(out[:1000])
    return r.returncode == 0, out[:1000]


def l2tp_block(name):
    ok, msg = l2tp_cmd("block", name)
    print(f"L2TP BLOCK {name}: {ok}")
    return ok, msg


def l2tp_unblock(name):
    ok, msg = l2tp_cmd("unblock", name)
    print(f"L2TP UNBLOCK {name}: {ok}")
    return ok, msg


def l2tp_terminate_session(session_fingerprint):
    ok, msg = l2tp_cmd("terminate-session", session_fingerprint)
    print(f"L2TP TERMINATE SESSION {session_fingerprint}: {ok}")
    return ok, msg


def local_unblock(name):'''
    replace_once(path, old, new, "IRANGATE_L2TP_ENFORCER_V1")

    old = '''def unblock_everywhere(name):
    results = {"local": None, "ikev2": None, "nodes": []}

    try:
        results["local"] = local_unblock(name)
    except Exception as e:
        print(f"LOCAL UNBLOCK FAIL {name}: {e}")
        results["local"] = False

    try:
        ok, msg = ikev2_unblock(name)
        results["ikev2"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"IKEV2 UNBLOCK FAIL {name}: {e}")
        results["ikev2"] = {"ok": False, "message": str(e)}

    for node in get_nodes():'''
    new = '''def unblock_everywhere(name):
    results = {"local": None, "ikev2": None, "l2tp": None, "nodes": []}

    try:
        results["local"] = local_unblock(name)
    except Exception as e:
        print(f"LOCAL UNBLOCK FAIL {name}: {e}")
        results["local"] = False

    try:
        ok, msg = ikev2_unblock(name)
        results["ikev2"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"IKEV2 UNBLOCK FAIL {name}: {e}")
        results["ikev2"] = {"ok": False, "message": str(e)}

    try:
        ok, msg = l2tp_unblock(name)
        results["l2tp"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"L2TP UNBLOCK FAIL {name}: {e}")
        results["l2tp"] = {"ok": False, "message": str(e)}

    for node in get_nodes():'''
    replace_once(path, old, new, 'results = {"local": None, "ikev2": None, "l2tp": None')

    old = '''def block_everywhere(name):
    results = {"local": None, "ikev2": None, "nodes": []}

    try:
        results["local"] = local_block(name)
    except Exception as e:
        print(f"LOCAL BLOCK FAIL {name}: {e}")
        results["local"] = False

    try:
        ok, msg = ikev2_block(name)
        results["ikev2"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"IKEV2 BLOCK FAIL {name}: {e}")
        results["ikev2"] = {"ok": False, "message": str(e)}

    for node in get_nodes():'''
    new = '''def block_everywhere(name):
    results = {"local": None, "ikev2": None, "l2tp": None, "nodes": []}

    try:
        results["local"] = local_block(name)
    except Exception as e:
        print(f"LOCAL BLOCK FAIL {name}: {e}")
        results["local"] = False

    try:
        ok, msg = ikev2_block(name)
        results["ikev2"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"IKEV2 BLOCK FAIL {name}: {e}")
        results["ikev2"] = {"ok": False, "message": str(e)}

    try:
        ok, msg = l2tp_block(name)
        results["l2tp"] = {"ok": ok, "message": msg}
    except Exception as e:
        print(f"L2TP BLOCK FAIL {name}: {e}")
        results["l2tp"] = {"ok": False, "message": str(e)}

    for node in get_nodes():'''
    replace_once(path, old, new, 'def block_everywhere(name):\n    results = {"local": None, "ikev2": None, "l2tp": None')

    old = '''                elif str(source_id) == "ikev2":
                    ok, msg = ikev2_terminate(client_name)
                    result["action"] = "ikev2_terminate"
                    result["ok"] = bool(ok)
                    result["message"] = msg

                    if ok:
                        cur.execute("""
                            DELETE FROM active_sessions
                            WHERE source_id = %s
                              AND client_name = %s
                              AND session_fingerprint = %s
                        """, (source_id, client_name, session_fp))

                else:'''
    new = '''                elif str(source_id) == "ikev2":
                    ok, msg = ikev2_terminate(client_name)
                    result["action"] = "ikev2_terminate"
                    result["ok"] = bool(ok)
                    result["message"] = msg

                    if ok:
                        cur.execute("""
                            DELETE FROM active_sessions
                            WHERE source_id = %s
                              AND client_name = %s
                              AND session_fingerprint = %s
                        """, (source_id, client_name, session_fp))

                elif str(source_id) == "l2tp":
                    ok, msg = l2tp_terminate_session(session_fp)
                    result["action"] = "l2tp_terminate_session"
                    result["ok"] = bool(ok)
                    result["message"] = msg

                    if ok:
                        cur.execute("""
                            DELETE FROM active_sessions
                            WHERE source_id = %s
                              AND client_name = %s
                              AND session_fingerprint = %s
                        """, (source_id, client_name, session_fp))

                else:'''
    replace_once(path, old, new, 'result["action"] = "l2tp_terminate_session"')


def patch_reset_client() -> None:
    path = ROOT / "deploy/master/scripts/irangate-pg-reset-client.py"
    old = '''local_unblock(name)
node_unblock(name)

print("===== refresh mirror after reset =====")'''
    new = '''local_unblock(name)
node_unblock(name)

# IRANGATE_L2TP_RESET_UNBLOCK_V1
for manager in (
    "/usr/local/sbin/irangate-ikev2",
    "/usr/local/sbin/irangate-l2tp",
):
    try:
        subprocess.run(
            [manager, "unblock", name],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=12,
        )
    except Exception as exc:
        print("VPN UNBLOCK WARN", manager, name, exc)

print("===== refresh mirror after reset =====")'''
    replace_once(path, old, new, "IRANGATE_L2TP_RESET_UNBLOCK_V1")


def patch_api() -> None:
    path = ROOT / "webpanel/backend/api.go"

    old = '''\t} else {
\t\tikev2Result = map[string]interface{}{
\t\t\t"enabled":    true,
\t\t\t"type":       "IKEv2/IPSec MSCHAPv2",
\t\t\t"server":     ikev2Cfg.Server,
\t\t\t"identifier": ikev2Cfg.Identifier,
\t\t\t"username":   ikev2Cfg.Username,
\t\t\t"password":   ikev2Cfg.Password,
\t\t}
\t}

\tnodeResults := []map[string]interface{}{}'''
    new = '''\t} else {
\t\tikev2Result = map[string]interface{}{
\t\t\t"enabled":    true,
\t\t\t"type":       "IKEv2/IPSec MSCHAPv2",
\t\t\t"server":     ikev2Cfg.Server,
\t\t\t"identifier": ikev2Cfg.Identifier,
\t\t\t"username":   ikev2Cfg.Username,
\t\t\t"password":   ikev2Cfg.Password,
\t\t}
\t}

\t// IRANGATE_L2TP_CREATE_RESPONSE_V1
\tl2tpResult := map[string]interface{}{
\t\t"enabled": false,
\t}
\tif ikev2Err == nil && ikev2Cfg.Available {
\t\tl2tpCfg, l2tpErr := ensureL2TPAccount(req.Name, ikev2Cfg.Password, true)
\t\tif l2tpErr != nil {
\t\t\tlog.Printf("L2TP create failed for %s: %v", req.Name, l2tpErr)
\t\t\tl2tpResult["error"] = l2tpErr.Error()
\t\t} else {
\t\t\tl2tpResult = map[string]interface{}{
\t\t\t\t"enabled":   true,
\t\t\t\t"type":      "L2TP/IPSec PSK",
\t\t\t\t"server":    l2tpCfg.Server,
\t\t\t\t"ipsec_psk": l2tpCfg.IPSecPSK,
\t\t\t\t"username":  l2tpCfg.Username,
\t\t\t\t"password":  l2tpCfg.Password,
\t\t\t}
\t\t}
\t} else {
\t\tl2tpResult["error"] = "shared IKEv2 credential is unavailable"
\t}

\tnodeResults := []map[string]interface{}{}'''
    replace_once(path, old, new, "IRANGATE_L2TP_CREATE_RESPONSE_V1")

    old = '''\t\t"openvpn_port_locations": selectedPortLocationIDs,
\t\t"ikev2":                  ikev2Result,
\t})'''
    new = '''\t\t"openvpn_port_locations": selectedPortLocationIDs,
\t\t"ikev2":                  ikev2Result,
\t\t"l2tp":                   l2tpResult,
\t})'''
    replace_once(path, old, new, '"l2tp":                   l2tpResult')

    old = '''\tif output, err := exec.Command("/usr/local/sbin/irangate-ikev2", "del", name).CombinedOutput(); err != nil {
\t\tlog.Printf("IKEv2 delete failed for %s: %v output=%s", name, err, string(output))
\t}

\tif err :='''
    new = '''\tif output, err := exec.Command("/usr/local/sbin/irangate-ikev2", "del", name).CombinedOutput(); err != nil {
\t\tlog.Printf("IKEv2 delete failed for %s: %v output=%s", name, err, string(output))
\t}

\tif output, err := exec.Command("/usr/local/sbin/irangate-l2tp", "del", name).CombinedOutput(); err != nil {
\t\tlog.Printf("L2TP delete failed for %s: %v output=%s", name, err, string(output))
\t}

\tif err :='''
    replace_once(path, old, new, 'log.Printf("L2TP delete failed')

    old = '''\tisOnline := presence.Online
\tif subIKEv2Online(clientName) {
\t\tisOnline = true
\t}
'''
    new = '''\tisOnline := presence.Online
\tif subIKEv2Online(clientName) || subL2TPOnline(clientName) {
\t\tisOnline = true
\t}
'''
    replace_once(path, old, new, "subIKEv2Online(clientName) || subL2TPOnline(clientName)")

    old = '''\tikev2DownloadURL := "/sub/" +
\t\ttemplate.URLQueryEscaper(token) +
\t\t"/ikev2.txt?v=" +
\t\tdownloadCacheVersion

\tmasterName := "Germany Master"'''
    new = '''\tikev2DownloadURL := "/sub/" +
\t\ttemplate.URLQueryEscaper(token) +
\t\t"/ikev2.txt?v=" +
\t\tdownloadCacheVersion

\t// IRANGATE_L2TP_SUBSCRIPTION_V1
\tl2tpCfg := L2TPSubscriptionInfo{}
\tif masterAllowed && ikev2Cfg.Available {
\t\tl2tpCfg, _ = ensureL2TPAccount(clientName, ikev2Cfg.Password, true)
\t}
\tif !masterAllowed {
\t\tl2tpCfg.Available = false
\t}
\tl2tpDownloadURL := "/sub/" +
\t\ttemplate.URLQueryEscaper(token) +
\t\t"/l2tp.txt?v=" +
\t\tdownloadCacheVersion

\tmasterName := "Germany Master"'''
    replace_once(path, old, new, "IRANGATE_L2TP_SUBSCRIPTION_V1")

    old = '''\t\t"IKEv2":                 ikev2Cfg,
\t\t"IKEv2DownloadURL":      ikev2DownloadURL,
\t}'''
    new = '''\t\t"IKEv2":                 ikev2Cfg,
\t\t"IKEv2DownloadURL":      ikev2DownloadURL,
\t\t"L2TP":                  l2tpCfg,
\t\t"L2TPDownloadURL":       l2tpDownloadURL,
\t}'''
    replace_once(path, old, new, '"L2TPDownloadURL":       l2tpDownloadURL')

    old = '''{{end}}

<div class="section-title">OpenVPN</div>'''
    new = '''{{end}}

{{if .L2TP.Available}}
    <div class="ike-card">
      <div class="ike-head">
        <div>
          <div class="ike-title"><span class="badge">L2TP</span> L2TP/IPSec</div>
          <div class="ike-sub">اتصال داخلی سیستم‌عامل با همان نام کاربری و رمز IKEv2</div>
        </div>
        <a class="ike-btn primary fresh-download" href="{{.L2TPDownloadURL}}">⬇️ دانلود اطلاعات L2TP</a>
      </div>

      <div class="ike-body">
        <div class="ike-grid">
          <div class="ike-item">
            <div class="ike-label">نوع اتصال</div>
            <div class="ike-value fa">L2TP/IPSec PSK</div>
          </div>
          <div class="ike-item">
            <div class="ike-label">Server</div>
            <div class="ike-value">{{.L2TP.Server}}</div>
          </div>
          <div class="ike-item">
            <div class="ike-label">IPSec PSK</div>
            <div class="ike-value ike-password">{{.L2TP.IPSecPSK}}</div>
          </div>
          <div class="ike-item">
            <div class="ike-label">Username</div>
            <div class="ike-value">{{.L2TP.Username}}</div>
          </div>
          <div class="ike-item">
            <div class="ike-label">Password</div>
            <div class="ike-value ike-password">{{.L2TP.Password}}</div>
          </div>
        </div>

        <div class="ike-actions">
          <button class="ike-btn" type="button" data-copy="{{.L2TP.Server}}" onclick="copyIKEValue(this)">کپی Server</button>
          <button class="ike-btn" type="button" data-copy="{{.L2TP.IPSecPSK}}" onclick="copyIKEValue(this)">کپی PSK</button>
          <button class="ike-btn" type="button" data-copy="{{.L2TP.Username}}" onclick="copyIKEValue(this)">کپی Username</button>
          <button class="ike-btn" type="button" data-copy="{{.L2TP.Password}}" onclick="copyIKEValue(this)">کپی Password</button>
        </div>

        <div class="ike-note">حجم و محدودیت تعداد اتصال بین OpenVPN، IKEv2 و L2TP مشترک است.</div>
      </div>
    </div>
{{end}}

<div class="section-title">OpenVPN</div>'''
    replace_once(path, old, new, "حجم و محدودیت تعداد اتصال بین OpenVPN")

    old = '''\tr.HandleFunc("/sub/{token}", subscriptionPageHandler).Methods("GET")
\tr.HandleFunc("/sub/{token}/ikev2.txt", subscriptionIKEv2DownloadHandler).Methods("GET")
\tr.HandleFunc("/sub/{token}/download", subscriptionDownloadHandler).Methods("GET")'''
    new = '''\tr.HandleFunc("/sub/{token}", subscriptionPageHandler).Methods("GET")
\tr.HandleFunc("/sub/{token}/ikev2.txt", subscriptionIKEv2DownloadHandler).Methods("GET")
\tr.HandleFunc("/sub/{token}/l2tp.txt", subscriptionL2TPDownloadHandler).Methods("GET")
\tr.HandleFunc("/sub/{token}/download", subscriptionDownloadHandler).Methods("GET")'''
    replace_once(path, old, new, '"/sub/{token}/l2tp.txt"')


def main() -> None:
    patch_ikev2_manager()
    patch_enforcer()
    patch_reset_client()
    patch_api()


if __name__ == "__main__":
    main()
