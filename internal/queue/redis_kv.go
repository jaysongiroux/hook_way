package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// RedisKVTarget describes a single Redis key-value destination. Name is the
// logical name used as the Publish "target" argument and drives key rendering
// via the {target} token.
type RedisKVTarget struct {
	// Name is the logical target identifier. It is substituted for {target} in
	// KeyTemplate during key rendering.
	Name string
	// TTL is the Redis key expiry. When <= 0, keys have no expiry.
	TTL time.Duration
	// KeyTemplate controls how the storage key is built. See renderKVKey for
	// the full set of supported tokens. When empty, the default template
	// "{prefix}:{endpoint}:{id}" is used.
	KeyTemplate string
}

// RedisKVConfig carries all configuration consumed by RedisKVPublisher.
// The factory builds this from the parsed config and passes it here.
//
// KeyPrefix is available to key templates as the {prefix} token.
// The factory must NOT pre-apply the prefix; just set cfg.KeyPrefix and pass
// the original target Names — the publisher owns all key rendering.
type RedisKVConfig struct {
	// KeyPrefix is available to key templates as the {prefix} token. When
	// empty, {prefix} renders as an empty string in templates.
	KeyPrefix string
	// DestinationID is the destination identifier used in error messages.
	DestinationID string
	// Logger is an optional structured logger. May be nil.
	Logger *slog.Logger
	// Targets enumerates the KV slots this publisher routes messages to.
	Targets []RedisKVTarget
}

// resolvedKVTarget is the internal state stored in the targets map after the
// constructor has resolved defaults and stored per-target settings.
type resolvedKVTarget struct {
	keyPrefix   string // resolved KeyPrefix from config (for {prefix} rendering)
	ttl         time.Duration
	keyTemplate string // never empty after constructor (default applied)
}

// RedisKVPublisher publishes webhook envelopes to Redis keys via SET.
//
// # Storage semantics — NOT a queue
//
// redis_kv is a key-value STORE (SET key=envelope), not a queue. Unlike
// redis_stream or redis_list there is no ordering guarantee, no consumer group,
// and no replay capability. Each write with the same rendered key overwrites the
// previous value (latest write wins). The TTL bounds the keyspace so old entries
// expire automatically.
//
// Best for: idempotency-key stores, latest-event caches, deduplication sidecars,
// and ephemeral state where only the most recent payload matters.
//
// The targets map is written only in NewRedisKVPublisher and is read-only during
// Publish, which makes concurrent fan-out reads safe without locking.
type RedisKVPublisher struct {
	ops    redisOps
	destID string
	logger *slog.Logger
	// targets maps the unprefixed target Name to its resolved state.
	// Written once in the constructor; read-only during Publish.
	targets map[string]resolvedKVTarget
}

// defaultKVKeyTemplate is used when RedisKVTarget.KeyTemplate is empty.
// {prefix} requires redis.key_prefix to be configured; when key_prefix is
// absent, {prefix} renders as an empty string, producing "::endpoint:id".
// Operators should configure key_prefix or use a template without {prefix}.
const defaultKVKeyTemplate = "{prefix}:{endpoint}:{id}"

// NewRedisKVPublisher constructs a RedisKVPublisher.
//
// For each target, KeyTemplate defaults to "{prefix}:{endpoint}:{id}" when
// empty. The prefix and template are stored per-target; Publish looks up by the
// original (unprefixed) Name so callers always use logical names.
//
// No Redis commands are issued at startup beyond what Ready() calls. There is no
// create concept for KV keys — they are created implicitly by SET.
func NewRedisKVPublisher(_ context.Context, ops redisOps, cfg RedisKVConfig) (*RedisKVPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &RedisKVPublisher{
		ops:     ops,
		destID:  cfg.DestinationID,
		logger:  logger,
		targets: make(map[string]resolvedKVTarget, len(cfg.Targets)),
	}

	for _, t := range cfg.Targets {
		tmpl := t.KeyTemplate
		if tmpl == "" {
			tmpl = defaultKVKeyTemplate
		}

		p.logger.Debug("redis_kv: registered target",
			slog.String("destination", cfg.DestinationID),
			slog.String("target", t.Name),
			slog.String("key_template", tmpl),
			slog.Duration("ttl", t.TTL),
		)

		// Store the resolved target — written only here, read-only in Publish.
		p.targets[t.Name] = resolvedKVTarget{
			keyPrefix:   cfg.KeyPrefix,
			ttl:         t.TTL,
			keyTemplate: tmpl,
		}
	}

	return p, nil
}

// Publish marshals msg to JSON and stores it at the rendered key via SET.
//
// target is the UNPREFIXED target Name. The key is rendered by renderKVKey
// using the target's configured KeyTemplate. The envelope is stored as a JSON
// string. When TTL <= 0 the key has no expiry (ops.Set already handles this).
func (p *RedisKVPublisher) Publish(ctx context.Context, _, target string, msg Message) error {
	// targets is written only in NewRedisKVPublisher (single goroutine);
	// this read is safe to perform concurrently across the server's publish
	// fan-out — mirrors the established SQS race invariant comment.
	rt, ok := p.targets[target]
	if !ok {
		return fmt.Errorf("redis_kv %q: target %q is not configured", p.destID, target)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("redis_kv %q: marshal message: %w", p.destID, err)
	}

	key := renderKVKey(rt.keyTemplate, rt.keyPrefix, msg, target)
	return p.ops.Set(ctx, key, string(payload), rt.ttl)
}

// Ready pings the Redis server to confirm the connection is live.
func (p *RedisKVPublisher) Ready(ctx context.Context) error {
	return p.ops.Ping(ctx)
}

// Close releases the underlying Redis connection pool.
func (p *RedisKVPublisher) Close() error {
	return p.ops.Close()
}

// renderKVKey renders a key from template by substituting the following tokens
// (literal "{...}" replacement):
//
//   - {prefix}          → cfg.KeyPrefix (empty string when key_prefix is not set;
//     note that {prefix} requires redis.key_prefix to be configured — when
//     key_prefix is absent this renders as an empty string)
//   - {target}          → the route target name (the Publish "target" argument)
//   - {endpoint}        → msg.Endpoint
//   - {id}              → msg.ID
//   - {idempotency_key} → msg.IdempotencyKey
//
// Unknown tokens (e.g. {custom}) are left as-is in the rendered key so
// operators can see misconfigured templates rather than silently dropping data.
func renderKVKey(template, prefix string, msg Message, target string) string {
	r := strings.NewReplacer(
		"{prefix}", prefix,
		"{target}", target,
		"{endpoint}", msg.Endpoint,
		"{id}", msg.ID,
		"{idempotency_key}", msg.IdempotencyKey,
	)
	return r.Replace(template)
}
