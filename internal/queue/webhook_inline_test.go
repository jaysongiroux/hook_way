package queue

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func statusServer(t *testing.T, status int, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// asyncWebhook builds an async (queueing) webhook publisher for target
// "orders", optionally with a dead-letter destination.
func asyncWebhook(t *testing.T, url string, retry config.RetryPolicyConfig, dlq config.WebhookDeadLetterConfig) *WebhookPublisher {
	t.Helper()
	pub, err := NewWebhookPublisher(config.WebhookConfig{
		URL: url, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: retry, DeadLetter: dlq,
	}, "notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: 2 * time.Second, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	return pub
}

func TestInlineDeliveryFlagSurvivesDerivedContexts(t *testing.T) {
	if InlineDelivery(context.Background()) {
		t.Fatal("InlineDelivery(Background) = true, want false")
	}
	ctx := WithInlineDelivery(context.Background())
	derived, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if !InlineDelivery(ctx) || !InlineDelivery(derived) {
		t.Fatal("the flag must survive WithoutCancel and WithTimeout; the executor derives publish contexts that way")
	}
}

func TestInlineDeliveryDeliversBeforePublishReturns(t *testing.T) {
	var hits int32
	pub := asyncWebhook(t, statusServer(t, http.StatusOK, &hits).URL, config.RetryPolicyConfig{}, config.WebhookDeadLetterConfig{})
	if err := pub.Publish(WithInlineDelivery(context.Background()), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish() = %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("hits when Publish returned = %d, want 1 (inline delivery is synchronous)", got)
	}
}

func TestInlineDeliveryReturnsTheRealOutcome(t *testing.T) {
	var hits int32
	pub := asyncWebhook(t, statusServer(t, http.StatusNotFound, &hits).URL, config.RetryPolicyConfig{}, config.WebhookDeadLetterConfig{})
	err := pub.Publish(WithInlineDelivery(context.Background()), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()})
	var de *DeliveryError
	if !errors.As(err, &de) || de.Outcome != "4xx" || de.Attempts != 1 {
		t.Fatalf("Publish() = %v, want a DeliveryError with outcome 4xx after 1 attempt", err)
	}
}

func TestInlineDeliveryNeverDeadLetters(t *testing.T) {
	var hits int32
	pub := asyncWebhook(t, statusServer(t, http.StatusNotFound, &hits).URL, config.RetryPolicyConfig{},
		config.WebhookDeadLetterConfig{Destination: "dlq", Target: "dead"})
	var deadLettered int32
	pub.setDeadLetterPublisher(func(context.Context, string, string, Message) error {
		atomic.AddInt32(&deadLettered, 1)
		return nil
	})
	if err := pub.Publish(WithInlineDelivery(context.Background()), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err == nil {
		t.Fatal("Publish() = nil, want the 4xx failure returned to the caller")
	}
	_ = pub.Close() // drain the engine: anything queued would dead-letter now
	if got := atomic.LoadInt32(&deadLettered); got != 0 {
		t.Fatalf("dead-letter dispatches = %d, want 0 (the caller owns an inline failure)", got)
	}
}

func TestInlineDeliveryHonorsDeadlineDuringBackoff(t *testing.T) {
	var hits int32
	pub := asyncWebhook(t, statusServer(t, http.StatusServiceUnavailable, &hits).URL,
		config.RetryPolicyConfig{MaxAttempts: 5, InitialBackoff: time.Second, MaxBackoff: time.Second},
		config.WebhookDeadLetterConfig{})
	ctx, cancel := context.WithTimeout(WithInlineDelivery(context.Background()), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := pub.Publish(ctx, "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()})
	if elapsed := time.Since(started); elapsed > 900*time.Millisecond {
		t.Fatalf("Publish took %s, want it to stop at the 100ms deadline instead of sleeping the 1s backoff", elapsed)
	}
	var de *DeliveryError
	if !errors.As(err, &de) || de.Outcome != "5xx" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Publish() = %v, want a 5xx DeliveryError wrapping context.DeadlineExceeded", err)
	}
}

func TestSyncDeliveryErrorKeepsItsMessage(t *testing.T) {
	var hits int32
	srv := statusServer(t, http.StatusServiceUnavailable, &hits)
	pub, err := NewWebhookPublisher(config.WebhookConfig{
		URL: srv.URL, Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: config.RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond},
	}, "notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	err = pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()})
	var de *DeliveryError
	if !errors.As(err, &de) || de.Outcome != "5xx" || de.Attempts != 3 {
		t.Fatalf("Publish() = %v, want a DeliveryError with outcome 5xx after 3 attempts", err)
	}
	const want = `webhook "notify": webhook "notify" target "orders": delivery failed after 3 attempts (5xx)`
	if err.Error() != want {
		t.Fatalf("error text = %q, want it unchanged: %q", err.Error(), want)
	}
}

func TestDeliveryErrorWithoutACauseStillPrints(t *testing.T) {
	if got := (&DeliveryError{Outcome: "5xx", Attempts: 2}).Error(); !strings.Contains(got, "5xx") {
		t.Fatalf("Error() = %q", got)
	}
}

func TestPublishToAnUnknownTargetSendsNothing(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	pub := asyncWebhook(t, srv.URL, config.RetryPolicyConfig{MaxAttempts: 1}, config.WebhookDeadLetterConfig{})
	err := pub.Publish(WithInlineDelivery(context.Background()), "notify", "not-a-target", Message{ID: "evt-1"})
	if !errors.Is(err, ErrTargetNotConfigured) || hits.Load() != 0 {
		t.Fatalf("Publish = %v after %d requests; want ErrTargetNotConfigured and none", err, hits.Load())
	}
	if !strings.Contains(err.Error(), `target "not-a-target" is not configured`) {
		t.Fatalf("error text = %q", err)
	}
}

type levelRecorder struct {
	mu     sync.Mutex
	levels map[string]slog.Level
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	r.levels[rec.Message] = rec.Level
	r.mu.Unlock()
	return nil
}
func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *levelRecorder) WithGroup(string) slog.Handler      { return r }

// Only an async delivery is followed by a dead letter; an inline one reports
// its failure to the caller, so it must not be demoted to Warn.
func TestInlineFailureOnADeadLetteredDestinationLogsAtError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(srv.Close)
	rec := &levelRecorder{levels: map[string]slog.Level{}}
	pub, err := NewWebhookPublisher(config.WebhookConfig{
		URL: srv.URL, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: config.RetryPolicyConfig{MaxAttempts: 1}, DeadLetter: config.WebhookDeadLetterConfig{Destination: "dlq", Target: "dead"},
	}, "notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 1, Logger: slog.New(rec)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	_ = pub.Publish(WithInlineDelivery(context.Background()), "notify", "orders", Message{ID: "evt-1"})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var found bool
	for msg, level := range rec.levels {
		if strings.Contains(msg, "webhook delivery") && strings.Contains(msg, "fail") {
			found = true
			if level != slog.LevelError {
				t.Fatalf("%q logged at %v, want Error", msg, level)
			}
		}
	}
	if !found {
		t.Fatalf("no terminal failure log; saw %v", rec.levels)
	}
}
