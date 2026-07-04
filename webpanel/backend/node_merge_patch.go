package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
)

type GlobalUsageItem struct {
	RX    float64 `json:"rx"`
	TX    float64 `json:"tx"`
	Total float64 `json:"total"`
}

func getNodeCurrentStatsDebugHandler(w http.ResponseWriter, r *http.Request) {
	global := loadGlobalUsageTotals()

	sendSuccess(w, "Global usage loaded", map[string]interface{}{
		"count":  len(global),
		"global": global,
	})
}

func getClientsWithNodeStatsHandler(w http.ResponseWriter, r *http.Request) {
	rec := httptest.NewRecorder()
	getClientsHandler(rec, r)

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}

	global := loadGlobalUsageTotals()
	payload["global_usage_count"] = len(global)

	mergeGlobalUsageIntoPayload(payload, global)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func loadGlobalUsageTotals() map[string]GlobalUsageItem {
	data, err := os.ReadFile("/opt/irangate/traffic/global_usage_totals.json")
	if err != nil {
		return map[string]GlobalUsageItem{}
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]GlobalUsageItem{}
	}

	totals, ok := raw["totals"].(map[string]interface{})
	if !ok {
		return map[string]GlobalUsageItem{}
	}

	out := map[string]GlobalUsageItem{}

	for name, val := range totals {
		m, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		rx := num(m, "rx")
		tx := num(m, "tx")
		total := num(m, "total")
		if total <= 0 {
			total = rx + tx
		}

		out[name] = GlobalUsageItem{
			RX:    rx,
			TX:    tx,
			Total: total,
		}
	}

	return out
}

func nameOf(m map[string]interface{}) string {
	for _, key := range []string{"name", "client_name", "common_name"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func num(m map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}

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
		}
	}

	return 0
}

func mergeGlobalUsageIntoPayload(payload map[string]interface{}, global map[string]GlobalUsageItem) {
	if arr, ok := payload["data"].([]interface{}); ok {
		payload["data"] = mergeGlobalArray(arr, global)
		return
	}

	if obj, ok := payload["data"].(map[string]interface{}); ok {
		for _, key := range []string{"clients", "items", "results", "data"} {
			if arr, ok := obj[key].([]interface{}); ok {
				obj[key] = mergeGlobalArray(arr, global)
				payload["data"] = obj
				return
			}
		}
	}

	if arr, ok := payload["clients"].([]interface{}); ok {
		payload["clients"] = mergeGlobalArray(arr, global)
		return
	}
}

func mergeGlobalArray(arr []interface{}, global map[string]GlobalUsageItem) []interface{} {
	index := map[string]map[string]interface{}{}

	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		name := nameOf(m)
		if name != "" {
			index[name] = m
		}
	}

	for name, usage := range global {
		if existing, ok := index[name]; ok {
			applyGlobalUsage(existing, usage)
		}
	}

	return arr
}

func applyGlobalUsage(m map[string]interface{}, usage GlobalUsageItem) {
	total := usage.Total
	if total <= 0 {
		total = usage.RX + usage.TX
	}

	m["global_bytes_received"] = usage.RX
	m["global_bytes_sent"] = usage.TX
	m["global_total_bytes"] = total

	m["bytes_received"] = usage.RX
	m["bytes_sent"] = usage.TX
	m["total_bytes"] = total

	m["current_received"] = usage.RX
	m["current_sent"] = usage.TX
	m["current_total"] = total

	m["daily_usage"] = total
	m["monthly_usage"] = total
	m["data_used_mb"] = total / 1024 / 1024

	m["upload"] = usage.RX
	m["download"] = usage.TX
	m["used_bytes"] = total
	m["used"] = total
	m["usage"] = total
	m["traffic"] = total
	m["used_traffic"] = total

	limit := subGlobalNumber(m["traffic_limit_bytes"])
	applyUsageStatusFromGlobal(m, total, limit)
}
