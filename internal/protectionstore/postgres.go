package protectionstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jaysongiroux/hook_way/internal/pgtls"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
	"github.com/pressly/goose/v3"
)

type Postgres struct {
	pool       *pgxpool.Pool
	cfg        protection.Config
	migrations *goose.Provider
}

func OpenPostgres(ctx context.Context, cfg protection.Config) (*Postgres, error) {
	if cfg.Postgres == nil {
		return nil, protection.ErrInvalidPolicy
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Defaults()
	ctx, cancel := bounded(ctx, cfg.StartupTimeout)
	defer cancel()
	pc, err := pgxpool.ParseConfig(cfg.Postgres.URL)
	if err != nil {
		return nil, protection.ErrUnavailable
	}
	pc.MaxConns = cfg.Postgres.MaxConns
	t := cfg.Postgres.TLS
	if t.Enabled {
		err = pgtls.Apply(&pc.ConnConfig.Config, tlsfiles.Files{CACert: t.CACert, ClientCert: t.ClientCert, ClientKey: t.ClientKey, ServerName: t.ServerName, InsecureSkipVerify: t.InsecureSkipVerify}, "protection database tls")
		if err != nil {
			return nil, protection.ErrUnavailable
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, protection.ErrUnavailable
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, protection.ErrUnavailable
	}
	migrations, err := newMigrationProvider(stdlib.OpenDBFromPool(pool))
	if err != nil {
		pool.Close()
		return nil, protection.ErrUnavailable
	}
	return &Postgres{pool: pool, cfg: cfg, migrations: migrations}, nil
}
func (s *Postgres) Close() error {
	err := s.migrations.Close()
	s.pool.Close()
	return unavailable(err)
}
func (s *Postgres) Statistics() protection.Statistics {
	v := s.pool.Stat()
	return protection.Statistics{InUse: int64(v.AcquiredConns()), Idle: int64(v.IdleConns()), WaitDuration: v.EmptyAcquireWaitTime()}
}
func (s *Postgres) Check(ctx context.Context) error {
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version != LatestSchemaVersion() {
		return errors.New("protection schema is incompatible; run hookway state-store migrate")
	}
	var usable bool
	err = s.pool.QueryRow(ctx, `SELECT has_schema_privilege('hookway_protection','USAGE') AND has_table_privilege('hookway_protection.policies','SELECT,INSERT,UPDATE,DELETE') AND has_table_privilege('hookway_protection.entries','SELECT,INSERT,UPDATE,DELETE')`).Scan(&usable)
	if err != nil || !usable {
		return protection.ErrUnavailable
	}
	// Probe real DML under rollback: privileges alone miss RLS restrictions.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return protection.ErrUnavailable
	}
	defer rollback(tx, s.cfg.Timeout)
	owner, err := protection.NewOwner()
	if err != nil {
		return err
	}
	id := "__probe/" + owner
	_, err = tx.Exec(ctx, `INSERT INTO hookway_protection.policies(namespace,id,document,fingerprint) VALUES($1,$2,'{}','probe')`, s.cfg.Namespace, id)
	if err != nil {
		return protection.ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO hookway_protection.entries(namespace,policy_id,identity_hash,state,expires_at) VALUES($1,$2,'probe','{}',clock_timestamp())`, s.cfg.Namespace, id)
	if err != nil {
		return protection.ErrUnavailable
	}
	tag, err := tx.Exec(ctx, `UPDATE hookway_protection.entries SET state='{}' WHERE namespace=$1 AND policy_id=$2`, s.cfg.Namespace, id)
	if err != nil || tag.RowsAffected() != 1 {
		return protection.ErrUnavailable
	}
	tag, err = tx.Exec(ctx, `DELETE FROM hookway_protection.entries WHERE namespace=$1 AND policy_id=$2`, s.cfg.Namespace, id)
	if err != nil || tag.RowsAffected() != 1 {
		return protection.ErrUnavailable
	}
	tag, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET fingerprint='checked' WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, id)
	if err != nil || tag.RowsAffected() != 1 {
		return protection.ErrUnavailable
	}
	tag, err = tx.Exec(ctx, `DELETE FROM hookway_protection.policies WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, id)
	if err != nil || tag.RowsAffected() != 1 {
		return protection.ErrUnavailable
	}
	return nil
}

type pgPolicy struct {
	policy      protection.Policy
	fingerprint string
	count       int64
	pending     *protection.Policy
	updateAt    int64
	cursor      string
}

func (s *Postgres) lockPolicy(ctx context.Context, tx pgx.Tx, id string) (pgPolicy, error) {
	var r pgPolicy
	var raw, pending []byte
	var at *int64
	err := tx.QueryRow(ctx, `SELECT document,fingerprint,live_entries,pending,update_at,cursor FROM hookway_protection.policies WHERE namespace=$1 AND id=$2 FOR UPDATE`, s.cfg.Namespace, id).Scan(&raw, &r.fingerprint, &r.count, &pending, &at, &r.cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, protection.ErrPolicyConflict
	}
	if err != nil {
		return r, protection.ErrUnavailable
	}
	if json.Unmarshal(raw, &r.policy) != nil {
		return r, protection.ErrUnavailable
	}
	if pending != nil {
		r.pending = &protection.Policy{}
		if json.Unmarshal(pending, r.pending) != nil {
			return r, protection.ErrUnavailable
		}
	}
	if at != nil {
		r.updateAt = *at
	}
	return r, nil
}
func backendTime(ctx context.Context, tx pgx.Tx) (int64, error) {
	var now int64
	err := tx.QueryRow(ctx, `SELECT floor(extract(epoch FROM clock_timestamp())*1000)::bigint`).Scan(&now)
	return now, unavailable(err)
}
func (s *Postgres) EnsurePolicy(ctx context.Context, p protection.Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	raw, _ := json.Marshal(p)
	_, err := s.pool.Exec(ctx, `INSERT INTO hookway_protection.policies(namespace,id,document,fingerprint) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.cfg.Namespace, p.ID, raw, p.Fingerprint())
	if err != nil {
		return protection.ErrUnavailable
	}
	return s.CheckPolicy(ctx, p)
}
func (s *Postgres) PolicyStatus(ctx context.Context, id string) (protection.Status, error) {
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	var raw, pending []byte
	var status protection.Status
	err := s.pool.QueryRow(ctx, `SELECT document,fingerprint,pending FROM hookway_protection.policies WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, id).Scan(&raw, &status.Fingerprint, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, protection.ErrPolicyConflict
	}
	if err != nil || json.Unmarshal(raw, &status.Policy) != nil {
		return status, protection.ErrUnavailable
	}
	if pending != nil {
		var p protection.Policy
		if json.Unmarshal(pending, &p) != nil {
			return status, protection.ErrUnavailable
		}
		status.Updating = true
		status.TargetFingerprint = p.Fingerprint()
	}
	return status, nil
}
func (s *Postgres) CheckPolicy(ctx context.Context, p protection.Policy) error {
	status, err := s.PolicyStatus(ctx, p.ID)
	if err != nil {
		return err
	}
	if status.Updating || status.Fingerprint != p.Fingerprint() {
		return protection.ErrPolicyConflict
	}
	return nil
}
func (s *Postgres) clean(ctx context.Context, tx pgx.Tx, id string, now int64, batch int) (int64, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM hookway_protection.entries WHERE (namespace,policy_id,identity_hash) IN (SELECT namespace,policy_id,identity_hash FROM hookway_protection.entries WHERE namespace=$1 AND policy_id=$2 AND expires_at<=to_timestamp($3::double precision/1000) ORDER BY expires_at LIMIT $4 FOR UPDATE SKIP LOCKED)`, s.cfg.Namespace, id, now, batch)
	if err != nil {
		return 0, protection.ErrUnavailable
	}
	n := tag.RowsAffected()
	if n > 0 {
		_, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET live_entries=live_entries-$3 WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, id, n)
	}
	return n, unavailable(err)
}
func (s *Postgres) put(ctx context.Context, tx pgx.Tx, p protection.Policy, key string, state protection.State, newEntry bool) error {
	raw, _ := json.Marshal(state)
	_, err := tx.Exec(ctx, `INSERT INTO hookway_protection.entries(namespace,policy_id,identity_hash,state,expires_at) VALUES($1,$2,$3,$4,to_timestamp($5::double precision/1000)) ON CONFLICT(namespace,policy_id,identity_hash) DO UPDATE SET state=EXCLUDED.state,expires_at=EXCLUDED.expires_at`, s.cfg.Namespace, p.ID, key, raw, state.ExpiresAt)
	if err != nil {
		return protection.ErrUnavailable
	}
	if newEntry {
		_, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET live_entries=live_entries+1 WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, p.ID)
	}
	return unavailable(err)
}
func (s *Postgres) operation(ctx context.Context, p protection.Policy, key, owner, op string) (protection.Decision, protection.Reservation, error) {
	if key == "" {
		return protection.Decision{}, protection.Reservation{}, protection.ErrMissingKey
	}
	ctx, cancel := bounded(ctx, s.cfg.Timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return protection.Decision{}, protection.Reservation{}, protection.ErrUnavailable
	}
	defer rollback(tx, s.cfg.Timeout)
	r, err := s.lockPolicy(ctx, tx, p.ID)
	if err != nil {
		return protection.Decision{}, protection.Reservation{}, err
	}
	if r.pending != nil || r.fingerprint != p.Fingerprint() {
		return protection.Decision{}, protection.Reservation{}, protection.ErrPolicyConflict
	}
	now, err := backendTime(ctx, tx)
	if err != nil {
		return protection.Decision{}, protection.Reservation{}, err
	}
	removed, err := s.clean(ctx, tx, p.ID, now, s.cfg.Cleanup.BatchSize)
	if err != nil {
		return protection.Decision{}, protection.Reservation{}, err
	}
	r.count -= removed
	hash := protection.HashIdentity(key)
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT state FROM hookway_protection.entries WHERE namespace=$1 AND policy_id=$2 AND identity_hash=$3`, s.cfg.Namespace, p.ID, hash).Scan(&raw)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return protection.Decision{}, protection.Reservation{}, protection.ErrUnavailable
	}
	var state protection.State
	if exists && json.Unmarshal(raw, &state) != nil {
		return protection.Decision{}, protection.Reservation{}, protection.ErrUnavailable
	}
	var decision protection.Decision
	var reservation protection.Reservation
	switch op {
	case "release":
		if exists && owner != "" && state.Owner == owner {
			_, err = tx.Exec(ctx, `DELETE FROM hookway_protection.entries WHERE namespace=$1 AND policy_id=$2 AND identity_hash=$3`, s.cfg.Namespace, p.ID, hash)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE hookway_protection.policies SET live_entries=live_entries-1 WHERE namespace=$1 AND id=$2`, s.cfg.Namespace, p.ID)
			}
		}
	case "reserve":
		if exists && state.ExpiresAt > now {
			reservation.ExpiresAt = time.UnixMilli(state.ExpiresAt)
			break
		}
		if !exists && r.count >= p.MaxEntries {
			return decision, reservation, protection.ErrCapacity
		}
		state = protection.State{Owner: owner, ExpiresAt: now + p.TTL.Milliseconds(), Version: p.Fingerprint()}
		reservation = protection.Reservation{Accepted: true, Owner: owner, ExpiresAt: time.UnixMilli(state.ExpiresAt)}
		err = s.put(ctx, tx, p, hash, state, !exists)
	case "allow":
		if !exists && r.count >= p.MaxEntries {
			return decision, reservation, protection.ErrCapacity
		}
		state, decision = protection.Admit(p, state, exists, now)
		state.Version = p.Fingerprint()
		err = s.put(ctx, tx, p, hash, state, !exists)
	}
	if err != nil {
		return protection.Decision{}, protection.Reservation{}, protection.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return protection.Decision{}, protection.Reservation{}, protection.ErrUnavailable
	}
	return decision, reservation, nil
}
func rollback(tx pgx.Tx, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func (s *Postgres) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	if p.Kind != protection.RateLimit {
		return protection.Decision{}, protection.ErrInvalidPolicy
	}
	d, _, err := s.operation(ctx, p, key, "", "allow")
	return d, err
}
func (s *Postgres) Reserve(ctx context.Context, p protection.Policy, key string) (protection.Reservation, error) {
	if p.Kind != protection.ReplayGuard {
		return protection.Reservation{}, protection.ErrInvalidPolicy
	}
	owner, err := protection.NewOwner()
	if err != nil {
		return protection.Reservation{}, err
	}
	_, r, err := s.operation(ctx, p, key, owner, "reserve")
	return r, err
}
func (s *Postgres) Release(ctx context.Context, p protection.Policy, key, owner string) error {
	_, _, err := s.operation(ctx, p, key, owner, "release")
	return err
}
