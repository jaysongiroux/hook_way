package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

const ResolutionOperator = "operator_confirmed"
const ResolutionRetry = "retry_succeeded"

// Resolution preserves operator confirmation separately from execution evidence.
type Resolution struct {
	RecordID       string    `json:"record_id"`
	Actor          string    `json:"actor"`
	IdempotencyKey string    `json:"idempotency_key"`
	Note           string    `json:"note"`
	Reference      string    `json:"reference,omitempty"`
	ResolvedAt     time.Time `json:"resolved_at"`
}

type ResolveRequest struct {
	RecordID       string
	Actor          string
	IdempotencyKey string
	uicontract.ResolutionInput
}

type ResolveResult struct {
	Resolution Resolution
	Created    bool
}

const resolutionColumns = `record_id::text, actor, idempotency_key, note, reference, resolved_at`

func scanResolution(row pgx.Row) (Resolution, error) {
	var r Resolution
	err := row.Scan(&r.RecordID, &r.Actor, &r.IdempotencyKey, &r.Note, &r.Reference, &r.ResolvedAt)
	r.ResolvedAt = r.ResolvedAt.UTC()
	return r, err
}

// ResolveUnknown records an operator's independent confirmation, without sending
// anything. The failure row serializes retry, dismissal and resolution decisions.
// Resolution idempotency keys are scoped to actor and the resolve operation;
// reusing one with different evidence or a different failure is rejected.
func (s *Store) ResolveUnknown(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	if !validID(req.RecordID) {
		return ResolveResult{}, ErrNotFound
	}
	if req.Actor == "" || len(uicontract.ValidateReplayAction(uicontract.ReplayActionInput{Action: uicontract.ActionResolve, IdempotencyKey: req.IdempotencyKey, Note: req.Note, Reference: req.Reference}).Issues) != 0 {
		return ResolveResult{}, ErrInvalidResolution
	}
	body, err := json.Marshal(struct {
		ID string
		uicontract.ResolutionInput
	}{ID: req.RecordID, ResolutionInput: req.ResolutionInput})
	if err != nil {
		return ResolveResult{}, err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	result, err := s.resolveUnknown(ctx, req, hash)
	if uniqueViolation(err, "delivery_resolutions_idempotency") {
		// Only retry a confirmed rolled-back uniqueness conflict, never a lost commit.
		result, err = s.resolveUnknown(ctx, req, hash)
	}
	return result, err
}

func (s *Store) resolveUnknown(ctx context.Context, req ResolveRequest, hash string) (ResolveResult, error) {
	var result ResolveResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		existing := func() (bool, error) {
			var previous string
			resolution, err := scanResolutionWithHash(tx.QueryRow(ctx, `SELECT `+resolutionColumns+`, request_hash FROM delivery_resolutions WHERE actor = $1 AND idempotency_key = $2`, req.Actor, req.IdempotencyKey), &previous)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if previous != hash {
				return true, ErrIdempotencyKeyMismatch
			}
			result = ResolveResult{Resolution: resolution}
			return true, nil
		}
		if found, err := existing(); found || err != nil {
			return err
		}
		var status, source, kind string
		var expiry time.Time
		var dismissedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT COALESCE(delivery_status, ''), COALESCE(resolution_source, ''), expires_at, kind, dismissed_at FROM records WHERE id = $1 FOR UPDATE`, req.RecordID).Scan(&status, &source, &expiry, &kind, &dismissedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if found, err := existing(); found || err != nil {
			return err
		}
		if kind != "failure" || dismissedAt != nil {
			return ErrNotResolvable
		}
		if source == ResolutionOperator {
			return ErrResolutionConflict
		}
		// PostgreSQL stores microseconds; return the same receipt on repeats.
		now := s.now().UTC().Truncate(time.Microsecond)
		if !now.Before(expiry) {
			return ErrExpired
		}
		// Do not lock retry rows after the failure row: workers lock in the reverse
		// order. A committed active run is visible here; retry creation needs our row.
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM retry_runs WHERE record_id = $1 AND status IN ($2,$3))`, req.RecordID, RunQueued, RunRunning).Scan(&active); err != nil {
			return err
		}
		if active || status == StatusRetrying {
			return ErrRunActive
		}
		if status != StatusUnknown {
			return ErrNotResolvable
		}
		resolution := Resolution{RecordID: req.RecordID, Actor: req.Actor, IdempotencyKey: req.IdempotencyKey, Note: req.Note, Reference: req.Reference, ResolvedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_resolutions(record_id,actor,idempotency_key,request_hash,note,reference,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, req.RecordID, req.Actor, req.IdempotencyKey, hash, req.Note, req.Reference, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE records SET delivery_status = $2, resolution_source = $3 WHERE id = $1`, req.RecordID, StatusResolved, ResolutionOperator); err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, req.RecordID, EventResolved, req.Actor, now, map[string]any{"source": ResolutionOperator, "note": req.Note, "reference": req.Reference, "idempotency_key": req.IdempotencyKey}); err != nil {
			return err
		}
		result = ResolveResult{Resolution: resolution, Created: true}
		return nil
	})
	if err != nil {
		return ResolveResult{}, err
	}
	return result, nil
}

func scanResolutionWithHash(row pgx.Row, hash *string) (Resolution, error) {
	var r Resolution
	err := row.Scan(&r.RecordID, &r.Actor, &r.IdempotencyKey, &r.Note, &r.Reference, &r.ResolvedAt, hash)
	r.ResolvedAt = r.ResolvedAt.UTC()
	return r, err
}
