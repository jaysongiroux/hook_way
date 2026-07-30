package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/replaystore"
)

// fakeReplayCapturer is a test double for ReplayCapturer: it records every
// Capture/RecordOutcome call and can be configured to return an error or
// panic instead, to exercise the best-effort discipline in handleWebhook.
type fakeReplayCapturer struct {
	mu sync.Mutex

	captureErr   error
	capturePanic bool
	captured     []replaystore.Event

	outcomeErr   error
	outcomePanic bool
	outcomes     []recordedOutcome

	// captureStarted, if non-nil, is closed the instant Capture is invoked
	// (before it blocks on captureRelease), so a test can deterministically
	// wait for capture to have begun instead of guessing with a sleep.
	captureStarted chan struct{}
	// captureRelease, if non-nil, is read from (blocking) before Capture
	// returns, letting a test hold the capture call open for as long as it
	// likes without a fixed sleep duration inside the fake itself.
	captureRelease chan struct{}
}

type recordedOutcome struct {
	id         string
	deliveries []replaystore.Delivery
}

func (f *fakeReplayCapturer) Capture(ctx context.Context, e replaystore.Event) error {
	if f.captureStarted != nil {
		close(f.captureStarted)
	}
	if f.captureRelease != nil {
		<-f.captureRelease
	}
	if f.capturePanic {
		panic("capture panic")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.captureErr != nil {
		return f.captureErr
	}
	f.captured = append(f.captured, e)
	return nil
}

func (f *fakeReplayCapturer) RecordOutcome(ctx context.Context, id string, deliveries []replaystore.Delivery) error {
	if f.outcomePanic {
		panic("outcome panic")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.outcomeErr != nil {
		return f.outcomeErr
	}
	f.outcomes = append(f.outcomes, recordedOutcome{id: id, deliveries: deliveries})
	return nil
}

func (f *fakeReplayCapturer) capturedEvents() []replaystore.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]replaystore.Event, len(f.captured))
	copy(out, f.captured)
	return out
}

func (f *fakeReplayCapturer) recordedOutcomes() []recordedOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedOutcome, len(f.outcomes))
	copy(out, f.outcomes)
	return out
}

func TestCaptureHappensAfterAuthWithEnvelope(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	captured := fake.capturedEvents()
	if len(captured) != 1 {
		t.Fatalf("captured = %#v, want 1 event", captured)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	published := pub.published[0].msg
	if captured[0].ID != published.ID {
		t.Fatalf("captured.ID = %q, published.ID = %q", captured[0].ID, published.ID)
	}
	if captured[0].Endpoint != "paddle_billing" {
		t.Fatalf("captured.Endpoint = %q", captured[0].Endpoint)
	}
	// The stored envelope must match the published queue.Message field-for-field
	// (aside from the per-route DestinationQueueID/Queue, which envelopeToMessage
	// leaves at the endpoint-level value rather than the per-route override).
	wantEnvelope := published
	wantEnvelope.DestinationQueueID = captured[0].Envelope.DestinationQueueID
	wantEnvelope.Queue = captured[0].Envelope.Queue
	if !reflectEqualMessage(captured[0].Envelope, wantEnvelope) {
		t.Fatalf("captured envelope = %#v, want %#v", captured[0].Envelope, wantEnvelope)
	}

	outcomes := fake.recordedOutcomes()
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %#v, want 1", outcomes)
	}
	if outcomes[0].id != published.ID {
		t.Fatalf("outcome id = %q, want %q", outcomes[0].id, published.ID)
	}
	if len(outcomes[0].deliveries) != 1 || outcomes[0].deliveries[0].Outcome != replaystore.OutcomeDelivered {
		t.Fatalf("deliveries = %#v", outcomes[0].deliveries)
	}
	if outcomes[0].deliveries[0].DestinationID != "billing" || outcomes[0].deliveries[0].Target != "billing-events" {
		t.Fatalf("delivery destination/target = %#v", outcomes[0].deliveries[0])
	}
}

// reflectEqualMessage compares the JSON-relevant fields of two queue.Message
// values via marshaling, sidestepping http.Header/map ordering concerns.
func reflectEqualMessage(a, b queue.Message) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

func TestCaptureBestEffortInsertFailureStill202(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)
	fake := &fakeReplayCapturer{captureErr: errors.New("insert failed")}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want event still delivered", pub.published)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_replay_capture_failures_total Replay store capture/outcome-update failures (including recovered panics), by operation.
# TYPE hookway_replay_capture_failures_total counter
hookway_replay_capture_failures_total{op="insert"} 1
`), "hookway_replay_capture_failures_total"); err != nil {
		t.Fatal(err)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_replay_captured_total Webhook events captured into the replay store.
# TYPE hookway_replay_captured_total counter
`), "hookway_replay_captured_total"); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureBestEffortPanicStill202(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)
	fake := &fakeReplayCapturer{capturePanic: true}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_replay_capture_failures_total Replay store capture/outcome-update failures (including recovered panics), by operation.
# TYPE hookway_replay_capture_failures_total counter
hookway_replay_capture_failures_total{op="insert"} 1
`), "hookway_replay_capture_failures_total"); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureBestEffortOutcomeFailureStill202(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)
	fake := &fakeReplayCapturer{outcomeErr: errors.New("update failed")}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_replay_capture_failures_total Replay store capture/outcome-update failures (including recovered panics), by operation.
# TYPE hookway_replay_capture_failures_total counter
hookway_replay_capture_failures_total{op="update"} 1
`), "hookway_replay_capture_failures_total"); err != nil {
		t.Fatal(err)
	}
	if len(fake.capturedEvents()) != 1 {
		t.Fatalf("captured events = %#v, want the insert to still have happened", fake.capturedEvents())
	}
}

func TestCaptureBestEffortOutcomePanicStill202(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)
	fake := &fakeReplayCapturer{outcomePanic: true}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_replay_capture_failures_total Replay store capture/outcome-update failures (including recovered panics), by operation.
# TYPE hookway_replay_capture_failures_total counter
hookway_replay_capture_failures_total{op="update"} 1
`), "hookway_replay_capture_failures_total"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordOutcomeReflectsSkipFailedPartial(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("activity down"), errDestination: "activity"}
	srv := NewServer(cfg, pub, slog.Default())
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	outcomes := fake.recordedOutcomes()
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %#v, want 1", outcomes)
	}
	byDest := map[string]replaystore.Delivery{}
	for _, d := range outcomes[0].deliveries {
		byDest[d.DestinationID] = d
	}
	if len(byDest) != 2 {
		t.Fatalf("deliveries = %#v, want 2", outcomes[0].deliveries)
	}
	if d := byDest["billing"]; d.Outcome != replaystore.OutcomeDelivered || d.Target != "billing-events" {
		t.Fatalf("billing delivery = %#v", d)
	}
	if d := byDest["activity"]; d.Outcome != replaystore.OutcomeFailed || d.Target != "activity-events" {
		t.Fatalf("activity delivery = %#v", d)
	}
}

// TestRecordOutcomeSkippedWhenNoDeliveries covers the stop_on_failure
// full-failure path: publishRoutesStopOnFailure returns nil, nil, err, so
// deliveriesFromResults(nil, nil) is an empty slice. Issuing the outcome
// UPDATE in that case is a wasted best-effort write (the row is already
// "pending" and there is nothing to record), so RecordOutcome must not be
// called at all -- not called-with-empty-deliveries, but simply skipped.
func TestRecordOutcomeSkippedWhenNoDeliveries(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("billing down"), errDestination: "billing"}
	srv := NewServer(cfg, pub, slog.Default())
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(fake.capturedEvents()) != 1 {
		t.Fatalf("captured = %#v, want 1 (the event is still captured even on full-failure)", fake.capturedEvents())
	}
	if outcomes := fake.recordedOutcomes(); len(outcomes) != 0 {
		t.Fatalf("outcomes = %#v, want none -- an empty-deliveries outcome UPDATE must be skipped entirely", outcomes)
	}
}

func TestSkipUnauthenticatedNoAuthEndpointNotCaptured(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true, SkipUnauthenticated: true}
	cfg.Endpoints["open_webhook"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/open",
		Destinations: []config.EndpointDestinationConfig{{
			Destination: "billing", Target: "open-events",
		}},
		Auth: config.AuthConfig{Type: "no_auth", Scheme: "no_auth"},
	}
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)

	// The no_auth endpoint: must not be captured.
	openReq := httptest.NewRequest(http.MethodPost, "/webhooks/open", bytes.NewReader([]byte(`{"a":1}`)))
	openRec := httptest.NewRecorder()
	srv.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusAccepted {
		t.Fatalf("open status = %d body = %s", openRec.Code, openRec.Body.String())
	}

	// The verified endpoint: must be captured.
	body := []byte(`{"event_type":"subscription.created"}`)
	verifiedReq := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	verifiedReq.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	verifiedRec := httptest.NewRecorder()
	srv.ServeHTTP(verifiedRec, verifiedReq)
	if verifiedRec.Code != http.StatusAccepted {
		t.Fatalf("verified status = %d body = %s", verifiedRec.Code, verifiedRec.Body.String())
	}

	captured := fake.capturedEvents()
	if len(captured) != 1 {
		t.Fatalf("captured = %#v, want exactly 1 (the verified endpoint only)", captured)
	}
	if captured[0].Endpoint != "paddle_billing" {
		t.Fatalf("captured endpoint = %q, want paddle_billing", captured[0].Endpoint)
	}
}

func TestReplayNilCapturerNoop(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	// Never call SetReplayCapturer: s.replay stays nil.

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
}

// TestFullyFilteredEndpointNotCaptured covers the endpoint-level filter chain:
// it drops the request before activeRoutes (and the capture code) is ever
// reached, via the silent-drop response path.
func TestFullyFilteredEndpointNotCaptured(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Filters = []config.FilterConditionConfig{
		{Source: "body", Path: "event_type", Operator: "eq", Value: "never_matches"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(fake.capturedEvents()) != 0 {
		t.Fatalf("captured = %#v, want none (fully filtered at endpoint level)", fake.capturedEvents())
	}
}

// TestAllRoutesFilteredNotCaptured covers the len(activeRoutes) > 0 guard
// directly: the endpoint-level filter passes (the request is not silently
// dropped), but every route-level filter fails, so activeRoutes is empty by
// the time handleWebhook reaches the capture INSERT. Nothing would be
// delivered, so nothing should be captured.
func TestAllRoutesFilteredNotCaptured(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{
			Destination: "billing", Target: "billing-events",
			Filters: []config.FilterConditionConfig{
				{Source: "body", Path: "event_type", Operator: "eq", Value: "never_matches"},
			},
		},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want none (route filtered out)", pub.published)
	}
	if len(fake.capturedEvents()) != 0 {
		t.Fatalf("captured = %#v, want none (activeRoutes empty)", fake.capturedEvents())
	}
	if len(fake.recordedOutcomes()) != 0 {
		t.Fatalf("outcomes = %#v, want none (activeRoutes empty)", fake.recordedOutcomes())
	}
}

// deadlineAwarePublisher records whether the context it was actually invoked
// with was already past its deadline, and fails the publish if so (mirroring
// how a real client -- an amqp channel, an HTTP RoundTripper -- reacts to a
// context whose deadline has already elapsed). It always succeeds otherwise.
// Used to prove, functionally, whether the publish budget survived a slow
// upstream call that ran before it.
type deadlineAwarePublisher struct {
	mu      sync.Mutex
	invoked bool
	ctxErr  error
}

func (p *deadlineAwarePublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	p.mu.Lock()
	p.invoked = true
	p.ctxErr = ctx.Err()
	p.mu.Unlock()
	return ctx.Err()
}
func (p *deadlineAwarePublisher) Ready(ctx context.Context) error { return nil }
func (p *deadlineAwarePublisher) Close() error                    { return nil }

func (p *deadlineAwarePublisher) snapshot() (invoked bool, ctxErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.invoked, p.ctxErr
}

// TestSlowCaptureDoesNotConsumePublishBudget covers the finding that
// best-effort replay capture must never share its deadline with the publish
// fan-out: a capture INSERT that runs slowly (but still succeeds) must not
// be able to eat into server.publish_timeout and turn a would-be-successful
// publish into a spurious 503.
//
// PublishTimeout is configured very short (50ms) and the fake capturer is
// held open on a channel well past that window before the test releases it.
// Under the pre-fix code (pubCtx shared by capture and publish, created
// before the capture call) that elapsed wall-clock time comes straight out of
// pubCtx's own budget, so by the time publishRoutes runs, pubCtx is already
// expired -- deadlineAwarePublisher observes ctx.Err() != nil and the
// handler responds 503. After the fix, capture runs on its own
// replayCaptureTimeout budget and pubCtx isn't created until after capture
// returns, so publishRoutes always gets the full, fresh PublishTimeout
// window: ctx.Err() == nil and the handler responds 202.
func TestSlowCaptureDoesNotConsumePublishBudget(t *testing.T) {
	cfg := testConfig()
	cfg.Replay = config.ReplayConfig{Enabled: true}
	cfg.Server.PublishTimeout = 50 * time.Millisecond

	captureStarted := make(chan struct{})
	captureRelease := make(chan struct{})
	fake := &fakeReplayCapturer{captureStarted: captureStarted, captureRelease: captureRelease}

	pub := &deadlineAwarePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	srv.SetReplayCapturer(fake)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-captureStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("capture was never invoked")
	}

	// Hold capture open well past PublishTimeout (50ms) so, under the bug,
	// pubCtx's own deadline (ticking since its creation) genuinely elapses in
	// wall-clock time while capture still has the request blocked.
	time.Sleep(200 * time.Millisecond)
	close(captureRelease)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never returned after capture was released")
	}

	invoked, ctxErr := pub.snapshot()
	if !invoked {
		t.Fatal("publisher was never invoked; capture must have blocked the publish entirely")
	}
	if ctxErr != nil {
		t.Fatalf("publish saw ctx.Err() = %v, want nil (a slow capture must not consume the publish deadline)", ctxErr)
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202 (slow-but-successful capture must not turn a good publish into a 503)", rec.Code, rec.Body.String())
	}
}
