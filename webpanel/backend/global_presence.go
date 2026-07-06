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

	return result
}

func formatSubscriptionLastConnection(
	t time.Time,
) string {

	if t.IsZero() {
		return "نامشخص"
	}

	return t.UTC().Format(
		"2006-01-02 15:04:05 UTC",
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
