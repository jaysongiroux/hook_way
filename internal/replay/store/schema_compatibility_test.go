package store

import (
	"context"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

type schemaQueryCounter struct{ count atomic.Int64 }

func (c *schemaQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}

func (*schemaQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestReadyBatchesMetadataWithoutCachingSchema(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	counter := new(schemaQueryCounter)
	cfg := s.pool.Config()
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	traced := &Store{pool: pool}
	check := func(wantError bool) {
		t.Helper()
		counter.count.Store(0)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		err := traced.Ready(ctx)
		if (err != nil) != wantError {
			t.Fatalf("Ready error = %v, want error %v", err, wantError)
		}
		if queries := counter.count.Load(); queries > 5 || queries < 4 {
			t.Fatalf("Ready executed %d queries, want at most five including BEGIN/COMMIT or ROLLBACK", queries)
		} else {
			t.Logf("Ready queries including transaction control: %d; schema changed=%v", queries, wantError)
		}
	}
	check(false)
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE records DROP CONSTRAINT records_kind_fields`); err != nil {
		t.Fatal(err)
	}
	check(true) // A successful startup must not cache away later schema drift.
}

func TestUnsupportedSchemaRejectionDoesNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name, setup, want string
		migrated          bool
	}{
		{"unversioned_legacy", `CREATE TABLE failures(id int PRIMARY KEY, payload text); INSERT INTO failures VALUES(1,'synthetic-preserved')`, "legacy", false},
		{"version_one_legacy", `ALTER TABLE records RENAME TO failures`, "legacy", true},
		{"future_version", `INSERT INTO goose_db_version(version_id,is_applied) VALUES(999,true)`, "ahead", true},
		{"fake_records_only", `CREATE TABLE records(id uuid); CREATE TABLE goose_db_version(id serial PRIMARY KEY, version_id bigint, is_applied bool); INSERT INTO goose_db_version(version_id,is_applied) VALUES(1,true)`, "columns", false},
		{"unversioned_records", `CREATE TABLE records(id uuid)`, "unversioned", false},
		{"wrong_column_type", `ALTER TABLE records ALTER COLUMN core_version TYPE varchar(100)`, "columns", true},
		{"missing_artifact_table", `DROP TABLE record_artifacts`, "columns", true},
		{"missing_invariant", `ALTER TABLE records DROP CONSTRAINT records_kind_fields`, "constraint", true},
		{"missing_fence", `DROP INDEX retry_runs_one_active`, "index", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t, replaytest.Database(t), defaultLimits())
			ctx := t.Context()
			var postgresVersion string
			if err := s.pool.QueryRow(ctx, `SHOW server_version`).Scan(&postgresVersion); err != nil {
				t.Fatal(err)
			}
			t.Logf("fixture PostgreSQL %s; generic-record schema %d", postgresVersion, LatestSchemaVersion())
			if tc.migrated {
				if _, err := s.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.pool.Exec(ctx, tc.setup+`; CREATE TABLE compatibility_sentinel(value text); INSERT INTO compatibility_sentinel VALUES('untouched')`); err != nil {
				t.Fatal(err)
			}
			before := compatibilitySnapshot(t, s)
			for _, run := range []func() error{
				func() error { return s.Ready(ctx) },
				func() error { _, err := s.Migrate(ctx); return err },
			} {
				if err := run(); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "preserve") {
					t.Fatalf("unsupported schema was not rejected actionably: %v", err)
				}
				if after := compatibilitySnapshot(t, s); after != before {
					t.Fatalf("rejection mutated schema/version/sentinel: before %s after %s", before, after)
				}
			}
			if tc.name == "unversioned_legacy" {
				var payload string
				if err := s.pool.QueryRow(ctx, `SELECT payload FROM failures WHERE id=1`).Scan(&payload); err != nil || payload != "synthetic-preserved" {
					t.Fatalf("legacy data changed: %q %v", payload, err)
				}
			}
		})
	}
}

// Include the Goose rows (or its absence), relation/column metadata and a data
// sentinel, so even migration bookkeeping cannot be silently created/changed.
func compatibilitySnapshot(t *testing.T, s *Store) string {
	t.Helper()
	ctx := t.Context()
	var schema, sentinel, versions string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(json_agg(row_to_json(c) ORDER BY c.table_name,c.ordinal_position)::text,'[]') FROM
		(SELECT table_name,ordinal_position,column_name,data_type FROM information_schema.columns WHERE table_schema='public') c`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT value FROM compatibility_sentinel`).Scan(&sentinel); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(json_agg(row_to_json(v) ORDER BY v.id)::text,'[]') FROM goose_db_version v`).Scan(&versions); err != nil {
			t.Fatal(err)
		}
	}
	return schema + "|" + sentinel + "|" + versions
}

func TestFailedOrInterruptedInitialMigrationCanBeRetried(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "statement_failure"
		if interrupted {
			name = "context_cancellation"
		}
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t, replaytest.Database(t), defaultLimits())
			ctx := t.Context()
			data, err := fs.ReadFile(migrationFS(), "00001_init.sql")
			if err != nil {
				t.Fatal(err)
			}
			fault := "SELECT 1/0;"
			if interrupted {
				fault = "SELECT pg_sleep(60); -- synthetic_phase5_migration_barrier"
			}
			data = []byte(strings.Replace(string(data), "-- +goose Down", fault+"\n-- +goose Down", 1))
			provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(s.pool), fstest.MapFS{
				"00001_init.sql": &fstest.MapFile{Data: data},
			}, goose.WithDisableGlobalRegistry(true))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = provider.Close() }()
			original := s.migrations
			defer func() { s.migrations = original }()
			s.migrations = provider
			migrationCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := s.Migrate(migrationCtx); done <- err }()
			if interrupted {
				deadline := time.Now().Add(5 * time.Second)
				for {
					var waiting bool
					if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND state='active' AND wait_event='PgSleep' AND query LIKE '%synthetic_phase5_migration_barrier%')`).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						cancel()
						break
					}
					if time.Now().After(deadline) {
						cancel()
						t.Fatal("migration never reached transactional cancellation barrier")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("faulted migration unexpectedly succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("migration did not release after failure/cancellation")
			}
			s.migrations = original
			if version, err := s.SchemaVersion(ctx); err != nil || version != 0 {
				t.Fatalf("failed migration advanced schema: %d %v", version, err)
			}
			if err := s.Ready(ctx); err == nil {
				t.Fatal("rollout could serve a failed migration")
			}
			for _, shape := range recordSchemaColumns {
				var exists bool
				if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, shape.table).Scan(&exists); err != nil || exists {
					t.Fatalf("partial migration object %s remains: %v %v", shape.table, exists, err)
				}
			}
			if applied, err := s.Migrate(ctx); err != nil || len(applied) != 1 || applied[0] != 1 {
				t.Fatalf("preserved database retry failed: %v %v", applied, err)
			}
			if err := s.Ready(ctx); err != nil {
				t.Fatalf("retry did not restore readiness: %v", err)
			}
		})
	}
}
