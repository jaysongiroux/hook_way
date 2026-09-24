package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

const (
	captureToken = "capture-token-0123456789abcdefghijkl"
	opsToken     = "ops-token-0123456789abcdefghijklmnopq"
	readToken    = "read-token-0123456789abcdefghijklmnop"
	testID       = "01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8e"
	testRunID    = "01926d3c-9999-7b3a-9c1e-2f4a5b6c7d8e"
)

// fakeStore records calls; an unset function panics, which the handler's
// panic recovery turns into a 500 the test will see.
type fakeStore struct {
	ingest       func(store.NewFailure) (store.IngestResult, error)
	list         func(store.Filter) (store.Page, error)
	get          func(string) (store.Detail, error)
	payload      func(id, actor string) (store.Payload, error)
	requestRetry func(store.RetryRequest) (store.RetryResult, error)
	getRun       func(string) (store.Run, error)
	dismiss      func(id, actor string) (store.Failure, error)
	ready        func() error
}

func (f *fakeStore) Ingest(_ context.Context, nf store.NewFailure) (store.IngestResult, error) {
	return f.ingest(nf)
}
func (f *fakeStore) List(_ context.Context, filter store.Filter) (store.Page, error) {
	return f.list(filter)
}
func (f *fakeStore) Get(_ context.Context, id string) (store.Detail, error) { return f.get(id) }
func (f *fakeStore) Payload(_ context.Context, id, actor string) (store.Payload, error) {
	return f.payload(id, actor)
}
func (f *fakeStore) RequestRetry(_ context.Context, r store.RetryRequest) (store.RetryResult, error) {
	return f.requestRetry(r)
}
func (f *fakeStore) GetRun(_ context.Context, id string) (store.Run, error) { return f.getRun(id) }
func (f *fakeStore) Dismiss(_ context.Context, id, actor string) (store.Failure, error) {
	return f.dismiss(id, actor)
}
func (f *fakeStore) Ready(context.Context) error { return f.ready() }

type harness struct {
	handler http.Handler
	metrics *metrics.Metrics
}

func newHarness(t *testing.T, fs *fakeStore, extra ...func(*Options)) *harness {
	t.Helper()
	m := metrics.New()
	a := auth.New([]auth.Key{
		{Name: "core-capture", Token: captureToken, Scopes: []auth.Scope{auth.ScopeCapture}},
		{Name: "ops", Token: opsToken, Scopes: []auth.Scope{auth.ScopeRead, auth.ScopePayload, auth.ScopeRetry}},
		{Name: "reader", Token: readToken, Scopes: []auth.Scope{auth.ScopeRead}},
	}, m.AuthFailure)
	opts := Options{Store: fs, Auth: a, Metrics: m, Logger: slog.New(slog.DiscardHandler), MaxRecordBytes: 1024}
	for _, fn := range extra {
		fn(&opts)
	}
	return &harness{handler: New(opts), metrics: m}
}

func (h *harness) do(t *testing.T, method, target, token string, body io.Reader, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, body)
	for k, v := range header {
		req.Header[k] = v
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, body %q", ct, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Code
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func notFoundStore() *fakeStore {
	return &fakeStore{
		ingest:       func(store.NewFailure) (store.IngestResult, error) { return 0, store.ErrNotFound },
		list:         func(store.Filter) (store.Page, error) { return store.Page{}, nil },
		get:          func(string) (store.Detail, error) { return store.Detail{}, store.ErrNotFound },
		payload:      func(string, string) (store.Payload, error) { return store.Payload{}, store.ErrNotFound },
		requestRetry: func(store.RetryRequest) (store.RetryResult, error) { return store.RetryResult{}, store.ErrNotFound },
		getRun:       func(string) (store.Run, error) { return store.Run{}, store.ErrNotFound },
		dismiss:      func(string, string) (store.Failure, error) { return store.Failure{}, store.ErrNotFound },
		ready:        func() error { return nil },
	}
}

func TestRoutesRequireTheirScope(t *testing.T) {
	routes := []struct {
		method, target string
		scope          auth.Scope
	}{
		{"POST", "/v1/failures", auth.ScopeCapture},
		{"GET", "/v1/failures", auth.ScopeRead},
		{"GET", "/v1/failures/" + testID, auth.ScopeRead},
		{"GET", "/v1/failures/" + testID + "/payload", auth.ScopePayload},
		{"POST", "/v1/failures/" + testID + "/retries", auth.ScopeRetry},
		{"POST", "/v1/failures/" + testID + "/dismiss", auth.ScopeRetry},
		{"POST", "/v1/retries", auth.ScopeRetry},
		{"GET", "/v1/retries/" + testRunID, auth.ScopeRead},
	}
	tokens := map[string][]auth.Scope{
		captureToken: {auth.ScopeCapture},
		readToken:    {auth.ScopeRead},
		opsToken:     {auth.ScopeRead, auth.ScopePayload, auth.ScopeRetry},
	}
	h := newHarness(t, notFoundStore())
	for _, r := range routes {
		if rec := h.do(t, r.method, r.target, "", nil, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401", r.method, r.target, rec.Code)
		}
		for token, scopes := range tokens {
			allowed := false
			for _, s := range scopes {
				allowed = allowed || s == r.scope
			}
			rec := h.do(t, r.method, r.target, token, strings.NewReader(""), nil)
			if forbidden := rec.Code == http.StatusForbidden; forbidden == allowed {
				t.Errorf("%s %s with scopes %v = %d; want forbidden=%v", r.method, r.target, scopes, rec.Code, !allowed)
			}
		}
	}
}

func TestHealthzAndReadyz(t *testing.T) {
	fs := notFoundStore()
	h := newHarness(t, fs)
	if rec := h.do(t, "GET", "/healthz", "", nil, nil); rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
	if rec := h.do(t, "GET", "/readyz", "", nil, nil); rec.Code != 200 {
		t.Fatalf("readyz = %d", rec.Code)
	}
	fs.ready = func() error { return errors.New("store: database schema version 0 is behind 1") }
	rec := h.do(t, "GET", "/readyz", "", nil, nil)
	if rec.Code != http.StatusServiceUnavailable || problemCode(t, rec) != "not_ready" || strings.Contains(rec.Body.String(), "schema version 0") {
		t.Fatalf("readyz = %d %q, want 503 not_ready without internal detail", rec.Code, rec.Body.String())
	}
}

func TestInstrumentBoundsRouteAndMethodLabels(t *testing.T) {
	h := newHarness(t, notFoundStore())
	h.do(t, "GET", "/v1/failures/"+testID, readToken, nil, nil)
	h.do(t, "GET", "/no/such/path/"+testID, "", nil, nil)
	h.do(t, "BREW", "/v1/failures", readToken, nil, nil)
	const want = `
# HELP hookway_replay_http_requests_total HTTP requests served, by route pattern, method, and status.
# TYPE hookway_replay_http_requests_total counter
hookway_replay_http_requests_total{method="GET",route="/v1/failures/{id}",status="404"} 1
hookway_replay_http_requests_total{method="GET",route="unmatched",status="404"} 1
hookway_replay_http_requests_total{method="other",route="unmatched",status="405"} 1
`
	if err := testutil.GatherAndCompare(h.metrics.Registry(), strings.NewReader(want), "hookway_replay_http_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestPanicsBecomeAProblem500(t *testing.T) {
	fs := notFoundStore()
	fs.get = func(string) (store.Detail, error) { panic("boom") }
	h := newHarness(t, fs)
	rec := h.do(t, "GET", "/v1/failures/"+testID, readToken, nil, nil)
	if rec.Code != 500 || problemCode(t, rec) != "internal" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	const want = `
# HELP hookway_replay_panics_total Recovered panics, by where they were recovered.
# TYPE hookway_replay_panics_total counter
hookway_replay_panics_total{location="handler"} 1
`
	if err := testutil.GatherAndCompare(h.metrics.Registry(), strings.NewReader(want), "hookway_replay_panics_total"); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedStoreErrorsWithholdDetail(t *testing.T) {
	fs := notFoundStore()
	fs.get = func(string) (store.Detail, error) {
		return store.Detail{}, errors.New("dial tcp 10.0.0.5:5432: connection refused")
	}
	h := newHarness(t, fs)
	rec := h.do(t, "GET", "/v1/failures/"+testID, readToken, nil, nil)
	if rec.Code != 500 || problemCode(t, rec) != "internal" || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestMetricsCanShareTheAPIListener(t *testing.T) {
	h := newHarness(t, notFoundStore(), func(o *Options) {
		o.MetricsPath = "/metrics"
		o.MetricsHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("metrics")) })
	})
	if rec := h.do(t, "GET", "/metrics", "", nil, nil); rec.Code != 200 || rec.Body.String() != "metrics" {
		t.Fatalf("metrics = %d %q", rec.Code, rec.Body.String())
	}
}
