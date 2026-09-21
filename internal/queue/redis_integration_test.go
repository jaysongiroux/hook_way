package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/testenv"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// startRedis boots a Redis (or Valkey) container using the given image and
// returns a "redis://host:port" connection URL. It mirrors startLocalStack and
// self-skips when Docker is unavailable or the container fails to start.
func startRedis(t *testing.T, image string) string {
	t.Helper()
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := tcredis.Run(ctx, image)
	testenv.RequireContainer(t, err, "redis")
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get redis connection string: %v", err)
	}
	return url
}

// rawRedisClient builds a bare go-redis client pointed at the given URL. Used
// in integration tests to verify stream state without going through redisOps.
func rawRedisClient(t *testing.T, url string) *redis.Client {
	t.Helper()
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url %q: %v", url, err)
	}
	return redis.NewClient(opts)
}

// TestRedisStreamPublishRoundTrip publishes a Message via RedisStreamPublisher
// and then verifies the stream contents with a raw go-redis client:
//   - XLEN == 1
//   - XRANGE entry has "payload" field that round-trips Message.ID
//   - flat fields "id", "idempotency_key", "endpoint" are present
//   - consumer group exists (XINFO GROUPS)
func TestRedisStreamPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName    = "webhooks.stream"
		consumerGroup = "workers"
		keyPrefix     = "hw"
		streamKey     = keyPrefix + ":" + targetName
	)

	pub, err := NewRedisStreamPublisher(ctx, ops, RedisStreamConfig{
		DestinationID: "test-dest",
		KeyPrefix:     keyPrefix,
		Targets: []RedisStreamTarget{
			{Name: targetName, MaxLen: 1000, ConsumerGroup: consumerGroup, Create: true},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisStreamPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID:             "evt_redis_001",
		IdempotencyKey: "ikey_001",
		Endpoint:       "ep_payments",
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		Body:           map[string]any{"amount": 9900},
	}
	if err := pub.Publish(ctx, "test-dest", targetName, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// Assert XLEN == 1.
	length, err := raw.XLen(ctx, streamKey).Result()
	if err != nil {
		t.Fatalf("XLEN %q: %v", streamKey, err)
	}
	if length != 1 {
		t.Errorf("XLEN got %d, want 1", length)
	}

	// Assert XRANGE returns the entry and payload field round-trips.
	entries, err := raw.XRange(ctx, streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE %q: %v", streamKey, err)
	}
	if len(entries) != 1 {
		t.Fatalf("XRANGE got %d entries, want 1", len(entries))
	}
	fields := entries[0].Values

	payloadStr, ok := fields["payload"].(string)
	if !ok || payloadStr == "" {
		t.Fatalf("payload field missing or not a string: %#v", fields["payload"])
	}
	var decoded Message
	if err := json.Unmarshal([]byte(payloadStr), &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.ID != msg.ID {
		t.Errorf("payload ID round-trip: got %q, want %q", decoded.ID, msg.ID)
	}

	// Flat indexed fields.
	for _, field := range []string{"id", "idempotency_key", "endpoint"} {
		if v, ok := fields[field]; !ok || fmt.Sprint(v) == "" {
			t.Errorf("field %q missing or empty in stream entry", field)
		}
	}
	if v, _ := fields["id"].(string); v != msg.ID {
		t.Errorf("flat id field: got %q, want %q", v, msg.ID)
	}

	// Assert the consumer group exists.
	groups, err := raw.XInfoGroups(ctx, streamKey).Result()
	if err != nil {
		t.Fatalf("XINFO GROUPS %q: %v", streamKey, err)
	}
	found := false
	for _, g := range groups {
		if g.Name == consumerGroup {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("consumer group %q not found on stream %q", consumerGroup, streamKey)
	}
}

// TestRedisStreamKeyPrefixRoundTrip verifies that KeyPrefix is applied
// end-to-end: the stream lives at "<prefix>:<target>", not just "<target>".
func TestRedisStreamKeyPrefixRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName = "evt"
		keyPrefix  = "myapp"
	)

	pub, err := NewRedisStreamPublisher(ctx, ops, RedisStreamConfig{
		DestinationID: "pfx-dest",
		KeyPrefix:     keyPrefix,
		Targets: []RedisStreamTarget{
			{Name: targetName, MaxLen: 100},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisStreamPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Publish(ctx, "pfx-dest", targetName, Message{ID: "pfx_evt_1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// The stream must live at "myapp:evt", not "evt".
	prefixedKey := keyPrefix + ":" + targetName
	if n, err := raw.XLen(ctx, prefixedKey).Result(); err != nil || n != 1 {
		t.Errorf("prefixed stream %q: XLEN=%d err=%v; want n=1,err=nil", prefixedKey, n, err)
	}
	// The un-prefixed key must not exist.
	if n, _ := raw.XLen(ctx, targetName).Result(); n != 0 {
		t.Errorf("un-prefixed stream %q should not exist but has XLEN=%d", targetName, n)
	}
}

// TestRedisStreamMaxLenBounds verifies that MAXLEN ~ approximate trimming keeps
// the stream bounded well below the number of published messages. Because Redis
// uses approximate (radix-tree-node-aligned) trimming, the actual length may
// exceed MaxLen slightly — we assert it stays below a generous slack ceiling.
func TestRedisStreamMaxLenBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName = "bounded"
		maxLen     = 5
		publish    = 200 // well above maxLen so trimming is clearly tested
	)

	pub, err := NewRedisStreamPublisher(ctx, ops, RedisStreamConfig{
		DestinationID: "maxlen-dest",
		Targets: []RedisStreamTarget{
			{Name: targetName, MaxLen: maxLen},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisStreamPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	for i := range publish {
		if err := pub.Publish(ctx, "maxlen-dest", targetName, Message{ID: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatalf("Publish[%d]: %v", i, err)
		}
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	length, err := raw.XLen(ctx, targetName).Result()
	if err != nil {
		t.Fatalf("XLEN %q: %v", targetName, err)
	}
	// Approximate trimming may keep slightly more entries than MaxLen (aligned
	// to an internal radix-tree node boundary). We accept up to 10× MaxLen as a
	// generous ceiling while still proving the stream is bounded well below
	// the number published.
	const slackFactor = 10
	if length > maxLen*slackFactor {
		t.Errorf("XLEN=%d exceeds MaxLen(%d) * slack(%d) = %d after publishing %d messages; trimming not working",
			length, maxLen, slackFactor, maxLen*slackFactor, publish)
	}
	t.Logf("XLEN=%d after publishing %d messages with MaxLen=%d (slack ceiling=%d)", length, publish, maxLen, maxLen*slackFactor)
}

// TestRedisXGroupCreateMkStreamBUSYGROUP verifies that XGroupCreateMkStream is
// idempotent: calling it a second time on the same stream+group returns nil
// (the BUSYGROUP error is suppressed) rather than propagating the error.
func TestRedisXGroupCreateMkStreamBUSYGROUP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}
	defer func() { _ = ops.Close() }()
	const (
		stream = "busygroup-test-stream"
		group  = "busygroup-test-group"
	)

	// First call: creates the stream + group. Must succeed.
	if err := ops.XGroupCreateMkStream(ctx, stream, group, "$"); err != nil {
		t.Fatalf("first XGroupCreateMkStream: %v", err)
	}

	// Second call: group already exists → BUSYGROUP error must be suppressed.
	if err := ops.XGroupCreateMkStream(ctx, stream, group, "$"); err != nil {
		t.Fatalf("second XGroupCreateMkStream (BUSYGROUP): expected nil, got %v", err)
	}
}

// TestRedisStreamValkeyParity mirrors TestRedisStreamPublishRoundTrip but
// targets the valkey/valkey:8 image to prove RESP-protocol compatibility.
func TestRedisStreamValkeyParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "valkey/valkey:8")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName    = "vk.stream"
		consumerGroup = "vk-workers"
	)

	pub, err := NewRedisStreamPublisher(ctx, ops, RedisStreamConfig{
		DestinationID: "valkey-dest",
		Targets: []RedisStreamTarget{
			{Name: targetName, MaxLen: 1000, ConsumerGroup: consumerGroup, Create: true},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisStreamPublisher (valkey): %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready (valkey): %v", err)
	}

	msg := Message{
		ID:             "evt_valkey_001",
		IdempotencyKey: "vk_ikey_001",
		Endpoint:       "ep_valkey",
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		Body:           map[string]any{"check": "valkey-parity"},
	}
	if err := pub.Publish(ctx, "valkey-dest", targetName, msg); err != nil {
		t.Fatalf("Publish (valkey): %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// Assert XLEN == 1.
	if n, err := raw.XLen(ctx, targetName).Result(); err != nil || n != 1 {
		t.Errorf("valkey XLEN: got %d, err=%v; want 1, nil", n, err)
	}

	// Assert XRANGE round-trips.
	entries, err := raw.XRange(ctx, targetName, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE (valkey): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("XRANGE (valkey): no entries returned")
	}
	payloadStr, ok := entries[0].Values["payload"].(string)
	if !ok || payloadStr == "" {
		t.Fatalf("payload field missing or not a string (valkey): %#v", entries[0].Values["payload"])
	}
	var decoded Message
	if err := json.Unmarshal([]byte(payloadStr), &decoded); err != nil {
		t.Fatalf("unmarshal payload (valkey): %v", err)
	}
	if decoded.ID != msg.ID {
		t.Errorf("valkey payload ID round-trip: got %q, want %q", decoded.ID, msg.ID)
	}

	// Assert consumer group exists.
	groups, err := raw.XInfoGroups(ctx, targetName).Result()
	if err != nil {
		t.Fatalf("XINFO GROUPS (valkey): %v", err)
	}
	found := false
	for _, g := range groups {
		if g.Name == consumerGroup {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("consumer group %q not found on valkey stream %q", consumerGroup, targetName)
	}
}

// TestRedisListPublishRoundTrip publishes a Message via RedisListPublisher and
// then verifies the list contents with a raw go-redis client:
//   - LLEN == 1
//   - LINDEX 0 (most-recently prepended) unmarshals back to the Message with
//     ID round-trip.
func TestRedisListPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName = "webhooks.list"
		keyPrefix  = "hw"
		listKey    = keyPrefix + ":" + targetName
	)

	pub, err := NewRedisListPublisher(ctx, ops, RedisListConfig{
		DestinationID: "test-list-dest",
		KeyPrefix:     keyPrefix,
		Targets: []RedisListTarget{
			{Name: targetName, MaxLen: 1000},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisListPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID:             "evt_list_001",
		IdempotencyKey: "ikey_list_001",
		Endpoint:       "ep_list_payments",
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		Body:           map[string]any{"amount": 1234},
	}
	if err := pub.Publish(ctx, "test-list-dest", targetName, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// Assert LLEN == 1.
	length, err := raw.LLen(ctx, listKey).Result()
	if err != nil {
		t.Fatalf("LLEN %q: %v", listKey, err)
	}
	if length != 1 {
		t.Errorf("LLEN got %d, want 1", length)
	}

	// Assert LINDEX 0 returns the element and it round-trips.
	elem, err := raw.LIndex(ctx, listKey, 0).Result()
	if err != nil {
		t.Fatalf("LINDEX %q 0: %v", listKey, err)
	}
	if elem == "" {
		t.Fatal("LINDEX returned empty string")
	}
	var decoded Message
	if err := json.Unmarshal([]byte(elem), &decoded); err != nil {
		t.Fatalf("unmarshal list element: %v", err)
	}
	if decoded.ID != msg.ID {
		t.Errorf("payload ID round-trip: got %q, want %q", decoded.ID, msg.ID)
	}
	if decoded.IdempotencyKey != msg.IdempotencyKey {
		t.Errorf("IdempotencyKey round-trip: got %q, want %q", decoded.IdempotencyKey, msg.IdempotencyKey)
	}
	if decoded.Endpoint != msg.Endpoint {
		t.Errorf("Endpoint round-trip: got %q, want %q", decoded.Endpoint, msg.Endpoint)
	}
}

// TestRedisListKeyPrefixRoundTrip verifies that KeyPrefix is applied
// end-to-end: the list lives at "<prefix>:<target>", not just "<target>".
func TestRedisListKeyPrefixRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName = "evt"
		keyPrefix  = "myapp"
	)

	pub, err := NewRedisListPublisher(ctx, ops, RedisListConfig{
		DestinationID: "pfx-list-dest",
		KeyPrefix:     keyPrefix,
		Targets:       []RedisListTarget{{Name: targetName, MaxLen: 100}},
	})
	if err != nil {
		t.Fatalf("NewRedisListPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Publish(ctx, "pfx-list-dest", targetName, Message{ID: "pfx_list_1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// The list must live at "myapp:evt", not "evt".
	prefixedKey := keyPrefix + ":" + targetName
	if n, err := raw.LLen(ctx, prefixedKey).Result(); err != nil || n != 1 {
		t.Errorf("prefixed list %q: LLEN=%d err=%v; want n=1,err=nil", prefixedKey, n, err)
	}
	// The un-prefixed key must not exist.
	if n, _ := raw.LLen(ctx, targetName).Result(); n != 0 {
		t.Errorf("un-prefixed list %q should not exist but has LLEN=%d", targetName, n)
	}
}

// TestRedisPubSubPublishRoundTrip publishes a Message via RedisPubSubPublisher
// and then verifies that a subscriber receives it:
//   - A raw go-redis PubSub subscription is established on the resolved channel.
//   - sub.Receive(ctx) is called once to consume the Subscription confirmation
//     that Redis sends after SUBSCRIBE — this ensures the subscription is active
//     before we publish so no message is missed.
//   - Publish is called via NewRedisPubSubPublisher.
//   - The message is read from sub.Channel() with a 5-second timeout.
//   - The payload unmarshals back to the original Message (ID round-trip).
//
// Timing note: the Subscription confirmation approach is the canonical way to
// verify that Redis has registered the subscriber before the publisher fires.
// We do NOT use sleep here because a sleep is inherently racy.
func TestRedisPubSubPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	const (
		targetName = "webhooks.pubsub"
		keyPrefix  = "hw"
		channel    = keyPrefix + ":" + targetName
	)

	// Subscribe with a raw client BEFORE publishing so no messages are lost.
	// Redis Pub/Sub is fire-and-forget: a message published before the subscriber
	// is connected is silently dropped.
	subClient := rawRedisClient(t, redisURL)
	defer func() { _ = subClient.Close() }()
	sub := subClient.Subscribe(ctx, channel)
	defer func() { _ = sub.Close() }()
	// sub.Receive(ctx) blocks until Redis sends the Subscription confirmation
	// (a *redis.Subscription message). This guarantees the subscription is fully
	// registered in Redis before we publish — no race, no sleep needed.
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe confirmation: %v", err)
	}

	// Build the publisher using a separate connection.
	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	pub, err := NewRedisPubSubPublisher(ctx, ops, RedisPubSubConfig{
		DestinationID: "test-pubsub-dest",
		KeyPrefix:     keyPrefix,
		Targets: []RedisPubSubTarget{
			{Name: targetName},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisPubSubPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID:             "evt_pubsub_001",
		IdempotencyKey: "ikey_pubsub_001",
		Endpoint:       "ep_pubsub",
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		Body:           map[string]any{"check": "pubsub-round-trip"},
	}
	if err := pub.Publish(ctx, "test-pubsub-dest", targetName, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Read the message from the subscription channel with a bounded timeout.
	// If no message arrives within 5 seconds the test fails rather than hanging.
	ch := sub.Channel()
	select {
	case received, ok := <-ch:
		if !ok {
			t.Fatal("subscriber channel closed before message arrived")
		}
		// The received channel must match the resolved (prefixed) channel.
		if received.Channel != channel {
			t.Errorf("received channel: got %q, want %q", received.Channel, channel)
		}
		// The payload must unmarshal to the original Message.
		var decoded Message
		if err := json.Unmarshal([]byte(received.Payload), &decoded); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if decoded.ID != msg.ID {
			t.Errorf("payload ID round-trip: got %q, want %q", decoded.ID, msg.ID)
		}
		if decoded.IdempotencyKey != msg.IdempotencyKey {
			t.Errorf("IdempotencyKey round-trip: got %q, want %q", decoded.IdempotencyKey, msg.IdempotencyKey)
		}
		if decoded.Endpoint != msg.Endpoint {
			t.Errorf("Endpoint round-trip: got %q, want %q", decoded.Endpoint, msg.Endpoint)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out after 5s waiting for pub/sub message — was the subscriber ready before publish?")
	}
}

// TestRedisPubSubKeyPrefixRoundTrip verifies that KeyPrefix is applied
// end-to-end: a message published via the publisher with a key prefix is
// received on the prefixed channel "<prefix>:<target>", not the bare "<target>".
//
// We subscribe only to the PREFIXED channel to keep the test straightforward
// (one Subscription confirmation, no multi-channel complexity). We assert both
// that the message arrives on the prefixed channel and that it round-trips the
// ID. The absence of a message on the un-prefixed channel is implicitly verified
// by the unit-test layer (KeyPrefixAppliedOnce asserts the resolved channel).
func TestRedisPubSubKeyPrefixRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	const (
		targetName   = "evt"
		keyPrefix    = "myapp"
		prefixedChan = keyPrefix + ":" + targetName
	)

	subClient := rawRedisClient(t, redisURL)
	defer func() { _ = subClient.Close() }()
	// Subscribe to the PREFIXED channel only. One Subscription confirmation is
	// returned by sub.Receive(ctx), guaranteeing the subscription is active
	// in Redis before we publish.
	sub := subClient.Subscribe(ctx, prefixedChan)
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe confirmation: %v", err)
	}

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	pub, err := NewRedisPubSubPublisher(ctx, ops, RedisPubSubConfig{
		DestinationID: "pfx-pubsub-dest",
		KeyPrefix:     keyPrefix,
		Targets:       []RedisPubSubTarget{{Name: targetName}},
	})
	if err != nil {
		t.Fatalf("NewRedisPubSubPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Publish(ctx, "pfx-pubsub-dest", targetName, Message{ID: "pfx_pubsub_1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ch := sub.Channel()
	select {
	case received, ok := <-ch:
		if !ok {
			t.Fatal("subscriber channel closed before message arrived")
		}
		// The message must arrive on the PREFIXED channel.
		if received.Channel != prefixedChan {
			t.Errorf("received channel: got %q, want prefixed %q", received.Channel, prefixedChan)
		}
		var decoded Message
		if err := json.Unmarshal([]byte(received.Payload), &decoded); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if decoded.ID != "pfx_pubsub_1" {
			t.Errorf("payload ID: got %q, want %q", decoded.ID, "pfx_pubsub_1")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out after 5s waiting for pub/sub message on prefixed channel")
	}
}

// TestRedisListMaxLenBounds verifies that LTRIM keeps the list bounded at
// exactly MaxLen elements after publishing more than MaxLen messages. Unlike
// streams (approximate), list LTRIM is exact.
func TestRedisListMaxLenBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName = "bounded-list"
		maxLen     = 5
		publish    = 20 // well above maxLen so trimming is clearly exercised
	)

	pub, err := NewRedisListPublisher(ctx, ops, RedisListConfig{
		DestinationID: "list-maxlen-dest",
		Targets:       []RedisListTarget{{Name: targetName, MaxLen: maxLen}},
	})
	if err != nil {
		t.Fatalf("NewRedisListPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	for i := range publish {
		if err := pub.Publish(ctx, "list-maxlen-dest", targetName, Message{ID: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatalf("Publish[%d]: %v", i, err)
		}
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	length, err := raw.LLen(ctx, targetName).Result()
	if err != nil {
		t.Fatalf("LLEN %q: %v", targetName, err)
	}
	// LPushTrim uses exact LTRIM (not approximate like XADD MAXLEN ~), so the
	// list must be exactly MaxLen.
	if length != maxLen {
		t.Errorf("LLEN=%d after publishing %d messages with MaxLen=%d; want exactly %d (exact trim)",
			length, publish, maxLen, maxLen)
	}
	t.Logf("LLEN=%d after publishing %d messages with MaxLen=%d (exact)", length, publish, maxLen)
}

// TestRedisKVPublishRoundTrip publishes a Message via RedisKVPublisher and then
// verifies the stored value with a raw go-redis client:
//   - GET at the rendered key returns a non-empty string.
//   - The value unmarshals back to the original Message (ID round-trip).
//   - The key TTL is set (PTTL > 0) and approximately matches the configured TTL.
func TestRedisKVPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName  = "webhooks.kv"
		keyPrefix   = "hw"
		kvTTL       = 168 * time.Hour // 7 days
		keyTemplate = "{prefix}:{endpoint}:{id}"
	)

	pub, err := NewRedisKVPublisher(ctx, ops, RedisKVConfig{
		DestinationID: "test-kv-dest",
		KeyPrefix:     keyPrefix,
		Targets: []RedisKVTarget{
			{Name: targetName, TTL: kvTTL, KeyTemplate: keyTemplate},
		},
	})
	if err != nil {
		t.Fatalf("NewRedisKVPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID:             "evt_kv_001",
		IdempotencyKey: "ikey_kv_001",
		Endpoint:       "ep_kv_payments",
		ReceivedAt:     time.Now().UTC().Truncate(time.Second),
		Body:           map[string]any{"amount": 5000},
	}
	if err := pub.Publish(ctx, "test-kv-dest", targetName, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// The key must exist at the rendered path: hw:ep_kv_payments:evt_kv_001
	renderedKey := "hw:ep_kv_payments:evt_kv_001"

	val, err := raw.Get(ctx, renderedKey).Result()
	if err != nil {
		t.Fatalf("GET %q: %v", renderedKey, err)
	}
	if val == "" {
		t.Fatal("GET returned empty string")
	}

	// The value must unmarshal back to the original Message.
	var decoded Message
	if err := json.Unmarshal([]byte(val), &decoded); err != nil {
		t.Fatalf("unmarshal stored value: %v", err)
	}
	if decoded.ID != msg.ID {
		t.Errorf("payload ID round-trip: got %q, want %q", decoded.ID, msg.ID)
	}
	if decoded.IdempotencyKey != msg.IdempotencyKey {
		t.Errorf("IdempotencyKey round-trip: got %q, want %q", decoded.IdempotencyKey, msg.IdempotencyKey)
	}
	if decoded.Endpoint != msg.Endpoint {
		t.Errorf("Endpoint round-trip: got %q, want %q", decoded.Endpoint, msg.Endpoint)
	}

	// The TTL must be set (PTTL > 0) and approximately match the configured TTL.
	pttl, err := raw.PTTL(ctx, renderedKey).Result()
	if err != nil {
		t.Fatalf("PTTL %q: %v", renderedKey, err)
	}
	if pttl <= 0 {
		t.Errorf("PTTL=%v: key has no expiry; expected TTL ~%v to be set", pttl, kvTTL)
	}
	// Allow 5 seconds of slack for test execution time.
	slack := 5 * time.Second
	if pttl > kvTTL || pttl < kvTTL-slack {
		t.Errorf("PTTL=%v not approximately equal to configured TTL=%v (slack=%v)",
			pttl, kvTTL, slack)
	}
	t.Logf("PTTL=%v for key %q (configured TTL=%v)", pttl, renderedKey, kvTTL)
}

// TestRedisKVKeyPrefixRoundTrip verifies that KeyPrefix is available as the
// {prefix} token in the rendered key end-to-end.
func TestRedisKVKeyPrefixRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const (
		targetName  = "evt"
		keyPrefix   = "myapp"
		keyTemplate = "{prefix}:{endpoint}:{id}"
	)

	pub, err := NewRedisKVPublisher(ctx, ops, RedisKVConfig{
		DestinationID: "pfx-kv-dest",
		KeyPrefix:     keyPrefix,
		Targets:       []RedisKVTarget{{Name: targetName, TTL: time.Hour, KeyTemplate: keyTemplate}},
	})
	if err != nil {
		t.Fatalf("NewRedisKVPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	msg := Message{ID: "pfx_kv_1", Endpoint: "ep_pfx"}
	if err := pub.Publish(ctx, "pfx-kv-dest", targetName, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	// The key must exist at the prefixed path.
	prefixedKey := "myapp:ep_pfx:pfx_kv_1"
	if val, err := raw.Get(ctx, prefixedKey).Result(); err != nil || val == "" {
		t.Errorf("prefixed key %q: val=%q err=%v; want non-empty string", prefixedKey, val, err)
	}

	// The un-prefixed key must not exist.
	unprefixedKey := "ep_pfx:pfx_kv_1"
	if val, err := raw.Get(ctx, unprefixedKey).Result(); err == nil && val != "" {
		t.Errorf("un-prefixed key %q should not exist but has value %q", unprefixedKey, val)
	}
}

// TestRedisKVTTLBounds verifies that the configured TTL is applied end-to-end:
// PTTL returns a positive value indicating the key will expire.
func TestRedisKVTTLBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()

	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}

	const wantTTL = 10 * time.Minute

	pub, err := NewRedisKVPublisher(ctx, ops, RedisKVConfig{
		DestinationID: "ttl-kv-dest",
		Targets:       []RedisKVTarget{{Name: "ttl-target", TTL: wantTTL, KeyTemplate: "ttl-test:{id}"}},
	})
	if err != nil {
		t.Fatalf("NewRedisKVPublisher: %v", err)
	}
	defer func() { _ = pub.Close() }()
	msg := Message{ID: "ttl-evt-1"}
	if err := pub.Publish(ctx, "ttl-kv-dest", "ttl-target", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw := rawRedisClient(t, redisURL)
	defer func() { _ = raw.Close() }()
	pttl, err := raw.PTTL(ctx, "ttl-test:ttl-evt-1").Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	if pttl <= 0 {
		t.Errorf("PTTL=%v; key should have expiry (configured TTL=%v)", pttl, wantTTL)
	}
	// Allow 5 seconds of slack.
	slack := 5 * time.Second
	if pttl > wantTTL || pttl < wantTTL-slack {
		t.Errorf("PTTL=%v not approximately equal to configured TTL=%v (slack=%v)",
			pttl, wantTTL, slack)
	}
	t.Logf("PTTL=%v for configured TTL=%v (slack=%v)", pttl, wantTTL, slack)
}
