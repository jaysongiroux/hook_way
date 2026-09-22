package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
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

// newRedisClient builds a redisOps from the shared RedisConfig block.
// redis.NewUniversalClient selects the correct client type automatically:
//   - "single" (or ""): single-node Client (or URL-parsed address)
//   - "cluster":         ClusterClient
//   - "sentinel":        FailoverClient (Sentinel-managed primary)
func newRedisClient(cfg config.RedisConfig) (redisOps, error) {
	opts := &redis.UniversalOptions{
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		PoolSize:     10,
	}

	switch cfg.Mode {
	case "", "single":
		if cfg.URL != "" {
			parsed, err := redis.ParseURL(cfg.URL)
			if err != nil {
				return nil, fmt.Errorf("redis: parse url: %w", err)
			}
			// Seed from the parsed URL; explicit cfg fields override below.
			opts.Addrs = []string{parsed.Addr}
			if parsed.Username != "" {
				opts.Username = parsed.Username
			}
			if parsed.Password != "" {
				opts.Password = parsed.Password
			}
			if parsed.DB != 0 {
				opts.DB = parsed.DB
			}
			if parsed.TLSConfig != nil {
				opts.TLSConfig = parsed.TLSConfig
			}
		} else if len(cfg.Addrs) > 0 {
			opts.Addrs = cfg.Addrs
		}

	case "cluster":
		opts.Addrs = cfg.Addrs

	case "sentinel":
		opts.MasterName = cfg.MasterName
		opts.Addrs = cfg.Addrs

	default:
		return nil, fmt.Errorf("redis: unsupported mode %q (expected single, cluster, or sentinel)", cfg.Mode)
	}

	// Explicit overrides take precedence over values parsed from the URL.
	if cfg.Username != "" {
		opts.Username = cfg.Username
	}
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	if cfg.DB != 0 {
		opts.DB = cfg.DB
	}

	// Build TLS config from the explicit TLS block when enabled.
	if cfg.TLS.Enabled {
		tlsCfg, err := buildRedisTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsCfg
	}

	return &goRedisOps{client: redis.NewUniversalClient(opts)}, nil
}

// buildRedisTLSConfig assembles a *tls.Config from the Redis TLS options.
// It mirrors buildTLSConfig (rabbitmq.go) so the two implementations stay
// consistent.
func buildRedisTLSConfig(c config.RedisTLSConfig) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.ServerName != "" {
		cfg.ServerName = c.ServerName
	}
	if c.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // intentional, user-opted in
	}
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("read redis tls ca_cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("redis tls ca_cert %q contained no valid certificates", c.CACert)
		}
		cfg.RootCAs = pool
	}
	if c.ClientCert != "" || c.ClientKey != "" {
		if c.ClientCert == "" || c.ClientKey == "" {
			return nil, errors.New("redis tls client_cert and client_key must both be set for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCert, c.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load redis tls client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
