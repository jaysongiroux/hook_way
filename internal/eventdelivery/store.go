// Package eventdelivery implements durable MCP webhook subscriptions. Its store
// is separate from replay: successful acceptance is a committed transaction.
package eventdelivery

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

var ErrUnavailable = errors.New("event store unavailable")
var ErrCapacity = errors.New("event store capacity exhausted")
var ErrConflict = errors.New("event identity already accepted with different data")

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLock int64 = 754829180

type Store struct {
	pool       *pgxpool.Pool
	cfg        flowconfig.EventDeliveryConfig
	migrations *goose.Provider
}

func Open(ctx context.Context, c flowconfig.EventDeliveryConfig) (*Store, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c = c.Defaults()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pc, err := pgxpool.ParseConfig(c.PostgresURL)
	if err != nil {
		return nil, ErrUnavailable
	}
	pc.MaxConns = int32(c.Workers + 4) // #nosec G115 -- Validate bounds Workers to 1..64.
	p, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, ErrUnavailable
	}
	sub, _ := fs.Sub(migrations, "migrations")
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(migrationLock))
	if err != nil {
		p.Close()
		return nil, ErrUnavailable
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(p), sub, goose.WithTableName("hookway_events.goose_db_version"), goose.WithSessionLocker(locker), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		p.Close()
		return nil, ErrUnavailable
	}
	return &Store{pool: p, cfg: c, migrations: provider}, nil
}
func (s *Store) Close() error { err := s.migrations.Close(); s.pool.Close(); return storeError(err) }
func storeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCapacity) || errors.Is(err, ErrConflict) {
		return err
	}
	var operationError *eventops.Error
	if errors.As(err, &operationError) {
		return err
	}
	return ErrUnavailable
}
func (s *Store) Migrate(ctx context.Context) error {
	// Schema creation and Goose's own migration table are protected independently.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return ErrUnavailable
	}
	_, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock+1)
	if err == nil {
		_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS hookway_events")
	}
	unlock, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e := conn.Exec(unlock, "SELECT pg_advisory_unlock($1)", migrationLock+1); e != nil {
		_ = conn.Conn().Close(unlock)
		err = e
	}
	conn.Release()
	if err != nil {
		return ErrUnavailable
	}
	_, err = s.migrations.Up(ctx)
	return storeError(err)
}
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('hookway_events.goose_db_version') IS NOT NULL").Scan(&exists); err != nil {
		return 0, ErrUnavailable
	}
	if !exists {
		return 0, nil
	}
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(max(version_id),0) FROM (SELECT DISTINCT ON(version_id) version_id,is_applied FROM hookway_events.goose_db_version ORDER BY version_id,id DESC) v WHERE is_applied`).Scan(&version)
	return version, storeError(err)
}
func (s *Store) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v, e := s.SchemaVersion(ctx)
	if e != nil || v != 2 {
		return ErrUnavailable
	}
	return nil
}
func (s *Store) transaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return ErrUnavailable
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,754829181))`, s.cfg.Namespace); e != nil {
		return ErrUnavailable
	}
	if e = fn(ctx, tx); e != nil {
		return storeError(e)
	}
	return storeError(tx.Commit(ctx))
}
func (s *Store) GC(ctx context.Context) error {
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ns := s.cfg.Namespace
		if _, e := tx.Exec(ctx, `UPDATE hookway_events.deliveries d SET state='cancelled',finished_at=now(),outcome='inactive' WHERE d.namespace=$1 AND d.state='pending' AND EXISTS(SELECT 1 FROM hookway_events.subscriptions s WHERE s.namespace=d.namespace AND s.id=d.subscription AND (NOT s.active OR s.expires_at<=now()))`, ns); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `DELETE FROM hookway_events.deliveries WHERE namespace=$1 AND finished_at<now()-interval '24 hours'`, ns); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `DELETE FROM hookway_events.occurrences o WHERE namespace=$1 AND created_at<now()-interval '24 hours' AND NOT EXISTS(SELECT 1 FROM hookway_events.deliveries d WHERE d.namespace=o.namespace AND d.endpoint=o.endpoint AND d.event=o.event AND d.event_id=o.event_id)`, ns); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `DELETE FROM hookway_events.actions WHERE namespace=$1 AND delivery IS NULL AND created_at<now()-interval '24 hours'`, ns); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `DELETE FROM hookway_events.subscriptions s WHERE namespace=$1 AND revoked_at IS NULL AND (NOT active OR expires_at<now()-interval '24 hours') AND NOT EXISTS(SELECT 1 FROM hookway_events.deliveries d WHERE d.namespace=s.namespace AND d.subscription=s.id)`, ns)
		return e
	})
}
