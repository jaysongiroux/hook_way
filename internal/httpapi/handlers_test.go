package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/queue"
)

func TestWebhookSuccessReturnsAccepted(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
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
	if pub.published[0].destinationID != "billing" {
		t.Fatalf("destination = %q", pub.published[0].destinationID)
	}
	if pub.published[0].msg.DestinationQueueID != "billing" {
		t.Fatalf("message destination = %q", pub.published[0].msg.DestinationQueueID)
	}
	var res struct {
		Status       string `json:"status"`
		Endpoint     string `json:"endpoint"`
		EventID      string `json:"event_id"`
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
			Queue              string `json:"queue"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "accepted" || res.Endpoint != "paddle_billing" || res.EventID == "" {
		t.Fatalf("bad response: %#v", res)
	}
	if len(res.Destinations) != 1 || res.Destinations[0].DestinationQueueID != "billing" || res.Destinations[0].Queue != "billing-events" {
		t.Fatalf("bad response: %#v", res)
	}
}

func TestWebhookRoutesToEndpointDestination(t *testing.T) {
	cfg := testConfig()
	cfg.Endpoints["github_events"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/github",
		Destinations: []config.EndpointDestinationConfig{{
			Destination: "activity", Target: "github-events",
		}},
		Auth: config.AuthConfig{Type: "hmac_sha256", Scheme: "hmac_sha256_hex", Secret: "secret", SignatureHeader: "X-Hub-Signature-256"},
	}
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"zen":"keep it logically awesome"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", hmacSHA256HexSigned("secret", body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if pub.published[0].destinationID != "activity" || pub.published[0].queue != "github-events" {
		t.Fatalf("publish target = %q/%q", pub.published[0].destinationID, pub.published[0].queue)
	}
}

func TestWebhookFansOutToEndpointDestinations(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 2 {
		t.Fatalf("published = %#v", pub.published)
	}
	byDestination := map[string]publishedMessage{}
	for _, message := range pub.published {
		byDestination[message.destinationID] = message
	}
	if byDestination["billing"].queue != "billing-events" || byDestination["billing"].msg.Queue != "billing-events" {
		t.Fatalf("billing publish = %#v", byDestination["billing"])
	}
	if byDestination["activity"].queue != "activity-events" || byDestination["activity"].msg.Queue != "activity-events" {
		t.Fatalf("activity publish = %#v", byDestination["activity"])
	}
	var res struct {
		Status       string `json:"status"`
		Endpoint     string `json:"endpoint"`
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
			Queue              string `json:"queue"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 2 || res.Destinations[1].DestinationQueueID != "activity" || res.Destinations[1].Queue != "activity-events" {
		t.Fatalf("bad response: %#v", res)
	}
}

func TestWebhookFanoutFailureReturnsUnavailableAndAllowsRetry(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("activity down"), errDestination: "activity"}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`true`)
	sig := signed("secret", time.Now(), body)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.Header.Set("Paddle-Signature", sig)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d status = %d body = %s", i, rec.Code, rec.Body.String())
		}
	}
	if len(pub.published) != 4 {
		t.Fatalf("published = %#v, want 4 attempts", pub.published)
	}
	successes, failures := 0, 0
	for _, published := range pub.published {
		switch published.destinationID {
		case "billing":
			if published.err != nil {
				t.Fatalf("billing publish failed: %#v", published)
			}
			successes++
		case "activity":
			if !errors.Is(published.err, pub.err) {
				t.Fatalf("activity publish = %#v", published)
			}
			failures++
		default:
			t.Fatalf("unexpected publish = %#v", published)
		}
	}
	if successes != 2 || failures != 2 {
		t.Fatalf("successes=%d failures=%d published=%#v", successes, failures, pub.published)
	}
}

func TestWebhookDefaultStopOnFailureDoesNotAttemptLaterDestinations(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("billing down"), errDestination: "billing"}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want only first destination attempted", pub.published)
	}
	if pub.published[0].destinationID != "billing" {
		t.Fatalf("published destination = %q", pub.published[0].destinationID)
	}
}

func TestWebhookSkipFailedReturnsAcceptedWithFailures(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("activity down"), errDestination: "activity"}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Status       string `json:"status"`
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
			Queue              string `json:"queue"`
		} `json:"destinations"`
		FailedDestinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
			Queue              string `json:"queue"`
		} `json:"failed_destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "accepted" || len(res.Destinations) != 1 || res.Destinations[0].DestinationQueueID != "billing" {
		t.Fatalf("bad successful destinations: %#v", res)
	}
	if len(res.FailedDestinations) != 1 || res.FailedDestinations[0].DestinationQueueID != "activity" {
		t.Fatalf("bad failed destinations: %#v", res)
	}
}

func TestWebhookSkipFailedReturnsUnavailableWhenAllDestinationsFail(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("all down")}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 2 {
		t.Fatalf("published = %#v, want every destination attempted", pub.published)
	}
}

// TestWebhookSkipFailedPartialSuccessReleasesReplayReservation covers B1: a
// partial-failure 202 (some destinations failed under skip_failed) must
// release the replay reservation, so an identical sender retry is accepted
// again rather than rejected as a replay — otherwise the failed destinations
// could never receive the event. This mirrors stop_on_failure's existing
// retry semantics; consumers already dedupe replays of already-published
// destinations via idempotency_key.
func TestWebhookSkipFailedPartialSuccessReleasesReplayReservation(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("activity down"), errDestination: "activity"}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`true`)
	sig := signed("secret", time.Now(), body)

	for i, want := range []int{http.StatusAccepted, http.StatusAccepted} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.Header.Set("Paddle-Signature", sig)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d want %d body = %s", i, rec.Code, want, rec.Body.String())
		}
	}
}

// TestWebhookSkipFailedFullSuccessKeepsReplayReservation is the counterpart
// to the partial-failure case above: when every destination succeeds under
// skip_failed, the replay reservation is kept (unchanged), so an identical
// retry is rejected as a replay.
func TestWebhookSkipFailedFullSuccessKeepsReplayReservation(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`true`)
	sig := signed("secret", time.Now(), body)

	for i, want := range []int{http.StatusAccepted, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.Header.Set("Paddle-Signature", sig)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d want %d body = %s", i, rec.Code, want, rec.Body.String())
		}
	}
}

func TestWebhookPublishFailureLogsUnderlyingError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	pub := &fakePublisher{err: errors.New("rabbitmq queue declare: PRECONDITION_FAILED")}
	srv := NewServer(testConfig(), pub, logger)
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	req.Header.Set("X-Request-ID", "req-paddle")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	logText := logs.String()
	// Per-route log uses canonical keys and carries the error.
	for _, want := range []string{
		`"msg":"publish route failed"`,
		`"endpoint":"paddle_billing"`,
		`"destination":"billing"`,
		`"target":"billing-events"`,
		`"request_id":"req-paddle"`,
		`rabbitmq queue declare: PRECONDITION_FAILED`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %s in %s", want, logText)
		}
	}
}

func TestWebhookFanoutFailureDoesNotMaskRootCauseWithContextCanceled(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &contextCancelPublisher{rootErr: errors.New("rabbitmq queue declare: PRECONDITION_FAILED")}
	srv := NewServer(cfg, pub, logger)
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	// The root-cause error is logged in the per-route "publish route failed" line,
	// not in the aggregate "request publish failed; returning 503" line.
	logText := logs.String()
	var perRouteErr string
	for _, line := range strings.Split(strings.TrimSpace(logText), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log line: %v: %s", err, line)
		}
		if entry["msg"] == "publish route failed" && entry["destination"] == "billing" {
			perRouteErr, _ = entry["error"].(string)
		}
	}
	if perRouteErr != "rabbitmq queue declare: PRECONDITION_FAILED" {
		t.Fatalf("per-route billing error = %q, logs: %s", perRouteErr, logText)
	}
}

func TestWebhookFanoutHonorsConcurrencyLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Server.FanoutConcurrency = 1
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{delay: 10 * time.Millisecond}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if pub.maxConcurrent != 1 {
		t.Fatalf("max concurrent publishes = %d, want 1", pub.maxConcurrent)
	}
}

// TestWebhookPublishSurvivesRequestContextCancellation covers B4: publishing
// is detached from the inbound request context, so a sender disconnect (the
// request context being canceled) mid-publish must not cancel or fail the
// publish. The fake publisher blocks until the *original* request context is
// canceled and then reports whether the context it was actually called with
// (the detached publish context) was itself canceled or expired.
func TestWebhookPublishSurvivesRequestContextCancellation(t *testing.T) {
	cfg := testConfig()
	reqCtx, cancelReq := context.WithCancel(context.Background())
	pub := &blockUntilClosedPublisher{unblock: reqCtx.Done()}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body)).WithContext(reqCtx)
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(rec, req)
		close(done)
	}()

	// Give the handler time to reach Publish (and start blocking on the
	// request context) before simulating the sender disconnecting.
	time.Sleep(20 * time.Millisecond)
	cancelReq()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request context was canceled")
	}

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202 (publish should survive request cancellation)", rec.Code, rec.Body.String())
	}
	if pub.sawCanceledCtx.Load() {
		t.Fatal("publish observed a canceled context; the publish context must be detached from the request context")
	}
}

// TestWebhookPublishTimeoutReturnsUnavailable covers B4's independent bound:
// even though the publish context is detached from the request, it is still
// governed by server.publish_timeout. A publisher that blocks longer than a
// short configured timeout must cause the publish to fail with
// context.DeadlineExceeded and the handler to respond 503.
func TestWebhookPublishTimeoutReturnsUnavailable(t *testing.T) {
	cfg := testConfig()
	cfg.Server.PublishTimeout = 20 * time.Millisecond
	pub := &deadlinePublisher{delay: 500 * time.Millisecond}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	if !errors.Is(pub.lastErr(), context.DeadlineExceeded) {
		t.Fatalf("publish error = %v, want context.DeadlineExceeded", pub.lastErr())
	}
}

func TestRequestIDPropagatesToResponseEnvelopeAndLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, logger)
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("X-Request-ID", "req_abc")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "req_abc" {
		t.Fatalf("response request id = %q", rec.Header().Get("X-Request-ID"))
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if pub.published[0].msg.RequestID != "req_abc" {
		t.Fatalf("published request id = %q", pub.published[0].msg.RequestID)
	}
	if pub.published[0].msg.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("published trace id = %q", pub.published[0].msg.TraceID)
	}
	if !strings.Contains(logs.String(), `"request_id":"req_abc"`) || !strings.Contains(logs.String(), `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`) {
		t.Fatalf("logs missing ids: %s", logs.String())
	}
}

// TestInboundTraceparentPropagatesByteIdenticalToPublishedMessage covers C4:
// a well-formed inbound Traceparent header must surface byte-identical on the
// published queue.Message (which the webhook engine later stamps onto the
// outbound request in both envelope and proxy modes).
func TestInboundTraceparentPropagatesByteIdenticalToPublishedMessage(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req.Header.Set("Traceparent", want)
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	if pub.published[0].msg.Traceparent != want {
		t.Fatalf("published traceparent = %q, want byte-identical %q", pub.published[0].msg.Traceparent, want)
	}
}

// TestAbsentInboundTraceparentSynthesizesValidOneMatchingTraceID covers C4:
// when no inbound Traceparent is present, the published message must still
// carry a syntactically valid one whose trace-id component matches the
// envelope's derived trace id (so envelope.trace_id and the traceparent stay
// consistent for whoever correlates the two).
func TestAbsentInboundTraceparentSynthesizesValidOneMatchingTraceID(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	msg := pub.published[0].msg
	parts := strings.Split(msg.Traceparent, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || parts[3] != "01" {
		t.Fatalf("synthesized traceparent %q is not syntactically valid", msg.Traceparent)
	}
	if parts[1] != msg.TraceID {
		t.Fatalf("synthesized traceparent trace-id %q != envelope trace id %q", parts[1], msg.TraceID)
	}
}

// TestMalformedInboundTraceparentIsSynthesizedNotPropagated covers C4: a
// garbled inbound Traceparent must not be forwarded verbatim (a downstream
// system should never receive a header that fails to parse); Hookway must
// synthesize a fresh, valid one instead.
func TestMalformedInboundTraceparentIsSynthesizedNotPropagated(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	garbage := "not-a-traceparent"
	req.Header.Set("Traceparent", garbage)
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	msg := pub.published[0].msg
	if msg.Traceparent == garbage {
		t.Fatalf("malformed inbound traceparent must not be propagated verbatim")
	}
	parts := strings.Split(msg.Traceparent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 || len(parts[2]) != 16 {
		t.Fatalf("synthesized traceparent %q is not syntactically valid", msg.Traceparent)
	}
}

// TestNonHexInboundTraceIDIsNotAdoptedForSynthesizedTraceparent covers C4: a
// crafted inbound Traceparent whose trace-id segment is the right length (32
// chars) but not valid hex must not be adopted verbatim into the synthesized
// outbound traceparent — doing so would produce a "00-<non-hex>-<span>-01"
// header that violates W3C well-formedness even though Hookway's own
// isValidTraceparent check correctly rejected the inbound header for
// propagation. The trace id used for synthesis (and stamped on the envelope)
// must be valid lowercase hex, and the two must still agree with each other.
func TestNonHexInboundTraceIDIsNotAdoptedForSynthesizedTraceparent(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	nonHexTraceID := strings.Repeat("g", 32)
	req.Header.Set("Traceparent", "00-"+nonHexTraceID+"-00f067aa0ba902b7-01")
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	msg := pub.published[0].msg
	parts := strings.Split(msg.Traceparent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 {
		t.Fatalf("synthesized traceparent %q is not syntactically valid", msg.Traceparent)
	}
	if !isHex(parts[1]) {
		t.Fatalf("synthesized traceparent trace-id %q is not valid hex", parts[1])
	}
	if parts[1] == nonHexTraceID {
		t.Fatalf("synthesized traceparent must not adopt the non-hex inbound trace id verbatim")
	}
	if parts[1] != msg.TraceID {
		t.Fatalf("synthesized traceparent trace-id %q != envelope trace id %q", parts[1], msg.TraceID)
	}
}

func TestReadyEndpointsServeCachedResultAndPublicErrorIsGeneric(t *testing.T) {
	var logs bytes.Buffer
	pub := &fakePublisher{readyErr: errors.New("rabbitmq dial tcp secret-url failed")}
	srv := NewServer(testConfig(), pub, slog.New(slog.NewJSONHandler(&logs, nil)))
	// Seed the cached readiness result the prober would maintain.
	srv.readiness.probeOnce(context.Background())
	for _, path := range []string{"/ready", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "rabbitmq") || !strings.Contains(rec.Body.String(), "not ready") {
			t.Fatalf("%s leaked readiness detail: %s", path, rec.Body.String())
		}
	}
	if !strings.Contains(logs.String(), "rabbitmq dial tcp") {
		t.Fatalf("internal readiness reason not logged: %s", logs.String())
	}
	// /health and /healthz are liveness aliases: alive even when not ready.
	for _, path := range []string{"/health", "/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want liveness OK while not ready", path, rec.Code)
		}
	}
}

func TestEndpointBodyLimitRateLimitIPAllowlistAndReplay(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.MaxBodyBytes = 4
	endpoint.IPAllowlist = []string{"203.0.113.0/24"}
	rateEnabled := true
	endpoint.RateLimit = config.EndpointRateLimitConfig{Enabled: &rateEnabled, RequestsPerMinute: 1, Burst: 1, KeyBy: "ip"}
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader([]byte("12345")))
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), []byte("12345")))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status = %d", rec.Code)
	}

	body := []byte(`true`)
	req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.1:1234"
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("allowlist status = %d", rec.Code)
	}

	sig := signed("secret", time.Now(), body)
	for i, want := range []int{http.StatusAccepted, http.StatusTooManyRequests} {
		req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.10:1234"
		req.Header.Set("Paddle-Signature", sig)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("rate request %d status = %d want %d", i, rec.Code, want)
		}
	}

	cfg = testConfig()
	endpoint = cfg.Endpoints["paddle_billing"]
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint
	srv = NewServer(cfg, &fakePublisher{}, slog.Default())
	sig = signed("secret", time.Now(), body)
	for i, want := range []int{http.StatusAccepted, http.StatusConflict} {
		req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.11:1234"
		req.Header.Set("Paddle-Signature", sig)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("replay request %d status = %d want %d", i, rec.Code, want)
		}
	}
}

func TestWebhookStatusCodes(t *testing.T) {
	body := []byte(`{"ok":true}`)
	tests := map[string]struct {
		method string
		path   string
		header string
		body   []byte
		pubErr error
		want   int
	}{
		"invalid method":    {http.MethodGet, "/webhooks/paddle/billing", "", body, nil, http.StatusMethodNotAllowed},
		"unknown endpoint":  {http.MethodPost, "/unknown/billing-events", signed("secret", time.Now(), body), body, nil, http.StatusNotFound},
		"invalid signature": {http.MethodPost, "/webhooks/paddle/billing", "bad", body, nil, http.StatusUnauthorized},
		"publish failure":   {http.MethodPost, "/webhooks/paddle/billing", signed("secret", time.Now(), body), body, errors.New("down"), http.StatusServiceUnavailable},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			pub := &fakePublisher{err: tt.pubErr}
			srv := NewServer(testConfig(), pub, slog.Default())
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(tt.body))
			req.Header.Set("Paddle-Signature", tt.header)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d want %d body = %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestWebhookRejectsOversizedAndRateLimited(t *testing.T) {
	cfg := testConfig()
	cfg.Server.MaxBodyBytes = 4
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader([]byte("12345")))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), []byte("12345")))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}

	cfg = testConfig()
	cfg.RateLimit.Enabled = true
	cfg.RateLimit.RequestsPerMinute = 1
	cfg.RateLimit.Burst = 1
	srv = NewServer(cfg, &fakePublisher{}, slog.Default())
	body := []byte(`{"ok":true}`)
	for i, want := range []int{http.StatusAccepted, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d want %d", i, rec.Code, want)
		}
	}
}

func TestHealthReadyAndDisabledMetrics(t *testing.T) {
	cfg := testConfig()
	cfg.Metrics.Enabled = false
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	srv.readiness.probeOnce(context.Background())
	for path, want := range map[string]int{"/health": 200, "/healthz": 200, "/ready": 200, "/readyz": 200, "/metrics": 404} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s status = %d want %d", path, rec.Code, want)
		}
	}
}

type publishedMessage struct {
	destinationID string
	queue         string
	msg           queue.Message
	err           error
}

type fakePublisher struct {
	err            error
	errDestination string
	readyErr       error
	delay          time.Duration
	mu             sync.Mutex
	active         int
	maxConcurrent  int
	published      []publishedMessage
}

func (f *fakePublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	f.mu.Lock()
	f.active++
	if f.active > f.maxConcurrent {
		f.maxConcurrent = f.active
	}
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if f.err != nil && (f.errDestination == "" || f.errDestination == destinationID) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.published = append(f.published, publishedMessage{destinationID: destinationID, queue: q, msg: msg, err: f.err})
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, publishedMessage{destinationID: destinationID, queue: q, msg: msg})
	return nil
}
func (f *fakePublisher) Ready(ctx context.Context) error { return f.readyErr }
func (f *fakePublisher) Close() error                    { return nil }

type contextCancelPublisher struct {
	rootErr error
}

func (p *contextCancelPublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	if destinationID == "billing" {
		return p.rootErr
	}
	<-ctx.Done()
	return ctx.Err()
}

func (p *contextCancelPublisher) Ready(ctx context.Context) error { return nil }
func (p *contextCancelPublisher) Close() error                    { return nil }

// blockUntilClosedPublisher blocks until unblock is closed, then reports
// success unless the context it was actually invoked with was itself
// canceled or expired by that point. Used to prove a publish context is (or
// is not) detached from some other context the test controls independently.
type blockUntilClosedPublisher struct {
	unblock        <-chan struct{}
	sawCanceledCtx atomic.Bool
}

func (p *blockUntilClosedPublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	<-p.unblock
	if ctx.Err() != nil {
		p.sawCanceledCtx.Store(true)
		return ctx.Err()
	}
	return nil
}
func (p *blockUntilClosedPublisher) Ready(ctx context.Context) error { return nil }
func (p *blockUntilClosedPublisher) Close() error                    { return nil }

// deadlinePublisher sleeps for delay, or returns earlier via ctx.Done() if
// the context governing the publish expires first. Used to exercise
// server.publish_timeout.
type deadlinePublisher struct {
	delay time.Duration
	mu    sync.Mutex
	err   error
}

func (p *deadlinePublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	var err error
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		err = ctx.Err()
	}
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	return err
}
func (p *deadlinePublisher) Ready(ctx context.Context) error { return nil }
func (p *deadlinePublisher) Close() error                    { return nil }
func (p *deadlinePublisher) lastErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// failingPublisher always returns an error from Publish.
type failingPublisher struct{ err error }

func (f failingPublisher) Publish(_ context.Context, _, _ string, _ queue.Message) error {
	return f.err
}
func (f failingPublisher) Ready(_ context.Context) error { return nil }
func (f failingPublisher) Close() error                  { return nil }

// findLogLine scans buf for a JSON log line whose "msg" field equals msgKey
// and returns it as map[string]any. Fatals if not found.
func findLogLine(t *testing.T, buf *bytes.Buffer, msgKey string) map[string]any {
	t.Helper()
	for _, raw := range strings.Split(buf.String(), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		if m["msg"] == msgKey {
			return m
		}
	}
	t.Fatalf("log line with msg=%q not found in:\n%s", msgKey, buf.String())
	return nil
}

func TestPublishFailureLogUsesCanonicalKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pub := failingPublisher{err: errors.New("boom")}
	srv := NewServer(testConfig(), pub, logger)

	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	line := findLogLine(t, &buf, "publish route failed")
	if line["destination"] == nil || line["target"] == nil || line["event_id"] == nil {
		t.Fatalf("missing canonical keys; got %v", line)
	}
	if _, ok := line["destination_queue_id"]; ok {
		t.Fatalf("legacy key destination_queue_id must be gone; got %v", line)
	}
	if _, ok := line["queue"]; ok {
		t.Fatalf("legacy key queue must be gone; got %v", line)
	}
	if line["error"] == nil {
		t.Fatalf("error field missing; got %v", line)
	}
}

func TestNewServerWarnsWhenEndpointUsesNoAuth(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	cfg := testConfig()
	cfg.Endpoints["open_webhook"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/open",
		Destinations: []config.EndpointDestinationConfig{{
			Destination: "billing", Target: "open-events",
		}},
		Auth: config.AuthConfig{Type: "no_auth", Scheme: "no_auth"},
	}

	NewServer(cfg, &fakePublisher{}, logger)

	if !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("expected a warning log, got: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"endpoint":"open_webhook"`) {
		t.Fatalf("warning should name the endpoint, got: %s", logs.String())
	}
}

func TestNewServerDoesNotWarnWithoutNoAuth(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	NewServer(testConfig(), &fakePublisher{}, logger)

	if strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("did not expect a warning log, got: %s", logs.String())
	}
}

func testConfig() config.Config {
	return config.Config{
		Server:    config.ServerConfig{Addr: ":0", MaxBodyBytes: 1024, PublishTimeout: 10 * time.Second},
		Security:  config.SecurityConfig{RequestIDHeader: "X-Request-ID"},
		RateLimit: config.RateLimitConfig{Enabled: false, RequestsPerMinute: 120, Burst: 20, KeyBy: "ip"},
		Metrics:   config.MetricsConfig{Enabled: true, Path: "/metrics"},
		Destinations: map[string]config.DestinationConfig{
			"billing":  {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://guest:guest@localhost:5672/", Exchange: "hookway.billing", ExchangeType: "direct", PublishTimeout: time.Second}},
			"activity": {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://guest:guest@localhost:5673/", Exchange: "hookway.activity", ExchangeType: "direct", PublishTimeout: time.Second}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {
				Enabled: true, Path: "/webhooks/paddle/billing",
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "billing", Target: "billing-events",
				}},
				Auth: config.AuthConfig{Type: "hmac_sha256", Secret: "secret", SignatureHeader: "Paddle-Signature", TimestampTolerance: 5 * time.Minute},
			},
		},
	}
}

func TestServerAcceptsValidHMACSHA256TSDotSignatureAndRejectsTampered(t *testing.T) {
	cfg := testConfig()
	cfg.Endpoints["stripe_billing"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/stripe/billing",
		Destinations: []config.EndpointDestinationConfig{{
			Destination: "billing", Target: "stripe-events",
		}},
		Auth: config.AuthConfig{Type: "hmac_sha256", Scheme: "hmac_sha256_ts_dot", Secret: "whsec", SignatureHeader: "Stripe-Signature", TimestampTolerance: 5 * time.Minute},
	}
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	body := []byte(`{"id":"evt_1"}`)
	now := time.Now()
	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write([]byte(fmt.Sprintf("%d.%s", now.Unix(), body)))
	sig := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))

	// Valid signature → accepted.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/billing", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", sig)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("valid hmac_sha256_ts_dot request status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Tampered body → rejected.
	bad := httptest.NewRequest(http.MethodPost, "/webhooks/stripe/billing", bytes.NewReader([]byte(`{"id":"evt_2"}`)))
	bad.Header.Set("Stripe-Signature", sig)
	badRec := httptest.NewRecorder()
	srv.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered hmac_sha256_ts_dot request status = %d, want 401", badRec.Code)
	}
}

func signed(secret string, ts time.Time, body []byte) string {
	payload := fmt.Sprintf("%d:%s", ts.Unix(), body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return fmt.Sprintf("ts=%d;h1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func TestServerAuthenticatesJWTAndEnforcesBodyHash(t *testing.T) {
	cfg := testConfig()
	cfg.Endpoints["jwt_webhook"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/jwt",
		Destinations: []config.EndpointDestinationConfig{{
			Destination: "billing", Target: "jwt-events",
		}},
		Auth: config.AuthConfig{
			Type: "jwt", Scheme: "jwt_hs256", Secret: "shared-secret",
			SignatureHeader: "Authorization", TimestampTolerance: time.Minute,
			BodyHashClaim: "body_sha256",
		},
	}
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	body := []byte(`{"id":"evt_1"}`)
	sum := sha256.Sum256(body)
	mkToken := func(bodyHash string) string {
		claims := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix(), "body_sha256": bodyHash}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		s, err := tok.SignedString([]byte("shared-secret"))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}

	// Correct body hash → 202 Accepted.
	good := httptest.NewRequest(http.MethodPost, "/webhooks/jwt", bytes.NewReader(body))
	good.Header.Set("Authorization", "Bearer "+mkToken(hex.EncodeToString(sum[:])))
	goodRec := httptest.NewRecorder()
	srv.ServeHTTP(goodRec, good)
	if goodRec.Code != http.StatusAccepted {
		t.Fatalf("valid jwt status = %d, body = %s", goodRec.Code, goodRec.Body.String())
	}

	// Valid token but swapped body (hash no longer matches) → 401.
	swapped := httptest.NewRequest(http.MethodPost, "/webhooks/jwt", bytes.NewReader([]byte(`{"id":"evt_2"}`)))
	swapped.Header.Set("Authorization", "Bearer "+mkToken(hex.EncodeToString(sum[:])))
	swapRec := httptest.NewRecorder()
	srv.ServeHTTP(swapRec, swapped)
	if swapRec.Code != http.StatusUnauthorized {
		t.Fatalf("body-swap status = %d, want 401", swapRec.Code)
	}
}

func TestServerRejectsMissingJWT(t *testing.T) {
	cfg := testConfig()
	cfg.Endpoints["jwt_webhook"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/jwt",
		Destinations: []config.EndpointDestinationConfig{{Destination: "billing", Target: "jwt-events"}},
		Auth:         config.AuthConfig{Type: "jwt", Scheme: "jwt_hs256", Secret: "shared-secret", SignatureHeader: "Authorization"},
	}
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/jwt", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", rec.Code)
	}
}
