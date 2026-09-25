package store

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

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
