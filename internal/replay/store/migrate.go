package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrationFS() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	return sub
}

func newMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("store: migration lock: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrationFS(),
		goose.WithSessionLocker(locker), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, fmt.Errorf("store: migrations: %w", err)
	}
	return p, nil
}

// LatestSchemaVersion is the newest migration embedded in this binary.
func LatestSchemaVersion() int64 {
	entries, err := fs.ReadDir(migrationFS(), ".")
	if err != nil {
		panic(err)
	}
	var latest int64
	for _, e := range entries {
		if v, err := goose.NumericComponent(e.Name()); err == nil && v > latest {
			latest = v
		}
	}
	return latest
}

// Migrate applies every pending migration and returns the versions it
// applied. goose holds a PostgreSQL session advisory lock while it runs, so
// replicas that migrate at once apply each migration exactly once.
func (s *Store) Migrate(ctx context.Context) ([]int64, error) {
	// Reject legacy, future and incomplete databases before Goose can write even
	// its version table. The only supported initial transition is an empty store.
	if err := s.checkSchema(ctx, true); err != nil {
		return nil, err
	}
	results, err := s.migrations.Up(ctx)
	applied := make([]int64, 0, len(results))
	for _, r := range results {
		if r.Error == nil {
			applied = append(applied, r.Source.Version)
		}
	}
	if err != nil {
		return applied, fmt.Errorf("store: migrate: %w", err)
	}
	return applied, s.Ready(ctx)
}

// SchemaVersion reports the applied schema version, or 0 before the first
// migrate. It only reads. goose's own version query creates the version table
// and takes the migration lock, and readiness probes and the state sampler
// must never contend with a running migrate.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	return schemaVersion(ctx, s.pool)
}

type schemaReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func schemaVersion(ctx context.Context, db schemaReader) (int64, error) {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var version int64
	// Goose appends both up and down events. Only the newest event for each
	// version says whether that migration is currently applied.
	if err := db.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM (
		SELECT DISTINCT ON (version_id) version_id, is_applied
		FROM goose_db_version ORDER BY version_id, id DESC
	) current_versions WHERE is_applied`).Scan(&version); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return version, nil
}
