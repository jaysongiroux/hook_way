package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/replayresult"
)

// Run statuses after the claim.
const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunBlocked   = "blocked"
	RunUnknown   = "unknown"
)

// History events written by workers.
const (
	EventRunFinished     = "run_finished"
	EventLateResult      = "late_result"
	EventChildrenCreated = "children_created"
)

// expireBatch bounds one lease-expiry transaction.
const expireBatch = 500

// recordStatusAfter is a record's status once its run finishes.
var recordStatusAfter = map[string]string{
	RunSucceeded: StatusResolved,
	RunFailed:    StatusOpen,
	RunBlocked:   StatusOpen,
	RunUnknown:   StatusUnknown,
}

// ClaimedRun is a run a worker now owns, with its record.
type ClaimedRun struct {
	Run    Run
	Record Record
}

// RunOutcome is how a run ended. Children are the downstream failures of a
// succeeded run; each becomes a child record.
type RunOutcome struct {
	RunID    string
	Status   string
	Result   json.RawMessage
	Children []NewRecord
	Actor    string
}

// FinalizeResult reports what FinalizeRun did. Late means the run was no
// longer running (its lease expired first), so nothing changed except a
// late_result history event.
type FinalizeResult struct {
	Late     bool
	ChildIDs []string
}

// ClaimRuns moves up to n of the oldest queued runs to running for workerID,
// with a lease ending lease from now, and returns them with their records.
// Concurrent claimers get disjoint runs (FOR UPDATE SKIP LOCKED). The claim
// commits before this returns; the network call happens outside any
// transaction.
func (s *Store) ClaimRuns(ctx context.Context, workerID string, n int, lease time.Duration) ([]ClaimedRun, error) {
	if n <= 0 {
		return nil, nil
	}
	var claimed []ClaimedRun
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now := s.now().UTC()
		rows, err := tx.Query(ctx, `UPDATE retry_runs SET status = $1, worker_id = $2, claimed_at = $3, lease_expires_at = $4
			WHERE run_id IN (SELECT run_id FROM retry_runs WHERE status = $5 ORDER BY created_at, run_id LIMIT $6 FOR UPDATE SKIP LOCKED)
			RETURNING `+runColumns, RunRunning, workerID, now, now.Add(lease), RunQueued, n)
		if err != nil {
			return err
		}
		runs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return scanRun(r) })
		if err != nil || len(runs) == 0 {
			return err
		}
		ids := make([]string, len(runs))
		for i, r := range runs {
			ids[i] = r.RecordID
		}
		rows, err = tx.Query(ctx, `SELECT `+recordColumns+` FROM records WHERE id = ANY($1::uuid[])`, ids)
		if err != nil {
			return err
		}
		failures, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Record, error) { return scanRecord(r) })
		if err != nil {
			return err
		}
		byID := make(map[string]Record, len(failures))
		for _, f := range failures {
			byID[f.ID] = f
		}
		sort.Slice(runs, func(i, j int) bool {
			if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
				return runs[i].CreatedAt.Before(runs[j].CreatedAt)
			}
			return runs[i].RunID < runs[j].RunID
		})
		for _, r := range runs {
			claimed = append(claimed, ClaimedRun{Run: r, Record: byID[r.RecordID]})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// Snapshot returns a record's snapshot after checking it against the size
// and digest recorded at ingest.
func (s *Store) Snapshot(ctx context.Context, recordID string) ([]byte, error) {
	if !validID(recordID) {
		return nil, ErrNotFound
	}
	var data []byte
	var size int64
	var digest string
	err := s.pool.QueryRow(ctx, `SELECT a.snapshot, f.snapshot_size, f.snapshot_sha256
		FROM record_artifacts a JOIN records f ON f.id = a.record_id WHERE a.record_id = $1 AND a.snapshot IS NOT NULL`, recordID).Scan(&data, &size, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(sum[:]) != digest {
		return nil, ErrCorrupt
	}
	return data, nil
}

// FinalizeRun records a run's outcome in one transaction fenced by the run
// still being running: the run's status and result, the record's next
// status, history, and — for a succeeded run — child records and the usage
// they add. Children are never rejected for capacity: a run's result must
// always be recorded. If the run is no longer running, the lease expired
// first; the outcome is logged as late_result and nothing else changes.
func (s *Store) FinalizeRun(ctx context.Context, o RunOutcome) (FinalizeResult, error) {
	next, ok := recordStatusAfter[o.Status]
	if !ok {
		return FinalizeResult{}, fmt.Errorf("store: finalize: unknown run status %q", o.Status)
	}
	if len(o.Children) > 0 && o.Status != RunSucceeded {
		return FinalizeResult{}, errors.New("store: finalize: only a succeeded run creates child records")
	}
	var res FinalizeResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		res = FinalizeResult{}
		now := s.now().UTC()
		var recordID string
		err := tx.QueryRow(ctx, `UPDATE retry_runs SET status = $2, result = $3, finished_at = $4
			WHERE run_id = $1 AND status = $5 RETURNING record_id::text`,
			o.RunID, o.Status, []byte(o.Result), now, RunRunning).Scan(&recordID)
		if errors.Is(err, pgx.ErrNoRows) {
			res.Late = true
			switch err := tx.QueryRow(ctx, `SELECT record_id::text FROM retry_runs WHERE run_id = $1`, o.RunID).Scan(&recordID); {
			case errors.Is(err, pgx.ErrNoRows):
				return nil // GC removed the record
			case err != nil:
				return err
			}
			return appendEvent(ctx, tx, recordID, EventLateResult, o.Actor, now, map[string]any{"run_id": o.RunID, "status": o.Status, "result": o.Result})
		}
		if err != nil {
			return err
		}
		parent, err := scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM records WHERE id = $1 FOR UPDATE`, recordID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE records SET delivery_status = $2, resolution_source = $3 WHERE id = $1`, recordID, next, nullableText(resolutionSource(next))); err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, recordID, EventRunFinished, o.Actor, now, map[string]any{"run_id": o.RunID, "status": o.Status}); err != nil {
			return err
		}
		if len(o.Children) == 0 {
			return nil
		}
		var bytes int64
		for _, c := range o.Children {
			if c.Metadata.Kind != "failure" || c.Metadata.ReplayMode != "resume" {
				return ErrNotRetryable
			}
			if err := validateRecord(c); err != nil {
				return err
			}
			hash, err := ingestHash(c.Metadata)
			if err != nil {
				return err
			}
			l := lineage{parentID: parent.ID, rootID: parent.RootID, capturedAt: now, expiresAt: parent.ExpiresAt}
			if err := insertRecord(ctx, tx, c, hash, l); err != nil {
				return err
			}
			if err := appendEvent(ctx, tx, c.Metadata.ID, EventCaptured, o.Actor, now, map[string]any{"parent_id": parent.ID, "run_id": o.RunID}); err != nil {
				return err
			}
			bytes += int64(len(c.Snapshot) + len(c.Payload))
			res.ChildIDs = append(res.ChildIDs, c.Metadata.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE usage SET records = records + $1, bytes = bytes + $2`, len(o.Children), bytes); err != nil {
			return err
		}
		return appendEvent(ctx, tx, recordID, EventChildrenCreated, o.Actor, now, map[string]any{"run_id": o.RunID, "children": res.ChildIDs})
	})
	if err != nil {
		return FinalizeResult{}, err
	}
	return res, nil
}

// ExpireLeases turns running runs whose lease has passed into unknown runs,
// and their records into unknown records, and returns the run ids. Replicas
// may run it at once: each skips runs another is expiring.
func (s *Store) ExpireLeases(ctx context.Context, actor string) ([]string, error) {
	var expired []string
	result, err := json.Marshal(replayresult.Result{Reason: replayresult.ReasonLeaseExpired})
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		expired = nil
		now := s.now().UTC()
		rows, err := tx.Query(ctx, `UPDATE retry_runs SET status = $1, finished_at = $2, result = $3
			WHERE run_id IN (SELECT run_id FROM retry_runs WHERE status = $4 AND lease_expires_at < $2
			                 ORDER BY lease_expires_at LIMIT $5 FOR UPDATE SKIP LOCKED)
			RETURNING run_id::text, record_id::text`,
			RunUnknown, now, result, RunRunning, expireBatch)
		if err != nil {
			return err
		}
		type pair struct{ run, failure string }
		pairs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) {
			var p pair
			err := r.Scan(&p.run, &p.failure)
			return p, err
		})
		if err != nil {
			return err
		}
		for _, p := range pairs {
			if _, err := tx.Exec(ctx, `UPDATE records SET delivery_status = $2 WHERE id = $1 AND delivery_status = $3`, p.failure, StatusUnknown, StatusRetrying); err != nil {
				return err
			}
			if err := appendEvent(ctx, tx, p.failure, EventRunFinished, actor, now,
				map[string]any{"run_id": p.run, "status": RunUnknown, "reason": "lease_expired"}); err != nil {
				return err
			}
			expired = append(expired, p.run)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return expired, nil
}

func resolutionSource(status string) string {
	if status == StatusResolved {
		return ResolutionRetry
	}
	return ""
}
