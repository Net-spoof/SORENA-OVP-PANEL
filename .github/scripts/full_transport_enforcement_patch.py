from pathlib import Path

path = Path("webpanel/backend/client_lifecycle.go")
text = path.read_text(encoding="utf-8")

old_stop = """\t// روی هر نود: block + kill
\tgo blockRemoteNodes(clientName)

\t// پاک‌کردن cache صفحه کاربران
"""

new_stop = """\t// روی هر نود: block + kill
\t// Keep this synchronous: lifecycle stop must not return before all
\t// configured remote nodes have received the block/kill request.
\tblockRemoteNodes(clientName)

\t// IKEv2 uses its own credential store, so OpenVPN block files are
\t// not sufficient. Block the account and terminate any active SA.
\tif out, err := ikev2Run(\"block\", clientName); err != nil {
\t\tlog.Printf(
\t\t\t\"Client %s IKEv2 block/terminate failed: %v (%s)\",
\t\t\tclientName,
\t\t\terr,
\t\t\tstrings.TrimSpace(out),
\t\t)
\t}

\t// پاک‌کردن cache صفحه کاربران
"""

count = text.count(old_stop)
if count != 1:
    raise SystemExit(f"expected exactly one lifecycle stop block, found {count}")
text = text.replace(old_stop, new_stop, 1)

marker = "func StartBlockedClientRemoteSync(\n"
helper = r'''func syncBlockedIKEv2Clients(blockedNames []string) {
	if len(blockedNames) == 0 {
		return
	}

	const usersDB = "/opt/irangate/ikev2/users.tsv"

	data, err := os.ReadFile(usersDB)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("IKEv2 blocked-client sync: read users database failed: %v", err)
		}
		return
	}

	active := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) < 4 {
			continue
		}

		name := strings.TrimSpace(parts[0])
		status := strings.TrimSpace(parts[3])

		if name != "" && strings.EqualFold(status, "active") {
			active[strings.ToLower(name)] = true
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("IKEv2 blocked-client sync: parse users database failed: %v", err)
		return
	}

	for _, blockedName := range blockedNames {
		blockedName = strings.TrimSpace(blockedName)
		if blockedName == "" || !active[strings.ToLower(blockedName)] {
			continue
		}

		out, err := ikev2Run("block", blockedName)
		if err != nil {
			log.Printf(
				"IKEv2 blocked-client sync failed for %s: %v (%s)",
				blockedName,
				err,
				strings.TrimSpace(out),
			)
			continue
		}

		log.Printf("Synced blocked client to IKEv2: %s", blockedName)
	}
}

'''

count = text.count(marker)
if count != 1:
    raise SystemExit(f"expected exactly one StartBlockedClientRemoteSync marker, found {count}")
text = text.replace(marker, helper + marker, 1)

old_sync = """\t\tif len(blockedNames) == 0 {
\t\t\treturn
\t\t}

\t\tfor _, node := range loadResetNodes() {
"""
new_sync = """\t\tif len(blockedNames) == 0 {
\t\t\treturn
\t\t}

\t\t// Keep IKEv2 access in parity with the same durable block set used
\t\t// by the master and remote OpenVPN nodes. This also repairs drift
\t\t// after restarts/restores or a transient IKEv2 block failure.
\t\tsyncBlockedIKEv2Clients(blockedNames)

\t\tfor _, node := range loadResetNodes() {
"""

count = text.count(old_sync)
if count != 1:
    raise SystemExit(f"expected exactly one blocked sync insertion point, found {count}")
text = text.replace(old_sync, new_sync, 1)

path.write_text(text, encoding="utf-8")
