package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jaysongiroux/hook_way/internal/vars"
	"log/slog"
)

// RedisListTarget describes a single Redis List destination. Name is the
// logical name used as the Publish "target" argument and as the base for the
// resolved list key (after KeyPrefix is applied).
type RedisListTarget struct {
	// Name is the logical target identifier and the unprefixed list key base.
	Name string
	// MaxLen is the cap passed to LPushTrim. When > 0 the list is trimmed to
	// the newest MaxLen elements after each LPUSH (exact trim, not approximate).
	// Zero means unbounded (no trim).
	MaxLen int64
	// Template is set for dynamic routes (target contains {variable}
	// placeholders); rendered targets the matcher accepts get this target's
	// settings with the list key derived from the rendered name.
	Template vars.Matcher
}

// RedisListConfig carries all configuration consumed by RedisListPublisher.
// The factory builds this from the parsed config and passes it here.
//
// KeyPrefix is applied INSIDE NewRedisListPublisher (prefix + ":" + Name).
// The factory must NOT apply the prefix again; just set cfg.KeyPrefix and pass
// the original target Names — the publisher owns key resolution.
type RedisListConfig struct {
	// KeyPrefix is prepended to every target Name as "<prefix>:<name>".
	// Leave empty for no prefix.
	KeyPrefix string
	// DestinationID is the destination identifier used in error messages.
	DestinationID string
	// Logger is an optional structured logger. May be nil.
	Logger *slog.Logger
	// Targets enumerates the lists this publisher routes messages to.
	Targets []RedisListTarget
}

// resolvedListTarget is the internal state stored in the targets map after the
// constructor has applied KeyPrefix and resolved the list key.
type resolvedListTarget struct {
	listKey string // fully resolved (prefixed) Redis list key
	maxLen  int64
}

// RedisListPublisher publishes webhook envelopes to Redis Lists via LPUSH.
// It satisfies the Publisher interface. The targets map is written only in
// NewRedisListPublisher and is read-only during Publish, which makes
// concurrent fan-out reads safe without locking.
type RedisListPublisher struct {
	ops    redisOps
	destID string
	logger *slog.Logger
	// targets maps the unprefixed target Name to its resolved state.
	// Written once in the constructor; read-only during Publish.
	targets   *targetResolver[resolvedListTarget]
	keyPrefix string
}

// NewRedisListPublisher constructs a RedisListPublisher.
//
// Key resolution: KeyPrefix is applied here as "<prefix>:<name>" when
// KeyPrefix != "". The resolved key is stored; Publish looks up by the original
// (unprefixed) Name so callers always use logical names, not prefixed keys.
//
// Lists are implicit in Redis — there is no create concept. No commands are
// issued at startup beyond populating the targets map.
func NewRedisListPublisher(_ context.Context, ops redisOps, cfg RedisListConfig) (*RedisListPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &RedisListPublisher{
		ops:       ops,
		destID:    cfg.DestinationID,
		logger:    logger,
		targets:   newTargetResolver[resolvedListTarget](0, logger),
		keyPrefix: cfg.KeyPrefix,
	}

	for _, t := range cfg.Targets {
		// Resolve the list key: apply KeyPrefix once here.
		listKey := t.Name
		if cfg.KeyPrefix != "" {
			listKey = cfg.KeyPrefix + ":" + t.Name
		}

		p.logger.Debug("redis_list: registered target",
			slog.String("destination", cfg.DestinationID),
			slog.String("target", t.Name),
			slog.String("list_key", listKey),
			slog.Int64("max_len", t.MaxLen),
		)

		state := resolvedListTarget{listKey: listKey, maxLen: t.MaxLen}
		if t.Template.IsTemplate() {
			p.targets.addTemplate(t.Template, state)
		} else {
			p.targets.addStatic(t.Name, state)
		}
	}

	return p, nil
}

// Publish marshals msg to JSON and prepends it to the configured Redis List.
//
// target is the UNPREFIXED target Name; the publisher resolves the list key.
// When the target's MaxLen > 0, LPushTrim trims the list to keep only the
// newest MaxLen elements (exact trim via LTRIM 0 maxLen-1).
func (p *RedisListPublisher) Publish(ctx context.Context, _, target string, msg Message) error {
	// targets is written only in NewRedisListPublisher (single goroutine);
	// this read is safe to perform concurrently across the server's publish
	// fan-out — mirrors the established SQS race invariant comment.
	rt, err := p.targets.resolve(ctx, target, p.provisionList)
	if err != nil {
		return fmt.Errorf("redis_list %q: target %q is not configured", p.destID, target)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("redis_list %q: marshal message: %w", p.destID, err)
	}

	return p.ops.LPushTrim(ctx, rt.listKey, string(payload), rt.maxLen)
}

// provisionList clones a dynamic route's settings for a rendered target,
// deriving the prefixed list key from the rendered name.
func (p *RedisListPublisher) provisionList(_ context.Context, base resolvedListTarget, rendered string) (resolvedListTarget, error) {
	base.listKey = rendered
	if p.keyPrefix != "" {
		base.listKey = p.keyPrefix + ":" + rendered
	}
	return base, nil
}

// Ready pings the Redis server to confirm the connection is live.
func (p *RedisListPublisher) Ready(ctx context.Context) error {
	return p.ops.Ping(ctx)
}

// Close releases the underlying Redis connection pool.
func (p *RedisListPublisher) Close() error {
	return p.ops.Close()
}
