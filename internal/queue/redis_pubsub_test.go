package queue

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// lastQueueLogLine parses newline-delimited JSON log output from buf and
// returns the first map whose "msg" field equals msg. Returns nil if not found.
// Other queue test files in this package can reuse this helper.
func lastQueueLogLine(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m["msg"] == msg {
			return m
		}
	}
	return nil
}

// --- no-subscriber observability tests ---

type fakePubSubMetrics struct{ noSub int }

func (f *fakePubSubMetrics) RedisPubSubNoSubscriber(destination, target string) { f.noSub++ }

func TestRedisPubSubNoSubscriberLogsAndCounts(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := newFakeRedisOps() // existing fake
	fake.publishReceivers = 0 // simulate no subscriber
	m := &fakePubSubMetrics{}
	pub, err := NewRedisPubSubPublisher(context.Background(), fake, RedisPubSubConfig{
		DestinationID: "archive", Logger: logger, Metrics: m,
		Targets: []RedisPubSubTarget{{Name: "events"}},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if err := pub.Publish(context.Background(), "archive", "events", Message{ID: "evt_1", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if m.noSub != 1 {
		t.Fatalf("expected no-subscriber metric to increment once, got %d", m.noSub)
	}
	line := lastQueueLogLine(t, &buf, "published to channel with no subscribers; message dropped")
	if line == nil || line["level"] != "WARN" || line["subscribers"] == nil || line["event_id"] != "evt_1" {
		t.Fatalf("missing/incorrect no-subscriber warn: %v", line)
	}
	if line["destination"] != "archive" {
		t.Fatalf("expected destination=archive; got %v", line["destination"])
	}
	if line["target"] != "events" {
		t.Fatalf("expected target=events; got %v", line["target"])
	}
}

func TestRedisPubSubWithSubscriberNoWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := newFakeRedisOps()
	fake.publishReceivers = 2
	m := &fakePubSubMetrics{}
	pub, _ := NewRedisPubSubPublisher(context.Background(), fake, RedisPubSubConfig{
		DestinationID: "archive", Logger: logger, Metrics: m, Targets: []RedisPubSubTarget{{Name: "events"}},
	})
	if err := pub.Publish(context.Background(), "archive", "events", Message{ID: "evt_2", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if m.noSub != 0 {
		t.Fatalf("expected no no-subscriber increments, got %d", m.noSub)
	}
	if strings.Contains(buf.String(), "no subscribers") {
		t.Fatalf("unexpected no-subscriber warn when subscribers present")
	}
}

// TestRedisPubSubPublisher_Publish verifies that ops.Publish is called with the
// resolved (prefixed) channel name and a JSON envelope that round-trips to the
// original Message.
func TestRedisPubSubPublisher_Publish(t *testing.T) {
	fake := newFakeRedisOps()
	cfg := RedisPubSubConfig{
		KeyPrefix:     "hw",
		DestinationID: "events",
		Targets: []RedisPubSubTarget{
			{Name: "webhooks"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	msg := Message{
		SchemaVersion:  "1",
		ID:             "msg-pubsub-001",
		IdempotencyKey: "idem-pubsub",
		Endpoint:       "ep-pubsub",
		ReceivedAt:     now,
		Body:           map[string]any{"event": "pubsub-test"},
	}
	if err := p.Publish(ctx, "events", "webhooks", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.publishCalls) != 1 {
		t.Fatalf("expected 1 Publish call, got %d", len(fake.publishCalls))
	}
	call := fake.publishCalls[0]

	// Channel must be prefixed.
	if call.channel != "hw:webhooks" {
		t.Errorf("channel: got %q, want %q", call.channel, "hw:webhooks")
	}

	// Payload must be valid JSON that round-trips back to the message.
	var decoded Message
	if err := json.Unmarshal([]byte(call.payload), &decoded); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if decoded.ID != msg.ID {
		t.Errorf("decoded ID: got %q, want %q", decoded.ID, msg.ID)
	}
	if decoded.IdempotencyKey != msg.IdempotencyKey {
		t.Errorf("decoded IdempotencyKey: got %q, want %q", decoded.IdempotencyKey, msg.IdempotencyKey)
	}
	if decoded.Endpoint != msg.Endpoint {
		t.Errorf("decoded Endpoint: got %q, want %q", decoded.Endpoint, msg.Endpoint)
	}
}

// TestRedisPubSubPublisher_UnknownTarget confirms that Publish returns an error
// for a target not present in the config.
func TestRedisPubSubPublisher_UnknownTarget(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisPubSubConfig{
		DestinationID: "events",
		Targets:       []RedisPubSubTarget{{Name: "known"}},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	err = p.Publish(ctx, "events", "unknown", Message{})
	if err == nil {
		t.Fatal("expected error for unknown target, got nil")
	}
}

// TestRedisPubSubPublisher_KeyPrefixAppliedOnce verifies that KeyPrefix is
// applied exactly once in the constructor — Publish looks up by the UNPREFIXED
// target name and the resolved channel contains the prefix exactly once.
func TestRedisPubSubPublisher_KeyPrefixAppliedOnce(t *testing.T) {
	fake := newFakeRedisOps()
	cfg := RedisPubSubConfig{
		KeyPrefix:     "pre",
		DestinationID: "events",
		Targets:       []RedisPubSubTarget{{Name: "mychan"}},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	// Publish using the UNPREFIXED name — the publisher resolves the channel.
	if err := p.Publish(ctx, "events", "mychan", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.publishCalls) != 1 {
		t.Fatalf("expected 1 Publish, got %d", len(fake.publishCalls))
	}
	got := fake.publishCalls[0].channel
	want := "pre:mychan"
	if got != want {
		t.Errorf("channel: got %q, want %q (double-prefixed?)", got, want)
	}
}

// TestRedisPubSubPublisher_NoKeyPrefix verifies that when KeyPrefix is empty,
// no colon prefix is added to the channel name.
func TestRedisPubSubPublisher_NoKeyPrefix(t *testing.T) {
	fake := newFakeRedisOps()
	cfg := RedisPubSubConfig{
		DestinationID: "events",
		Targets:       []RedisPubSubTarget{{Name: "bare-channel"}},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if err := p.Publish(ctx, "events", "bare-channel", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := fake.publishCalls[0].channel; got != "bare-channel" {
		t.Errorf("channel: got %q, want %q", got, "bare-channel")
	}
}

// TestRedisPubSubPublisher_Ready confirms that Ready delegates to ops.Ping.
func TestRedisPubSubPublisher_Ready(t *testing.T) {
	ctx := context.Background()

	t.Run("ping_success", func(t *testing.T) {
		fake := &fakeRedisOps{pingErr: nil}
		cfg := RedisPubSubConfig{DestinationID: "e", Targets: []RedisPubSubTarget{{Name: "c"}}}
		p, _ := NewRedisPubSubPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); err != nil {
			t.Errorf("Ready: unexpected error: %v", err)
		}
	})

	t.Run("ping_error_propagated", func(t *testing.T) {
		wantErr := errors.New("redis unreachable")
		fake := &fakeRedisOps{pingErr: wantErr}
		cfg := RedisPubSubConfig{DestinationID: "e", Targets: []RedisPubSubTarget{{Name: "c"}}}
		p, _ := NewRedisPubSubPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); !errors.Is(err, wantErr) {
			t.Errorf("Ready: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisPubSubPublisher_Close confirms that Close delegates to ops.Close.
func TestRedisPubSubPublisher_Close(t *testing.T) {
	ctx := context.Background()

	t.Run("close_success", func(t *testing.T) {
		fake := &fakeRedisOps{}
		cfg := RedisPubSubConfig{DestinationID: "e", Targets: []RedisPubSubTarget{{Name: "c"}}}
		p, _ := NewRedisPubSubPublisher(ctx, fake, cfg)
		if err := p.Close(); err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	})

	t.Run("close_error_propagated", func(t *testing.T) {
		wantErr := errors.New("close failed")
		fake := &fakeRedisOps{closeErr: wantErr}
		cfg := RedisPubSubConfig{DestinationID: "e", Targets: []RedisPubSubTarget{{Name: "c"}}}
		p, _ := NewRedisPubSubPublisher(ctx, fake, cfg)
		if err := p.Close(); !errors.Is(err, wantErr) {
			t.Errorf("Close: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisPubSubPublisher_MultipleTargets verifies that multiple targets are
// resolved independently and Publish routes to the correct channel.
func TestRedisPubSubPublisher_MultipleTargets(t *testing.T) {
	fake := newFakeRedisOps()
	cfg := RedisPubSubConfig{
		KeyPrefix:     "app",
		DestinationID: "events",
		Targets: []RedisPubSubTarget{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if err := p.Publish(ctx, "events", "beta", Message{ID: "beta-msg"}); err != nil {
		t.Fatalf("Publish to beta: %v", err)
	}
	if err := p.Publish(ctx, "events", "alpha", Message{ID: "alpha-msg"}); err != nil {
		t.Fatalf("Publish to alpha: %v", err)
	}

	if len(fake.publishCalls) != 2 {
		t.Fatalf("expected 2 Publish calls, got %d", len(fake.publishCalls))
	}

	// First call was to beta.
	if fake.publishCalls[0].channel != "app:beta" {
		t.Errorf("first call channel: got %q, want %q", fake.publishCalls[0].channel, "app:beta")
	}

	// Second call was to alpha.
	if fake.publishCalls[1].channel != "app:alpha" {
		t.Errorf("second call channel: got %q, want %q", fake.publishCalls[1].channel, "app:alpha")
	}
}

// TestRedisPubSubPublisher_LoggerUsed verifies that a non-nil logger is exercised
// during construction (smoke-test: no panic, no dead-code elimination).
func TestRedisPubSubPublisher_LoggerUsed(t *testing.T) {
	fake := newFakeRedisOps()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := RedisPubSubConfig{
		KeyPrefix:     "pfx",
		DestinationID: "logged-dest",
		Logger:        logger,
		Targets:       []RedisPubSubTarget{{Name: "chan1"}},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor with logger: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil publisher")
	}
}

// TestRedisPubSubPublisher_PublishError verifies that ops.Publish errors are
// propagated back to the caller.
func TestRedisPubSubPublisher_PublishError(t *testing.T) {
	wantErr := errors.New("PUBLISH failed")
	fake := newFakeRedisOps()
	fake.publishErr = wantErr
	cfg := RedisPubSubConfig{
		DestinationID: "events",
		Targets:       []RedisPubSubTarget{{Name: "chan"}},
	}
	ctx := context.Background()
	p, err := NewRedisPubSubPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	err = p.Publish(ctx, "events", "chan", Message{})
	if !errors.Is(err, wantErr) {
		t.Errorf("Publish: got %v, want %v", err, wantErr)
	}
}
