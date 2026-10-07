package store

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

func TestRuntimeRoleCanOperateWithoutMigrationPrivileges(t *testing.T) {
	adminURL := replaytest.Database(t)
	admin := openTestStore(t, adminURL, defaultLimits())
	ctx := t.Context()
	if _, err := admin.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.pool.Exec(ctx, `CREATE ROLE replay_runtime LOGIN PASSWORD 'synthetic-runtime';
		GRANT USAGE ON SCHEMA public TO replay_runtime;
		GRANT SELECT ON goose_db_version TO replay_runtime;
		GRANT SELECT,INSERT,UPDATE,DELETE ON records,record_artifacts,retry_runs,record_events,usage,delivery_resolutions TO replay_runtime;
		GRANT USAGE,SELECT ON SEQUENCE record_events_id_seq TO replay_runtime`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("replay_runtime", "synthetic-runtime")
	runtime := openTestStore(t, u.String(), defaultLimits())
	runtime.now = admin.now
	if err := runtime.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	f, _ := unknownFailure(t, runtime)
	if _, err := runtime.ResolveUnknown(ctx, resolveRequest(f.Metadata.ID, "least-privilege")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Payload(ctx, f.Metadata.ID, "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Get(ctx, f.Metadata.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.pool.Exec(ctx, `CREATE TABLE public.forbidden(id int)`); err == nil {
		t.Fatal("runtime role could create migration objects")
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE goose_db_version SET version_id=999`); err == nil {
		t.Fatal("runtime role could rewrite schema version")
	}
}

func TestLatestSchemaVersionMatchesTheEmbeddedMigrations(t *testing.T) {
	if got := LatestSchemaVersion(); got != 1 {
		t.Fatalf("LatestSchemaVersion = %d, want 1 (00001_init.sql)", got)
	}
}

func TestOpenRejectsAMalformedURL(t *testing.T) {
	_, err := Open(context.Background(), Options{URL: "postgres://%zz"})
	if err == nil || !strings.Contains(err.Error(), "parse database url") {
		t.Fatalf("Open = %v", err)
	}
}

func TestOpenReportsAnUnreachableDatabase(t *testing.T) {
	_, err := Open(context.Background(), Options{URL: "postgres://replay@127.0.0.1:1/replay?connect_timeout=1"})
	if err == nil {
		t.Fatal("Open succeeded against a closed port")
	}
}

func TestMigrateAppliesEachVersionOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, replaytest.Database(t), defaultLimits())
	if v, err := s.SchemaVersion(ctx); err != nil || v != 0 {
		t.Fatalf("SchemaVersion before migrate = %d, %v; want 0", v, err)
	}
	if err := s.Ready(ctx); err == nil || !strings.Contains(err.Error(), "hookway-replay migrate") {
		t.Fatalf("Ready before migrate = %v, want an error telling the operator to migrate", err)
	}
	if v, _ := s.SchemaVersion(ctx); v != 0 {
		t.Fatal("SchemaVersion or Ready wrote to the database")
	}
	applied, err := s.Migrate(ctx)
	if err != nil || len(applied) != 1 || applied[0] != 1 {
		t.Fatalf("Migrate = %v, %v; want [1]", applied, err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != LatestSchemaVersion() {
		t.Fatalf("SchemaVersion after migrate = %d, %v", v, err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("Ready after migrate = %v", err)
	}
	if again, err := s.Migrate(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second Migrate = %v, %v; want nothing applied", again, err)
	}
}

func TestConcurrentMigrateIsSafe(t *testing.T) {
	ctx := context.Background()
	url := replaytest.Database(t)
	stores := []*Store{openTestStore(t, url, defaultLimits()), openTestStore(t, url, defaultLimits())}
	var wg sync.WaitGroup
	errs := make([]error, len(stores))
	for i, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Migrate(ctx)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d Migrate: %v", i, err)
		}
	}
	if v, err := stores[0].SchemaVersion(ctx); err != nil || v != LatestSchemaVersion() {
		t.Fatalf("SchemaVersion = %d, %v", v, err)
	}
}

func TestSchemaVersionIgnoresRolledBackMigrations(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, replaytest.Database(t), defaultLimits())
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.migrations.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SchemaVersion(ctx); err != nil || got != 0 {
		t.Fatalf("SchemaVersion after rollback = %d, %v; want 0", got, err)
	}
	if err := s.Ready(ctx); err == nil {
		t.Fatal("Ready accepted a database whose migration was rolled back")
	}
}

func TestReadyRejectsFailureShapedVersionOneSchema(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE records RENAME TO failures`); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(t.Context()); err == nil || !strings.Contains(err.Error(), "fresh replay database") {
		t.Fatalf("legacy schema admitted: %v", err)
	}
}
