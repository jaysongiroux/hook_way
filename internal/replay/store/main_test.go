package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

func TestMain(m *testing.M) { os.Exit(replaytest.Main(m)) }

func defaultLimits() Limits {
	return Limits{TTL: 24 * time.Hour, MaxRecords: 1000, MaxBytes: 1 << 30}
}

// openTestStore opens a store on a fresh database without migrating it.
func openTestStore(t *testing.T, url string, limits Limits) *Store {
	t.Helper()
	s, err := Open(context.Background(), Options{URL: url, MaxConns: 8, Limits: limits})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// newTestStore opens and migrates a store on a fresh database, with a fixed
// clock.
func newTestStore(t *testing.T, limits Limits) *Store {
	t.Helper()
	s := openTestStore(t, replaytest.Database(t), limits)
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s.now = func() time.Time { return testNow }
	return s
}

var testNow = time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC)
