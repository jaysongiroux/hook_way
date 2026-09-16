package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/replayapi"
	"github.com/jasongiroux/hookway/internal/replaystore"
)

func TestFlowCaptureInsertBeforePublishContext(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Replay.Enabled = true
	cfg.Server.PublishTimeout = 50 * time.Millisecond
	captureStarted := make(chan struct{})
	captureRelease := make(chan struct{})
	fake := &fakeReplayCapturer{captureStarted: captureStarted, captureRelease: captureRelease}
	pub := &deadlineAwarePublisher{}
	srv, err := NewFlowServer(cfg, pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.SetReplayCapturer(fake)

	body := []byte(`{"ok":true}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
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
	time.Sleep(200 * time.Millisecond)
	close(captureRelease)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never returned")
	}
	invoked, ctxErr := pub.snapshot()
	if !invoked {
		t.Fatal("publisher was never invoked")
	}
	if ctxErr != nil {
		t.Fatalf("I9: publish context expired after slow capture: %v", ctxErr)
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
}

func TestFlowCaptureIsBestEffort(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Replay.Enabled = true
	pub := &fakePublisher{}
	srv, err := NewFlowServer(cfg, pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.SetReplayCapturer(&fakeReplayCapturer{captureErr: errors.New("insert failed")})
	body := []byte(`{"ok":true}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("capture error changed the response: %d %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}

	srv.SetReplayCapturer(&fakeReplayCapturer{capturePanic: true})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("capture panic changed the response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSkipUnauthenticatedNoAuthFlowNotCaptured(t *testing.T) {
	src := `
schema_version: 2
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  open:
    nodes:
      - {id: in, type: http_trigger, path: /open, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>auth, auth>env, env>out]
`
	cfg := loadFlowCfg(t, src)
	cfg.Replay.SkipUnauthenticated = true
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)
	req := httptest.NewRequest(http.MethodPost, "/open", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if len(fake.capturedEvents()) != 0 {
		t.Fatalf("captured no_auth event despite skip_unauthenticated: %+v", fake.capturedEvents())
	}
}

func TestV2RedeliveryPublishesCapturedEnvelope(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Replay.Enabled = true
	ingest := &fakePublisher{}
	srv, err := NewFlowServer(cfg, ingest, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	fake := &fakeReplayCapturer{}
	srv.SetReplayCapturer(fake)
	body := []byte(`{"ok":true}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d %s", rec.Code, rec.Body.String())
	}
	captured := fake.capturedEvents()
	if len(captured) != 1 {
		t.Fatalf("captured = %d", len(captured))
	}
	outcomes := fake.recordedOutcomes()
	if len(outcomes) != 1 || len(outcomes[0].deliveries) == 0 {
		t.Fatalf("outcomes = %+v", outcomes)
	}

	store := &memoryReplayStore{event: captured[0], deliveries: outcomes[0].deliveries}
	pub := &fakePublisher{}
	handler := replayapi.Handler("k", store, pub, nil, nil, slog.Default())
	replayReq := httptest.NewRequest(http.MethodPost, "/replay/events/"+captured[0].ID+"/replay", bytes.NewReader([]byte(`{}`)))
	replayReq.Header.Set("Authorization", "Bearer k")
	replayRec := httptest.NewRecorder()
	handler.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusOK {
		t.Fatalf("replay status = %d %s", replayRec.Code, replayRec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("redelivered = %d, want 1", len(pub.published))
	}
	if pub.published[0].msg.ReplayOf != captured[0].ID {
		t.Fatalf("ReplayOf = %q, want %q", pub.published[0].msg.ReplayOf, captured[0].ID)
	}
}

type memoryReplayStore struct {
	event      replaystore.Event
	deliveries []replaystore.Delivery
}

func (m *memoryReplayStore) List(context.Context, replaystore.Filter) ([]replaystore.EventSummary, string, error) {
	return nil, "", nil
}
func (m *memoryReplayStore) Get(_ context.Context, id string) (replaystore.Event, bool, error) {
	if id != m.event.ID {
		return replaystore.Event{}, false, nil
	}
	return m.event, true, nil
}
func (m *memoryReplayStore) Count(context.Context, replaystore.Filter) (int64, error) { return 1, nil }
func (m *memoryReplayStore) Deliveries(_ context.Context, eventID string) ([]replaystore.Delivery, error) {
	if eventID != m.event.ID {
		return nil, nil
	}
	return m.deliveries, nil
}
