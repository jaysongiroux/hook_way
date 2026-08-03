package editor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The SPA index is served at / without an apikey (you need the HTML before you
// can supply a key).
func TestServesSPAIndexUnauthenticated(t *testing.T) {
	srv := httptest.NewServer(Handler("secret-key"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / content-type = %q, want text/html…", ct)
	}
}

// Unknown client-side paths fall back to index.html (SPA routing), still 200.
func TestUnknownPathFallsBackToIndex(t *testing.T) {
	srv := httptest.NewServer(Handler(""))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/some/client/route")
	if err != nil {
		t.Fatalf("GET unknown: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown path status = %d, want 200 (index fallback)", resp.StatusCode)
	}
}

// The API stays guarded: /schema without the apikey is rejected, proving UI
// serving did not remove auth from the API routes.
func TestSchemaStillGuarded(t *testing.T) {
	srv := httptest.NewServer(Handler("secret-key"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/schema")
	if err != nil {
		t.Fatalf("GET /schema: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("GET /schema without key returned 200; want guarded (401)")
	}
}
