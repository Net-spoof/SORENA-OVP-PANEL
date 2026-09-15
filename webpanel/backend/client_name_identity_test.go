package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withIdentityTestDataDir(t *testing.T) string {
	t.Helper()
	old := DataBaseDir
	dir := t.TempDir()
	DataBaseDir = dir
	t.Cleanup(func() { DataBaseDir = old })
	if err := os.MkdirAll(filepath.Join(dir, "database", "clients"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "traffic"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExactGlobalUsagePreservesClientCase(t *testing.T) {
	dir := withIdentityTestDataDir(t)
	payload := `{"totals":{"meeee":{"total":100},"Meeee":{"total":250}}}`
	if err := os.WriteFile(filepath.Join(dir, "traffic", "global_usage_totals.json"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	_, _, lower, ok := exactGlobalUsage("meeee")
	if !ok || lower != 100 {
		t.Fatalf("lower bucket = %d ok=%v", lower, ok)
	}
	_, _, upper, ok := exactGlobalUsage("Meeee")
	if !ok || upper != 250 {
		t.Fatalf("upper bucket = %d ok=%v", upper, ok)
	}
	if _, _, _, ok := exactGlobalUsage("MEEEE"); ok {
		t.Fatal("unexpected case-insensitive usage match")
	}
}

func TestCreateClientRejectsCaseInsensitiveCollision(t *testing.T) {
	dir := withIdentityTestDataDir(t)
	existing := filepath.Join(dir, "database", "clients", "Meeee.json")
	if err := os.WriteFile(existing, []byte(`{"name":"Meeee"}`), 0600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"name":"MEEEE"}`))
	rec := httptest.NewRecorder()
	createClientHandler(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCanonicalNewClientNameIsLowercase(t *testing.T) {
	if got := canonicalClientNameForCreate("  AbC_123  "); got != "abc_123" {
		t.Fatalf("canonical=%q", got)
	}
}
