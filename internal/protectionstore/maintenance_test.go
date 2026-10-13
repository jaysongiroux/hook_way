package protectionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func TestInterruptedUpdateResumesWithoutReset(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			a, b, _ := backendPair(t, backend)
			ctx := context.Background()
			p := protection.Policy{Kind: protection.RateLimit, ID: "maintenance", KeyScope: "ip", Strategy: protection.TokenBucket, RPM: 1, Burst: 1, MaxEntries: 1000}
			if err := a.EnsurePolicy(ctx, p); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 250; i++ {
				d, err := a.Allow(ctx, p, fmt.Sprint(i))
				if err != nil || !d.Allowed {
					t.Fatal(d, err)
				}
			}
			next := p
			next.Burst = 2
			switch s := a.(type) {
			case *Redis:
				v, err := s.call(ctx, "update", next, "", "", p.Fingerprint())
				if err != nil || redisInt(v[1]) != 0 {
					t.Fatal(v, err)
				}
			case *Postgres:
				done, err := s.updateBatch(ctx, next, p.Fingerprint())
				if err != nil || done {
					t.Fatal(done, err)
				}
			}
			status, err := b.PolicyStatus(ctx, p.ID)
			if err != nil || !status.Updating || status.Fingerprint != p.Fingerprint() || status.TargetFingerprint != next.Fingerprint() {
				t.Fatal(status, err)
			}
			if _, err = b.Allow(ctx, p, "0"); !errors.Is(err, protection.ErrPolicyConflict) {
				t.Fatal(err)
			}
			if _, err = b.Allow(ctx, next, "0"); !errors.Is(err, protection.ErrPolicyConflict) {
				t.Fatal(err)
			}
			wrong := next
			wrong.Burst = 3
			if err = b.UpdatePolicy(ctx, wrong, p.Fingerprint()); !errors.Is(err, protection.ErrPolicyConflict) {
				t.Fatal(err)
			}
			if err = b.UpdatePolicy(ctx, next, p.Fingerprint()); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 250; i++ {
				d, err := a.Allow(ctx, next, fmt.Sprint(i))
				if err != nil || d.Allowed {
					t.Fatalf("identity%d quota reset on resume: %+v %v", i, d, err)
				}
			}
		})
	}
}
func TestPostgresMigrationsStartupPermissionsAndConcurrentMigration(t *testing.T) {
	ctx := context.Background()
	cfg := protection.Config{Namespace: "migrations", Timeout: 2 * time.Second, Postgres: &protection.PostgresConfig{URL: postgresDatabase(t)}}
	s, err := OpenPostgres(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v, err := s.SchemaVersion(ctx); err != nil || v != 0 {
		t.Fatal(v, err)
	}
	if err = s.Check(ctx); err == nil {
		t.Fatal("unmigrated database accepted")
	}
	if _, err = Open(ctx, cfg); err == nil {
		t.Fatal("serving implicitly migrated empty database")
	}
	other, err := OpenPostgres(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	results := make(chan error, 2)
	for _, client := range []*Postgres{s, other} {
		go func(client *Postgres) { _, err := client.Migrate(ctx); results <- err }(client)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if applied, err := s.Migrate(ctx); err != nil || len(applied) != 0 {
		t.Fatal(applied, err)
	}
	if err = s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `CREATE ROLE protection_reader LOGIN PASSWORD 'reader'; GRANT USAGE ON SCHEMA hookway_protection TO protection_reader; GRANT SELECT ON ALL TABLES IN SCHEMA hookway_protection TO protection_reader`); err != nil {
		t.Fatal(err)
	}
	readerCfg := cfg
	readerCfg.Postgres = &protection.PostgresConfig{URL: withRoleURL(t, cfg.Postgres.URL, "protection_reader", "reader")}
	reader, err := OpenPostgres(ctx, readerCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err = reader.Check(ctx); err == nil {
		t.Fatal("read-only runtime role accepted")
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO hookway_protection.goose_db_version(version_id,is_applied) VALUES(999,true)`); err != nil {
		t.Fatal(err)
	}
	if err = s.Check(ctx); err == nil {
		t.Fatal("unknown future schema accepted")
	}
}
func withRoleURL(t testing.TB, raw, user, password string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
func TestRedisStoredIdentityAndSameSlotKeys(t *testing.T) {
	a, _, _ := backendPair(t, "redis")
	s := a.(*Redis)
	ctx := context.Background()
	p := protection.Policy{Kind: protection.ReplayGuard, ID: "keys", KeyScope: "identity", TTL: time.Minute, MaxEntries: 10}
	if err := s.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, p, "private/customer/key"); err != nil {
		t.Fatal(err)
	}
	fields, err := s.client.HKeys(ctx, s.keys(p.ID)[1]).Result()
	if err != nil || len(fields) != 1 || fields[0] != protection.HashIdentity("private/customer/key") {
		t.Fatal(fields, err)
	}
	for _, key := range s.keys(p.ID) {
		if !strings.Contains(key, "{"+protection.PolicyKey(s.cfg.Namespace, p.ID)+"}") {
			t.Fatal("cross-slot script key", key)
		}
	}
	raw, err := s.client.Get(ctx, s.keys(p.ID)[0]).Result()
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if json.Unmarshal([]byte(raw), &stored) != nil {
		t.Fatal("invalid persisted policy")
	}
}

func TestPostgresRuntimeRoleNeedsNoMigrationPrivileges(t *testing.T) {
	ctx := t.Context()
	cfg := protection.Config{Namespace: "runtime-grants", Timeout: 2 * time.Second, Postgres: &protection.PostgresConfig{URL: postgresDatabase(t)}}
	admin, err := OpenPostgres(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	if _, err = admin.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Test-only role, restricted to exactly the live adapter's tables.
	if _, err = admin.pool.Exec(ctx, `CREATE ROLE protection_runtime LOGIN PASSWORD 'synthetic-runtime'; GRANT USAGE ON SCHEMA hookway_protection TO protection_runtime; GRANT SELECT ON hookway_protection.goose_db_version TO protection_runtime; GRANT SELECT,INSERT,UPDATE,DELETE ON hookway_protection.policies,hookway_protection.entries TO protection_runtime`); err != nil {
		t.Fatal(err)
	}
	runtimeCfg := cfg
	runtimeCfg.Postgres = &protection.PostgresConfig{URL: withRoleURL(t, cfg.Postgres.URL, "protection_runtime", "synthetic-runtime")}
	runtime, err := Open(ctx, runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	p := protection.Policy{Kind: protection.ReplayGuard, ID: "live", KeyScope: "id", TTL: time.Minute, MaxEntries: 10}
	if err = runtime.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if r, err := runtime.Reserve(ctx, p, "event"); err != nil || !r.Accepted {
		t.Fatal(r, err)
	}
	pg := runtime.(*Postgres)
	if _, err = pg.pool.Exec(ctx, `CREATE TABLE hookway_protection.forbidden(id int)`); err == nil {
		t.Fatal("runtime role could create migration objects")
	}
	if _, err = pg.pool.Exec(ctx, `UPDATE hookway_protection.goose_db_version SET version_id=999`); err == nil {
		t.Fatal("runtime role could rewrite schema version")
	}
}
