package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestRedisListPublisher_Publish verifies that LPushTrim is called with the
// resolved (prefixed) list key, the JSON envelope, and the configured maxLen.
func TestRedisListPublisher_Publish(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisListConfig{
		KeyPrefix:     "hw",
		DestinationID: "events",
		Targets: []RedisListTarget{
			{Name: "webhooks", MaxLen: 1000},
		},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	msg := Message{
		SchemaVersion:  "1",
		ID:             "msg-456",
		IdempotencyKey: "idem-def",
		Endpoint:       "ep-abc",
		ReceivedAt:     now,
		Body:           map[string]any{"event": "list-test"},
	}
	if err := p.Publish(ctx, "events", "webhooks", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.lpushTrimCalls) != 1 {
		t.Fatalf("expected 1 LPushTrim call, got %d", len(fake.lpushTrimCalls))
	}
	call := fake.lpushTrimCalls[0]

	// List key must be prefixed.
	if call.key != "hw:webhooks" {
		t.Errorf("list key: got %q, want %q", call.key, "hw:webhooks")
	}

	// MaxLen must match target config.
	if call.approxMaxLen != 1000 {
		t.Errorf("approxMaxLen: got %d, want 1000", call.approxMaxLen)
	}

	// value must be valid JSON that round-trips back to the message.
	var decoded Message
	if err := json.Unmarshal([]byte(call.value), &decoded); err != nil {
		t.Fatalf("value not valid JSON: %v", err)
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

// TestRedisListPublisher_UnknownTarget confirms that Publish returns an error
// for a target not present in the config.
func TestRedisListPublisher_UnknownTarget(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisListConfig{
		DestinationID: "events",
		Targets:       []RedisListTarget{{Name: "known"}},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	err = p.Publish(ctx, "events", "unknown", Message{})
	if err == nil {
		t.Fatal("expected error for unknown target, got nil")
	}
}

// TestRedisListPublisher_KeyPrefixAppliedOnce verifies that the prefix is
// applied exactly once — the resolved key stored in the targets map, and that
// Publish looks up by the UNPREFIXED target argument without double-prefixing.
func TestRedisListPublisher_KeyPrefixAppliedOnce(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisListConfig{
		KeyPrefix:     "pre",
		DestinationID: "events",
		Targets:       []RedisListTarget{{Name: "mylist"}},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	// Publish using the UNPREFIXED name — the publisher resolves the key.
	if err := p.Publish(ctx, "events", "mylist", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.lpushTrimCalls) != 1 {
		t.Fatalf("expected 1 LPushTrim, got %d", len(fake.lpushTrimCalls))
	}
	got := fake.lpushTrimCalls[0].key
	want := "pre:mylist"
	if got != want {
		t.Errorf("list key: got %q, want %q (double-prefixed?)", got, want)
	}
}

// TestRedisListPublisher_NoKeyPrefix verifies that when KeyPrefix is empty,
// no colon prefix is added.
func TestRedisListPublisher_NoKeyPrefix(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisListConfig{
		DestinationID: "events",
		Targets:       []RedisListTarget{{Name: "bare-list"}},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if err := p.Publish(ctx, "events", "bare-list", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := fake.lpushTrimCalls[0].key; got != "bare-list" {
		t.Errorf("list key: got %q, want %q", got, "bare-list")
	}
}

// TestRedisListPublisher_MaxLenZeroUnbounded confirms that when MaxLen=0,
// LPushTrim is called with approxMaxLen=0 (unbounded list).
func TestRedisListPublisher_MaxLenZeroUnbounded(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisListConfig{
		DestinationID: "events",
		Targets:       []RedisListTarget{{Name: "list", MaxLen: 0}},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if err := p.Publish(ctx, "events", "list", Message{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if fake.lpushTrimCalls[0].approxMaxLen != 0 {
		t.Errorf("expected approxMaxLen=0 for unbounded, got %d", fake.lpushTrimCalls[0].approxMaxLen)
	}
}

// TestRedisListPublisher_Ready confirms that Ready delegates to ops.Ping.
func TestRedisListPublisher_Ready(t *testing.T) {
	ctx := context.Background()

	t.Run("ping_success", func(t *testing.T) {
		fake := &fakeRedisOps{pingErr: nil}
		cfg := RedisListConfig{DestinationID: "e", Targets: []RedisListTarget{{Name: "l"}}}
		p, _ := NewRedisListPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); err != nil {
			t.Errorf("Ready: unexpected error: %v", err)
		}
	})

	t.Run("ping_error_propagated", func(t *testing.T) {
		wantErr := errors.New("redis unreachable")
		fake := &fakeRedisOps{pingErr: wantErr}
		cfg := RedisListConfig{DestinationID: "e", Targets: []RedisListTarget{{Name: "l"}}}
		p, _ := NewRedisListPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); !errors.Is(err, wantErr) {
			t.Errorf("Ready: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisListPublisher_Close confirms that Close delegates to ops.Close.
func TestRedisListPublisher_Close(t *testing.T) {
	ctx := context.Background()

	t.Run("close_success", func(t *testing.T) {
		fake := &fakeRedisOps{}
		cfg := RedisListConfig{DestinationID: "e", Targets: []RedisListTarget{{Name: "l"}}}
		p, _ := NewRedisListPublisher(ctx, fake, cfg)
		if err := p.Close(); err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	})

	t.Run("close_error_propagated", func(t *testing.T) {
		wantErr := errors.New("close failed")
		fake := &fakeRedisOps{closeErr: wantErr}
		cfg := RedisListConfig{DestinationID: "e", Targets: []RedisListTarget{{Name: "l"}}}
		p, _ := NewRedisListPublisher(ctx, fake, cfg)
		if err := p.Close(); !errors.Is(err, wantErr) {
			t.Errorf("Close: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisListPublisher_MultipleTargets verifies that multiple targets are
// resolved independently and Publish routes to the correct list key.
func TestRedisListPublisher_MultipleTargets(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisListConfig{
		KeyPrefix:     "app",
		DestinationID: "events",
		Targets: []RedisListTarget{
			{Name: "alpha", MaxLen: 100},
			{Name: "beta", MaxLen: 200},
		},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if err := p.Publish(ctx, "events", "beta", Message{ID: "beta-msg"}); err != nil {
		t.Fatalf("Publish to beta: %v", err)
	}
	if err := p.Publish(ctx, "events", "alpha", Message{ID: "alpha-msg"}); err != nil {
		t.Fatalf("Publish to alpha: %v", err)
	}

	if len(fake.lpushTrimCalls) != 2 {
		t.Fatalf("expected 2 LPushTrim calls, got %d", len(fake.lpushTrimCalls))
	}

	// First call was to beta.
	if fake.lpushTrimCalls[0].key != "app:beta" {
		t.Errorf("first call key: got %q, want %q", fake.lpushTrimCalls[0].key, "app:beta")
	}
	if fake.lpushTrimCalls[0].approxMaxLen != 200 {
		t.Errorf("beta maxLen: got %d, want 200", fake.lpushTrimCalls[0].approxMaxLen)
	}

	// Second call was to alpha.
	if fake.lpushTrimCalls[1].key != "app:alpha" {
		t.Errorf("second call key: got %q, want %q", fake.lpushTrimCalls[1].key, "app:alpha")
	}
	if fake.lpushTrimCalls[1].approxMaxLen != 100 {
		t.Errorf("alpha maxLen: got %d, want 100", fake.lpushTrimCalls[1].approxMaxLen)
	}
}

// TestRedisListPublisher_LoggerUsed verifies that a non-nil logger is exercised
// during construction (smoke-test: no panic, no dead-code elimination).
func TestRedisListPublisher_LoggerUsed(t *testing.T) {
	fake := &fakeRedisOps{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := RedisListConfig{
		KeyPrefix:     "pfx",
		DestinationID: "logged-dest",
		Logger:        logger,
		Targets:       []RedisListTarget{{Name: "list1", MaxLen: 50}},
	}
	ctx := context.Background()
	p, err := NewRedisListPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor with logger: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil publisher")
	}
}
