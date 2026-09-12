package editor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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

// builtFS mimics a bundle staged by `make ui`.
func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(`<!doctype html><script type="module" src="/assets/app-abc123.js"></script>`)},
		"assets/app-abc123.js": {Data: []byte("export const x = 1")},
	}
}

// get issues a request against h and returns status, content-type and body.
func get(t *testing.T, h http.Handler, path string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

// A binary built without `make ui` has no index.html in the embedded FS. Every
// request must explain that, rather than serve a page whose module script 404s
// (which renders blank) or a bare 404.
func TestUnstagedBundleServesPlaceholder(t *testing.T) {
	h := newUIHandler(fstest.MapFS{})
	for _, path := range []string{"/", "/some/client/route", "/assets/app-abc123.js"} {
		status, ct, body := get(t, h, path)
		if status != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, status)
		}
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s content-type = %q, want text/html…", path, ct)
		}
		if !strings.Contains(body, "not built") {
			t.Errorf("GET %s body does not explain the missing bundle: %q", path, body)
		}
	}
}

// The placeholder is tracked source, so it is always embedded and non-empty —
// the whole point of moving it out of the generated webui/ directory.
func TestPlaceholderIsEmbedded(t *testing.T) {
	if len(placeholderHTML) == 0 {
		t.Fatal("placeholderHTML is empty; placeholder.html must be tracked and embedded")
	}
	if !strings.Contains(string(placeholderHTML), "make ui") {
		t.Error("placeholder should tell the reader to run make ui")
	}
}

// With a bundle staged, real files are served and client-side routes fall back
// to index.html.
func TestStagedBundleServesFilesAndSPARoutes(t *testing.T) {
	h := newUIHandler(builtFS())

	status, _, body := get(t, h, "/assets/app-abc123.js")
	if status != http.StatusOK || !strings.Contains(body, "export const x") {
		t.Errorf("asset: status = %d, body = %q", status, body)
	}
	status, ct, body := get(t, h, "/some/client/route")
	if status != http.StatusOK || !strings.HasPrefix(ct, "text/html") {
		t.Errorf("client route: status = %d, ct = %q", status, ct)
	}
	if strings.Contains(body, "not built") {
		t.Error("client route served the placeholder despite a staged bundle")
	}
}

// A missing build artifact must 404 rather than fall back to index.html:
// answering a module-script request with HTML gives a blank page and a console
// MIME error instead of a legible failure.
func TestMissingAssetDoesNotFallBackToIndex(t *testing.T) {
	h := newUIHandler(builtFS())
	status, _, body := get(t, h, "/assets/app-stale999.js")
	if status != http.StatusNotFound {
		t.Errorf("stale asset status = %d, want 404", status)
	}
	if strings.Contains(body, "<!doctype html>") {
		t.Error("stale asset served index.html; browsers render that as a blank page")
	}
}
