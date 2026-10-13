package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// TestRedisStreamPublisher_Publish verifies that XAdd is called with the resolved
// (prefixed) stream key, the correct maxLen, a "payload" field containing the
// full JSON envelope, and flat indexed fields (id, idempotency_key, endpoint,
// received_at).
func TestRedisStreamPublisher_Publish(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisStreamConfig{
		KeyPrefix:     "hw",
		DestinationID: "events",
		Targets: []RedisStreamTarget{
			{Name: "webhooks", MaxLen: 50000},
		},
	}
	ctx := context.Background()
	p, err := NewRedisStreamPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	msg := Message{
		SchemaVersion:  "1",
		ID:             "msg-123",
		IdempotencyKey: "idem-abc",
		Endpoint:       "ep-xyz",
		ReceivedAt:     now,
		Body:           map[string]any{"event": "test"},
	}
	if err := p.Publish(ctx, "events", "webhooks", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.xaddCalls) != 1 {
		t.Fatalf("expected 1 XAdd call, got %d", len(fake.xaddCalls))
	}
	call := fake.xaddCalls[0]

	// Stream key must be prefixed.
	if call.stream != "hw:webhooks" {
		t.Errorf("stream key: got %q, want %q", call.stream, "hw:webhooks")
	}

	// MaxLen must match target config.
	if call.approxMaxLen != 50000 {
		t.Errorf("approxMaxLen: got %d, want 50000", call.approxMaxLen)
	}

	// payload field must be valid JSON that round-trips back to the message.
	payloadRaw, ok := call.values["payload"]
	if !ok {
		t.Fatal("values missing 'payload' field")
	}
	payloadStr, ok := payloadRaw.(string)
	if !ok {
		t.Fatalf("payload field must be a string, got %T", payloadRaw)
	}
	var decoded Message
	if err := json.Unmarshal([]byte(payloadStr), &decoded); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if decoded.ID != msg.ID {
		t.Errorf("decoded ID: got %q, want %q", decoded.ID, msg.ID)
	}

	// Flat indexed fields.
	assertField(t, call.values, "id", msg.ID)
	assertField(t, call.values, "idempotency_key", msg.IdempotencyKey)
	assertField(t, call.values, "endpoint", msg.Endpoint)
	assertField(t, call.values, "received_at", now.Format(time.RFC3339Nano))
}

// TestRedisStreamPublisher_UnknownTarget confirms that Publish returns an error
// for a target not present in the config.
func TestRedisStreamPublisher_UnknownTarget(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisStreamConfig{
		DestinationID: "events",
		Targets:       []RedisStreamTarget{{Name: "known"}},
	}
	ctx := context.Background()
	p, err := NewRedisStreamPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	err = p.Publish(ctx, "events", "unknown", Message{})
	if err == nil {
		t.Fatal("expected error for unknown target, got nil")
	}
}

// TestRedisStreamPublisher_ConsumerGroupCreated confirms that XGroupCreateMkStream
// is called for each target with Create=true and a non-empty ConsumerGroup.
func TestRedisStreamPublisher_ConsumerGroupCreated(t *testing.T) {
	var groupCalls []struct{ stream, group, start string }

	fake := &fakeRedisOpsWithGroupTracking{
		fakeRedisOps: &fakeRedisOps{},
		onXGroupCreate: func(stream, group, start string) {
			groupCalls = append(groupCalls, struct{ stream, group, start string }{stream, group, start})
		},
	}

	cfg := RedisStreamConfig{
		KeyPrefix:     "pfx",
		DestinationID: "events",
		Targets: []RedisStreamTarget{
			{Name: "s1", ConsumerGroup: "workers", Create: true},
			{Name: "s2", ConsumerGroup: "readers", Create: true},
			{Name: "s3", ConsumerGroup: "ignored", Create: false}, // Create=false → no group call
			{Name: "s4", Create: true},                            // no group name → no group call
		},
	}
	ctx := context.Background()
	_, err := NewRedisStreamPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if len(groupCalls) != 2 {
		t.Fatalf("expected 2 XGroupCreateMkStream calls, got %d", len(groupCalls))
	}
	// Both calls must use the prefixed stream key.
	for _, c := range groupCalls {
		if c.stream != "pfx:s1" && c.stream != "pfx:s2" {
			t.Errorf("unexpected stream in group create call: %q", c.stream)
		}
		if c.start != "$" {
			t.Errorf("group start: got %q, want %q", c.start, "$")
		}
	}
}

// TestRedisStreamPublisher_KeyPrefixAppliedOnce verifies that the prefix is
// applied exactly once — the resolved key stored in the targets map, and that
// Publish looks up by the UNPREFIXED target argument without double-prefixing.
func TestRedisStreamPublisher_KeyPrefixAppliedOnce(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisStreamConfig{
		KeyPrefix:     "pre",
		DestinationID: "events",
		Targets:       []RedisStreamTarget{{Name: "stream"}},
	}
	ctx := context.Background()
	p, err := NewRedisStreamPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	// Publish using the UNPREFIXED name — the publisher resolves the key.
	if err := p.Publish(ctx, "events", "stream", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.xaddCalls) != 1 {
		t.Fatalf("expected 1 XAdd, got %d", len(fake.xaddCalls))
	}
	got := fake.xaddCalls[0].stream
	want := "pre:stream"
	if got != want {
		t.Errorf("stream key: got %q, want %q (double-prefixed?)", got, want)
	}
}

// TestRedisStreamPublisher_NoKeyPrefix verifies that when KeyPrefix is empty,
// no colon prefix is added.
func TestRedisStreamPublisher_NoKeyPrefix(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisStreamConfig{
		DestinationID: "events",
		Targets:       []RedisStreamTarget{{Name: "bare-stream"}},
	}
	ctx := context.Background()
	p, err := NewRedisStreamPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if err := p.Publish(ctx, "events", "bare-stream", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := fake.xaddCalls[0].stream; got != "bare-stream" {
		t.Errorf("stream key: got %q, want %q", got, "bare-stream")
	}
}

// TestRedisStreamPublisher_Ready confirms that Ready delegates to ops.Ping.
func TestRedisStreamPublisher_Ready(t *testing.T) {
	ctx := context.Background()

	t.Run("ping_success", func(t *testing.T) {
		fake := &fakeRedisOps{pingErr: nil}
		cfg := RedisStreamConfig{DestinationID: "e", Targets: []RedisStreamTarget{{Name: "s"}}}
		p, _ := NewRedisStreamPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); err != nil {
			t.Errorf("Ready: unexpected error: %v", err)
		}
	})

	t.Run("ping_error_propagated", func(t *testing.T) {
		wantErr := errors.New("redis unreachable")
		fake := &fakeRedisOps{pingErr: wantErr}
		cfg := RedisStreamConfig{DestinationID: "e", Targets: []RedisStreamTarget{{Name: "s"}}}
		p, _ := NewRedisStreamPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); !errors.Is(err, wantErr) {
			t.Errorf("Ready: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisStreamPublisher_Close confirms that Close delegates to ops.Close.
func TestRedisStreamPublisher_Close(t *testing.T) {
	ctx := context.Background()

	t.Run("close_success", func(t *testing.T) {
		fake := &fakeRedisOps{}
		cfg := RedisStreamConfig{DestinationID: "e", Targets: []RedisStreamTarget{{Name: "s"}}}
		p, _ := NewRedisStreamPublisher(ctx, fake, cfg)
		if err := p.Close(); err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	})

	t.Run("close_error_propagated", func(t *testing.T) {
		wantErr := errors.New("close failed")
		fake := &fakeRedisOps{closeErr: wantErr}
		cfg := RedisStreamConfig{DestinationID: "e", Targets: []RedisStreamTarget{{Name: "s"}}}
		p, _ := NewRedisStreamPublisher(ctx, fake, cfg)
		if err := p.Close(); !errors.Is(err, wantErr) {
			t.Errorf("Close: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisStreamPublisher_MaxLenZeroUnbounded confirms that when MaxLen=0,
// XAdd is called with approxMaxLen=0 (unbounded stream).
func TestRedisStreamPublisher_MaxLenZeroUnbounded(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisStreamConfig{
		DestinationID: "events",
		Targets:       []RedisStreamTarget{{Name: "s", MaxLen: 0}},
	}
	ctx := context.Background()
	p, err := NewRedisStreamPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if err := p.Publish(ctx, "events", "s", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if fake.xaddCalls[0].approxMaxLen != 0 {
		t.Errorf("expected approxMaxLen=0 for unbounded, got %d", fake.xaddCalls[0].approxMaxLen)
	}
}

// TestRedisStreamLogsCreatedConsumerGroup asserts that NewRedisStreamPublisher
// emits an Info "created consumer group" log when Create=true and ConsumerGroup
// is non-empty.
func TestRedisStreamLogsCreatedConsumerGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fake := &fakeRedisOpsWithGroupTracking{
		fakeRedisOps:   &fakeRedisOps{},
		onXGroupCreate: func(_, _, _ string) {},
	}

	_, err := NewRedisStreamPublisher(context.Background(), fake, RedisStreamConfig{
		KeyPrefix:     "hw",
		DestinationID: "events",
		Logger:        logger,
		Targets: []RedisStreamTarget{
			{Name: "orders", ConsumerGroup: "workers", Create: true},
		},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	line := lastQueueLogLine(t, &buf, "created consumer group")
	if line == nil || line["level"] != "INFO" || line["destination"] != "events" || line["target"] != "orders" || line["group"] != "workers" {
		t.Fatalf("missing created-consumer-group info log: %v", line)
	}
}

// --- helpers ---

func assertField(t *testing.T, values map[string]any, key, want string) {
	t.Helper()
	v, ok := values[key]
	if !ok {
		t.Errorf("values missing field %q", key)
		return
	}
	got, ok := v.(string)
	if !ok {
		t.Errorf("field %q: expected string, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("field %q: got %q, want %q", key, got, want)
	}
}

// fakeRedisOpsWithGroupTracking wraps fakeRedisOps and intercepts
// XGroupCreateMkStream calls for assertion purposes.
type fakeRedisOpsWithGroupTracking struct {
	*fakeRedisOps
	onXGroupCreate func(stream, group, start string)
}

func (f *fakeRedisOpsWithGroupTracking) XGroupCreateMkStream(_ context.Context, stream, group, start string) error {
	if f.onXGroupCreate != nil {
		f.onXGroupCreate(stream, group, start)
	}
	return f.xgroupErr
}
