package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amiridev-org/irangate-ov/pkg/utils"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

var (
	DataBaseDir     = readEnvOrDefault("IRANGATE_DATA_DIR", "/opt/irangate")
	AdminDBPath     = filepath.Join(DataBaseDir, "webpanel", "admin.json")
	TelegramDBPath  = filepath.Join(DataBaseDir, "webpanel", "telegram.json")
	ServerDBPath    = filepath.Join(DataBaseDir, "webpanel", "server.json")
	NodesDBPath     = filepath.Join(DataBaseDir, "webpanel", "nodes.json")
	TemplatesPath   = filepath.Join(DataBaseDir, "webpanel", "templates")
	JWTSecret       = readEnvOrDefault("JWT_SECRET", "your-secret-key-change-this-in-production")
	TokenExpiration = 24 * time.Hour
)

func readEnvOrDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

type Admin struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Email        string    `json:"email"`
	CreatedAt    time.Time `json:"created_at"`
	LastLogin    time.Time `json:"last_login"`
}
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type LoginResponse struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}
type ServerConfig struct {
	ServerIP        string `json:"server_ip"`
	IKEv2ServerHost string `json:"ikev2_server_host,omitempty"`
	ServerDomain    string `json:"server_domain"`
	ServerPort      int    `json:"server_port"`
	Protocol        string `json:"protocol"`
}

type NodeConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Flag      string `json:"flag,omitempty"`
	Location  string `json:"location"`
	PublicIP  string `json:"public_ip"`
	AgentURL  string `json:"agent_url"`
	Token     string `json:"token,omitempty"`
	Enabled   bool   `json:"enabled"`
	IsLocal   bool   `json:"is_local"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type NodesFile struct {
	Nodes []NodeConfig `json:"nodes"`
}
type Template struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Content     string    `json:"content"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func syncAdminCredentialsFromEnv() error {
	username := readEnvOrDefault(
		"WEBPANEL_ADMIN_USER",
		"admin",
	)

	password := readEnvOrDefault(
		"WEBPANEL_ADMIN_PASS",
		"adminadmin",
	)

	emailFromEnv := strings.TrimSpace(
		os.Getenv("WEBPANEL_ADMIN_EMAIL"),
	)

	if err := os.MkdirAll(
		filepath.Dir(AdminDBPath),
		0755,
	); err != nil {
		return fmt.Errorf(
			"create admin database directory: %w",
			err,
		)
	}

	now := time.Now()

	admin := Admin{
		CreatedAt: now,
		Email:     "admin@irangate.local",
	}

	existingData, err := os.ReadFile(
		AdminDBPath,
	)

	switch {
	case err == nil:
		if err := json.Unmarshal(
			existingData,
			&admin,
		); err != nil {
			return fmt.Errorf(
				"parse existing admin database: %w",
				err,
			)
		}

		if admin.CreatedAt.IsZero() {
			admin.CreatedAt = now
		}

	case os.IsNotExist(err):
		// Fresh installation.

	default:
		return fmt.Errorf(
			"read admin database: %w",
			err,
		)
	}

	admin.Username = username
	admin.PasswordHash = hashPassword(password)

	if emailFromEnv != "" {
		admin.Email = emailFromEnv
	} else if strings.TrimSpace(admin.Email) == "" {
		admin.Email = "admin@irangate.local"
	}

	data, err := json.MarshalIndent(
		admin,
		"",
		"  ",
	)

	if err != nil {
		return fmt.Errorf(
			"encode admin database: %w",
			err,
		)
	}

	tempPath := AdminDBPath + ".tmp"

	if err := os.WriteFile(
		tempPath,
		data,
		0600,
	); err != nil {
		return fmt.Errorf(
			"write temporary admin database: %w",
			err,
		)
	}

	if err := os.Rename(
		tempPath,
		AdminDBPath,
	); err != nil {
		_ = os.Remove(tempPath)

		return fmt.Errorf(
			"replace admin database: %w",
			err,
		)
	}

	if err := os.Chmod(
		AdminDBPath,
		0600,
	); err != nil {
		return fmt.Errorf(
			"secure admin database permissions: %w",
			err,
		)
	}

	return nil
}

func initDatabases() error {
	os.MkdirAll(filepath.Dir(AdminDBPath), 0755)
	os.MkdirAll(TemplatesPath, 0755)
	if _, err := os.Stat(AdminDBPath); os.IsNotExist(err) {
		initialUser := readEnvOrDefault("WEBPANEL_ADMIN_USER", "admin")
		initialPass := readEnvOrDefault("WEBPANEL_ADMIN_PASS", "adminadmin")
		initialEmail := readEnvOrDefault("WEBPANEL_ADMIN_EMAIL", "admin@irangate.local")
		passwordHash := hashPassword(initialPass)
		admin := Admin{
			Username:     initialUser,
			PasswordHash: passwordHash,
			Email:        initialEmail,
			CreatedAt:    time.Now(),
		}
		data, err := json.MarshalIndent(admin, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(AdminDBPath, data, 0600); err != nil {
			return err
		}
		log.Printf("⚠️  Initial admin created - Username: %s\n", initialUser)
		if initialPass == "adminadmin" {
			log.Println("⚠️  Default admin password is in use; change it for production.")
		}
	}
	if _, err := os.Stat(TelegramDBPath); os.IsNotExist(err) {
		telegram := TelegramConfig{
			Enabled: false,
		}
		data, err := json.MarshalIndent(telegram, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(TelegramDBPath, data, 0600); err != nil {
			return err
		}
	}
	if _, err := os.Stat(ServerDBPath); os.IsNotExist(err) {
		server := ServerConfig{
			ServerPort: 1194,
			Protocol:   "udp",
		}
		data, err := json.MarshalIndent(server, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(ServerDBPath, data, 0600); err != nil {
			return err
		}
	}
	return nil
}
func hashPassword(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Warning: Failed to hash password with bcrypt, falling back to SHA-256: %v", err)
		shaHash := sha256.Sum256([]byte(password))
		return fmt.Sprintf("%x", shaHash)
	}
	return string(hash)
}
func verifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err == nil {
		return true
	}
	shaHash := sha256.Sum256([]byte(password))
	shaHashStr := fmt.Sprintf("%x", shaHash)
	return shaHashStr == hash
}
func generateToken(username string) (string, time.Time, error) {
	expirationTime := time.Now().Add(TokenExpiration)
	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(JWTSecret))
	return tokenString, expirationTime, err
}
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			sendError(w, "Authorization header required", http.StatusUnauthorized)
			return
		}
		tokenString := strings.Replace(authHeader, "Bearer ", "", 1)
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(JWTSecret), nil
		})
		if err != nil || !token.Valid {
			sendError(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
func sendError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(APIResponse{
		Success: false,
		Message: message,
	})
}
func sendSuccess(w http.ResponseWriter, message string, data interface{}) {
	sendJSON(w, APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}
func loginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(AdminDBPath)
	if err != nil {
		sendError(w, "Authentication failed", http.StatusInternalServerError)
		return
	}
	var admin Admin
	if err := json.Unmarshal(data, &admin); err != nil {
		sendError(w, "Authentication failed", http.StatusInternalServerError)
		return
	}
	if admin.Username != req.Username || !verifyPassword(req.Password, admin.PasswordHash) {
		sendError(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}
	token, expiresAt, err := generateToken(req.Username)
	if err != nil {
		sendError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	admin.LastLogin = time.Now()
	data, err = json.MarshalIndent(admin, "", "  ")
	if err != nil {
		log.Printf("Warning: Failed to marshal admin data: %v", err)
	}
	if err := os.WriteFile(AdminDBPath, data, 0600); err != nil {
		log.Printf("Warning: Failed to write admin data: %v", err)
	}
	sendSuccess(w, "Login successful", LoginResponse{
		Token:     token,
		Username:  req.Username,
		ExpiresAt: expiresAt,
	})
}

var clientsCache struct {
	data       []interface{}
	timestamp  time.Time
	generation uint64
	mutex      sync.RWMutex
}

// Invalidate after a mutation reaches its final persistent state. The generation
// prevents older, still-running GET requests from repopulating a stale cache.
func invalidateClientsCache() {
	clientsCache.mutex.Lock()
	clientsCache.data = nil
	clientsCache.timestamp = time.Time{}
	clientsCache.generation++
	clientsCache.mutex.Unlock()
}

func cacheClientsIfCurrent(generation uint64, clients []interface{}) bool {
	clientsCache.mutex.Lock()
	defer clientsCache.mutex.Unlock()

	if clientsCache.generation != generation {
		return false
	}

	clientsCache.data = clients
	clientsCache.timestamp = time.Now()
	return true
}

func getClientsHandler(w http.ResponseWriter, r *http.Request) {
	clientsCache.mutex.RLock()
	cacheValid := time.Since(clientsCache.timestamp) < 20*time.Second && clientsCache.data != nil
	cachedClients := clientsCache.data
	cacheGeneration := clientsCache.generation
	clientsCache.mutex.RUnlock()
	if cacheValid {
		sendSuccess(w, "Clients retrieved successfully (cached)", cachedClients)
		return
	}
	cmd := exec.Command("irangate", "client", "list", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to get clients: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var clients []interface{}
	if err := json.Unmarshal(output, &clients); err != nil {
		sendError(w, "Failed to parse clients", http.StatusInternalServerError)
		return
	}
	trafficDB := NewTrafficDatabase()
	trafficStats, err := trafficDB.GetCurrentClientStats()
	cumulativeTotals := trafficDB.loadCumulativeTotalsMap()
	globalUsageTotals := delegatedGlobalUsageMap()
	parser := NewOpenVPNStatusParser()
	currentlyConnected := make(map[string]bool)
	connectedSince := make(map[string]time.Time)
	foundAnyClients := false
	for _, statusFile := range parser.StatusFiles {
		if clients, err := parser.ParseStatusLog(statusFile); err == nil && len(clients) > 0 {
			foundAnyClients = true
			for _, client := range clients {
				currentlyConnected[client.CommonName] = true
				if !client.ConnectedSince.IsZero() {
					connectedSince[client.CommonName] = client.ConnectedSince
				}
			}
			log.Printf("Found %d connected clients from %s", len(clients), statusFile)
		} else if err != nil {
			log.Printf("Error parsing status file %s: %v", statusFile, err)
		}
	}
	if !foundAnyClients {
		log.Printf("Warning: No connected clients found in any status file. Checked: %v", parser.StatusFiles)
	}
	if err == nil {
		trafficMap := make(map[string]ClientTrafficStats)
		for _, stat := range trafficStats {
			trafficMap[stat.ClientName] = stat
		}
		for i, client := range clients {
			if clientMap, ok := client.(map[string]interface{}); ok {
				if name, ok := clientMap["name"].(string); ok {
					if authoritativeUsed, exists := globalUsageTotals[strings.TrimSpace(name)]; exists {
						// Explicitly distinguish a valid PostgreSQL zero from absent
						// usage data. Live OpenVPN session counters are not a fallback
						// for a manually reset authoritative total of zero.
						clientMap["traffic_used_bytes"] = authoritativeUsed
						clientMap["traffic_used_bytes_authoritative"] = true
					}
					if token, err := ensureClientSubscriptionToken(name); err == nil {
						clientMap["subscription_token"] = token
						clientMap["subscription_url"] = buildSubscriptionURL(r, token)
					}

					clientJSONPath := filepath.Join(DataBaseDir, "database", "clients", name+".json")
					if diskDataBytes, err := os.ReadFile(clientJSONPath); err == nil {
						var diskData map[string]interface{}
						if err := json.Unmarshal(diskDataBytes, &diskData); err == nil {
							if nodes, ok := diskData["nodes"]; ok {
								clientMap["nodes"] = nodes
							}
							if email, ok := diskData["email"]; ok {
								clientMap["email"] = email
							}
							if expiresAt, ok := diskData["expires_at"]; ok {
								clientMap["expires_at"] = expiresAt
							}
							if limit, ok := diskData["traffic_limit_bytes"]; ok {
								clientMap["traffic_limit_bytes"] = limit
							}
							if connLimit, ok := diskData["connection_limit"]; ok {
								clientMap["connection_limit"] = connLimit
							}
							if ownerAdmin, ok := diskData["owner_admin"]; ok {
								clientMap["owner_admin"] = ownerAdmin
							}
							if adminSuspended, ok := diskData["admin_suspended"]; ok {
								clientMap["admin_suspended"] = adminSuspended
							}
							if suspendReason, ok := diskData["admin_suspend_reason"]; ok {
								clientMap["admin_suspend_reason"] = suspendReason
							}
							if persistedStatus, ok := diskData["status"]; ok {
								clientMap["persisted_status"] = persistedStatus
							}
						}
					}

					if _, ok := clientMap["nodes"]; !ok {
						clientMap["nodes"] = []string{"local"}
					}

					isCurrentlyConnected := currentlyConnected[name]
					if stat, exists := trafficMap[name]; exists {
						stat.Connected = isCurrentlyConnected
						if isCurrentlyConnected {
							if connTime, ok := connectedSince[name]; ok && !connTime.IsZero() {
								if connTime.After(stat.LastSeen) {
									stat.LastSeen = connTime
								}
								clientMap["connected_since"] = connTime.Format(time.RFC3339)
							} else {
								clientMap["connected_since"] = time.Now().Format(time.RFC3339)
							}
						}
						clientMap["bytes_received"] = stat.CurrentReceived
						clientMap["bytes_sent"] = stat.CurrentSent
						clientMap["total_bytes"] = stat.CurrentTotal
						clientMap["last_seen"] = stat.LastSeen.Format(time.RFC3339)
						clientMap["last_connection"] = stat.LastSeen.Format(time.RFC3339)
						clientMap["connected"] = isCurrentlyConnected
						clientMap["active"] = isCurrentlyConnected
						clientMap["daily_usage"] = stat.DailyUsage
						clientMap["monthly_usage"] = stat.MonthlyUsage
					} else {
						clientMap["connected"] = isCurrentlyConnected
						clientMap["active"] = isCurrentlyConnected
						if isCurrentlyConnected {
							if connTime, ok := connectedSince[name]; ok && !connTime.IsZero() {
								clientMap["last_seen"] = connTime.Format(time.RFC3339)
								clientMap["last_connection"] = connTime.Format(time.RFC3339)
								clientMap["connected_since"] = connTime.Format(time.RFC3339)
							} else {
								clientMap["last_seen"] = time.Now().Format(time.RFC3339)
								clientMap["last_connection"] = time.Now().Format(time.RFC3339)
								clientMap["connected_since"] = time.Now().Format(time.RFC3339)
							}
						} else {
							if total, exists := cumulativeTotals[name]; exists {
								lastSeen := total.LastUpdate

								if !lastSeen.IsZero() {
									clientMap["last_seen"] = lastSeen.Format(time.RFC3339)
									clientMap["last_connection"] = lastSeen.Format(time.RFC3339)
								} else {
									clientMap["last_seen"] = ""
									clientMap["last_connection"] = ""
								}

								received := total.TotalReceived + total.LastSessionReceived
								sent := total.TotalSent + total.LastSessionSent

								clientMap["bytes_received"] = received
								clientMap["bytes_sent"] = sent
								clientMap["total_bytes"] = received + sent
							} else {
								clientMap["last_seen"] = ""
								clientMap["last_connection"] = ""
							}
						}
						if _, exists := clientMap["bytes_received"]; !exists {
							clientMap["bytes_received"] = 0
						}
						if _, exists := clientMap["bytes_sent"]; !exists {
							clientMap["bytes_sent"] = 0
						}
						if _, exists := clientMap["total_bytes"]; !exists {
							clientMap["total_bytes"] = 0
						}
						clientMap["daily_usage"] = int64(0)
						clientMap["monthly_usage"] = int64(0)
					}
					clients[i] = clientMap
				}
			}
		}
	}
	cacheClientsIfCurrent(cacheGeneration, clients)
	sendSuccess(w, "Clients retrieved successfully", clients)
}

// IRANGATE_CLIENT_METADATA_ATOMIC_STAGE7C
func persistCreatedClientMetadata(
	clientPath string,
	expirationDays int,
	trafficLimitBytes int64,
	connectionLimit int,
	email string,
	nodeIDs []string,
	portLocationIDs []string,
) error {
	data, err := os.ReadFile(clientPath)
	if err != nil {
		return fmt.Errorf(
			"read created client metadata: %w",
			err,
		)
	}

	var clientData map[string]interface{}

	if err := json.Unmarshal(
		data,
		&clientData,
	); err != nil {
		return fmt.Errorf(
			"parse created client metadata: %w",
			err,
		)
	}

	if clientData == nil {
		clientData = make(map[string]interface{})
	}

	if expirationDays > 0 {
		clientData["expires_at"] =
			time.Now().
				UTC().
				AddDate(0, 0, expirationDays).
				Format(time.RFC3339)
	}

	if trafficLimitBytes > 0 {
		clientData["traffic_limit_bytes"] =
			trafficLimitBytes
	}

	clientData["connection_limit"] =
		connectionLimit

	if strings.TrimSpace(email) != "" {
		clientData["email"] =
			strings.TrimSpace(email)
	}

	selectedNodes := []string{
		"local",
	}

	seenNodes := map[string]bool{
		"local": true,
	}

	for _, rawNodeID := range nodeIDs {
		nodeID := normalizeNodeID(rawNodeID)

		if nodeID == "" || seenNodes[nodeID] {
			continue
		}

		seenNodes[nodeID] = true

		selectedNodes = append(
			selectedNodes,
			nodeID,
		)
	}

	clientData["nodes"] = selectedNodes

	clientData["openvpn_port_locations"] =
		append(
			[]string(nil),
			portLocationIDs...,
		)

	payload, err := json.MarshalIndent(
		clientData,
		"",
		"  ",
	)
	if err != nil {
		return fmt.Errorf(
			"encode created client metadata: %w",
			err,
		)
	}

	tempPath :=
		clientPath +
			fmt.Sprintf(
				".tmp-%d",
				time.Now().UnixNano(),
			)

	if err := os.WriteFile(
		tempPath,
		payload,
		0644,
	); err != nil {
		return fmt.Errorf(
			"write temporary client metadata: %w",
			err,
		)
	}

	if err := os.Chmod(
		tempPath,
		0644,
	); err != nil {
		_ = os.Remove(tempPath)

		return fmt.Errorf(
			"chmod temporary client metadata: %w",
			err,
		)
	}

	if err := os.Rename(
		tempPath,
		clientPath,
	); err != nil {
		_ = os.Remove(tempPath)

		return fmt.Errorf(
			"commit created client metadata: %w",
			err,
		)
	}

	return nil
}

func createClientHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                 string   `json:"name"`
		Email                string   `json:"email,omitempty"`
		ExpirationDays       int      `json:"expiration_days,omitempty"`
		TrafficLimitBytes    int64    `json:"traffic_limit_bytes,omitempty"`
		ConnectionLimit      int      `json:"connection_limit,omitempty"`
		Nodes                []string `json:"nodes,omitempty"`
		OpenVPNPortLocations []string `json:"openvpn_port_locations,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Keep all human-facing traffic quotas on the NET SPOOF decimal-SI
	// contract, while accepting legacy callers that still send MiB/GiB/TiB.
	req.TrafficLimitBytes = canonicalTrafficQuotaBytes(req.TrafficLimitBytes)

	// IRANGATE_CASE_SAFE_CREATE_V1
	req.Name = canonicalClientNameForCreate(req.Name)
	clientCreateIdentityMu.Lock()
	defer clientCreateIdentityMu.Unlock()

	if existing, collision, err := findCaseInsensitiveClientCollision(req.Name); err != nil {
		sendError(w, "Failed to validate client-name uniqueness", http.StatusInternalServerError)
		return
	} else if collision {
		sendError(
			w,
			fmt.Sprintf("Client name conflicts with existing client %q", existing),
			http.StatusConflict,
		)
		return
	}

	selectedPortLocationIDs, err :=
		validateOpenVPNPortLocationSelection(
			req.OpenVPNPortLocations,
		)

	if err != nil {
		sendError(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	if req.ConnectionLimit <= 0 {
		req.ConnectionLimit = 1
	}
	if req.ConnectionLimit > 100 {
		sendError(w, "Connection limit must be between 1 and 100", http.StatusBadRequest)
		return
	}

	cmd := exec.Command("irangate", "client", "add", req.Name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to create client: "+string(output), http.StatusInternalServerError)
		return
	}

	dataDir := os.Getenv("IRANGATE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientPath := filepath.Join(
		dataDir,
		"database",
		"clients",
		req.Name+".json",
	)

	if err := persistCreatedClientMetadata(
		clientPath,
		req.ExpirationDays,
		req.TrafficLimitBytes,
		req.ConnectionLimit,
		req.Email,
		req.Nodes,
		selectedPortLocationIDs,
	); err != nil {

		rollbackOutput, rollbackErr :=
			exec.Command(
				"irangate",
				"client",
				"remove",
				req.Name,
			).CombinedOutput()

		log.Printf(
			"created client metadata failed for %s: %v; rollback=%v output=%s",
			req.Name,
			err,
			rollbackErr,
			strings.TrimSpace(
				string(rollbackOutput),
			),
		)

		sendError(
			w,
			"Failed to save client metadata: "+
				err.Error(),
			http.StatusInternalServerError,
		)

		return
	}

	ikev2Cfg, ikev2Err := ensureIKEv2Account(req.Name, true)
	ikev2Result := map[string]interface{}{
		"enabled": false,
	}
	if ikev2Err != nil {
		log.Printf("IKEv2 create failed for %s: %v", req.Name, ikev2Err)
		ikev2Result["error"] = ikev2Err.Error()
	} else {
		ikev2Result = map[string]interface{}{
			"enabled":    true,
			"type":       "IKEv2/IPSec MSCHAPv2",
			"server":     ikev2Cfg.Server,
			"identifier": ikev2Cfg.Identifier,
			"username":   ikev2Cfg.Username,
			"password":   ikev2Cfg.Password,
		}
	}

	nodeResults := []map[string]interface{}{}
	for _, node := range activeRemoteNodesByIDs(req.Nodes) {
		err := createClientOnNode(node, req.Name)
		res := map[string]interface{}{
			"id":       node.ID,
			"name":     node.Name,
			"location": node.Location,
			"success":  err == nil,
		}
		if err != nil {
			res["error"] = err.Error()
		}
		nodeResults = append(nodeResults, res)
	}

	invalidateClientsCache()

	sendSuccess(w, "Client created successfully", map[string]interface{}{
		"name":                   req.Name,
		"nodes":                  nodeResults,
		"openvpn_port_locations": selectedPortLocationIDs,
		"ikev2":                  ikev2Result,
	})
}
func deleteClientHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	cmd := exec.Command("irangate", "client", "remove", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to remove client: "+string(output), http.StatusInternalServerError)
		return
	}

	if output, err := exec.Command("/usr/local/sbin/irangate-ikev2", "del", name).CombinedOutput(); err != nil {
		log.Printf("IKEv2 delete failed for %s: %v output=%s", name, err, string(output))
	}

	if err :=
		killOpenVPNClientEverywhere(
			name,
		); err != nil {

		log.Printf(
			"deleted client local session cleanup failed for %s: %v",
			name,
			err,
		)
	}

	invalidateClientsCache()
	sendSuccess(w, "Client removed and certificate revoked", map[string]string{"name": name})
}

// IRANGATE_TRAFFIC_RESET_ATOMIC_STAGE7C
func resetClientTrafficDisplay(
	clientName string,
) error {
	now := time.Now().
		UTC().
		Format(time.RFC3339)

	patchListFile := func(
		path string,
		createClient bool,
		patch func(map[string]interface{}),
	) error {
		records :=
			make(
				[]map[string]interface{},
				0,
			)

		data, err := os.ReadFile(path)

		if err == nil {
			if strings.TrimSpace(
				string(data),
			) != "" {

				if err := json.Unmarshal(
					data,
					&records,
				); err != nil {
					return fmt.Errorf(
						"parse %s: %w",
						path,
						err,
					)
				}
			}

		} else if !os.IsNotExist(err) {
			return fmt.Errorf(
				"read %s: %w",
				path,
				err,
			)
		}

		changed := false

		for _, record := range records {
			name :=
				strings.TrimSpace(
					fmt.Sprintf(
						"%v",
						record["client_name"],
					),
				)

			if !strings.EqualFold(
				name,
				clientName,
			) {
				continue
			}

			patch(record)
			changed = true
		}

		if !changed && createClient {
			record :=
				map[string]interface{}{
					"client_name": clientName,
				}

			patch(record)

			records = append(
				records,
				record,
			)

			changed = true
		}

		if !changed {
			return nil
		}

		payload, err := json.MarshalIndent(
			records,
			"",
			"  ",
		)
		if err != nil {
			return fmt.Errorf(
				"encode %s: %w",
				path,
				err,
			)
		}

		if err := os.MkdirAll(
			filepath.Dir(path),
			0755,
		); err != nil {
			return fmt.Errorf(
				"create traffic directory: %w",
				err,
			)
		}

		tempPath :=
			path +
				fmt.Sprintf(
					".tmp-%d",
					time.Now().UnixNano(),
				)

		if err := os.WriteFile(
			tempPath,
			payload,
			0644,
		); err != nil {
			return fmt.Errorf(
				"write temporary %s: %w",
				path,
				err,
			)
		}

		if err := os.Rename(
			tempPath,
			path,
		); err != nil {
			_ = os.Remove(tempPath)

			return fmt.Errorf(
				"commit %s: %w",
				path,
				err,
			)
		}

		return nil
	}

	currentPath := filepath.Join(
		DataBaseDir,
		"traffic",
		"current_stats.json",
	)

	if err := patchListFile(
		currentPath,
		false,
		func(record map[string]interface{}) {
			record["current_received"] = 0
			record["current_sent"] = 0
			record["current_total"] = 0
			record["daily_usage"] = 0
			record["monthly_usage"] = 0
			record["quota_used_percent"] = 0
			record["connected"] = false
			record["last_seen"] = now
		},
	); err != nil {
		log.Printf(
			"current traffic display reset warning for %s: %v",
			clientName,
			err,
		)
	}

	cumulativePath := filepath.Join(
		DataBaseDir,
		"traffic",
		"cumulative_totals.json",
	)

	if err := patchListFile(
		cumulativePath,
		true,
		func(record map[string]interface{}) {
			record["total_received"] = 0
			record["total_sent"] = 0
			record["last_session_received"] = 0
			record["last_session_sent"] = 0
			record["last_update"] = now
		},
	); err != nil {
		return fmt.Errorf(
			"reset cumulative OpenVPN traffic: %w",
			err,
		)
	}

	return nil
}

func enforceClientAfterEdit(name string, resetTraffic bool) {
	if resetTraffic {
		_ = exec.Command("/usr/local/bin/irangate-connection-guard", "--kick", name).Run()
		return
	}

	go func() {
		_ = exec.Command("/usr/local/bin/irangate-connection-guard", "--once", name).Run()
	}()
}

func adsFinalInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case int32:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}

func adsFinalExpiresInFuture(clientData map[string]interface{}) bool {
	expiresRaw, ok := clientData["expires_at"]
	if !ok {
		return true
	}

	expiresStr := fmt.Sprintf("%v", expiresRaw)
	if expiresStr == "" || expiresStr == "<nil>" {
		return true
	}

	t, err := time.Parse(time.RFC3339, expiresStr)
	if err != nil {
		return true
	}

	return time.Now().UTC().Before(t)
}

func adsFinalReactivateAfterEdit(clientData map[string]interface{}, requestedActive *bool) {
	if requestedActive == nil || !*requestedActive {
		return
	}

	limit := adsFinalInt64(clientData["traffic_limit_bytes"])
	used := adsFinalInt64(clientData["traffic_used_bytes"])

	if used <= 0 {
		used = adsFinalInt64(clientData["upload"]) + adsFinalInt64(clientData["download"])
	}
	if used <= 0 {
		used = adsFinalInt64(clientData["bytes_sent"]) + adsFinalInt64(clientData["bytes_received"])
	}

	if (limit <= 0 || used < limit) && adsFinalExpiresInFuture(clientData) {
		clientData["active"] = true
		clientData["status"] = "active"
		delete(clientData, "traffic_exceeded_at")
	}
}

// sameClientSelection compares already-normalized, de-duplicated client
// location IDs without treating order changes as access changes.
func sameClientSelection(existing, requested []string) bool {
	if len(existing) != len(requested) {
		return false
	}
	known := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		known[id] = struct{}{}
	}
	for _, id := range requested {
		if _, ok := known[id]; !ok {
			return false
		}
	}
	return true
}

func updateClientHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := strings.TrimSpace(vars["name"])
	if name == "" {
		sendError(w, "Client name is required", http.StatusBadRequest)
		return
	}

	type UpdateClientRequest struct {
		ExpirationDays       *int      `json:"expiration_days,omitempty"`
		TrafficLimitBytes    *int64    `json:"traffic_limit_bytes,omitempty"`
		ConnectionLimit      *int      `json:"connection_limit,omitempty"`
		Active               *bool     `json:"active,omitempty"`
		ResetTraffic         bool      `json:"reset_traffic,omitempty"`
		Nodes                *[]string `json:"nodes,omitempty"`
		OpenVPNPortLocations *[]string `json:"openvpn_port_locations,omitempty"`
	}

	var req UpdateClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.TrafficLimitBytes != nil {
		normalized := canonicalTrafficQuotaBytes(*req.TrafficLimitBytes)
		req.TrafficLimitBytes = &normalized
	}

	clientPath := filepath.Join(DataBaseDir, "database", "clients", name+".json")
	data, err := os.ReadFile(clientPath)
	if err != nil {
		sendError(w, "Client not found", http.StatusNotFound)
		return
	}

	var clientData map[string]interface{}
	if err := json.Unmarshal(data, &clientData); err != nil {
		sendError(w, "Failed to parse client data", http.StatusInternalServerError)
		return
	}

	previousNodeIDs :=
		nodeIDsFromClientData(
			clientData,
		)
	previousPortLocationIDs := openVPNPortLocationIDsFromClientData(clientData)
	nodesChanged := false
	portLocationsChanged := false

	var selectedPortLocationIDs []string

	if req.OpenVPNPortLocations != nil {
		selectedPortLocationIDs, err =
			validateOpenVPNPortLocationSelection(
				*req.OpenVPNPortLocations,
			)

		if err != nil {
			sendError(
				w,
				err.Error(),
				http.StatusBadRequest,
			)
			return
		}

		portLocationsChanged = !sameClientSelection(previousPortLocationIDs, selectedPortLocationIDs)
		if portLocationsChanged {
			clientData["openvpn_port_locations"] = selectedPortLocationIDs
		}
	}

	if req.ExpirationDays != nil {
		if *req.ExpirationDays <= 0 || *req.ExpirationDays > 3650 {
			sendError(w, "Expiration days must be between 1 and 3650", http.StatusBadRequest)
			return
		}
		expiresAt := time.Now().UTC().AddDate(0, 0, *req.ExpirationDays)
		clientData["expires_at"] = expiresAt.Format(time.RFC3339)
	}

	if req.TrafficLimitBytes != nil {
		if *req.TrafficLimitBytes < 0 {
			sendError(w, "Traffic limit cannot be negative", http.StatusBadRequest)
			return
		}
		clientData["traffic_limit_bytes"] = *req.TrafficLimitBytes
	}

	if req.ConnectionLimit != nil {
		if *req.ConnectionLimit < 1 || *req.ConnectionLimit > 100 {
			sendError(w, "Connection limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
		clientData["connection_limit"] = *req.ConnectionLimit
	}

	if req.Nodes != nil {
		nf, err := loadNodesFile()
		if err != nil {
			sendError(
				w,
				"Failed to load available nodes",
				http.StatusInternalServerError,
			)
			return
		}

		allowedNodes := make(map[string]bool)

		for _, node := range nf.Nodes {
			id := normalizeNodeID(node.ID)

			if id == "" || !node.Enabled {
				continue
			}

			allowedNodes[id] = true
		}

		selectedNodes := make(
			[]string,
			0,
			len(*req.Nodes),
		)

		seenNodes := make(map[string]bool)

		for _, rawID := range *req.Nodes {
			id := normalizeNodeID(rawID)

			if id == "" || !allowedNodes[id] {
				sendError(
					w,
					fmt.Sprintf(
						"Invalid or disabled node: %s",
						rawID,
					),
					http.StatusBadRequest,
				)
				return
			}

			if seenNodes[id] {
				continue
			}

			seenNodes[id] = true

			selectedNodes = append(
				selectedNodes,
				id,
			)
		}

		if len(selectedNodes) == 0 {
			sendError(
				w,
				"At least one node must be selected",
				http.StatusBadRequest,
			)
			return
		}

		nodesChanged = !sameClientSelection(previousNodeIDs, selectedNodes)
		if nodesChanged {
			clientData["nodes"] = selectedNodes
		}
	}

	if req.ResetTraffic {
		clientData["traffic_used_bytes"] = 0
		clientData["bytes_sent"] = 0
		clientData["bytes_received"] = 0
		clientData["upload"] = 0
		clientData["download"] = 0
		clientData["data_used_mb"] = 0
		clientData["active"] = false
		clientData["status"] = "inactive"
		delete(clientData, "traffic_exceeded_at")
		if err := resetClientTrafficDisplay(
			name,
		); err != nil {
			sendError(
				w,
				"Failed to reset traffic state: "+
					err.Error(),
				http.StatusInternalServerError,
			)
			return
		}
	}

	if req.Active != nil {
		clientData["active"] = *req.Active
		if *req.Active {
			clientData["status"] = "active"
			if req.ResetTraffic {
				delete(clientData, "traffic_exceeded_at")
			}
		} else {
			clientData["status"] = "inactive"
		}
	}

	adsFinalReactivateAfterEdit(clientData, req.Active)

	clientData["last_update"] = time.Now().UTC().Format(time.RFC3339)

	out, err := json.MarshalIndent(clientData, "", "  ")
	if err != nil {
		sendError(w, "Failed to encode client data", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(clientPath, out, 0644); err != nil {
		sendError(w, "Failed to save client data", http.StatusInternalServerError)
		return
	}

	// A quota/time edit must never depend on the availability of remote nodes.
	// Only change ACLs when the requested node selection actually differs.
	if nodesChanged {
		if err := syncClientNodeACL(
			name,
			*req.Nodes,
		); err != nil {

			rollbackErr :=
				os.WriteFile(
					clientPath,
					data,
					0644,
				)

			aclRollbackErr :=
				syncClientNodeACL(
					name,
					previousNodeIDs,
				)

			log.Printf(
				"node ACL update failed for %s: %v; file rollback=%v; ACL rollback=%v",
				name,
				err,
				rollbackErr,
				aclRollbackErr,
			)

			sendError(
				w,
				"Failed to apply node access: "+err.Error(),
				http.StatusBadGateway,
			)

			return
		}
	}

	// Preserve live OpenVPN sessions when only quota, expiry or connection
	// limits were edited (even if a legacy client re-sends the same locations).
	if portLocationsChanged {
		if err :=
			killOpenVPNClientEverywhere(
				name,
			); err != nil {

			log.Printf(
				"port-location session refresh failed for %s: %v",
				name,
				err,
			)
		}
	}

	// IRANGATE_PORT_LOCATION_CLIENT_RUNTIME_STAGE5C
	invalidateClientsCache()

	go func() {
		if err := exec.Command(
			"systemctl",
			"start",
			"irangate-pg-sync-clients.service",
		).Run(); err != nil {

			log.Printf(
				"client node PostgreSQL sync trigger failed for %s: %v",
				name,
				err,
			)
		}
	}()

	enforceClientAfterEdit(name, req.ResetTraffic)

	sendSuccess(w, "Client updated successfully", clientData)
}

func activateClientHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if err := ActivateClient(name); err != nil {
		sendError(w, fmt.Sprintf("Failed to activate client: %v", err), http.StatusInternalServerError)
		return
	}

	invalidateClientsCache()

	sendSuccess(w, "Client activated successfully", map[string]string{"name": name, "status": "active"})
}
func deactivateClientHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if err := RevokeClientCertificate(name, "manual_deactivation"); err != nil {
		log.Printf("Warning: Failed to revoke certificate for %s: %v", name, err)
	}

	if err := DeactivateClient(name); err != nil {
		sendError(w, fmt.Sprintf("Failed to deactivate client: %v", err), http.StatusInternalServerError)
		return
	}

	invalidateClientsCache()

	sendSuccess(w, "Client deactivated successfully", map[string]string{"name": name, "status": "inactive"})
}
func getClientStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	cmd := exec.Command("irangate", "client", "status", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to get client status", http.StatusInternalServerError)
		return
	}
	status := strings.TrimSpace(string(output))
	sendSuccess(w, "Client status retrieved", map[string]string{
		"name":   name,
		"status": status,
	})
}
func exportClientHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	cmd := exec.Command("irangate", "client", "export", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to export client config", http.StatusInternalServerError)
		return
	}
	lines := strings.Split(string(output), "\n")
	var cleanedLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "time=") ||
			strings.HasPrefix(trimmed, "level=") ||
			(strings.Contains(trimmed, "time=") && strings.Contains(trimmed, "level=") && strings.Contains(trimmed, "msg=")) {
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}
	cleanedOutput := strings.Join(cleanedLines, "\n")
	w.Header().Set("Content-Type", "application/x-openvpn-profile")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.ovpn", name))
	w.Write([]byte(cleanedOutput))
}
func getOrphanedClientsHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "client", "list-orphaned", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to get orphaned clients", http.StatusInternalServerError)
		return
	}
	var result interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		sendError(w, "Invalid orphaned certificate scan output", http.StatusInternalServerError)
		return
	}
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		sendError(w, "Invalid orphaned certificate scan response", http.StatusInternalServerError)
		return
	}
	count, ok := resultMap["count"].(float64)
	if !ok || count < 0 || count != float64(int(count)) {
		sendError(w, "Invalid orphaned certificate count", http.StatusInternalServerError)
		return
	}
	orphaned, ok := resultMap["orphaned"].([]interface{})
	if !ok || len(orphaned) != int(count) {
		sendError(w, "Invalid orphaned certificate list", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Orphaned clients retrieved", result)
}
func cleanupOrphanedHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "client", "cleanup-orphaned", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to cleanup orphaned clients", http.StatusInternalServerError)
		return
	}
	var result interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		sendError(w, "Failed to parse cleanup result", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Orphaned clients cleaned up", result)
}
func getConfigHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "config", "show", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to get configuration", http.StatusInternalServerError)
		return
	}
	var config interface{}
	if err := json.Unmarshal(output, &config); err != nil {
		sendError(w, "Failed to parse configuration", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Configuration retrieved", config)
}
func updateConfigHandler(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	for key, value := range req {
		cmd := exec.Command("irangate", "config", "set", key, value)
		if _, err := cmd.CombinedOutput(); err != nil {
			sendError(w, fmt.Sprintf("Failed to update %s", key), http.StatusInternalServerError)
			return
		}
	}
	sendSuccess(w, "Configuration updated successfully", nil)
}
func getStatsHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "monitor", "live", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to get statistics", http.StatusInternalServerError)
		return
	}
	var stats interface{}
	if err := json.Unmarshal(output, &stats); err != nil {
		sendError(w, "Failed to parse statistics", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Statistics retrieved", stats)
}
func getServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to get server status", http.StatusInternalServerError)
		return
	}
	status := strings.TrimSpace(string(output))
	isRunning := strings.Contains(status, "running") || strings.Contains(status, "active")
	sendSuccess(w, "Server status retrieved", map[string]interface{}{
		"status":     status,
		"is_running": isRunning,
	})
}
func restartServerHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "restart")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to restart server: "+string(output), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server restarted successfully", nil)
}
func startServerHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "start")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to start server: "+string(output), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server started successfully", nil)
}
func stopServerHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "stop")
	output, err := cmd.CombinedOutput()
	if err != nil {
		sendError(w, "Failed to stop server: "+string(output), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server stopped successfully", nil)
}
func getAdminSettingsHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(AdminDBPath)
	if err != nil {
		sendError(w, "Failed to read admin settings", http.StatusInternalServerError)
		return
	}
	var admin Admin
	if err := json.Unmarshal(data, &admin); err != nil {
		sendError(w, "Failed to parse admin settings", http.StatusInternalServerError)
		return
	}
	admin.PasswordHash = ""
	sendSuccess(w, "Admin settings retrieved", admin)
}
func changePasswordHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(AdminDBPath)
	if err != nil {
		sendError(w, "Failed to read admin data", http.StatusInternalServerError)
		return
	}
	var admin Admin
	if err := json.Unmarshal(data, &admin); err != nil {
		sendError(w, "Failed to parse admin data", http.StatusInternalServerError)
		return
	}
	if !verifyPassword(req.OldPassword, admin.PasswordHash) {
		sendError(w, "Incorrect old password", http.StatusUnauthorized)
		return
	}
	admin.PasswordHash = hashPassword(req.NewPassword)
	data, err = json.MarshalIndent(admin, "", "  ")
	if err != nil {
		sendError(w, "Failed to serialize admin data", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(AdminDBPath, data, 0600); err != nil {
		sendError(w, "Failed to save admin data", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Password changed successfully", nil)
}
func getTelegramSettingsHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(TelegramDBPath)
	if err != nil {
		sendError(w, "Failed to read telegram settings", http.StatusInternalServerError)
		return
	}
	var telegram TelegramConfig
	if err := json.Unmarshal(data, &telegram); err != nil {
		sendError(w, "Failed to parse telegram settings", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Telegram settings retrieved", telegram)
}

func normalizeNodeID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	v = strings.ReplaceAll(v, "_", "-")
	if v == "" {
		v = fmt.Sprintf("node-%d", time.Now().Unix())
	}
	return v
}

func loadNodesFile() (NodesFile, error) {
	var nf NodesFile

	data, err := os.ReadFile(NodesDBPath)
	if os.IsNotExist(err) {
		now := time.Now().Format(time.RFC3339)
		nf.Nodes = []NodeConfig{
			{
				ID:        "local",
				Name:      "Germany Master",
				Flag:      "🇩🇪",
				Location:  "DE",
				PublicIP:  "",
				AgentURL:  "local",
				Token:     "",
				Enabled:   true,
				IsLocal:   true,
				CreatedAt: now,
				UpdatedAt: now,
			},
		}
		_ = saveNodesFile(nf)
		return nf, nil
	}
	if err != nil {
		return nf, err
	}
	if len(data) == 0 {
		nf.Nodes = []NodeConfig{}
		return nf, nil
	}
	if err := json.Unmarshal(data, &nf); err != nil {
		return nf, err
	}
	if nf.Nodes == nil {
		nf.Nodes = []NodeConfig{}
	}
	return nf, nil
}

func saveNodesFile(nf NodesFile) error {
	if err := os.MkdirAll(filepath.Dir(NodesDBPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(nf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(NodesDBPath, data, 0600)
}

func findNodeConfig(id string) (NodeConfig, bool) {
	nf, err := loadNodesFile()
	if err != nil {
		return NodeConfig{}, false
	}

	for _, node := range nf.Nodes {
		if node.ID == id {
			return node, true
		}
	}

	return NodeConfig{}, false
}

func activeRemoteNodesByIDs(ids []string) []NodeConfig {
	nf, err := loadNodesFile()
	if err != nil {
		return nil
	}

	wanted := map[string]bool{}
	for _, id := range ids {
		id = normalizeNodeID(id)
		if id != "" && id != "local" {
			wanted[id] = true
		}
	}

	if len(wanted) == 0 {
		return nil
	}

	out := []NodeConfig{}
	for _, node := range nf.Nodes {
		if node.IsLocal || node.AgentURL == "local" || !node.Enabled {
			continue
		}

		if wanted[node.ID] {
			out = append(out, node)
		}
	}

	return out
}

func clientHasNode(clientData map[string]interface{}, nodeID string) bool {
	nodeID = normalizeNodeID(nodeID)
	if nodeID == "" {
		return false
	}

	raw, ok := clientData["nodes"]
	if !ok {
		return nodeID == "local"
	}

	if arr, ok := raw.([]interface{}); ok {
		for _, v := range arr {
			if normalizeNodeID(fmt.Sprintf("%v", v)) == nodeID {
				return true
			}
		}
	}

	if arr, ok := raw.([]string); ok {
		for _, v := range arr {
			if normalizeNodeID(v) == nodeID {
				return true
			}
		}
	}

	return false
}

func createClientOnNode(node NodeConfig, clientName string) error {
	payload, err := json.Marshal(map[string]string{"name": clientName})
	if err != nil {
		return fmt.Errorf("encode node create request: %w", err)
	}

	body := strings.NewReader(string(payload))

	req, err := http.NewRequest("POST", strings.TrimRight(node.AgentURL, "/")+"/clients", body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if node.Token != "" {
		req.Header.Set("Authorization", "Bearer "+node.Token)
	}

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("node %s create failed: HTTP %d %s", node.ID, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(respBody, &parsed); err == nil {
		if ok, exists := parsed["success"].(bool); exists && !ok {
			return fmt.Errorf("node %s create failed: %v", node.ID, parsed["message"])
		}
	}

	return nil
}

func ensureClientOnNode(node NodeConfig, clientName string) error {
	return createClientOnNode(node, clientName)
}

func configuredOpenVPNDownloadHost() (string, error) {
	paths := []string{
		ServerDBPath,
		filepath.Join(DataBaseDir, "server_config.json"),
		filepath.Join(DataBaseDir, "database", "settings.json"),
	}

	validateHost := func(host string) (string, error) {
		host = strings.TrimSpace(host)

		if host == "" {
			return "", nil
		}

		if strings.Contains(host, "://") ||
			strings.ContainsAny(host, " \t\r\n/") {
			return "", fmt.Errorf(
				"invalid configured OpenVPN public host",
			)
		}

		return host, nil
	}

	for _, configPath := range paths {
		data, err := os.ReadFile(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return "", fmt.Errorf(
				"read OpenVPN server settings %s: %w",
				configPath,
				err,
			)
		}

		var config map[string]interface{}
		if err := json.Unmarshal(data, &config); err != nil {
			continue
		}

		serverIP := strings.TrimSpace(
			fmt.Sprintf("%v", config["server_ip"]),
		)
		serverDomain := strings.TrimSpace(
			fmt.Sprintf("%v", config["server_domain"]),
		)

		if serverIP == "<nil>" {
			serverIP = ""
		}
		if serverDomain == "<nil>" {
			serverDomain = ""
		}

		useDomain := false

		if settings, ok := config["settings"].(map[string]interface{}); ok {
			if value, ok := settings["use_domain"].(bool); ok {
				useDomain = value
			}
		}

		if value, ok := config["use_domain"].(bool); ok {
			useDomain = value
		}

		if useDomain && serverDomain != "" {
			return validateHost(serverDomain)
		}

		if serverIP != "" {
			return validateHost(serverIP)
		}

		if serverDomain != "" {
			return validateHost(serverDomain)
		}
	}

	return "", fmt.Errorf(
		"OpenVPN public IP or domain is not configured",
	)
}

func rewriteOpenVPNProfileHost(
	profile []byte,
	host string,
) ([]byte, int, error) {
	host = strings.TrimSpace(host)

	if host == "" ||
		strings.Contains(host, "://") ||
		strings.ContainsAny(host, " \t\r\n/") {
		return nil, 0, fmt.Errorf(
			"invalid OpenVPN public host",
		)
	}

	lines := strings.Split(string(profile), "\n")
	updated := 0

	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		fields := strings.Fields(trimmed)

		if len(fields) < 2 ||
			strings.ToLower(fields[0]) != "remote" {
			continue
		}

		indentLength := len(line) - len(trimmed)

		replacement := line[:indentLength] +
			"remote " +
			host

		if len(fields) > 2 {
			replacement += " " +
				strings.Join(fields[2:], " ")
		}

		if replacement == line {
			continue
		}

		lines[index] = replacement
		updated++
	}

	return []byte(strings.Join(lines, "\n")), updated, nil
}

func writeOpenVPNProfile(
	w http.ResponseWriter,
	filename string,
	profile []byte,
) {
	w.Header().Set(
		"Content-Type",
		"application/x-openvpn-profile",
	)
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(
			"attachment; filename=\"%s\"",
			filename,
		),
	)

	// Never allow an old downloaded profile to be reused from cache.
	w.Header().Set(
		"Cache-Control",
		"no-store, no-cache, must-revalidate, max-age=0",
	)
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	_, _ = w.Write(profile)
}

func downloadClientFromNode(
	w http.ResponseWriter,
	r *http.Request,
	node NodeConfig,
	clientName string,
) {
	if err := ensureClientOnNode(node, clientName); err != nil {
		http.Error(
			w,
			"Node client create failed: "+err.Error(),
			http.StatusBadGateway,
		)
		return
	}

	req, err := http.NewRequest(
		"GET",
		strings.TrimRight(node.AgentURL, "/")+
			"/clients/"+
			clientName+
			"/download",
		nil,
	)
	if err != nil {
		http.Error(
			w,
			"Invalid node agent URL",
			http.StatusBadRequest,
		)
		return
	}

	if node.Token != "" {
		req.Header.Set(
			"Authorization",
			"Bearer "+node.Token,
		)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(
			w,
			"Node download failed: "+err.Error(),
			http.StatusBadGateway,
		)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(
			io.LimitReader(
				resp.Body,
				2048,
			),
		)

		http.Error(
			w,
			fmt.Sprintf(
				"Node download failed: HTTP %d %s",
				resp.StatusCode,
				strings.TrimSpace(string(body)),
			),
			http.StatusBadGateway,
		)
		return
	}

	profile, err := io.ReadAll(
		io.LimitReader(
			resp.Body,
			32<<20,
		),
	)
	if err != nil {
		http.Error(
			w,
			"Failed to read node config: "+err.Error(),
			http.StatusBadGateway,
		)
		return
	}

	// Preserve the host and port generated by the node.
	// Node profiles must not inherit the master public host.
	log.Printf(
		"Subscription node profile preserved: client=%s node=%s",
		clientName,
		node.ID,
	)

	safeName := strings.NewReplacer(
		"\\", "_",
		"\"", "_",
		"\r", "_",
		"\n", "_",
	).Replace(clientName)

	safeNode := strings.NewReplacer(
		"\\", "_",
		"\"", "_",
		"\r", "_",
		"\n", "_",
	).Replace(node.ID)

	writeOpenVPNProfile(
		w,
		safeName+"-"+safeNode+".ovpn",
		profile,
	)
}

func getNodesHandler(w http.ResponseWriter, r *http.Request) {
	nf, err := loadNodesFile()
	if err != nil {
		sendError(w, "Failed to read nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Nodes retrieved successfully", nf.Nodes)
}

func createNodeHandler(w http.ResponseWriter, r *http.Request) {
	var node NodeConfig
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	node.ID = normalizeNodeID(node.ID)
	if node.Name == "" {
		node.Name = node.ID
	}
	if node.Location == "" {
		node.Location = strings.ToUpper(node.ID)
	}
	if node.Flag == "" {
		node.Flag = node.Location
	}
	if node.AgentURL == "" {
		sendError(w, "Agent URL is required", http.StatusBadRequest)
		return
	}
	node.AgentURL = strings.TrimRight(node.AgentURL, "/")
	if !node.Enabled {
		node.Enabled = true
	}

	now := time.Now().Format(time.RFC3339)
	node.CreatedAt = now
	node.UpdatedAt = now

	nf, err := loadNodesFile()
	if err != nil {
		sendError(w, "Failed to read nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, existing := range nf.Nodes {
		if existing.ID == node.ID {
			sendError(w, "Node already exists", http.StatusConflict)
			return
		}
	}

	nf.Nodes = append(nf.Nodes, node)
	if err := saveNodesFile(nf); err != nil {
		sendError(w, "Failed to save node: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sendSuccess(w, "Node created successfully", node)
}

func updateNodeHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var patch NodeConfig
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	nf, err := loadNodesFile()
	if err != nil {
		sendError(w, "Failed to read nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for i := range nf.Nodes {
		if nf.Nodes[i].ID == id {
			if nf.Nodes[i].IsLocal {
				patch.IsLocal = true
				patch.AgentURL = "local"
				patch.ID = "local"
			} else {
				if patch.ID == "" {
					patch.ID = nf.Nodes[i].ID
				}
				patch.ID = normalizeNodeID(patch.ID)
				patch.AgentURL = strings.TrimRight(patch.AgentURL, "/")
			}

			if patch.Name == "" {
				patch.Name = nf.Nodes[i].Name
			}
			if patch.Location == "" {
				patch.Location = nf.Nodes[i].Location
			}
			if patch.Flag == "" {
				patch.Flag = nf.Nodes[i].Flag
			}
			if patch.Token == "" {
				patch.Token = nf.Nodes[i].Token
			}
			patch.CreatedAt = nf.Nodes[i].CreatedAt
			patch.UpdatedAt = time.Now().Format(time.RFC3339)

			nf.Nodes[i] = patch
			if err := saveNodesFile(nf); err != nil {
				sendError(w, "Failed to save node: "+err.Error(), http.StatusInternalServerError)
				return
			}
			sendSuccess(w, "Node updated successfully", patch)
			return
		}
	}

	sendError(w, "Node not found", http.StatusNotFound)
}

func deleteNodeHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	nf, err := loadNodesFile()
	if err != nil {
		sendError(w, "Failed to read nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out := make([]NodeConfig, 0, len(nf.Nodes))
	deleted := false
	for _, node := range nf.Nodes {
		if node.ID == id {
			if node.IsLocal {
				sendError(w, "Local node cannot be deleted", http.StatusBadRequest)
				return
			}
			deleted = true
			continue
		}
		out = append(out, node)
	}

	if !deleted {
		sendError(w, "Node not found", http.StatusNotFound)
		return
	}

	nf.Nodes = out
	if err := saveNodesFile(nf); err != nil {
		sendError(w, "Failed to save nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sendSuccess(w, "Node deleted successfully", map[string]string{"id": id})
}

func pingNodeHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	nf, err := loadNodesFile()
	if err != nil {
		sendError(w, "Failed to read nodes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for _, node := range nf.Nodes {
		if node.ID != id {
			continue
		}

		if node.IsLocal || node.AgentURL == "local" {
			sendSuccess(w, "Local node is available", map[string]interface{}{
				"id":      node.ID,
				"name":    node.Name,
				"status":  "ok",
				"isLocal": true,
			})
			return
		}

		req, err := http.NewRequest("GET", strings.TrimRight(node.AgentURL, "/")+"/status", nil)
		if err != nil {
			sendError(w, "Invalid agent URL", http.StatusBadRequest)
			return
		}
		if node.Token != "" {
			req.Header.Set("Authorization", "Bearer "+node.Token)
		}

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			sendError(w, "Node ping failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		var body interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)

		sendSuccess(w, "Node ping completed", map[string]interface{}{
			"id":          node.ID,
			"name":        node.Name,
			"status_code": resp.StatusCode,
			"response":    body,
		})
		return
	}

	sendError(w, "Node not found", http.StatusNotFound)
}

func getServerSettingsHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(ServerDBPath)
	if err != nil {
		sendError(w, "Failed to read server settings", http.StatusInternalServerError)
		return
	}
	var server ServerConfig
	if err := json.Unmarshal(data, &server); err != nil {
		sendError(w, "Failed to parse server settings", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server settings retrieved", server)
}

var serverSettingsUpdateMu sync.Mutex

func effectiveIKEv2ServerHost(server ServerConfig) string {
	if host := strings.TrimSpace(server.IKEv2ServerHost); host != "" {
		return host
	}

	if host := strings.TrimSpace(server.ServerDomain); host != "" {
		return host
	}

	return strings.TrimSpace(server.ServerIP)
}

func ikev2IdentitySyncAvailable() bool {
	required := []string{
		"/usr/local/sbin/irangate-ikev2-identity-sync",
		"/etc/swanctl/conf.d/irangate-ikev2-mschapv2.conf",
		"/etc/swanctl/x509/irangate-server-cert.pem",
		"/etc/swanctl/private/irangate-server-key.pem",
	}

	for _, path := range required {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}

	return true
}

func runIKEv2IdentityCommand(args ...string) (string, error) {
	const helper = "/usr/local/sbin/irangate-ikev2-identity-sync"

	ctx, cancel := context.WithTimeout(
		context.Background(),
		90*time.Second,
	)
	defer cancel()

	cmd := exec.CommandContext(ctx, helper, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))

	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("IKEv2 identity synchronization timed out")
	}

	if err != nil {
		if text == "" {
			text = err.Error()
		}

		return text, fmt.Errorf(
			"IKEv2 identity synchronization failed: %s",
			text,
		)
	}

	return text, nil
}

func currentIKEv2Identity() (string, error) {
	output, err := runIKEv2IdentityCommand("current")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

func syncIKEv2Identity(host string) error {
	host = strings.TrimSpace(host)

	if host == "" {
		return nil
	}

	_, err := runIKEv2IdentityCommand("sync", host)
	return err
}

func restoreServerSettingsFile(
	data []byte,
	existed bool,
) error {
	if existed {
		return os.WriteFile(ServerDBPath, data, 0600)
	}

	err := os.Remove(ServerDBPath)
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func rollbackServerSettingsUpdate(
	previous ServerConfig,
	previousData []byte,
	previousExists bool,
	previousIdentity string,
) error {
	var rollbackErrors []string

	if previousIdentity != "" {
		if err := syncIKEv2Identity(previousIdentity); err != nil {
			rollbackErrors = append(
				rollbackErrors,
				"restore IKEv2 identity: "+err.Error(),
			)
		}
	}

	if previousExists {
		if err := syncServerSettingsForCLI(previous); err != nil {
			rollbackErrors = append(
				rollbackErrors,
				"restore CLI settings: "+err.Error(),
			)
		}
	}

	if err := restoreServerSettingsFile(
		previousData,
		previousExists,
	); err != nil {
		rollbackErrors = append(
			rollbackErrors,
			"restore panel settings: "+err.Error(),
		)
	}

	if len(rollbackErrors) > 0 {
		return fmt.Errorf("%s", strings.Join(rollbackErrors, "; "))
	}

	return nil
}

func updateServerSettingsHandler(w http.ResponseWriter, r *http.Request) {
	serverSettingsUpdateMu.Lock()
	defer serverSettingsUpdateMu.Unlock()

	var server ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&server); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	server.ServerIP = strings.TrimSpace(server.ServerIP)
	server.ServerDomain = strings.TrimSpace(server.ServerDomain)
	server.IKEv2ServerHost = strings.TrimSpace(server.IKEv2ServerHost)
	server.Protocol = strings.TrimSpace(server.Protocol)

	data, err := json.MarshalIndent(server, "", "  ")
	if err != nil {
		sendError(
			w,
			"Failed to serialize server settings",
			http.StatusInternalServerError,
		)
		return
	}

	if err := os.MkdirAll(filepath.Dir(ServerDBPath), 0755); err != nil {
		sendError(
			w,
			"Failed to create webpanel config directory",
			http.StatusInternalServerError,
		)
		return
	}

	previousData, readErr := os.ReadFile(ServerDBPath)
	previousExists := readErr == nil

	if readErr != nil && !os.IsNotExist(readErr) {
		sendError(
			w,
			"Failed to read existing server settings",
			http.StatusInternalServerError,
		)
		return
	}

	var previous ServerConfig

	if previousExists {
		if err := json.Unmarshal(previousData, &previous); err != nil {
			sendError(
				w,
				"Failed to parse existing server settings",
				http.StatusInternalServerError,
			)
			return
		}
	}

	previousIdentity := ""
	newIdentity := effectiveIKEv2ServerHost(server)
	identityChanged := false

	if ikev2IdentitySyncAvailable() {
		previousIdentity, err = currentIKEv2Identity()
		if err != nil {
			sendError(
				w,
				err.Error(),
				http.StatusInternalServerError,
			)
			return
		}

		identityChanged =
			newIdentity != "" &&
				newIdentity != previousIdentity

		if identityChanged {
			if err := syncIKEv2Identity(newIdentity); err != nil {
				sendError(
					w,
					err.Error(),
					http.StatusInternalServerError,
				)
				return
			}
		}
	}

	failWithRollback := func(message string, cause error) {
		rollbackErr := rollbackServerSettingsUpdate(
			previous,
			previousData,
			previousExists,
			func() string {
				if identityChanged {
					return previousIdentity
				}

				return ""
			}(),
		)

		fullMessage := message + ": " + cause.Error()

		if rollbackErr != nil {
			fullMessage += "; rollback failed: " + rollbackErr.Error()
		}

		sendError(
			w,
			fullMessage,
			http.StatusInternalServerError,
		)
	}

	if err := syncServerSettingsForCLI(server); err != nil {
		failWithRollback(
			"Failed to sync server settings with CLI",
			err,
		)
		return
	}

	if err := os.WriteFile(ServerDBPath, data, 0600); err != nil {
		failWithRollback(
			"Failed to save server settings",
			err,
		)
		return
	}

	sendSuccess(
		w,
		"Server settings and IKEv2 identity updated successfully",
		server,
	)
}

func syncServerSettingsForCLI(server ServerConfig) error {
	now := time.Now().Format(time.RFC3339)

	serverIP := strings.TrimSpace(server.ServerIP)
	serverDomain := strings.TrimSpace(server.ServerDomain)
	ikev2ServerHost := strings.TrimSpace(server.IKEv2ServerHost)
	protocol := strings.ToLower(strings.TrimSpace(server.Protocol))

	serverConfigPath := filepath.Join(DataBaseDir, "server_config.json")
	serverConfig, err := readJSONMapAllowMissing(serverConfigPath)
	if err != nil {
		return err
	}

	if serverIP != "" {
		serverConfig["server_ip"] = serverIP
		serverConfig["auto_detect_ip"] = false
	}
	serverConfig["server_domain"] = serverDomain

	if ikev2ServerHost != "" {
		serverConfig["ikev2_server_host"] = ikev2ServerHost
	} else {
		delete(serverConfig, "ikev2_server_host")
	}

	if server.ServerPort > 0 {
		serverConfig["server_port"] = server.ServerPort
	}
	if protocol != "" {
		serverConfig["protocol"] = protocol
	}

	serverConfig["last_updated"] = now

	detectedIPs, _ := serverConfig["detected_ips"].(map[string]interface{})
	if detectedIPs == nil {
		detectedIPs = map[string]interface{}{}
		serverConfig["detected_ips"] = detectedIPs
	}
	if serverIP != "" {
		detectedIPs["primary"] = serverIP
		detectedIPs["manual"] = serverIP
	}

	settingsMap, _ := serverConfig["settings"].(map[string]interface{})
	if settingsMap == nil {
		settingsMap = map[string]interface{}{}
		serverConfig["settings"] = settingsMap
	}
	settingsMap["use_domain"] = serverDomain != ""
	settingsMap["fallback_to_ip"] = true
	settingsMap["force_manual_ip"] = serverIP != ""

	if err := writeJSONMap(serverConfigPath, serverConfig, 0644); err != nil {
		return err
	}

	dbSettingsPath := filepath.Join(DataBaseDir, "database", "settings.json")
	dbSettings, err := readJSONMapAllowMissing(dbSettingsPath)
	if err != nil {
		return err
	}

	if serverIP != "" {
		dbSettings["server_ip"] = serverIP
	}

	if ikev2ServerHost != "" {
		dbSettings["ikev2_server_host"] = ikev2ServerHost
	} else {
		delete(dbSettings, "ikev2_server_host")
	}

	if server.ServerPort > 0 {
		dbSettings["server_port"] = server.ServerPort
	}
	if protocol != "" {
		dbSettings["protocol"] = protocol
	}
	dbSettings["updated_at"] = now

	if err := writeJSONMap(dbSettingsPath, dbSettings, 0600); err != nil {
		return err
	}

	openVPNHost := serverIP
	if openVPNHost == "" {
		openVPNHost = serverDomain
	}

	if openVPNHost != "" {
		updated, err := syncExistingOpenVPNRemoteHost(openVPNHost)
		if err != nil {
			return fmt.Errorf(
				"sync existing OpenVPN client configs: %w",
				err,
			)
		}

		fmt.Printf(
			"Synced OpenVPN public host in %d existing client configs\n",
			updated,
		)
	}

	return nil
}

func syncExistingOpenVPNRemoteHost(host string) (int, error) {
	host = strings.TrimSpace(host)

	if host == "" {
		return 0, nil
	}

	if strings.Contains(host, "://") ||
		strings.ContainsAny(host, " \t\r\n/") {
		return 0, fmt.Errorf("invalid OpenVPN public host")
	}

	type configSnapshot struct {
		data []byte
		mode os.FileMode
	}

	roots := []string{
		"/etc/openvpn/client",
		"/etc/openvpn/clients",
		filepath.Join(DataBaseDir, "clients"),
	}

	snapshots := make(map[string]configSnapshot)
	files := make([]string, 0)

	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return 0, fmt.Errorf(
				"stat client config directory %s: %w",
				root,
				err,
			)
		}

		err := filepath.Walk(
			root,
			func(
				path string,
				info os.FileInfo,
				walkErr error,
			) error {
				if walkErr != nil {
					return walkErr
				}

				if info == nil ||
					info.IsDir() ||
					info.Mode()&os.ModeSymlink != 0 ||
					!strings.EqualFold(
						filepath.Ext(path),
						".ovpn",
					) {
					return nil
				}

				if _, exists := snapshots[path]; exists {
					return nil
				}

				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				snapshots[path] = configSnapshot{
					data: append([]byte(nil), data...),
					mode: info.Mode().Perm(),
				}

				files = append(files, path)

				return nil
			},
		)

		if err != nil {
			return 0, fmt.Errorf(
				"scan client config directory %s: %w",
				root,
				err,
			)
		}
	}

	changedPaths := make([]string, 0)

	rollback := func() {
		for index := len(changedPaths) - 1; index >= 0; index-- {
			path := changedPaths[index]
			snapshot := snapshots[path]

			temp := fmt.Sprintf(
				"%s.irangate-rollback-%d",
				path,
				os.Getpid(),
			)

			if err := os.WriteFile(
				temp,
				snapshot.data,
				snapshot.mode,
			); err != nil {
				continue
			}

			_ = os.Rename(temp, path)
		}
	}

	updated := 0

	for _, path := range files {
		snapshot := snapshots[path]
		lines := strings.Split(string(snapshot.data), "\n")
		changed := false

		for index, line := range lines {
			trimmed := strings.TrimLeft(line, " \t")
			fields := strings.Fields(trimmed)

			if len(fields) < 2 || fields[0] != "remote" {
				continue
			}

			indentLength := len(line) - len(trimmed)
			replacement := line[:indentLength] +
				"remote " +
				host

			if len(fields) > 2 {
				replacement += " " +
					strings.Join(fields[2:], " ")
			}

			if replacement == line {
				continue
			}

			lines[index] = replacement
			changed = true
		}

		if !changed {
			continue
		}

		output := []byte(strings.Join(lines, "\n"))

		temp := fmt.Sprintf(
			"%s.irangate-tmp-%d",
			path,
			os.Getpid(),
		)

		if err := os.WriteFile(
			temp,
			output,
			snapshot.mode,
		); err != nil {
			rollback()

			return updated, fmt.Errorf(
				"write temporary client config %s: %w",
				path,
				err,
			)
		}

		if err := os.Rename(temp, path); err != nil {
			_ = os.Remove(temp)
			rollback()

			return updated, fmt.Errorf(
				"replace client config %s: %w",
				path,
				err,
			)
		}

		changedPaths = append(changedPaths, path)
		updated++
	}

	return updated, nil
}

func readJSONMapAllowMissing(path string) (map[string]interface{}, error) {
	result := map[string]interface{}{}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	if strings.TrimSpace(string(data)) == "" {
		return result, nil
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	if result == nil {
		result = map[string]interface{}{}
	}

	return result, nil
}

func writeJSONMap(path string, value map[string]interface{}, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, perm)
}

func exportBackupHandler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("irangate", "config", "backup")
	if _, err := cmd.CombinedOutput(); err != nil {
		sendError(w, "Failed to create backup", http.StatusInternalServerError)
		return
	}
	backupDir := filepath.Join(DataBaseDir, "database", "backups")
	os.MkdirAll(backupDir, 0755)
	backupDir += "/"
	files, _ := os.ReadDir(backupDir)
	if len(files) == 0 {
		sendError(w, "No backup found", http.StatusNotFound)
		return
	}
	latestFile := files[len(files)-1]
	backupPath := filepath.Join(backupDir, latestFile.Name())
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", latestFile.Name()))
	file, err := os.Open(backupPath)
	if err != nil {
		sendError(w, "Failed to open backup file", http.StatusInternalServerError)
		return
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Printf("Warning: Failed to close backup file: %v\n", closeErr)
		}
	}()
	io.Copy(w, file)
}
func importBackupHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(32 << 20)
	file, handler, err := r.FormFile("backup")
	if err != nil {
		sendError(w, "Failed to read backup file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	backupPath := filepath.Join(DataBaseDir, "database", "backups", handler.Filename)
	dst, err := os.Create(backupPath)
	if err != nil {
		sendError(w, "Failed to save backup file", http.StatusInternalServerError)
		return
	}
	defer func() {
		if closeErr := dst.Close(); closeErr != nil {
			fmt.Printf("Warning: Failed to close backup destination file: %v\n", closeErr)
		}
	}()
	io.Copy(dst, file)
	cmd := exec.Command("irangate", "config", "restore", backupPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		sendError(w, "Failed to restore backup: "+string(output), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Backup restored successfully", nil)
}

var processStartedAt = time.Now()

func healthHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"version":   "1.0.0",
		"uptime":    time.Since(processStartedAt).Truncate(time.Second).String(),
	}
	sendSuccess(w, "Health check passed", health)
}
func getUsersHandler(w http.ResponseWriter, r *http.Request) {
	users, err := loadUsers()
	if err != nil {
		sendError(w, "Failed to load users", http.StatusInternalServerError)
		return
	}
	for i := range users {
		users[i].PasswordHash = ""
	}
	sendSuccess(w, "Users retrieved successfully", users)
}
func createUserHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	role := Role(req.Role)
	if role != RoleAdmin && role != RoleModerator && role != RoleViewer {
		sendError(w, "Invalid role", http.StatusBadRequest)
		return
	}
	createdBy := "admin"
	user, err := createUser(req.Username, req.Password, req.Email, role, createdBy)
	if err != nil {
		sendError(w, err.Error(), http.StatusBadRequest)
		return
	}
	user.PasswordHash = ""
	sendSuccess(w, "User created successfully", user)
}
func updateUserHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	username := vars["username"]
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := updateUser(username, updates); err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "User updated successfully", nil)
}
func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	username := vars["username"]
	users, _ := loadUsers()
	adminCount := 0
	for _, u := range users {
		if u.Role == RoleAdmin && u.Active {
			adminCount++
		}
	}
	user, _ := getUserByUsername(username)
	if user != nil && user.Role == RoleAdmin && adminCount <= 1 {
		sendError(w, "Cannot delete the last admin user", http.StatusBadRequest)
		return
	}
	if err := deleteUser(username); err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "User deleted successfully", nil)
}
func getSubscriptionsHandler(w http.ResponseWriter, _ *http.Request) {
	subs, err := loadSubscriptions()
	if err != nil {
		sendError(w, "Failed to load subscriptions", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Subscriptions retrieved successfully", subs)
}
func createSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	var sub Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := createSubscription(sub); err != nil {
		sendError(w, err.Error(), http.StatusBadRequest)
		return
	}
	sendSuccess(w, "Subscription created successfully", sub)
}
func updateSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["client"]
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := updateSubscription(clientName, updates); err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Subscription updated successfully", nil)
}
func deleteSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["client"]
	if err := deleteSubscription(clientName); err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Subscription deleted successfully", nil)
}
func getExpiringHandler(w http.ResponseWriter, r *http.Request) {
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		fmt.Sscanf(d, "%d", &days)
	}
	expiring, err := getExpiringSubscriptions(days)
	if err != nil {
		sendError(w, "Failed to get expiring subscriptions", http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Expiring subscriptions retrieved", map[string]interface{}{
		"count":         len(expiring),
		"days":          days,
		"subscriptions": expiring,
	})
}
func renewSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["client"]
	var req struct {
		Days int `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Days = 30
	}
	if err := renewSubscription(clientName, req.Days); err != nil {
		sendError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, fmt.Sprintf("Subscription renewed for %d days", req.Days), nil)
}

func generateSubscriptionToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ensureClientSubscriptionToken(name string) (string, error) {
	clientPath := filepath.Join(DataBaseDir, "database", "clients", name+".json")

	data, err := os.ReadFile(clientPath)
	if err != nil {
		return "", err
	}

	var clientData map[string]interface{}
	if err := json.Unmarshal(data, &clientData); err != nil {
		return "", err
	}

	if token, ok := clientData["subscription_token"].(string); ok && token != "" {
		return token, nil
	}

	token, err := generateSubscriptionToken()
	if err != nil {
		return "", err
	}

	clientData["subscription_token"] = token

	out, err := json.MarshalIndent(clientData, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(clientPath, out, 0644); err != nil {
		return "", err
	}

	return token, nil
}

func buildSubscriptionURL(r *http.Request, token string) string {
	scheme := strings.TrimSpace(
		readEnvOrDefault("SUBSCRIPTION_SCHEME", ""),
	)

	if scheme == "" {
		scheme = "http"

		if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
			scheme = strings.TrimSpace(
				strings.Split(forwarded, ",")[0],
			)
		} else if r.TLS != nil {
			scheme = "https"
		}
	}

	hostname := strings.TrimSpace(
		readEnvOrDefault("SUBSCRIPTION_PUBLIC_HOST", ""),
	)

	if hostname == "" {
		host := r.Host

		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = strings.TrimSpace(
				strings.Split(forwardedHost, ",")[0],
			)
		}

		hostname = host

		if strings.Count(host, ":") == 1 {
			hostname = strings.SplitN(host, ":", 2)[0]
		}
	}

	port := strings.TrimSpace(
		readEnvOrDefault("SUBSCRIPTION_PORT", "8090"),
	)

	authority := hostname

	if !((scheme == "http" && port == "80") ||
		(scheme == "https" && port == "443")) {
		authority = fmt.Sprintf("%s:%s", hostname, port)
	}

	return fmt.Sprintf(
		"%s://%s/sub/%s",
		scheme,
		authority,
		token,
	)
}

type IKEv2SubscriptionInfo struct {
	Available  bool
	Server     string
	Identifier string
	Username   string
	Password   string
	Error      string
}

func ikev2StringFromMap(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		v := strings.TrimSpace(fmt.Sprintf("%v", m[key]))
		if v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}

func ikev2PreferredServerHost() string {
	for _, path := range []string{
		"/opt/irangate/webpanel/server.json",
		"/opt/irangate/server_config.json",
		"/opt/irangate/database/settings.json",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var m map[string]interface{}
		if json.Unmarshal(b, &m) != nil {
			continue
		}

		// IKEv2 can intentionally use a different public IP/domain than OpenVPN.
		if v, ok := m["ikev2_server_host"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}

		if v, ok := m["server_domain"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}

		if v, ok := m["server_ip"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}

		if v, ok := m["public_ip"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	out, err := exec.Command(
		"sh",
		"-c",
		`ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}'`,
	).Output()

	if err == nil {
		host := strings.TrimSpace(
			string(out),
		)

		if host != "" {
			return host
		}
	}

	return ""
}

func ikev2Run(args ...string) (string, error) {
	cmd := exec.Command("/usr/local/sbin/irangate-ikev2", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func parseIKEv2AccountOutput(out string) IKEv2SubscriptionInfo {
	info := IKEv2SubscriptionInfo{
		Server:     ikev2PreferredServerHost(),
		Identifier: ikev2PreferredServerHost(),
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Username:") {
			info.Username = strings.TrimSpace(strings.TrimPrefix(line, "Username:"))
		}

		if strings.HasPrefix(line, "Password:") {
			info.Password = strings.TrimSpace(strings.TrimPrefix(line, "Password:"))
		}
	}

	info.Available = info.Username != "" && info.Password != ""
	return info
}

func ensureIKEv2Account(clientName string, createIfMissing bool) (IKEv2SubscriptionInfo, error) {
	out, err := ikev2Run("show", clientName)
	if err == nil {
		info := parseIKEv2AccountOutput(out)
		if info.Available {
			return info, nil
		}
	}

	if !createIfMissing {
		info := IKEv2SubscriptionInfo{
			Server:     ikev2PreferredServerHost(),
			Identifier: ikev2PreferredServerHost(),
			Error:      strings.TrimSpace(out),
		}
		if info.Error == "" && err != nil {
			info.Error = err.Error()
		}
		return info, err
	}

	out, err = ikev2Run("add", clientName)
	if err != nil {
		info := IKEv2SubscriptionInfo{
			Server:     ikev2PreferredServerHost(),
			Identifier: ikev2PreferredServerHost(),
			Error:      strings.TrimSpace(out),
		}
		if info.Error == "" {
			info.Error = err.Error()
		}
		return info, fmt.Errorf("%s", info.Error)
	}

	info := parseIKEv2AccountOutput(out)
	if !info.Available {
		return info, fmt.Errorf("IKEv2 account output did not include username/password")
	}

	return info, nil
}

func ikev2InfoText(clientName string, info IKEv2SubscriptionInfo) string {
	return fmt.Sprintf(`🔐 IKEv2/IPSec

Type: IKEv2/IPSec MSCHAPv2
Server: %s
IPSec identifier: %s
CA certificate: Do not validate
IPSec server certificate: Received from server
Username: %s
Password: %s

OpenVPN user: %s
`,
		info.Server,
		info.Identifier,
		info.Username,
		info.Password,
		clientName,
	)
}

func findClientBySubscriptionToken(token string) (string, map[string]interface{}, error) {
	if len(token) < 32 {
		return "", nil, fmt.Errorf("invalid token")
	}

	clientsDir := filepath.Join(DataBaseDir, "database", "clients")
	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		return "", nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(clientsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var clientData map[string]interface{}
		if err := json.Unmarshal(data, &clientData); err != nil {
			continue
		}

		if savedToken, ok := clientData["subscription_token"].(string); ok && savedToken == token {
			name := strings.TrimSuffix(entry.Name(), ".json")
			if jsonName, ok := clientData["name"].(string); ok && jsonName != "" {
				name = jsonName
			}
			return name, applySubGlobalUsageToClientData(name, clientData), nil
		}
	}

	return "", nil, fmt.Errorf("subscription not found")
}

func subInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case int32:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		var n int64
		fmt.Sscanf(t, "%d", &n)
		return n
	default:
		return 0
	}
}

func subFormatBytes(n int64) string {
	if n < 0 {
		n = 0
	}

	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(n)
	unit := 0

	for value >= 1000 && unit < len(units)-1 {
		value = value / 1000
		unit++
	}

	if unit == 0 {
		return fmt.Sprintf("%d %s", n, units[unit])
	}

	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func subDaysLeft(clientData map[string]interface{}) string {
	raw, ok := clientData["expires_at"].(string)
	if !ok || raw == "" {
		return "Unlimited"
	}

	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "Unknown"
	}

	d := time.Until(t)
	if d <= 0 {
		return "Expired"
	}

	days := int(d.Hours()+23) / 24
	return fmt.Sprintf("%d days", days)
}

func getClientSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	token, err := ensureClientSubscriptionToken(name)
	if err != nil {
		sendError(w, "Failed to create subscription link: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]string{
		"name":               name,
		"subscription_token": token,
		"subscription_url":   buildSubscriptionURL(r, token),
	}

	sendSuccess(w, "Subscription link retrieved", data)
}

func subIKEv2Online(clientName string) bool {
	data, err := os.ReadFile("/opt/irangate/traffic/ikev2_current_stats.json")
	if err != nil {
		return false
	}

	var rows []map[string]interface{}
	if json.Unmarshal(data, &rows) != nil {
		return false
	}

	now := time.Now().UTC()

	for _, row := range rows {
		name := strings.TrimSpace(fmt.Sprintf("%v", row["client_name"]))
		if name == "" {
			name = strings.TrimSpace(fmt.Sprintf("%v", row["name"]))
		}
		if name == "" {
			name = strings.TrimSpace(fmt.Sprintf("%v", row["username"]))
		}

		if name != clientName {
			continue
		}

		connected := false
		switch v := row["connected"].(type) {
		case bool:
			connected = v
		case string:
			s := strings.ToLower(strings.TrimSpace(v))
			connected = s == "true" || s == "1" || s == "yes" || s == "online"
		}

		if !connected {
			continue
		}

		lastRaw := strings.TrimSpace(fmt.Sprintf("%v", row["last_seen"]))
		if lastRaw == "" || lastRaw == "<nil>" {
			return true
		}

		lastSeen, err := time.Parse(time.RFC3339, lastRaw)
		if err != nil {
			return true
		}

		return now.Sub(lastSeen.UTC()) <= 90*time.Second
	}

	return false
}

func subGlobalUsageTotal(clientName string) int64 {
	_, _, total, ok := exactGlobalUsage(clientName)
	if !ok {
		return 0
	}
	return total
}

func subscriptionPageHandler(w http.ResponseWriter, r *http.Request) {
	// The subscription page and all generated download links must never
	// be reused from browser, proxy or download-manager caches.
	w.Header().Set(
		"Cache-Control",
		"no-store, no-cache, must-revalidate, max-age=0",
	)
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	token := mux.Vars(r)["token"]

	clientName, clientData, err := findClientBySubscriptionToken(token)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	limit := subInt64(clientData["traffic_limit_bytes"])

	used := subInt64(clientData["global_total_bytes"])
	if used <= 0 {
		used = subInt64(clientData["total_bytes"])
	}
	if used <= 0 {
		used = subInt64(clientData["current_total"])
	}
	if used <= 0 {
		used = subInt64(clientData["used_bytes"])
	}
	if used <= 0 {
		used = subInt64(clientData["used"])
	}
	if used <= 0 {
		used = subInt64(clientData["usage"])
	}
	if used <= 0 {
		used = subInt64(clientData["traffic"])
	}
	if used <= 0 {
		used = subInt64(clientData["traffic_used_bytes"])
	}
	if used <= 0 {
		used = subInt64(clientData["upload"]) + subInt64(clientData["download"])
	}
	if used <= 0 {
		used = subInt64(clientData["bytes_sent"]) + subInt64(clientData["bytes_received"])
	}

	if globalUsed := subGlobalUsageTotal(clientName); globalUsed > used {
		used = globalUsed
	}

	left := int64(0)
	if limit > 0 && used < limit {
		left = limit - used
	}

	serviceFinished := false

	if limit > 0 && used >= limit {
		serviceFinished = true
	}

	if rawExp := fmt.Sprintf("%v", clientData["expires_at"]); rawExp != "" && rawExp != "<nil>" && !strings.HasPrefix(rawExp, "0001-01-01") {
		if expTime, err := time.Parse(time.RFC3339, rawExp); err == nil && !time.Now().UTC().Before(expTime) {
			serviceFinished = true
		}
	}

	statusLower := strings.ToLower(fmt.Sprintf("%v", clientData["status"]))
	if strings.Contains(statusLower, "traffic_limit_exceeded") ||
		strings.Contains(statusLower, "expired") ||
		strings.Contains(statusLower, "disabled") ||
		strings.Contains(statusLower, "deactive") {
		serviceFinished = true
	}

	statusText := "فعال"
	if serviceFinished {
		statusText = "سرویس تمام شده"
	}

	presence := globalPresenceForClient(
		clientName,
		clientData,
	)

	isOnline := presence.Online
	if subIKEv2Online(clientName) {
		isOnline = true
	}

	onlineText := "آفلاین"
	if isOnline {
		onlineText = "آنلاین"
	}

	lastConnectionText :=
		formatSubscriptionLastConnection(
			presence.LastSeen,
		)

	connectionLimit := subInt64(clientData["connection_limit"])
	if connectionLimit <= 0 {
		connectionLimit = 1
	}

	daysLeft := subDaysLeft(clientData)
	daysLeft = strings.ReplaceAll(daysLeft, " days", " روز")
	daysLeft = strings.ReplaceAll(daysLeft, " day", " روز")
	daysLeft = strings.ReplaceAll(daysLeft, "Unlimited", "نامحدود")
	daysLeft = strings.ReplaceAll(daysLeft, "Expired", "تمام شده")
	daysLeft = strings.ReplaceAll(daysLeft, "Unknown", "نامشخص")

	limitText := "نامحدود"
	leftText := "نامحدود"
	if limit > 0 {
		limitText = subFormatBytes(limit)
		leftText = subFormatBytes(left)
	}

	progressPercent := 0.0
	if limit > 0 {
		progressPercent = float64(used) * 100 / float64(limit)
		if progressPercent > 100 {
			progressPercent = 100
		}
		if progressPercent < 0 {
			progressPercent = 0
		}
	}

	progressText := "مصرف نامحدود"
	if limit > 0 {
		progressText = fmt.Sprintf("%s از %s مصرف شده است", subFormatBytes(used), subFormatBytes(limit))
	}

	downloadCacheVersion := strconv.FormatInt(
		time.Now().UnixNano(),
		10,
	)

	downloadURL := "/sub/" +
		template.URLQueryEscaper(token) +
		"/download?v=" +
		downloadCacheVersion

	subURL := buildSubscriptionURL(r, token)

	masterAllowed := clientHasNode(
		clientData,
		"local",
	)

	// Subscription views must never silently issue a new IKEv2 password
	// when a restored account is missing from the credential store.
	// Such auto-creation invalidates existing phone configurations.
	ikev2Cfg, _ := ensureIKEv2Account(clientName, false)
	if !masterAllowed {
		ikev2Cfg.Available = false
	}
	ikev2DownloadURL := "/sub/" +
		template.URLQueryEscaper(token) +
		"/ikev2.txt?v=" +
		downloadCacheVersion

	masterName := "Germany Master"
	masterFlag := "🇩🇪"

	subNodes := []map[string]string{}
	if nf, err := loadNodesFile(); err == nil {
		for _, node := range nf.Nodes {
			if node.IsLocal || node.AgentURL == "local" {
				if node.Name != "" {
					masterName = node.Name
				}
				if node.Flag != "" {
					masterFlag = node.Flag
				}
				continue
			}
			if !node.Enabled {
				continue
			}
			if !clientHasNode(clientData, node.ID) {
				continue
			}
			subNodes = append(subNodes, map[string]string{
				"ID":       node.ID,
				"Name":     node.Name,
				"Flag":     node.Flag,
				"Location": node.Location,
				"URL": "/sub/" +
					template.URLQueryEscaper(token) +
					"/node/" +
					template.URLQueryEscaper(node.ID) +
					"/download?v=" +
					downloadCacheVersion,
			})
		}
	}

	subPortLocations :=
		subscriptionOpenVPNPortLocationDownloads(
			clientData,
			token,
			downloadCacheVersion,
		)

	pageData := map[string]interface{}{
		"ClientName":            clientName,
		"UserCount":             connectionLimit,
		"Status":                statusText,
		"OnlineStatus":          onlineText,
		"IsOnline":              isOnline,
		"ShowLastConnection":    !isOnline,
		"LastConnection":        lastConnectionText,
		"Limit":                 limitText,
		"Used":                  subFormatBytes(used),
		"Left":                  leftText,
		"DaysLeft":              daysLeft,
		"ProgressPercent":       fmt.Sprintf("%.2f", progressPercent),
		"ProgressText":          progressText,
		"DownloadURL":           downloadURL,
		"MasterName":            masterName,
		"MasterFlag":            masterFlag,
		"MasterAllowed":         masterAllowed,
		"NodeDownloads":         subNodes,
		"PortLocationDownloads": subPortLocations,
		"SubscriptionURL":       subURL,
		"IKEv2":                 ikev2Cfg,
		"IKEv2DownloadURL":      ikev2DownloadURL,
	}

	tpl := template.Must(template.New("sub").Parse(`<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, max-age=0">
<meta http-equiv="Pragma" content="no-cache">
<meta http-equiv="Expires" content="0">
<title>لینک ساب - {{.ClientName}}</title>
<style>
*{box-sizing:border-box}
body{margin:0;font-family:Tahoma,Arial,sans-serif;background:radial-gradient(circle at top left,#3b0764 0,#020617 34%,#020617 100%);color:#e5e7eb;min-height:100vh}
.top{height:48px;background:rgba(15,23,42,.72);border-bottom:1px solid rgba(148,163,184,.12);display:flex;align-items:center;justify-content:flex-start;padding:0 24px}
.dot{width:22px;height:22px;border-radius:50%;border:6px solid rgba(15,23,42,.85)}
.dot.online{background:#22c55e;box-shadow:0 0 18px rgba(34,197,94,.85)}
.dot.offline{background:#ef4444;box-shadow:0 0 18px rgba(239,68,68,.85)}
.wrap{max-width:1120px;margin:24px auto;padding:0 16px}
.card{background:rgba(15,23,42,.78);border:1px solid rgba(148,163,184,.16);border-radius:18px;box-shadow:0 22px 70px rgba(0,0,0,.38);padding:16px}
.profile{display:flex;align-items:center;justify-content:space-between;gap:18px;margin-bottom:18px}
.user{display:flex;align-items:center;gap:14px}
.avatar{width:82px;height:82px;border-radius:22px;background:linear-gradient(135deg,#22d3ee,#6366f1);display:flex;align-items:center;justify-content:center;font-size:42px;box-shadow:0 0 30px rgba(34,211,238,.28)}
.name{font-size:20px;font-weight:800;color:#fff}
.pass{font-size:13px;color:#94a3b8;margin-top:6px}
.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-top:18px}
.stat{background:rgba(30,41,59,.72);border:1px solid rgba(148,163,184,.14);border-radius:16px;padding:18px;text-align:center}
.stat.ok{background:rgba(6,78,59,.42);border-color:rgba(16,185,129,.36)}
.label{font-size:12px;color:#94a3b8;margin-bottom:8px}
.value{font-size:16px;font-weight:900;color:#e0f2fe}
.value.green{color:#00f5a0}
.usage{margin-top:20px;background:rgba(30,41,59,.72);border:1px solid rgba(148,163,184,.14);border-radius:14px;padding:14px}
.usage-head{display:flex;align-items:center;justify-content:space-between;font-size:12px;color:#94a3b8;margin-bottom:10px}
.bar{height:8px;background:#090d22;border-radius:999px;overflow:hidden}
.fill{height:100%;width:{{.ProgressPercent}}%;background:linear-gradient(90deg,#06b6d4,#00f5a0);border-radius:999px;box-shadow:0 0 18px rgba(34,211,238,.5)}
.usage-text{text-align:left;direction:ltr;color:#94a3b8;font-size:12px;margin-top:10px}
.section{margin-top:24px;background:rgba(15,23,42,.78);border:1px solid rgba(148,163,184,.16);border-radius:18px;padding:16px}
.section-title{font-weight:900;font-size:16px;color:#fff;margin-bottom:16px}
.config{display:flex;align-items:center;justify-content:space-between;gap:16px;background:rgba(15,23,42,.82);border:1px solid rgba(148,163,184,.14);border-radius:14px;padding:16px}
.config-name{font-size:16px;font-weight:900;color:#fff}
.badge{display:inline-flex;align-items:center;justify-content:center;margin-left:8px;padding:3px 8px;border-radius:999px;background:#064e3b;color:#5eead4;font-size:11px;font-weight:800}
.meta{font-size:12px;color:#94a3b8;margin-top:8px;line-height:1.8}
.actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
.btn{border:0;cursor:pointer;text-decoration:none;padding:13px 18px;border-radius:13px;font-weight:900;font-size:14px}
.btn-main{background:linear-gradient(135deg,#7c3aed,#06b6d4);color:#020617}
.btn-ghost{background:rgba(30,41,59,.8);border:1px solid rgba(148,163,184,.24);color:#e5e7eb}
.footer{margin-top:16px;text-align:center;color:#64748b;font-size:12px}
@media(max-width:800px){.grid{grid-template-columns:1fr}.profile,.config{flex-direction:column;align-items:stretch}.actions{justify-content:stretch}.btn{text-align:center;flex:1}.avatar{width:72px;height:72px}}

.ike-card{margin-bottom:18px;padding:0;overflow:hidden;border:1px solid rgba(99,102,241,.28);background:linear-gradient(180deg,rgba(30,41,59,.62),rgba(15,23,42,.74));border-radius:18px}
.ike-head{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:16px;border-bottom:1px solid rgba(148,163,184,.13)}
.ike-title{display:flex;align-items:center;gap:10px;font-weight:900;color:#fff;font-size:17px}
.ike-badge{display:inline-flex;align-items:center;justify-content:center;padding:5px 10px;border-radius:999px;background:linear-gradient(135deg,#7c3aed,#06b6d4);color:#fff;font-size:12px;font-weight:900}
.ike-sub{color:#94a3b8;font-size:12px;margin-top:4px}
.ike-body{padding:16px}
.ike-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}
.ike-item{background:rgba(2,6,23,.28);border:1px solid rgba(148,163,184,.13);border-radius:14px;padding:12px;min-height:72px}
.ike-label{color:#94a3b8;font-size:12px;margin-bottom:7px}
.ike-value{direction:ltr;text-align:left;color:#e5e7eb;font-size:14px;font-weight:800;word-break:break-all;line-height:1.7}
.ike-value.fa{direction:rtl;text-align:right}
.ike-password{color:#fef3c7}
.ike-actions{display:flex;align-items:center;justify-content:flex-start;gap:10px;flex-wrap:wrap;margin-top:14px}
.ike-btn{border:0;cursor:pointer;display:inline-flex;align-items:center;gap:8px;padding:11px 14px;border-radius:13px;background:rgba(30,41,59,.8);color:#e5e7eb;text-decoration:none;font-weight:900;font-size:13px}
.ike-btn.primary{background:linear-gradient(135deg,#7c3aed,#2563eb)}
.ike-btn:hover{filter:brightness(1.12)}
.ike-note{margin-top:12px;color:#94a3b8;font-size:12px;line-height:1.9}
@media(max-width:720px){.ike-grid{grid-template-columns:1fr}.ike-head{align-items:flex-start;flex-direction:column}.ike-actions{justify-content:stretch}.ike-btn{width:100%;justify-content:center}}

</style>
</head>
<body>
<div class="top"><div class="dot {{if .IsOnline}}online{{else}}offline{{end}}"></div></div>

<div class="wrap">
  <div class="card">
    <div class="profile">
      <div class="user">
        <div class="avatar">👨🏻‍💻</div>
        <div>
          <div class="name">نام کاربری: {{.ClientName}}</div>
          <div class="pass">رمز عبور: فایل کانفیگ OpenVPN</div>
        </div>
      </div>
    </div>

    <div class="grid">
      <div class="stat">
        <div class="label">وضعیت آنلاین</div>
        <div class="value">{{.OnlineStatus}}</div>
      </div>
      <div class="stat">
        <div class="label">زمان باقی‌مانده</div>
        <div class="value">{{.DaysLeft}}</div>
      </div>
      <div class="stat ok">
        <div class="label">وضعیت سرویس</div>
        <div class="value green">{{.Status}}</div>
      </div>
    </div>

    <div class="usage">
      <div class="usage-head">
        <span>{{.ProgressPercent}}%</span>
        <span>مصرف ترافیک</span>
      </div>
      <div class="bar"><div class="fill"></div></div>
      <div class="usage-text">{{.ProgressText}}</div>
    </div>
  </div>

  <div class="section">

{{if .IKEv2.Available}}
    <div class="ike-card">
      <div class="ike-head">
        <div>
          <div class="ike-title">
            <span>🔐 IKEv2 / IPSec</span>
            <span class="ike-badge">MSCHAPv2</span>
          </div>
          <div class="ike-sub">اتصال داخلی گوشی‌های اندروید و iOS بدون نیاز به فایل OVPN</div>
        </div>
        <a class="ike-btn primary fresh-download" href="{{.IKEv2DownloadURL}}">⬇️ دانلود اطلاعات IKEv2</a>
      </div>

      <div class="ike-body">
        <div class="ike-grid">
          <div class="ike-item">
            <div class="ike-label">نوع اتصال</div>
            <div class="ike-value fa">IKEv2/IPSec MSCHAPv2</div>
          </div>

          <div class="ike-item">
            <div class="ike-label">Server</div>
            <div class="ike-value">{{.IKEv2.Server}}</div>
          </div>

          <div class="ike-item">
            <div class="ike-label">IPSec identifier</div>
            <div class="ike-value">{{.IKEv2.Identifier}}</div>
          </div>

          <div class="ike-item">
            <div class="ike-label">Username</div>
            <div class="ike-value">{{.IKEv2.Username}}</div>
          </div>

          <div class="ike-item">
            <div class="ike-label">Password</div>
            <div class="ike-value ike-password">{{.IKEv2.Password}}</div>
          </div>

          <div class="ike-item">
            <div class="ike-label">Certificate</div>
            <div class="ike-value fa">CA: Do not validate<br>Server: Received from server</div>
          </div>
        </div>

        <div class="ike-actions">
          <button class="ike-btn" type="button" data-copy="{{.IKEv2.Server}}" onclick="copyIKEValue(this)">کپی Server</button>
          <button class="ike-btn" type="button" data-copy="{{.IKEv2.Identifier}}" onclick="copyIKEValue(this)">کپی Identifier</button>
          <button class="ike-btn" type="button" data-copy="{{.IKEv2.Username}}" onclick="copyIKEValue(this)">کپی Username</button>
          <button class="ike-btn" type="button" data-copy="{{.IKEv2.Password}}" onclick="copyIKEValue(this)">کپی Password</button>
        </div>

        <div class="ike-note">در تنظیمات VPN گوشی، نوع اتصال را روی <b>IKEv2/IPSec MSCHAPv2</b> بگذارید. مقدار CA را روی <b>Do not validate</b> و Server certificate را روی <b>Received from server</b> قرار دهید.</div>
      </div>
    </div>
{{end}}

<div class="section-title">OpenVPN</div>

    <div class="config">
      <div>
        <div class="config-name"><span class="badge">OVPN</span> کانفیگ اختصاصی {{.ClientName}}</div>
        <div class="meta">
          تعداد کاربر: {{.UserCount}} کاربر<br>
          حجم کل: {{.Limit}}<br>
          مصرف‌شده: {{.Used}}<br>
          باقی‌مانده: {{.Left}}{{if .ShowLastConnection}}<br>
          آخرین اتصال: {{.LastConnection}}{{end}}
        </div>
      </div>

      <div class="actions">
        {{if .MasterAllowed}}
        <a class="btn btn-main fresh-download" href="{{.DownloadURL}}">دانلود {{.MasterFlag}} {{.MasterName}}</a>
        {{end}}
        {{range .NodeDownloads}}
        <a class="btn btn-ghost fresh-download" href="{{.URL}}">دانلود {{.Flag}} {{.Name}}</a>
        {{end}}
        {{range .PortLocationDownloads}}
        <a class="btn btn-ghost fresh-download" href="{{.URL}}">دانلود {{.Flag}} {{.Name}} — UDP {{.Port}}</a>
        {{end}}
      </div>
    </div>

    <div class="footer">این لینک خصوصی است؛ هر کسی لینک را داشته باشد می‌تواند وضعیت سرویس و فایل کانفیگ را دریافت کند.</div>
  </div>
</div>


<script>
function copyIKEValue(btn) {
  var value = btn.getAttribute('data-copy') || '';
  var oldText = btn.textContent;

  function show(ok) {
    btn.textContent = ok ? 'کپی شد ✅' : 'کپی نشد ❌';
    setTimeout(function () {
      btn.textContent = oldText;
    }, 1400);
  }

  function fallbackCopy() {
    var ta = document.createElement('textarea');
    ta.value = value;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '-1000px';
    ta.style.left = '-1000px';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, ta.value.length);

    var ok = false;
    try {
      ok = document.execCommand('copy');
    } catch (e) {
      ok = false;
    }

    document.body.removeChild(ta);
    show(ok);
  }

  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(value).then(function () {
      show(true);
    }).catch(function () {
      fallbackCopy();
    });
  } else {
    fallbackCopy();
  }
}
</script>

<script>
document.querySelectorAll(".fresh-download").forEach(function(link) {
    link.addEventListener("click", function(event) {
        event.preventDefault();

        const freshURL = new URL(
            link.getAttribute("href"),
            window.location.href
        );

        freshURL.searchParams.set(
            "v",
            Date.now().toString()
        );

        window.location.assign(
            freshURL.toString()
        );
    });
});
</script>
</body>
</html>`))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl.Execute(w, pageData)
}

func subscriptionIKEv2DownloadHandler(w http.ResponseWriter, r *http.Request) {
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

	// Do not silently replace a missing IKEv2 credential on download.
	// Administrators must recover the old secret or explicitly reset it.
	info, err := ensureIKEv2Account(clientName, false)
	if err != nil || !info.Available {
		msg := "IKEv2 config is not ready"
		if err != nil {
			msg += ": " + err.Error()
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	safeName := strings.NewReplacer("\\", "_", "\"", "_", "\r", "_", "\n", "_").Replace(clientName)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-ikev2.txt\"", safeName))
	_, _ = w.Write([]byte(ikev2InfoText(clientName, info)))
}

func subscriptionDownloadHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	token := mux.Vars(r)["token"]

	clientName, clientData, err :=
		findClientBySubscriptionToken(token)

	if err != nil {
		http.Error(
			w,
			"Subscription not found",
			http.StatusNotFound,
		)
		return
	}

	if !clientHasNode(
		clientData,
		"local",
	) {
		http.Error(
			w,
			"Node access denied",
			http.StatusForbidden,
		)
		return
	}

	configPath := filepath.Join(
		"/etc/openvpn/client",
		clientName+".ovpn",
	)

	profile, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(
				w,
				"Config file not found",
				http.StatusNotFound,
			)
			return
		}

		http.Error(
			w,
			"Failed to read config file: "+err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	host, err := configuredOpenVPNDownloadHost()
	if err != nil {
		http.Error(
			w,
			"Failed to resolve OpenVPN public host: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	profile, updated, err := rewriteOpenVPNProfileHost(
		profile,
		host,
	)
	if err != nil {
		http.Error(
			w,
			"Failed to rewrite OpenVPN config: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	log.Printf(
		"Subscription master profile host resolved: client=%s host=%s updated_lines=%d",
		clientName,
		host,
		updated,
	)

	safeName := strings.NewReplacer(
		"\\", "_",
		"\"", "_",
		"\r", "_",
		"\n", "_",
	).Replace(clientName)

	writeOpenVPNProfile(
		w,
		safeName+".ovpn",
		profile,
	)
}

func subscriptionNodeDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	token := vars["token"]
	nodeID := normalizeNodeID(vars["node"])

	clientName, clientData, err := findClientBySubscriptionToken(token)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	if !clientHasNode(clientData, nodeID) {
		http.Error(w, "Node not selected for this client", http.StatusNotFound)
		return
	}

	node, ok := findNodeConfig(nodeID)
	if !ok || node.IsLocal || node.AgentURL == "local" || !node.Enabled {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	downloadClientFromNode(w, r, node, clientName)
}

func main() {
	if len(os.Args) > 1 &&
		os.Args[1] == "--sync-admin-from-env" {

		if err := syncAdminCredentialsFromEnv(); err != nil {
			log.Fatalf(
				"Admin credential synchronization failed: %v",
				err,
			)
		}

		log.Printf(
			"Admin credentials synchronized successfully for user %q",
			readEnvOrDefault(
				"WEBPANEL_ADMIN_USER",
				"admin",
			),
		)

		return
	}

	if err := initDatabases(); err != nil {
		log.Fatal("Failed to initialize databases:", err)
	}

	if migrated, err := migrateLegacyTrafficQuotaUnits(); err != nil {
		log.Printf("Traffic quota unit migration warning: %v", err)
	} else if migrated.DelegatedAdmins > 0 || migrated.Clients > 0 {
		log.Printf(
			"Traffic quota unit migration completed: delegated_admins=%d clients=%d",
			migrated.DelegatedAdmins,
			migrated.Clients,
		)
	}

	go hub.run()
	go startStatsBroadcaster()
	go startSystemResourcesBroadcaster()
	go func() {
		parser := NewOpenVPNStatusParser()
		collectionInterval := 1 * time.Minute
		configPath := "/etc/irangate/traffic_config.json"
		if configData, err := os.ReadFile(configPath); err == nil {
			var config struct {
				CollectionIntervalSeconds int `json:"collection_interval_seconds"`
			}
			if err := json.Unmarshal(configData, &config); err == nil && config.CollectionIntervalSeconds > 0 {
				collectionInterval = time.Duration(config.CollectionIntervalSeconds) * time.Second
				log.Printf("Traffic collection interval set to %v from config", collectionInterval)
			}
		}
		parser.StartTrafficCollection(collectionInterval)
	}()
	// Legacy destructive data-limit scanner disabled.
	go StartClientLifecycleMonitor(20)
	go StartBlockedClientRemoteSync(60)
	go StartDelegatedAdminMonitor(15)
	go StartAsyncCleanup()
	basePathEnv := readEnvOrDefault("WEBPANEL_BASEPATH", "/")
	basePath := strings.TrimSpace(basePathEnv)
	if basePath == "" {
		basePath = "/"
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if len(basePath) > 1 && strings.HasSuffix(basePath, "/") {
		basePath = strings.TrimRight(basePath, "/")
	}
	portStr := readEnvOrDefault("WEBPANEL_PORT", "8080")
	if _, err := strconv.Atoi(portStr); err != nil {
		log.Fatalf("Invalid WEBPANEL_PORT: %s", portStr)
	}
	staticDir := readEnvOrDefault("WEBPANEL_STATIC_DIR", "/opt/irangate/webpanel/frontend")
	rootRouter := mux.NewRouter()

	// API, subscription and WebSocket routes must always remain
	// available from the server root. WEBPANEL_BASEPATH controls
	// only where the frontend UI is served.
	r := rootRouter
	r.HandleFunc("/ws", wsHandler)
	r.HandleFunc("/sub/{token}", subscriptionPageHandler).Methods("GET")
	r.HandleFunc("/sub/{token}/ikev2.txt", subscriptionIKEv2DownloadHandler).Methods("GET")
	r.HandleFunc("/sub/{token}/download", subscriptionDownloadHandler).Methods("GET")
	r.HandleFunc("/sub/{token}/node/{node}/download", subscriptionNodeDownloadHandler).Methods("GET")
	r.HandleFunc("/sub/{token}/port/{location}/download", subscriptionPortLocationDownloadHandler).Methods("GET")
	r.HandleFunc("/api/login", delegatedLoginHandler).Methods("POST")
	r.HandleFunc("/api/health", healthHandler).Methods("GET")
	api := r.PathPrefix("/api").Subrouter()
	api.Use(delegatedAuthMiddleware)
	api.HandleFunc("/me", delegatedMeHandler).Methods("GET")
	api.HandleFunc("/delegated-admins", listDelegatedAdminsHandler).Methods("GET")
	api.HandleFunc("/delegated-admins", createDelegatedAdminHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/add-traffic", addDelegatedAdminTrafficHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/add-time", addDelegatedAdminTimeHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/password", resetDelegatedAdminPasswordHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/disable", disableDelegatedAdminHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}/enable", enableDelegatedAdminHandler).Methods("POST")
	api.HandleFunc("/delegated-admins/{username}", deleteDelegatedAdminHandler).Methods("DELETE")
	api.HandleFunc("/clients", delegatedGetClientsHandler).Methods("GET")
	api.HandleFunc("/clients", delegatedCreateClientHandler).Methods("POST")
	api.HandleFunc("/clients/{name}", updateClientWithGlobalResetHandler).Methods("PUT")
	api.HandleFunc("/clients/{name}", deleteClientHandler).Methods("DELETE")
	api.HandleFunc("/clients/{name}/activate", activateClientHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/rework", reworkClientHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/deactivate", deactivateClientHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/status", getClientStatusHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/subscription", getClientSubscriptionHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/export", exportClientHandler).Methods("GET")
	api.HandleFunc("/clients/orphaned", getOrphanedClientsHandler).Methods("GET")
	api.HandleFunc("/clients/cleanup-orphaned", cleanupOrphanedHandler).Methods("POST")
	api.HandleFunc("/clients/bulk-create", bulkCreateClientsHandler).Methods("POST")
	api.HandleFunc("/clients/bulk-delete", bulkDeleteClientsHandler).Methods("POST")
	api.HandleFunc("/clients/bulk-export", bulkExportClientsHandler).Methods("POST")
	api.HandleFunc("/openvpn/configs", getOpenVPNConfigsHandler).Methods("GET")
	api.HandleFunc("/openvpn/sync", syncOpenVPNConfigsHandler).Methods("POST")
	api.HandleFunc("/openvpn/configs/{name}", getOpenVPNConfigHandler).Methods("GET")
	api.HandleFunc("/openvpn/sync/status", getOpenVPNSyncStatusHandler).Methods("GET")
	api.HandleFunc("/openvpn/create-configs", createOpenVPNConfigsHandler).Methods("POST")
	api.HandleFunc("/openvpn/update-config", updateOpenVPNConfigHandler).Methods("PUT")
	api.HandleFunc("/nodes", delegatedNodesHandler).Methods("GET")
	api.HandleFunc("/node-current-stats", getNodeCurrentStatsDebugHandler).Methods("GET")
	api.HandleFunc("/nodes", createNodeHandler).Methods("POST")
	api.HandleFunc("/nodes/{id}", updateNodeHandler).Methods("PUT")
	api.HandleFunc("/nodes/{id}", deleteNodeHandler).Methods("DELETE")
	api.HandleFunc("/nodes/{id}/ping", pingNodeHandler).Methods("GET")
	api.HandleFunc("/openvpn-port-locations", getOpenVPNPortLocationsHandler).Methods("GET")
	api.HandleFunc("/openvpn-port-locations", createOpenVPNPortLocationHandler).Methods("POST")
	api.HandleFunc("/openvpn-port-locations/{id}", updateOpenVPNPortLocationHandler).Methods("PUT")
	api.HandleFunc("/openvpn-port-locations/{id}", deleteOpenVPNPortLocationHandler).Methods("DELETE")
	api.HandleFunc("/openvpn-port-locations/apply", applyOpenVPNPortLocationsHandler).Methods("POST")
	api.HandleFunc("/routing/status", getRoutingStatusHandler).Methods("GET")
	api.HandleFunc("/routing/outbounds", getRoutingOutboundsHandler).Methods("GET")
	api.HandleFunc("/routing/outbounds", createRoutingOutboundHandler).Methods("POST")
	api.HandleFunc("/routing/outbounds/{tag}", updateRoutingOutboundHandler).Methods("PUT")
	api.HandleFunc("/routing/outbounds/{tag}", deleteRoutingOutboundHandler).Methods("DELETE")
	api.HandleFunc("/routing/bind", bindRoutingHandler).Methods("POST")
	api.HandleFunc("/routing/generate", generateRoutingHandler).Methods("POST")
	api.HandleFunc("/routing/apply", applyRoutingHandler).Methods("POST")
	api.HandleFunc("/routing/test", testRoutingOutboundHandler).Methods("POST")

	api.HandleFunc("/proxy/status", panelProxyStatusHandler).Methods("GET")
	api.HandleFunc("/proxy/apply", panelProxyApplyHandler).Methods("POST")
	api.HandleFunc("/proxy/clear", panelProxyClearHandler).Methods("POST")
	api.HandleFunc("/proxy/test", panelProxyTestHandler).Methods("POST")
	api.HandleFunc("/server-checker/ping", initiatePingCheckHandler).Methods("POST")
	api.HandleFunc("/server-checker/status/{request_id}", getPingCheckStatusHandler).Methods("GET")
	api.HandleFunc("/server-checker/history", getPingCheckHistoryHandler).Methods("GET")
	api.HandleFunc("/docs/{name}", getDocumentationHandler).Methods("GET")
	api.HandleFunc("/docs", saveDocumentationHandler).Methods("POST")
	api.HandleFunc("/docs", listDocumentationHandler).Methods("GET")
	api.HandleFunc("/clients/bulk-extend", bulkExtendClientsHandler).Methods("POST")
	api.HandleFunc("/clients/bulk-import", bulkImportClientsHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/qrcode", generateClientQRCodeHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/qrcode-download", downloadClientQRCodeHandler).Methods("GET")
	api.HandleFunc("/qrcode/generate", generateQRCodeHandler).Methods("POST")
	api.HandleFunc("/qrcode/batch-generate", batchGenerateQRCodeHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/device", registerDeviceHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/devices", getClientDevicesHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/device/{id}", updateDeviceHandler).Methods("PUT")
	api.HandleFunc("/clients/{name}/device/{id}", removeDeviceHandler).Methods("DELETE")
	api.HandleFunc("/devices/all", getAllDevicesHandler).Methods("GET")
	api.HandleFunc("/devices/cleanup", cleanupInactiveDevicesHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/quota", setClientQuotaHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/quota-status", getClientQuotaStatusHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/data-limit", setClientDataLimitHandler).Methods("POST")
	api.HandleFunc("/clients/{name}/data-limit", getClientDataLimitHandler).Methods("GET")
	api.HandleFunc("/clients/{name}/data-limit", deleteClientDataLimitHandler).Methods("DELETE")
	api.HandleFunc("/data-limits/check", checkDataLimitsHandler).Methods("POST")
	api.HandleFunc("/config", getConfigHandler).Methods("GET")
	api.HandleFunc("/config", updateConfigHandler).Methods("PUT")
	api.HandleFunc("/monitor/stats", getStatsHandler).Methods("GET")
	api.HandleFunc("/server/status", getServerStatusHandler).Methods("GET")
	api.HandleFunc("/server/restart", restartServerHandler).Methods("POST")
	api.HandleFunc("/server/start", startServerHandler).Methods("POST")
	api.HandleFunc("/server/stop", stopServerHandler).Methods("POST")
	api.HandleFunc("/system/resources", getSystemResourcesHandler).Methods("GET")
	api.HandleFunc("/system/resources/history", getSystemResourcesHistoryHandler).Methods("GET")
	api.HandleFunc("/system/processes", getTopProcessesHandler).Methods("GET")
	api.HandleFunc("/analytics/traffic-history", trafficHistoryHandler).Methods("GET", "POST")
	api.HandleFunc("/analytics/bandwidth-usage", bandwidthUsageHandler).Methods("GET")
	api.HandleFunc("/analytics/peak-hours", peakHoursHandler).Methods("GET")
	api.HandleFunc("/analytics/current-traffic", getCurrentTrafficHandler).Methods("GET")
	api.HandleFunc("/analytics/client-traffic/{name}", getClientTrafficHandler).Methods("GET")
	api.HandleFunc("/analytics/export-report", exportReportHandler).Methods("GET")
	api.HandleFunc("/traffic/stats", getTrafficStatsHandler).Methods("GET")
	api.HandleFunc("/traffic/stats/{name}", getClientTrafficStatsHandler).Methods("GET")
	api.HandleFunc("/traffic/history", getTrafficHistoryHandler).Methods("GET")
	api.HandleFunc("/traffic/top-users", getTopUsersHandler).Methods("GET")
	api.HandleFunc("/traffic/export", exportTrafficHandler).Methods("GET")
	api.HandleFunc("/traffic/quota/{name}", getQuotaUsageHandler).Methods("GET")
	api.HandleFunc("/traffic/efficiency/{name}", getBandwidthEfficiencyHandler).Methods("GET")
	api.HandleFunc("/traffic/anomalies/{name}", detectAnomaliesHandler).Methods("GET")
	api.HandleFunc("/traffic/realtime", getRealTimeStatsHandler).Methods("GET")
	api.HandleFunc("/async/operations", getAsyncOperationsHandler).Methods("GET")
	api.HandleFunc("/async/operations/{id}", getAsyncOperationStatusHandler).Methods("GET")
	api.HandleFunc("/async/clients", startAsyncClientListHandler).Methods("POST")
	api.HandleFunc("/async/server-status", startAsyncServerStatusHandler).Methods("POST")
	api.HandleFunc("/async/config-update", startAsyncConfigUpdateHandler).Methods("POST")
	api.HandleFunc("/settings/admin", getAdminSettingsHandler).Methods("GET")
	api.HandleFunc("/settings/admin/password", changePasswordHandler).Methods("PUT")
	api.HandleFunc("/settings/telegram", getTelegramSettingsPersistentHandler).Methods("GET")
	api.HandleFunc("/settings/telegram", updateTelegramSettingsPersistentHandler).Methods("PUT")
	api.HandleFunc("/settings/telegram-backup", getTelegramBackupSettingsHandler).Methods("GET")
	api.HandleFunc("/settings/telegram-backup", updateTelegramBackupSettingsHandler).Methods("PUT")
	api.HandleFunc("/settings/telegram-backup/run", runTelegramBackupNowHandler).Methods("POST")
	api.HandleFunc("/settings/server", getServerSettingsHandler).Methods("GET")
	api.HandleFunc("/settings/server", updateServerSettingsHandler).Methods("PUT")
	api.HandleFunc("/notifications/telegram/test", telegramTestHandler).Methods("POST")
	api.HandleFunc("/notifications/telegram/configure", telegramConfigureHandler).Methods("POST")
	api.HandleFunc("/notifications/telegram/status", telegramStatusHandler).Methods("GET")
	api.HandleFunc("/notifications/telegram/settings", telegramSettingsHandler).Methods("GET")
	api.HandleFunc("/autoactions", getAutoActionsHandler).Methods("GET")
	api.HandleFunc("/autoactions", createAutoActionHandler).Methods("POST")
	api.HandleFunc("/autoactions/{id}", updateAutoActionHandler).Methods("PUT")
	api.HandleFunc("/autoactions/{id}", deleteAutoActionHandler).Methods("DELETE")
	api.HandleFunc("/autoactions/{id}/test", testAutoActionHandler).Methods("POST")
	api.HandleFunc("/autoactions/{id}/execute", executeAutoActionHandler).Methods("POST")
	api.HandleFunc("/backup/schedules", getBackupSchedulesHandler).Methods("GET")
	api.HandleFunc("/backup/schedules", createBackupScheduleHandler).Methods("POST")
	api.HandleFunc("/backup/schedules/{id}", updateBackupScheduleHandler).Methods("PUT")
	api.HandleFunc("/backup/schedules/{id}", deleteBackupScheduleHandler).Methods("DELETE")
	api.HandleFunc("/backup/schedules/{id}/run", runBackupScheduleHandler).Methods("POST")
	api.HandleFunc("/backup/list", listBackupsHandler).Methods("GET")
	api.HandleFunc("/backup/{filename}", deleteBackupHandler).Methods("DELETE")
	api.HandleFunc("/backup/{filename}/download", downloadBackupHandler).Methods("GET")
	api.HandleFunc("/server-config", getServerConfigHandler).Methods("GET")
	api.HandleFunc("/server-config", updateServerConfigHandler).Methods("PUT")
	api.HandleFunc("/server-config/detect-ip", detectServerIPHandler).Methods("POST")
	api.HandleFunc("/logs/openvpn", getOpenVPNLogsHandler).Methods("GET")
	api.HandleFunc("/logs/webpanel", getWebPanelLogsHandler).Methods("GET")
	api.HandleFunc("/logs/system", getSystemLogsHandler).Methods("GET")
	api.HandleFunc("/logs/search", searchLogsHandler).Methods("GET", "POST")
	api.HandleFunc("/logs/tail", tailLogsHandler).Methods("GET")
	api.HandleFunc("/logs/stream", streamLogsHandler).Methods("GET")
	api.HandleFunc("/backup/export", exportBackupV2Handler).Methods("GET")
	api.HandleFunc("/backup/import", importBackupV2Handler).Methods("POST")
	api.HandleFunc("/backup/import/status/{jobID:[a-f0-9]{32}}", getBackupImportJobStatusHandler).Methods("GET")
	api.HandleFunc("/users", getUsersHandler).Methods("GET")
	api.HandleFunc("/users", createUserHandler).Methods("POST")
	api.HandleFunc("/users/{username}", updateUserHandler).Methods("PUT")
	api.HandleFunc("/users/{username}", deleteUserHandler).Methods("DELETE")
	api.HandleFunc("/subscriptions", getSubscriptionsHandler).Methods("GET")
	api.HandleFunc("/subscriptions", createSubscriptionHandler).Methods("POST")
	api.HandleFunc("/subscriptions/{client}", updateSubscriptionHandler).Methods("PUT")
	api.HandleFunc("/subscriptions/{client}", deleteSubscriptionHandler).Methods("DELETE")
	api.HandleFunc("/subscriptions/expiring", getExpiringHandler).Methods("GET")
	api.HandleFunc("/subscriptions/{client}/renew", renewSubscriptionHandler).Methods("POST")
	if basePath == "/" {
		rootRouter.PathPrefix("/").Handler(http.FileServer(http.Dir(staticDir)))
	} else {
		rootRouter.PathPrefix(basePath + "/").Handler(http.StripPrefix(basePath+"/", http.FileServer(http.Dir(staticDir))))
	}
	corsHandler := handlers.CORS(
		handlers.AllowedOrigins([]string{"*"}),
		handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}),
		handlers.AllowedHeaders([]string{"Content-Type", "Authorization"}),
	)(rootRouter)
	log.Println("🚀 SURENA Web Panel API Starting...")
	log.Printf("📍 API Server: http://localhost:%s", portStr)
	log.Println("🔐 Default Login: configured via env or admin/admin123")
	if readEnvOrDefault("WEBPANEL_ADMIN_PASS", "adminadmin") == "admin123" {
		log.Println("⚠️  CHANGE DEFAULT PASSWORD IMMEDIATELY!")
	}
	addr := ":" + portStr
	if err := http.ListenAndServe(addr, corsHandler); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
func getCurrentTrafficHandler(w http.ResponseWriter, r *http.Request) {
	db := NewTrafficDatabase()
	stats, err := db.GetCurrentClientStats()
	if err != nil {
		sendError(w, "Failed to get current traffic stats", http.StatusInternalServerError)
		return
	}
	var totalReceived, totalSent, totalBytes int64
	var connectedClients int
	for _, stat := range stats {
		totalReceived += stat.CurrentReceived
		totalSent += stat.CurrentSent
		totalBytes += stat.CurrentTotal
		if stat.Connected {
			connectedClients++
		}
	}
	response := map[string]interface{}{
		"clients": stats,
		"summary": map[string]interface{}{
			"total_clients":     len(stats),
			"connected_clients": connectedClients,
			"total_received":    totalReceived,
			"total_sent":        totalSent,
			"total_bytes":       totalBytes,
			"total_received_gb": float64(totalReceived) / (1024 * 1024 * 1024),
			"total_sent_gb":     float64(totalSent) / (1024 * 1024 * 1024),
			"total_bytes_gb":    float64(totalBytes) / (1024 * 1024 * 1024),
		},
	}
	sendSuccess(w, "Current traffic data retrieved successfully", response)
}
func getClientTrafficHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["name"]
	if clientName == "" {
		sendError(w, "Client name is required", http.StatusBadRequest)
		return
	}
	db := NewTrafficDatabase()
	stats, err := db.GetCurrentClientStats()
	if err != nil {
		sendError(w, "Failed to get current traffic stats", http.StatusInternalServerError)
		return
	}
	var clientStats *ClientTrafficStats
	for _, stat := range stats {
		if stat.ClientName == clientName {
			clientStats = &stat
			break
		}
	}
	if clientStats == nil {
		sendError(w, "Client not found", http.StatusNotFound)
		return
	}
	today := time.Now()
	dailySummary, err := db.GetDailySummary(clientName, today)
	if err != nil {
		sendError(w, "Failed to get daily summary", http.StatusInternalServerError)
		return
	}
	weekAgo := today.AddDate(0, 0, -7)
	history, err := db.GetClientTrafficHistory(clientName, weekAgo, today)
	if err != nil {
		sendError(w, "Failed to get traffic history", http.StatusInternalServerError)
		return
	}
	response := map[string]interface{}{
		"client_name":    clientName,
		"current_stats":  clientStats,
		"daily_summary":  dailySummary,
		"weekly_history": history,
	}
	sendSuccess(w, "Client traffic data retrieved successfully", response)
}
func setClientDataLimitHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["name"]
	var req struct {
		LimitGB float64 `json:"limit_gb"`
		Enabled bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	limitBytes := int64(req.LimitGB * 1024 * 1024 * 1024)
	if err := SetClientDataLimit(clientName, limitBytes, req.Enabled); err != nil {
		sendError(w, fmt.Sprintf("Failed to set data limit: %v", err), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Data limit set successfully", map[string]interface{}{
		"client_name": clientName,
		"limit_bytes": limitBytes,
		"limit_gb":    req.LimitGB,
		"enabled":     req.Enabled,
	})
}
func getClientDataLimitHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["name"]
	limits, err := LoadDataLimits()
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to load data limits: %v", err), http.StatusInternalServerError)
		return
	}
	config, exists := limits[clientName]
	if !exists {
		sendSuccess(w, "No data limit set for this client", map[string]interface{}{
			"client_name": clientName,
			"enabled":     false,
		})
		return
	}
	usage, err := GetClientDataUsage(clientName)
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to get data usage: %v", err), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Data limit retrieved successfully", map[string]interface{}{
		"client_name":     clientName,
		"limit_bytes":     config.LimitBytes,
		"limit_gb":        float64(config.LimitBytes) / (1024 * 1024 * 1024),
		"enabled":         config.Enabled,
		"current_bytes":   usage.TotalBytes,
		"current_gb":      float64(usage.TotalBytes) / (1024 * 1024 * 1024),
		"percentage_used": float64(usage.TotalBytes) / float64(config.LimitBytes) * 100,
		"remaining_bytes": config.LimitBytes - usage.TotalBytes,
		"remaining_gb":    float64(config.LimitBytes-usage.TotalBytes) / (1024 * 1024 * 1024),
	})
}
func deleteClientDataLimitHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clientName := vars["name"]
	limits, err := LoadDataLimits()
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to load data limits: %v", err), http.StatusInternalServerError)
		return
	}
	delete(limits, clientName)
	if err := SaveDataLimits(limits); err != nil {
		sendError(w, fmt.Sprintf("Failed to save data limits: %v", err), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Data limit removed successfully", map[string]string{
		"client_name": clientName,
	})
}
func checkDataLimitsHandler(w http.ResponseWriter, r *http.Request) {
	if err := CheckAndEnforceDataLimits(); err != nil {
		sendError(w, fmt.Sprintf("Failed to check data limits: %v", err), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Data limit check completed successfully", nil)
}
func getAsyncOperationsHandler(w http.ResponseWriter, r *http.Request) {
	operations := asyncManager.ListOperations()
	sendSuccess(w, "Async operations retrieved successfully", operations)
}
func getAsyncOperationStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	operationID := vars["id"]
	operation, exists := asyncManager.GetOperationStatus(operationID)
	if !exists {
		sendError(w, "Operation not found", http.StatusNotFound)
		return
	}
	sendSuccess(w, "Operation status retrieved successfully", operation)
}
func startAsyncClientListHandler(w http.ResponseWriter, r *http.Request) {
	operationID := asyncManager.AsyncClientList()
	sendSuccess(w, "Async client list operation started", map[string]string{
		"operation_id": operationID,
	})
}
func startAsyncServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	operationID := asyncManager.AsyncServerStatus()
	sendSuccess(w, "Async server status operation started", map[string]string{
		"operation_id": operationID,
	})
}
func startAsyncConfigUpdateHandler(w http.ResponseWriter, r *http.Request) {
	var config map[string]string
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	operationID := asyncManager.AsyncConfigUpdate(config)
	sendSuccess(w, "Async config update operation started", map[string]string{
		"operation_id": operationID,
	})
}
func getSystemResourcesHandler(w http.ResponseWriter, r *http.Request) {
	resources, err := getSystemResources()
	if err != nil {
		sendError(w, "Failed to get system resources: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "System resources retrieved successfully", resources)
}
func getSystemResourcesHistoryHandler(w http.ResponseWriter, r *http.Request) {
	history := map[string]interface{}{
		"data":    []interface{}{},
		"message": "Historical data not yet implemented",
	}
	sendSuccess(w, "System resources history retrieved", history)
}
func getTopProcessesHandler(w http.ResponseWriter, r *http.Request) {
	processes, err := getTopProcesses()
	if err != nil {
		sendError(w, "Failed to get top processes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Top processes retrieved successfully", processes)
}
func getServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	serverConfigPath := filepath.Join(DataBaseDir, "server_config.json")
	config, err := utils.LoadServerConfig(serverConfigPath)
	if err != nil {
		sendError(w, "Failed to load server config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server configuration retrieved successfully", config)
}
func updateServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerIP        string `json:"server_ip"`
		IKEv2ServerHost string `json:"ikev2_server_host,omitempty"`
		ServerDomain    string `json:"server_domain"`
		UseDomain       bool   `json:"use_domain"`
		AutoDetectIP    bool   `json:"auto_detect_ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	serverConfigPath := filepath.Join(DataBaseDir, "server_config.json")
	config, err := utils.LoadServerConfig(serverConfigPath)
	if err != nil {
		sendError(w, "Failed to load server config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	config.ServerIP = req.ServerIP
	config.ServerDomain = req.ServerDomain
	config.Settings.UseDomain = req.UseDomain
	config.AutoDetectIP = req.AutoDetectIP
	config.LastUpdated = time.Now().Format(time.RFC3339)
	if err := utils.SaveServerConfig(serverConfigPath, config); err != nil {
		sendError(w, "Failed to save server config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server configuration updated successfully", config)
}
func detectServerIPHandler(w http.ResponseWriter, r *http.Request) {
	detectedIP := utils.GetServerIP()
	if detectedIP == "" || detectedIP == "YOUR_SERVER_IP" {
		sendError(w, "Failed to detect server IP", http.StatusInternalServerError)
		return
	}
	serverConfigPath := filepath.Join(DataBaseDir, "server_config.json")
	config, err := utils.LoadServerConfig(serverConfigPath)
	if err != nil {
		sendError(w, "Failed to load server config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	config.ServerIP = detectedIP
	config.DetectedIPs.Primary = detectedIP
	config.LastUpdated = time.Now().Format(time.RFC3339)
	if err := utils.SaveServerConfig(serverConfigPath, config); err != nil {
		sendError(w, "Failed to save server config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sendSuccess(w, "Server IP detected and updated successfully", map[string]interface{}{
		"detected_ip": detectedIP,
		"config":      config,
	})
}
