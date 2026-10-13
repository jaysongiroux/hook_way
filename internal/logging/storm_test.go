package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStormBoundAndSummariesShareStateAcrossClones(t *testing.T) {
	var buf bytes.Buffer
	var dropped atomic.Uint64
	h := StormHandler(slog.NewJSONHandler(&buf, nil), func() { dropped.Add(1) }).(*stormHandler)
	now := time.Unix(1700000000, 0)
	h.state.now = func() time.Time { return now }
	logger := slog.New(h)
	for i := range 10000 {
		logger.With("request_id", i).WithGroup("diagnostic").Error("node failed", "error", "backend unavailable")
	}
	if got := dropped.Load(); got != 9990 {
		t.Fatalf("dropped=%d, want9990", got)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 11 {
		t.Fatalf("storm emitted=%d, want10diagnostics+1summary", len(lines))
	}
	var first, summary map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[10]), &summary); err != nil {
		t.Fatal(err)
	}
	if first["request_id"] != float64(0) || summary["category"] != "node failed" || summary["request_id"] != nil || summary["diagnostic"] != nil {
		t.Fatal("first correlation lost or suppression inherited request attributes")
	}
	now = now.Add(time.Second)
	logger.Error("node failed", "request_id", "recovered-window")
	if !strings.Contains(buf.String(), `"suppressed_since_last_summary":9989`) || !strings.Contains(buf.String(), "recovered-window") {
		t.Fatal("next window must summarize its accumulated tail and retain next diagnostic")
	}
	logger.Info("hookway stopped")
	logger.Error("record dismissed")
	if !strings.Contains(buf.String(), "hookway stopped") || !strings.Contains(buf.String(), "record dismissed") {
		t.Fatal("lifecycle and operator audit messages must bypass sampling")
	}
	// Every fixed category gets an independent bounded budget, not a map keyed
	// by an untrusted value. Unknown messages never create limiter state.
	for _, message := range stormMessages {
		logger.Warn(message)
	}
}

func TestStormConcurrentCountAndDebugOptIn(t *testing.T) {
	var dropped atomic.Uint64
	h := StormHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil), func() { dropped.Add(1) }).(*stormHandler)
	h.state.now = func() time.Time { return time.Unix(1700000000, 0) }
	logger := slog.New(h)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 1000 {
				logger.Error("node failed")
			}
		})
	}
	wg.Wait()
	if dropped.Load() != 9990 {
		t.Fatalf("concurrent dropped=%d", dropped.Load())
	}
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	var buf bytes.Buffer
	debugLogger := slog.New(StormHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}), func() { t.Error("debug should bypass sampling") }))
	for range 50 {
		debugLogger.ErrorContext(context.Background(), "node failed")
	}
	if strings.Count(buf.String(), "node failed") != 50 {
		t.Fatal("explicit debug troubleshooting must retain all diagnostics")
	}
}
