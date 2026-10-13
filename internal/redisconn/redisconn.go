// Package redisconn builds native Redis connections without publisher commands.
// It must stay outside the browser/WASM dependency graph.
package redisconn

import (
	"fmt"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
	"github.com/redis/go-redis/v9"
)

// Policy supplies socket, pool, and retry settings for a consumer. Zero values
// use go-redis defaults. MaxRetries -1 disables command retries; socket
// timeouts and retry backoff use go-redis semantics. DialerRetries counts dial
// attempts (values <= 0 use the SDK default). URL query parameters do not
// override this policy.
type Policy struct {
	DialTimeout           time.Duration
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
	PoolSize              int
	MaxRetries            int
	MinRetryBackoff       time.Duration
	MaxRetryBackoff       time.Duration
	DialerRetries         int
	DialerRetryTimeout    time.Duration
	ContextTimeoutEnabled bool
}

// Options resolves topology, URL defaults, explicit credentials, and TLS.
// Empty credentials and DB zero leave URL values intact, matching existing
// publisher configuration. The caller's address slice is not retained.
func Options(cfg config.RedisConfig, policy Policy) (*redis.UniversalOptions, error) {
	opts := &redis.UniversalOptions{
		DialTimeout:           policy.DialTimeout,
		ReadTimeout:           policy.ReadTimeout,
		WriteTimeout:          policy.WriteTimeout,
		PoolSize:              policy.PoolSize,
		MaxRetries:            policy.MaxRetries,
		MinRetryBackoff:       policy.MinRetryBackoff,
		MaxRetryBackoff:       policy.MaxRetryBackoff,
		DialerRetries:         policy.DialerRetries,
		DialerRetryTimeout:    policy.DialerRetryTimeout,
		ContextTimeoutEnabled: policy.ContextTimeoutEnabled,
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
			opts.Addrs = append([]string(nil), cfg.Addrs...)
		}

	case "cluster":
		opts.Addrs = append([]string(nil), cfg.Addrs...)

	case "sentinel":
		opts.MasterName = cfg.MasterName
		opts.Addrs = append([]string(nil), cfg.Addrs...)

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

	// Explicit TLS replaces rediss:// TLS only when enabled. Other TLS fields
	// remain ignored when disabled, including invalid certificate paths.
	if cfg.TLS.Enabled {
		c := cfg.TLS
		tlsCfg, err := (tlsfiles.Files{
			CACert: c.CACert, ClientCert: c.ClientCert, ClientKey: c.ClientKey,
			ServerName: c.ServerName, InsecureSkipVerify: c.InsecureSkipVerify,
		}).Build("redis tls", tlsfiles.BuildOptions{PreserveCAPath: true})
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsCfg
	}
	return opts, nil
}

// New constructs a lazy client without Ping or other startup network calls.
// UniversalClient chooses Sentinel when MasterName is set, cluster for multiple
// addresses, and single-node otherwise. It retains that library selection even
// for a cluster mode with only one seed address.
func New(cfg config.RedisConfig, policy Policy) (redis.UniversalClient, error) {
	opts, err := Options(cfg, policy)
	if err != nil {
		return nil, err
	}
	return redis.NewUniversalClient(opts), nil
}
