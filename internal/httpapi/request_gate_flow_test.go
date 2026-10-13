package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

type gatedPublisher struct {
	entered, release chan struct{}
	calls            atomic.Int64
}

func (p *gatedPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (p *gatedPublisher) Ready(context.Context) error { return nil }
func (p *gatedPublisher) Close() error                { return nil }

type watchedBody struct {
	io.ReadCloser
	reads atomic.Int64
}

func (b *watchedBody) Read(p []byte) (int, error) { b.reads.Add(1); return b.ReadCloser.Read(p) }

func TestWaitingFlowReadsBodyOnlyAfterAdmissionAndExposesMetrics(t *testing.T) {
	for _, kind := range []string{"admitted", "closing"} {
		t.Run(kind, func(t *testing.T) {
			cfg := loadFlowCfg(t, testFlowYAML)
			cfg.Server.MaxInflightRequests = 1
			cfg.Server.MaxPendingRequests = 1
			cfg.Server.AdmissionWaitTimeout = time.Second
			m := metrics.New()
			pub := &gatedPublisher{entered: make(chan struct{}), release: make(chan struct{})}
			srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), m)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			first := make(chan struct{})
			go func() { defer close(first); srv.ServeHTTP(httptest.NewRecorder(), githubPOST([]byte(`{"ok":true}`))) }()
			<-pub.entered
			request := githubPOST([]byte(`{"ok":true}`))
			body := &watchedBody{ReadCloser: request.Body}
			request.Body = body
			second := make(chan struct{})
			response := httptest.NewRecorder()
			go func() { defer close(second); srv.ServeHTTP(response, request) }()
			deadline := time.Now().Add(time.Second)
			for admissionGauge(t, m, "hookway_admission_waiting_requests", "") != 1 {
				if time.Now().After(deadline) {
					t.Fatal("request never queued")
				}
				time.Sleep(time.Millisecond)
			}
			if body.reads.Load() != 0 || admissionGauge(t, m, "hookway_admitted_requests", "bounded") != 1 {
				t.Fatal("waiting request read its body or began execution")
			}
			rejected := httptest.NewRecorder()
			srv.ServeHTTP(rejected, githubPOST([]byte(`{"ok":true}`)))
			if rejected.Code != 503 {
				t.Fatalf("full queue response: %d", rejected.Code)
			}
			if kind == "closing" {
				if err := srv.Close(); err != nil {
					t.Fatal(err)
				}
			}
			close(pub.release)
			<-first
			select {
			case <-second:
			case <-time.After(2 * time.Second):
				t.Fatal("waiter did not settle")
			}
			if kind == "admitted" {
				if response.Code != 202 || pub.calls.Load() != 2 || body.reads.Load() == 0 {
					t.Fatalf("queued flow response=%d publishes=%d", response.Code, pub.calls.Load())
				}
			} else if response.Code != 503 || pub.calls.Load() != 1 || body.reads.Load() != 0 {
				t.Fatal("closing server started queued flow")
			}
			if admissionGauge(t, m, "hookway_admission_waiting_requests", "") != 0 || admissionGauge(t, m, "hookway_admitted_requests", "bounded") != 0 {
				t.Fatal("occupancy gauges leaked")
			}
			families, err := m.Registry().Gather()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range families {
				if f.GetName() == "hookway_admission_wait_total" {
					for _, s := range f.Metric {
						for _, l := range s.Label {
							if l.GetName() == "result" && l.GetValue() == kind && s.GetCounter().GetValue() == 1 {
								found = true
							}
						}
					}
				}
			}
			if !found {
				t.Fatal("queued outcome metric missing")
			}
		})
	}
}
