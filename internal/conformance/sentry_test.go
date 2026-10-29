package conformance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

type discordReceipt struct {
	Content         string `json:"content"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

func sentryRequest(body, resource, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/sentry", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sentry-Hook-Resource", resource)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	req.Header.Set("Sentry-Hook-Signature", hex.EncodeToString(mac.Sum(nil)))
	return req
}

func sentryExample(t *testing.T, handler http.HandlerFunc) *httpapi.FlowServer {
	t.Helper()
	stub := httptest.NewServer(handler)
	t.Cleanup(stub.Close)
	t.Setenv("SENTRY_CLIENT_SECRET", "sentry-secret")
	t.Setenv("DISCORD_WEBHOOK_URL", stub.URL+"/api/webhooks/42/test-token?wait=true")
	t.Setenv("HOOKWAY_API_KEY", "test-key")
	raw, err := os.ReadFile("../../examples/sentry-discord.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := loadCfg(t, os.ExpandEnv(string(raw)))
	cfg.Server.FanoutConcurrency = 1
	pub, err := queue.NewFactory(queue.FactoryOptions{Logger: slog.New(slog.DiscardHandler)}).Create(context.Background(), cfg.ServingConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pub.Close(); err != nil {
			t.Error(err)
		}
	})
	srv, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestSentryDiscordExample(t *testing.T) {
	receipts := make(chan discordReceipt, 10)
	srv := sentryExample(t, func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/api/webhooks/42/test-token?wait=true" || r.Method != http.MethodPost {
			t.Errorf("wrong Discord endpoint: %s %s", r.Method, r.RequestURI)
		}
		if r.Header.Get("Sentry-Hook-Signature") != "" || r.Header.Get("Authorization") != "" {
			t.Error("forwarded source credentials")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "must-not-forward") || strings.Contains(string(body), "schema_version") {
			t.Error("forwarded full event/envelope")
		}
		var receipt discordReceipt
		if err := json.Unmarshal(body, &receipt); err != nil {
			t.Error(err)
		}
		receipts <- receipt
		w.WriteHeader(http.StatusNoContent)
	})
	fixture, err := os.ReadFile("../../examples/fixtures/sentry-issue-alert.json")
	if err != nil {
		t.Fatal(err)
	}
	body := string(fixture)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Sentry-Hook-Signature") },
		func(r *http.Request) {
			r.Header.Set("Sentry-Hook-Signature", "sha256="+r.Header.Get("Sentry-Hook-Signature"))
		},
		func(r *http.Request) { r.Header.Add("Sentry-Hook-Signature", r.Header.Get("Sentry-Hook-Signature")) },
	} {
		r := sentryRequest(body, "event_alert", "sentry-secret")
		mutate(r)
		if got := post(srv, r).Code; got != 401 {
			t.Fatalf("invalid auth: %d", got)
		}
	}
	if got := post(srv, sentryRequest(body, "event_alert", "wrong")).Code; got != 401 {
		t.Fatal(got)
	}
	tampered := sentryRequest(body, "event_alert", "sentry-secret")
	tampered.Body = io.NopCloser(strings.NewReader(body + " "))
	if got := post(srv, tampered).Code; got != 401 {
		t.Fatal(got)
	}
	if got := post(srv, sentryRequest(body, "event_alert", "sentry-secret")).Code; got != 202 {
		t.Fatal(got)
	}
	select {
	case receipt := <-receipts:
		want := "**Sentry: ReferenceError: checkout is not defined**\nRule: New production issue\nProject: 42 · Environment: production · Level: error\n<https://sentry.io/organizations/example/issues/123/events/0123456789abcdef0123456789abcdef/>"
		if receipt.Content != want || receipt.AllowedMentions.Parse == nil || len(receipt.AllowedMentions.Parse) != 0 {
			t.Fatalf("wrong Discord message: %+v", receipt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing Discord message")
	}
	// The unsigned timestamp/attempt ID cannot bypass the body-based guard.
	duplicate := sentryRequest(body, "event_alert", "sentry-secret")
	duplicate.Header.Set("Sentry-Hook-Timestamp", "9999999999")
	duplicate.Header.Set("Request-ID", "different-attempt")
	if got := post(srv, duplicate).Code; got != 409 {
		t.Fatal(got)
	}
	for _, tc := range []struct{ body, resource string }{
		{`{"action":"created","data":{}}`, "issue"},
		{`{"action":"resolved","data":{}}`, "event_alert"},
		{`{"action":"triggered","data":{"other":true}}`, "metric_alert"},
	} {
		if got := post(srv, sentryRequest(tc.body, tc.resource, "sentry-secret")).Code; got != 200 {
			t.Fatal(got)
		}
	}
	if got := post(srv, sentryRequest(`{"action":"triggered"}`, "event_alert", "sentry-secret")).Code; got != 202 {
		t.Fatal(got)
	}
	select {
	case receipt := <-receipts:
		if receipt.Content != "**Sentry: Issue alert triggered**\nRule: Sentry issue alert\nProject: unknown · Environment: unknown · Level: unknown" {
			t.Fatalf("unexpected fallback or extra delivery: %+v", receipt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing fallback message")
	}
	select {
	case receipt := <-receipts:
		t.Fatalf("extra message: %+v", receipt)
	default:
	}
	longBody, err := json.Marshal(map[string]any{"action": "triggered", "data": map[string]any{
		"triggered_rule": strings.Repeat("*", 3000),
		"event": map[string]any{"title": strings.Repeat("*", 3000) + "@everyone", "project": strings.Repeat("_", 3000),
			"environment": strings.Repeat("~", 3000), "level": strings.Repeat("|", 3000),
			"web_url": "https://example.test/" + strings.Repeat("a", 750)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := post(srv, sentryRequest(string(longBody), "event_alert", "sentry-secret")).Code; got != 202 {
		t.Fatal(got)
	}
	select {
	case receipt := <-receipts:
		if len(receipt.Content) > 2000 || !strings.Contains(receipt.Content, `\*`) || len(receipt.AllowedMentions.Parse) != 0 || receipt.AllowedMentions.Parse == nil {
			t.Fatal("message limits, escaping or disabled mentions lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing bounded message")
	}
}

func TestSentryAcknowledgesBeforeDiscordResponds(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	srv := sentryExample(t, func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	defer close(release)
	done := make(chan int, 1)
	go func() {
		done <- post(srv, sentryRequest(`{"action":"triggered"}`, "event_alert", "sentry-secret")).Code
	}()
	select {
	case status := <-done:
		if status != 202 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("acknowledgement waited for Discord")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Discord was never called")
	}
}

func TestSentryDiscordRetriesTransientFailures(t *testing.T) {
	var attempts atomic.Int32
	done := make(chan struct{}, 1)
	srv := sentryExample(t, func(w http.ResponseWriter, _ *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(429)
		case 2:
			w.WriteHeader(500)
		default:
			w.WriteHeader(204)
			done <- struct{}{}
		}
	})
	if got := post(srv, sentryRequest(`{"action":"triggered"}`, "event_alert", "sentry-secret")).Code; got != 202 {
		t.Fatal(got)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("transient delivery was not retried")
	}
	if attempts.Load() != 3 {
		t.Fatal(attempts.Load())
	}
}
