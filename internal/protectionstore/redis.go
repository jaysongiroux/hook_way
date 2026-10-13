package protectionstore

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

type Redis struct {
	client   redis.UniversalClient
	cfg      protection.Config
	policies map[string]protection.Policy
	knownMu  sync.Mutex
}

// Keep policy names local only for bounded maintenance; admission keys stay hashed.

func OpenRedis(ctx context.Context, cfg protection.Config) (*Redis, error) {
	if cfg.Redis == nil {
		return nil, protection.ErrInvalidPolicy
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Defaults()
	ctx, cancel := bounded(ctx, cfg.StartupTimeout)
	defer cancel()
	r := cfg.Redis
	t := r.TLS
	opts, err := redisconn.Options(config.RedisConfig{URL: r.URL, Mode: r.Mode, Addrs: r.Addrs, MasterName: r.MasterName, Username: r.Username, Password: r.Password, DB: r.DB, TLS: config.RedisTLSConfig{Enabled: t.Enabled, CACert: t.CACert, ClientCert: t.ClientCert, ClientKey: t.ClientKey, ServerName: t.ServerName, InsecureSkipVerify: t.InsecureSkipVerify}}, redisconn.Policy{DialTimeout: cfg.Timeout, ReadTimeout: cfg.Timeout, WriteTimeout: cfg.Timeout, PoolSize: r.PoolSize, MaxRetries: -1, DialerRetries: 1, ContextTimeoutEnabled: true})
	if err != nil {
		return nil, protection.ErrUnavailable
	}
	// Cluster's redirect loop also retries transport errors. Disable it along
	// with node retries; routing refresh may make the next independent call work.
	opts.MaxRedirects = -1
	var client redis.UniversalClient
	if r.Mode == "cluster" {
		client = redis.NewClusterClient(opts.Cluster())
	} else {
		client = redis.NewUniversalClient(opts)
	}
	s := &Redis{client: client, cfg: cfg, policies: map[string]protection.Policy{}}
	if err = client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, protection.ErrUnavailable
	}
	return s, nil
}
func (s *Redis) keys(id string) []string {
	base := "hookway:protection:{" + protection.PolicyKey(s.cfg.Namespace, id) + "}:"
	return []string{base + "policy", base + "entries", base + "expiry", base + "identities"}
}
func (s *Redis) call(ctx context.Context, op string, p protection.Policy, key, owner, expected string, limits ...int) ([]any, error) {
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	raw, _ := json.Marshal(p)
	batch := s.cfg.Cleanup.BatchSize
	if len(limits) > 0 {
		batch = limits[0]
	}
	v, err := s.client.Eval(ctx, redisScript, s.keys(p.ID), op, string(raw), p.Fingerprint(), protection.HashIdentity(key), owner, expected, batch).Slice()
	if err != nil {
		return nil, protection.ErrUnavailable
	}
	if len(v) == 0 {
		return nil, protection.ErrUnavailable
	}
	switch v[0].(string) {
	case "conflict":
		return nil, protection.ErrPolicyConflict
	case "capacity":
		return nil, protection.ErrCapacity
	case "ok":
		return v, nil
	default:
		return nil, protection.ErrUnavailable
	}
}
func (s *Redis) EnsurePolicy(ctx context.Context, p protection.Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := s.call(ctx, "ensure", p, "", "", "")
	if err == nil {
		s.knownMu.Lock()
		s.policies[p.ID] = p
		s.knownMu.Unlock()
	}
	return err
}
func (s *Redis) CheckPolicy(ctx context.Context, p protection.Policy) error {
	_, err := s.call(ctx, "check_policy", p, "", "", "")
	if err == nil {
		s.knownMu.Lock()
		s.policies[p.ID] = p
		s.knownMu.Unlock()
	}
	return err
}
func (s *Redis) PolicyStatus(ctx context.Context, id string) (protection.Status, error) {
	v, err := s.call(ctx, "status", protection.Policy{ID: id}, "", "", "")
	if err != nil {
		return protection.Status{}, err
	}
	var record redisRecord
	if err = json.Unmarshal([]byte(v[1].(string)), &record); err != nil {
		return protection.Status{}, protection.ErrUnavailable
	}
	status := protection.Status{Policy: record.Policy, Fingerprint: record.Fingerprint, Updating: record.Pending != nil}
	if record.Pending != nil {
		status.TargetFingerprint = record.Pending.Fingerprint()
	}
	s.knownMu.Lock()
	s.policies[id] = record.Policy
	s.knownMu.Unlock()
	return status, nil
}

type redisRecord struct {
	Policy      protection.Policy  `json:"policy"`
	Fingerprint string             `json:"fingerprint"`
	Pending     *protection.Policy `json:"pending"`
}

func redisInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
func (s *Redis) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	if key == "" {
		return protection.Decision{}, protection.ErrMissingKey
	}
	if p.Kind != protection.RateLimit {
		return protection.Decision{}, protection.ErrInvalidPolicy
	}
	v, err := s.call(ctx, "allow", p, key, "", "")
	if err != nil {
		return protection.Decision{}, err
	}
	return protection.Decision{Allowed: redisInt(v[1]) == 1, Remaining: redisInt(v[2]), RetryAfter: time.Duration(redisInt(v[3])) * time.Millisecond}, nil
}
func (s *Redis) Reserve(ctx context.Context, p protection.Policy, key string) (protection.Reservation, error) {
	if key == "" {
		return protection.Reservation{}, protection.ErrMissingKey
	}
	if p.Kind != protection.ReplayGuard {
		return protection.Reservation{}, protection.ErrInvalidPolicy
	}
	owner, err := protection.NewOwner()
	if err != nil {
		return protection.Reservation{}, err
	}
	v, err := s.call(ctx, "reserve", p, key, owner, "")
	if err != nil {
		return protection.Reservation{}, err
	}
	accepted := redisInt(v[1]) == 1
	if !accepted {
		owner = ""
	}
	return protection.Reservation{Accepted: accepted, Owner: owner, ExpiresAt: time.UnixMilli(redisInt(v[2]))}, nil
}
func (s *Redis) Release(ctx context.Context, p protection.Policy, key, owner string) error {
	if key == "" {
		return protection.ErrMissingKey
	}
	_, err := s.call(ctx, "release", p, key, owner, "")
	return err
}
func (s *Redis) UpdatePolicy(ctx context.Context, p protection.Policy, expected string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return protection.ErrUnavailable
		}
		v, err := s.call(ctx, "update", p, "", "", expected)
		if err != nil {
			return err
		}
		if redisInt(v[1]) == 1 {
			s.knownMu.Lock()
			s.policies[p.ID] = p
			s.knownMu.Unlock()
			return nil
		}
	}
}
func (s *Redis) GC(ctx context.Context) (int64, error) {
	ctx, cancel := bounded(ctx, s.cfg.Cleanup.Budget)
	defer cancel()
	s.knownMu.Lock()
	policies := make([]protection.Policy, 0, len(s.policies))
	for _, p := range s.policies {
		policies = append(policies, p)
	}
	s.knownMu.Unlock()
	var n int64
	for _, p := range policies {
		if n >= int64(s.cfg.Cleanup.BatchSize) {
			break
		}
		status, err := s.PolicyStatus(ctx, p.ID)
		if err != nil {
			return n, err
		}
		if status.Updating {
			continue
		}
		v, err := s.call(ctx, "gc", status.Policy, "", "", "", s.cfg.Cleanup.BatchSize-int(n))
		if err != nil {
			return n, err
		}
		n += redisInt(v[1])
	}
	return n, nil
}
func (s *Redis) Check(ctx context.Context) error {
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	check := func(ctx context.Context, c *redis.Client) error {
		eviction, err := c.ConfigGet(ctx, "maxmemory-policy").Result()
		if err != nil || eviction["maxmemory-policy"] != "noeviction" {
			return protection.ErrUnavailable
		}
		info, err := c.Info(ctx, "replication").Result()
		if err != nil || !strings.Contains(info, "role:master") {
			return protection.ErrUnavailable
		}
		return nil
	}
	if c, ok := s.client.(*redis.ClusterClient); ok {
		if err := c.ForEachMaster(ctx, check); err != nil {
			return unavailable(err)
		}
		owner, err := protection.NewOwner()
		if err != nil {
			return err
		}
		_, err = s.call(ctx, "probe", protection.Policy{ID: "\x00capability/" + owner}, "", "", "")
		return unavailable(err)
	}
	info, err := s.client.Info(ctx, "replication").Result()
	if err != nil || !strings.Contains(info, "role:master") {
		return protection.ErrUnavailable
	}
	eviction, err := s.client.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil || eviction["maxmemory-policy"] != "noeviction" {
		return protection.ErrUnavailable
	}
	owner, err := protection.NewOwner()
	if err != nil {
		return err
	}
	_, err = s.call(ctx, "probe", protection.Policy{ID: "\x00capability/" + owner}, "", "", "")
	return unavailable(err)
}
func (s *Redis) Close() error { return unavailable(s.client.Close()) }
func (s *Redis) Statistics() protection.Statistics {
	v := s.client.PoolStats()
	return protection.Statistics{InUse: max(int64(0), int64(v.TotalConns)-int64(v.IdleConns)), Idle: int64(v.IdleConns), WaitDuration: time.Duration(v.WaitDurationNs)}
}
