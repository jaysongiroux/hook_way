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

func TestReplaySchemaAndValidateEndpoints(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	resp := doRequest(t, http.MethodGet, srv.URL+"/replay-schema", "", nil)
	var schema map[string]any
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&schema) != nil {
		_ = resp.Body.Close()
		t.Fatalf("replay schema response = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	body := strings.NewReader("server: {unknown: true}\n")
	resp = doRequest(t, http.MethodPost, srv.URL+"/validate-replay", "application/yaml", body)
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Issues []map[string]any `json:"issues"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Issues) == 0 {
		t.Fatalf("replay validate response = %d, issues=%v", resp.StatusCode, out.Issues)
	}
}

func TestReplayEditorEndpointsUseExistingAuthAndMethods(t *testing.T) {
	srv := httptest.NewServer(Handler("s3cret"))
	defer srv.Close()

	resp := doRequest(t, http.MethodGet, srv.URL+"/replay-schema", "", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /replay-schema without key = %d, want 401", resp.StatusCode)
	}
	resp = doRequest(t, http.MethodPost, srv.URL+"/validate-replay", "application/yaml", strings.NewReader("{}"))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /validate-replay without key = %d, want 401", resp.StatusCode)
	}

	open := httptest.NewServer(Handler(""))
	defer open.Close()
	resp = doRequest(t, http.MethodGet, open.URL+"/validate-replay", "", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /validate-replay = %d, want 405", resp.StatusCode)
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

	for _, path := range []string{"/validate", "/validate-replay"} {
		t.Run(path, func(t *testing.T) {
			body := strings.NewReader(strings.Repeat("a", 6<<20))
			resp := doRequest(t, http.MethodPost, srv.URL+path, "application/yaml", body)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400 for oversized body, got %d", resp.StatusCode)
			}
		})
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

func TestEveryProtectedEditorRouteRejectsDuplicateAuthorization(t *testing.T) {
	const key = "editor-boundary-key"
	srv := httptest.NewServer(Handler(key))
	t.Cleanup(srv.Close)
	for _, route := range Routes() {
		for _, headers := range [][]string{{"Bearer " + key, "Bearer wrong"}, {"Bearer " + key, "Bearer " + key}} {
			req, err := http.NewRequestWithContext(t.Context(), route.Method, srv.URL+route.Path, strings.NewReader("not valid input"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header["Authorization"] = headers
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), key) {
				t.Fatalf("%s %s duplicate auth: %d %q, err %v", route.Method, route.Path, resp.StatusCode, body, err)
			}
		}
	}
}
