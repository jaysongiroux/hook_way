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
)

// RetryRequest asks for one run of one record.
type RetryRequest struct {
	FailureID                string
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
	RecordsByStatus map[string]int64
	UsageRecords    int64
	UsageBytes      int64
	SchemaVersion   int64
	RunsByStatus    map[string]int64 // queued and running
	OldestQueuedAge time.Duration
}

func retryRequestHash(r RetryRequest) string {
	sum := sha256.Sum256([]byte(r.FailureID + "\x00" + strconv.FormatBool(r.AcknowledgeDuplicateRisk)))
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
	if !validID(req.FailureID) {
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
		var retryable bool
		err := tx.QueryRow(ctx, `SELECT status, retryable FROM failures WHERE id = $1 FOR UPDATE`, req.FailureID).Scan(&status, &retryable)
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
		case !retryable || status == StatusResolved || status == StatusDismissed:
			return ErrNotRetryable
		case status == StatusRetrying:
			return ErrRunActive
		case status == StatusUnknown && !req.AcknowledgeDuplicateRisk:
			return ErrDuplicateRiskUnacknowledged
		}
		runID := uuid.Must(uuid.NewV7()).String()
		now := s.now().UTC()
		if _, err := tx.Exec(ctx, `INSERT INTO retry_runs (run_id, failure_id, status, requested_by, idempotency_key,
			request_hash, ack_duplicate_risk, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			runID, req.FailureID, RunQueued, req.Actor, req.IdempotencyKey, hash, req.AcknowledgeDuplicateRisk, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE failures SET status = $2 WHERE id = $1`, req.FailureID, StatusRetrying); err != nil {
			return err
		}
		res = RetryResult{RunID: runID, Created: true}
		return appendEvent(ctx, tx, req.FailureID, EventRunRequested, req.Actor, now,
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
func (s *Store) Dismiss(ctx context.Context, id, actor string) (Failure, error) {
	if !validID(id) {
		return Failure{}, ErrNotFound
	}
	var f Failure
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		f, err = scanFailure(tx.QueryRow(ctx, `SELECT `+failureColumns+` FROM failures WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if f.Status != StatusOpen && f.Status != StatusUnknown {
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
			if _, err := tx.Exec(ctx, `DELETE FROM failure_artifacts WHERE failure_id = $1`, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE failures SET status = $2, has_artifacts = false WHERE id = $1`, id, StatusDismissed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE usage SET records = records - 1, bytes = bytes - $1`, freed); err != nil {
			return err
		}
		f.Status, f.HasArtifacts = StatusDismissed, false
		return appendEvent(ctx, tx, id, EventDismissed, actor, s.now().UTC(), map[string]any{"freed_bytes": freed})
	})
	if err != nil {
		return Failure{}, err
	}
	return f, nil
}

// Stats samples record counts by status, usage, and the schema version.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	st := Stats{RecordsByStatus: map[string]int64{}}
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM failures GROUP BY status`)
	if err != nil {
		return Stats{}, err
	}
	type count struct {
		status string
		n      int64
	}
	counts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (count, error) {
		var c count
		err := r.Scan(&c.status, &c.n)
		return c, err
	})
	if err != nil {
		return Stats{}, err
	}
	for _, c := range counts {
		st.RecordsByStatus[c.status] = c.n
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
