package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

const routingCLIBin = "/usr/local/bin/irangate-routing"
const routingDataDir = "/opt/irangate/routing"

type routingCommandResult struct {
	Command  []string `json:"command"`
	Output   string   `json:"output"`
	ExitCode int      `json:"exit_code"`
}

type createRoutingOutboundRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Link     string `json:"link"`
	Remote   string `json:"remote"`
	Username string `json:"username"`
	Password string `json:"password"`
	Version  string `json:"version"`
}

type bindRoutingRequest struct {
	Port      int    `json:"port"`
	Mode      string `json:"mode"`
	Outbounds string `json:"outbounds"`
}

type updateRoutingOutboundRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Link     string `json:"link"`
	Remote   string `json:"remote"`
	Username string `json:"username"`
	Password string `json:"password"`
	Version  string `json:"version"`
}

type testRoutingOutboundRequest struct {
	Tag string `json:"tag"`
}

func runRoutingCommand(r *http.Request, timeout time.Duration, args ...string) (routingCommandResult, error) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	cmdArgs := append([]string{}, args...)
	result := routingCommandResult{
		Command: append([]string{routingCLIBin}, cmdArgs...),
	}

	cmd := exec.CommandContext(ctx, routingCLIBin, cmdArgs...)
	output, err := cmd.CombinedOutput()
	result.Output = strings.TrimSpace(string(output))

	if ctx.Err() == context.DeadlineExceeded {
		result.ExitCode = 124
		return result, fmt.Errorf("routing command timeout")
	}

	if err != nil {
		result.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		if result.Output != "" {
			return result, fmt.Errorf(result.Output)
		}
		return result, err
	}

	result.ExitCode = 0
	return result, nil
}

func readRoutingJSONFile(name string) interface{} {
	path := filepath.Join(routingDataDir, name)

	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{
			"error": err.Error(),
			"path":  path,
		}
	}

	var decoded interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return map[string]interface{}{
			"error": err.Error(),
			"path":  path,
			"raw":   string(data),
		}
	}

	return decoded
}

func getRoutingStatusHandler(w http.ResponseWriter, r *http.Request) {
	listResult, _ := runRoutingCommand(r, 20*time.Second, "list")

	serviceActive := "unknown"
	if out, err := exec.Command("systemctl", "is-active", "irangate-routing.service").CombinedOutput(); err == nil {
		serviceActive = strings.TrimSpace(string(out))
	} else {
		serviceActive = strings.TrimSpace(string(out))
	}

	configSummary := map[string]interface{}{}
	configData, err := os.ReadFile("/etc/sing-box/irangate-routing.json")
	if err == nil {
		var parsed map[string]interface{}
		if json.Unmarshal(configData, &parsed) == nil {
			configSummary = parsed
		}
	}

	sendSuccess(w, "Routing status retrieved", map[string]interface{}{
		"service_active": serviceActive,
		"list_output":    listResult.Output,
		"outbounds":      readRoutingJSONFile("outbounds.json"),
		"balancers":      readRoutingJSONFile("balancers.json"),
		"rules":          readRoutingJSONFile("rules.json"),
		"state":          readRoutingJSONFile("state.json"),
		"engine":         readRoutingJSONFile("engine.json"),
		"sing_box":       configSummary,
	})
}

func getRoutingOutboundsHandler(w http.ResponseWriter, r *http.Request) {
	sendSuccess(w, "Routing outbounds retrieved", map[string]interface{}{
		"outbounds": readRoutingJSONFile("outbounds.json"),
		"balancers": readRoutingJSONFile("balancers.json"),
		"rules":     readRoutingJSONFile("rules.json"),
		"state":     readRoutingJSONFile("state.json"),
	})
}

func createRoutingOutboundHandler(w http.ResponseWriter, r *http.Request) {
	var req createRoutingOutboundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Type = strings.TrimSpace(req.Type)
	req.Link = strings.TrimSpace(req.Link)

	if req.Name == "" {
		sendError(w, "Outbound name is required", http.StatusBadRequest)
		return
	}

	if req.Type == "" || req.Type == "auto" {
		req.Type = "xray"
	}

	args := []string{
		"add-outbound",
		"--name", req.Name,
		"--type", req.Type,
	}

	if req.Link != "" {
		args = append(args, "--link", req.Link)
	}
	if strings.TrimSpace(req.Remote) != "" {
		args = append(args, "--remote", strings.TrimSpace(req.Remote))
	}
	if strings.TrimSpace(req.Username) != "" {
		args = append(args, "--username", strings.TrimSpace(req.Username))
	}
	if strings.TrimSpace(req.Password) != "" {
		args = append(args, "--password", strings.TrimSpace(req.Password))
	}
	if strings.TrimSpace(req.Version) != "" {
		args = append(args, "--version", strings.TrimSpace(req.Version))
	}

	result, err := runRoutingCommand(r, 30*time.Second, args...)
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to add outbound: %v", err), http.StatusBadRequest)
		return
	}

	sendSuccess(w, "Outbound added", result)
}

func bindRoutingHandler(w http.ResponseWriter, r *http.Request) {
	var req bindRoutingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Port <= 0 {
		req.Port = 443
	}
	req.Mode = strings.TrimSpace(req.Mode)
	if req.Mode == "" {
		req.Mode = "single"
	}
	req.Outbounds = strings.TrimSpace(req.Outbounds)
	if req.Outbounds == "" {
		sendError(w, "Outbounds is required", http.StatusBadRequest)
		return
	}

	result, err := runRoutingCommand(
		r,
		30*time.Second,
		"bind",
		"--port", fmt.Sprintf("%d", req.Port),
		"--mode", req.Mode,
		"--outbounds", req.Outbounds,
	)
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to bind routing: %v", err), http.StatusBadRequest)
		return
	}

	sendSuccess(w, "Routing bind updated", result)
}

func generateRoutingHandler(w http.ResponseWriter, r *http.Request) {
	result, err := runRoutingCommand(r, 60*time.Second, "generate-engine", "--write", "--check")
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to generate routing engine: %v", err), http.StatusInternalServerError)
		return
	}

	sendSuccess(w, "Routing engine generated", result)
}

func applyRoutingHandler(w http.ResponseWriter, r *http.Request) {
	result, err := runRoutingCommand(r, 90*time.Second, "apply-routing")
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to apply routing: %v", err), http.StatusInternalServerError)
		return
	}

	sendSuccess(w, "Routing applied", result)
}

func testRoutingOutboundHandler(w http.ResponseWriter, r *http.Request) {
	var req testRoutingOutboundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Tag = strings.TrimSpace(req.Tag)
	if req.Tag == "" {
		sendError(w, "Outbound tag is required", http.StatusBadRequest)
		return
	}

	result, err := runRoutingCommand(r, 30*time.Second, "test-outbound", req.Tag)
	if err != nil {
		sendError(w, fmt.Sprintf("Outbound test failed: %v", err), http.StatusBadRequest)
		return
	}

	sendSuccess(w, "Outbound test completed", result)
}

func updateRoutingOutboundHandler(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimSpace(mux.Vars(r)["tag"])
	if tag == "" {
		sendError(w, "Outbound tag is required", http.StatusBadRequest)
		return
	}

	var req updateRoutingOutboundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	args := []string{"update-outbound", tag}

	if strings.TrimSpace(req.Name) != "" {
		args = append(args, "--name", strings.TrimSpace(req.Name))
	}
	if strings.TrimSpace(req.Type) != "" && strings.TrimSpace(req.Type) != "auto" {
		args = append(args, "--type", strings.TrimSpace(req.Type))
	}
	if req.Link != "" {
		args = append(args, "--link", strings.TrimSpace(req.Link))
	}
	if req.Remote != "" {
		args = append(args, "--remote", strings.TrimSpace(req.Remote))
	}
	if req.Username != "" {
		args = append(args, "--username", strings.TrimSpace(req.Username))
	}
	if req.Password != "" {
		args = append(args, "--password", strings.TrimSpace(req.Password))
	}
	if req.Version != "" {
		args = append(args, "--version", strings.TrimSpace(req.Version))
	}

	result, err := runRoutingCommand(r, 30*time.Second, args...)
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to update outbound: %v", err), http.StatusBadRequest)
		return
	}

	sendSuccess(w, "Outbound updated", result)
}

func deleteRoutingOutboundHandler(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimSpace(mux.Vars(r)["tag"])
	if tag == "" {
		sendError(w, "Outbound tag is required", http.StatusBadRequest)
		return
	}

	args := []string{"delete-outbound", tag}
	if r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true" {
		args = append(args, "--force")
	}

	result, err := runRoutingCommand(r, 30*time.Second, args...)
	if err != nil {
		sendError(w, fmt.Sprintf("Failed to delete outbound: %v", err), http.StatusBadRequest)
		return
	}

	sendSuccess(w, "Outbound deleted", result)
}
