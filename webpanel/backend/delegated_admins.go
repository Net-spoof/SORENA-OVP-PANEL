package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

const (
	delegatedRoleRoot  = "root"
	delegatedRoleAdmin = "delegated_admin"
)

var (
	DelegatedAdminsDBPath = filepath.Join(DataBaseDir, "webpanel", "delegated_admins.json")
	delegatedAdminsMu     sync.Mutex
	delegatedUsernameRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)
)

type DelegatedAdmin struct {
	Username          string           `json:"username"`
	PasswordHash      string           `json:"password_hash"`
	Active            bool             `json:"active"`
	TrafficLimitBytes int64            `json:"traffic_limit_bytes"`
	TrafficUsedBytes  int64            `json:"traffic_used_bytes"`
	ExpiresAt         time.Time        `json:"expires_at"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	LastLogin         time.Time        `json:"last_login"`
	Suspended         bool             `json:"suspended"`
	SuspendReason     string           `json:"suspend_reason,omitempty"`
	UsageSnapshots    map[string]int64 `json:"usage_snapshots,omitempty"`
}

type delegatedPrincipal struct {
	Username string
	Role     string
}

type delegatedPrincipalContextKey struct{}

type delegatedAdminView struct {
	Username              string    `json:"username"`
	Active                bool      `json:"active"`
	TrafficLimitBytes     int64     `json:"traffic_limit_bytes"`
	TrafficUsedBytes      int64     `json:"traffic_used_bytes"`
	TrafficRemainingBytes int64     `json:"traffic_remaining_bytes"`
	ExpiresAt             time.Time `json:"expires_at"`
	DaysRemaining         int       `json:"days_remaining"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
	LastLogin             time.Time `json:"last_login"`
	Suspended             bool      `json:"suspended"`
	SuspendReason         string    `json:"suspend_reason,omitempty"`
	Status                string    `json:"status"`
	ClientCount           int       `json:"client_count"`
}

type createDelegatedAdminRequest struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	TrafficLimitBytes int64  `json:"traffic_limit_bytes"`
	ExpirationDays    int    `json:"expiration_days"`
}

type delegatedTrafficTopupRequest struct {
	Bytes int64 `json:"bytes"`
}

type delegatedTimeTopupRequest struct {
	Days int `json:"days"`
}

type delegatedPasswordRequest struct {
	Password string `json:"password"`
}

func loadDelegatedAdminsLocked() ([]DelegatedAdmin, error) {
	data, err := os.ReadFile(DelegatedAdminsDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []DelegatedAdmin{}, nil
		}
		return nil, err
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return []DelegatedAdmin{}, nil
	}

	var admins []DelegatedAdmin
	if err := json.Unmarshal(data, &admins); err != nil {
		return nil, fmt.Errorf("parse delegated admin database: %w", err)
	}

	for i := range admins {
		if admins[i].UsageSnapshots == nil {
			admins[i].UsageSnapshots = map[string]int64{}
		}
	}

	return admins, nil
}

func saveDelegatedAdminsLocked(admins []DelegatedAdmin) error {
	if err := os.MkdirAll(filepath.Dir(DelegatedAdminsDBPath), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(admins, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := DelegatedAdminsDBPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, DelegatedAdminsDBPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(DelegatedAdminsDBPath, 0600)
}

func delegatedAdminIndex(admins []DelegatedAdmin, username string) int {
	for i := range admins {
		if strings.EqualFold(strings.TrimSpace(admins[i].Username), strings.TrimSpace(username)) {
			return i
		}
	}
	return -1
}

func getDelegatedAdmin(username string) (DelegatedAdmin, bool, error) {
	delegatedAdminsMu.Lock()
	defer delegatedAdminsMu.Unlock()

	admins, err := loadDelegatedAdminsLocked()
	if err != nil {
		return DelegatedAdmin{}, false, err
	}
	idx := delegatedAdminIndex(admins, username)
	if idx < 0 {
		return DelegatedAdmin{}, false, nil
	}
	return admins[idx], true, nil
}

func loadRootAdmin() (Admin, error) {
	var admin Admin
	data, err := os.ReadFile(AdminDBPath)
	if err != nil {
		return admin, err
	}
	if err := json.Unmarshal(data, &admin); err != nil {
		return admin, err
	}
	return admin, nil
}

func currentDelegatedPrincipal(r *http.Request) delegatedPrincipal {
	value, _ := r.Context().Value(delegatedPrincipalContextKey{}).(delegatedPrincipal)
	return value
}

func delegatedOwnedClientNames(username string) []string {
	clientsDir := filepath.Join(DataBaseDir, "database", "clients")
	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		return nil
	}

	result := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(clientsDir, entry.Name()))
		if err != nil {
			continue
		}

		var client map[string]interface{}
		if json.Unmarshal(data, &client) != nil {
			continue
		}

		owner := strings.TrimSpace(fmt.Sprintf("%v", client["owner_admin"]))
		if owner == "" || owner == "<nil>" || !strings.EqualFold(owner, username) {
			continue
		}

		name := strings.TrimSpace(fmt.Sprintf("%v", client["name"]))
		if name == "" || name == "<nil>" {
			name = strings.TrimSuffix(entry.Name(), ".json")
		}
		if name != "" {
			result = append(result, name)
		}
	}

	sort.Strings(result)
	return result
}

func delegatedClientOwner(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return ""
	}

	path := filepath.Join(DataBaseDir, "database", "clients", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var client map[string]interface{}
	if json.Unmarshal(data, &client) != nil {
		return ""
	}
	owner := strings.TrimSpace(fmt.Sprintf("%v", client["owner_admin"]))
	if owner == "<nil>" {
		return ""
	}
	return owner
}

func delegatedOwnsClient(username, name string) bool {
	owner := delegatedClientOwner(name)
	return owner != "" && strings.EqualFold(owner, username)
}

func delegatedGlobalUsageMap() map[string]int64 {
	path := filepath.Join(DataBaseDir, "traffic", "global_usage_totals.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]int64{}
	}

	var root map[string]interface{}
	if json.Unmarshal(data, &root) != nil {
		return map[string]int64{}
	}
	totals, _ := root["totals"].(map[string]interface{})
	result := map[string]int64{}

	for rawName, raw := range totals {
		row, _ := raw.(map[string]interface{})
		if row == nil {
			continue
		}
		total := delegatedInt64(row["total"])
		if total <= 0 {
			total = delegatedInt64(row["rx"]) + delegatedInt64(row["tx"])
		}
		if total < 0 {
			total = 0
		}
		key := strings.TrimSpace(rawName)
		if key != "" {
			// Existing client identity is exact/case-sensitive. Never merge
			// meeee and Meeee into one delegated-admin quota bucket.
			result[key] = total
		}
	}
	return result
}

func delegatedInt64(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		var n int64
		_, _ = fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
		return n
	default:
		return 0
	}
}

func delegatedEffectiveReason(admin DelegatedAdmin, now time.Time) string {
	if !admin.Active {
		return "disabled"
	}
	if !admin.ExpiresAt.IsZero() && !now.Before(admin.ExpiresAt) {
		return "expired"
	}
	if admin.TrafficLimitBytes > 0 && admin.TrafficUsedBytes >= admin.TrafficLimitBytes {
		return "traffic_exhausted"
	}
	return ""
}

func updateDelegatedUsageRecord(admin *DelegatedAdmin, global map[string]int64) {
	if admin.UsageSnapshots == nil {
		admin.UsageSnapshots = map[string]int64{}
	}

	for _, clientName := range delegatedOwnedClientNames(admin.Username) {
		key := strings.TrimSpace(clientName)
		current := global[key]
		previous, exists := admin.UsageSnapshots[key]
		if !exists {
			// New ownership starts from the current global counter. Future deltas are charged.
			admin.UsageSnapshots[key] = current
			continue
		}
		if current > previous {
			admin.TrafficUsedBytes += current - previous
		}
		// A per-client traffic reset must never refund the delegated-admin pool.
		admin.UsageSnapshots[key] = current
	}

	if admin.TrafficUsedBytes < 0 {
		admin.TrafficUsedBytes = 0
	}
}

func refreshDelegatedAdmin(username string, enforce bool) (DelegatedAdmin, error) {
	global := delegatedGlobalUsageMap()
	now := time.Now().UTC()

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err != nil {
		delegatedAdminsMu.Unlock()
		return DelegatedAdmin{}, err
	}
	idx := delegatedAdminIndex(admins, username)
	if idx < 0 {
		delegatedAdminsMu.Unlock()
		return DelegatedAdmin{}, os.ErrNotExist
	}

	admin := admins[idx]
	wasSuspended := admin.Suspended
	oldReason := admin.SuspendReason
	oldUsed := admin.TrafficUsedBytes
	updateDelegatedUsageRecord(&admin, global)
	reason := delegatedEffectiveReason(admin, now)
	admin.Suspended = reason != ""
	admin.SuspendReason = reason
	if oldUsed != admin.TrafficUsedBytes || wasSuspended != admin.Suspended || oldReason != admin.SuspendReason {
		admin.UpdatedAt = now
	}
	admins[idx] = admin
	err = saveDelegatedAdminsLocked(admins)
	delegatedAdminsMu.Unlock()
	if err != nil {
		return DelegatedAdmin{}, err
	}

	if enforce {
		switch {
		case admin.Suspended && (!wasSuspended || oldReason != admin.SuspendReason):
			suspendDelegatedAdminClients(admin.Username, admin.SuspendReason)
		case !admin.Suspended && wasSuspended:
			resumeDelegatedAdminClients(admin.Username)
		}
	}
	return admin, nil
}

func reconcileAllDelegatedAdmins(force bool) {
	global := delegatedGlobalUsageMap()
	now := time.Now().UTC()

	type enforcement struct {
		username string
		suspend  bool
		reason   string
		resume   bool
	}
	actions := make([]enforcement, 0)

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err != nil {
		delegatedAdminsMu.Unlock()
		log.Printf("Delegated admin monitor: load failed: %v", err)
		return
	}

	changed := false
	for i := range admins {
		beforeUsed := admins[i].TrafficUsedBytes
		beforeSuspended := admins[i].Suspended
		beforeReason := admins[i].SuspendReason

		updateDelegatedUsageRecord(&admins[i], global)
		reason := delegatedEffectiveReason(admins[i], now)
		admins[i].Suspended = reason != ""
		admins[i].SuspendReason = reason

		if beforeUsed != admins[i].TrafficUsedBytes || beforeSuspended != admins[i].Suspended || beforeReason != reason {
			admins[i].UpdatedAt = now
			changed = true
		}

		if admins[i].Suspended {
			if force || !beforeSuspended || beforeReason != reason {
				actions = append(actions, enforcement{username: admins[i].Username, suspend: true, reason: reason})
			}
		} else if beforeSuspended {
			actions = append(actions, enforcement{username: admins[i].Username, resume: true})
		}
	}

	if changed {
		if err := saveDelegatedAdminsLocked(admins); err != nil {
			log.Printf("Delegated admin monitor: save failed: %v", err)
		}
	}
	delegatedAdminsMu.Unlock()

	for _, action := range actions {
		if action.suspend {
			suspendDelegatedAdminClients(action.username, action.reason)
		} else if action.resume {
			resumeDelegatedAdminClients(action.username)
		}
	}
}

func StartDelegatedAdminMonitor(intervalSeconds int) {
	if intervalSeconds <= 0 {
		intervalSeconds = 15
	}

	// Re-assert persisted suspension state after every web-panel restart.
	reconcileAllDelegatedAdmins(true)

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()
	log.Printf("Delegated admin quota monitor started (every %d seconds)", intervalSeconds)
	for range ticker.C {
		reconcileAllDelegatedAdmins(false)
	}
}

func delegatedClientPath(name string) string {
	return filepath.Join(DataBaseDir, "database", "clients", name+".json")
}

func readDelegatedClientData(name string) (map[string]interface{}, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return nil, fmt.Errorf("invalid client name")
	}
	data, err := os.ReadFile(delegatedClientPath(name))
	if err != nil {
		return nil, err
	}
	var client map[string]interface{}
	if err := json.Unmarshal(data, &client); err != nil {
		return nil, err
	}
	return client, nil
}

func writeDelegatedClientData(name string, client map[string]interface{}) error {
	data, err := json.MarshalIndent(client, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := delegatedClientPath(name)
	tmp := path + ".admin.tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	clientsCache.mutex.Lock()
	clientsCache.data = nil
	clientsCache.timestamp = time.Time{}
	clientsCache.mutex.Unlock()
	return nil
}

func delegatedBool(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return false
	}
}

func ikev2WasBlocked(name string) bool {
	out, err := ikev2Run("show", name)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			return strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "Status:")), "blocked")
		}
	}
	return false
}

func suspendDelegatedClient(name, reason string) error {
	client, err := readDelegatedClientData(name)
	if err != nil {
		return err
	}

	alreadySuspended := delegatedBool(client["admin_suspended"])
	if !alreadySuspended {
		client["admin_suspended_prev_active"] = delegatedBool(client["active"])
		client["admin_suspended_prev_status"] = strings.TrimSpace(fmt.Sprintf("%v", client["status"]))
		_, statErr := os.Stat(filepath.Join("/etc/openvpn/blocked-clients", name))
		client["admin_suspended_prev_blocked"] = statErr == nil
		client["admin_suspended_prev_ikev2_blocked"] = ikev2WasBlocked(name)
	}

	client["admin_suspended"] = true
	client["admin_suspend_reason"] = reason
	client["admin_suspended_at"] = time.Now().UTC().Format(time.RFC3339)
	client["active"] = false
	client["status"] = "admin_" + reason
	client["last_update"] = time.Now().UTC().Format(time.RFC3339)

	if err := writeDelegatedClientData(name, client); err != nil {
		return err
	}

	blockLocalClient(name)
	killLocalOpenVPNClient(name)
	blockRemoteNodes(name)
	if out, err := ikev2Run("block", name); err != nil {
		log.Printf("Delegated admin IKEv2 block failed for %s: %v (%s)", name, err, strings.TrimSpace(out))
	}
	return nil
}

func delegatedClientOwnLimitReason(name string, client map[string]interface{}) string {
	now := time.Now().UTC()
	expiresRaw := strings.TrimSpace(fmt.Sprintf("%v", client["expires_at"]))
	if expiresRaw != "" && expiresRaw != "<nil>" && !strings.HasPrefix(expiresRaw, "0001-01-01") {
		if expiresAt, err := time.Parse(time.RFC3339, expiresRaw); err == nil && !now.Before(expiresAt) {
			return "expired"
		}
	}

	limit := delegatedInt64(client["traffic_limit_bytes"])
	if limit > 0 {
		used := fastEnforceGlobalUsage(name)
		if used >= limit {
			return "traffic_limit_exceeded"
		}
	}
	return ""
}

func resumeDelegatedClient(name string) error {
	client, err := readDelegatedClientData(name)
	if err != nil {
		return err
	}
	if !delegatedBool(client["admin_suspended"]) {
		return nil
	}

	prevActive := delegatedBool(client["admin_suspended_prev_active"])
	prevBlocked := delegatedBool(client["admin_suspended_prev_blocked"])
	prevIKEBlocked := delegatedBool(client["admin_suspended_prev_ikev2_blocked"])
	prevStatus := strings.TrimSpace(fmt.Sprintf("%v", client["admin_suspended_prev_status"]))
	if prevStatus == "<nil>" || strings.HasPrefix(prevStatus, "admin_") {
		prevStatus = "Active"
	}

	ownReason := delegatedClientOwnLimitReason(name, client)
	delete(client, "admin_suspended")
	delete(client, "admin_suspend_reason")
	delete(client, "admin_suspended_at")
	delete(client, "admin_suspended_prev_active")
	delete(client, "admin_suspended_prev_status")
	delete(client, "admin_suspended_prev_blocked")
	delete(client, "admin_suspended_prev_ikev2_blocked")

	if ownReason != "" {
		client["active"] = false
		client["status"] = ownReason
		client["last_update"] = time.Now().UTC().Format(time.RFC3339)
		if err := writeDelegatedClientData(name, client); err != nil {
			return err
		}
		blockLocalClient(name)
		killLocalOpenVPNClient(name)
		blockRemoteNodes(name)
		_, _ = ikev2Run("block", name)
		return nil
	}

	client["active"] = prevActive
	client["status"] = prevStatus
	client["last_update"] = time.Now().UTC().Format(time.RFC3339)
	if err := writeDelegatedClientData(name, client); err != nil {
		return err
	}

	if !prevBlocked {
		unblockLocalClient(name)
		unblockRemoteNodes(name)
	}
	if !prevIKEBlocked {
		if out, err := ikev2Run("unblock", name); err != nil {
			log.Printf("Delegated admin IKEv2 unblock failed for %s: %v (%s)", name, err, strings.TrimSpace(out))
		}
	}
	return nil
}

func runDelegatedClientWorkers(names []string, worker func(string) error) {
	if len(names) == 0 {
		return
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	workers := 8
	if len(names) < workers {
		workers = len(names)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				if err := worker(name); err != nil {
					log.Printf("Delegated admin client enforcement failed for %s: %v", name, err)
				}
			}
		}()
	}
	for _, name := range names {
		jobs <- name
	}
	close(jobs)
	wg.Wait()
}

func suspendDelegatedAdminClients(username, reason string) {
	names := delegatedOwnedClientNames(username)
	runDelegatedClientWorkers(names, func(name string) error {
		return suspendDelegatedClient(name, reason)
	})
	log.Printf("Delegated admin %s suspended: reason=%s clients=%d", username, reason, len(names))
}

func resumeDelegatedAdminClients(username string) {
	names := delegatedOwnedClientNames(username)
	runDelegatedClientWorkers(names, resumeDelegatedClient)
	log.Printf("Delegated admin %s resumed: clients=%d", username, len(names))
}

func stampDelegatedClientOwner(name, username string) error {
	client, err := readDelegatedClientData(name)
	if err != nil {
		return err
	}
	owner := strings.TrimSpace(fmt.Sprintf("%v", client["owner_admin"]))
	if owner != "" && owner != "<nil>" && !strings.EqualFold(owner, username) {
		return fmt.Errorf("client already belongs to another admin")
	}
	client["owner_admin"] = username
	client["owner_assigned_at"] = time.Now().UTC().Format(time.RFC3339)
	if err := writeDelegatedClientData(name, client); err != nil {
		return err
	}

	// Baseline current traffic immediately so a later traffic reset cannot refund the pool.
	current := delegatedGlobalUsageMap()[strings.ToLower(name)]
	delegatedAdminsMu.Lock()
	defer delegatedAdminsMu.Unlock()
	admins, err := loadDelegatedAdminsLocked()
	if err != nil {
		return err
	}
	idx := delegatedAdminIndex(admins, username)
	if idx < 0 {
		return os.ErrNotExist
	}
	if admins[idx].UsageSnapshots == nil {
		admins[idx].UsageSnapshots = map[string]int64{}
	}
	admins[idx].UsageSnapshots[strings.ToLower(name)] = current
	admins[idx].UpdatedAt = time.Now().UTC()
	return saveDelegatedAdminsLocked(admins)
}

func delegatedAdminToView(admin DelegatedAdmin) delegatedAdminView {
	remaining := admin.TrafficLimitBytes - admin.TrafficUsedBytes
	if remaining < 0 {
		remaining = 0
	}
	days := 0
	if !admin.ExpiresAt.IsZero() {
		hours := time.Until(admin.ExpiresAt).Hours()
		if hours > 0 {
			days = int(math.Ceil(hours / 24))
		}
	}
	status := "active"
	if reason := delegatedEffectiveReason(admin, time.Now().UTC()); reason != "" {
		status = reason
	}
	return delegatedAdminView{
		Username:              admin.Username,
		Active:                admin.Active,
		TrafficLimitBytes:     admin.TrafficLimitBytes,
		TrafficUsedBytes:      admin.TrafficUsedBytes,
		TrafficRemainingBytes: remaining,
		ExpiresAt:             admin.ExpiresAt,
		DaysRemaining:         days,
		CreatedAt:             admin.CreatedAt,
		UpdatedAt:             admin.UpdatedAt,
		LastLogin:             admin.LastLogin,
		Suspended:             admin.Suspended,
		SuspendReason:         admin.SuspendReason,
		Status:                status,
		ClientCount:           len(delegatedOwnedClientNames(admin.Username)),
	}
}

func delegatedRootOnly(w http.ResponseWriter, r *http.Request) bool {
	principal := currentDelegatedPrincipal(r)
	if principal.Role != delegatedRoleRoot {
		sendError(w, "Root admin access required", http.StatusForbidden)
		return false
	}
	return true
}

func delegatedLoginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)

	root, rootErr := loadRootAdmin()
	if rootErr == nil && root.Username == req.Username && verifyPassword(req.Password, root.PasswordHash) {
		token, expiresAt, err := generateToken(req.Username)
		if err != nil {
			sendError(w, "Failed to generate token", http.StatusInternalServerError)
			return
		}
		root.LastLogin = time.Now().UTC()
		if data, err := json.MarshalIndent(root, "", "  "); err == nil {
			if err := os.WriteFile(AdminDBPath, data, 0600); err != nil {
				log.Printf("Warning: failed to update root admin last login: %v", err)
			}
		}
		sendSuccess(w, "Login successful", map[string]interface{}{
			"token": token, "username": req.Username, "expires_at": expiresAt, "role": delegatedRoleRoot,
		})
		return
	}

	admin, exists, err := getDelegatedAdmin(req.Username)
	if err != nil {
		sendError(w, "Authentication failed", http.StatusInternalServerError)
		return
	}
	if !exists || !verifyPassword(req.Password, admin.PasswordHash) {
		sendError(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	admin, err = refreshDelegatedAdmin(admin.Username, true)
	if err != nil {
		sendError(w, "Authentication failed", http.StatusInternalServerError)
		return
	}
	if delegatedEffectiveReason(admin, time.Now().UTC()) != "" || admin.Suspended {
		sendError(w, "Admin account is disabled or its quota has expired", http.StatusForbidden)
		return
	}

	token, expiresAt, err := generateToken(admin.Username)
	if err != nil {
		sendError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	delegatedAdminsMu.Lock()
	admins, loadErr := loadDelegatedAdminsLocked()
	if loadErr == nil {
		idx := delegatedAdminIndex(admins, admin.Username)
		if idx >= 0 {
			admins[idx].LastLogin = time.Now().UTC()
			admins[idx].UpdatedAt = time.Now().UTC()
			loadErr = saveDelegatedAdminsLocked(admins)
		}
	}
	delegatedAdminsMu.Unlock()
	if loadErr != nil {
		log.Printf("Warning: failed to update delegated admin last login: %v", loadErr)
	}

	sendSuccess(w, "Login successful", map[string]interface{}{
		"token": token, "username": admin.Username, "expires_at": expiresAt, "role": delegatedRoleAdmin,
	})
}

func delegatedClientNameFromPath(path string) string {
	trimmed := strings.TrimPrefix(path, "/api/clients/")
	if trimmed == path || trimmed == "" {
		return ""
	}
	part := strings.SplitN(trimmed, "/", 2)[0]
	name, err := url.PathUnescape(part)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

func delegatedRequestAllowed(r *http.Request, principal delegatedPrincipal) bool {
	if principal.Role == delegatedRoleRoot {
		return true
	}
	if principal.Role != delegatedRoleAdmin {
		return false
	}

	path := r.URL.Path
	method := r.Method
	if path == "/api/me" && method == http.MethodGet {
		return true
	}
	if path == "/api/clients" && (method == http.MethodGet || method == http.MethodPost) {
		return true
	}
	if path == "/api/nodes" && method == http.MethodGet {
		return true
	}
	if path == "/api/openvpn/configs" && method == http.MethodGet {
		return true
	}
	if path == "/api/openvpn-port-locations" && method == http.MethodGet {
		return true
	}
	if strings.HasPrefix(path, "/api/clients/") {
		name := delegatedClientNameFromPath(path)
		return name != "" && delegatedOwnsClient(principal.Username, name)
	}
	return false
}

func delegatedAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authHeader, "Bearer ") {
			sendError(w, "Authorization header required", http.StatusUnauthorized)
			return
		}
		tokenString := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(JWTSecret), nil
		})
		if err != nil || !token.Valid || strings.TrimSpace(claims.Username) == "" {
			sendError(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}

		principal := delegatedPrincipal{Username: claims.Username}
		if root, err := loadRootAdmin(); err == nil && root.Username == claims.Username {
			principal.Role = delegatedRoleRoot
		} else {
			admin, exists, err := getDelegatedAdmin(claims.Username)
			if err != nil || !exists {
				sendError(w, "Account no longer exists", http.StatusUnauthorized)
				return
			}
			admin, err = refreshDelegatedAdmin(admin.Username, true)
			if err != nil {
				sendError(w, "Failed to validate delegated admin", http.StatusInternalServerError)
				return
			}
			if delegatedEffectiveReason(admin, time.Now().UTC()) != "" || admin.Suspended {
				sendError(w, "Admin quota expired or account disabled", http.StatusForbidden)
				return
			}
			principal.Role = delegatedRoleAdmin
		}

		if !delegatedRequestAllowed(r, principal) {
			sendError(w, "This account is limited to its own OpenVPN clients", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), delegatedPrincipalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func delegatedMeHandler(w http.ResponseWriter, r *http.Request) {
	principal := currentDelegatedPrincipal(r)
	if principal.Role == delegatedRoleRoot {
		sendSuccess(w, "Account loaded", map[string]interface{}{
			"username": principal.Username, "role": delegatedRoleRoot,
		})
		return
	}
	admin, err := refreshDelegatedAdmin(principal.Username, false)
	if err != nil {
		sendError(w, "Failed to load account", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Account loaded", map[string]interface{}{
		"username": principal.Username, "role": delegatedRoleAdmin, "quota": delegatedAdminToView(admin),
	})
}

func forwardRecordedResponse(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for key, values := range rec.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	code := rec.Code
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	_, _ = w.Write(rec.Body.Bytes())
}

func delegatedGetClientsHandler(w http.ResponseWriter, r *http.Request) {
	principal := currentDelegatedPrincipal(r)
	if principal.Role == delegatedRoleRoot {
		getClientsWithNodeStatsHandler(w, r)
		return
	}

	rec := httptest.NewRecorder()
	getClientsWithNodeStatsHandler(rec, r)
	if rec.Code >= 400 {
		forwardRecordedResponse(w, rec)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		forwardRecordedResponse(w, rec)
		return
	}
	if items, ok := payload["data"].([]interface{}); ok {
		filtered := make([]interface{}, 0, len(items))
		for _, raw := range items {
			row, _ := raw.(map[string]interface{})
			if row == nil {
				continue
			}
			name := strings.TrimSpace(fmt.Sprintf("%v", row["name"]))
			if name != "" && delegatedOwnsClient(principal.Username, name) {
				filtered = append(filtered, raw)
			}
		}
		payload["data"] = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func delegatedCreateClientHandler(w http.ResponseWriter, r *http.Request) {
	principal := currentDelegatedPrincipal(r)
	if principal.Role == delegatedRoleRoot {
		createClientHandler(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()
	var request struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &request) != nil || strings.TrimSpace(request.Name) == "" {
		sendError(w, "Client name is required", http.StatusBadRequest)
		return
	}

	if admin, err := refreshDelegatedAdmin(principal.Username, true); err != nil || admin.Suspended {
		sendError(w, "Admin quota expired or account disabled", http.StatusForbidden)
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := httptest.NewRecorder()
	createClientHandler(rec, r)
	if rec.Code < 200 || rec.Code >= 300 {
		forwardRecordedResponse(w, rec)
		return
	}
	if err := stampDelegatedClientOwner(canonicalClientNameForCreate(request.Name), principal.Username); err != nil {
		log.Printf("CRITICAL: client %s was created but delegated ownership could not be stamped: %v", request.Name, err)
		sendError(w, "Client was created but ownership registration failed; root admin intervention is required", http.StatusInternalServerError)
		return
	}
	forwardRecordedResponse(w, rec)
}

func delegatedNodesHandler(w http.ResponseWriter, r *http.Request) {
	principal := currentDelegatedPrincipal(r)
	if principal.Role == delegatedRoleRoot {
		getNodesHandler(w, r)
		return
	}

	data, err := os.ReadFile(NodesDBPath)
	if err != nil {
		sendSuccess(w, "Nodes retrieved", []interface{}{})
		return
	}

	var file NodesFile
	if err := json.Unmarshal(data, &file); err != nil {
		var direct []NodeConfig
		if json.Unmarshal(data, &direct) != nil {
			sendError(w, "Failed to load nodes", http.StatusInternalServerError)
			return
		}
		file.Nodes = direct
	}

	safe := make([]map[string]interface{}, 0, len(file.Nodes))
	for _, node := range file.Nodes {
		safe = append(safe, map[string]interface{}{
			"id": node.ID, "name": node.Name, "flag": node.Flag, "location": node.Location,
			"enabled": node.Enabled, "is_local": node.IsLocal,
		})
	}
	sendSuccess(w, "Nodes retrieved", safe)
}

func listDelegatedAdminsHandler(w http.ResponseWriter, r *http.Request) {
	if !delegatedRootOnly(w, r) {
		return
	}
	reconcileAllDelegatedAdmins(false)

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	delegatedAdminsMu.Unlock()
	if err != nil {
		sendError(w, "Failed to load admins", http.StatusInternalServerError)
		return
	}
	views := make([]delegatedAdminView, 0, len(admins))
	for _, admin := range admins {
		views = append(views, delegatedAdminToView(admin))
	}
	sort.Slice(views, func(i, j int) bool { return strings.ToLower(views[i].Username) < strings.ToLower(views[j].Username) })
	sendSuccess(w, "Admins retrieved", views)
}

func createDelegatedAdminHandler(w http.ResponseWriter, r *http.Request) {
	if !delegatedRootOnly(w, r) {
		return
	}
	var req createDelegatedAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !delegatedUsernameRE.MatchString(req.Username) {
		sendError(w, "Username must be 3-64 characters: letters, numbers, dot, underscore or hyphen", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		sendError(w, "Password must be 8-128 characters", http.StatusBadRequest)
		return
	}
	if req.TrafficLimitBytes <= 0 {
		sendError(w, "Traffic quota must be greater than zero", http.StatusBadRequest)
		return
	}
	if req.ExpirationDays <= 0 || req.ExpirationDays > 3650 {
		sendError(w, "Expiration days must be between 1 and 3650", http.StatusBadRequest)
		return
	}
	if root, err := loadRootAdmin(); err == nil && strings.EqualFold(root.Username, req.Username) {
		sendError(w, "Username is already used by root admin", http.StatusConflict)
		return
	}

	now := time.Now().UTC()
	admin := DelegatedAdmin{
		Username: req.Username, PasswordHash: hashPassword(req.Password), Active: true,
		TrafficLimitBytes: req.TrafficLimitBytes, TrafficUsedBytes: 0,
		ExpiresAt: now.Add(time.Duration(req.ExpirationDays) * 24 * time.Hour),
		CreatedAt: now, UpdatedAt: now, UsageSnapshots: map[string]int64{},
	}

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err == nil && delegatedAdminIndex(admins, admin.Username) >= 0 {
		err = fmt.Errorf("exists")
	} else if err == nil {
		admins = append(admins, admin)
		err = saveDelegatedAdminsLocked(admins)
	}
	delegatedAdminsMu.Unlock()
	if err != nil {
		if err.Error() == "exists" {
			sendError(w, "Admin username already exists", http.StatusConflict)
		} else {
			sendError(w, "Failed to create admin", http.StatusInternalServerError)
		}
		return
	}
	sendSuccess(w, "Admin created", delegatedAdminToView(admin))
}

func addDelegatedAdminTrafficHandler(w http.ResponseWriter, r *http.Request) {
	if !delegatedRootOnly(w, r) {
		return
	}
	var req delegatedTrafficTopupRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Bytes <= 0 {
		sendError(w, "Positive traffic bytes are required", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(mux.Vars(r)["username"])

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err == nil {
		idx := delegatedAdminIndex(admins, username)
		if idx < 0 {
			err = os.ErrNotExist
		} else {
			admins[idx].TrafficLimitBytes += req.Bytes
			admins[idx].UpdatedAt = time.Now().UTC()
			err = saveDelegatedAdminsLocked(admins)
		}
	}
	delegatedAdminsMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			sendError(w, "Admin not found", http.StatusNotFound)
		} else {
			sendError(w, "Failed to add traffic", http.StatusInternalServerError)
		}
		return
	}
	admin, _ := refreshDelegatedAdmin(username, true)
	sendSuccess(w, "Traffic added", delegatedAdminToView(admin))
}

func addDelegatedAdminTimeHandler(w http.ResponseWriter, r *http.Request) {
	if !delegatedRootOnly(w, r) {
		return
	}
	var req delegatedTimeTopupRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Days <= 0 || req.Days > 3650 {
		sendError(w, "Days must be between 1 and 3650", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(mux.Vars(r)["username"])
	now := time.Now().UTC()

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err == nil {
		idx := delegatedAdminIndex(admins, username)
		if idx < 0 {
			err = os.ErrNotExist
		} else {
			base := admins[idx].ExpiresAt
			if base.IsZero() || base.Before(now) {
				base = now
			}
			admins[idx].ExpiresAt = base.Add(time.Duration(req.Days) * 24 * time.Hour)
			admins[idx].UpdatedAt = now
			err = saveDelegatedAdminsLocked(admins)
		}
	}
	delegatedAdminsMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			sendError(w, "Admin not found", http.StatusNotFound)
		} else {
			sendError(w, "Failed to add time", http.StatusInternalServerError)
		}
		return
	}
	admin, _ := refreshDelegatedAdmin(username, true)
	sendSuccess(w, "Time added", delegatedAdminToView(admin))
}

func resetDelegatedAdminPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if !delegatedRootOnly(w, r) {
		return
	}
	var req delegatedPasswordRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Password) < 8 || len(req.Password) > 128 {
		sendError(w, "Password must be 8-128 characters", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(mux.Vars(r)["username"])

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err == nil {
		idx := delegatedAdminIndex(admins, username)
		if idx < 0 {
			err = os.ErrNotExist
		} else {
			admins[idx].PasswordHash = hashPassword(req.Password)
			admins[idx].UpdatedAt = time.Now().UTC()
			err = saveDelegatedAdminsLocked(admins)
		}
	}
	delegatedAdminsMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			sendError(w, "Admin not found", http.StatusNotFound)
		} else {
			sendError(w, "Failed to reset password", http.StatusInternalServerError)
		}
		return
	}
	sendSuccess(w, "Password updated", nil)
}

func setDelegatedAdminActive(w http.ResponseWriter, r *http.Request, active bool) {
	if !delegatedRootOnly(w, r) {
		return
	}
	username := strings.TrimSpace(mux.Vars(r)["username"])
	now := time.Now().UTC()

	delegatedAdminsMu.Lock()
	admins, err := loadDelegatedAdminsLocked()
	if err == nil {
		idx := delegatedAdminIndex(admins, username)
		if idx < 0 {
			err = os.ErrNotExist
		} else {
			admins[idx].Active = active
			admins[idx].UpdatedAt = now
			if !active {
				admins[idx].Suspended = true
				admins[idx].SuspendReason = "disabled"
			}
			err = saveDelegatedAdminsLocked(admins)
		}
	}
	delegatedAdminsMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			sendError(w, "Admin not found", http.StatusNotFound)
		} else {
			sendError(w, "Failed to update admin", http.StatusInternalServerError)
		}
		return
	}

	if !active {
		suspendDelegatedAdminClients(username, "disabled")
	}
	admin, err := refreshDelegatedAdmin(username, active)
	if err != nil {
		sendError(w, "Admin state saved but enforcement refresh failed", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Admin state updated", delegatedAdminToView(admin))
}

func disableDelegatedAdminHandler(w http.ResponseWriter, r *http.Request) {
	setDelegatedAdminActive(w, r, false)
}

func enableDelegatedAdminHandler(w http.ResponseWriter, r *http.Request) {
	setDelegatedAdminActive(w, r, true)
}
