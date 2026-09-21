package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApplyWebhookAuthAPIKey(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "POST", "http://x", nil)
	applyWebhookAuth(req, webhookAuth{Type: "api_key", Header: "X-API-Key", Value: "secret123"}, nil, "evt", time.Now())
	if req.Header.Get("X-API-Key") != "secret123" {
		t.Fatalf("api key header = %q", req.Header.Get("X-API-Key"))
	}
}

func TestApplyWebhookAuthHMACVerifiable(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	secret := "s3cr3t"
	now := time.Unix(1700000000, 0)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "http://x", nil)
	applyWebhookAuth(req, webhookAuth{Type: "hmac", Secret: secret, Header: "X-Hookway-Signature", Scheme: "hmac_sha256_ts_dot", EventIDHeader: "X-Hookway-Event-Id"}, body, "evt_1", now)

	sig := req.Header.Get("X-Hookway-Signature") // t=<ts>,v1=<hex>
	if !strings.HasPrefix(sig, "t=1700000000,v1=") {
		t.Fatalf("signature header format: %q", sig)
	}
	if req.Header.Get("X-Hookway-Event-Id") != "evt_1" {
		t.Fatalf("event id header = %q", req.Header.Get("X-Hookway-Event-Id"))
	}
	// recompute HMAC over "<ts>.<body>" and compare
	got := strings.TrimPrefix(sig, "t=1700000000,v1=")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", now.Unix(), body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("hmac mismatch: got %s want %s", got, want)
	}
}

func TestSignWebhookHMACTsColon(t *testing.T) {
	body := []byte(`{"event":"order.created"}`)
	secret := "mycolon secret"
	ts := int64(1700000001)

	got := signWebhookHMAC("hmac_sha256_ts_colon", secret, ts, body)

	// Expected format: ts=<ts>;h1=<hex>
	prefix := fmt.Sprintf("ts=%d;h1=", ts)
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("hmac_sha256_ts_colon format: got %q, want prefix %q", got, prefix)
	}
	hexPart := strings.TrimPrefix(got, prefix)

	// Recompute: signing string is ascii_bytes(ts) + b":" + body_bytes
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d:%s", ts, body)
	want := hex.EncodeToString(mac.Sum(nil))

	if hexPart != want {
		t.Fatalf("hmac_sha256_ts_colon HMAC mismatch: got %s want %s", hexPart, want)
	}
}

func TestSignWebhookHMACHex(t *testing.T) {
	body := []byte(`{"event":"order.updated"}`)
	secret := "myhex secret"
	ts := int64(1700000002)

	got := signWebhookHMAC("hmac_sha256_hex", secret, ts, body)

	// Expected format: sha256=<hex>
	if !strings.HasPrefix(got, "sha256=") {
		t.Fatalf("hmac_sha256_hex format: got %q, want prefix \"sha256=\"", got)
	}
	hexPart := strings.TrimPrefix(got, "sha256=")

	// Recompute: signing string is raw body bytes only (no timestamp)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if hexPart != want {
		t.Fatalf("hmac_sha256_hex HMAC mismatch: got %s want %s", hexPart, want)
	}
}

func TestApplyWebhookAuthNoneAndProxyAreNoops(t *testing.T) {
	for _, typ := range []string{"none", "proxy", ""} {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "http://x", nil)
		before := len(req.Header)
		applyWebhookAuth(req, webhookAuth{Type: typ}, []byte("b"), "evt", time.Now())
		if len(req.Header) != before {
			t.Fatalf("type %q should not add headers", typ)
		}
	}
}
