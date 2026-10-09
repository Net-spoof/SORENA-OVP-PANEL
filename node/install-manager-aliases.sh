#!/usr/bin/env bash
# Safe alias-only update for existing SURENA/IranGate OpenVPN nodes.
# Does not reinstall Node Agent, OpenVPN, PKI, tokens, clients, or systemd units.
set -Eeuo pipefail

if [[ "$EUID" -ne 0 ]]; then
    echo "Run as root: sudo bash node/install-manager-aliases.sh" >&2
    exit 1
fi

manager="/usr/local/bin/ovp-node"
if [[ ! -f "$manager" || ! -x "$manager" ]]; then
    echo "Existing Node manager is missing: $manager" >&2
    echo "This command is for an already-installed node; it is not a Node installer." >&2
    exit 1
fi

# All paths must be free or already be symlinks. Refuse to replace
# arbitrary executables, directories, and unrelated files.
for alias_name in surena-node serena-node Serena-node; do
    alias_path="/usr/local/bin/$alias_name"
    if [[ -e "$alias_path" && ! -L "$alias_path" ]]; then
        echo "Refusing to replace non-symlink $alias_path" >&2
        exit 1
    fi
done

for alias_name in surena-node serena-node Serena-node; do
    alias_path="/usr/local/bin/$alias_name"
    ln -sfn "$manager" "$alias_path"
    [[ "$(readlink -f "$alias_path")" == "$manager" ]] || {
        echo "Alias verification failed: $alias_path" >&2
        exit 1
    }
done

echo "SURENA Node manager aliases installed:"
for alias_name in surena-node serena-node Serena-node; do
    printf '  %s -> %s\n' "$alias_name" "$(readlink "/usr/local/bin/$alias_name")"
done
echo "Legacy ovp-node command remains available."
echo "Existing node, VPN services, tokens, certificates and users unchanged."
