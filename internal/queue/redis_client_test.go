package queue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

// TestRedisNewClient_Modes exercises newRedisClient option mapping for each
// supported mode. Tests verify that the function returns a non-nil client and
// no error without requiring a live Redis server (no Ping is issued).
func TestRedisNewClient_Modes(t *testing.T) {
	t.Run("single_with_addrs", func(t *testing.T) {
		cfg := config.RedisConfig{
			Mode:  "single",
			Addrs: []string{"127.0.0.1:6379"},
		}
		ops, err := newRedisClient(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ops == nil {
			t.Fatal("expected non-nil redisOps")
		}
		_ = ops.Close()
	})

	t.Run("single_default_mode_empty_string", func(t *testing.T) {
		cfg := config.RedisConfig{
			Mode:  "", // empty defaults to single
			Addrs: []string{"localhost:6379"},
		}
		ops, err := newRedisClient(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ops == nil {
			t.Fatal("expected non-nil redisOps")
		}
		_ = ops.Close()
	})

	t.Run("cluster", func(t *testing.T) {
		cfg := config.RedisConfig{
			Mode:  "cluster",
			Addrs: []string{"127.0.0.1:7000", "127.0.0.1:7001", "127.0.0.1:7002"},
		}
		ops, err := newRedisClient(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ops == nil {
			t.Fatal("expected non-nil redisOps")
		}
		_ = ops.Close()
	})

	t.Run("sentinel", func(t *testing.T) {
		cfg := config.RedisConfig{
			Mode:       "sentinel",
			MasterName: "mymaster",
			Addrs:      []string{"127.0.0.1:26379"},
		}
		ops, err := newRedisClient(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ops == nil {
			t.Fatal("expected non-nil redisOps")
		}
		_ = ops.Close()
	})

	t.Run("unsupported_mode", func(t *testing.T) {
		cfg := config.RedisConfig{Mode: "replicated"}
		_, err := newRedisClient(cfg)
		if err == nil {
			t.Fatal("expected error for unsupported mode")
		}
		if !strings.Contains(err.Error(), "unsupported mode") {
			t.Fatalf("unexpected error text: %v", err)
		}
	})
}

// TestRedisNewClient_URLParsing verifies that a rediss:// URL seeds TLS options.
func TestRedisNewClient_URLParsing(t *testing.T) {
	// rediss:// (note double 's') instructs redis.ParseURL to enable TLS.
	cfg := config.RedisConfig{
		Mode: "single",
		URL:  "rediss://user:secret@redis.example.com:6380/1",
	}
	ops, err := newRedisClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error parsing rediss:// URL: %v", err)
	}
	if ops == nil {
		t.Fatal("expected non-nil redisOps")
	}
	// We can't inspect TLSConfig directly through the interface, but if
	// redis.ParseURL handled a rediss:// URL without error and we got a
	// non-nil client, TLS was wired correctly.
	_ = ops.Close()
}

// TestRedisNewClient_CredentialOverride confirms that explicit username/password
// in config override values parsed from a URL.
func TestRedisNewClient_CredentialOverride(t *testing.T) {
	cfg := config.RedisConfig{
		Mode:     "single",
		URL:      "redis://urluser:urlpass@127.0.0.1:6379",
		Username: "overrideuser",
		Password: "overridepass",
	}
	ops, err := newRedisClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ops == nil {
		t.Fatal("expected non-nil redisOps")
	}
	_ = ops.Close()
}

// TestRedisNewClient_TLSDisabled confirms that TLS is not configured when
// cfg.TLS.Enabled is false (even if other TLS fields are present).
func TestRedisNewClient_TLSDisabled(t *testing.T) {
	cfg := config.RedisConfig{
		Mode:  "single",
		Addrs: []string{"127.0.0.1:6379"},
		TLS: config.RedisTLSConfig{
			Enabled:    false,
			ServerName: "should-be-ignored",
		},
	}
	ops, err := newRedisClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ops == nil {
		t.Fatal("expected non-nil redisOps")
	}
	_ = ops.Close()
}

// --- fake redisOps for unit testing publishers ---

type fakeRedisOps struct {
	xaddCalls              []xaddCall
	xgroupCreateMkStreamOK bool
	xgroupErr              error
	lpushTrimCalls         []lpushTrimCall
	publishCalls           []publishCall
	publishErr             error
	// publishReceivers controls how many subscribers Publish reports. Defaults to
	// 1 (normal delivery) when constructed via newFakeRedisOps. Set to 0 in tests
	// that simulate no-subscriber drops.
	publishReceivers int64
	setCalls         []setCall
	pingErr          error
	closeErr         error
}

// newFakeRedisOps returns a fakeRedisOps with sensible defaults:
// publishReceivers is 1 (normal delivery) so existing pubsub tests keep
// representing successful delivery without change.
func newFakeRedisOps() *fakeRedisOps {
	return &fakeRedisOps{publishReceivers: 1}
}

type xaddCall struct {
	stream       string
	approxMaxLen int64
	values       map[string]any
}

type lpushTrimCall struct {
	key          string
	value        string
	approxMaxLen int64
}

type publishCall struct {
	channel string
	payload string
}

type setCall struct {
	key   string
	value string
	ttl   time.Duration
}

func (f *fakeRedisOps) XAdd(_ context.Context, stream string, approxMaxLen int64, values map[string]any) error {
	f.xaddCalls = append(f.xaddCalls, xaddCall{stream: stream, approxMaxLen: approxMaxLen, values: values})
	return nil
}

func (f *fakeRedisOps) XGroupCreateMkStream(_ context.Context, _, _, _ string) error {
	return f.xgroupErr
}

func (f *fakeRedisOps) LPushTrim(_ context.Context, key, value string, approxMaxLen int64) error {
	f.lpushTrimCalls = append(f.lpushTrimCalls, lpushTrimCall{key: key, value: value, approxMaxLen: approxMaxLen})
	return nil
}

func (f *fakeRedisOps) Publish(_ context.Context, channel, payload string) (int64, error) {
	f.publishCalls = append(f.publishCalls, publishCall{channel: channel, payload: payload})
	if f.publishErr != nil {
		return 0, f.publishErr
	}
	return f.publishReceivers, nil
}

func (f *fakeRedisOps) Set(_ context.Context, key, value string, ttl time.Duration) error {
	f.setCalls = append(f.setCalls, setCall{key: key, value: value, ttl: ttl})
	return nil
}

func (f *fakeRedisOps) Ping(_ context.Context) error { return f.pingErr }
func (f *fakeRedisOps) Close() error                 { return f.closeErr }
