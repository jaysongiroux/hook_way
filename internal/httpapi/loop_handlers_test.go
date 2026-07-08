package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

// loopConfig returns a testConfig with the given loop-protection settings.
func loopConfig(enabled *bool, maxHops int) config.Config {
	cfg := testConfig()
	cfg.Security.LoopProtection = config.LoopProtectionConfig{Enabled: enabled, MaxHops: maxHops}
	return cfg
}

func postWithHops(t *testing.T, srv *Server, hops string) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	if hops != "" {
		req.Header.Set(config.LoopHopsHeader, hops)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestWebhookLoopRejectedAtDefaultMax(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(loopConfig(nil, 0), pub, slog.Default()) // defaults: enabled, max 3

	rec := postWithHops(t, srv, "3")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want nothing on loop reject", pub.published)
	}
}

func TestWebhookLoopBelowMaxPassesAndCarriesHops(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(loopConfig(nil, 0), pub, slog.Default())

	rec := postWithHops(t, srv, "2") // 2 < 3
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	// The inbound hop count must reach the message so the destination can stamp +1.
	if pub.published[0].msg.Hops != 2 {
		t.Fatalf("msg.Hops = %d, want 2", pub.published[0].msg.Hops)
	}
}

func TestWebhookLoopNoHeaderIsFreshDelivery(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(loopConfig(nil, 0), pub, slog.Default())

	rec := postWithHops(t, srv, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 || pub.published[0].msg.Hops != 0 {
		t.Fatalf("fresh delivery should have Hops 0: %#v", pub.published)
	}
}

func TestWebhookLoopConfigurableMax(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(loopConfig(nil, 1), pub, slog.Default()) // max 1

	if rec := postWithHops(t, srv, "1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("hops=1 with max=1 status = %d, want 400", rec.Code)
	}
	if rec := postWithHops(t, srv, "0"); rec.Code != http.StatusAccepted {
		t.Fatalf("hops=0 with max=1 status = %d, want 202", rec.Code)
	}
}

func TestWebhookLoopDisabledAllowsHighHops(t *testing.T) {
	disabled := false
	pub := &fakePublisher{}
	srv := NewServer(loopConfig(&disabled, 0), pub, slog.Default())

	rec := postWithHops(t, srv, strconv.Itoa(99))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 when loop protection disabled; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
}

func TestWebhookLoopMalformedHeaderTreatedAsZero(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(loopConfig(nil, 0), pub, slog.Default())

	rec := postWithHops(t, srv, "not-a-number")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (malformed header = 0); body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 || pub.published[0].msg.Hops != 0 {
		t.Fatalf("malformed header should yield Hops 0: %#v", pub.published)
	}
}
