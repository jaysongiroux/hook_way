package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

type originPublisher struct {
	mu      sync.Mutex
	origins []queue.Origin
}

func (p *originPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	o, _ := queue.OriginFrom(ctx)
	p.mu.Lock()
	p.origins = append(p.origins, o)
	p.mu.Unlock()
	return nil
}
func (p *originPublisher) Ready(context.Context) error { return nil }
func (p *originPublisher) Close() error                { return nil }

const captureFlowYAML = `
server: {addr: ":8080", max_body_bytes: 1048576}
replay:
  url: "http://127.0.0.1:1"
  capture_token: "capture-token-0123456789abcdefghijkl"
  control: {addr: ":9092", token: "control-token-0123456789abcdefghij"}
destinations:
  hook: {type: webhook, webhook: {url: "https://example.com/h"}}
flows:
  orders:
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: notify, type: publish, destination: hook, target: inbound}
    edges: [in>body, body>auth, auth>env, env>notify]
`

// A publish carries its flow, node, and the request's Settled, so an async
// webhook failure can be captured once the response status is final.
func TestFlowServerGivesPublishesACaptureOrigin(t *testing.T) {
	cfg, issues := flowconfig.LoadBytes([]byte(captureFlowYAML), "test", false)
	for _, issue := range issues {
		if issue.EffectiveSeverity() == config.SeverityError {
			t.Fatalf("issues = %+v", issues)
		}
	}
	pub := &originPublisher{}
	srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	c, err := capture.New(capture.Options{Config: cfg, Metrics: metrics.New(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	srv.SetCapturer(c)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/orders", bytes.NewReader([]byte(`{"a":1}`)))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || len(pub.origins) != 1 {
		t.Fatalf("status %d, origins %+v", rec.Code, pub.origins)
	}
	o := pub.origins[0]
	if o.Flow != "orders" || o.NodeID != "notify" || o.Settled == nil {
		t.Fatalf("origin = %+v", o)
	}
	var settled int
	o.Settled.AfterSettle(func(status int) { settled = status })
	if settled != http.StatusAccepted {
		t.Fatalf("Settled = %d, want the response status 202", settled)
	}
}
