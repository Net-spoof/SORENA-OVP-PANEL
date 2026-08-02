package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OpenVPNStatusParser struct {
	StatusFiles []string
	Database    *TrafficDatabase
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	running     bool
}
type OpenVPNClient struct {
	CommonName     string
	RealAddress    string
	BytesReceived  int64
	BytesSent      int64
	ConnectedSince time.Time
	VirtualIP      string

	// IRANGATE_MULTI_PORT_TRAFFIC_STAGE4
	Source     string
	SessionKey string
}

func NewOpenVPNStatusParser() *OpenVPNStatusParser {
	ctx, cancel :=
		context.WithCancel(
			context.Background(),
		)

	return &OpenVPNStatusParser{
		StatusFiles: configuredOpenVPNStatusFiles(),

		Database: NewTrafficDatabase(),

		ctx:     ctx,
		cancel:  cancel,
		running: false,
	}
}

func (p *OpenVPNStatusParser) ParseStatusLog(filename string) ([]OpenVPNClient, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Printf("Warning: Failed to close file %s: %v\n", filename, closeErr)
		}
	}()
	var clients []OpenVPNClient
	scanner := bufio.NewScanner(file)
	inClientList := false
	inRoutingTable := false
	routingTable := make(map[string]string)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "OpenVPN CLIENT LIST" {
			inClientList = true
			inRoutingTable = false
			continue
		}
		if line == "ROUTING TABLE" {
			inClientList = false
			inRoutingTable = true
			continue
		}
		if line == "GLOBAL STATS" || line == "END" {
			inClientList = false
			inRoutingTable = false
			continue
		}
		if inClientList && line != "Updated," && !strings.HasPrefix(line, "Common Name,") {
			parts := strings.Split(line, ",")
			if len(parts) >= 5 {
				client := OpenVPNClient{
					CommonName:  strings.TrimSpace(parts[0]),
					RealAddress: strings.TrimSpace(parts[1]),
				}
				if bytes, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil {
					client.BytesReceived = bytes
				}
				if bytes, err := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64); err == nil {
					client.BytesSent = bytes
				}
				if connectedSince, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(parts[4])); err == nil {
					client.ConnectedSince = connectedSince
				}
				clients = append(clients, client)
			}
		}
		if inRoutingTable && line != "Virtual Address," && !strings.HasPrefix(line, "Virtual Address,") {
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				virtualIP := strings.TrimSpace(parts[0])
				commonName := strings.TrimSpace(parts[1])
				routingTable[commonName] = virtualIP
			}
		}
	}
	for i := range clients {
		if virtualIP, exists := routingTable[clients[i].CommonName]; exists {
			clients[i].VirtualIP = virtualIP
		}
	}
	return clients, scanner.Err()
}
func (p *OpenVPNStatusParser) CollectTrafficData() error {
	now := time.Now().UTC()

	statusFiles :=
		configuredOpenVPNStatusFiles()

	p.mu.Lock()

	p.StatusFiles =
		append(
			[]string(nil),
			statusFiles...,
		)

	p.mu.Unlock()

	allClients := make(
		[]OpenVPNClient,
		0,
	)

	for _, statusFile := range statusFiles {

		clients, err :=
			p.ParseStatusLog(
				statusFile,
			)

		if err != nil {
			fmt.Printf(
				"Warning: Failed to parse %s: %v\n",
				statusFile,
				err,
			)

			continue
		}

		for index := range clients {

			clients[index].Source =
				statusFile

			clients[index].SessionKey =
				openVPNSessionKey(
					clients[index],
				)
		}

		allClients = append(
			allClients,
			clients...,
		)
	}

	deltas, totals, err :=
		p.Database.
			ApplyOpenVPNMultiPortSnapshots(
				allClients,
				now,
			)

	if err != nil {
		return fmt.Errorf(
			"apply OpenVPN multi-port snapshots: %w",
			err,
		)
	}

	type activeClientAggregate struct {
		RealAddress string
		VirtualIP   string
	}

	activeClients := make(
		map[string]activeClientAggregate,
	)

	for _, client := range allClients {

		name :=
			strings.TrimSpace(
				client.CommonName,
			)

		if name == "" ||
			name == "UNDEF" {

			continue
		}

		item := activeClients[name]

		if item.RealAddress == "" {
			item.RealAddress =
				client.RealAddress
		}

		if item.VirtualIP == "" {
			item.VirtualIP =
				client.VirtualIP
		}

		activeClients[name] = item
	}

	for name, delta := range deltas {

		if delta.Received <= 0 &&
			delta.Sent <= 0 {

			continue
		}

		active :=
			activeClients[name]

		record :=
			TrafficRecord{
				ClientName: name,

				Timestamp: now,

				BytesReceived: delta.Received,

				BytesSent: delta.Sent,

				TotalBytes: delta.Received +
					delta.Sent,

				Connected: true,

				RealAddress: active.RealAddress,

				VirtualIP: active.VirtualIP,
			}

		if err :=
			p.Database.
				StoreOpenVPNTrafficDeltaRecord(
					record,
				); err != nil {

			fmt.Printf(
				"Warning: Failed to store multi-port delta for %s: %v\n",
				name,
				err,
			)
		}
	}

	existingStats, err :=
		p.Database.
			GetCurrentClientStats()

	if err != nil {
		fmt.Printf(
			"Warning: Failed to load current stats: %v\n",
			err,
		)

		existingStats =
			[]ClientTrafficStats{}
	}

	existingByName := make(
		map[string]ClientTrafficStats,
		len(existingStats),
	)

	for _, stat := range existingStats {

		existingByName[stat.ClientName] = stat
	}

	quotaBytesByClient :=
		loadClientQuotaBytes()

	currentStats := make(
		[]ClientTrafficStats,
		0,
		len(activeClients),
	)

	for name, active := range activeClients {

		total :=
			totals[name]

		previous :=
			existingByName[name]

		stats :=
			ClientTrafficStats{
				ClientName: name,

				CurrentReceived: total.Received,

				CurrentSent: total.Sent,

				CurrentTotal: total.Received +
					total.Sent,

				LastSeen: now,

				Connected: true,

				RealAddress: active.RealAddress,

				VirtualIP: active.VirtualIP,

				DailyUsage: previous.DailyUsage,

				MonthlyUsage: previous.MonthlyUsage,

				QuotaUsed: previous.QuotaUsed,
			}

		quotaBytes :=
			quotaBytesByClient[name]

		if quotaBytes > 0 {
			stats.QuotaUsed =
				(float64(
					stats.CurrentTotal,
				) /
					float64(
						quotaBytes,
					)) *
					100
		} else {
			stats.QuotaUsed = 0
		}

		currentStats = append(
			currentStats,
			stats,
		)
	}

	if err :=
		p.Database.
			UpdateCurrentStats(
				currentStats,
			); err != nil {

		return fmt.Errorf(
			"update multi-port current stats: %w",
			err,
		)
	}

	return nil
}

func (p *OpenVPNStatusParser) processClientBatch(
	clients []OpenVPNClient,
	currentStats *[]ClientTrafficStats,
	existingByName map[string]ClientTrafficStats,
) error {
	now := time.Now()

	for _, client := range clients {
		record := TrafficRecord{
			ClientName:    client.CommonName,
			Timestamp:     now,
			BytesReceived: client.BytesReceived,
			BytesSent:     client.BytesSent,
			TotalBytes: client.BytesReceived +
				client.BytesSent,
			Connected:   true,
			RealAddress: client.RealAddress,
			VirtualIP:   client.VirtualIP,
		}

		if err := p.Database.StoreTrafficRecord(record); err != nil {
			fmt.Printf(
				"Warning: Failed to store traffic record for %s: %v\n",
				client.CommonName,
				err,
			)
		}

		previous := existingByName[client.CommonName]

		stats := ClientTrafficStats{
			ClientName: client.CommonName,

			// در پایان CollectTrafficData از cumulative_totals
			// با مقدار دقیق جایگزین می‌شوند.
			CurrentReceived: client.BytesReceived,
			CurrentSent:     client.BytesSent,
			CurrentTotal: client.BytesReceived +
				client.BytesSent,

			LastSeen:     now,
			Connected:    true,
			RealAddress:  client.RealAddress,
			VirtualIP:    client.VirtualIP,
			DailyUsage:   previous.DailyUsage,
			MonthlyUsage: previous.MonthlyUsage,
			QuotaUsed:    previous.QuotaUsed,
		}

		*currentStats = append(*currentStats, stats)
	}

	return nil
}

func (p *OpenVPNStatusParser) calculateDailyUsage(clientName string, date time.Time) int64 {
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)
	records, err := p.Database.GetClientTrafficHistory(clientName, startOfDay, endOfDay)
	if err != nil {
		return 0
	}
	var totalUsage int64
	for _, record := range records {
		totalUsage += record.TotalBytes
	}
	return totalUsage
}
func (p *OpenVPNStatusParser) calculateMonthlyUsage(clientName string, date time.Time) int64 {
	startOfMonth := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location())
	endOfMonth := startOfMonth.AddDate(0, 1, 0)
	records, err := p.Database.GetClientTrafficHistory(clientName, startOfMonth, endOfMonth)
	if err != nil {
		return 0
	}
	var totalUsage int64
	for _, record := range records {
		totalUsage += record.TotalBytes
	}
	return totalUsage
}
func (p *OpenVPNStatusParser) calculateQuotaUsage(clientName string, currentUsage int64) float64 {
	client, err := getClientFromDatabase(clientName)
	if err != nil {
		return 0
	}
	quotaEnabled, _ := client["quota_enabled"].(bool)
	quotaGB, _ := client["bandwidth_quota_gb"].(float64)
	if !quotaEnabled || quotaGB <= 0 {
		return 0
	}
	quotaBytes := int64(quotaGB * 1024 * 1024 * 1024)
	if quotaBytes <= 0 {
		return 0
	}
	return (float64(currentUsage) / float64(quotaBytes)) * 100
}
func loadClientQuotaBytes() map[string]int64 {
	quotas := make(map[string]int64)

	cmd := exec.Command(
		"irangate",
		"client",
		"list",
		"--json",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf(
			"Warning: Failed to load client quotas: %v\n",
			err,
		)
		return quotas
	}

	var clients []map[string]interface{}
	if err := json.Unmarshal(output, &clients); err != nil {
		fmt.Printf(
			"Warning: Failed to parse client quotas: %v\n",
			err,
		)
		return quotas
	}

	for _, client := range clients {
		name, _ := client["name"].(string)
		if name == "" {
			continue
		}

		// ساختار جدیدتر کلاینت‌ها
		if limit, ok := client["traffic_limit_bytes"].(float64); ok &&
			limit > 0 {
			quotas[name] = int64(limit)
			continue
		}

		// سازگاری با ساختار قدیمی پنل
		enabled, _ := client["quota_enabled"].(bool)
		quotaGB, _ := client["bandwidth_quota_gb"].(float64)

		if enabled && quotaGB > 0 {
			quotas[name] = int64(
				quotaGB * 1024 * 1024 * 1024,
			)
		}
	}

	return quotas
}

func (p *OpenVPNStatusParser) StartTrafficCollection(
	interval time.Duration,
) {
	p.mu.Lock()

	if p.running {
		p.mu.Unlock()
		return
	}

	p.running = true
	p.mu.Unlock()

	p.wg.Add(1)

	go func() {
		defer p.wg.Done()

		collectionTicker := time.NewTicker(interval)
		cleanupTicker := time.NewTicker(6 * time.Hour)

		defer collectionTicker.Stop()
		defer cleanupTicker.Stop()

		fmt.Printf(
			"Starting traffic collection service (interval: %v)\n",
			interval,
		)

		for {
			select {
			case <-p.ctx.Done():
				fmt.Println(
					"Traffic collection service stopping...",
				)
				return

			case <-collectionTicker.C:
				// اجرای synchronous باعث می‌شود دو چرخه
				// به‌صورت هم‌زمان روی هم نیفتند.
				startedAt := time.Now()

				if err := p.CollectTrafficData(); err != nil {
					fmt.Printf(
						"Error collecting traffic data: %v\n",
						err,
					)
				}

				elapsed := time.Since(startedAt)

				if elapsed > interval {
					fmt.Printf(
						"Warning: Traffic collection took %v, interval is %v\n",
						elapsed,
						interval,
					)
				}

			case <-cleanupTicker.C:
				// پاک‌سازی سنگین فقط هر شش ساعت
				if err := p.Database.CleanupOldData(90); err != nil {
					fmt.Printf(
						"Error cleaning up old data: %v\n",
						err,
					)
				}
			}
		}
	}()
}

func (p *OpenVPNStatusParser) StopTrafficCollection() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
	fmt.Println("Traffic collection service stopped")
}
func getClientFromDatabase(clientName string) (map[string]interface{}, error) {
	cmd := exec.Command("irangate", "client", "list", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	var clients []map[string]interface{}
	if err := json.Unmarshal(output, &clients); err != nil {
		return nil, err
	}
	for _, client := range clients {
		if name, ok := client["name"].(string); ok && name == clientName {
			return client, nil
		}
	}
	return map[string]interface{}{
		"name":               clientName,
		"quota_enabled":      false,
		"bandwidth_quota_gb": 0,
	}, nil
}
