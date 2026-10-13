package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// This sink isolates Hookway CPU/allocation work from network/broker latency.
// It accepts bytes without storing them; throughput is not durable delivery.
type performancePublisher struct{}

func (performancePublisher) Publish(_ context.Context, _, _ string, message queue.Message) error {
	_, err := queue.BrokerBody(message)
	return err
}
func (performancePublisher) Ready(context.Context) error { return nil }
func (performancePublisher) Close() error                { return nil }

func performanceConfig(size int, signing bool) (flowconfig.Config, []byte, string, string, error) {
	auth := "{id: auth, type: auth, scheme: exact, secret: synthetic-key, signature_header: X-Performance-Token}"
	wire := "json"
	if signing {
		auth = "{id: auth, type: auth, scheme: hmac_sha256_headers_dot, secret: synthetic-key, signature_header: X-Performance-Signature, timestamp_header: X-Performance-Timestamp, timestamp_unit: milliseconds, timestamp_policy: age, timestamp_tolerance: 5m, body_representation: json_js}"
		wire = "json_js"
	}
	source := fmt.Sprintf(`
server: {addr: ":8080", max_body_bytes: 1048576, max_inflight_requests: 1024}
destinations: {broker: {type: sqs, sqs: {region: us-east-1}}}
flows:
  synthetic:
    responses: {published: {status: 200, body: '{"accepted":true}'}}
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/performance, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - %s
      - {id: env, type: envelope}
      - {id: send, type: publish, destination: broker, target: synthetic-performance, create: false, wire_body: %s}
    edges: [in>body, body>auth, auth>env, env>send]
`, auth, wire)
	cfg, issues := flowconfig.LoadBytes([]byte(source), "performance", false)
	for _, issue := range issues {
		if issue.Severity == "error" {
			return cfg, nil, "", "", fmt.Errorf("config: %s", issue.Message)
		}
	}
	body := []byte(`{"event_id":"synthetic-event","padding":"` + strings.Repeat("x", size) + `"}`)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte("synthetic-key"))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	return cfg, body, timestamp, hex.EncodeToString(mac.Sum(nil)), nil
}

func BenchmarkPerformanceFlow(b *testing.B) {
	for _, size := range []int{1024, 16384} {
		for _, mode := range []string{"exact_warn", "exact_info", "json_signing_warn"} {
			b.Run(fmt.Sprintf("%s/%dB", mode, size), func(b *testing.B) {
				cfg, body, timestamp, signature, err := performanceConfig(size, strings.HasPrefix(mode, "json"))
				if err != nil {
					b.Fatal(err)
				}
				level := slog.LevelWarn
				if mode == "exact_info" {
					level = slog.LevelInfo
				}
				logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level}))
				server, err := NewFlowServer(cfg, performancePublisher{}, logger, metrics.New())
				if err != nil {
					b.Fatal(err)
				}
				defer func() { _ = server.Close() }()
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						request := httptest.NewRequestWithContext(b.Context(), "POST", "/webhooks/performance", bytes.NewReader(body))
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("X-Performance-Token", "synthetic-key")
						request.Header.Set("X-Performance-Timestamp", timestamp)
						request.Header.Set("X-Performance-Signature", signature)
						response := httptest.NewRecorder()
						server.ServeHTTP(response, request)
						if response.Code != 200 {
							b.Errorf("status %d", response.Code)
							return
						}
					}
				})
			})
		}
	}
}

// benchmarkResponse avoids retaining a second full response in the harness.
type benchmarkResponse struct {
	header http.Header
	status int
	size   int
}

func (w *benchmarkResponse) Header() http.Header    { return w.header }
func (w *benchmarkResponse) WriteHeader(status int) { w.status = status }
func (w *benchmarkResponse) Write(body []byte) (int, error) {
	w.size += len(body)
	return len(body), nil
}

func BenchmarkPerformanceRawProxy(b *testing.B) {
	body := bytes.Repeat([]byte{0xa5}, 12<<20)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, r.Body) }))
	defer upstream.Close()
	source := fmt.Sprintf(`
server: {addr: ":8080", max_body_bytes: 20971520, max_inflight_requests: 8}
destinations: {}
flows:
  files:
    nodes:
      - {id: in, type: http_trigger, path: /private/performance/files, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 20971520}
      - {id: auth, type: auth, scheme: exact, secret: synthetic-key, signature_header: X-Performance-Token}
      - {id: env, type: envelope, body_format: raw}
      - {id: up, type: http_request, url: '%s', method: POST, body: raw, max_response_bytes: 20971520, timeout: 30s}
      - {id: reply, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>reply]
`, upstream.URL)
	for _, concurrency := range []int{1, 4, 8} {
		b.Run(fmt.Sprintf("12MiB/concurrency-%d", concurrency), func(b *testing.B) {
			if concurrency > 1 && concurrency%runtime.GOMAXPROCS(0) != 0 {
				b.Fatal("use -cpu=1 or -cpu=2 for exact worker counts")
			}
			cfg, issues := flowconfig.LoadBytes([]byte(source), "performance", false)
			for _, issue := range issues {
				if issue.Severity == "error" {
					b.Fatal(issue.Message)
				}
			}
			server, err := NewFlowServer(cfg, performancePublisher{}, slog.New(slog.DiscardHandler), metrics.New())
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			runtime.GC()
			var peak atomic.Uint64
			stop := make(chan struct{})
			stopped := make(chan struct{})
			sample := func() {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				for previous := peak.Load(); m.HeapAlloc > previous; previous = peak.Load() {
					if peak.CompareAndSwap(previous, m.HeapAlloc) {
						break
					}
				}
			}
			sample()
			go func() {
				defer close(stopped)
				ticker := time.NewTicker(25 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						sample()
					}
				}
			}()
			request := func() {
				r := httptest.NewRequestWithContext(b.Context(), "POST", "/private/performance/files", bytes.NewReader(body))
				r.Header.Set("X-Performance-Token", "synthetic-key")
				w := &benchmarkResponse{header: make(http.Header)}
				server.ServeHTTP(w, r)
				if w.status != 200 || w.size != len(body) {
					b.Errorf("proxy response %d, size %d", w.status, w.size)
				}
			}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			if concurrency == 1 {
				for range b.N {
					request()
				}
			} else {
				b.SetParallelism(concurrency / runtime.GOMAXPROCS(0))
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						request()
					}
				})
			}
			b.StopTimer()
			close(stop)
			<-stopped
			b.ReportMetric(float64(peak.Load())/(1<<20), "sampled-heap-MiB")
		})
	}
}

// Representative event through the real handler, with a no-network publisher.
// This separates runtime CPU/allocation cost from outbound scheduling and I/O.
func BenchmarkRepresentativeSignedFlow(b *testing.B) {
	cfg, _, timestamp, _, err := performanceConfig(0, true)
	if err != nil {
		b.Fatal(err)
	}
	body, err := os.ReadFile("../testdata/record-update.json")
	if err != nil {
		b.Fatal(err)
	}
	body = bytes.TrimSpace(body)
	mac := hmac.New(sha256.New, []byte("synthetic-key"))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))
	server, err := NewFlowServer(cfg, performancePublisher{}, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			request := httptest.NewRequestWithContext(b.Context(), "POST", "/webhooks/performance", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Performance-Timestamp", timestamp)
			request.Header.Set("X-Performance-Signature", signature)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != 200 {
				b.Fatalf("status %d", response.Code)
			}
		}
	})
}
