package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrGCLocked reports that another replica is sweeping; the caller skips.
var ErrGCLocked = errors.New("store: another replica holds the GC lock")

// gcLockKey is the session advisory lock that serializes sweeps across
// replicas ("hookwayg"); goose's migration lock uses a different key.
const gcLockKey int64 = 0x686f6f6b77617967

// SweepResult is what a sweep deleted: records (dismissed ones included) and
// the artifact bytes they held.
type SweepResult struct {
	Records int64
	Bytes   int64
}

// Sweep deletes expired records without an active run, batchSize per
// transaction, until none remain or budget has elapsed. It holds the GC
// advisory lock on one connection for the whole sweep and returns
// ErrGCLocked without waiting when another replica holds it. The lock
// releases with the connection if this process dies.
func (s *Store) Sweep(ctx context.Context, batchSize int, budget time.Duration) (SweepResult, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return SweepResult{}, fmt.Errorf("store: gc: %w", err)
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, gcLockKey).Scan(&locked); err != nil {
		conn.Release()
		return SweepResult{}, fmt.Errorf("store: gc lock: %w", err)
	}
	if !locked {
		conn.Release()
		return SweepResult{}, ErrGCLocked
	}
	defer func() {
		unlockCtx := context.WithoutCancel(ctx)
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, gcLockKey); err != nil {
			// The connection may still hold the lock; close it instead of
			// returning it to the pool, which releases the lock.
			_ = conn.Hijack().Close(unlockCtx)
			return
		}
		conn.Release()
	}()
	var total SweepResult
	stop := time.Now().Add(budget)
	for {
		// Batches run on the connection that holds the lock, so a sweep
		// never needs a second pooled connection (max_conns may be 1).
		deleted, res, err := s.sweepBatch(ctx, conn, batchSize)
		total.Records += res.Records
		total.Bytes += res.Bytes
		if err != nil {
			return total, fmt.Errorf("store: gc: %w", err)
		}
		if deleted < batchSize || !time.Now().Before(stop) {
			return total, nil
		}
	}
}

// sweepBatch deletes one batch — cascading to artifacts, runs, and history,
// and orphaning children — and gives back the usage those records held. It
// returns how many rows it selected. Rows another transaction has locked are
// skipped for a later sweep.
//
// Choosing and deleting are two statements. In READ COMMITTED a statement's
// NOT EXISTS sees the snapshot taken when the statement began, so a run that
// RequestRetry commits after that snapshot but before the row lock would be
// invisible, and the record deleted under its new run. The DELETE takes a
// fresh snapshot after the rows are locked, and RequestRetry locks the record
// before it inserts a run, so every run that can exist is visible to it.
func (s *Store) sweepBatch(ctx context.Context, conn *pgxpool.Conn, batchSize int) (int, SweepResult, error) {
	var selected int
	var res SweepResult
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		selected, res = 0, SweepResult{}
		rows, err := tx.Query(ctx, `SELECT f.id::text FROM records f
			WHERE f.expires_at < $1
			  AND NOT EXISTS (SELECT 1 FROM retry_runs r WHERE r.record_id = f.id AND r.status IN ($2, $3))
			ORDER BY f.expires_at
			LIMIT $4
			FOR UPDATE OF f SKIP LOCKED`, s.now().UTC(), RunQueued, RunRunning, batchSize)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(ids) == 0 {
			return err
		}
		selected = len(ids)
		if s.afterGCLock != nil {
			s.afterGCLock()
		}
		rows, err = tx.Query(ctx, `DELETE FROM records f
			WHERE f.id = ANY($1::uuid[])
			  AND NOT EXISTS (SELECT 1 FROM retry_runs r WHERE r.record_id = f.id AND r.status IN ($2, $3))
			RETURNING f.dismissed_at, f.has_artifacts, f.snapshot_size + f.payload_size`, ids, RunQueued, RunRunning)
		if err != nil {
			return err
		}
		type gone struct {
			dismissedAt *time.Time
			has         bool
			size        int64
		}
		deletedRows, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (gone, error) {
			var g gone
			err := r.Scan(&g.dismissedAt, &g.has, &g.size)
			return g, err
		})
		if err != nil {
			return err
		}
		var live int64
		for _, g := range deletedRows {
			if g.dismissedAt == nil {
				live++
			}
			if g.has {
				res.Bytes += g.size
			}
		}
		res.Records = int64(len(deletedRows))
		if live == 0 && res.Bytes == 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE usage SET records = records - $1, bytes = bytes - $2`, live, res.Bytes)
		return err
	})
	return selected, res, err
}
