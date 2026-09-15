package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// nodeProfileRepairTransport keeps remote-node profile provisioning reliable.
//
// Older code in api.go can emit an escaped JSON body for POST /clients, and
// existing clients can have node ACL metadata without the corresponding .ovpn
// file on the remote node. This transport fixes the create payload and, for a
// node-profile download, provisions the profile with valid JSON before the GET.
//
// A missing node profile is treated as a recoverable state. The transport
// provisions the client and retries the download a few times before returning a
// diagnostic error. This keeps subscription links self-healing after node
// migration/reinstall or when an older master binary created only the ACL entry.
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
	// Provision first so the common case succeeds with a single GET. This is
	// intentionally done through t.base to avoid recursively re-entering this
	// RoundTripper.
	lastProvisionErr := t.provisionProfileForDownload(req, name)

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

	// The node explicitly says the .ovpn file is missing. Re-provision and retry
	// a few times. The node client helper is synchronous, but the short retry
	// window also covers filesystem/service timing during migrations and restores.
	lastBody := body
	for attempt := 1; attempt <= 3; attempt++ {
		if err := t.provisionProfileForDownload(req, name); err != nil {
			lastProvisionErr = err
			time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
			continue
		}

		lastProvisionErr = nil
		time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)

		retry, retryErr := t.base.RoundTrip(req.Clone(req.Context()))
		if retryErr != nil {
			lastProvisionErr = retryErr
			continue
		}

		if retry.StatusCode != http.StatusNotFound {
			return retry, nil
		}

		retryBody, retryReadErr := io.ReadAll(io.LimitReader(retry.Body, 64*1024))
		_ = retry.Body.Close()
		if retryReadErr != nil {
			lastProvisionErr = retryReadErr
			continue
		}

		lastBody = retryBody
		if !nodeConfigMissingBody(retryBody) {
			retry.Body = io.NopCloser(bytes.NewReader(retryBody))
			retry.ContentLength = int64(len(retryBody))
			return retry, nil
		}
	}

	if lastProvisionErr == nil {
		lastProvisionErr = fmt.Errorf("node accepted provisioning but the profile is still missing")
	}

	message := fmt.Sprintf(
		"Node profile provisioning failed for %s: %v; last download response: %s",
		name,
		lastProvisionErr,
		strings.TrimSpace(string(lastBody)),
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
