package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type GlobalClientPresence struct {
	Online   bool
	LastSeen time.Time
	NodeID   string
	NodeName string
}

type globalPresenceSource struct {
	Path            string
	DefaultNodeID   string
	DefaultNodeName string
	Freshness       time.Duration
}

func presenceString(v interface{}) string {
	if v == nil {
		return ""
	}

	return strings.TrimSpace(
		fmt.Sprintf("%v", v),
	)
}

func presenceTruthy(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t

	case string:
		x := strings.ToLower(
			strings.TrimSpace(t),
		)

		switch x {
		case "1",
			"true",
			"yes",
			"on",
			"online",
			"connected",
			"active":
			return true
		}

	case float64:
		return t != 0

	case float32:
		return t != 0

	case int:
		return t != 0

	case int64:
		return t != 0

	case json.Number:
		n, err := t.Int64()
		return err == nil && n != 0
	}

	return false
}

func presenceNumber(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t

	case float32:
		return float64(t)

	case int:
		return float64(t)

	case int64:
		return float64(t)

	case int32:
		return float64(t)

	case json.Number:
		n, _ := t.Float64()
		return n

	case string:
		n, _ := strconv.ParseFloat(
			strings.TrimSpace(t),
			64,
		)
		return n
	}

	return 0
}

func presenceNameOf(
	m map[string]interface{},
) string {

	for _, key := range []string{
		"name",
		"client_name",
		"common_name",
		"username",
	} {
		if value, ok := m[key].(string); ok {
			value = strings.TrimSpace(value)

			if value != "" {
				return value
			}
		}
	}

	return ""
}

func presenceTimeValue(
	v interface{},
) time.Time {

	switch t := v.(type) {
	case float64:
		if t > 0 {
			return time.Unix(
				int64(t),
				0,
			).UTC()
		}

	case float32:
		if t > 0 {
			return time.Unix(
				int64(t),
				0,
			).UTC()
		}

	case int:
		if t > 0 {
			return time.Unix(
				int64(t),
				0,
			).UTC()
		}

	case int64:
		if t > 0 {
			return time.Unix(
				t,
				0,
			).UTC()
		}

	case json.Number:
		n, err := t.Int64()

		if err == nil && n > 0 {
			return time.Unix(
				n,
				0,
			).UTC()
		}

	case string:
		s := strings.TrimSpace(t)

		if s == "" ||
			s == "<nil>" {
			return time.Time{}
		}

		if unixValue, err :=
			strconv.ParseInt(
				s,
				10,
				64,
			); err == nil &&
			unixValue > 0 {

			return time.Unix(
				unixValue,
				0,
			).UTC()
		}

		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05",
		}

		for _, layout := range layouts {
			parsed, err := time.Parse(
				layout,
				s,
			)

			if err == nil {
				return parsed.UTC()
			}
		}
	}

	return time.Time{}
}

func presenceTimeFromMap(
	m map[string]interface{},
) time.Time {

	var newest time.Time

	for _, key := range []string{
		"last_updated_ts",
		"last_updated",
		"last_seen",
		"last_connection",
	} {
		t := presenceTimeValue(
			m[key],
		)

		if t.After(newest) {
			newest = t
		}
	}

	return newest
}

func presenceRowOnline(
	m map[string]interface{},
) bool {

	for _, key := range []string{
		"connected",
		"is_connected",
		"online",
		"is_online",
		"active",
	} {
		if presenceTruthy(m[key]) {
			return true
		}
	}

	status := strings.ToLower(
		presenceString(
			m["status"],
		),
	)

	switch status {
	case "active",
		"online",
		"connected":
		return true
	}

	return false
}

func loadGlobalPresence() map[string]GlobalClientPresence {
	result := map[string]GlobalClientPresence{}

	sources := []globalPresenceSource{
		{
			Path:            "/opt/irangate/traffic/current_stats.json",
			DefaultNodeID:   "local",
			DefaultNodeName: "Master",
			Freshness:       2 * time.Minute,
		},
		{
			Path: "/opt/irangate/traffic/ikev2_current_stats.json",
		},
		{
			Path:      "/opt/irangate/traffic/node_current_stats.json",
			Freshness: 45 * time.Second,
		},
	}

	now := time.Now().UTC()

	for _, source := range sources {
		data, err := os.ReadFile(
			source.Path,
		)

		if err != nil {
			continue
		}

		var rows []map[string]interface{}

		if err := json.Unmarshal(
			data,
			&rows,
		); err != nil {
			continue
		}

		info, _ := os.Stat(
			source.Path,
		)

		for _, row := range rows {
			name := presenceNameOf(row)

			if name == "" {
				continue
			}

			key := strings.ToLower(name)

			seen := presenceTimeFromMap(row)

			if seen.IsZero() &&
				info != nil {

				seen = info.ModTime().UTC()
			}

			online := presenceRowOnline(row)

			if online &&
				!seen.IsZero() &&
				now.After(
					seen.Add(
						source.Freshness,
					),
				) {

				online = false
			}

			nodeID := presenceString(
				row["node_id"],
			)

			if nodeID == "" {
				nodeID =
					source.DefaultNodeID
			}

			nodeName := presenceString(
				row["node_name"],
			)

			if nodeName == "" {
				nodeName =
					source.DefaultNodeName
			}

			current := result[key]

			if seen.After(
				current.LastSeen,
			) {
				current.LastSeen = seen

				if nodeID != "" {
					current.NodeID = nodeID
				}

				if nodeName != "" {
					current.NodeName = nodeName
				}
			}

			if online {
				current.Online = true

				if nodeID != "" {
					current.NodeID = nodeID
				}

				if nodeName != "" {
					current.NodeName = nodeName
				}
			}

			result[key] = current
		}
	}

	return result
}

func latestIKEv2HistoryPresenceTime(
	name string,
) time.Time {

	data, err := os.ReadFile(
		"/opt/irangate/traffic/ikev2_last_seen.json",
	)

	if err != nil {
		return time.Time{}
	}

	var rows []map[string]interface{}

	if err := json.Unmarshal(
		data,
		&rows,
	); err != nil {
		return time.Time{}
	}

	wanted := strings.ToLower(
		strings.TrimSpace(name),
	)

	if wanted == "" {
		return time.Time{}
	}

	var latest time.Time

	for _, row := range rows {
		rowName := strings.TrimSpace(
			presenceString(
				row["client_name"],
			),
		)

		if rowName == "" {
			rowName = strings.TrimSpace(
				presenceString(
					row["name"],
				),
			)
		}

		if rowName == "" {
			rowName = strings.TrimSpace(
				presenceString(
					row["username"],
				),
			)
		}

		if strings.ToLower(rowName) != wanted {
			continue
		}

		for _, key := range []string{
			"last_seen",
			"last_connection",
			"updated_at",
		} {
			t := presenceTimeValue(
				row[key],
			)

			if t.After(latest) {
				latest = t
			}
		}
	}

	return latest
}

func globalPresenceForClient(
	name string,
	clientData map[string]interface{},
) GlobalClientPresence {

	presence := loadGlobalPresence()

	result := presence[strings.ToLower(
		strings.TrimSpace(name),
	)]

	if clientData != nil {
		for _, key := range []string{
			"last_seen",
			"last_connection",
		} {
			t := presenceTimeValue(
				clientData[key],
			)

			if t.After(
				result.LastSeen,
			) {
				result.LastSeen = t
			}
		}
	}

	historySeen :=
		latestTrafficHistoryPresenceTime(
			name,
		)

	if historySeen.After(
		result.LastSeen,
	) {
		result.LastSeen =
			historySeen
	}

	ikev2HistorySeen :=
		latestIKEv2HistoryPresenceTime(
			name,
		)

	if ikev2HistorySeen.After(
		result.LastSeen,
	) {
		result.LastSeen =
			ikev2HistorySeen
	}

	return result
}

func latestTrafficHistoryPresenceTime(
	name string,
) time.Time {

	name = strings.TrimSpace(name)

	if name == "" {
		return time.Time{}
	}

	database :=
		NewTrafficDatabase()

	now := time.Now()
	weekAgo :=
		now.AddDate(
			0,
			0,
			-7,
		)

	history, err :=
		database.GetClientTrafficHistory(
			name,
			weekAgo,
			now,
		)

	if err != nil ||
		len(history) == 0 {

		return time.Time{}
	}

	var latest time.Time

	for _, record := range history {
		if record.Timestamp.After(
			latest,
		) {
			latest =
				record.Timestamp
		}
	}

	if latest.IsZero() {
		return time.Time{}
	}

	return latest.UTC()
}

func gregorianToJalali(
	gy int,
	gm int,
	gd int,
) (
	int,
	int,
	int,
) {

	gregorianMonthDays := []int{
		0,
		31,
		59,
		90,
		120,
		151,
		181,
		212,
		243,
		273,
		304,
		334,
	}

	jy := 0

	if gy > 1600 {
		jy = 979
		gy -= 1600
	} else {
		jy = 0
		gy -= 621
	}

	gy2 := gy

	if gm > 2 {
		gy2++
	}

	days :=
		365*gy +
			(gy2+3)/4 -
			(gy2+99)/100 +
			(gy2+399)/400 -
			80 +
			gd +
			gregorianMonthDays[gm-1]

	jy += 33 * (days / 12053)
	days %= 12053

	jy += 4 * (days / 1461)
	days %= 1461

	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}

	var jm int
	var jd int

	if days < 186 {
		jm = 1 + days/31
		jd = 1 + days%31
	} else {
		jm = 7 + (days-186)/30
		jd = 1 + (days-186)%30
	}

	return jy, jm, jd
}

func toPersianDigits(
	value string,
) string {

	replacer := strings.NewReplacer(
		"0", "۰",
		"1", "۱",
		"2", "۲",
		"3", "۳",
		"4", "۴",
		"5", "۵",
		"6", "۶",
		"7", "۷",
		"8", "۸",
		"9", "۹",
	)

	return replacer.Replace(
		value,
	)
}

func formatSubscriptionLastConnection(
	t time.Time,
) string {

	if t.IsZero() {
		return "نامشخص"
	}

	iranLocation, err :=
		time.LoadLocation(
			"Asia/Tehran",
		)

	if err != nil {
		iranLocation =
			time.FixedZone(
				"IRST",
				3*60*60+
					30*60,
			)
	}

	iranTime :=
		t.In(
			iranLocation,
		)

	gy, gm, gd :=
		iranTime.Date()

	jy, jm, jd :=
		gregorianToJalali(
			gy,
			int(gm),
			gd,
		)

	formatted := fmt.Sprintf(
		"%04d/%02d/%02d - %02d:%02d:%02d",
		jy,
		jm,
		jd,
		iranTime.Hour(),
		iranTime.Minute(),
		iranTime.Second(),
	)

	return toPersianDigits(
		formatted,
	)
}

func mergeGlobalPresenceIntoPayload(
	payload map[string]interface{},
	presence map[string]GlobalClientPresence,
) {

	if arr, ok :=
		payload["data"].([]interface{}); ok {

		mergeGlobalPresenceArray(
			arr,
			presence,
		)

		return
	}

	if obj, ok :=
		payload["data"].(map[string]interface{}); ok {

		for _, key := range []string{
			"clients",
			"items",
			"results",
			"data",
		} {
			if arr, ok :=
				obj[key].([]interface{}); ok {

				mergeGlobalPresenceArray(
					arr,
					presence,
				)

				return
			}
		}
	}

	if arr, ok :=
		payload["clients"].([]interface{}); ok {

		mergeGlobalPresenceArray(
			arr,
			presence,
		)
	}
}

func mergeGlobalPresenceArray(
	rows []interface{},
	presence map[string]GlobalClientPresence,
) {

	for _, raw := range rows {
		client, ok :=
			raw.(map[string]interface{})

		if !ok {
			continue
		}

		applyGlobalPresenceToClient(
			client,
			presence,
		)
	}
}

func applyGlobalPresenceToClient(
	client map[string]interface{},
	presence map[string]GlobalClientPresence,
) {

	name := presenceNameOf(client)

	if name == "" {
		return
	}

	item, exists := presence[strings.ToLower(name)]

	online :=
		presenceTruthy(
			client["connected"],
		) ||
			presenceTruthy(
				client["is_connected"],
			) ||
			presenceTruthy(
				client["online"],
			) ||
			presenceTruthy(
				client["is_online"],
			)

	if exists && item.Online {
		online = true
	}

	client["connected"] = online
	client["is_connected"] = online
	client["online"] = online
	client["is_online"] = online

	if exists &&
		!item.LastSeen.IsZero() {

		existingLastSeen :=
			presenceTimeValue(
				client["last_seen"],
			)

		if item.LastSeen.After(
			existingLastSeen,
		) {
			value :=
				item.LastSeen.UTC().
					Format(
						time.RFC3339,
					)

			client["last_seen"] =
				value

			client["last_connection"] =
				value
		}
	}

	if exists && item.Online {
		if item.NodeID != "" {
			client["online_node_id"] =
				item.NodeID
		}

		if item.NodeName != "" {
			client["online_node_name"] =
				item.NodeName
		}
	}

	total := presenceNumber(
		client["global_total_bytes"],
	)

	if total <= 0 {
		total = presenceNumber(
			client["total_bytes"],
		)
	}

	limit := presenceNumber(
		client["traffic_limit_bytes"],
	)

	applyUsageStatusFromGlobal(
		client,
		total,
		limit,
	)
}
