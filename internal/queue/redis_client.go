package queue

import (
	"context"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// redisOps is the thin, mockable interface that redis_* publishers depend on.
// It wraps the subset of go-redis operations used by the publisher family so
// unit tests can inject a fake without touching a live server.
type redisOps interface {
	// XAdd appends values to a Stream. When approxMaxLen > 0, MAXLEN ~ is used
	// to keep the stream bounded (approximate trim). Passing 0 means no trim.
	XAdd(ctx context.Context, stream string, approxMaxLen int64, values map[string]any) error

	// XGroupCreateMkStream creates a consumer group on stream, creating the
	// stream if it does not exist (MKSTREAM). BUSYGROUP errors (group already
	// exists) are silently ignored so the call is idempotent.
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) error

	// LPushTrim prepends value to the list at key. When approxMaxLen > 0, the
	// list is trimmed to the newest approxMaxLen elements after the push.
	LPushTrim(ctx context.Context, key, value string, approxMaxLen int64) error

	// Publish sends payload to channel (Redis Pub/Sub) and returns the number of
	// clients that received the message. A count of 0 means no subscriber was
	// listening and the message was dropped (Redis pub/sub is at-most-once).
	Publish(ctx context.Context, channel, payload string) (int64, error)

	// Set stores value at key. When ttl > 0 the key expires after ttl; when
	// ttl == 0 the key has no expiry.
	Set(ctx context.Context, key, value string, ttl time.Duration) error

	// Ping checks liveness of the Redis connection.
	Ping(ctx context.Context) error

	// Close releases the underlying connection pool.
	Close() error
}

// goRedisOps wraps a redis.UniversalClient and implements redisOps.
type goRedisOps struct {
	client redis.UniversalClient
}

func (r *goRedisOps) XAdd(ctx context.Context, stream string, approxMaxLen int64, values map[string]any) error {
	args := &redis.XAddArgs{
		Stream: stream,
		Values: values,
	}
	if approxMaxLen > 0 {
		args.MaxLen = approxMaxLen
		args.Approx = true
	}
	return r.client.XAdd(ctx, args).Err()
}

func (r *goRedisOps) XGroupCreateMkStream(ctx context.Context, stream, group, start string) error {
	err := r.client.XGroupCreateMkStream(ctx, stream, group, start).Err()
	if err != nil && strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

func (r *goRedisOps) LPushTrim(ctx context.Context, key, value string, approxMaxLen int64) error {
	if err := r.client.LPush(ctx, key, value).Err(); err != nil {
		return err
	}
	if approxMaxLen > 0 {
		return r.client.LTrim(ctx, key, 0, approxMaxLen-1).Err()
	}
	return nil
}

func (r *goRedisOps) Publish(ctx context.Context, channel, payload string) (int64, error) {
	return r.client.Publish(ctx, channel, payload).Result()
}

func (r *goRedisOps) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if ttl < 0 {
		ttl = 0
	}
	return r.client.Set(ctx, key, value, ttl).Err()
}

func (r *goRedisOps) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *goRedisOps) Close() error {
	return r.client.Close()
}

// newRedisClient keeps the publisher's historical socket/pool defaults and
// retry settings; consumers of redisconn choose their own policy.
func newRedisClient(cfg config.RedisConfig) (redisOps, error) {
	client, err := redisconn.New(cfg, redisconn.Policy{
		DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, PoolSize: 10,
		MaxRetries: 3, MinRetryBackoff: 8 * time.Millisecond,
		MaxRetryBackoff: 512 * time.Millisecond, DialerRetries: 5,
		DialerRetryTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	return &goRedisOps{client: client}, nil
}
