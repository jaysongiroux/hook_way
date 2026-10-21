package eventdelivery

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

type rowQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type subscriptionRow struct {
	eventops.Subscription
	active     bool
	generation int64
	principal  []byte
}

const subColumns = `s.id,s.endpoint,s.event,s.owner,s.arguments::text,s.callback,s.expires_at,s.version::text,s.revoked_at,s.revoked_by,s.revoke_reason,s.active,s.generation,s.principal`

func scanSubscription(row pgx.Row) (subscriptionRow, error) {
	var v subscriptionRow
	v.Actions = []eventops.Receipt{}
	var callback string
	err := row.Scan(&v.ID, &v.Endpoint, &v.Event, &v.Owner, &v.ArgumentsJSON, &callback, &v.ExpiresAt, &v.Version, &v.RevokedAt, &v.RevokedBy, &v.Reason, &v.active, &v.generation, &v.principal)
	if err != nil {
		return v, readError(err)
	}
	if u, e := url.Parse(callback); e == nil {
		v.CallbackOrigin = u.Scheme + "://" + u.Host
	}
	v.State = "active"
	if !v.active {
		v.State = "inactive"
	}
	if !v.ExpiresAt.After(time.Now()) {
		v.State = "expired"
	}
	if v.RevokedAt != nil {
		v.State = "revoked"
	}
	v.Revoke = eventops.Eligibility{Allowed: v.RevokedAt == nil}
	v.Restore = eventops.Eligibility{Allowed: v.RevokedAt != nil}
	if !v.Revoke.Allowed {
		v.Revoke.Reason = "This subscription is already revoked."
	}
	if !v.Restore.Allowed {
		v.Restore.Reason = "Only a revoked subscription can be restored."
	}
	return v, nil
}
func readError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return eventops.Fail(404, "not_found")
	}
	return storeError(err)
}
func (r *Runtime) subscription(ctx context.Context, q rowQuery, id string) (subscriptionRow, error) {
	return scanSubscription(q.QueryRow(ctx, `SELECT `+subColumns+` FROM hookway_events.subscriptions s WHERE s.namespace=$1 AND s.id=$2`, r.cfg.Namespace, id))
}

type deliveryRow struct {
	eventops.Delivery
	sub        subscriptionRow
	generation int64
	leaseUntil *time.Time
}

const deliveryColumns = `d.id::text,d.endpoint,d.event,d.event_id,d.subscription,d.state,d.outcome,d.version::text,d.attempts,d.cycle,d.cycle_attempts,o.created_at,d.available_at,d.finished_at,d.http_status,d.generation,d.lease_until,CASE WHEN d.state='failed' THEN o.body ELSE NULL END,`

func (r *Runtime) scanDelivery(ctx context.Context, row pgx.Row) (deliveryRow, error) {
	var d deliveryRow
	var callback string
	var payload []byte
	s := &d.sub
	err := row.Scan(&d.ID, &d.Endpoint, &d.Event, &d.EventID, &d.SubscriptionID, &d.State, &d.Outcome, &d.Version, &d.Attempts, &d.Cycle, &d.CycleAttempts, &d.CreatedAt, &d.AvailableAt, &d.FinishedAt, &d.HTTPStatus, &d.generation, &d.leaseUntil, &payload, &s.ID, &s.Endpoint, &s.Event, &s.Owner, &s.ArgumentsJSON, &callback, &s.ExpiresAt, &s.Version, &s.RevokedAt, &s.RevokedBy, &s.Reason, &s.active, &s.generation, &s.principal)
	if err != nil {
		return d, readError(err)
	}
	d.Owner = s.Owner
	d.History = []eventops.Attempt{}
	d.Actions = []eventops.Receipt{}
	d.Retry = eventops.Eligibility{Reason: "Only a retained failed delivery in its original active monitoring interval can be retried."}
	var p flowconfig.EventPrincipal
	var a map[string]any
	e, ok := r.endpoints[d.Endpoint][d.Event]
	if d.State == "failed" && d.FinishedAt != nil && d.FinishedAt.After(time.Now().Add(-24*time.Hour)) && s.active && s.RevokedAt == nil && s.ExpiresAt.After(time.Now()) && d.generation == s.generation && (d.leaseUntil == nil || !d.leaseUntil.After(time.Now())) && (d.HTTPStatus == nil || (*d.HTTPStatus != 410 && *d.HTTPStatus != 413)) && ok && json.Unmarshal(s.principal, &p) == nil && flowconfig.DecodeMCPJSON([]byte(s.ArgumentsJSON), &a) == nil && validArguments(e, a) && r.authorized(ctx, d.Endpoint, p, e) {
		d.Retry = eventops.Eligibility{Allowed: true}
		var occurrence map[string]any
		if flowconfig.DecodeMCPJSON(payload, &occurrence) != nil {
			d.Retry = eventops.Eligibility{Reason: "The retained payload cannot be retried."}
		} else if data, _ := occurrence["data"].(map[string]any); data == nil || e.payload.Validate(data) != nil || !matches(e, a, data) {
			d.Retry = eventops.Eligibility{Reason: "The retained payload no longer matches the event contract or filters."}
		}
	}
	return d, nil
}

const deliveryJoin = ` FROM hookway_events.deliveries d JOIN hookway_events.subscriptions s ON s.namespace=d.namespace AND s.id=d.subscription JOIN hookway_events.occurrences o ON (o.namespace,o.endpoint,o.event,o.event_id)=(d.namespace,d.endpoint,d.event,d.event_id)`

func (r *Runtime) delivery(ctx context.Context, q rowQuery, id string) (deliveryRow, error) {
	n, e := strconv.ParseInt(id, 10, 64)
	if e != nil || n < 1 {
		return deliveryRow{}, eventops.Fail(404, "not_found")
	}
	return r.scanDelivery(ctx, q.QueryRow(ctx, `SELECT `+deliveryColumns+subColumns+deliveryJoin+` WHERE d.namespace=$1 AND d.id=$2`, r.cfg.Namespace, n))
}

// Operate is available only behind the private authenticated control listener.
func (r *Runtime) Operate(ctx context.Context, req eventops.Request) (any, error) {
	if req.Operation == "status" {
		if r == nil {
			return eventops.Status{State: "disabled"}, nil
		}
		if r.Ready(ctx) != nil {
			return eventops.Status{State: "unavailable"}, nil
		}
		return eventops.Status{State: "enabled"}, nil
	}
	if r == nil {
		return nil, eventops.Fail(503, "events_disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch req.Operation {
	case "subscriptions", "deliveries":
		return r.list(ctx, req.Operation, req.Filter)
	case "subscription":
		v, e := r.subscription(ctx, r.store.pool, req.ID)
		if e != nil {
			return nil, e
		}
		rows, e := r.store.pool.Query(ctx, `SELECT receipt FROM hookway_events.actions WHERE namespace=$1 AND subscription=$2 ORDER BY created_at DESC LIMIT 200`, r.cfg.Namespace, req.ID)
		if e != nil {
			return nil, ErrUnavailable
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var receipt eventops.Receipt
			if rows.Scan(&raw) != nil || json.Unmarshal(raw, &receipt) != nil {
				return nil, ErrUnavailable
			}
			v.Actions = append(v.Actions, receipt)
		}
		if rows.Err() != nil {
			return nil, ErrUnavailable
		}
		return v.Subscription, nil
	case "delivery":
		d, e := r.delivery(ctx, r.store.pool, req.ID)
		if e != nil {
			return nil, e
		}
		rows, e := r.store.pool.Query(ctx, `SELECT attempt,cycle,started_at,finished_at,http_status,duration_ms,outcome FROM hookway_events.attempts WHERE delivery=$1 ORDER BY attempt DESC LIMIT 200`, req.ID)
		if e != nil {
			return nil, ErrUnavailable
		}
		for rows.Next() {
			var a eventops.Attempt
			if e = rows.Scan(&a.Number, &a.Cycle, &a.StartedAt, &a.FinishedAt, &a.HTTPStatus, &a.DurationMS, &a.Outcome); e != nil {
				rows.Close()
				return nil, ErrUnavailable
			}
			d.History = append(d.History, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, ErrUnavailable
		}
		rows, e = r.store.pool.Query(ctx, `SELECT receipt FROM hookway_events.actions WHERE namespace=$1 AND delivery=$2 ORDER BY created_at DESC LIMIT 200`, r.cfg.Namespace, req.ID)
		if e != nil {
			return nil, ErrUnavailable
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var receipt eventops.Receipt
			if e = rows.Scan(&raw); e != nil {
				return nil, ErrUnavailable
			}
			if json.Unmarshal(raw, &receipt) != nil {
				return nil, ErrUnavailable
			}
			d.Actions = append(d.Actions, receipt)
		}
		if rows.Err() != nil {
			return nil, ErrUnavailable
		}
		return d.Delivery, nil
	case "payload":
		d, e := r.delivery(ctx, r.store.pool, req.ID)
		if e != nil {
			return nil, e
		}
		if d.FinishedAt != nil && d.FinishedAt.Before(time.Now().Add(-24*time.Hour)) {
			return nil, eventops.Fail(410, "retention_expired")
		}
		var body []byte
		e = r.store.pool.QueryRow(ctx, `SELECT o.body`+deliveryJoin+` WHERE d.namespace=$1 AND d.id=$2`, r.cfg.Namespace, req.ID).Scan(&body)
		return json.RawMessage(body), readError(e)
	case "retry", "revoke", "restore":
		return r.act(ctx, req)
	default:
		return nil, eventops.Fail(400, "invalid_operation")
	}
}

type listCursor struct {
	Last    string `json:"last"`
	Binding string `json:"binding"`
}

func (r *Runtime) list(ctx context.Context, kind string, f eventops.Filter) (any, error) {
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, eventops.Fail(400, "invalid_limit")
	}
	bindingFilter := f
	bindingFilter.Cursor = ""
	bindingFilter.Limit = 0
	b, _ := json.Marshal(bindingFilter)
	hash := sha256.Sum256(append([]byte(kind), b...))
	binding := hex.EncodeToString(hash[:])
	last := ""
	if f.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		var c listCursor
		if e != nil || len(raw) > 1024 || eventops.Decode(raw, &c) != nil || c.Binding != binding {
			return nil, eventops.Fail(400, "invalid_cursor")
		}
		last = c.Last
	}
	for _, s := range []string{f.Endpoint, f.Event, f.Owner, f.Subscription, f.State} {
		if len(s) > 1024 || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return nil, eventops.Fail(400, "invalid_filter")
		}
	}
	params := []any{r.cfg.Namespace}
	clauses := []string{"s.namespace=$1"}
	add := func(column, op string, value any) {
		params = append(params, value)
		clauses = append(clauses, fmt.Sprintf("%s%s$%d", column, op, len(params)))
	}
	for _, v := range []struct{ column, value string }{{"s.endpoint", f.Endpoint}, {"s.event", f.Event}, {"s.owner", f.Owner}, {"s.id", f.Subscription}} {
		if v.value != "" {
			add(v.column, "=", v.value)
		}
	}
	timeColumn := "s.expires_at"
	idColumn := "s.id"
	order := " ASC"
	columns := subColumns
	from := " FROM hookway_events.subscriptions s"
	if kind == "deliveries" {
		timeColumn = "o.created_at"
		idColumn = "d.id"
		order = " DESC"
		columns = deliveryColumns + subColumns
		from = deliveryJoin
	}
	if f.State != "" {
		if kind == "deliveries" {
			if !strings.Contains("|pending|delivered|failed|cancelled|", "|"+f.State+"|") {
				return nil, eventops.Fail(400, "invalid_state")
			}
			add("d.state", "=", f.State)
		} else {
			states := map[string]string{"active": "s.active AND s.revoked_at IS NULL AND s.expires_at>now()", "inactive": "NOT s.active AND s.revoked_at IS NULL AND s.expires_at>now()", "expired": "s.revoked_at IS NULL AND s.expires_at<=now()", "revoked": "s.revoked_at IS NOT NULL"}
			clause, ok := states[f.State]
			if !ok {
				return nil, eventops.Fail(400, "invalid_state")
			}
			clauses = append(clauses, clause)
		}
	}
	for _, v := range []struct{ value, op string }{{f.Since, ">="}, {f.Until, "<"}} {
		if v.value != "" {
			stamp, e := time.Parse(time.RFC3339Nano, v.value)
			if e != nil {
				return nil, eventops.Fail(400, "invalid_time")
			}
			add(timeColumn, v.op, stamp)
		}
	}
	if f.Since != "" && f.Until != "" {
		a, _ := time.Parse(time.RFC3339Nano, f.Since)
		b, _ := time.Parse(time.RFC3339Nano, f.Until)
		if !a.Before(b) {
			return nil, eventops.Fail(400, "invalid_time")
		}
	}
	if last != "" {
		if kind == "deliveries" {
			n, e := strconv.ParseInt(last, 10, 64)
			if e != nil || n < 1 {
				return nil, eventops.Fail(400, "invalid_cursor")
			}
			add(idColumn, "<", n)
		} else {
			add(idColumn, ">", last)
		}
	}
	params = append(params, limit+1)
	rows, e := r.store.pool.Query(ctx, `SELECT `+columns+from+` WHERE `+strings.Join(clauses, " AND ")+` ORDER BY `+idColumn+order+fmt.Sprintf(" LIMIT $%d", len(params)), params...)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	sp := eventops.SubscriptionPage{Items: []eventops.Subscription{}}
	dp := eventops.DeliveryPage{Items: []eventops.Delivery{}}
	pageBytes := 0
	for rows.Next() {
		var item any
		var count int
		if kind == "subscriptions" {
			v, e := scanSubscription(rows)
			if e != nil {
				return nil, e
			}
			sp.Items = append(sp.Items, v.Subscription)
			item, count = v.Subscription, len(sp.Items)
		} else {
			v, e := r.scanDelivery(ctx, rows)
			if e != nil {
				return nil, e
			}
			dp.Items = append(dp.Items, v.Delivery)
			item, count = v.Delivery, len(dp.Items)
		}
		encoded, _ := json.Marshal(item)
		pageBytes += len(encoded)
		// Large filters can fill the private response budget before the row
		// limit. Keep the overflowing row as the normal pagination sentinel.
		if pageBytes > 1<<20 && count > 1 {
			limit = count - 1
			break
		}
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	cursor := func(id string) string {
		raw, _ := json.Marshal(listCursor{id, binding})
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	if len(sp.Items) > limit {
		sp.Items = sp.Items[:limit]
		sp.NextCursor = cursor(sp.Items[limit-1].ID)
	}
	if len(dp.Items) > limit {
		dp.Items = dp.Items[:limit]
		dp.NextCursor = cursor(dp.Items[limit-1].ID)
	}
	if kind == "subscriptions" {
		return sp, nil
	}
	return dp, nil
}

func (r *Runtime) act(ctx context.Context, req eventops.Request) (any, error) {
	if len(req.Actor) == 0 || len(req.Actor) > 1024 || len(uicontract.ValidateEventAction(req.Operation, req.Key, req.Input).Issues) > 0 {
		return nil, eventops.Fail(400, "invalid_action")
	}
	raw, _ := json.Marshal([]any{req.Operation, req.ID, req.Input})
	hash := sha256.Sum256(raw)
	var receipt eventops.Receipt
	err := r.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var previous, stored []byte
		err := tx.QueryRow(ctx, `SELECT input_hash,receipt FROM hookway_events.actions WHERE namespace=$1 AND actor=$2 AND key=$3`, r.cfg.Namespace, req.Actor, req.Key).Scan(&previous, &stored)
		if err == nil {
			if hex.EncodeToString(previous) != hex.EncodeToString(hash[:]) {
				return eventops.Fail(409, "idempotency_conflict")
			}
			if json.Unmarshal(stored, &receipt) != nil {
				return ErrUnavailable
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var version string
		var deliveryID any
		var subscriptionID any
		if req.Operation == "retry" {
			d, e := r.delivery(ctx, tx, req.ID)
			if e != nil {
				return e
			}
			if d.Version != req.Input.Version {
				return eventops.Fail(409, "version_conflict")
			}
			if !d.Retry.Allowed {
				return eventops.Fail(422, "delivery_not_retryable")
			}
			var pending int
			var bytes int64
			if e = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM hookway_events.deliveries WHERE namespace=$1 AND state='pending'),(SELECT COALESCE(sum(octet_length(body)),0) FROM hookway_events.occurrences WHERE namespace=$1)`, r.cfg.Namespace).Scan(&pending, &bytes); e != nil {
				return e
			}
			if pending >= r.cfg.MaxPending || bytes > r.cfg.MaxBytes {
				return ErrCapacity
			}
			// Revalidate the retained payload against the current contract and filters.
			var body []byte
			if e = tx.QueryRow(ctx, `SELECT o.body`+deliveryJoin+` WHERE d.namespace=$1 AND d.id=$2`, r.cfg.Namespace, req.ID).Scan(&body); e != nil {
				return e
			}
			var occurrence map[string]any
			var args map[string]any
			if flowconfig.DecodeMCPJSON(body, &occurrence) != nil || flowconfig.DecodeMCPJSON([]byte(d.sub.ArgumentsJSON), &args) != nil {
				return eventops.Fail(422, "contract_changed")
			}
			data, _ := occurrence["data"].(map[string]any)
			definition := r.endpoints[d.Endpoint][d.Event]
			if data == nil || definition.payload.Validate(data) != nil || !matches(definition, args, data) {
				return eventops.Fail(422, "contract_changed")
			}
			e = tx.QueryRow(ctx, `UPDATE hookway_events.deliveries SET state='pending',outcome='operator_retry',version=version+1,cycle=cycle+1,cycle_attempts=0,available_at=now(),finished_at=NULL,lease=NULL,lease_until=NULL WHERE namespace=$1 AND id=$2 AND state='failed' AND version::text=$3 RETURNING version::text`, r.cfg.Namespace, req.ID, req.Input.Version).Scan(&version)
			if errors.Is(e, pgx.ErrNoRows) {
				return eventops.Fail(409, "version_conflict")
			}
			if e != nil {
				return e
			}
			deliveryID = req.ID
		} else {
			s, e := r.subscription(ctx, tx, req.ID)
			if e != nil {
				return e
			}
			if s.Version != req.Input.Version {
				return eventops.Fail(409, "version_conflict")
			}
			if req.Operation == "revoke" {
				if s.RevokedAt != nil {
					return eventops.Fail(422, "already_revoked")
				}
				e = tx.QueryRow(ctx, `UPDATE hookway_events.subscriptions SET active=false,generation=generation+1,version=version+1,revoked_at=now(),revoked_by=$3,revoke_reason=$4,secret=''::bytea,previous_secret=NULL,overlap_until=NULL WHERE namespace=$1 AND id=$2 RETURNING version::text`, r.cfg.Namespace, req.ID, req.Actor, req.Input.Reason).Scan(&version)
				if e == nil {
					_, e = tx.Exec(ctx, `UPDATE hookway_events.deliveries SET state='cancelled',version=version+1,finished_at=now(),outcome='revoked',lease=NULL,lease_until=NULL WHERE namespace=$1 AND subscription=$2 AND state='pending'`, r.cfg.Namespace, req.ID)
				}
			} else {
				if s.RevokedAt == nil {
					return eventops.Fail(422, "not_revoked")
				}
				e = tx.QueryRow(ctx, `UPDATE hookway_events.subscriptions SET active=false,version=version+1,revoked_at=NULL,revoked_by='',revoke_reason='' WHERE namespace=$1 AND id=$2 RETURNING version::text`, r.cfg.Namespace, req.ID).Scan(&version)
			}
			if e != nil {
				return e
			}
			subscriptionID = req.ID
		}
		receipt = eventops.Receipt{Action: req.Operation, Target: req.ID, Actor: req.Actor, At: time.Now().UTC(), Version: version, Reason: req.Input.Reason}
		stored, _ = json.Marshal(receipt)
		_, err = tx.Exec(ctx, `INSERT INTO hookway_events.actions(namespace,target,action,actor,key,input_hash,receipt,delivery,subscription) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.cfg.Namespace, req.ID, req.Operation, req.Actor, req.Key, hash[:], stored, deliveryID, subscriptionID)
		return err
	})
	if err == nil {
		r.metric("operator_"+req.Operation, "ok")
	}
	return receipt, err
}
