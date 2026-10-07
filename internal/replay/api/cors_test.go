package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

const consoleOrigin = "http://127.0.0.1:5183"

func TestOperatorCORSPreflightsAreAnonymousAndRouteAware(t *testing.T) {
	h := newHarness(t, notFoundStore(), func(o *Options) { o.AllowedOrigins = []string{consoleOrigin} })
	for _, c := range []struct {
		path, method, headers string
		want                  int
	}{
		{"/v1/me", "GET", "Authorization", 204},
		{"/v1/records", "GET", "Authorization, Content-Type", 204},
		{"/v1/records/" + testID + "/retries", "POST", "authorization, content-type, idempotency-key", 204},
		{"/v1/actions/validate", "POST", "Authorization, Content-Type", 204},
		{"/v1/records", "POST", "Authorization, Content-Type", 403},
		{"/v1/me", "POST", "Authorization", 403},
		{"/v1/me", "DELETE", "Authorization", 403},
		{"/v1/me", "GET", "Authorization, Cookie", 403},
		{"/v1/does-not-exist", "GET", "Authorization", 404},
		{"/healthz", "GET", "Authorization", 405},
	} {
		rec := h.do(t, "OPTIONS", c.path, "", nil, http.Header{"Origin": {consoleOrigin}, "Access-Control-Request-Method": {c.method}, "Access-Control-Request-Headers": {c.headers}})
		if rec.Code != c.want {
			t.Fatalf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" || rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("cookie permissions leaked")
		}
		if c.want == 204 && (rec.Header().Get("Access-Control-Allow-Origin") != consoleOrigin || rec.Header().Get("Access-Control-Allow-Methods") != c.method) {
			t.Fatal(rec.Header())
		}
		if c.want != 204 && rec.Header().Get("Access-Control-Allow-Methods") != "" {
			t.Fatal("failed preflight grants methods")
		}
		if c.path == "/v1/records" && c.method == "POST" && rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("capture preflight grants browser origin")
		}
	}
}

func TestOperatorCORSRejectsForeignOriginsBeforeStoreOrAuthentication(t *testing.T) {
	h := newHarness(t, &fakeStore{}, func(o *Options) { o.AllowedOrigins = []string{consoleOrigin, "*", "null"} })
	for _, origin := range []string{"", "null", "*", "http://127.0.0.1:5184", consoleOrigin + "/", "https://evil.test", consoleOrigin + ", https://evil.test"} {
		for _, token := range []string{"", opsToken} {
			rec := h.do(t, "GET", "/v1/records", token, nil, http.Header{"Origin": {origin}})
			if rec.Code != 403 || problemCode(t, rec) != "origin_forbidden" || rec.Header().Get("Access-Control-Allow-Origin") != "" || !strings.Contains(rec.Header().Get("Vary"), "Origin") {
				t.Fatalf("%q: %d %v", origin, rec.Code, rec.Header())
			}
		}
	}
	rec := h.do(t, "GET", "/v1/me", opsToken, nil, http.Header{"Origin": {consoleOrigin, consoleOrigin}})
	if rec.Code != 403 {
		t.Fatal("duplicate Origin accepted")
	}
}

func TestOperatorCORSIncludesErrorsAndDownloadHeaders(t *testing.T) {
	fs := notFoundStore()
	fs.list = func(store.Filter) (store.Page, error) { return store.Page{}, errors.New("unavailable") }
	h := newHarness(t, fs, func(o *Options) { o.AllowedOrigins = []string{consoleOrigin} })
	for _, c := range []struct {
		path, token string
		status      int
	}{
		{"/v1/me", opsToken, 200}, {"/v1/me", "", 401},
		{"/v1/records/" + testID + "/payload", readToken, 403},
		{"/v1/records/" + testID, opsToken, 404},
		{"/v1/records?unexpected=true", opsToken, 400},
		{"/v1/records", opsToken, 500},
	} {
		rec := h.do(t, "GET", c.path, c.token, nil, http.Header{"Origin": {consoleOrigin}})
		if rec.Code != c.status || rec.Header().Get("Access-Control-Allow-Origin") != consoleOrigin || !strings.Contains(rec.Header().Get("Vary"), "Origin") {
			t.Fatalf("%s: %d %v", c.path, rec.Code, rec.Header())
		}
		if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Content-Disposition, X-Hookway-Payload-Content-Type, Retry-After" {
			t.Fatal(got)
		}
	}
	for i := 0; i < 12; i++ {
		rec := h.do(t, "GET", "/v1/me", "", nil, http.Header{"Origin": {consoleOrigin}})
		if rec.Code == 429 {
			assertContractBody(t, contractDocument(t), "Problem", rec.Body.Bytes())
		}
		if i == 11 && (rec.Code != 429 || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Access-Control-Allow-Origin") != consoleOrigin) {
			t.Fatalf("throttle CORS: %d %v", rec.Code, rec.Header())
		}
	}
}

func TestCaptureHealthAndMetricsHaveNoCORSPermissions(t *testing.T) {
	h := newHarness(t, notFoundStore(), func(o *Options) {
		o.AllowedOrigins = []string{consoleOrigin}
		o.MetricsPath = "/metrics"
		o.MetricsHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("anonymous metrics invoked") })
	})
	for _, c := range []struct {
		method, path string
		want         int
	}{{"POST", "/v1/records", 401}, {"GET", "/healthz", 200}, {"GET", "/readyz", 200}, {"GET", "/metrics", 401}} {
		rec := h.do(t, c.method, c.path, "", nil, http.Header{"Origin": {"https://evil.test"}})
		if rec.Code != c.want || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Expose-Headers") != "" {
			t.Fatalf("%s: %d %v", c.path, rec.Code, rec.Header())
		}
	}
	// CLI requests keep working with an empty allowlist and no Origin.
	noBrowser := newHarness(t, notFoundStore())
	if rec := noBrowser.do(t, "GET", "/v1/me", opsToken, nil, nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := noBrowser.do(t, "GET", "/v1/me", opsToken, nil, http.Header{"Origin": {consoleOrigin}}); rec.Code != 403 {
		t.Fatal(rec.Code)
	}
}
