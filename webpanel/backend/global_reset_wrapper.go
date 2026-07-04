package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

func updateClientWithGlobalResetHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	resetRequested := hasResetTrafficFlag(body, r.URL.Query())
	clientName := strings.TrimSpace(mux.Vars(r)["name"])
	if clientName == "" {
		clientName = clientNameFromBody(body)
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	rec := httptest.NewRecorder()
	updateClientHandler(rec, r)

	code := rec.Code
	if code == 0 {
		code = http.StatusOK
	}

	if resetRequested && code >= 200 && code < 300 && clientName != "" {
		if err := resetClientPersistentUsage(clientName); err != nil {
			log.Printf("global reset failed for %s: %v", clientName, err)
		} else {
			log.Printf("global reset done for %s", clientName)
		}
	}

	for k, vals := range rec.Header() {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(code)
	_, _ = w.Write(rec.Body.Bytes())
}

func hasResetTrafficFlag(body []byte, q url.Values) bool {
	for k, vals := range q {
		if isResetTrafficKey(k) {
			for _, v := range vals {
				if isTruthyString(v) {
					return true
				}
			}
		}
	}

	var v interface{}
	if json.Unmarshal(body, &v) != nil {
		return false
	}

	return walkResetFlag("", v)
}

func walkResetFlag(key string, v interface{}) bool {
	if isResetTrafficKey(key) && isTruthyValue(v) {
		return true
	}

	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if walkResetFlag(k, val) {
				return true
			}
		}
	case []interface{}:
		for _, val := range t {
			if walkResetFlag(key, val) {
				return true
			}
		}
	}

	return false
}

func isResetTrafficKey(k string) bool {
	k = strings.ToLower(strings.ReplaceAll(k, "-", "_"))

	if !strings.Contains(k, "reset") {
		return false
	}

	return strings.Contains(k, "traffic") ||
		strings.Contains(k, "usage") ||
		strings.Contains(k, "used") ||
		strings.Contains(k, "data")
}

func isTruthyValue(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return isTruthyString(t)
	case float64:
		return t == 1
	case int:
		return t == 1
	}

	return false
}

func isTruthyString(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

func clientNameFromBody(body []byte) string {
	var m map[string]interface{}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}

	for _, k := range []string{"name", "client_name", "common_name", "username"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

func resetClientPersistentUsage(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) {
		return nil
	}

	cmd := exec.Command("/usr/local/bin/irangate-pg-reset-client.py", name)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("postgres reset output for %s: %s", name, strings.TrimSpace(string(out)))
	}
	if err != nil {
		return err
	}

	return nil
}

func resetGlobalUsageFile(name string) error {
	path := "/opt/irangate/traffic/global_usage_totals.json"

	data := readJSONMap(path)
	if data == nil {
		data = map[string]interface{}{}
	}

	last := ensureMap(data, "last")
	totals := ensureMap(data, "totals")
	blocked := ensureMap(data, "blocked")

	for k := range last {
		if k == name || strings.HasSuffix(k, ":"+name) {
			delete(last, k)
		}
	}

	totals[name] = map[string]interface{}{
		"rx":         0,
		"tx":         0,
		"total":      0,
		"updated_at": time.Now().Unix(),
	}

	delete(blocked, name)

	baselineCurrentStats(last, "local", "/opt/irangate/traffic/current_stats.json", name)
	baselineNodeStats(last, "/opt/irangate/traffic/node_current_stats.json", name)

	return writeJSONFile(path, data)
}

func baselineCurrentStats(last map[string]interface{}, source string, path string, name string) {
	arr := readJSONArray(path)

	for _, item := range arr {
		if itemName(item) != name {
			continue
		}

		last[source+":"+name] = counterMap(item)
	}
}

func baselineNodeStats(last map[string]interface{}, path string, name string) {
	arr := readJSONArray(path)

	for _, item := range arr {
		if itemName(item) != name {
			continue
		}

		nodeID, _ := item["node_id"].(string)
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			nodeID = "node"
		}

		last[nodeID+":"+name] = counterMap(item)
	}
}

func counterMap(item map[string]interface{}) map[string]interface{} {
	rx := resetNum(item, "bytes_received", "current_received", "upload")
	tx := resetNum(item, "bytes_sent", "current_sent", "download")
	total := resetNum(item, "total_bytes", "current_total", "used_bytes", "usage", "traffic")

	if total <= 0 {
		total = rx + tx
	}

	return map[string]interface{}{
		"rx":    rx,
		"tx":    tx,
		"total": total,
	}
}

func cleanGenericUsageFile(path string, name string) {
	data := readJSONMap(path)
	if data == nil {
		return
	}

	delete(data, name)

	for _, section := range []string{"last", "totals", "blocked"} {
		m, ok := data[section].(map[string]interface{})
		if !ok {
			continue
		}

		for k := range m {
			if k == name || strings.HasSuffix(k, ":"+name) {
				delete(m, k)
			}
		}
	}

	_ = writeJSONFile(path, data)
}

func readJSONMap(path string) map[string]interface{} {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var m map[string]interface{}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}

	return m
}

func readJSONArray(path string) []map[string]interface{} {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var arr []map[string]interface{}
	if json.Unmarshal(b, &arr) != nil {
		return nil
	}

	return arr
}

func writeJSONFile(path string, data interface{}) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

func ensureMap(data map[string]interface{}, key string) map[string]interface{} {
	if m, ok := data[key].(map[string]interface{}); ok {
		return m
	}

	m := map[string]interface{}{}
	data[key] = m
	return m
}

func itemName(item map[string]interface{}) string {
	for _, k := range []string{"name", "client_name", "common_name", "username"} {
		if v, ok := item[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

func resetNum(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}

		switch t := v.(type) {
		case float64:
			return t
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

func blockLocalClient(name string) {
	_ = os.MkdirAll("/etc/openvpn/blocked-clients", 0755)
	_ = os.WriteFile(filepath.Join("/etc/openvpn/blocked-clients", name), []byte("blocked\n"), 0644)
}

func unblockLocalClient(name string) {
	_ = os.Remove(filepath.Join("/etc/openvpn/blocked-clients", name))
}

func killLocalOpenVPNClient(name string) {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:7505", 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("kill " + name + "\nquit\n"))
}

type resetNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	AgentURL string `json:"agent_url"`
	Token    string `json:"token"`
	IsLocal  bool   `json:"is_local"`
}

func loadResetNodes() []resetNode {
	b, err := os.ReadFile("/opt/irangate/webpanel/nodes.json")
	if err != nil {
		return nil
	}

	var arr []resetNode
	if json.Unmarshal(b, &arr) == nil && len(arr) > 0 {
		return arr
	}

	var obj struct {
		Nodes []resetNode `json:"nodes"`
	}
	if json.Unmarshal(b, &obj) == nil {
		return obj.Nodes
	}

	return nil
}

func blockRemoteNodes(name string) {
	for _, node := range loadResetNodes() {
		if node.IsLocal {
			continue
		}
		callResetNode(node, "/block-client", name)
	}
}

func unblockRemoteNodes(name string) {
	for _, node := range loadResetNodes() {
		if node.IsLocal {
			continue
		}
		callResetNode(node, "/unblock-client", name)
	}
}

func callResetNode(node resetNode, path string, name string) {
	u, err := url.Parse(node.AgentURL)
	if err != nil || u.Hostname() == "" {
		return
	}

	target := u.Scheme + "://" + u.Hostname() + ":5024" + path + "?name=" + url.QueryEscape(name)

	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return
	}

	req.Header.Set("Authorization", "Bearer "+node.Token)

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("node reset call failed %s %s: %v", node.ID, path, err)
		return
	}

	_ = resp.Body.Close()
}
