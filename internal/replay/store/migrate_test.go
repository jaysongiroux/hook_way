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
		GRANT SELECT,INSERT,UPDATE,DELETE ON failures,failure_artifacts,retry_runs,failure_events,usage,failure_resolutions TO replay_runtime;
		GRANT USAGE,SELECT ON SEQUENCE failure_events_id_seq TO replay_runtime`); err != nil {
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
	if got := LatestSchemaVersion(); got != 2 {
		t.Fatalf("LatestSchemaVersion = %d, want 2 (00002_operator_resolution.sql)", got)
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
	if err != nil || len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
		t.Fatalf("Migrate = %v, %v; want [1 2]", applied, err)
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
	if got, err := s.SchemaVersion(ctx); err != nil || got != 1 {
		t.Fatalf("SchemaVersion after rollback = %d, %v; want 1", got, err)
	}
	if err := s.Ready(ctx); err == nil {
		t.Fatal("Ready accepted a database whose migration was rolled back")
	}
}

func TestUpgradeFromVersionOnePreservesArtifactsAndOutcome(t *testing.T) {
	s := openTestStore(t, replaytest.Database(t), defaultLimits())
	ctx := t.Context()
	if _, err := s.migrations.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	f := testFailure(t, nil)
	if _, err := s.Ingest(ctx, f); err != nil {
		t.Fatal(err)
	}
	// Version 1 did not record a resolution source. Backfill must distinguish
	// existing worker success without deleting the retained captured bytes.
	if _, err := s.pool.Exec(ctx, `UPDATE failures SET status='resolved' WHERE id=$1`, f.Metadata.ID); err != nil {
		t.Fatal(err)
	}
	if applied, err := s.Migrate(ctx); err != nil || len(applied) != 1 || applied[0] != 2 {
		t.Fatal(applied, err)
	}
	detail, err := s.Get(ctx, f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "resolved" || detail.ResolutionSource != "retry_succeeded" {
		t.Fatalf("upgrade outcome%+v", detail.Failure)
	}
	payload, err := s.Payload(ctx, f.Metadata.ID, "upgrade-ops")
	if err != nil {
		t.Fatal(err)
	}
	if string(payload.Bytes) != string(f.Payload) {
		t.Fatal("upgrade changed payload bytes")
	}
}
