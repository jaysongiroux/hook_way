package protectionstore

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/testenv"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

var pgOnce, redisOnce sync.Once
var pgContainer *tcpostgres.PostgresContainer
var redisContainer *tcredis.RedisContainer
var pgURL, redisURL string
var pgErr, redisErr error
var databases atomic.Int64

func postgresDatabase(t testing.TB) string {
	t.Helper()
	testenv.SkipIfShort(t)
	pgOnce.Do(func() {
		ctx := context.Background()
		pgContainer, pgErr = tcpostgres.Run(ctx, "postgres:17-alpine", tcpostgres.WithDatabase("protection"), tcpostgres.WithUsername("protection"), tcpostgres.WithPassword("protection"), tcpostgres.BasicWaitStrategies())
		if pgErr == nil {
			pgURL, pgErr = pgContainer.ConnectionString(ctx, "sslmode=disable")
		}
	})
	testenv.RequireContainer(t, pgErr, "postgres")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	name := fmt.Sprintf("protection_%d_%d", os.Getpid(), databases.Add(1))
	if _, err = conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(pgURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
func redisAddress(t testing.TB) string {
	t.Helper()
	testenv.SkipIfShort(t)
	redisOnce.Do(func() {
		ctx := context.Background()
		redisContainer, redisErr = tcredis.Run(ctx, "redis:7-alpine")
		if redisErr == nil {
			redisURL, redisErr = redisContainer.ConnectionString(ctx)
		}
	})
	testenv.RequireContainer(t, redisErr, "redis")
	return redisURL
}
func TestMain(m *testing.M) {
	code := m.Run()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if pgContainer != nil {
		_ = pgContainer.Terminate(ctx)
	}
	if redisContainer != nil {
		_ = redisContainer.Terminate(ctx)
	}
	os.Exit(code)
}
func backendPair(t testing.TB, kind string) (protection.Store, protection.Store, protection.Config) {
	t.Helper()
	cfg := protection.Config{Namespace: fmt.Sprintf("test/%d/%d", os.Getpid(), databases.Add(1)), Timeout: 2 * time.Second, Cleanup: protection.CleanupConfig{Budget: 2 * time.Second}}
	switch kind {
	case "memory":
		cfg.Memory = &protection.MemoryConfig{}
		m := protection.NewMemory(cfg.Namespace)
		t.Cleanup(func() { _ = m.Close() })
		return m, m, cfg
	case "redis":
		cfg.Redis = &protection.RedisConfig{URL: redisAddress(t)}
	case "postgres":
		cfg.Postgres = &protection.PostgresConfig{URL: postgresDatabase(t)}
		s, err := OpenPostgres(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
	a, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b, cfg
}
