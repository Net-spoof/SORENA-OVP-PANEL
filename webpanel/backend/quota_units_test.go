package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalTrafficQuotaBytes(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want int64
	}{
		{"decimal 100 GB stays SI", 100_000_000_000, 100_000_000_000},
		{"legacy 100 GiB becomes 100 GB", 100 * (1 << 30), 100_000_000_000},
		{"legacy 1.5 GiB becomes 1.5 GB", 3 * (1 << 29), 1_500_000_000},
		{"legacy 500 MiB becomes 500 MB", 500 * (1 << 20), 500_000_000},
		{"legacy 1 TiB becomes 1 TB", 1 << 40, 1_000_000_000_000},
		{"decimal 0.01 MB stays SI", 10_000, 10_000},
		{"zero stays zero", 0, 0},
		{"negative stays negative", -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalTrafficQuotaBytes(tt.in); got != tt.want {
				t.Fatalf("canonicalTrafficQuotaBytes(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestMigrateLegacyTrafficQuotaUnits(t *testing.T) {
	oldDataBaseDir := DataBaseDir
	oldDelegatedAdminsDBPath := DelegatedAdminsDBPath
	defer func() {
		DataBaseDir = oldDataBaseDir
		DelegatedAdminsDBPath = oldDelegatedAdminsDBPath
	}()

	root := t.TempDir()
	DataBaseDir = root
	DelegatedAdminsDBPath = filepath.Join(root, "webpanel", "delegated_admins.json")

	if err := os.MkdirAll(filepath.Dir(DelegatedAdminsDBPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "database", "clients"), 0755); err != nil {
		t.Fatal(err)
	}

	admins := []DelegatedAdmin{{
		Username:          "res_test",
		TrafficLimitBytes: 100 * (1 << 30),
		UsageSnapshots:    map[string]int64{},
	}}
	adminData, err := json.Marshal(admins)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DelegatedAdminsDBPath, adminData, 0600); err != nil {
		t.Fatal(err)
	}

	clientPath := filepath.Join(root, "database", "clients", "client.json")
	clientData := []byte(`{"name":"client","traffic_limit_bytes":53687091200,"traffic_used_bytes":12345}`)
	if err := os.WriteFile(clientPath, clientData, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateLegacyTrafficQuotaUnits()
	if err != nil {
		t.Fatal(err)
	}
	if result.DelegatedAdmins != 1 || result.Clients != 1 {
		t.Fatalf("migration result = %+v, want delegated_admins=1 clients=1", result)
	}

	var gotAdmins []DelegatedAdmin
	raw, err := os.ReadFile(DelegatedAdminsDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &gotAdmins); err != nil {
		t.Fatal(err)
	}
	if len(gotAdmins) != 1 || gotAdmins[0].TrafficLimitBytes != 100_000_000_000 {
		t.Fatalf("delegated quota after migration = %+v", gotAdmins)
	}

	var gotClient map[string]interface{}
	raw, err = os.ReadFile(clientPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &gotClient); err != nil {
		t.Fatal(err)
	}
	if got := int64(gotClient["traffic_limit_bytes"].(float64)); got != 50_000_000_000 {
		t.Fatalf("client quota after migration = %d, want 50000000000", got)
	}
	if got := int64(gotClient["traffic_used_bytes"].(float64)); got != 12345 {
		t.Fatalf("usage counter changed during quota migration: got %d", got)
	}

	again, err := migrateLegacyTrafficQuotaUnits()
	if err != nil {
		t.Fatal(err)
	}
	if again.DelegatedAdmins != 0 || again.Clients != 0 {
		t.Fatalf("migration is not idempotent: second result = %+v", again)
	}
}
