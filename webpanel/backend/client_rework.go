package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

var clientReworkLock sync.Mutex
var clientReworkName = regexp.MustCompile(`^[A-Za-z0-9_.@+-]{1,128}$`)

type clientReworkNodeResult struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Location    string `json:"location,omitempty"`
	Success     bool   `json:"success"`
	Quarantined bool   `json:"quarantined,omitempty"`
	Error       string `json:"error,omitempty"`
}

type clientReworkResponse struct {
	Name                   string                   `json:"name"`
	SubscriptionToken      string                   `json:"subscription_token"`
	SubscriptionURL        string                   `json:"subscription_url"`
	OldSubscriptionRevoked bool                     `json:"old_subscription_revoked"`
	MasterRotated          bool                     `json:"master_profile_rotated"`
	MasterOldSerial        string                   `json:"master_old_serial,omitempty"`
	MasterNewSerial        string                   `json:"master_new_serial,omitempty"`
	Nodes                  []clientReworkNodeResult `json:"nodes"`
	FailedNodes            []string                 `json:"failed_nodes"`
	IKEv2Rotated           bool                     `json:"ikev2_rotated"`
	IKEv2                  IKEv2SubscriptionInfo    `json:"ikev2,omitempty"`
	IKEv2Error             string                   `json:"ikev2_error,omitempty"`
	SafetyDirectory        string                   `json:"safety_directory,omitempty"`
	ReworkedAt             string                   `json:"reworked_at"`
	Partial                bool                     `json:"partial"`
}

func clientReworkRun(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return text, fmt.Errorf(
			"%s %s failed: %w; output=%s",
			name,
			strings.Join(args, " "),
			err,
			text,
		)
	}
	return text, nil
}

func clientReworkCopy(source, destination string) error {
	if _, err := os.Lstat(source); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	output, err := exec.Command("cp", "-a", source, destination).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"copy %s to %s failed: %w; output=%s",
			source,
			destination,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func clientReworkMasterProfile(name string) (string, error) {
	for _, candidate := range []string{
		filepath.Join("/opt/irangate/clients", name+".ovpn"),
		filepath.Join("/etc/openvpn/clients", name+".ovpn"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("OpenVPN profile not found for %s", name)
}

func clientReworkServerConfig() (string, error) {
	for _, candidate := range []string{
		"/etc/openvpn/server.conf",
		"/etc/openvpn/server/server.conf",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("OpenVPN server configuration not found")
}

func clientReworkSerial(name string) string {
	cert := filepath.Join(
		"/etc/openvpn/easy-rsa/pki/issued",
		name+".crt",
	)
	output, err := exec.Command(
		"openssl",
		"x509",
		"-in",
		cert,
		"-noout",
		"-serial",
	).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(string(output), "serial="))
}

func clientReworkEnsureCRL() error {
	configPath, err := clientReworkServerConfig()
	if err != nil {
		return err
	}

	crlSource := "/etc/openvpn/easy-rsa/pki/crl.pem"
	crlRuntime := "/etc/openvpn/crl.pem"

	if _, err := os.Stat(crlSource); err != nil {
		return fmt.Errorf("generated CRL is missing: %w", err)
	}

	if err := clientReworkCopy(crlSource, crlRuntime); err != nil {
		return err
	}
	if err := os.Chmod(crlRuntime, 0644); err != nil {
		return err
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "crl-verify ") {
			found = true
			if trimmed != "crl-verify "+crlRuntime {
				return fmt.Errorf("unexpected crl-verify directive: %s", trimmed)
			}
		}
	}

	if !found {
		content := strings.TrimRight(string(raw), "\n") +
			"\ncrl-verify " + crlRuntime + "\n"
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	for _, service := range []string{
		"openvpn@server.service",
		"openvpn@server",
	} {
		if exec.Command("systemctl", "reload", service).Run() == nil {
			if exec.Command("systemctl", "is-active", "--quiet", service).Run() == nil {
				return nil
			}
		}
		if exec.Command("systemctl", "restart", service).Run() == nil {
			if exec.Command("systemctl", "is-active", "--quiet", service).Run() == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("OpenVPN master reload/restart failed")
}

func clientReworkWriteJSON(path string, data map[string]interface{}) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".rework.tmp"
	if err := os.WriteFile(temporary, encoded, 0644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func clientReworkRestoreMetadata(
	clientPath string,
	oldData map[string]interface{},
	token string,
	reworkedAt string,
) error {
	generatedRaw, err := os.ReadFile(clientPath)
	if err != nil {
		return err
	}

	var generated map[string]interface{}
	if err := json.Unmarshal(generatedRaw, &generated); err != nil {
		return err
	}

	preserved := []string{
		"email",
		"expires_at",
		"traffic_limit_bytes",
		"traffic_used_bytes",
		"bytes_sent",
		"bytes_received",
		"upload",
		"download",
		"data_used_mb",
		"connection_limit",
		"nodes",
		"quota_enabled",
		"quota_reset_date",
		"active",
		"status",
		"created_at",
		"last_connection",
		"last_seen",
	}

	for _, key := range preserved {
		if value, exists := oldData[key]; exists {
			generated[key] = value
		}
	}

	generated["subscription_token"] = token
	generated["last_reworked_at"] = reworkedAt
	generated["credential_generation"] = time.Now().UTC().UnixNano()
	generated["last_update"] = reworkedAt

	return clientReworkWriteJSON(clientPath, generated)
}

func clientReworkMaster(
	name string,
	clientPath string,
	oldData map[string]interface{},
	newToken string,
	reworkedAt string,
) (string, string, string, error) {
	profilePath, err := clientReworkMasterProfile(name)
	if err != nil {
		return "", "", "", err
	}

	oldSerial := clientReworkSerial(name)
	safetyDir := filepath.Join(
		"/opt/irangate/rework-safety",
		name,
		time.Now().UTC().Format("20060102T150405Z"),
	)

	if err := os.MkdirAll(safetyDir, 0700); err != nil {
		return "", "", "", err
	}

	if err := clientReworkCopy(
		"/etc/openvpn/easy-rsa/pki",
		filepath.Join(safetyDir, "pki"),
	); err != nil {
		return "", "", "", err
	}
	if err := clientReworkCopy(
		profilePath,
		filepath.Join(safetyDir, "client.ovpn"),
	); err != nil {
		return "", "", "", err
	}
	if err := clientReworkCopy(
		clientPath,
		filepath.Join(safetyDir, "client.json"),
	); err != nil {
		return "", "", "", err
	}

	if _, err := clientReworkRun(
		"irangate",
		"client",
		"remove",
		name,
	); err != nil {
		return "", "", safetyDir, err
	}

	if _, err := clientReworkRun(
		"irangate",
		"client",
		"add",
		name,
	); err != nil {
		return "", "", safetyDir, fmt.Errorf(
			"new Master identity creation failed after revocation: %w; safety=%s",
			err,
			safetyDir,
		)
	}

	if err := clientReworkRestoreMetadata(
		clientPath,
		oldData,
		newToken,
		reworkedAt,
	); err != nil {
		return "", "", safetyDir, err
	}

	if err := clientReworkEnsureCRL(); err != nil {
		return "", "", safetyDir, err
	}

	killLocalOpenVPNClient(name)

	newSerial := clientReworkSerial(name)
	if oldSerial == "" || newSerial == "" || oldSerial == newSerial {
		return "", "", safetyDir, fmt.Errorf(
			"Master certificate serial did not rotate",
		)
	}

	return oldSerial, newSerial, safetyDir, nil
}

func clientReworkNode(node NodeConfig, name string) error {
	payload, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}

	request, err := http.NewRequest(
		http.MethodPost,
		strings.TrimRight(node.AgentURL, "/")+"/clients/rework",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")
	if node.Token != "" {
		request.Header.Set("Authorization", "Bearer "+node.Token)
	}

	response, err := (&http.Client{Timeout: 150 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf(
			"node %s rework failed: HTTP %d %s",
			node.ID,
			response.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var parsed map[string]interface{}
	if json.Unmarshal(body, &parsed) == nil {
		if success, ok := parsed["success"].(bool); ok && !success {
			return fmt.Errorf(
				"node %s rework failed: %v",
				node.ID,
				parsed["message"],
			)
		}
	}

	return nil
}

func clientReworkIKEv2(name string) (IKEv2SubscriptionInfo, error) {
	oldInfo, _ := ensureIKEv2Account(name, false)

	deleteOutput, deleteErr := ikev2Run("del", name)
	if deleteErr != nil && oldInfo.Available {
		return IKEv2SubscriptionInfo{}, fmt.Errorf(
			"IKEv2 delete failed: %v; output=%s",
			deleteErr,
			strings.TrimSpace(deleteOutput),
		)
	}

	newInfo, err := ensureIKEv2Account(name, true)
	if err != nil {
		return newInfo, err
	}

	if oldInfo.Available &&
		oldInfo.Password != "" &&
		oldInfo.Password == newInfo.Password {
		return newInfo, fmt.Errorf("IKEv2 password did not rotate")
	}

	return newInfo, nil
}

func reworkClientHandler(w http.ResponseWriter, r *http.Request) {
	clientReworkLock.Lock()
	defer clientReworkLock.Unlock()

	name := strings.TrimSpace(mux.Vars(r)["name"])
	if !clientReworkName.MatchString(name) {
		sendError(w, "Invalid client name", http.StatusBadRequest)
		return
	}

	clientPath := filepath.Join(
		DataBaseDir,
		"database",
		"clients",
		name+".json",
	)

	raw, err := os.ReadFile(clientPath)
	if err != nil {
		if os.IsNotExist(err) {
			sendError(w, "Client not found", http.StatusNotFound)
			return
		}
		sendError(w, "Failed to read client data", http.StatusInternalServerError)
		return
	}

	var clientData map[string]interface{}
	if err := json.Unmarshal(raw, &clientData); err != nil {
		sendError(w, "Failed to parse client data", http.StatusInternalServerError)
		return
	}

	oldToken, _ := clientData["subscription_token"].(string)
	newToken, err := generateSubscriptionToken()
	if err != nil || newToken == "" || newToken == oldToken {
		sendError(w, "Failed to generate a new subscription token", http.StatusInternalServerError)
		return
	}

	reworkedAt := time.Now().UTC().Format(time.RFC3339)
	result := clientReworkResponse{
		Name:        name,
		Nodes:       []clientReworkNodeResult{},
		FailedNodes: []string{},
		ReworkedAt:  reworkedAt,
	}

	if clientHasNode(clientData, "local") {
		oldSerial, newSerial, safetyDir, rotateErr := clientReworkMaster(
			name,
			clientPath,
			clientData,
			newToken,
			reworkedAt,
		)
		if rotateErr != nil {
			sendError(
				w,
				"Master credential rework failed: "+rotateErr.Error(),
				http.StatusInternalServerError,
			)
			return
		}
		result.MasterRotated = true
		result.MasterOldSerial = oldSerial
		result.MasterNewSerial = newSerial
		result.SafetyDirectory = safetyDir
	}

	nodes := activeRemoteNodesByIDs(nodeIDsFromClientData(clientData))
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	for _, node := range nodes {
		nodeResult := clientReworkNodeResult{
			ID:       node.ID,
			Name:     node.Name,
			Location: node.Location,
		}

		if rotateErr := clientReworkNode(node, name); rotateErr != nil {
			nodeResult.Error = rotateErr.Error()
			result.FailedNodes = append(result.FailedNodes, node.ID)

			if quarantineErr := setRemoteNodeAccess(node, name, false); quarantineErr == nil {
				nodeResult.Quarantined = true
			} else {
				nodeResult.Error += "; quarantine failed: " + quarantineErr.Error()
			}
		} else {
			nodeResult.Success = true
			if allowErr := setRemoteNodeAccess(node, name, true); allowErr != nil {
				nodeResult.Success = false
				nodeResult.Error = "rotated but allow-node failed: " + allowErr.Error()
				result.FailedNodes = append(result.FailedNodes, node.ID)
			}
		}

		result.Nodes = append(result.Nodes, nodeResult)
	}

	if !clientHasNode(clientData, "local") {
		if err := clientReworkRestoreMetadata(
			clientPath,
			clientData,
			newToken,
			reworkedAt,
		); err != nil {
			sendError(w, "Failed to update subscription token: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if clientHasNode(clientData, "local") {
		ikeInfo, ikeErr := clientReworkIKEv2(name)
		if ikeErr != nil {
			result.IKEv2Error = ikeErr.Error()
		} else {
			result.IKEv2Rotated = true
			result.IKEv2 = ikeInfo
		}
	}

	result.SubscriptionToken = newToken
	result.SubscriptionURL = buildSubscriptionURL(r, newToken)
	result.OldSubscriptionRevoked = oldToken != "" && oldToken != newToken
	result.Partial = len(result.FailedNodes) > 0 || result.IKEv2Error != ""

	clientsCache.mutex.Lock()
	clientsCache.data = nil
	clientsCache.timestamp = time.Time{}
	clientsCache.mutex.Unlock()

	go func(clientName string) {
		if syncErr := exec.Command(
			"systemctl",
			"start",
			"irangate-pg-sync-clients.service",
		).Run(); syncErr != nil {
			log.Printf(
				"client rework PostgreSQL sync failed for %s: %v",
				clientName,
				syncErr,
			)
		}
	}(name)

	message := "Client credentials and subscription link reworked successfully"
	if result.Partial {
		message = "Client reworked with warnings; failed nodes were quarantined where possible"
	}

	sendSuccess(w, message, result)
}
