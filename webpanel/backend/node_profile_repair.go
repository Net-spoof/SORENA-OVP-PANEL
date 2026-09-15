package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// nodeProfileRepairTransport keeps remote-node profile provisioning reliable.
//
// Older code in api.go can emit an escaped JSON body for POST /clients, and
// existing clients can have node ACL metadata without the corresponding .ovpn
// file on the remote node. This transport fixes the create payload and, for a
// node-profile download, provisions the profile with valid JSON before the GET.
type nodeProfileRepairTransport struct {
	base http.RoundTripper
}

func init() {
	base := http.DefaultTransport
	http.DefaultTransport = &nodeProfileRepairTransport{base: base}
}

func (t *nodeProfileRepairTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return t.base.RoundTrip(req)
	}

	if req.Method == http.MethodPost && req.URL.Path == "/clients" {
		return t.roundTripFixedCreate(req)
	}

	if req.Method == http.MethodGet {
		if name, ok := nodeDownloadClientName(req.URL.Path); ok {
			return t.roundTripEnsuredDownload(req, name)
		}
	}

	return t.base.RoundTrip(req)
}

func (t *nodeProfileRepairTransport) roundTripFixedCreate(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return t.base.RoundTrip(req)
	}

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()

	body := bytes.TrimSpace(raw)
	if !json.Valid(body) {
		repaired := bytes.ReplaceAll(body, []byte(`\"`), []byte(`"`))
		if json.Valid(repaired) {
			body = repaired
		}
	}

	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	return t.base.RoundTrip(clone)
}

func (t *nodeProfileRepairTransport) roundTripEnsuredDownload(req *http.Request, name string) (*http.Response, error) {
	provisionErr := t.provisionProfileForDownload(req, name)

	resp, err := t.base.RoundTrip(req.Clone(req.Context()))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusNotFound {
		return resp, nil
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}

	if !nodeConfigMissingBody(body) {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		return resp, nil
	}

	// If the pre-flight provision failed, try one explicit repair once more. This
	// also handles a transient failure between client creation and first download.
	if provisionErr != nil {
		provisionErr = t.provisionProfileForDownload(req, name)
	}

	if provisionErr == nil {
		retry, retryErr := t.base.RoundTrip(req.Clone(req.Context()))
		if retryErr != nil {
			return nil, retryErr
		}
		return retry, nil
	}

	message := fmt.Sprintf(
		"Node profile provisioning failed for %s: %v; original download response: %s",
		name,
		provisionErr,
		strings.TrimSpace(string(body)),
	)
	return nodeRepairErrorResponse(req, http.StatusBadGateway, message), nil
}

func (t *nodeProfileRepairTransport) provisionProfileForDownload(req *http.Request, name string) error {
	createURL := *req.URL
	createURL.Path = "/clients"
	createURL.RawPath = ""
	createURL.RawQuery = ""
	createURL.Fragment = ""

	payload, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}

	createReq, err := http.NewRequestWithContext(
		req.Context(),
		http.MethodPost,
		createURL.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}

	createReq.Header.Set("Content-Type", "application/json")
	if auth := req.Header.Get("Authorization"); auth != "" {
		createReq.Header.Set("Authorization", auth)
	}

	created, err := t.base.RoundTrip(createReq)
	if err != nil {
		return err
	}
	defer created.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(created.Body, 64*1024))
	if created.StatusCode < 200 || created.StatusCode >= 300 {
		return fmt.Errorf(
			"HTTP %d %s",
			created.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	if len(bytes.TrimSpace(body)) != 0 {
		var payload map[string]interface{}
		if json.Unmarshal(body, &payload) == nil {
			if success, exists := payload["success"].(bool); exists && !success {
				return fmt.Errorf("%v", payload["message"])
			}
		}
	}

	return nil
}

func nodeConfigMissingBody(body []byte) bool {
	var payload map[string]interface{}
	if json.Unmarshal(body, &payload) == nil {
		message := strings.TrimSpace(fmt.Sprintf("%v", payload["message"]))
		return strings.EqualFold(message, "Client config not found")
	}

	return bytes.Contains(body, []byte("Client config not found"))
}

func nodeRepairErrorResponse(req *http.Request, status int, message string) *http.Response {
	body := []byte(message)
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")

	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func nodeDownloadClientName(path string) (string, bool) {
	const prefix = "/clients/"
	const suffix = "/download"

	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}

	escaped := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if escaped == "" || strings.Contains(escaped, "/") {
		return "", false
	}

	name, err := url.PathUnescape(escaped)
	if err != nil || name == "" || strings.ContainsAny(name, "/\\\x00\r\n") {
		return "", false
	}

	return name, true
}
