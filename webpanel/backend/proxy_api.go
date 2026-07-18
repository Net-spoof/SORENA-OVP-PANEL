package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const panelProxyEnvFile = "/opt/irangate/panel-proxy.env"
const panelProxyDropinDir = "/etc/systemd/system/irangate-webpanel.service.d"
const panelProxyDropinFile = "/etc/systemd/system/irangate-webpanel.service.d/proxy.conf"

type panelProxyRequest struct {
	Enabled  bool   `json:"enabled"`
	ProxyURL string `json:"proxy_url"`
	TestURL  string `json:"test_url"`
}

func panelProxyJSON(w http.ResponseWriter, code int, payload map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func panelProxyError(w http.ResponseWriter, msg string, code int) {
	panelProxyJSON(w, code, map[string]interface{}{
		"success": false,
		"message": msg,
	})
}

func validatePanelProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("proxy_url is required")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s; use http:// or socks5://", u.Scheme)
	}

	if u.Host == "" {
		return nil, errors.New("proxy host is required")
	}

	return u, nil
}

func readPanelProxyURLFromEnvFile() string {
	b, err := os.ReadFile(panelProxyEnvFile)
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ALL_PROXY=") {
			v := strings.TrimPrefix(line, "ALL_PROXY=")
			v = strings.Trim(v, "\"'")
			return v
		}
	}

	return ""
}

func panelProxyStatusHandler(w http.ResponseWriter, r *http.Request) {
	proxyURL := readPanelProxyURLFromEnvFile()
	_, dropinErr := os.Stat(panelProxyDropinFile)

	envOut, _ := exec.Command("systemctl", "show", "irangate-webpanel", "-p", "Environment", "--value").CombinedOutput()

	panelProxyJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Panel proxy status retrieved",
		"data": map[string]interface{}{
			"enabled":       dropinErr == nil && proxyURL != "",
			"proxy_url":     proxyURL,
			"env_file":      panelProxyEnvFile,
			"dropin_file":   panelProxyDropinFile,
			"service_env":   strings.TrimSpace(string(envOut)),
			"service":       "irangate-webpanel",
			"note":          "This affects irangate-webpanel outbound HTTP/HTTPS requests and child processes, not OpenVPN client traffic.",
		},
	})
}

func writePanelProxyEnv(proxyURL string) error {
	if err := os.MkdirAll("/opt/irangate", 0755); err != nil {
		return err
	}

	noProxy := "127.0.0.1,localhost,::1,10.8.0.0/24,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"

	content := ""
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		content += key + "=" + strconv.Quote(proxyURL) + "\n"
	}
	content += "NO_PROXY=" + strconv.Quote(noProxy) + "\n"
	content += "no_proxy=" + strconv.Quote(noProxy) + "\n"

	return os.WriteFile(panelProxyEnvFile, []byte(content), 0600)
}

func writePanelProxyDropin() error {
	if err := os.MkdirAll(panelProxyDropinDir, 0755); err != nil {
		return err
	}

	content := `[Service]
EnvironmentFile=-/opt/irangate/panel-proxy.env
`

	return os.WriteFile(panelProxyDropinFile, []byte(content), 0644)
}

func scheduleWebpanelRestart() {
	go func() {
		time.Sleep(1200 * time.Millisecond)
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", "restart", "irangate-webpanel").Run()
	}()
}

func panelProxyApplyHandler(w http.ResponseWriter, r *http.Request) {
	var req panelProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		panelProxyError(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !req.Enabled {
		clearPanelProxyFiles()
		_ = exec.Command("systemctl", "daemon-reload").Run()
		scheduleWebpanelRestart()

		panelProxyJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "Panel proxy disabled. Webpanel restart scheduled.",
		})
		return
	}

	u, err := validatePanelProxyURL(req.ProxyURL)
	if err != nil {
		panelProxyError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := writePanelProxyEnv(u.String()); err != nil {
		panelProxyError(w, "Failed to write env file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := writePanelProxyDropin(); err != nil {
		panelProxyError(w, "Failed to write systemd drop-in: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		panelProxyError(w, "systemctl daemon-reload failed: "+string(out), http.StatusInternalServerError)
		return
	}

	scheduleWebpanelRestart()

	panelProxyJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Panel proxy saved. Webpanel restart scheduled.",
		"data": map[string]interface{}{
			"proxy_url": u.String(),
			"env_file": panelProxyEnvFile,
			"dropin": panelProxyDropinFile,
		},
	})
}

func clearPanelProxyFiles() {
	_ = os.Remove(panelProxyEnvFile)
	_ = os.Remove(panelProxyDropinFile)
}

func panelProxyClearHandler(w http.ResponseWriter, r *http.Request) {
	clearPanelProxyFiles()
	_ = exec.Command("systemctl", "daemon-reload").Run()
	scheduleWebpanelRestart()

	panelProxyJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Panel proxy cleared. Webpanel restart scheduled.",
	})
}

func panelProxyTestHandler(w http.ResponseWriter, r *http.Request) {
	var req panelProxyRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	testURL := strings.TrimSpace(req.TestURL)
	if testURL == "" {
		testURL = "https://api.telegram.org"
	}

	u, err := validatePanelProxyURL(req.ProxyURL)
	if err != nil {
		panelProxyError(w, err.Error(), http.StatusBadRequest)
		return
	}

	proxyFunc := http.ProxyURL(u)
	tr := &http.Transport{
		Proxy: proxyFunc,
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   12 * time.Second,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		panelProxyError(w, "Invalid test_url: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := client.Do(httpReq)
	elapsed := time.Since(start)

	if err != nil {
		panelProxyJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "Proxy test failed",
			"data": map[string]interface{}{
				"proxy_url": u.String(),
				"test_url":  testURL,
				"error":     err.Error(),
				"latency_ms": elapsed.Milliseconds(),
			},
		})
		return
	}
	defer resp.Body.Close()

	panelProxyJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Proxy test OK",
		"data": map[string]interface{}{
			"proxy_url":   u.String(),
			"test_url":    testURL,
			"status_code": resp.StatusCode,
			"latency_ms":  elapsed.Milliseconds(),
		},
	})
}
