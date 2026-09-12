package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jasongiroux/hookway/internal/vars"
)

// RedisStreamTarget describes a single Redis Stream destination. Name is the
// logical name used as the Publish "target" argument and as the base for the
// resolved stream key (after KeyPrefix is applied).
type RedisStreamTarget struct {
	// Name is the logical target identifier and the unprefixed stream key base.
	Name string
	// MaxLen is the approximate MAXLEN cap passed to XADD MAXLEN ~. Zero means
	// unbounded (no trim).
	MaxLen int64
	// ConsumerGroup is the consumer group to create when Create is true.
	// Ignored when empty.
	ConsumerGroup string
	// Create, when true and ConsumerGroup is non-empty, causes
	// NewRedisStreamPublisher to call XGroupCreateMkStream at startup so the
	// group exists before any consumer subscribes.
	Create bool
	// Template is set for dynamic routes (Name contains {variable}
	// placeholders). Rendered streams the matcher accepts get these settings;
	// the consumer group is created on first sight when Create is set.
	Template vars.Matcher
}

// RedisStreamConfig carries all configuration consumed by RedisStreamPublisher.
// The factory (T4) builds this from the parsed config and passes it here.
//
// KeyPrefix is applied INSIDE NewRedisStreamPublisher (prefix + ":" + Name).
// The factory must NOT apply the prefix again; just set cfg.KeyPrefix and pass
// the original target Names — the publisher owns key resolution.
type RedisStreamConfig struct {
	// KeyPrefix is prepended to every target Name as "<prefix>:<name>".
	// Leave empty for no prefix.
	KeyPrefix string
	// DestinationID is the destination identifier used in error messages.
	DestinationID string
	// Logger is an optional structured logger. May be nil.
	Logger *slog.Logger
	// Targets enumerates the streams this publisher routes messages to.
	Targets []RedisStreamTarget
}

// resolvedTarget is the internal state stored in the targets map after the
// constructor has applied KeyPrefix and resolved the stream key.
type resolvedTarget struct {
	streamKey     string // fully resolved (prefixed) Redis stream key
	maxLen        int64
	consumerGroup string
	create        bool // whether to create the consumer group on first sight (dynamic routes)
}

// RedisStreamPublisher publishes webhook envelopes to Redis Streams via XADD.
// It satisfies the Publisher interface. The targets map is written only in
// NewRedisStreamPublisher and is read-only during Publish, which makes
// concurrent fan-out reads safe without locking.
type RedisStreamPublisher struct {
	ops    redisOps
	destID string
	logger *slog.Logger
	// targets maps the unprefixed target Name to its resolved state; dynamic
	// routes resolve rendered names on first sight.
	targets   *targetResolver[resolvedTarget]
	keyPrefix string
}

// NewRedisStreamPublisher constructs a RedisStreamPublisher.
//
// Key resolution: KeyPrefix is applied here as "<prefix>:<name>" when
// KeyPrefix != "". The resolved key is stored; Publish looks up by the original
// (unprefixed) Name so callers always use logical names, not prefixed keys.
//
// Consumer group creation: for each target with Create==true and a non-empty
// ConsumerGroup, XGroupCreateMkStream is called with start="$" (only new
// messages after group creation). BUSYGROUP errors (group already exists) are
// treated as success by the goRedisOps implementation.
func NewRedisStreamPublisher(ctx context.Context, ops redisOps, cfg RedisStreamConfig) (*RedisStreamPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &RedisStreamPublisher{
		ops:       ops,
		destID:    cfg.DestinationID,
		logger:    logger,
		targets:   newTargetResolver[resolvedTarget](0, logger),
		keyPrefix: cfg.KeyPrefix,
	}

	for _, t := range cfg.Targets {
		if t.Template.IsTemplate() {
			// Dynamic route: the stream key and consumer group are created on
			// first sight of a rendered name.
			p.targets.addTemplate(t.Template, resolvedTarget{maxLen: t.MaxLen, consumerGroup: t.ConsumerGroup, create: t.Create})
			continue
		}
		// Resolve the stream key: apply KeyPrefix once here.
		streamKey := t.Name
		if cfg.KeyPrefix != "" {
			streamKey = cfg.KeyPrefix + ":" + t.Name
		}

		// Optionally create the consumer group at startup.
		if t.Create && t.ConsumerGroup != "" {
			if err := ops.XGroupCreateMkStream(ctx, streamKey, t.ConsumerGroup, "$"); err != nil {
				return nil, fmt.Errorf("redis_stream %q: create consumer group %q on stream %q: %w",
					cfg.DestinationID, t.ConsumerGroup, streamKey, err)
			}
			p.logger.Info("created consumer group",
				slog.String("destination", cfg.DestinationID),
				slog.String("target", t.Name),
				slog.String("group", t.ConsumerGroup),
			)
		}

		p.targets.addStatic(t.Name, resolvedTarget{
			streamKey:     streamKey,
			maxLen:        t.MaxLen,
			consumerGroup: t.ConsumerGroup,
		})
	}

	return p, nil
}

// provisionStream is the resolver hook for a rendered dynamic target: derive
// the prefixed stream key and create the consumer group when configured.
func (p *RedisStreamPublisher) provisionStream(ctx context.Context, base resolvedTarget, rendered string) (resolvedTarget, error) {
	base.streamKey = rendered
	if p.keyPrefix != "" {
		base.streamKey = p.keyPrefix + ":" + rendered
	}
	if base.create && base.consumerGroup != "" {
		if err := p.ops.XGroupCreateMkStream(ctx, base.streamKey, base.consumerGroup, "$"); err != nil {
			return resolvedTarget{}, fmt.Errorf("redis_stream %q: create consumer group %q on stream %q: %w",
				p.destID, base.consumerGroup, base.streamKey, err)
		}
		p.logger.Info("created consumer group",
			slog.String("destination", p.destID),
			slog.String("target", rendered),
			slog.String("group", base.consumerGroup),
		)
	}
	return base, nil
}

// Publish marshals msg to JSON and appends it to the configured Redis Stream.
//
// The XADD values map contains:
//   - "payload"         — the full JSON envelope (string)
//   - "id"             — msg.ID
//   - "idempotency_key" — msg.IdempotencyKey
//   - "endpoint"       — msg.Endpoint
//   - "received_at"    — msg.ReceivedAt in RFC3339Nano format
//
// The flat fields duplicate a subset of the envelope for efficient stream
// filtering and observability without parsing the full JSON payload.
//
// target is the UNPREFIXED target Name; the publisher resolves the stream key.
func (p *RedisStreamPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	// targets is written only in NewRedisStreamPublisher (single goroutine);
	// this read is safe to perform concurrently across the server's publish
	// fan-out — mirrors the established SQS race invariant comment.
	rt, err := p.targets.resolve(ctx, target, p.provisionStream)
	if err != nil {
		if errors.Is(err, errTargetNotConfigured) {
			return fmt.Errorf("redis_stream %q: target %q is not configured", p.destID, target)
		}
		return err
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("redis_stream %q: marshal message: %w", p.destID, err)
	}

	values := map[string]any{
		"payload":         string(payload),
		"id":              msg.ID,
		"idempotency_key": msg.IdempotencyKey,
		"endpoint":        msg.Endpoint,
		"received_at":     msg.ReceivedAt.Format(time.RFC3339Nano),
	}

	return p.ops.XAdd(ctx, rt.streamKey, rt.maxLen, values)
}

// Ready pings the Redis server to confirm the connection is live.
func (p *RedisStreamPublisher) Ready(ctx context.Context) error {
	return p.ops.Ping(ctx)
}

// Close releases the underlying Redis connection pool.
func (p *RedisStreamPublisher) Close() error {
	return p.ops.Close()
}
