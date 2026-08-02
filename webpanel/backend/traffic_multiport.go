package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const openVPNMultiPortTrafficStateVersion = 1

var openVPNMultiPortTrafficMutex sync.Mutex

type openVPNMultiPortSessionCounter struct {
	ClientName    string    `json:"client_name"`
	BytesReceived int64     `json:"bytes_received"`
	BytesSent     int64     `json:"bytes_sent"`
	LastSeen      time.Time `json:"last_seen"`
}

type openVPNMultiPortTotal struct {
	Received   int64     `json:"received"`
	Sent       int64     `json:"sent"`
	LastUpdate time.Time `json:"last_update"`
}

type openVPNMultiPortState struct {
	Version       int                                       `json:"version"`
	InitializedAt time.Time                                 `json:"initialized_at"`
	UpdatedAt     time.Time                                 `json:"updated_at"`
	Totals        map[string]openVPNMultiPortTotal          `json:"totals"`
	Sessions      map[string]openVPNMultiPortSessionCounter `json:"sessions"`
}

type openVPNTrafficDelta struct {
	Received int64
	Sent     int64
}

func configuredOpenVPNStatusFiles() []string {
	primary :=
		strings.TrimSpace(
			os.Getenv(
				"OPENVPN_STATUS_LOG_PATH",
			),
		)

	if primary == "" {
		primary =
			"/etc/openvpn/openvpn-status.log"
	}

	result := make(
		[]string,
		0,
	)

	seen := make(
		map[string]bool,
	)

	appendPath := func(path string) {
		path =
			strings.TrimSpace(path)

		if path == "" ||
			!filepath.IsAbs(path) {

			return
		}

		path = filepath.Clean(path)

		if seen[path] {
			return
		}

		seen[path] = true

		result = append(
			result,
			path,
		)
	}

	appendPath(primary)

	locations, err :=
		loadOpenVPNPortLocations()

	if err == nil {
		for _, location := range locations.Locations {

			if !location.Enabled {
				continue
			}

			appendPath(
				location.StatusFile,
			)
		}
	}

	return result
}

func openVPNSessionKey(
	client OpenVPNClient,
) string {
	if strings.TrimSpace(
		client.SessionKey,
	) != "" {
		return strings.TrimSpace(
			client.SessionKey,
		)
	}

	connectedSince := ""

	if !client.ConnectedSince.IsZero() {
		connectedSince =
			client.ConnectedSince.
				UTC().
				Format(
					time.RFC3339Nano,
				)
	}

	seed :=
		strings.Join(
			[]string{
				filepath.Clean(
					strings.TrimSpace(
						client.Source,
					),
				),
				strings.ToLower(
					strings.TrimSpace(
						client.CommonName,
					),
				),
				strings.TrimSpace(
					client.RealAddress,
				),
				connectedSince,
			},
			"\x1f",
		)

	sum :=
		sha256.Sum256(
			[]byte(seed),
		)

	return hex.EncodeToString(
		sum[:],
	)
}

func writeOpenVPNMultiPortJSONAtomic(
	path string,
	value interface{},
	mode os.FileMode,
) error {
	if err := os.MkdirAll(
		filepath.Dir(path),
		0755,
	); err != nil {
		return err
	}

	payload, err :=
		json.MarshalIndent(
			value,
			"",
			"  ",
		)

	if err != nil {
		return err
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
		mode,
	); err != nil {
		return err
	}

	if err := os.Chmod(
		tempPath,
		mode,
	); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	if err := os.Rename(
		tempPath,
		path,
	); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	return nil
}

func loadOpenVPNMultiPortLegacyTotals(
	td *TrafficDatabase,
) map[string]openVPNMultiPortTotal {
	result := make(
		map[string]openVPNMultiPortTotal,
	)

	for name, total := range td.loadCumulativeTotalsMap() {

		name =
			strings.TrimSpace(name)

		if name == "" {
			continue
		}

		result[name] =
			openVPNMultiPortTotal{
				Received: total.TotalReceived +
					total.LastSessionReceived,

				Sent: total.TotalSent +
					total.LastSessionSent,

				LastUpdate: total.LastUpdate,
			}
	}

	return result
}

func saveOpenVPNMultiPortCumulativeTotals(
	td *TrafficDatabase,
	totals map[string]openVPNMultiPortTotal,
) error {
	names := make(
		[]string,
		0,
		len(totals),
	)

	for name := range totals {
		names = append(
			names,
			name,
		)
	}

	sort.Strings(names)

	output := make(
		[]ClientTotalTraffic,
		0,
		len(names),
	)

	for _, name := range names {
		total := totals[name]

		output = append(
			output,
			ClientTotalTraffic{
				ClientName:    name,
				TotalReceived: total.Received,
				TotalSent:     total.Sent,

				// شمارنده Sessionها در فایل مستقل
				// نگهداری می‌شود؛ بنابراین این دو مقدار
				// باید صفر بمانند.
				LastSessionReceived: 0,
				LastSessionSent:     0,
				LastUpdate:          total.LastUpdate,
			},
		)
	}

	return writeOpenVPNMultiPortJSONAtomic(
		filepath.Join(
			td.DataDir,
			"cumulative_totals.json",
		),
		output,
		0644,
	)
}

func copyOpenVPNMultiPortTotals(
	source map[string]openVPNMultiPortTotal,
) map[string]openVPNMultiPortTotal {
	result := make(
		map[string]openVPNMultiPortTotal,
		len(source),
	)

	for name, total := range source {
		result[name] = total
	}

	return result
}

func (td *TrafficDatabase) ApplyOpenVPNMultiPortSnapshots(
	clients []OpenVPNClient,
	now time.Time,
) (
	map[string]openVPNTrafficDelta,
	map[string]openVPNMultiPortTotal,
	error,
) {
	openVPNMultiPortTrafficMutex.Lock()
	defer openVPNMultiPortTrafficMutex.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	if err := os.MkdirAll(
		td.DataDir,
		0755,
	); err != nil {
		return nil, nil, err
	}

	statePath :=
		filepath.Join(
			td.DataDir,
			"openvpn_multiport_state.json",
		)

	cumulativePath :=
		filepath.Join(
			td.DataDir,
			"cumulative_totals.json",
		)

	state := openVPNMultiPortState{
		Version: openVPNMultiPortTrafficStateVersion,

		Totals: make(
			map[string]openVPNMultiPortTotal,
		),

		Sessions: make(
			map[string]openVPNMultiPortSessionCounter,
		),
	}

	stateExists := false

	var stateModTime time.Time

	if payload, err :=
		os.ReadFile(statePath); err == nil {

		if err := json.Unmarshal(
			payload,
			&state,
		); err != nil {
			return nil, nil, fmt.Errorf(
				"parse multi-port traffic state: %w",
				err,
			)
		}

		stateExists = true

		if info, statErr :=
			os.Stat(statePath); statErr == nil {

			stateModTime =
				info.ModTime()
		}
	}

	if state.Totals == nil {
		state.Totals =
			make(
				map[string]openVPNMultiPortTotal,
			)
	}

	if state.Sessions == nil {
		state.Sessions =
			make(
				map[string]openVPNMultiPortSessionCounter,
			)
	}

	legacyTotals :=
		loadOpenVPNMultiPortLegacyTotals(td)

	var cumulativeModTime time.Time

	if info, err :=
		os.Stat(cumulativePath); err == nil {

		cumulativeModTime =
			info.ModTime()
	}

	snapshots := make(
		map[string]OpenVPNClient,
	)

	for _, client := range clients {
		client.CommonName =
			strings.TrimSpace(
				client.CommonName,
			)

		if client.CommonName == "" ||
			client.CommonName == "UNDEF" {

			continue
		}

		if client.BytesReceived < 0 {
			client.BytesReceived = 0
		}

		if client.BytesSent < 0 {
			client.BytesSent = 0
		}

		key :=
			openVPNSessionKey(client)

		if key == "" {
			continue
		}

		client.SessionKey = key

		previous, exists :=
			snapshots[key]

		if exists {
			if client.BytesReceived <
				previous.BytesReceived {

				client.BytesReceived =
					previous.BytesReceived
			}

			if client.BytesSent <
				previous.BytesSent {

				client.BytesSent =
					previous.BytesSent
			}
		}

		snapshots[key] = client
	}

	// اولین اجرای نسخه جدید:
	// مصرف قبلی از cumulative_totals وارد می‌شود و
	// Sessionهای فعال فقط Baseline می‌شوند تا مصرف
	// قدیمی دوباره شمرده نشود.
	if !stateExists {
		state.Totals =
			copyOpenVPNMultiPortTotals(
				legacyTotals,
			)

		for key, client := range snapshots {

			state.Sessions[key] =
				openVPNMultiPortSessionCounter{
					ClientName: client.CommonName,

					BytesReceived: client.BytesReceived,

					BytesSent: client.BytesSent,

					LastSeen: now,
				}
		}

		state.Version =
			openVPNMultiPortTrafficStateVersion

		state.InitializedAt = now
		state.UpdatedAt = now

		if err :=
			writeOpenVPNMultiPortJSONAtomic(
				statePath,
				state,
				0600,
			); err != nil {

			return nil, nil, err
		}

		if err :=
			saveOpenVPNMultiPortCumulativeTotals(
				td,
				state.Totals,
			); err != nil {

			return nil, nil, err
		}

		return map[string]openVPNTrafficDelta{},
			copyOpenVPNMultiPortTotals(
				state.Totals,
			),
			nil
	}

	resetClients := make(
		map[string]bool,
	)

	// اگر Reset Traffic خارج از Collector انجام شده باشد،
	// cumulative_totals جدیدتر و کوچک‌تر از State خواهد بود.
	if !cumulativeModTime.IsZero() &&
		!stateModTime.IsZero() &&
		cumulativeModTime.After(
			stateModTime,
		) {

		for name, stateTotal := range state.Totals {

			legacyTotal :=
				legacyTotals[name]

			if legacyTotal.Received <
				stateTotal.Received ||
				legacyTotal.Sent <
					stateTotal.Sent {

				state.Totals[name] =
					legacyTotal

				resetClients[name] = true
			}
		}
	}

	if len(resetClients) != 0 {
		for key, session := range state.Sessions {

			if resetClients[session.ClientName] {
				delete(
					state.Sessions,
					key,
				)
			}
		}
	}

	deltas := make(
		map[string]openVPNTrafficDelta,
	)

	for key, client := range snapshots {

		name := client.CommonName

		// بلافاصله پس از Reset، مقدار فعلی Session
		// فقط Baseline می‌شود و مصرف قبل از Reset
		// دوباره برنمی‌گردد.
		if resetClients[name] {
			state.Sessions[key] =
				openVPNMultiPortSessionCounter{
					ClientName:    name,
					BytesReceived: client.BytesReceived,
					BytesSent:     client.BytesSent,
					LastSeen:      now,
				}

			continue
		}

		previous, exists :=
			state.Sessions[key]

		var receivedDelta int64
		var sentDelta int64

		if !exists ||
			previous.ClientName != name {

			receivedDelta =
				client.BytesReceived

			sentDelta =
				client.BytesSent

		} else {
			if client.BytesReceived >=
				previous.BytesReceived {

				receivedDelta =
					client.BytesReceived -
						previous.BytesReceived
			} else {
				// Reset داخلی شمارنده همان Session.
				receivedDelta =
					client.BytesReceived
			}

			if client.BytesSent >=
				previous.BytesSent {

				sentDelta =
					client.BytesSent -
						previous.BytesSent
			} else {
				sentDelta =
					client.BytesSent
			}
		}

		state.Sessions[key] =
			openVPNMultiPortSessionCounter{
				ClientName:    name,
				BytesReceived: client.BytesReceived,
				BytesSent:     client.BytesSent,
				LastSeen:      now,
			}

		if receivedDelta == 0 &&
			sentDelta == 0 {

			continue
		}

		delta := deltas[name]

		delta.Received +=
			receivedDelta

		delta.Sent +=
			sentDelta

		deltas[name] = delta

		total := state.Totals[name]

		total.Received +=
			receivedDelta

		total.Sent +=
			sentDelta

		total.LastUpdate = now

		state.Totals[name] = total
	}

	// Sessionهای قدیمی برای جلوگیری از رشد نامحدود State
	// پس از سه روز حذف می‌شوند.
	cutoff :=
		now.Add(
			-72 * time.Hour,
		)

	for key, session := range state.Sessions {

		if session.LastSeen.Before(
			cutoff,
		) {
			delete(
				state.Sessions,
				key,
			)
		}
	}

	state.Version =
		openVPNMultiPortTrafficStateVersion

	if state.InitializedAt.IsZero() {
		state.InitializedAt = now
	}

	state.UpdatedAt = now

	// ابتدا State منبع حقیقت ذخیره می‌شود؛ سپس فایل
	// سازگار cumulative_totals از روی آن بازسازی می‌شود.
	if err :=
		writeOpenVPNMultiPortJSONAtomic(
			statePath,
			state,
			0600,
		); err != nil {

		return nil, nil, err
	}

	if err :=
		saveOpenVPNMultiPortCumulativeTotals(
			td,
			state.Totals,
		); err != nil {

		return nil, nil, err
	}

	return deltas,
		copyOpenVPNMultiPortTotals(
			state.Totals,
		),
		nil
}

func (td *TrafficDatabase) StoreOpenVPNTrafficDeltaRecord(
	record TrafficRecord,
) error {
	if record.BytesReceived <= 0 &&
		record.BytesSent <= 0 {

		return nil
	}

	openVPNMultiPortTrafficMutex.Lock()
	defer openVPNMultiPortTrafficMutex.Unlock()

	if record.Timestamp.IsZero() {
		record.Timestamp =
			time.Now().UTC()
	}

	record.TotalBytes =
		record.BytesReceived +
			record.BytesSent

	record.Connected = true

	dateDir :=
		filepath.Join(
			td.DataDir,
			"daily",
			record.Timestamp.
				Format(
					"2006-01-02",
				),
		)

	if err := os.MkdirAll(
		dateDir,
		0755,
	); err != nil {
		return err
	}

	recordPath :=
		filepath.Join(
			dateDir,
			fmt.Sprintf(
				"%s_%s-multiport.json",
				record.ClientName,
				record.Timestamp.
					Format(
						"15-04",
					),
			),
		)

	if payload, err :=
		os.ReadFile(recordPath); err == nil {

		var previous TrafficRecord

		if json.Unmarshal(
			payload,
			&previous,
		) == nil &&
			previous.ClientName ==
				record.ClientName {

			record.BytesReceived +=
				previous.BytesReceived

			record.BytesSent +=
				previous.BytesSent

			record.TotalBytes =
				record.BytesReceived +
					record.BytesSent
		}
	}

	return writeOpenVPNMultiPortJSONAtomic(
		recordPath,
		record,
		0644,
	)
}
