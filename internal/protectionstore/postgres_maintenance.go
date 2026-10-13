package protectionstore

import (
	"context"
	"encoding/json"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func (s *Postgres) UpdatePolicy(ctx context.Context, p protection.Policy, expected string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return protection.ErrUnavailable
		}
		done, err := s.updateBatch(ctx, p, expected)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}
func (s *Postgres) updateBatch(ctx context.Context, p protection.Policy, expected string) (bool, error) {
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, protection.ErrUnavailable
	}
	defer rollback(tx, s.cfg.Timeout)
	r, err := s.lockPolicy(ctx, tx, p.ID)
	if err != nil {
		return false, err
	}
	if r.fingerprint != expected || !r.policy.IsCompatibleUpdate(p) {
		return false, protection.ErrPolicyConflict
	}
	if r.pending != nil && r.pending.Fingerprint() != p.Fingerprint() {
		return false, protection.ErrPolicyConflict
	}
	if r.pending == nil {
		now, err := backendTime(ctx, tx)
		if err != nil {
			return false, err
		}
		removed, err := s.clean(ctx, tx, p.ID, now, s.cfg.Cleanup.BatchSize)
		if err != nil {
			return false, err
		}
		if r.count-removed > p.MaxEntries {
			return false, protection.ErrCapacity
		}
		r.updateAt = now
		raw, _ := json.Marshal(p)
		_, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET pending=$3,update_at=$4,cursor='' WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, p.ID, raw, now)
		if err != nil {
			return false, protection.ErrUnavailable
		}
	}
	rows, err := tx.Query(ctx, `SELECT identity_hash,state FROM hookway_protection.entries WHERE namespace=$1 AND policy_id=$2 AND identity_hash>$3 ORDER BY identity_hash LIMIT 100`, s.cfg.Namespace, p.ID, r.cursor)
	if err != nil {
		return false, protection.ErrUnavailable
	}
	type entry struct {
		key   string
		state protection.State
	}
	entries := make([]entry, 0, 100)
	for rows.Next() {
		var e entry
		var raw []byte
		if rows.Scan(&e.key, &raw) != nil || json.Unmarshal(raw, &e.state) != nil {
			rows.Close()
			return false, protection.ErrUnavailable
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, protection.ErrUnavailable
	}
	for _, e := range entries {
		if e.state.Version != p.Fingerprint() {
			state := protection.Reconfigure(r.policy, p, e.state, r.updateAt)
			state.Version = p.Fingerprint()
			if err = s.put(ctx, tx, p, e.key, state, false); err != nil {
				return false, err
			}
		}
		r.cursor = e.key
	}
	done := len(entries) < 100
	if done {
		raw, _ := json.Marshal(p)
		_, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET document=$3,fingerprint=$4,pending=NULL,update_at=NULL,cursor='' WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, p.ID, raw, p.Fingerprint())
	} else {
		_, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET cursor=$3 WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, p.ID, r.cursor)
	}
	if err != nil {
		return false, protection.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return false, protection.ErrUnavailable
	}
	return done, nil
}
func (s *Postgres) GC(ctx context.Context) (int64, error) {
	ctx, cancel := bounded(ctx, s.cfg.Cleanup.Budget)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, protection.ErrUnavailable
	}
	defer rollback(tx, s.cfg.Timeout)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, gcLockID).Scan(&locked); err != nil {
		return 0, protection.ErrUnavailable
	}
	if !locked {
		return 0, nil
	}
	now, err := backendTime(ctx, tx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT policy_id FROM hookway_protection.entries WHERE namespace=$1 AND expires_at<=to_timestamp($2::double precision/1000) ORDER BY expires_at LIMIT $3`, s.cfg.Namespace, now, s.cfg.Cleanup.BatchSize)
	if err != nil {
		return 0, protection.ErrUnavailable
	}
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return 0, protection.ErrUnavailable
		}
		ids[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, protection.ErrUnavailable
	}
	var n int64
	for id := range ids {
		var ready bool
		err = tx.QueryRow(ctx, `SELECT pending IS NULL FROM hookway_protection.policies WHERE namespace=$1 AND id=$2 FOR UPDATE SKIP LOCKED`, s.cfg.Namespace, id).Scan(&ready)
		if err != nil || !ready {
			continue
		}
		now, err = backendTime(ctx, tx)
		if err != nil {
			return 0, err
		}
		removed, err := s.clean(ctx, tx, id, now, s.cfg.Cleanup.BatchSize-int(n))
		if err != nil {
			return 0, err
		}
		n += removed
		if n >= int64(s.cfg.Cleanup.BatchSize) {
			break
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, protection.ErrUnavailable
	}
	return n, nil
}
