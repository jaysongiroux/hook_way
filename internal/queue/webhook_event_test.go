package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
)

func TestWebhookJSONPayloadSignsFrozenBody(t *testing.T) {
	delivery := envelopeDelivery("https://example.test/events")
	delivery.auth = webhookAuth{Type: "hmac", Scheme: "hmac_sha256_hex", Secret: "outbound-signing-key"}
	body := []byte(`{"eventId":"event-1","name":"issue.opened","timestamp":"2026-10-21T00:00:00Z","data":{"id":"issue-1"}}`)
	msg := Message{ID: "ingress-id", Body: map[string]any{"changed": true}, RawBody: []byte(`{"original":true}`),
		Publish: &deliverymeta.Publish{WireBody: "json", Body: body}}
	req, err := buildWebhookRequest(context.Background(), delivery, msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) || req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("frozen payload changed: %s", got)
	}
	mac := hmac.New(sha256.New, []byte(delivery.auth.Secret))
	_, _ = mac.Write(body)
	if req.Header.Get("X-Hookway-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("signature did not cover the delivered JSON body")
	}
	msg.Publish.Body = nil
	if _, err := buildWebhookRequest(context.Background(), delivery, msg); err == nil {
		t.Fatal("invalid frozen JSON silently fell back to another payload")
	}
}
