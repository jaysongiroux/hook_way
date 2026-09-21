package editor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doRequest(t *testing.T, method, url, contentType string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSchemaEndpointReturnsJSON(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	resp := doRequest(t, http.MethodGet, srv.URL+"/schema", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS *, got %q", got)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("schema not JSON: %v", err)
	}
}

func TestNodeSchemaEndpointReturnsJSON(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	resp := doRequest(t, http.MethodGet, srv.URL+"/node-schema", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("node-schema not JSON: %v", err)
	}
	if _, ok := doc["http_trigger"]; !ok {
		t.Fatal("node-schema missing http_trigger")
	}
}

func TestValidateEndpointReturnsIssues(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	body := strings.NewReader("server:\n  max_body_bytes: 1048576\n") // missing addr
	resp := doRequest(t, http.MethodPost, srv.URL+"/validate", "application/yaml", body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("response not JSON envelope: %v", err)
	}
	if len(out.Issues) == 0 {
		t.Fatal("expected issues for config missing server.addr")
	}
}

func TestValidateEndpointRejectsNonPOST(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	resp := doRequest(t, http.MethodGet, srv.URL+"/validate", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /validate, got %d", resp.StatusCode)
	}
}

func TestOptionsPreflightReturns204(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	resp := doRequest(t, http.MethodOptions, srv.URL+"/validate", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS *, got %q", got)
	}
}

func TestValidateRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	body := strings.NewReader(strings.Repeat("a", 6<<20))
	resp := doRequest(t, http.MethodPost, srv.URL+"/validate", "application/yaml", body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", resp.StatusCode)
	}
}

func TestAPIKeyRequiredWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(Handler("s3cret"))
	defer srv.Close()

	resp := doRequest(t, http.MethodGet, srv.URL+"/schema", "", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/schema without key = %d, want 401", resp.StatusCode)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/schema", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/schema with key = %d, want 200", resp.StatusCode)
	}
}

func TestOptionsPreflightSkipsAuthWhenKeySet(t *testing.T) {
	srv := httptest.NewServer(Handler("s3cret"))
	defer srv.Close()

	resp := doRequest(t, http.MethodOptions, srv.URL+"/validate", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS with key set = %d, want 204 (preflight must skip auth)", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header on preflight")
	}
	// The browser UI sends the key as Authorization; the preflight must allow it,
	// otherwise the cross-origin authenticated request is blocked by the browser.
	if allow := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(allow, "Authorization") {
		t.Fatalf("preflight Access-Control-Allow-Headers = %q, must include Authorization", allow)
	}
}
