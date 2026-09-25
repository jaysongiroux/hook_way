// Package replaytest starts the PostgreSQL container that the replay
// service's container tests share. Each test gets a fresh database, so tests
// stay isolated without paying for a container each.
package replaytest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jaysongiroux/hook_way/internal/testenv"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	adminURL  string
	startErr  error
	databases atomic.Int64
)

// Database returns the URL of a new, empty database on the shared
// container. It skips under -short and when Docker is unavailable, and fails
// when HOOKWAY_REQUIRE_INTEGRATION=1.
func Database(t testing.TB) string {
	t.Helper()
	testenv.SkipIfShort(t)
	once.Do(start)
	testenv.RequireContainer(t, startErr, "postgres")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	name := fmt.Sprintf("replay_test_%d_%d", os.Getpid(), databases.Add(1))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse admin url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// Main runs a package's tests, then stops the shared container. Call it from
// TestMain: func TestMain(m *testing.M) { os.Exit(replaytest.Main(m)) }.
func Main(m *testing.M) int {
	code := m.Run()
	if container != nil {
		_ = container.Terminate(context.Background())
	}
	return code
}

func start() {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("replay"),
		tcpostgres.WithUsername("replay"),
		tcpostgres.WithPassword("replay"),
		tcpostgres.BasicWaitStrategies(),
	)
	if c != nil {
		container = c
	}
	if err != nil {
		startErr = err
		return
	}
	adminURL, startErr = c.ConnectionString(ctx, "sslmode=disable")
}
