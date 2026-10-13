package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

func admissionGauge(t *testing.T, m *metrics.Metrics, name, mode string) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, series := range family.Metric {
			if mode == "" {
				return series.GetGauge().GetValue()
			}
			for _, label := range series.Label {
				if label.GetName() == "mode" && label.GetValue() == mode {
					return series.GetGauge().GetValue()
				}
			}
		}
	}
	t.Fatalf("missing gauge %s mode %q", name, mode)
	return 0
}

func TestAdmissionMetricsExcludeRejectionsAndAuxiliaryHandlers(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Server.MaxInflightRequests = 1
	m := metrics.New()
	pub := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	done := make(chan struct{})
	go func() { defer close(done); srv.ServeHTTP(httptest.NewRecorder(), githubPOST([]byte(`{"ok":true}`))) }()
	<-pub.entered
	if got := admissionGauge(t, m, "hookway_admitted_requests", "bounded"); got != 1 {
		t.Fatal(got)
	}
	if got := admissionGauge(t, m, "hookway_admission_capacity", ""); got != 1 {
		t.Fatal(got)
	}
	r := httptest.NewRecorder()
	srv.ServeHTTP(r, githubPOST([]byte(`{"ok":true}`)))
	if r.Code != 503 {
		t.Fatal(r.Code)
	}
	for _, path := range []string{"/healthz", "/metrics", "/unknown"} {
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
		if got := admissionGauge(t, m, "hookway_admitted_requests", "bounded"); got != 1 {
			t.Fatalf("%s changed occupancy to %v", path, got)
		}
	}
	if got := admissionGauge(t, m, "hookway_inflight_requests", ""); got != 1 {
		t.Fatal(got)
	}
	close(pub.release)
	<-done
	if got := admissionGauge(t, m, "hookway_admitted_requests", "bounded"); got != 0 {
		t.Fatal(got)
	}
	if got := admissionGauge(t, m, "hookway_inflight_requests", ""); got != 0 {
		t.Fatal(got)
	}
}

type cancelAdmissionPublisher struct{ entered chan struct{} }

func (p cancelAdmissionPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	close(p.entered)
	<-ctx.Done()
	return ctx.Err()
}
func (p cancelAdmissionPublisher) Ready(context.Context) error { return nil }
func (p cancelAdmissionPublisher) Close() error                { return nil }

func TestAdmissionReleasedOnCancellationAuthFailureAndPanic(t *testing.T) {
	for _, kind := range []string{"cancel", "auth", "panic"} {
		t.Run(kind, func(t *testing.T) {
			cfg := loadFlowCfg(t, testFlowYAML)
			cfg.Server.MaxInflightRequests = 0
			cfg.Server.PublishTimeout = 150 * time.Millisecond
			m := metrics.New()
			pub := cancelAdmissionPublisher{entered: make(chan struct{})}
			srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), m)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Close() })
			r := githubPOST([]byte(`{"ok":true}`))
			if kind == "auth" {
				r.Header.Set("X-Hub-Signature-256", "invalid")
			}
			if kind == "panic" {
				r.Body = io.NopCloser(panicReadCloser{})
			}
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			done := make(chan struct{})
			go func() { defer close(done); srv.ServeHTTP(httptest.NewRecorder(), r) }()
			if kind == "cancel" {
				<-pub.entered
				if got := admissionGauge(t, m, "hookway_admitted_requests", "unlimited"); got != 1 {
					t.Fatal(got)
				}
				cancel()
				// Publishing deliberately outlives client cancellation until its own budget expires.
				if got := admissionGauge(t, m, "hookway_admitted_requests", "unlimited"); got != 1 {
					t.Fatal("cancelled client released admission before publication settled", got)
				}
			}
			<-done
			if got := admissionGauge(t, m, "hookway_admitted_requests", "unlimited"); got != 0 {
				t.Fatal(got)
			}
			if got := admissionGauge(t, m, "hookway_admission_capacity", ""); got != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestAdmissionCapacityMatchesOverlappingServersAndGracefulShutdown(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Server.MaxInflightRequests = 1
	m := metrics.New()
	pub := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	old, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	cfg.Server.MaxInflightRequests = 3
	next, err := NewFlowServer(cfg, &fakePublisher{}, slog.New(slog.DiscardHandler), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	httpServer := httptest.NewServer(old)
	t.Cleanup(httpServer.Close)
	done := make(chan error, 1)
	go func() {
		req := githubPOST([]byte(`{"ok":true}`))
		req.URL.Scheme = "http"
		req.URL.Host = httpServer.Listener.Addr().String()
		req.RequestURI = ""
		resp, e := http.DefaultClient.Do(req)
		if e == nil {
			_, e = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Errorf("drained request status %d", resp.StatusCode)
			}
		}
		done <- e
	}()
	<-pub.entered
	if got := admissionGauge(t, m, "hookway_admission_capacity", ""); got != 4 {
		t.Fatal(got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	shutdown := make(chan error, 1)
	go func() { shutdown <- httpServer.Config.Shutdown(ctx) }()
	select {
	case e := <-shutdown:
		t.Fatalf("shutdown returned before request drained: %v", e)
	case <-time.After(25 * time.Millisecond):
	}
	close(pub.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := <-shutdown; e != nil {
		t.Fatal(e)
	}
	_ = old.Close()
	_ = old.Close()
	if got := admissionGauge(t, m, "hookway_admission_capacity", ""); got != 3 {
		t.Fatal(got)
	}
	if got := admissionGauge(t, m, "hookway_admitted_requests", "bounded"); got != 0 {
		t.Fatal(got)
	}
	r := httptest.NewRecorder()
	old.ServeHTTP(r, githubPOST([]byte(`{"ok":true}`)))
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed server admitted request: %d", r.Code)
	}
}
