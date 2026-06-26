package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// RedisPubSubTarget describes a single Redis Pub/Sub destination. Name is the
// logical name used as the Publish "target" argument and as the base for the
// resolved channel name (after KeyPrefix is applied).
type RedisPubSubTarget struct {
	// Name is the logical target identifier and the unprefixed channel name base.
	Name string
}

// RedisPubSubConfig carries all configuration consumed by RedisPubSubPublisher.
// The factory builds this from the parsed config and passes it here.
//
// KeyPrefix is applied INSIDE NewRedisPubSubPublisher (prefix + ":" + Name).
// The factory must NOT apply the prefix again; just set cfg.KeyPrefix and pass
// the original target Names — the publisher owns channel name resolution.
type RedisPubSubConfig struct {
	// KeyPrefix is prepended to every target Name as "<prefix>:<name>".
	// Leave empty for no prefix.
	KeyPrefix string
	// DestinationID is the destination identifier used in error messages.
	DestinationID string
	// Logger is an optional structured logger. May be nil.
	Logger *slog.Logger
	// Targets enumerates the channels this publisher routes messages to.
	Targets []RedisPubSubTarget
}

// RedisPubSubPublisher publishes webhook envelopes to Redis Pub/Sub channels
// via PUBLISH. It satisfies the Publisher interface.
//
// # Durability warning — fire-and-forget
//
// Redis Pub/Sub is NOT durable. Any message published when no subscriber is
// actively listening on the channel is silently dropped by Redis. This is
// deliberately fire-and-forget / at-most-once delivery. If you need at-least-
// once delivery use redis_stream or redis_list instead.
//
// # Redis Cluster note
//
// On Redis Cluster, plain PUBLISH broadcasts through the cluster bus so all
// nodes receive the message and forward it to subscribed clients — channel names
// are NOT keyspace-sharded. Sharded pub/sub (SPUBLISH/SSUBSCRIBE, available in
// Redis 7.0+ Cluster) can reduce intra-cluster bandwidth by scoping delivery to
// the shard that owns the channel's hash slot; this is a possible future
// enhancement but is NOT implemented here.
//
// The targets map is written only in NewRedisPubSubPublisher and is read-only
// during Publish, which makes concurrent fan-out reads safe without locking.
type RedisPubSubPublisher struct {
	ops    redisOps
	destID string
	// targets maps the unprefixed target Name to its resolved channel name.
	// Written once in the constructor; read-only during Publish.
	targets map[string]string
}

// NewRedisPubSubPublisher constructs a RedisPubSubPublisher.
//
// Channel resolution: KeyPrefix is applied here as "<prefix>:<name>" when
// KeyPrefix != "". The resolved channel name is stored; Publish looks up by the
// original (unprefixed) Name so callers always use logical names, not prefixed
// channel names.
//
// Pub/Sub channels are implicit in Redis — there is no create or declare
// concept. No commands are issued at startup beyond populating the targets map.
func NewRedisPubSubPublisher(_ context.Context, ops redisOps, cfg RedisPubSubConfig) (*RedisPubSubPublisher, error) {
	p := &RedisPubSubPublisher{
		ops:     ops,
		destID:  cfg.DestinationID,
		targets: make(map[string]string, len(cfg.Targets)),
	}

	for _, t := range cfg.Targets {
		// Resolve the channel name: apply KeyPrefix once here.
		channel := t.Name
		if cfg.KeyPrefix != "" {
			channel = cfg.KeyPrefix + ":" + t.Name
		}

		if cfg.Logger != nil {
			cfg.Logger.Debug("redis_pubsub: registered target",
				slog.String("destination", cfg.DestinationID),
				slog.String("name", t.Name),
				slog.String("channel", channel),
			)
		}

		// Store the resolved channel — written only here, read-only in Publish.
		p.targets[t.Name] = channel
	}

	return p, nil
}

// Publish marshals msg to JSON and sends it to the configured Redis Pub/Sub
// channel via PUBLISH. Delivery is fire-and-forget: if no subscriber is
// listening the message is lost.
//
// target is the UNPREFIXED target Name; the publisher resolves the channel name.
func (p *RedisPubSubPublisher) Publish(ctx context.Context, _, target string, msg Message) error {
	// targets is written only in NewRedisPubSubPublisher (single goroutine);
	// this read is safe to perform concurrently across the server's publish
	// fan-out — mirrors the established SQS race invariant comment.
	channel, ok := p.targets[target]
	if !ok {
		return fmt.Errorf("redis_pubsub %q: target %q is not configured", p.destID, target)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("redis_pubsub %q: marshal message: %w", p.destID, err)
	}

	return p.ops.Publish(ctx, channel, string(payload))
}

// Ready pings the Redis server to confirm the connection is live.
func (p *RedisPubSubPublisher) Ready(ctx context.Context) error {
	return p.ops.Ping(ctx)
}

// Close releases the underlying Redis connection pool.
func (p *RedisPubSubPublisher) Close() error {
	return p.ops.Close()
}
