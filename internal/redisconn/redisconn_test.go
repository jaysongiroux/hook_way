package redisconn

import (
	"crypto/tls"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/redis/go-redis/v9"
)

func TestOptionsURLAndOverrides(t *testing.T) {
	cfg := config.RedisConfig{
		URL:   "rediss://urluser:urlpass@redis.example:6380/4?read_timeout=1s&max_retries=8", // #nosec G101 -- parse fixture
		Addrs: []string{"ignored:6379"},
	}
	policy := Policy{DialTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second, PoolSize: 7, MaxRetries: -1}
	opts, err := Options(cfg, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Addrs, []string{"redis.example:6380"}) || opts.Username != "urluser" || opts.Password != "urlpass" || opts.DB != 4 {
		t.Fatalf("URL connection fields = %+v", opts)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.ServerName != "redis.example" || opts.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("rediss URL TLS = %+v", opts.TLSConfig)
	}
	if opts.DialTimeout != policy.DialTimeout || opts.ReadTimeout != policy.ReadTimeout || opts.WriteTimeout != policy.WriteTimeout || opts.PoolSize != 7 || opts.MaxRetries != -1 {
		t.Fatalf("URL must not override socket/retry policy: %+v", opts)
	}

	cfg.Username, cfg.Password, cfg.DB = "explicituser", "explicitpass", 6
	cfg.TLS = config.RedisTLSConfig{Enabled: true, ServerName: "custom.example", InsecureSkipVerify: true}
	opts, err = Options(cfg, policy)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Username != cfg.Username || opts.Password != cfg.Password || opts.DB != 6 {
		t.Fatalf("explicit credential/database overrides = %+v", opts)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.ServerName != "custom.example" || !opts.TLSConfig.InsecureSkipVerify || opts.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("explicit TLS override = %+v", opts.TLSConfig)
	}
}

func TestOptionsTLSGateAndErrors(t *testing.T) {
	for _, url := range []string{"redis://localhost:6379", "rediss://localhost:6380"} {
		cfg := config.RedisConfig{URL: url, TLS: config.RedisTLSConfig{CACert: "/missing/ca.pem", ServerName: "ignored"}}
		opts, err := Options(cfg, Policy{})
		if err != nil {
			t.Fatalf("disabled explicit TLS must ignore its material: %v", err)
		}
		if strings.HasPrefix(url, "rediss:") {
			if opts.TLSConfig == nil || opts.TLSConfig.ServerName != "localhost" {
				t.Fatalf("disabled explicit TLS must retain URL TLS: %+v", opts.TLSConfig)
			}
		} else if opts.TLSConfig != nil {
			t.Fatalf("plaintext URL unexpectedly gained TLS: %+v", opts.TLSConfig)
		}
	}
	opts, err := Options(config.RedisConfig{URL: "rediss://localhost:6380", TLS: config.RedisTLSConfig{Enabled: true}}, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.ServerName != "" || opts.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("enabled empty TLS block must replace URL TLS: %+v", opts.TLSConfig)
	}
	for name, cfg := range map[string]config.RedisConfig{
		"invalid URL":      {URL: "https://localhost"},
		"unsupported mode": {Mode: "replicated"},
		"missing CA":       {TLS: config.RedisTLSConfig{Enabled: true, CACert: "/missing/ca.pem"}},
		"partial mTLS":     {TLS: config.RedisTLSConfig{Enabled: true, ClientKey: "only-key.pem"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Options(cfg, Policy{}); err == nil || !strings.Contains(err.Error(), "redis") {
				t.Fatalf("Options error = %v, want Redis-labelled error", err)
			}
			if client, err := New(cfg, Policy{}); err == nil || client != nil {
				t.Fatalf("New = %T, %v, want nil and error", client, err)
			}
		})
	}
}

func TestTopologyOptionsAndLazyClients(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     config.RedisConfig
		cluster bool
	}{
		{"default", config.RedisConfig{Addrs: []string{"localhost:6379"}}, false},
		{"single", config.RedisConfig{Mode: "single", Addrs: []string{"localhost:6379"}}, false},
		{"cluster", config.RedisConfig{Mode: "cluster", Addrs: []string{"localhost:7000", "localhost:7001"}, URL: "invalid-url-ignored"}, true},
		{"sentinel", config.RedisConfig{Mode: "sentinel", Addrs: []string{"localhost:26379", "localhost:26380"}, MasterName: "primary", URL: "invalid-url-ignored"}, false},
		// Preserve go-redis's actual historical selection at the factory boundary.
		{"cluster with one seed", config.RedisConfig{Mode: "cluster", Addrs: []string{"localhost:7000"}}, false},
		{"single with two addresses", config.RedisConfig{Mode: "single", Addrs: []string{"localhost:7000", "localhost:7001"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := Options(tc.cfg, Policy{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(opts.Addrs, tc.cfg.Addrs) || opts.MasterName != tc.cfg.MasterName {
				t.Fatalf("topology options = %+v", opts)
			}
			// No endpoint must be reachable: construction is lazy, with no Ping.
			client, err := New(tc.cfg, Policy{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Error(err)
				}
			})
			if stats := client.PoolStats(); stats.TotalConns != 0 {
				t.Fatalf("factory opened connections during lazy construction: %+v", stats)
			}
			_, cluster := client.(*redis.ClusterClient)
			if cluster != tc.cluster {
				t.Fatalf("client = %T, want cluster=%t", client, tc.cluster)
			}
			if !cluster {
				if _, ok := client.(*redis.Client); !ok {
					t.Fatalf("client = %T", client)
				}
			}
			// Options must not alias the authored configuration address list.
			opts.Addrs[0] = "mutated:1"
			if tc.cfg.Addrs[0] == "mutated:1" {
				t.Fatal("options alias config address slice")
			}
		})
	}
}

func TestConsumerPolicy(t *testing.T) {
	policy := Policy{
		DialTimeout: time.Second, ReadTimeout: -1, WriteTimeout: 2 * time.Second,
		PoolSize: 3, MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1,
		DialerRetries: 1, DialerRetryTimeout: 100 * time.Millisecond, ContextTimeoutEnabled: true,
	}
	client, err := New(config.RedisConfig{URL: "redis://localhost:6379"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	opts := client.(*redis.Client).Options()
	if opts.DialTimeout != time.Second || opts.ReadTimeout != 0 || opts.WriteTimeout != 2*time.Second || opts.PoolSize != 3 || opts.MaxRetries != 0 || opts.MinRetryBackoff != 0 || opts.MaxRetryBackoff != 0 || opts.DialerRetries != 1 || opts.DialerRetryTimeout != 100*time.Millisecond || !opts.ContextTimeoutEnabled {
		t.Fatalf("effective consumer policy = %+v", opts)
	}
}
