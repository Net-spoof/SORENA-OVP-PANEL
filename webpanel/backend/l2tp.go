package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/mux"
)

type L2TPSubscriptionInfo struct {
	Available bool
	Server    string
	IPSecPSK  string
	Username  string
	Password  string
	Error     string
}

func l2tpRun(args ...string) (string, error) {
	if _, err := os.Stat("/usr/local/sbin/irangate-l2tp"); err != nil {
		return "", fmt.Errorf("L2TP runtime is not installed: %w", err)
	}

	cmd := exec.Command("/usr/local/sbin/irangate-l2tp", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func parseL2TPAccountOutput(out string) L2TPSubscriptionInfo {
	info := L2TPSubscriptionInfo{}

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "server":
			info.Server = value
		case "ipsec psk":
			info.IPSecPSK = value
		case "username":
			info.Username = value
		case "password":
			info.Password = value
		}
	}

	info.Available = info.Server != "" &&
		info.IPSecPSK != "" &&
		info.Username != "" &&
		info.Password != ""

	return info
}

func ensureL2TPAccount(
	clientName string,
	sharedPassword string,
	create bool,
) (L2TPSubscriptionInfo, error) {
	clientName = strings.TrimSpace(clientName)
	sharedPassword = strings.TrimSpace(sharedPassword)

	if clientName == "" {
		return L2TPSubscriptionInfo{}, fmt.Errorf("empty L2TP username")
	}

	showOutput, showErr := l2tpRun("show", clientName)
	if showErr == nil {
		info := parseL2TPAccountOutput(showOutput)
		if info.Available {
			if sharedPassword != "" && info.Password != sharedPassword {
				if !create {
					return info, fmt.Errorf("L2TP password is not synchronized with IKEv2")
				}

				addOutput, addErr := l2tpRun("add", clientName, sharedPassword)
				if addErr != nil {
					return L2TPSubscriptionInfo{}, fmt.Errorf(
						"synchronize L2TP password: %w; output=%s",
						addErr,
						strings.TrimSpace(addOutput),
					)
				}

				showOutput, showErr = l2tpRun("show", clientName)
				if showErr != nil {
					return L2TPSubscriptionInfo{}, fmt.Errorf(
						"read synchronized L2TP account: %w; output=%s",
						showErr,
						strings.TrimSpace(showOutput),
					)
				}
				info = parseL2TPAccountOutput(showOutput)
			}

			if info.Available {
				return info, nil
			}
		}
	}

	if !create {
		return L2TPSubscriptionInfo{}, showErr
	}

	if sharedPassword == "" {
		return L2TPSubscriptionInfo{}, fmt.Errorf(
			"cannot create L2TP account without the shared IKEv2 password",
		)
	}

	addOutput, addErr := l2tpRun("add", clientName, sharedPassword)
	if addErr != nil {
		return L2TPSubscriptionInfo{}, fmt.Errorf(
			"create L2TP account: %w; output=%s",
			addErr,
			strings.TrimSpace(addOutput),
		)
	}

	showOutput, showErr = l2tpRun("show", clientName)
	if showErr != nil {
		return L2TPSubscriptionInfo{}, fmt.Errorf(
			"read created L2TP account: %w; output=%s",
			showErr,
			strings.TrimSpace(showOutput),
		)
	}

	info := parseL2TPAccountOutput(showOutput)
	if !info.Available {
		return info, fmt.Errorf("L2TP account output is incomplete")
	}
	if info.Password != sharedPassword {
		return info, fmt.Errorf("L2TP password synchronization verification failed")
	}

	return info, nil
}

func l2tpInfoText(clientName string, info L2TPSubscriptionInfo) string {
	return fmt.Sprintf(
		"IranGate L2TP/IPSec\n\nClient: %s\nType: L2TP/IPSec PSK\nServer: %s\nIPSec PSK: %s\nUsername: %s\nPassword: %s\n\nThe L2TP username/password is shared with IKEv2.\n",
		clientName,
		info.Server,
		info.IPSecPSK,
		info.Username,
		info.Password,
	)
}

func subL2TPOnline(clientName string) bool {
	raw, err := os.ReadFile("/opt/irangate/traffic/l2tp_sessions.json")
	if err != nil {
		return false
	}

	var rows []map[string]interface{}
	if json.Unmarshal(raw, &rows) != nil {
		return false
	}

	for _, row := range rows {
		name := strings.TrimSpace(fmt.Sprintf("%v", row["client_name"]))
		connected, _ := row["connected"].(bool)
		if strings.EqualFold(name, clientName) && connected {
			return true
		}
	}

	return false
}

func subscriptionL2TPDownloadHandler(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]

	clientName, clientData, err := findClientBySubscriptionToken(token)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	if !clientHasNode(clientData, "local") {
		http.Error(w, "Node access denied", http.StatusForbidden)
		return
	}

	ikeInfo, err := ensureIKEv2Account(clientName, true)
	if err != nil || !ikeInfo.Available {
		msg := "IKEv2 shared credential is not ready"
		if err != nil {
			msg += ": " + err.Error()
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	info, err := ensureL2TPAccount(clientName, ikeInfo.Password, true)
	if err != nil || !info.Available {
		msg := "L2TP config is not ready"
		if err != nil {
			msg += ": " + err.Error()
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	safeName := strings.NewReplacer(
		"\\", "_",
		"\"", "_",
		"\r", "_",
		"\n", "_",
	).Replace(clientName)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%s-l2tp.txt\"", safeName),
	)
	_, _ = w.Write([]byte(l2tpInfoText(clientName, info)))
}
