from pathlib import Path
import re
import subprocess

VERSION = "delegated-portal-20260911-v1"
ROOT = Path("webpanel/frontend")


def replace_once(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected 1 match, found {count}")
    return text.replace(old, new, 1)


# Allow delegated admins to open only their dedicated dashboard, clients and usage pages.
api = ROOT / "js/api.js"
text = api.read_text(encoding="utf-8")
old = """    const role = localStorage.getItem('role') || 'root';
    const currentPage = (window.location.pathname.split('/').pop() || '').split('?')[0];
    if (role === 'delegated_admin' && currentPage !== 'clients.html') {
        window.location.replace('clients.html');
        return false;
    }
"""
new = """    const role = localStorage.getItem('role') || 'root';
    const currentPage = (window.location.pathname.split('/').pop() || '').split('?')[0];
    if (role === 'delegated_admin') {
        const allowedPages = new Set(['dealer-dashboard.html', 'clients.html', 'usage.html']);
        if (!allowedPages.has(currentPage)) {
            window.location.replace('dealer-dashboard.html');
            return false;
        }
    }
"""
api.write_text(replace_once(text, old, new, "api auth"), encoding="utf-8")


# Delegated login/session lands on the dedicated dashboard.
index = ROOT / "index.html"
text = index.read_text(encoding="utf-8")
text = replace_once(
    text,
    "data.data.role === 'delegated_admin' ? 'clients.html' : 'dashboard.html'",
    "data.data.role === 'delegated_admin' ? 'dealer-dashboard.html' : 'dashboard.html'",
    "login redirect",
)
text = replace_once(
    text,
    "localStorage.getItem('role') === 'delegated_admin' ? 'clients.html' : 'dashboard.html'",
    "localStorage.getItem('role') === 'delegated_admin' ? 'dealer-dashboard.html' : 'dashboard.html'",
    "session redirect",
)
index.write_text(text, encoding="utf-8")


# The existing sidebar keeps root/admin UI unchanged. A delegated-only override is loaded
# on the three delegated portal pages, and api.js gets a fresh cache key there.
for name in ("dealer-dashboard.html", "clients.html", "usage.html"):
    page = ROOT / name
    text = page.read_text(encoding="utf-8")

    if "js/delegated-portal.js" not in text:
        match = re.search(r'<script src="js/sidebar\.js[^\"]*"></script>', text)
        if not match:
            raise SystemExit(f"{name}: sidebar.js script not found")
        insertion = (
            match.group(0)
            + f'\n    <script src="js/delegated-portal.js?v={VERSION}"></script>'
        )
        text = text[: match.start()] + insertion + text[match.end() :]

    text, count = re.subn(
        r'<script src="js/api\.js[^\"]*"></script>',
        f'<script src="js/api.js?v={VERSION}"></script>',
        text,
    )
    if count != 1:
        raise SystemExit(f"{name}: expected one api.js script, found {count}")

    page.write_text(text, encoding="utf-8")


# Static invariants.
api_text = api.read_text(encoding="utf-8")
index_text = index.read_text(encoding="utf-8")
assert "allowedPages = new Set(['dealer-dashboard.html', 'clients.html', 'usage.html'])" in api_text
assert "delegated_admin' ? 'dealer-dashboard.html'" in index_text

for name in ("dealer-dashboard.html", "clients.html", "usage.html"):
    text = (ROOT / name).read_text(encoding="utf-8")
    assert f"js/delegated-portal.js?v={VERSION}" in text
    assert f"js/api.js?v={VERSION}" in text

portal_js = (ROOT / "js/delegated-portal.js").read_text(encoding="utf-8")
for token in ("Dashboard", "Clients", "Usage"):
    assert token in portal_js

for token in ("Active Users", "Online Now", "traffic_remaining_bytes", "days_remaining"):
    assert token in (ROOT / "dealer-dashboard.html").read_text(encoding="utf-8")

assert "new Chart" in (ROOT / "usage.html").read_text(encoding="utf-8")

# JavaScript syntax checks.
subprocess.run(["node", "--check", str(ROOT / "js/api.js")], check=True)
subprocess.run(["node", "--check", str(ROOT / "js/delegated-portal.js")], check=True)

for name in ("dealer-dashboard.html", "usage.html"):
    text = (ROOT / name).read_text(encoding="utf-8")
    scripts = re.findall(r"<script(?:\s[^>]*)?>(.*?)</script>", text, re.S | re.I)
    temp = Path("/tmp") / f"{name}.js"
    temp.write_text("\n".join(s for s in scripts if s.strip()), encoding="utf-8")
    subprocess.run(["node", "--check", str(temp)], check=True)

print("Delegated portal integration validated")
