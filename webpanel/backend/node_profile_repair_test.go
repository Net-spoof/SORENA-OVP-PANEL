package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNodeProfileRepairEnsuresProfileBeforeDownload(t *testing.T) {
	var mu sync.Mutex
	provisioned := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/clients":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if payload["name"] != "alice" {
				http.Error(w, "wrong name", http.StatusBadRequest)
				return
			}
			mu.Lock()
			provisioned = true
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"success":true}`)

		case r.Method == http.MethodGet && r.URL.Path == "/clients/alice/download":
			mu.Lock()
			ready := provisioned
			mu.Unlock()
			if !ready {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"success":false,"message":"Client config not found"}`)
				return
			}
			_, _ = io.WriteString(w, "client\nremote node.example 443\n")

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	transport := &nodeProfileRepairTransport{base: server.Client().Transport}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL + "/clients/alice/download")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "remote node.example 443") {
		t.Fatalf("unexpected profile: %s", body)
	}
}

func TestNodeProfileRepairFixesEscapedCreateJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if payload["name"] != "bob" {
			http.Error(w, "wrong name", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()

	transport := &nodeProfileRepairTransport{base: server.Client().Transport}
	client := &http.Client{Transport: transport}

	req, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/clients",
		strings.NewReader(`{\"name\":"bob"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, body)
	}
}
