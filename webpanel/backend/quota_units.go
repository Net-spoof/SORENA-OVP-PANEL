package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// NET SPOOF quota contract is decimal SI:
// 1 MB = 1,000,000 bytes, 1 GB = 1,000,000,000 bytes,
// 1 TB = 1,000,000,000,000 bytes.
//
// Older Telegram/API callers used MiB/GiB/TiB while labelling those values as
// MB/GB/TB. canonicalTrafficQuotaBytes keeps current SI byte values unchanged
// and converts only values that exactly look like a legacy human-sized binary
// quota (to 0.01 of a binary unit). Metered traffic counters never pass through
// this function.
func canonicalTrafficQuotaBytes(n int64) int64 {
	if n <= 0 {
		return n
	}

	// Current web UIs emit decimal quotas at 0.01 MB precision or coarser.
	// Protect those values before applying legacy compatibility detection.
	if n%10_000 == 0 {
		return n
	}

	type unitPair struct {
		binary  int64
		decimal int64
	}

	units := []unitPair{
		{binary: 1 << 40, decimal: 1_000_000_000_000}, // TiB -> TB
		{binary: 1 << 30, decimal: 1_000_000_000},     // GiB -> GB
		{binary: 1 << 20, decimal: 1_000_000},         // MiB -> MB
	}

	for _, unit := range units {
		q := n / unit.binary
		r := n % unit.binary

		// Does n represent an exact value with at most two decimal places in
		// this binary unit? r is always < 2^40, so r*100 is int64-safe.
		scaledRemainder := r * 100
		if scaledRemainder%unit.binary != 0 {
			continue
		}

		hundredths := q*100 + scaledRemainder/unit.binary
		if hundredths <= 0 {
			continue
		}

		decimalHundredth := unit.decimal / 100
		if hundredths > math.MaxInt64/decimalHundredth {
			continue
		}

		converted := hundredths * decimalHundredth
		if converted != n {
			return converted
		}
	}

	return n
}

type quotaMigrationResult struct {
	DelegatedAdmins int
	Clients         int
}

func quotaJSONInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, true
		}
		if f, err := x.Float64(); err == nil && f >= math.MinInt64 && f <= math.MaxInt64 {
			return int64(math.Round(f)), true
		}
	case float64:
		if x >= math.MinInt64 && x <= math.MaxInt64 {
			return int64(math.Round(x)), true
		}
	case int64:
		return x, true
	case int:
		return int64(x), true
	case string:
		var number json.Number = json.Number(strings.TrimSpace(x))
		if n, err := number.Int64(); err == nil {
			return n, true
		}
	}
	return 0, false
}

func writeQuotaJSONAtomic(path string, value interface{}, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := path + ".quota-migrate.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, mode)
}

// migrateLegacyTrafficQuotaUnits is intentionally idempotent. It only rewrites
// configured quota limits; usage/accounting counters are never converted.
func migrateLegacyTrafficQuotaUnits() (quotaMigrationResult, error) {
	var result quotaMigrationResult

	if data, err := os.ReadFile(DelegatedAdminsDBPath); err == nil {
		var admins []DelegatedAdmin
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &admins); err != nil {
				return result, fmt.Errorf("parse delegated admins for quota migration: %w", err)
			}
		}

		changed := false
		for i := range admins {
			before := admins[i].TrafficLimitBytes
			after := canonicalTrafficQuotaBytes(before)
			if after != before {
				admins[i].TrafficLimitBytes = after
				admins[i].UpdatedAt = timeNowUTC()
				result.DelegatedAdmins++
				changed = true
			}
		}

		if changed {
			if err := writeQuotaJSONAtomic(DelegatedAdminsDBPath, admins, 0600); err != nil {
				return result, fmt.Errorf("write delegated-admin quota migration: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("read delegated admins for quota migration: %w", err)
	}

	clientsDir := filepath.Join(DataBaseDir, "database", "clients")
	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read clients for quota migration: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(clientsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return result, fmt.Errorf("read %s for quota migration: %w", entry.Name(), err)
		}

		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var client map[string]interface{}
		if err := dec.Decode(&client); err != nil {
			return result, fmt.Errorf("parse %s for quota migration: %w", entry.Name(), err)
		}

		before, ok := quotaJSONInt64(client["traffic_limit_bytes"])
		if !ok || before <= 0 {
			continue
		}
		after := canonicalTrafficQuotaBytes(before)
		if after == before {
			continue
		}

		client["traffic_limit_bytes"] = after
		mode := os.FileMode(0600)
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := writeQuotaJSONAtomic(path, client, mode); err != nil {
			return result, fmt.Errorf("write %s quota migration: %w", entry.Name(), err)
		}
		result.Clients++
	}

	return result, nil
}

// Small wrapper so migration tests can stub time later without changing quota
// semantics.
var timeNowUTC = func() time.Time {
	return time.Now().UTC()
}
