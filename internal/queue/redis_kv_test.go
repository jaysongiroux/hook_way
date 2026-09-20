package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestRedisKVPublisher_Publish verifies that Set is called with the rendered key,
// the JSON envelope, and the configured TTL.
func TestRedisKVPublisher_Publish(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "hw",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "webhooks.kv", TTL: 24 * time.Hour, KeyTemplate: "{prefix}:{endpoint}:{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{
		SchemaVersion:  "1",
		ID:             "evt-001",
		IdempotencyKey: "idem-xyz",
		Endpoint:       "payments",
		ReceivedAt:     time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		Body:           map[string]any{"amount": 9900},
	}
	if err := p.Publish(ctx, "events", "webhooks.kv", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.setCalls) != 1 {
		t.Fatalf("expected 1 Set call, got %d", len(fake.setCalls))
	}
	call := fake.setCalls[0]

	// Key must render correctly: {prefix}=hw, {endpoint}=payments, {id}=evt-001
	want := "hw:payments:evt-001"
	if call.key != want {
		t.Errorf("key: got %q, want %q", call.key, want)
	}

	// TTL must match target config.
	if call.ttl != 24*time.Hour {
		t.Errorf("ttl: got %v, want %v", call.ttl, 24*time.Hour)
	}

	// Value must be valid JSON that round-trips back to the message.
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

func TestRedisKVPublisher_ObjectPayloadStoresRawBytes(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "hw",
		DestinationID: "files",
		Targets:       []RedisKVTarget{{Name: "uploads", TTL: time.Hour}},
	}
	p, err := NewRedisKVPublisher(context.Background(), fake, cfg)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{
		ID:          "evt-bin",
		Endpoint:    "upload",
		PayloadMode: "object",
		ObjectBytes: []byte{0x00, 0xff, 0x10},
	}
	if err := p.Publish(context.Background(), "files", "uploads", msg); err != nil {
		t.Fatal(err)
	}
	if len(fake.setCalls) != 1 {
		t.Fatalf("setCalls = %d", len(fake.setCalls))
	}
	if fake.setCalls[0].value != string([]byte{0x00, 0xff, 0x10}) {
		t.Fatalf("stored value = %q, want raw object bytes", fake.setCalls[0].value)
	}
}

// TestRedisKVPublisher_UnknownTarget confirms that Publish returns an error for
// a target not present in the config.
func TestRedisKVPublisher_UnknownTarget(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		DestinationID: "events",
		Targets:       []RedisKVTarget{{Name: "known", TTL: time.Hour}},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	err = p.Publish(ctx, "events", "unknown", Message{})
	if err == nil {
		t.Fatal("expected error for unknown target, got nil")
	}
}

// TestRedisKVPublisher_DefaultTemplate verifies that when KeyTemplate is empty,
// the default "{prefix}:{endpoint}:{id}" template is used.
func TestRedisKVPublisher_DefaultTemplate(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "myapp",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "hook", TTL: time.Hour, KeyTemplate: ""}, // empty → default
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{ID: "e-123", Endpoint: "orders"}
	if err := p.Publish(ctx, "events", "hook", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(fake.setCalls) != 1 {
		t.Fatalf("expected 1 Set call, got %d", len(fake.setCalls))
	}
	// Default template: "{prefix}:{endpoint}:{id}" → "myapp:orders:e-123"
	want := "myapp:orders:e-123"
	if got := fake.setCalls[0].key; got != want {
		t.Errorf("key with default template: got %q, want %q", got, want)
	}
}

// TestRedisKVPublisher_AllTokens verifies that every supported token is rendered.
func TestRedisKVPublisher_AllTokens(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "pfx",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{
				Name:        "all-tokens",
				TTL:         time.Hour,
				KeyTemplate: "{prefix}|{target}|{endpoint}|{id}|{idempotency_key}",
			},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{
		ID:             "id-abc",
		IdempotencyKey: "ikey-def",
		Endpoint:       "ep-ghi",
	}
	if err := p.Publish(ctx, "events", "all-tokens", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := "pfx|all-tokens|ep-ghi|id-abc|ikey-def"
	if got := fake.setCalls[0].key; got != want {
		t.Errorf("all-tokens key: got %q, want %q", got, want)
	}
}

// TestRedisKVPublisher_EmptyPrefix verifies that when KeyPrefix is empty,
// {prefix} renders as an empty string rather than producing a leading colon.
func TestRedisKVPublisher_EmptyPrefix(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "", // no prefix configured
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "nopfx", TTL: time.Hour, KeyTemplate: "{prefix}:{endpoint}:{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{ID: "x-1", Endpoint: "ep"}
	if err := p.Publish(ctx, "events", "nopfx", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// {prefix} is empty → rendered key is ":ep:x-1" (leading colon visible,
	// documenting that {prefix} requires redis.key_prefix to be configured).
	want := ":ep:x-1"
	if got := fake.setCalls[0].key; got != want {
		t.Errorf("no-prefix key: got %q, want %q", got, want)
	}
}

// TestRedisKVPublisher_TargetToken verifies that {target} renders to the route
// target name passed to Publish.
func TestRedisKVPublisher_TargetToken(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "app",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "my-target", TTL: time.Hour, KeyTemplate: "{prefix}:{target}:{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{ID: "msg-999"}
	if err := p.Publish(ctx, "events", "my-target", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := "app:my-target:msg-999"
	if got := fake.setCalls[0].key; got != want {
		t.Errorf("target token: got %q, want %q", got, want)
	}
}

// TestRedisKVPublisher_IdempotencyKeyToken verifies that {idempotency_key}
// renders to msg.IdempotencyKey in the template.
func TestRedisKVPublisher_IdempotencyKeyToken(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "app",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "dedup", TTL: time.Hour, KeyTemplate: "{prefix}:dedup:{idempotency_key}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{ID: "skip", IdempotencyKey: "idem-42"}
	if err := p.Publish(ctx, "events", "dedup", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := "app:dedup:idem-42"
	if got := fake.setCalls[0].key; got != want {
		t.Errorf("idempotency_key token: got %q, want %q", got, want)
	}
}

// TestRedisKVPublisher_UnknownTokensLeftAsIs verifies that tokens not in the
// supported set are preserved verbatim in the rendered key.
func TestRedisKVPublisher_UnknownTokensLeftAsIs(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "app",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "misc", TTL: time.Hour, KeyTemplate: "{prefix}:{unknown}:{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	msg := Message{ID: "z-1"}
	if err := p.Publish(ctx, "events", "misc", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// {unknown} is not a supported token → it must appear unchanged in the key.
	want := "app:{unknown}:z-1"
	if got := fake.setCalls[0].key; got != want {
		t.Errorf("unknown token: got %q, want %q (should be left as-is)", got, want)
	}
}

// TestRedisKVPublisher_TTLPassedToSet verifies that the configured TTL is
// forwarded to ops.Set.
func TestRedisKVPublisher_TTLPassedToSet(t *testing.T) {
	fake := &fakeRedisOps{}
	wantTTL := 7 * 24 * time.Hour // 7 days
	cfg := RedisKVConfig{
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "t", TTL: wantTTL, KeyTemplate: "{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if err := p.Publish(ctx, "events", "t", Message{ID: "ttl-test"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if fake.setCalls[0].ttl != wantTTL {
		t.Errorf("TTL: got %v, want %v", fake.setCalls[0].ttl, wantTTL)
	}
}

// TestRedisKVPublisher_ZeroTTLNoExpiry verifies that a zero TTL is passed
// through to Set (meaning no expiry per ops.Set contract).
func TestRedisKVPublisher_ZeroTTLNoExpiry(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "t", TTL: 0, KeyTemplate: "{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if err := p.Publish(ctx, "events", "t", Message{ID: "no-ttl"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if fake.setCalls[0].ttl != 0 {
		t.Errorf("TTL: got %v, want 0 (no expiry)", fake.setCalls[0].ttl)
	}
}

// TestRedisKVPublisher_Ready confirms that Ready delegates to ops.Ping.
func TestRedisKVPublisher_Ready(t *testing.T) {
	ctx := context.Background()

	t.Run("ping_success", func(t *testing.T) {
		fake := &fakeRedisOps{pingErr: nil}
		cfg := RedisKVConfig{DestinationID: "e", Targets: []RedisKVTarget{{Name: "k", KeyTemplate: "{id}"}}}
		p, _ := NewRedisKVPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); err != nil {
			t.Errorf("Ready: unexpected error: %v", err)
		}
	})

	t.Run("ping_error_propagated", func(t *testing.T) {
		wantErr := errors.New("redis unreachable")
		fake := &fakeRedisOps{pingErr: wantErr}
		cfg := RedisKVConfig{DestinationID: "e", Targets: []RedisKVTarget{{Name: "k", KeyTemplate: "{id}"}}}
		p, _ := NewRedisKVPublisher(ctx, fake, cfg)
		if err := p.Ready(ctx); !errors.Is(err, wantErr) {
			t.Errorf("Ready: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisKVPublisher_Close confirms that Close delegates to ops.Close.
func TestRedisKVPublisher_Close(t *testing.T) {
	ctx := context.Background()

	t.Run("close_success", func(t *testing.T) {
		fake := &fakeRedisOps{}
		cfg := RedisKVConfig{DestinationID: "e", Targets: []RedisKVTarget{{Name: "k", KeyTemplate: "{id}"}}}
		p, _ := NewRedisKVPublisher(ctx, fake, cfg)
		if err := p.Close(); err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	})

	t.Run("close_error_propagated", func(t *testing.T) {
		wantErr := errors.New("close failed")
		fake := &fakeRedisOps{closeErr: wantErr}
		cfg := RedisKVConfig{DestinationID: "e", Targets: []RedisKVTarget{{Name: "k", KeyTemplate: "{id}"}}}
		p, _ := NewRedisKVPublisher(ctx, fake, cfg)
		if err := p.Close(); !errors.Is(err, wantErr) {
			t.Errorf("Close: got %v, want %v", err, wantErr)
		}
	})
}

// TestRedisKVPublisher_MultipleTargets verifies that multiple targets are
// resolved independently and Publish routes to the correct key.
func TestRedisKVPublisher_MultipleTargets(t *testing.T) {
	fake := &fakeRedisOps{}
	cfg := RedisKVConfig{
		KeyPrefix:     "app",
		DestinationID: "events",
		Targets: []RedisKVTarget{
			{Name: "alpha", TTL: time.Hour, KeyTemplate: "{prefix}:alpha:{id}"},
			{Name: "beta", TTL: 2 * time.Hour, KeyTemplate: "{prefix}:beta:{id}"},
		},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if err := p.Publish(ctx, "events", "beta", Message{ID: "b-1"}); err != nil {
		t.Fatalf("Publish to beta: %v", err)
	}
	if err := p.Publish(ctx, "events", "alpha", Message{ID: "a-1"}); err != nil {
		t.Fatalf("Publish to alpha: %v", err)
	}

	if len(fake.setCalls) != 2 {
		t.Fatalf("expected 2 Set calls, got %d", len(fake.setCalls))
	}

	// First call was to beta.
	if fake.setCalls[0].key != "app:beta:b-1" {
		t.Errorf("first call key: got %q, want %q", fake.setCalls[0].key, "app:beta:b-1")
	}
	if fake.setCalls[0].ttl != 2*time.Hour {
		t.Errorf("beta TTL: got %v, want %v", fake.setCalls[0].ttl, 2*time.Hour)
	}

	// Second call was to alpha.
	if fake.setCalls[1].key != "app:alpha:a-1" {
		t.Errorf("second call key: got %q, want %q", fake.setCalls[1].key, "app:alpha:a-1")
	}
	if fake.setCalls[1].ttl != time.Hour {
		t.Errorf("alpha TTL: got %v, want %v", fake.setCalls[1].ttl, time.Hour)
	}
}

// TestRedisKVPublisher_LoggerUsed verifies that a non-nil logger is exercised
// during construction (smoke-test: no panic).
func TestRedisKVPublisher_LoggerUsed(t *testing.T) {
	fake := &fakeRedisOps{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := RedisKVConfig{
		KeyPrefix:     "pfx",
		DestinationID: "logged-dest",
		Logger:        logger,
		Targets:       []RedisKVTarget{{Name: "kv1", TTL: time.Hour, KeyTemplate: "{id}"}},
	}
	ctx := context.Background()
	p, err := NewRedisKVPublisher(ctx, fake, cfg)
	if err != nil {
		t.Fatalf("constructor with logger: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil publisher")
	}
}

// TestRedisKVLogsTarget asserts that the "registered target" Debug log uses
// the canonical "target" key (not "name").
func TestRedisKVLogsTarget(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := &fakeRedisOps{}
	_, err := NewRedisKVPublisher(context.Background(), fake, RedisKVConfig{
		KeyPrefix:     "pfx",
		DestinationID: "events",
		Logger:        logger,
		Targets:       []RedisKVTarget{{Name: "orders", TTL: time.Hour}},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	line := lastQueueLogLine(t, &buf, "redis_kv: registered target")
	if line == nil {
		t.Fatal("missing registered-target debug log")
	}
	if line["target"] != "orders" {
		t.Fatalf("expected target=orders, got: %v", line)
	}
	if _, hasName := line["name"]; hasName {
		t.Fatalf("log must not use key 'name', got: %v", line)
	}
}

// TestRenderKVKey exercises renderKVKey directly for comprehensive token coverage.
func TestRenderKVKey(t *testing.T) {
	msg := Message{
		ID:             "evt-42",
		IdempotencyKey: "idem-99",
		Endpoint:       "payments",
	}

	tests := []struct {
		name     string
		template string
		prefix   string
		target   string
		want     string
	}{
		{
			name:     "all_tokens",
			template: "{prefix}|{target}|{endpoint}|{id}|{idempotency_key}",
			prefix:   "hw",
			target:   "my-target",
			want:     "hw|my-target|payments|evt-42|idem-99",
		},
		{
			name:     "default_template",
			template: "{prefix}:{endpoint}:{id}",
			prefix:   "hw",
			target:   "ignored",
			want:     "hw:payments:evt-42",
		},
		{
			name:     "empty_prefix_renders_empty",
			template: "{prefix}:{endpoint}:{id}",
			prefix:   "",
			target:   "t",
			want:     ":payments:evt-42",
		},
		{
			name:     "target_token",
			template: "{prefix}:{target}:{id}",
			prefix:   "app",
			target:   "webhooks.kv",
			want:     "app:webhooks.kv:evt-42",
		},
		{
			name:     "idempotency_key_as_storage_key",
			template: "{prefix}:dedup:{idempotency_key}",
			prefix:   "svc",
			target:   "t",
			want:     "svc:dedup:idem-99",
		},
		{
			name:     "unknown_token_left_as_is",
			template: "{prefix}:{custom}:{id}",
			prefix:   "app",
			target:   "t",
			want:     "app:{custom}:evt-42",
		},
		{
			name:     "no_tokens_literal",
			template: "static-key",
			prefix:   "pfx",
			target:   "t",
			want:     "static-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderKVKey(tt.template, tt.prefix, msg, tt.target)
			if got != tt.want {
				t.Errorf("renderKVKey(%q, %q, msg, %q) = %q, want %q",
					tt.template, tt.prefix, tt.target, got, tt.want)
			}
		})
	}
}
