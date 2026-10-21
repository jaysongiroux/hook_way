package eventdelivery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/testenv"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))

func fixtureConfig(t *testing.T) flowconfig.Config {
	t.Helper()
	raw, err := os.ReadFile("../../examples/mcp-events.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "${EVENT_DATABASE_URL}", database(t)))
	cfg, issues := flowconfig.LoadBytes(raw, "example", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	t.Setenv("EVENT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("MCP_EVENTS_KEY", strings.Repeat("m", 32))
	t.Setenv("SAMPLE_WEBHOOK_SECRET", "incoming-secret")
	s, err := Open(context.Background(), *cfg.EventDelivery)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	return cfg
}
func runtimeFor(t *testing.T, cfg flowconfig.Config) *Runtime {
	t.Helper()
	r, err := New(context.Background(), cfg, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	r.BindEventAuthorizer("agent_tools", func(_ context.Context, p flowconfig.EventPrincipal) bool {
		return p.ID == "key:reader" && p.Key == "reader"
	})
	t.Cleanup(func() { _ = r.Close() })
	return r
}
func principalFor() flowconfig.EventPrincipal {
	return flowconfig.EventPrincipal{ID: "key:reader", Key: "reader"}
}
func subscribe(t *testing.T, r *Runtime, url string, patch map[string]any) (map[string]any, error) {
	t.Helper()
	q := map[string]any{"name": "issue.opened", "arguments": map[string]any{"repository": "acme/app"}, "delivery": map[string]any{"mode": "webhook", "url": url, "secret": "whsec_" + testKey}}
	for k, v := range patch {
		q[k] = v
	}
	raw, _ := json.Marshal(q)
	out, err := r.EventCall(context.Background(), "agent_tools", "events/subscribe", principalFor(), raw)
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}
func occurrence(id, repo string) map[string]any {
	return map[string]any{"eventId": id, "name": "issue.opened", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": map[string]any{"issue_id": id, "repository": repo, "title": "A new issue"}}
}
func callback(t *testing.T, status *atomic.Int32) (*httptest.Server, <-chan []byte, *atomic.Int32) {
	t.Helper()
	events := make(chan []byte, 100)
	challenges := &atomic.Int32{}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var msg map[string]any
		_ = json.Unmarshal(b, &msg)
		key, _ := base64.StdEncoding.DecodeString(testKey)
		if !strings.Contains(req.Header.Get("webhook-signature"), signature(key, req.Header.Get("webhook-id"), req.Header.Get("webhook-timestamp"), b)) {
			t.Error("invalid signature")
		}
		if msg["type"] == "verification" {
			challenges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"challenge": msg["challenge"]})
			return
		}
		events <- b
		code := int(status.Load())
		if code == 0 {
			code = 200
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	return s, events, challenges
}
func count(t *testing.T, r *Runtime, table, where string) int {
	t.Helper()
	var n int
	q := fmt.Sprintf("SELECT count(*) FROM hookway_events.%s WHERE namespace=$1 %s", table, where)
	if err := r.store.pool.QueryRow(context.Background(), q, r.cfg.Namespace).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestPostgresLifecyclePersistenceAndIsolation(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, _, challenges := callback(t, &status)
	r.client = server.Client()
	first, err := subscribe(t, r, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := subscribe(t, r, server.URL, nil)
	if err != nil || first["id"] != again["id"] || challenges.Load() != 1 {
		t.Fatalf("refresh %#v %v challenges=%d", again, err, challenges.Load())
	}
	if count(t, r, "subscriptions", "") != 1 {
		t.Fatal("duplicate subscription")
	}
	var sealed []byte
	if err = r.store.pool.QueryRow(context.Background(), `SELECT secret FROM hookway_events.subscriptions`).Scan(&sealed); err != nil || strings.Contains(string(sealed), testKey) {
		t.Fatal("secret not encrypted")
	}
	if _, err = r.openSecret("wrong-id", sealed); err == nil {
		t.Fatal("cross-subscription decryption")
	}
	if err = r.Publish(context.Background(), "agent_tools", "issue.opened", occurrence("one", "other/app")); err != nil || count(t, r, "deliveries", "") != 0 {
		t.Fatal("nonmatching event accepted")
	}
	event := occurrence("one", "acme/app")
	if err = r.Publish(context.Background(), "agent_tools", "issue.opened", event); err != nil {
		t.Fatal(err)
	}
	event["timestamp"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	if err = r.Publish(context.Background(), "agent_tools", "issue.opened", event); err != nil || count(t, r, "deliveries", "") != 1 {
		t.Fatal("idempotent acceptance failed", err)
	}
	event["data"].(map[string]any)["title"] = "changed"
	if err = r.Publish(context.Background(), "agent_tools", "issue.opened", event); !errors.Is(err, ErrConflict) {
		t.Fatal("identity conflict accepted", err)
	}
	restarted := runtimeFor(t, cfg)
	if count(t, restarted, "subscriptions", "") != 1 || count(t, restarted, "deliveries", "") != 1 {
		t.Fatal("restart lost state")
	}
	raw, _ := json.Marshal(map[string]any{"name": "issue.opened", "arguments": map[string]any{"repository": "acme/app"}, "delivery": map[string]any{"mode": "webhook", "url": server.URL}})
	if _, err = r.EventCall(context.Background(), "agent_tools", "events/unsubscribe", flowconfig.EventPrincipal{ID: "key:other", Key: "other"}, raw); err == nil {
		t.Fatal("other principal unsubscribed")
	}
	if _, err = r.EventCall(context.Background(), "other_endpoint", "events/unsubscribe", principalFor(), raw); err == nil {
		t.Fatal("other endpoint unsubscribed")
	}
	for i := 0; i < 2; i++ {
		if _, err = r.EventCall(context.Background(), "agent_tools", "events/unsubscribe", principalFor(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, r, "deliveries", "AND state='cancelled'") != 1 {
		t.Fatal("unsubscribe did not cancel")
	}
	otherCfg := cfg
	copyCfg := *cfg.EventDelivery
	copyCfg.Namespace = "another-deployment"
	otherCfg.EventDelivery = &copyCfg
	other := runtimeFor(t, otherCfg)
	if count(t, other, "subscriptions", "") != 0 {
		t.Fatal("namespace leak")
	}
}
func TestPostgresLeasesRetryRecoveryAndLimits(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, received, _ := callback(t, &status)
	r.client = server.Client()
	if _, e := subscribe(t, r, server.URL, nil); e != nil {
		t.Fatal(e)
	}
	if e := r.Publish(context.Background(), "agent_tools", "issue.opened", occurrence("retry", "acme/app")); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	j, e := r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.claim(ctx); e == nil {
		t.Fatal("job claimed twice")
	}
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.deliveries SET lease_until=now()-interval '1 second'`)
	newJob, e := r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r.finish(ctx, j, "delivered", "stale", 0)
	if count(t, r, "deliveries", "AND state='pending'") != 1 {
		t.Fatal("stale worker completed job")
	}
	status.Store(503)
	r.attempt(ctx, newJob)
	first := <-received
	if count(t, r, "deliveries", "AND state='pending' AND outcome='retry'") != 1 {
		t.Fatal("transient failure not retried")
	}
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.deliveries SET available_at=now()`)
	newJob, e = r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	status.Store(200)
	r.attempt(ctx, newJob)
	second := <-received
	if string(first) != string(second) || count(t, r, "deliveries", "AND state='delivered'") != 1 {
		t.Fatal("retry changed event bytes")
	}
	if e = r.Publish(ctx, "agent_tools", "issue.opened", occurrence("permanent", "acme/app")); e != nil {
		t.Fatal(e)
	}
	j, e = r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	status.Store(410)
	r.attempt(ctx, j)
	<-received
	if count(t, r, "deliveries", "AND state='failed'") != 1 {
		t.Fatal("410 retried")
	}
	r.cfg.MaxPending = 1
	if e = r.Publish(ctx, "agent_tools", "issue.opened", occurrence("capacity", "acme/app")); e != nil {
		t.Fatal(e)
	}
	if e = r.Publish(ctx, "agent_tools", "issue.opened", occurrence("overflow", "acme/app")); !errors.Is(e, ErrCapacity) {
		t.Fatal("capacity not enforced", e)
	}
	if count(t, r, "occurrences", "AND event_id='overflow'") != 0 {
		t.Fatal("capacity transaction partially committed")
	}
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.subscriptions SET expires_at=now()-interval '1 second'`)
	if e = r.store.GC(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, r, "deliveries", "AND state='pending'") != 0 {
		t.Fatal("expired pending deliveries retained")
	}
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.deliveries SET finished_at=now()-interval '25 hours';UPDATE hookway_events.occurrences SET created_at=now()-interval '25 hours'`)
	if e = r.store.GC(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, r, "occurrences", "") != 0 {
		t.Fatal("old bodies retained")
	}
}
func TestPostgresConcurrentAcceptanceAndRotation(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, _, _ := callback(t, &status)
	r.client = server.Client()
	if _, e := subscribe(t, r, server.URL, nil); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := r.Publish(context.Background(), "agent_tools", "issue.opened", occurrence("concurrent", "acme/app")); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if count(t, r, "deliveries", "") != 1 {
		t.Fatal("concurrent duplicate fanout")
	}
	rotated := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32)))
	r.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var data map[string]any
		_ = json.NewDecoder(req.Body).Decode(&data)
		if data["type"] != "verification" {
			sig := req.Header.Get("webhook-signature")
			if len(strings.Fields(sig)) != 2 {
				t.Error("rotation did not dual-sign")
			}
		}
		body, _ := json.Marshal(map[string]any{"challenge": data["challenge"]})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})}

	if _, err := subscribe(t, r, server.URL, map[string]any{"delivery": map[string]any{"mode": "webhook", "url": server.URL, "secret": "whsec_" + rotated}}); err != nil {
		t.Fatal(err)
	}
	if count(t, r, "subscriptions", "AND previous_secret IS NOT NULL AND overlap_until>now()") != 1 {
		t.Fatal("rotation lost overlap")
	}
	if _, err := subscribe(t, r, server.URL, map[string]any{"delivery": map[string]any{"mode": "webhook", "url": server.URL, "secret": "whsec_" + rotated}}); err != nil {
		t.Fatal(err)
	}
	if count(t, r, "subscriptions", "AND previous_secret IS NOT NULL") != 1 {
		t.Fatal("refresh removed overlap")
	}
	ctx := context.Background()
	job, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.attempt(ctx, job)
	if count(t, r, "deliveries", "AND state='delivered'") != 1 {
		t.Fatal("rotated signing delivery failed")
	}
	r.store.pool.Close()
	if r.Ready(ctx) == nil {
		t.Fatal("failed store ready")
	}
	if err := r.Publish(ctx, "agent_tools", "issue.opened", occurrence("unavailable", "acme/app")); err == nil {
		t.Fatal("unavailable store acknowledged")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPostgresGatewayAndAuthenticatedWebhook(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, received, _ := callback(t, &status)
	r.client = server.Client()
	pub := &Publisher{Runtime: r, Endpoint: "agent_tools"}
	app, e := httpapi.NewFlowServerWithEvents(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), metrics.New(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = app.Close() }()
	call := func(method string, params any) map[string]any {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": withProtocol(params)})
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/agents/mcp", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("MCP-Method", method)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		var out map[string]any
		body := w.Body.Bytes()
		if strings.HasPrefix(string(body), "event:") {
			for _, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(line, "data: ") {
					body = []byte(strings.TrimPrefix(line, "data: "))
					break
				}
			}
		}
		if e := json.Unmarshal(body, &out); e != nil {
			t.Fatalf("%s status %d body %s", method, w.Code, w.Body.String())
		}
		return out
	}
	discovered := call("server/discover", map[string]any{})
	result, _ := discovered["result"].(map[string]any)
	caps, _ := result["capabilities"].(map[string]any)
	if caps["events"] == nil || caps["tools"] == nil {
		t.Fatalf("capabilities lost: %v", discovered)
	}
	if v := call("events/list", map[string]any{}); v["error"] != nil {
		t.Fatal(v)
	}
	if v := call("events/subscribe", map[string]any{"name": "issue.opened", "arguments": map[string]any{"repository": "acme/app"}, "delivery": map[string]any{"mode": "webhook", "url": server.URL, "secret": "whsec_" + testKey}}); v["error"] != nil {
		t.Fatal(v)
	}
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/webhooks/issues", strings.NewReader(`{"issue_id":"from-http","repository":"acme/app","title":"New"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "incoming-secret")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code < 200 || w.Code >= 300 {
		t.Fatalf("webhook status %d: %s", w.Code, w.Body.String())
	}
	r.Start()
	select {
	case b := <-received:
		var ev map[string]any
		_ = json.Unmarshal(b, &ev)
		if ev["eventId"] != "from-http" || ev["cursor"] != nil {
			t.Fatal("incorrect event", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not deliver")
	}

	r.Stop()
	r.wg.Wait()
	if err := r.Publish(context.Background(), "agent_tools", "issue.opened", occurrence("after-restart", "acme/app")); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	recovered := runtimeFor(t, cfg)
	recovered.client = server.Client()
	recoveredPub := &Publisher{Runtime: recovered, Endpoint: "agent_tools"}
	recoveredApp, err := httpapi.NewFlowServerWithEvents(cfg, recoveredPub, slog.New(slog.NewTextHandler(io.Discard, nil)), metrics.New(), recovered)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recoveredApp.Close() }()
	recovered.Start()
	select {
	case b := <-received:
		var ev map[string]any
		_ = json.Unmarshal(b, &ev)
		if ev["eventId"] != "after-restart" {
			t.Fatal("restart changed event", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restart failed to recover pending work")
	}

}

func withProtocol(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}
	m["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "event-test", "version": "1"}}
	return m
}

func database(t *testing.T) string {
	t.Helper()
	testenv.SkipIfShort(t)
	container, err := tcpostgres.Run(context.Background(), "postgres:16-alpine", tcpostgres.WithDatabase("events"), tcpostgres.WithUsername("events"), tcpostgres.WithPassword("test-password"), tcpostgres.BasicWaitStrategies())
	testenv.RequireContainer(t, err, "postgres")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	url, err := container.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}
