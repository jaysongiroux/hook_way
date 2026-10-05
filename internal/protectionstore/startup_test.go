package protectionstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func TestUnavailableStartupIsBoundedAndRedacted(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			cfg := protection.Config{Namespace: "unavailable", Timeout: 10 * time.Millisecond, StartupTimeout: 100 * time.Millisecond}
			if backend == "redis" {
				// #nosec G101 -- synthetic credentials for a redaction test against closed localhost port 1.
				cfg.Redis = &protection.RedisConfig{URL: "redis://user:must-not-be-logged@127.0.0.1:1"}
			} else {
				// #nosec G101 -- synthetic credentials for a redaction test against closed localhost port 1.
				cfg.Postgres = &protection.PostgresConfig{URL: "postgres://user:must-not-be-logged@127.0.0.1:1/database"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			store, err := Open(ctx, cfg)
			if store != nil || !errors.Is(err, protection.ErrUnavailable) || strings.Contains(err.Error(), "must-not-be-logged") || time.Since(start) > 2*time.Second {
				t.Fatal("startup failure was not bounded/redacted", err)
			}
		})
	}
}
func TestCanceledCleanupCannotRemoveLiveReservations(t *testing.T) {
	for _, backend := range []string{"memory", "redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			a, _, _ := backendPair(t, backend)
			p := protection.Policy{Kind: protection.ReplayGuard, ID: "cleanup_deadline", KeyScope: "id", TTL: time.Minute, MaxEntries: 10}
			ctx := context.Background()
			if err := a.EnsurePolicy(ctx, p); err != nil {
				t.Fatal(err)
			}
			if r, err := a.Reserve(ctx, p, "live"); err != nil || !r.Accepted {
				t.Fatal(r, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if n, err := a.GC(canceled); n != 0 || !errors.Is(err, protection.ErrUnavailable) {
				t.Fatal("canceled cleanup did not fail closed", n, err)
			}
			r, err := a.Reserve(ctx, p, "live")
			if err != nil || r.Accepted {
				t.Fatal("cleanup released live guard", r, err)
			}
		})
	}
}
