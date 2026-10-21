package eventdelivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	randv2 "math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

type job struct {
	id                                  int64
	lease, endpoint, name, eventID, sub string
	attempts, cycleAttempts, status     int
}

func (r *Runtime) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	for i := 0; i < r.cfg.Workers; i++ {
		r.wg.Add(1)
		go func() { defer r.wg.Done(); r.work(ctx) }()
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			r.state(ctx)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = r.store.GC(ctx)
				cancel()
			}
		}
	}()
}
func (r *Runtime) Close() error {
	var e error
	r.once.Do(func() {
		r.mu.Lock()
		if r.cancel != nil {
			r.cancel()
		}
		r.mu.Unlock()
		r.wg.Wait()
		r.client.CloseIdleConnections()
		e = r.store.Close()
	})
	return e
}
func (r *Runtime) state(ctx context.Context) {
	if r.observe == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var count, size int64
	err := r.store.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM hookway_events.deliveries WHERE namespace=$1 AND state='pending'),(SELECT COALESCE(sum(octet_length(body)),0) FROM hookway_events.occurrences WHERE namespace=$1)`, r.cfg.Namespace).Scan(&count, &size)
	r.observe.EventDeliveryState(count, size, err == nil)
}
func (r *Runtime) claim(ctx context.Context) (job, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var j job
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return j, err
	}
	j.lease = hex.EncodeToString(token)
	tx, err := r.store.pool.Begin(ctx)
	if err != nil {
		return j, err
	}
	defer rollbackAttempt(tx)
	err = tx.QueryRow(ctx, `WITH candidate AS (SELECT id FROM hookway_events.deliveries WHERE namespace=$1 AND state='pending' AND available_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE hookway_events.deliveries d SET lease=$2,lease_until=now()+interval '30 seconds',attempts=attempts+1,cycle_attempts=cycle_attempts+1,version=version+1 FROM candidate c WHERE d.id=c.id RETURNING d.id,d.endpoint,d.event,d.event_id,d.subscription,d.attempts,d.cycle_attempts`, r.cfg.Namespace, j.lease).Scan(&j.id, &j.endpoint, &j.name, &j.eventID, &j.sub, &j.attempts, &j.cycleAttempts)
	if err != nil {
		return j, err
	}
	if _, err = tx.Exec(ctx, `UPDATE hookway_events.attempts SET finished_at=now(),outcome='unknown',duration_ms=(EXTRACT(EPOCH FROM (now()-started_at))*1000)::bigint WHERE delivery=$1 AND finished_at IS NULL`, j.id); err != nil {
		return j, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO hookway_events.attempts(delivery,attempt,cycle,lease) SELECT id,attempts,cycle,$2 FROM hookway_events.deliveries WHERE id=$1`, j.id, j.lease); err != nil {
		return j, err
	}
	err = tx.Commit(ctx)
	return j, err
}
func (r *Runtime) work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		j, err := r.claim(ctx)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				r.metric("worker", "store_unavailable")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		r.attempt(ctx, j)
	}
}
func (r *Runtime) attempt(ctx context.Context, j job) {
	if j.cycleAttempts > 10 {
		r.finish(ctx, j, "failed", "exhausted", 0)
		return
	}
	// Once claimed, allow the bounded attempt to finish during shutdown.
	attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	var raw, pr, args, secret, previous []byte
	var url string
	var overlap *time.Time
	err := r.store.pool.QueryRow(attemptCtx, `SELECT o.body,s.principal,s.arguments,s.callback,s.secret,s.previous_secret,s.overlap_until FROM hookway_events.deliveries d JOIN hookway_events.occurrences o ON (o.namespace,o.endpoint,o.event,o.event_id)=(d.namespace,d.endpoint,d.event,d.event_id) JOIN hookway_events.subscriptions s ON s.namespace=d.namespace AND s.id=d.subscription WHERE d.namespace=$1 AND d.id=$2 AND d.lease=$3 AND d.lease_until>now() AND d.state='pending' AND s.active AND s.revoked_at IS NULL AND d.generation=s.generation AND s.expires_at>now()`, r.cfg.Namespace, j.id, j.lease).Scan(&raw, &pr, &args, &url, &secret, &previous, &overlap)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.finish(attemptCtx, j, "cancelled", "inactive", 0)
		}
		return
	}
	e, ok := r.endpoints[j.endpoint][j.name]
	var p flowconfig.EventPrincipal
	var a map[string]any
	var body map[string]any
	if !ok || json.Unmarshal(pr, &p) != nil || flowconfig.DecodeMCPJSON(args, &a) != nil || !r.authorized(attemptCtx, j.endpoint, p, e) || !validArguments(e, a) || flowconfig.DecodeMCPJSON(raw, &body) != nil {
		r.finish(attemptCtx, j, "cancelled", "forbidden", 0)
		return
	}
	data, _ := body["data"].(map[string]any)
	if data == nil || e.payload.Validate(data) != nil || !matches(e, a, data) {
		r.finish(attemptCtx, j, "cancelled", "contract_changed", 0)
		return
	}
	key, err := r.openSecret(j.sub, secret)
	if err != nil {
		r.finish(attemptCtx, j, "failed", "key_unavailable", 0)
		return
	}
	defer clear(key)
	var old []byte
	if overlap != nil && time.Now().Before(*overlap) && len(previous) > 0 {
		old, err = r.openSecret(j.sub, previous)
		if err != nil {
			r.finish(attemptCtx, j, "failed", "key_unavailable", 0)
			return
		}
		defer clear(old)
	}
	response, err := r.send(attemptCtx, url, j.eventID, j.sub, raw, key, old)
	status := 0
	if response != nil {
		status = response.StatusCode
		_ = response.Body.Close()
	}
	j.status = status
	r.metric("attempt", "sent")
	if err == nil && status >= 200 && status < 300 {
		r.finish(attemptCtx, j, "delivered", "accepted", 0)
		return
	}
	transient := err != nil || status == 408 || status == 429 || status >= 500
	if !transient || j.cycleAttempts >= 10 {
		r.finish(attemptCtx, j, "failed", "terminal", 0)
		return
	}
	backoff := min(time.Duration(math.Pow(2, float64(j.cycleAttempts-1)))*time.Second, 5*time.Minute)
	backoff = time.Duration(float64(backoff) * (0.75 + randv2.Float64()*0.5)) // #nosec G404 -- Retry jitter is not security-sensitive.
	r.finish(attemptCtx, j, "pending", "retry", backoff)
}
func (r *Runtime) finish(ctx context.Context, j job, state, outcome string, delay time.Duration) {
	tx, err := r.store.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer rollbackAttempt(tx)
	result, err := tx.Exec(ctx, `UPDATE hookway_events.deliveries SET state=$4,outcome=$5,version=version+1,http_status=NULLIF($7,0),available_at=now()+($6*interval '1 millisecond'),lease=NULL,lease_until=NULL,finished_at=CASE WHEN $4='pending' THEN NULL ELSE now() END WHERE namespace=$1 AND id=$2 AND lease=$3 AND lease_until>now() AND state='pending'`, r.cfg.Namespace, j.id, j.lease, state, outcome, delay.Milliseconds(), j.status)
	if err != nil || result.RowsAffected() != 1 {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE hookway_events.attempts SET finished_at=now(),outcome=$3,http_status=NULLIF($4,0),duration_ms=(EXTRACT(EPOCH FROM (now()-started_at))*1000)::bigint WHERE delivery=$1 AND lease=$2 AND finished_at IS NULL`, j.id, j.lease, outcome, j.status); err != nil {
		return
	}
	if tx.Commit(ctx) == nil {
		r.metric("delivery", outcome)
	}
}

func rollbackAttempt(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (r *Runtime) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}
