package main

import "testing"

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
