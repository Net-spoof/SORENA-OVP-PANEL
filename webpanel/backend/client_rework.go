package main

import (
	"bytes"
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
	IKEv2Revoked           bool                     `json:"ikev2_revoked"`
	IKEv2                  IKEv2SubscriptionInfo    `json:"ikev2,omitempty"`
	IKEv2Error             string                   `json:"ikev2_error,omitempty"`
	SafetyDirectory        string                   `json:"safety_directory,omitempty"`
	ReworkedAt             string                   `json:"reworked_at"`
	Partial                bool                     `json:"partial"`
}

type clientReworkBackup struct {
	Source  string
	Backup  string
	Existed bool
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

func clientReworkBackupPaths(
	safetyDir string,
	sources []string,
) ([]clientReworkBackup, error) {
	backups := make([]clientReworkBackup, 0, len(sources))
	for index, source := range sources {
		backup := clientReworkBackup{
			Source: source,
			Backup: filepath.Join(
				safetyDir,
				fmt.Sprintf("item-%02d", index),
			),
		}
		if _, err := os.Lstat(source); err == nil {
			backup.Existed = true
			if err := clientReworkCopy(source, backup.Backup); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		backups = append(backups, backup)
	}
	return backups, nil
}

func clientReworkRestorePaths(backups []clientReworkBackup) error {
	var errors []string
	for _, backup := range backups {
		if err := os.RemoveAll(backup.Source); err != nil {
			errors = append(errors, err.Error())
			continue
		}
		if backup.Existed {
			if err := clientReworkCopy(backup.Backup, backup.Source); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("restore failed: %s", strings.Join(errors, "; "))
	}
	return nil
}

func clientReworkMasterProfile(name string) (string, error) {
	for _, candidate := range []string{
		filepath.Join("/opt/irangate/clients", name+".ovpn"),
		filepath.Join(
			"/etc/openvpn/client",
			name+".ovpn",
		),
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
	certificate := filepath.Join(
		"/etc/openvpn/easy-rsa/pki/issued",
		name+".crt",
	)
	output, err := exec.Command(
		"openssl",
		"x509",
		"-in",
		certificate,
		"-noout",
		"-serial",
	).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(string(output), "serial="))
}

func clientReworkReloadMasterOpenVPN() error {
	for _, service := range []string{
		"openvpn@server.service",
		"openvpn@server",
	} {
		if exec.Command(
			"systemctl",
			"is-active",
			"--quiet",
			service,
		).Run() == nil {
			return nil
		}
	}

	return fmt.Errorf(
		"OpenVPN master is not active",
	)
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
	return clientReworkReloadMasterOpenVPN()
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
	for key, value := range oldData {
		generated[key] = value
	}
	generated["subscription_token"] = token
	generated["last_reworked_at"] = reworkedAt
	generated["credential_generation"] = time.Now().UTC().UnixNano()
	generated["last_update"] = reworkedAt
	delete(generated, "rework_failed_nodes")
	return clientReworkWriteJSON(clientPath, generated)
}

func clientReworkMaster(
	name string,
	clientPath string,
	oldData map[string]interface{},
	newToken string,
	reworkedAt string,
) (
	oldSerial string,
	newSerial string,
	safetyDir string,
	err error,
) {
	profilePath, err := clientReworkMasterProfile(name)
	if err != nil {
		return "", "", "", err
	}
	serverConfig, err := clientReworkServerConfig()
	if err != nil {
		return "", "", "", err
	}
	oldSerial = clientReworkSerial(name)
	if oldSerial == "" {
		return "", "", "", fmt.Errorf("existing Master certificate serial is missing")
	}
	safetyDir = filepath.Join(
		"/opt/irangate/rework-safety",
		name,
		time.Now().UTC().Format("20060102T150405Z"),
	)
	if err := os.MkdirAll(safetyDir, 0700); err != nil {
		return "", "", "", err
	}
	backups, err := clientReworkBackupPaths(
		safetyDir,
		[]string{
			"/etc/openvpn/easy-rsa/pki",
			profilePath,
			clientPath,
			serverConfig,
			"/etc/openvpn/crl.pem",
			filepath.Join("/etc/openvpn/ccd", name),
			filepath.Join("/etc/openvpn/blocked-clients", name),
			filepath.Join("/etc/openvpn/node-denied-clients", name),
		},
	)
	if err != nil {
		return "", "", safetyDir, err
	}
	rollback := func(cause error) error {
		restoreErr := clientReworkRestorePaths(backups)
		reloadErr := clientReworkReloadMasterOpenVPN()
		if restoreErr != nil || reloadErr != nil {
			return fmt.Errorf(
				"%w; rollback restore=%v reload=%v; safety=%s",
				cause,
				restoreErr,
				reloadErr,
				safetyDir,
			)
		}
		return fmt.Errorf("%w; rollback applied; safety=%s", cause, safetyDir)
	}
	if _, err := clientReworkRun("irangate", "client", "remove", name); err != nil {
		return "", "", safetyDir, rollback(err)
	}
	if _, err := clientReworkRun("irangate", "client", "add", name); err != nil {
		return "", "", safetyDir, rollback(err)
	}
	if err := clientReworkRestoreMetadata(
		clientPath,
		oldData,
		newToken,
		reworkedAt,
	); err != nil {
		return "", "", safetyDir, rollback(err)
	}
	if err := clientReworkEnsureCRL(); err != nil {
		return "", "", safetyDir, rollback(err)
	}
	killLocalOpenVPNClient(name)
	newSerial = clientReworkSerial(name)
	if newSerial == "" || newSerial == oldSerial {
		return "", "", safetyDir, rollback(
			fmt.Errorf("Master certificate serial did not rotate"),
		)
	}
	return oldSerial, newSerial, safetyDir, nil
}

func clientReworkSelectedRemoteNodes(
	clientData map[string]interface{},
) ([]NodeConfig, error) {
	wanted := map[string]bool{}
	for _, rawID := range nodeIDsFromClientData(clientData) {
		id := normalizeNodeID(rawID)
		if id != "" && id != "local" {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return []NodeConfig{}, nil
	}
	nodesFile, err := loadNodesFile()
	if err != nil {
		return nil, err
	}
	result := []NodeConfig{}
	for _, node := range nodesFile.Nodes {
		id := normalizeNodeID(node.ID)
		if wanted[id] {
			result = append(result, node)
			delete(wanted, id)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf(
			"selected node configuration missing: %s",
			strings.Join(missing, ", "),
		)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func clientReworkNodeRequest(
	node NodeConfig,
	method string,
	path string,
	payload []byte,
	timeout time.Duration,
) ([]byte, error) {
	request, err := http.NewRequest(
		method,
		strings.TrimRight(node.AgentURL, "/")+path,
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, err
	}
	if len(payload) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if node.Token != "" {
		request.Header.Set("Authorization", "Bearer "+node.Token)
	}
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return body, fmt.Errorf(
			"node %s request %s failed: HTTP %d %s",
			node.ID,
			path,
			response.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}
	return body, nil
}

func clientReworkPreflightNode(node NodeConfig) error {
	body, err := clientReworkNodeRequest(
		node,
		http.MethodGet,
		"/capabilities",
		nil,
		12*time.Second,
	)
	if err != nil {
		return err
	}
	var parsed struct {
		Success      bool `json:"success"`
		ClientRework bool `json:"client_rework"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("node %s capabilities response is invalid: %w", node.ID, err)
	}
	if !parsed.Success || !parsed.ClientRework {
		return fmt.Errorf("node %s does not advertise client_rework", node.ID)
	}
	return nil
}

func clientReworkNode(node NodeConfig, name string) error {
	payload, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	body, err := clientReworkNodeRequest(
		node,
		http.MethodPost,
		"/clients/rework",
		payload,
		150*time.Second,
	)
	if err != nil {
		return err
	}
	var parsed map[string]interface{}
	if json.Unmarshal(body, &parsed) == nil {
		if success, ok := parsed["success"].(bool); ok && !success {
			return fmt.Errorf("node %s rework failed: %v", node.ID, parsed["message"])
		}
	}
	return nil
}

func clientReworkRestoreIKEv2(safetyDir string) error {
	var errors []string
	mappings := []struct {
		Backup string
		Target string
	}{
		{
			filepath.Join(safetyDir, "users.tsv"),
			"/opt/irangate/ikev2/users.tsv",
		},
		{
			filepath.Join(safetyDir, "users.conf"),
			"/etc/swanctl/conf.d/irangate-ikev2-users.conf",
		},
	}
	for _, mapping := range mappings {
		if _, err := os.Stat(mapping.Backup); err == nil {
			if err := clientReworkCopy(mapping.Backup, mapping.Target); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}
	if output, err := exec.Command("swanctl", "--load-creds").CombinedOutput(); err != nil {
		errors = append(errors, strings.TrimSpace(string(output)))
	}
	if len(errors) > 0 {
		return fmt.Errorf("IKEv2 rollback failed: %s", strings.Join(errors, "; "))
	}
	return nil
}

func clientReworkIKEv2(
	name string,
	safetyRoot string,
) (IKEv2SubscriptionInfo, error) {
	oldInfo, _ := ensureIKEv2Account(name, false)
	ikeSafety := filepath.Join(safetyRoot, "ikev2")
	if err := os.MkdirAll(ikeSafety, 0700); err != nil {
		return IKEv2SubscriptionInfo{}, err
	}
	for source, destination := range map[string]string{
		"/opt/irangate/ikev2/users.tsv":                 filepath.Join(ikeSafety, "users.tsv"),
		"/etc/swanctl/conf.d/irangate-ikev2-users.conf": filepath.Join(ikeSafety, "users.conf"),
	} {
		if _, err := os.Stat(source); err == nil {
			if err := clientReworkCopy(source, destination); err != nil {
				return IKEv2SubscriptionInfo{}, err
			}
		}
	}
	// Rotate only this user's IKEv2 credential, without an intervening delete.
	// A global users.tsv rollback can otherwise revert unrelated concurrent
	// client additions, revocations, or password changes.
	if oldInfo.Available {
		output, rotateErr := ikev2Run("rotate", name)
		if rotateErr != nil {
			return IKEv2SubscriptionInfo{}, fmt.Errorf(
				"IKEv2 rotation failed: %v; safety=%s",
				rotateErr, ikeSafety,
			)
		}
		newInfo := parseIKEv2AccountOutput(output)
		if !newInfo.Available || newInfo.Password == oldInfo.Password {
			return newInfo, fmt.Errorf(
				"IKEv2 rotation verification failed; safety=%s", ikeSafety,
			)
		}
		return newInfo, nil
	}
	// Only create a credential for clients without an existing IKEv2 record.
	// The user must download its newly issued password before connecting.
	return ensureIKEv2Account(name, true)
}

func clientReworkRevokeIKEv2(name string) (bool, error) {
	oldInfo, _ := ensureIKEv2Account(name, false)
	if !oldInfo.Available {
		return false, nil
	}
	output, err := ikev2Run("del", name)
	if err != nil {
		return false, fmt.Errorf(
			"IKEv2 revoke failed: %v; output=%s",
			err,
			strings.TrimSpace(output),
		)
	}
	return true, nil
}

func reworkClientHandler(w http.ResponseWriter, r *http.Request) {
	clientReworkLock.Lock()
	defer clientReworkLock.Unlock()

	name := strings.TrimSpace(mux.Vars(r)["name"])
	log.Printf("client rework request started: name=%s", name)
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

	nodes, err := clientReworkSelectedRemoteNodes(clientData)
	if err != nil {
		sendError(w, "Client rework preflight failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	for _, node := range nodes {
		if err := clientReworkPreflightNode(node); err != nil {
			sendError(w, "Client rework preflight failed: "+err.Error(), http.StatusBadGateway)
			return
		}
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
	criticalFailure := false

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
				criticalFailure = true
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

	if criticalFailure {
		sendError(
			w,
			"Client rework stopped: a failed node could not be quarantined; repair node control access before retrying",
			http.StatusBadGateway,
		)
		return
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

		ikeInfo, ikeErr := clientReworkIKEv2(name, safetyDir)
		if ikeErr != nil {
			result.IKEv2Error = ikeErr.Error()
		} else {
			result.IKEv2Rotated = true
			result.IKEv2 = ikeInfo
		}
	} else {
		if err := clientReworkRestoreMetadata(
			clientPath,
			clientData,
			newToken,
			reworkedAt,
		); err != nil {
			sendError(w, "Failed to update subscription token: "+err.Error(), http.StatusInternalServerError)
			return
		}
		revoked, revokeErr := clientReworkRevokeIKEv2(name)
		if revokeErr != nil {
			result.IKEv2Error = revokeErr.Error()
		} else {
			result.IKEv2Revoked = revoked
		}
	}

	clientRaw, readErr := os.ReadFile(clientPath)
	if readErr == nil {
		var latest map[string]interface{}
		if json.Unmarshal(clientRaw, &latest) == nil {
			latest["rework_failed_nodes"] = result.FailedNodes
			_ = clientReworkWriteJSON(clientPath, latest)
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
