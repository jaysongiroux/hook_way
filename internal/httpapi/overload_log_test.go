package httpapi

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/metrics"
)

type stalledLogWriter struct {
	entered, release chan struct{}
	once             sync.Once
	writes           atomic.Int64
}

func (w *stalledLogWriter) Write(p []byte) (int, error) {
	w.writes.Add(1)
	w.once.Do(func() { close(w.entered); <-w.release })
	return len(p), nil
}

func TestRejectedRequestsDoNotWaitForSharedLogWriter(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Server.MaxInflightRequests = 1
	m := metrics.New()
	writer := &stalledLogWriter{entered: make(chan struct{}), release: make(chan struct{})}
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv, err := NewFlowServer(cfg, &fakePublisher{}, logger, m)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	if srv.gate.acquire(context.Background(), nil) != gateAdmitted {
		t.Fatal("cannot occupy admission")
	}
	defer srv.gate.release()
	first := make(chan struct{})
	go func() {
		defer close(first)
		srv.ServeHTTP(httptest.NewRecorder(), githubPOST([]byte(`{"ok":true}`)))
	}()
	select {
	case <-writer.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first refusal did not reach logger")
	}
	// Release the stalled writer even when an assertion fails.
	defer func() { close(writer.release); <-first }()
	const requests = 200
	completed := make(chan int, requests)
	for range requests {
		go func() {
			r := httptest.NewRecorder()
			srv.ServeHTTP(r, githubPOST([]byte(`{"ok":true}`)))
			completed <- r.Code
		}()
	}
	deadline := time.After(2 * time.Second)
	for range requests {
		select {
		case status := <-completed:
			if status != 503 {
				t.Fatalf("status=%d", status)
			}
		case <-deadline:
			t.Fatal("refused handlers serialized behind the blocked log writer")
		}
	}
	if writer.writes.Load() != 1 {
		t.Fatal("more than one refusal attempted logging")
	}
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hookway_saturated_total", "hookway_http_requests_total"} {
		var count float64
		for _, family := range families {
			if family.GetName() == name {
				for _, sample := range family.Metric {
					count += sample.GetCounter().GetValue()
				}
			}
		}
		if count != requests+1 {
			t.Fatalf("%s=%v; want exact count %d", name, count, requests+1)
		}
	}
}

func TestOverloadLogAggregatesAndResumes(t *testing.T) {
	writer := &stalledLogWriter{entered: make(chan struct{}), release: make(chan struct{})}
	close(writer.release)
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	var log overloadLog
	log.record(logger, "sample")
	for range 99 {
		log.record(logger, "sample")
	}
	if writer.writes.Load() != 1 || log.pending.Load() != 99 {
		t.Fatal("refusals were not aggregated")
	}
	// No concurrent users here: advance the next reporting boundary explicitly.
	log.next = time.Now().Add(-time.Second)
	log.record(logger, "sample")
	if writer.writes.Load() != 2 || log.pending.Load() != 0 {
		t.Fatal("next summary did not consume the accumulated count")
	}
}
