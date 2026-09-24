package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// benchPublisher accepts "primary" and fails "broken", like a partial outage.
type benchPublisher struct{}

func (benchPublisher) Publish(_ context.Context, dest, _ string, _ queue.Message) error {
	if dest == "broken" {
		return errors.New("broken is down")
	}
	return nil
}
func (benchPublisher) Ready(context.Context) error { return nil }
func (benchPublisher) Close() error                { return nil }

const benchFlowYAML = `
server: {addr: ":8080", max_body_bytes: 33554432}
replay:
  url: "REPLAY_URL"
  capture_token: "capture-token-0123456789abcdefghijkl"
  control: {addr: ":9092", token: "control-token-0123456789abcdefghij"}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.com/a"}}
  broken: {type: webhook, webhook: {url: "https://example.com/b"}}
flows:
  orders:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 33554432}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: raw}
      - {id: ok, type: publish, destination: primary, target: a}
      - {id: bad, type: publish, destination: broken, target: b}
    edges: [in>body, body>auth, auth>env, env>ok, env>bad]
`

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%dMiB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%dKiB", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}

// BenchmarkCaptureOverhead measures the request path of a skip_failed flow
// with one destination down, with and without capture. Capture's work runs
// after the response, so the difference is what the request pays.
func BenchmarkCaptureOverhead(b *testing.B) {
	// The fake replay counts full and metadata-only records, so a row can
	// never silently time a failure capture dropped.
	var full, metadataOnly atomic.Int64
	replay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err == nil {
			parts := 0
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				_, _ = io.Copy(io.Discard, p)
				parts++
			}
			if parts > 1 {
				full.Add(1)
			} else {
				metadataOnly.Add(1)
			}
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer replay.Close()
	cfg, _ := flowconfig.LoadBytes([]byte(strings.ReplaceAll(benchFlowYAML, "REPLAY_URL", replay.URL)), "bench", false)
	for _, size := range []int{1 << 10, 256 << 10, 4 << 20, 16 << 20} {
		body := bytes.Repeat([]byte("a"), size)
		for _, on := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/capture=%v", humanSize(size), on), func(b *testing.B) {
				srv, err := NewFlowServer(cfg, benchPublisher{}, slog.New(slog.DiscardHandler), metrics.New())
				if err != nil {
					b.Fatal(err)
				}
				defer func() { _ = srv.Close() }()
				var c *capture.Capturer
				if on {
					c, err = capture.New(capture.Options{Config: cfg, Metrics: metrics.New(), Logger: slog.New(slog.DiscardHandler)})
					if err != nil {
						b.Fatal(err)
					}
					srv.SetCapturer(c)
				}
				full.Store(0)
				metadataOnly.Store(0)
				took := make([]time.Duration, 0, b.N)
				b.SetBytes(int64(size))
				b.ResetTimer()
				for range b.N {
					req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/orders", bytes.NewReader(body))
					rec := httptest.NewRecorder()
					start := time.Now()
					srv.ServeHTTP(rec, req)
					took = append(took, time.Since(start))
					if rec.Code != http.StatusAccepted {
						b.Fatalf("status %d", rec.Code)
					}
				}
				b.StopTimer()
				slices.Sort(took)
				p99 := took[(len(took)*99+99)/100-1] // the ceil(0.99·n)-th fastest
				b.ReportMetric(float64(p99.Microseconds())/1000, "p99-ms")
				if c != nil {
					_ = c.Close() // drains the uploads before counting
					b.ReportMetric(float64(full.Load())/float64(b.N), "full-records/op")
					b.ReportMetric(float64(metadataOnly.Load())/float64(b.N), "metadata-only/op")
				}
			})
		}
	}
}
