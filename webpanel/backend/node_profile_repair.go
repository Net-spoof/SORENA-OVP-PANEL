package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// nodeProfileRepairTransport repairs remote-node client provisioning and
// self-heals profiles missing on a node when a subscription download is used.
type nodeProfileRepairTransport struct {
	base http.RoundTripper
}

func init() {
	http.DefaultTransport = &nodeProfileRepairTransport{base: http.DefaultTransport}
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
			return t.roundTripDownloadWithRepair(req, name)
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
	if bytes.HasPrefix(body, []byte(`{\"name\":`)) {
		body = bytes.ReplaceAll(body, []byte(`\"`), []byte(`"`))
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return t.base.RoundTrip(clone)
}

func (t *nodeProfileRepairTransport) roundTripDownloadWithRepair(req *http.Request, name string) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
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
	if !bytes.Contains(body, []byte(`"message": "Client config not found"`)) &&
		!bytes.Contains(body, []byte(`"message":"Client config not found"`)) {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		return resp, nil
	}

	createURL := *req.URL
	createURL.Path = "/clients"
	createURL.RawPath = ""
	createURL.RawQuery = ""
	createURL.Fragment = ""
	payload, _ := json.Marshal(map[string]string{"name": name})
	createReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, createURL.String(), bytes.NewReader(payload))
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		return resp, nil
	}
	createReq.Header.Set("Content-Type", "application/json")
	if auth := req.Header.Get("Authorization"); auth != "" {
		createReq.Header.Set("Authorization", auth)
	}
	created, err := t.base.RoundTrip(createReq)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(created.Body, 64*1024))
	_ = created.Body.Close()
	if created.StatusCode < 200 || created.StatusCode >= 300 {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		return resp, nil
	}
	return t.base.RoundTrip(req.Clone(req.Context()))
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
