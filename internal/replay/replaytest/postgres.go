// Package replaytest starts the PostgreSQL container that the replay
// service's container tests share. Each test gets a fresh database, so tests
// stay isolated without paying for a container each.
package replaytest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// ContainerID identifies only the disposable PostgreSQL owned by this fixture.
// Fault tests use it instead of searching unrelated containers on the host.
func ContainerID(t testing.TB) string {
	t.Helper()
	testenv.SkipIfShort(t)
	once.Do(start)
	testenv.RequireContainer(t, startErr, "postgres")
	return container.GetContainerID()
}

// BackupRestore copies a pg_dump archive out of the container, then restores it
// into a fresh database. Callers must quiesce writers before invoking it.
func BackupRestore(t testing.TB, source string) string {
	t.Helper()
	u, err := url.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	database := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(database, "replay_test_") || strings.ContainsAny(database, "/\\ \n\r") {
		t.Fatal("backup requires a replaytest database")
	}
	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		code, output, err := container.Exec(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(output)
		if err != nil || code != 0 {
			t.Fatalf("database archive command failed (exit %d): %v %s", code, err, data)
		}
	}
	archive := "/tmp/" + database + ".dump"
	started := time.Now()
	run("pg_dump", "-U", "replay", "-Fc", "-f", archive, database)
	dumpDuration := time.Since(started)
	reader, err := container.CopyFileFromContainer(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || len(data) == 0 {
		t.Fatalf("read database archive: %v", err)
	}
	local := filepath.Join(t.TempDir(), "replay.dump")
	if err := os.WriteFile(local, data, 0o600); err != nil {
		t.Fatal(err)
	}
	restored := Database(t)
	target, err := url.Parse(restored)
	if err != nil {
		t.Fatal(err)
	}
	importPath := archive + ".restored"
	if err := container.CopyFileToContainer(ctx, local, importPath, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreStarted := time.Now()
	run("pg_restore", "-U", "replay", "--exit-on-error", "--no-owner", "-d", strings.TrimPrefix(target.Path, "/"), importPath)
	t.Logf("quiescent backup archive_bytes=%d archive_sha256=%x dump=%s restore=%s total=%s source=%s target=%s", len(data), sha256.Sum256(data), dumpDuration, time.Since(restoreStarted), time.Since(started), database, strings.TrimPrefix(target.Path, "/"))
	return restored
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
