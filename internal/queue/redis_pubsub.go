package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jaysongiroux/hook_way/internal/vars"
	"log/slog"
)

// RedisPubSubTarget describes a single Redis Pub/Sub destination. Name is the
// logical name used as the Publish "target" argument and as the base for the
// resolved channel name (after KeyPrefix is applied).
type RedisPubSubTarget struct {
	// Name is the logical target identifier and the unprefixed channel name base.
	Name string
	// Template is set for dynamic routes (target contains {variable}
	// placeholders); the channel is derived from the rendered name.
	Template vars.Matcher
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
	// Logger is an optional structured logger. May be nil (defaults to slog.Default()).
	Logger *slog.Logger
	// Metrics is an optional observability hook for no-subscriber drops. May be nil.
	Metrics RedisPubSubMetrics
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
	ops     redisOps
	destID  string
	logger  *slog.Logger
	metrics RedisPubSubMetrics
	// targets maps the unprefixed target Name to its resolved channel name.
	// Written once in the constructor; read-only during Publish.
	targets   *targetResolver[string]
	keyPrefix string
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
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &RedisPubSubPublisher{
		ops:       ops,
		destID:    cfg.DestinationID,
		logger:    logger,
		metrics:   cfg.Metrics,
		targets:   newTargetResolver[string](0, logger),
		keyPrefix: cfg.KeyPrefix,
	}

	for _, t := range cfg.Targets {
		// Resolve the channel name: apply KeyPrefix once here.
		channel := t.Name
		if cfg.KeyPrefix != "" {
			channel = cfg.KeyPrefix + ":" + t.Name
		}

		logger.Debug("redis_pubsub: registered target",
			slog.String("destination", cfg.DestinationID),
			slog.String("target", t.Name),
			slog.String("channel", channel),
		)

		if t.Template.IsTemplate() {
			p.targets.addTemplate(t.Template, channel)
		} else {
			p.targets.addStatic(t.Name, channel)
		}
	}

	return p, nil
}

// Publish marshals msg to JSON and sends it to the configured Redis Pub/Sub
// channel via PUBLISH. Delivery is fire-and-forget: if no subscriber is
// listening the message is lost. When no subscriber receives the message, a
// Warn is logged and the no-subscriber metric is incremented (if wired).
//
// target is the UNPREFIXED target Name; the publisher resolves the channel name.
func (p *RedisPubSubPublisher) Publish(ctx context.Context, _, target string, msg Message) error {
	// targets is written only in NewRedisPubSubPublisher (single goroutine);
	// this read is safe to perform concurrently across the server's publish
	// fan-out — mirrors the established SQS race invariant comment.
	channel, err := p.targets.resolve(ctx, target, p.provisionChannel)
	if err != nil {
		return fmt.Errorf("redis_pubsub %q: target %q is not configured", p.destID, target)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("redis_pubsub %q: marshal message: %w", p.destID, err)
	}

	receivers, err := p.ops.Publish(ctx, channel, string(payload))
	if err != nil {
		return fmt.Errorf("redis_pubsub %q: publish: %w", p.destID, err)
	}
	if receivers == 0 {
		p.logger.Warn("published to channel with no subscribers; message dropped",
			slog.String("destination", p.destID),
			slog.String("target", target),
			slog.String("event_id", msg.ID),
			slog.Int64("subscribers", receivers),
		)
		if p.metrics != nil {
			p.metrics.RedisPubSubNoSubscriber(p.destID, target)
		}
	}
	return nil
}

// provisionChannel derives the prefixed channel for a rendered dynamic target.
func (p *RedisPubSubPublisher) provisionChannel(_ context.Context, _ string, rendered string) (string, error) {
	if p.keyPrefix != "" {
		return p.keyPrefix + ":" + rendered, nil
	}
	return rendered, nil
}

// Ready pings the Redis server to confirm the connection is live.
func (p *RedisPubSubPublisher) Ready(ctx context.Context) error {
	return p.ops.Ping(ctx)
}

// Close releases the underlying Redis connection pool.
func (p *RedisPubSubPublisher) Close() error {
	return p.ops.Close()
}
