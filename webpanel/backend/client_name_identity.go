package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// New client names are canonical lowercase. Existing clients retain their
// exact historical casing until an operator explicitly migrates them.
var clientCreateIdentityMu sync.Mutex

func canonicalClientNameForCreate(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func findCaseInsensitiveClientCollision(name string) (string, bool, error) {
	wanted := canonicalClientNameForCreate(name)
	if wanted == "" {
		return "", false, nil
	}

	dir := filepath.Join(DataBaseDir, "database", "clients")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read clients directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		existing := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if strings.EqualFold(strings.TrimSpace(existing), wanted) {
			return existing, true, nil
		}
	}
	return "", false, nil
}

func exactUsageInt64(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	default:
		return 0
	}
}

func exactGlobalUsage(clientName string) (int64, int64, int64, bool) {
	name := strings.TrimSpace(clientName)
	if name == "" {
		return 0, 0, 0, false
	}

	path := filepath.Join(DataBaseDir, "traffic", "global_usage_totals.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, false
	}

	var root map[string]interface{}
	if json.Unmarshal(payload, &root) != nil {
		return 0, 0, 0, false
	}
	totals, ok := root["totals"].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}

	raw, exists := totals[name]
	if !exists {
		return 0, 0, 0, false
	}
	row, ok := raw.(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}

	rx := exactUsageInt64(row["rx"])
	tx := exactUsageInt64(row["tx"])
	total := exactUsageInt64(row["total"])
	if total <= 0 {
		total = rx + tx
	}
	if rx < 0 {
		rx = 0
	}
	if tx < 0 {
		tx = 0
	}
	if total < 0 {
		total = 0
	}
	return rx, tx, total, true
}
