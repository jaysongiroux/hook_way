package editor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaEndpointReturnsJSON(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/schema")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
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

func TestValidateEndpointReturnsIssues(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	body := strings.NewReader("server:\n  max_body_bytes: 1048576\n") // missing addr
	resp, err := http.Post(srv.URL+"/validate", "application/yaml", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
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
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/validate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /validate, got %d", resp.StatusCode)
	}
}

func TestOptionsPreflightReturns204(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/validate", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS *, got %q", got)
	}
}

func TestValidateRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	body := strings.NewReader(strings.Repeat("a", 6<<20))
	resp, err := http.Post(srv.URL+"/validate", "application/yaml", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", resp.StatusCode)
	}
}
