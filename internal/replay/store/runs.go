package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// RetryRequest asks for one run of one record.
type RetryRequest struct {
	RecordID                 string
	Actor                    string // key name; scopes the idempotency key
	IdempotencyKey           string
	AcknowledgeDuplicateRisk bool
}

// RetryResult is the run a request created or, for a repeated idempotency
// key, the run the first request created.
type RetryResult struct {
	RunID   string
	Created bool
}

// Stats is one sample for the state gauges.
type Stats struct {
	RecordsByState  map[replaywire.RecordState]int64
	UsageRecords    int64
	UsageBytes      int64
	SchemaVersion   int64
	RunsByStatus    map[string]int64 // queued and running
	OldestQueuedAge time.Duration
}

func retryRequestHash(r RetryRequest) string {
	sum := sha256.Sum256([]byte(r.RecordID + "\x00" + strconv.FormatBool(r.AcknowledgeDuplicateRisk)))
	return hex.EncodeToString(sum[:])
}

func uniqueViolation(err error, index string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == index
}

// RequestRetry queues a run for a record and moves the record to retrying.
// The record row is locked for the whole decision, so concurrent requests
// for one record serialize: one creates the run, the rest see it. A repeated
// idempotency key returns the first request's run, whatever the record's
// status is now; a repeated key with a different request is rejected.
func (s *Store) RequestRetry(ctx context.Context, req RetryRequest) (RetryResult, error) {
	if !validID(req.RecordID) {
		return RetryResult{}, ErrNotFound
	}
	hash := retryRequestHash(req)
	res, err := s.requestRetry(ctx, req, hash)
	if uniqueViolation(err, "retry_runs_idempotency") {
		// The same key was used concurrently for a different record; the
		// retry now finds the committed run and reports the mismatch.
		res, err = s.requestRetry(ctx, req, hash)
	}
	if uniqueViolation(err, "retry_runs_one_active") {
		return RetryResult{}, ErrRunActive
	}
	return res, err
}

func (s *Store) requestRetry(ctx context.Context, req RetryRequest, hash string) (RetryResult, error) {
	var res RetryResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if found, err := existingRun(ctx, tx, req, hash, &res); found || err != nil {
			return err
		}
		var status string
		var dismissedAt *time.Time
		var retryable bool
		var kind string
		var expiresAt time.Time
		err := tx.QueryRow(ctx, `SELECT COALESCE(delivery_status, ''), retryable, expires_at, kind, dismissed_at FROM records WHERE id = $1 FOR UPDATE`, req.RecordID).Scan(&status, &retryable, &expiresAt, &kind, &dismissedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// A request with the same key may have committed while this one
		// waited for the row lock.
		if found, err := existingRun(ctx, tx, req, hash, &res); found || err != nil {
			return err
		}
		switch {
		case !expiresAt.After(s.now().UTC()):
			return ErrExpired
		case kind != "failure" || !retryable || status == StatusResolved || dismissedAt != nil:
			return ErrNotRetryable
		case status == StatusRetrying:
			return ErrRunActive
		case status == StatusUnknown && !req.AcknowledgeDuplicateRisk:
			return ErrDuplicateRiskUnacknowledged
		}
		runID := uuid.Must(uuid.NewV7()).String()
		now := s.now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO retry_runs (run_id, record_id, status, requested_by, idempotency_key,
			request_hash, ack_duplicate_risk, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			runID, req.RecordID, RunQueued, req.Actor, req.IdempotencyKey, hash, req.AcknowledgeDuplicateRisk, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE records SET delivery_status = $2 WHERE id = $1`, req.RecordID, StatusRetrying); err != nil {
			return err
		}
		res = RetryResult{RunID: runID, Created: true}
		return appendEvent(ctx, tx, req.RecordID, EventRunRequested, req.Actor, now,
			map[string]any{"run_id": runID, "acknowledge_duplicate_risk": req.AcknowledgeDuplicateRisk})
	})
	if err != nil {
		return RetryResult{}, err
	}
	return res, nil
}

// existingRun finds a run already created under the request's key.
func existingRun(ctx context.Context, tx pgx.Tx, req RetryRequest, hash string, res *RetryResult) (bool, error) {
	var runID, previous string
	err := tx.QueryRow(ctx, `SELECT run_id::text, request_hash FROM retry_runs WHERE requested_by = $1 AND idempotency_key = $2`,
		req.Actor, req.IdempotencyKey).Scan(&runID, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if previous != hash {
		return true, ErrIdempotencyKeyMismatch
	}
	*res = RetryResult{RunID: runID}
	return true, nil
}

// GetRun returns one run.
func (s *Store) GetRun(ctx context.Context, runID string) (Run, error) {
	if !validID(runID) {
		return Run{}, ErrNotFound
	}
	r, err := scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM retry_runs WHERE run_id = $1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// Dismiss closes an open or unknown record: its artifacts are deleted at
// once, it stops counting against capacity, and the dismissal is logged.
func (s *Store) Dismiss(ctx context.Context, id, actor string) (Record, error) {
	if !validID(id) {
		return Record{}, ErrNotFound
	}
	var f Record
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		f, err = scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM records WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if f.DismissedAt != nil || (f.Kind != "inspection" && f.DeliveryStatus != StatusOpen && f.DeliveryStatus != StatusUnknown) {
			return ErrInvalidState
		}
		var freed int64
		if f.HasArtifacts {
			if f.Snapshot != nil {
				freed += f.Snapshot.Size
			}
			if f.Payload != nil {
				freed += f.Payload.Size
			}
			if _, err := tx.Exec(ctx, `DELETE FROM record_artifacts WHERE record_id = $1`, id); err != nil {
				return err
			}
		}
		at := s.now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `UPDATE records SET dismissed_at = $2, has_artifacts = false WHERE id = $1`, id, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE usage SET records = records - 1, bytes = bytes - $1`, freed); err != nil {
			return err
		}
		f.DismissedAt, f.HasArtifacts = &at, false
		return appendEvent(ctx, tx, id, EventDismissed, actor, at, map[string]any{"freed_bytes": freed})
	})
	if err != nil {
		return Record{}, err
	}
	return f, nil
}

// Stats samples record counts by kind, delivery status and dismissal, usage, and the schema version.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	st := Stats{RecordsByState: map[replaywire.RecordState]int64{}}
	rows, err := s.pool.Query(ctx, `SELECT kind, COALESCE(delivery_status, ''), dismissed_at IS NOT NULL, count(*) FROM records GROUP BY 1, 2, 3`)
	if err != nil {
		return Stats{}, err
	}
	type count struct {
		state replaywire.RecordState
		n     int64
	}
	counts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (count, error) {
		var c count
		err := r.Scan(&c.state.Kind, &c.state.DeliveryStatus, &c.state.Dismissed, &c.n)
		return c, err
	})
	if err != nil {
		return Stats{}, err
	}
	for _, c := range counts {
		st.RecordsByState[c.state] = c.n
	}
	st.RunsByStatus = map[string]int64{RunQueued: 0, RunRunning: 0}
	rows, err = s.pool.Query(ctx, `SELECT status, count(*), min(created_at) FROM retry_runs WHERE status IN ($1, $2) GROUP BY status`, RunQueued, RunRunning)
	if err != nil {
		return Stats{}, err
	}
	type runCount struct {
		status string
		n      int64
		oldest time.Time
	}
	runCounts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (runCount, error) {
		var c runCount
		err := r.Scan(&c.status, &c.n, &c.oldest)
		return c, err
	})
	if err != nil {
		return Stats{}, err
	}
	for _, c := range runCounts {
		st.RunsByStatus[c.status] = c.n
		if c.status == RunQueued {
			st.OldestQueuedAge = s.now().Sub(c.oldest)
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT records, bytes FROM usage`).Scan(&st.UsageRecords, &st.UsageBytes); err != nil {
		return Stats{}, err
	}
	if st.SchemaVersion, err = s.SchemaVersion(ctx); err != nil {
		return Stats{}, err
	}
	return st, nil
}
