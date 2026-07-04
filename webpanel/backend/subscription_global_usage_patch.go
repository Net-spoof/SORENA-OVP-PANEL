package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func applySubGlobalUsageToClientData(clientName string, clientData map[string]interface{}) map[string]interface{} {
	clientName = strings.TrimSpace(clientName)
	if clientName == "" || clientData == nil {
		return clientData
	}

	rx, tx, total, ok := loadSubGlobalUsage(clientName)
	if !ok {
		return clientData
	}

	if total <= 0 {
		total = rx + tx
	}

	limit := subGlobalNumber(clientData["traffic_limit_bytes"])
	remaining := limit - total
	if remaining < 0 {
		remaining = 0
	}

	clientData["global_bytes_received"] = rx
	clientData["global_bytes_sent"] = tx
	clientData["global_total_bytes"] = total

	clientData["bytes_received"] = rx
	clientData["bytes_sent"] = tx
	clientData["total_bytes"] = total

	clientData["current_received"] = rx
	clientData["current_sent"] = tx
	clientData["current_total"] = total

	clientData["daily_usage"] = total
	clientData["monthly_usage"] = total
	clientData["data_used_mb"] = total / 1024 / 1024

	clientData["upload"] = rx
	clientData["download"] = tx
	clientData["used"] = total
	clientData["usage"] = total
	clientData["traffic"] = total
	clientData["used_bytes"] = total
	clientData["used_traffic"] = total
	clientData["traffic_used_bytes"] = total

	clientData["remaining_bytes"] = remaining
	clientData["remaining_traffic"] = remaining
	clientData["remaining"] = remaining

	applyUsageStatusFromGlobal(clientData, total, limit)

	return clientData
}

func applyUsageStatusFromGlobal(clientData map[string]interface{}, total float64, limit float64) {
	if clientData == nil {
		return
	}

	if subGlobalIsExpired(clientData["expires_at"]) {
		clientData["status"] = "expired"
		clientData["active"] = false
		return
	}

	if limit > 0 && total >= limit {
		clientData["status"] = "traffic_limit_exceeded"
		clientData["active"] = false
		return
	}

	delete(clientData, "traffic_exceeded_at")

	if subGlobalBool(clientData["connected"]) {
		clientData["status"] = "active"
		clientData["active"] = true
		return
	}

	clientData["status"] = "inactive"
	clientData["active"] = false
}

func subGlobalIsExpired(v interface{}) bool {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" || s == "<nil>" || strings.HasPrefix(s, "0001-01-01") {
		return false
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return false
	}

	return !time.Now().UTC().Before(t)
}

func loadSubGlobalUsage(clientName string) (float64, float64, float64, bool) {
	b, err := os.ReadFile("/opt/irangate/traffic/global_usage_totals.json")
	if err != nil {
		return 0, 0, 0, false
	}

	var raw map[string]interface{}
	if json.Unmarshal(b, &raw) != nil {
		return 0, 0, 0, false
	}

	totals, ok := raw["totals"].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}

	item, ok := totals[clientName].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}

	rx := subGlobalNumber(item["rx"])
	tx := subGlobalNumber(item["tx"])
	total := subGlobalNumber(item["total"])

	if total <= 0 {
		total = rx + tx
	}

	return rx, tx, total, true
}

func subGlobalBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		x := strings.ToLower(strings.TrimSpace(t))
		return x == "1" || x == "true" || x == "yes" || x == "on" || x == "active" || x == "connected"
	case float64:
		return t == 1
	case int:
		return t == 1
	}
	return false
}

func subGlobalNumber(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	}

	return 0
}
