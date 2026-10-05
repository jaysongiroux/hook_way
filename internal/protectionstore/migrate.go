package protectionstore

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"

	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationLockID int64 = 754829163
const gcLockID int64 = 754829165

func newMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(migrationLockID))
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, sub, goose.WithTableName("hookway_protection.goose_db_version"), goose.WithSessionLocker(locker), goose.WithDisableGlobalRegistry(true))
}
func LatestSchemaVersion() int64 { return 1 }
func (s *Postgres) Migrate(ctx context.Context) ([]int64, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, protection.ErrUnavailable
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID+1); err != nil {
		return nil, protection.ErrUnavailable
	}
	_, createErr := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS hookway_protection`)
	unlockCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()
	_, unlockErr := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationLockID+1)
	if unlockErr != nil {
		_ = conn.Conn().Close(unlockCtx)
	}
	if createErr != nil || unlockErr != nil {
		return nil, protection.ErrUnavailable
	}
	conn.Release()
	results, err := s.migrations.Up(ctx)
	versions := make([]int64, 0, len(results))
	for _, r := range results {
		if r.Error == nil {
			versions = append(versions, r.Source.Version)
		}
	}
	return versions, unavailable(err)
}
func (s *Postgres) SchemaVersion(ctx context.Context) (int64, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('hookway_protection.goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, protection.ErrUnavailable
	}
	if !exists {
		return 0, nil
	}
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(max(version_id),0) FROM (SELECT DISTINCT ON(version_id) version_id,is_applied FROM hookway_protection.goose_db_version ORDER BY version_id,id DESC) versions WHERE is_applied`).Scan(&version)
	return version, unavailable(err)
}
