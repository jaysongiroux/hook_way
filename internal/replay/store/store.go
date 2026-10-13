// Package store is the replay service's PostgreSQL persistence: capture
// records, their artifacts, retry runs, history, and the usage counter.
// Every change that spans rows is one transaction. A transaction that locks
// more than one of a retry_runs row, a records row, and the usage row locks
// them in that order; ingest locks only the usage row, and GC skips locked
// records rows, so the lock graph has no cycle.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/jaysongiroux/hook_way/internal/pgtls"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// Failure delivery states are independent of record dismissal.
const (
	StatusOpen     = "open"
	StatusRetrying = "retrying"
	StatusResolved = "resolved"
	StatusUnknown  = "unknown"
)

// DeliveryStatuses lists the failure delivery states.
var DeliveryStatuses = []string{StatusOpen, StatusRetrying, StatusResolved, StatusUnknown}

// RunQueued is a new run's status; a worker claims it (lifecycle.go).
const RunQueued = "queued"

// History events written in phase 4.
const (
	EventCaptured          = "captured"
	EventRunRequested      = "run_requested"
	EventPayloadDownloaded = "payload_downloaded"
	EventDismissed         = "dismissed"
	EventResolved          = "operator_resolved"
)

var (
	ErrNotResolvable               = errors.New("store: only an unknown record without an active retry can be operator resolved")
	ErrResolutionConflict          = errors.New("store: this record already has a different operator resolution")
	ErrInvalidResolution           = errors.New("store: invalid resolution evidence or idempotency key")
	ErrExpired                     = errors.New("store: the record has expired")
	ErrNotFound                    = errors.New("store: not found")
	ErrConflict                    = errors.New("store: a record with this id exists with different content")
	ErrCapacity                    = errors.New("store: record or byte capacity reached")
	ErrBadCursor                   = errors.New("store: malformed cursor")
	ErrNoPayload                   = errors.New("store: the record was captured without a payload")
	ErrGone                        = errors.New("store: the record's artifacts were deleted")
	ErrCorrupt                     = errors.New("store: the stored payload does not match its size or digest")
	ErrInvalidState                = errors.New("store: the record's status does not allow this operation")
	ErrRunActive                   = errors.New("store: a run is already queued or running for this record")
	ErrNotRetryable                = errors.New("store: the record cannot be retried")
	ErrDuplicateRiskUnacknowledged = errors.New("store: the record's last run has an unknown outcome; acknowledge the duplicate risk to retry")
	ErrIdempotencyKeyMismatch      = errors.New("store: the idempotency key was used for a different request")
)

// Limits are the retention settings the store enforces.
type Limits struct {
	TTL        time.Duration
	MaxRecords int64
	MaxBytes   int64
}

// Options configures Open.
type Options struct {
	URL      string
	MaxConns int32
	TLS      tlsfiles.Files
	Limits   Limits
}

type Store struct {
	pool       *pgxpool.Pool
	migrations *goose.Provider
	limits     Limits
	now        func() time.Time
	// afterGCLock, when set (tests only), runs between a sweep batch
	// locking its rows and deleting them.
	afterGCLock func()
}

// Open connects and pings. It does not migrate: serve refuses to start on an
// old schema, and migrate is an explicit command.
func Open(ctx context.Context, opts Options) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	if opts.MaxConns > 0 {
		poolCfg.MaxConns = opts.MaxConns
	}
	if err := pgtls.Apply(&poolCfg.ConnConfig.Config, opts.TLS, "database tls"); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	migrations, err := newMigrationProvider(stdlib.OpenDBFromPool(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, migrations: migrations, limits: opts.Limits, now: time.Now}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	_ = s.migrations.Close() // closes the database/sql wrapper, not the pool
	s.pool.Close()
}

// PoolStat returns the connection pool's statistics.
func (s *Store) PoolStat() *pgxpool.Stat { return s.pool.Stat() }

// Ready reports whether the store can serve: the database answers and its
// schema exactly matches this binary's supported generic-record baseline.
func (s *Store) Ready(ctx context.Context) error {
	return s.checkSchema(ctx, false)
}

// validID reports whether id is a canonical lowercase UUID. Store methods
// treat anything else as not found instead of sending it to PostgreSQL.
func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}
