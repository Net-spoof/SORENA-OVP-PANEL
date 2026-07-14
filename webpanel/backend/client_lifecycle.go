package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var lifecycleMutex sync.Mutex

type RevocationLog struct {
	ClientName string    `json:"client_name"`
	Reason     string    `json:"reason"`
	Timestamp  time.Time `json:"timestamp"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

var RevocationLogPath = func() string {
	if dir := os.Getenv("IRANGATE_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "logs", "revocations.json")
	}
	return "/opt/irangate/logs/revocations.json"
}()

func logRevocation(clientName, reason string, success bool, errMsg string) {
	logEntry := RevocationLog{
		ClientName: clientName,
		Reason:     reason,
		Timestamp:  time.Now(),
		Success:    success,
		Error:      errMsg,
	}

	var logs []RevocationLog
	if data, err := os.ReadFile(RevocationLogPath); err == nil {
		json.Unmarshal(data, &logs)
	}

	logs = append(logs, logEntry)

	os.MkdirAll(filepath.Dir(RevocationLogPath), 0755)
	if data, err := json.MarshalIndent(logs, "", "  "); err == nil {
		os.WriteFile(RevocationLogPath, data, 0644)
	}

	if success {
		log.Printf("✅ Client %s revoked: %s", clientName, reason)
	} else {
		log.Printf("❌ Failed to revoke client %s: %s - %s", clientName, reason, errMsg)
	}
}

func RevokeClientCertificate(clientName string, reason string) error {
	lifecycleMutex.Lock()
	defer lifecycleMutex.Unlock()

	log.Printf("Revoking certificate for client: %s (reason: %s)", clientName, reason)

	cmd := exec.Command("easyrsa", "revoke", clientName)
	cmd.Dir = "/etc/openvpn/easy-rsa"
	cmd.Env = append(os.Environ(), "EASYRSA_BATCH=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		if string(output) != "" && (strings.Contains(string(output), "already revoked") || strings.Contains(string(output), "Not Found")) {
			log.Printf("Certificate for %s may already be revoked or not found", clientName)
		} else {
			errMsg := fmt.Sprintf("easyrsa revoke failed: %v, output: %s", err, string(output))
			logRevocation(clientName, reason, false, errMsg)
			return fmt.Errorf("%s", errMsg)
		}
	}

	genCRLCmd := exec.Command("easyrsa", "gen-crl")
	genCRLCmd.Dir = "/etc/openvpn/easy-rsa"
	genCRLCmd.Env = append(os.Environ(), "EASYRSA_BATCH=1")

	if output, err := genCRLCmd.CombinedOutput(); err != nil {
		errMsg := fmt.Sprintf("easyrsa gen-crl failed: %v, output: %s", err, string(output))
		logRevocation(clientName, reason, false, errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	crlSource := "/etc/openvpn/easy-rsa/pki/crl.pem"
	crlDest := "/etc/openvpn/crl.pem"
	if _, err := exec.Command("cp", crlSource, crlDest).CombinedOutput(); err != nil {
		log.Printf("Warning: Failed to copy CRL to OpenVPN directory: %v", err)
	}

	exec.Command("systemctl", "reload", "openvpn@server").Run()

	logRevocation(clientName, reason, true, "")
	return nil
}

func DeactivateClient(clientName string) error {
	dataDir := os.Getenv("IRANGATE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientPath := filepath.Join(dataDir, "database", "clients", clientName+".json")

	data, err := os.ReadFile(clientPath)
	if err != nil {
		return fmt.Errorf("failed to read client data: %v", err)
	}

	var clientData map[string]interface{}
	if err := json.Unmarshal(data, &clientData); err != nil {
		return fmt.Errorf("failed to parse client data: %v", err)
	}

	clientData["active"] = false
	clientData["status"] = "Inactive"

	newData, err := json.MarshalIndent(clientData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal client data: %v", err)
	}

	if err := os.WriteFile(clientPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to save client data: %v", err)
	}

	return nil
}

func ActivateClient(clientName string) error {
	dataDir := os.Getenv("IRANGATE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientPath := filepath.Join(dataDir, "database", "clients", clientName+".json")

	data, err := os.ReadFile(clientPath)
	if err != nil {
		return fmt.Errorf("failed to read client data: %v", err)
	}

	var clientData map[string]interface{}
	if err := json.Unmarshal(data, &clientData); err != nil {
		return fmt.Errorf("failed to parse client data: %v", err)
	}

	clientData["active"] = true
	clientData["status"] = "Active"

	newData, err := json.MarshalIndent(clientData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal client data: %v", err)
	}

	if err := os.WriteFile(clientPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to save client data: %v", err)
	}

	return nil
}

type localOpenVPNSession struct {
	ClientName  string
	RealAddress string
	ClientID    string
	ConnectedAt time.Time
}

func fastEnforceNumber(value interface{}) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)

	case float32:
		return int64(typed)

	case int:
		return int64(typed)

	case int64:
		return typed

	case int32:
		return int64(typed)

	case json.Number:
		number, _ := typed.Int64()
		return number

	case string:
		number, _ := strconv.ParseInt(
			strings.TrimSpace(typed),
			10,
			64,
		)
		return number
	}

	return 0
}

func fastEnforceGlobalUsage(clientName string) int64 {
	path := "/opt/irangate/traffic/global_usage_totals.json"

	if data, err := os.ReadFile(path); err == nil {
		var root map[string]interface{}

		if json.Unmarshal(data, &root) == nil {
			if totals, ok := root["totals"].(map[string]interface{}); ok {
				for name, raw := range totals {
					if !strings.EqualFold(name, clientName) {
						continue
					}

					if row, ok := raw.(map[string]interface{}); ok {
						total := fastEnforceNumber(row["total"])

						if total > 0 {
							return total
						}

						rx := fastEnforceNumber(row["rx"])
						tx := fastEnforceNumber(row["tx"])

						if rx+tx > 0 {
							return rx + tx
						}
					}
				}
			}
		}
	}

	database := NewTrafficDatabase()
	totals := database.loadCumulativeTotalsMap()

	if total, exists := totals[clientName]; exists {
		return total.TotalReceived +
			total.TotalSent +
			total.LastSessionReceived +
			total.LastSessionSent
	}

	for name, total := range totals {
		if strings.EqualFold(name, clientName) {
			return total.TotalReceived +
				total.TotalSent +
				total.LastSessionReceived +
				total.LastSessionSent
		}
	}

	return 0
}

func fastEnforceStopClient(
	clientName string,
	reason string,
	usedBytes int64,
) error {
	dataDir := os.Getenv("IRANGATE_DATA_DIR")

	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientPath := filepath.Join(
		dataDir,
		"database",
		"clients",
		clientName+".json",
	)

	data, err := os.ReadFile(clientPath)
	if err != nil {
		return fmt.Errorf(
			"failed to read client data: %v",
			err,
		)
	}

	var clientData map[string]interface{}

	if err := json.Unmarshal(data, &clientData); err != nil {
		return fmt.Errorf(
			"failed to parse client data: %v",
			err,
		)
	}

	clientData["active"] = false
	clientData["status"] = reason
	clientData["last_update"] = time.Now().
		UTC().
		Format(time.RFC3339)

	if usedBytes >= 0 {
		clientData["traffic_used_bytes"] = usedBytes
		clientData["data_used_mb"] = usedBytes / 1000000
		clientData["traffic_exceeded_at"] = time.Now().
			UTC().
			Format(time.RFC3339)
	}

	newData, err := json.MarshalIndent(
		clientData,
		"",
		"  ",
	)
	if err != nil {
		return err
	}

	tempPath := clientPath + ".tmp"

	if err := os.WriteFile(
		tempPath,
		newData,
		0644,
	); err != nil {
		return err
	}

	if err := os.Rename(
		tempPath,
		clientPath,
	); err != nil {
		return err
	}

	// جلوگیری از اتصال مجدد روی مستر
	blockLocalClient(clientName)

	// قطع فوری تمام sessionهای فعال روی مستر
	killLocalOpenVPNClient(clientName)

	// روی هر نود: block + kill
	go blockRemoteNodes(clientName)

	// پاک‌کردن cache صفحه کاربران
	clientsCache.mutex.Lock()
	clientsCache.data = nil
	clientsCache.timestamp = time.Time{}
	clientsCache.mutex.Unlock()

	log.Printf(
		"Client %s blocked and disconnected: %s",
		clientName,
		reason,
	)

	return nil
}

func fastEnforceLoadLocalSessions() ([]localOpenVPNSession, error) {
	conn, err := net.DialTimeout(
		"tcp",
		"127.0.0.1:7505",
		2*time.Second,
	)
	if err != nil {
		return nil, err
	}

	defer conn.Close()

	_ = conn.SetDeadline(
		time.Now().Add(4 * time.Second),
	)

	if _, err := conn.Write(
		[]byte("status 3\nquit\n"),
	); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(conn)
	sessions := make([]localOpenVPNSession, 0)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if !strings.HasPrefix(line, "CLIENT_LIST,") {
			continue
		}

		parts := strings.Split(line, ",")

		if len(parts) < 11 {
			continue
		}

		clientName := strings.TrimSpace(parts[1])
		realAddress := strings.TrimSpace(parts[2])
		clientID := strings.TrimSpace(parts[10])

		if clientName == "" {
			continue
		}

		var connectedAt time.Time

		if unixValue, err := strconv.ParseInt(
			strings.TrimSpace(parts[8]),
			10,
			64,
		); err == nil && unixValue > 0 {
			connectedAt = time.Unix(
				unixValue,
				0,
			).UTC()
		}

		if connectedAt.IsZero() {
			connectedAt, _ = time.Parse(
				"2006-01-02 15:04:05",
				strings.TrimSpace(parts[7]),
			)
		}

		sessions = append(
			sessions,
			localOpenVPNSession{
				ClientName:  clientName,
				RealAddress: realAddress,
				ClientID:    clientID,
				ConnectedAt: connectedAt,
			},
		)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return sessions, nil
}

func fastEnforceKillSession(
	session localOpenVPNSession,
) error {
	conn, err := net.DialTimeout(
		"tcp",
		"127.0.0.1:7505",
		2*time.Second,
	)
	if err != nil {
		return err
	}

	defer conn.Close()

	_ = conn.SetDeadline(
		time.Now().Add(4 * time.Second),
	)

	command := ""

	if session.ClientID != "" {
		command = "client-kill " +
			session.ClientID
	} else if session.RealAddress != "" {
		command = "kill " +
			session.RealAddress
	} else {
		return fmt.Errorf(
			"session has no client id or address",
		)
	}

	if _, err := conn.Write(
		[]byte(command + "\nquit\n"),
	); err != nil {
		return err
	}

	scanner := bufio.NewScanner(conn)
	var response strings.Builder

	for scanner.Scan() {
		response.WriteString(scanner.Text())
		response.WriteByte('\n')
	}

	output := response.String()

	if strings.Contains(
		strings.ToUpper(output),
		"ERROR",
	) {
		return fmt.Errorf(
			"management error: %s",
			strings.TrimSpace(output),
		)
	}

	return nil
}

func CheckConnectionLimits() error {
	sessions, err := fastEnforceLoadLocalSessions()
	if err != nil {
		return fmt.Errorf(
			"failed to load local OpenVPN sessions: %v",
			err,
		)
	}

	dataDir := os.Getenv("IRANGATE_DATA_DIR")

	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientsDir := filepath.Join(
		dataDir,
		"database",
		"clients",
	)

	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		return err
	}

	limits := make(map[string]int)

	for _, entry := range entries {
		if entry.IsDir() ||
			filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(
			filepath.Join(
				clientsDir,
				entry.Name(),
			),
		)
		if err != nil {
			continue
		}

		var clientData map[string]interface{}

		if json.Unmarshal(
			data,
			&clientData,
		) != nil {
			continue
		}

		active, _ := clientData["active"].(bool)

		if !active {
			continue
		}

		name, _ := clientData["name"].(string)

		if name == "" {
			name = strings.TrimSuffix(
				entry.Name(),
				".json",
			)
		}

		limit := int(
			fastEnforceNumber(
				clientData["connection_limit"],
			),
		)

		if limit <= 0 {
			limit = 1
		}

		limits[strings.ToLower(name)] = limit
	}

	grouped := make(
		map[string][]localOpenVPNSession,
	)

	for _, session := range sessions {
		key := strings.ToLower(
			session.ClientName,
		)

		grouped[key] = append(
			grouped[key],
			session,
		)
	}

	for key, clientSessions := range grouped {
		limit, exists := limits[key]

		if !exists || limit <= 0 {
			continue
		}

		if len(clientSessions) <= limit {
			continue
		}

		sort.Slice(
			clientSessions,
			func(i, j int) bool {
				left := clientSessions[i].ConnectedAt
				right := clientSessions[j].ConnectedAt

				if left.Equal(right) {
					return clientSessions[i].RealAddress <
						clientSessions[j].RealAddress
				}

				if left.IsZero() {
					return true
				}

				if right.IsZero() {
					return false
				}

				return left.Before(right)
			},
		)

		extra := len(clientSessions) - limit

		for index := 0; index < extra; index++ {
			session := clientSessions[index]

			if err := fastEnforceKillSession(
				session,
			); err != nil {
				log.Printf(
					"Connection limit kill failed for %s (%s): %v",
					session.ClientName,
					session.RealAddress,
					err,
				)

				continue
			}

			log.Printf(
				"Connection limit enforced for %s: killed oldest session %s, active=%d, limit=%d",
				session.ClientName,
				session.RealAddress,
				len(clientSessions),
				limit,
			)
		}
	}

	return nil
}

func StartBlockedClientRemoteSync(
	intervalSeconds int,
) {
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	syncBlockedClients := func() {
		entries, err := os.ReadDir(
			"/etc/openvpn/blocked-clients",
		)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf(
					"Failed to read blocked clients: %v",
					err,
				)
			}
			return
		}

		blockedNames := make([]string, 0)

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := strings.TrimSpace(
				entry.Name(),
			)

			if name != "" {
				blockedNames = append(
					blockedNames,
					name,
				)
			}
		}

		if len(blockedNames) == 0 {
			return
		}

		for _, node := range loadResetNodes() {
			if node.IsLocal {
				continue
			}

			parsed, err := url.Parse(
				node.AgentURL,
			)
			if err != nil ||
				parsed.Hostname() == "" {
				continue
			}

			address := net.JoinHostPort(
				parsed.Hostname(),
				"5024",
			)

			conn, err := net.DialTimeout(
				"tcp",
				address,
				1200*time.Millisecond,
			)
			if err != nil {
				continue
			}

			_ = conn.Close()

			var waitGroup sync.WaitGroup
			semaphore := make(chan struct{}, 8)

			for _, blockedName := range blockedNames {

				name := blockedName

				waitGroup.Add(1)

				go func() {
					defer waitGroup.Done()

					semaphore <- struct{}{}
					defer func() {
						<-semaphore
					}()

					callResetNode(
						node,
						"/block-client",
						name,
					)
				}()
			}

			waitGroup.Wait()

			log.Printf(
				"Synced %d blocked clients to node %s",
				len(blockedNames),
				node.Name,
			)
		}
	}

	syncBlockedClients()

	ticker := time.NewTicker(
		time.Duration(intervalSeconds) *
			time.Second,
	)
	defer ticker.Stop()

	log.Printf(
		"Remote blocked-client sync started (every %d seconds)",
		intervalSeconds,
	)

	for range ticker.C {
		syncBlockedClients()
	}
}

func CheckExpiredClients() error {
	log.Println("Checking for expired clients...")

	dataDir := os.Getenv("IRANGATE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientsDir := filepath.Join(
		dataDir,
		"database",
		"clients",
	)

	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		return fmt.Errorf(
			"failed to read clients directory: %v",
			err,
		)
	}

	now := time.Now().UTC()
	expiredCount := 0

	for _, entry := range entries {
		if entry.IsDir() ||
			filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		clientPath := filepath.Join(
			clientsDir,
			entry.Name(),
		)

		data, err := os.ReadFile(clientPath)
		if err != nil {
			continue
		}

		var clientData map[string]interface{}

		if json.Unmarshal(
			data,
			&clientData,
		) != nil {
			continue
		}

		active, _ := clientData["active"].(bool)
		if !active {
			continue
		}

		clientName, _ :=
			clientData["name"].(string)

		if clientName == "" {
			clientName = strings.TrimSuffix(
				entry.Name(),
				".json",
			)
		}

		expiresAtRaw, _ :=
			clientData["expires_at"].(string)

		if expiresAtRaw == "" ||
			strings.HasPrefix(
				expiresAtRaw,
				"0001-01-01",
			) {
			continue
		}

		expiresAt, err := time.Parse(
			time.RFC3339,
			expiresAtRaw,
		)
		if err != nil {
			continue
		}

		if now.Before(expiresAt) {
			continue
		}

		log.Printf(
			"Client %s expired at %s",
			clientName,
			expiresAt.Format(time.RFC3339),
		)

		if err := fastEnforceStopClient(
			clientName,
			"expired",
			-1,
		); err != nil {
			log.Printf(
				"Failed to stop expired client %s: %v",
				clientName,
				err,
			)
			continue
		}

		expiredCount++
	}

	log.Printf(
		"Expiration check complete: %d stopped",
		expiredCount,
	)

	return nil
}

func CheckTrafficLimits() error {
	log.Println("Checking traffic limits...")

	dataDir := os.Getenv("IRANGATE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/irangate"
	}

	clientsDir := filepath.Join(
		dataDir,
		"database",
		"clients",
	)

	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		return fmt.Errorf(
			"failed to read clients directory: %v",
			err,
		)
	}

	exceededCount := 0

	for _, entry := range entries {
		if entry.IsDir() ||
			filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		clientPath := filepath.Join(
			clientsDir,
			entry.Name(),
		)

		data, err := os.ReadFile(clientPath)
		if err != nil {
			continue
		}

		var clientData map[string]interface{}

		if json.Unmarshal(
			data,
			&clientData,
		) != nil {
			continue
		}

		active, _ := clientData["active"].(bool)
		if !active {
			continue
		}

		clientName, _ :=
			clientData["name"].(string)

		if clientName == "" {
			clientName = strings.TrimSuffix(
				entry.Name(),
				".json",
			)
		}

		trafficLimit := fastEnforceNumber(
			clientData["traffic_limit_bytes"],
		)

		if trafficLimit <= 0 {
			continue
		}

		totalBytes := fastEnforceGlobalUsage(
			clientName,
		)

		if totalBytes < trafficLimit {
			continue
		}

		log.Printf(
			"Client %s exceeded traffic limit: used=%d limit=%d",
			clientName,
			totalBytes,
			trafficLimit,
		)

		if err := fastEnforceStopClient(
			clientName,
			"traffic_limit_exceeded",
			totalBytes,
		); err != nil {
			log.Printf(
				"Failed to stop traffic-limited client %s: %v",
				clientName,
				err,
			)
			continue
		}

		exceededCount++
	}

	log.Printf(
		"Traffic limit check complete: %d stopped",
		exceededCount,
	)

	return nil
}

func StartClientLifecycleMonitor(intervalSeconds int) {
	if intervalSeconds <= 0 {
		intervalSeconds = 20
	}

	interval := time.Duration(
		intervalSeconds,
	) * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf(
		"Client enforcement monitor started (checking every %d seconds)",
		intervalSeconds,
	)

	runChecks := func() {
		startedAt := time.Now()

		if err := CheckExpiredClients(); err != nil {
			log.Printf(
				"Expiration check error: %v",
				err,
			)
		}

		if err := CheckTrafficLimits(); err != nil {
			log.Printf(
				"Traffic limit check error: %v",
				err,
			)
		}

		if err := CheckConnectionLimits(); err != nil {
			log.Printf(
				"Connection limit check error: %v",
				err,
			)
		}

		elapsed := time.Since(startedAt)

		if elapsed > interval {
			log.Printf(
				"Warning: enforcement cycle took %v, interval=%v",
				elapsed,
				interval,
			)
		}
	}

	runChecks()

	for range ticker.C {
		runChecks()
	}
}
