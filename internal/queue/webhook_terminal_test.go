package queue

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

type terminalRecorder struct {
	mu   sync.Mutex
	seen []TerminalFailure
}

func (r *terminalRecorder) record(tf TerminalFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, tf)
}

func (r *terminalRecorder) all() []TerminalFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TerminalFailure(nil), r.seen...)
}

func failingWebhook(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return srv
}

func terminalWebhook(t *testing.T, url string, dlq bool) *WebhookPublisher {
	t.Helper()
	cfg := config.WebhookConfig{URL: url, Timeout: time.Second, DeliveryMode: "async", Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: config.RetryPolicyConfig{MaxAttempts: 1}}
	if dlq {
		cfg.DeadLetter = config.WebhookDeadLetterConfig{Destination: "dlq", Target: "dead"}
	}
	p, err := NewWebhookPublisher(cfg, "notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 1, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func withTestOrigin() context.Context {
	return WithOrigin(context.Background(), Origin{Flow: "orders_flow", NodeID: "notify_node", Settled: NewSettled()})
}

func TestTerminalFailureIsReportedWithoutADeadLetter(t *testing.T) {
	p := terminalWebhook(t, failingWebhook(t, 500).URL, false)
	var rec terminalRecorder
	p.setTerminalFailure(rec.record)
	if err := p.Publish(withTestOrigin(), "notify", "orders", Message{ID: "evt-1"}); err != nil {
		t.Fatal(err)
	}
	_ = p.Close() // drains the engine
	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("reported %d terminal failures, want 1", len(got))
	}
	tf := got[0]
	if tf.DestinationID != "notify" || tf.Target != "orders" || tf.RenderedTarget != "orders" || tf.Message.ID != "evt-1" ||
		tf.Outcome != "5xx" || tf.Attempts != 1 || tf.Err == nil || tf.FailedAt.IsZero() ||
		tf.Origin.Flow != "orders_flow" || tf.Origin.NodeID != "notify_node" || tf.Origin.Settled == nil {
		t.Fatalf("terminal failure = %+v", tf)
	}
}

func TestTerminalFailureIsNotReportedWhenDeadLettered(t *testing.T) {
	p := terminalWebhook(t, failingWebhook(t, 404).URL, true)
	var rec terminalRecorder
	var dead int
	p.setTerminalFailure(rec.record)
	p.setDeadLetterPublisher(func(context.Context, string, string, Message) error { dead++; return nil })
	if err := p.Publish(withTestOrigin(), "notify", "orders", Message{ID: "evt-1"}); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	if dead != 1 || len(rec.all()) != 0 {
		t.Fatalf("dead-lettered %d, reported %d; want 1 and 0", dead, len(rec.all()))
	}
}

func TestTerminalFailureIsReportedWhenTheDeadLetterFails(t *testing.T) {
	p := terminalWebhook(t, failingWebhook(t, 404).URL, true)
	var rec terminalRecorder
	p.setTerminalFailure(rec.record)
	p.setDeadLetterPublisher(func(context.Context, string, string, Message) error { return errors.New("dlq down") })
	if err := p.Publish(withTestOrigin(), "notify", "orders", Message{ID: "evt-1"}); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	if len(rec.all()) != 1 {
		t.Fatalf("reported %d terminal failures, want 1", len(rec.all()))
	}
}

// A redrive delivers inline and owns its outcome: it must never be captured.
func TestInlineDeliveryNeverReportsATerminalFailure(t *testing.T) {
	p := terminalWebhook(t, failingWebhook(t, 500).URL, false)
	var rec terminalRecorder
	p.setTerminalFailure(rec.record)
	err := p.Publish(WithInlineDelivery(withTestOrigin()), "notify", "orders", Message{ID: "evt-1"})
	var de *DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("Publish = %v, want a DeliveryError", err)
	}
	_ = p.Close()
	if len(rec.all()) != 0 {
		t.Fatal("an inline delivery was reported as a terminal async failure")
	}
}

func TestFactoryWiresTerminalFailuresIntoWebhookPublishers(t *testing.T) {
	srv := failingWebhook(t, 404)
	cfg := dlqFactoryConfig(srv.URL, true)
	notify := cfg.Destinations["notify"]
	notify.Webhook.DeadLetter = config.WebhookDeadLetterConfig{}
	cfg.Destinations["notify"] = notify
	delete(cfg.Destinations, "dlq")
	var rec terminalRecorder
	pub, err := NewFactory(FactoryOptions{OnTerminalFailure: rec.record, Logger: testLogger()}).Create(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(withTestOrigin(), "notify", "orders", Message{ID: "evt-1"}); err != nil {
		t.Fatal(err)
	}
	_ = pub.Close()
	if len(rec.all()) != 1 {
		t.Fatalf("reported %d terminal failures, want 1", len(rec.all()))
	}
}
