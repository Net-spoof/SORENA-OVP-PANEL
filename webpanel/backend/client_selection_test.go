package main

import "testing"

func TestSameClientSelection(t *testing.T) {
	tests := []struct {
		name      string
		existing  []string
		requested []string
		want      bool
	}{
		{name: "unchanged master", existing: []string{"local"}, requested: []string{"local"}, want: true},
		{name: "reordered nodes", existing: []string{"local", "de", "tr"}, requested: []string{"tr", "local", "de"}, want: true},
		{name: "added node", existing: []string{"local"}, requested: []string{"local", "de"}, want: false},
		{name: "removed node", existing: []string{"local", "de"}, requested: []string{"local"}, want: false},
		{name: "different location", existing: []string{"de"}, requested: []string{"tr"}, want: false},
		{name: "empty selections", existing: nil, requested: []string{}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sameClientSelection(tt.existing, tt.requested)
			if got != tt.want {
				t.Errorf("sameClientSelection(%v, %v) = %v; want %v", tt.existing, tt.requested, got, tt.want)
			}
		})
	}
}
