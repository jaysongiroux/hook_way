package eventdelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// Publisher is an adapter only; lifecycle belongs to the one core Runtime.
type Publisher struct {
	Runtime  *Runtime
	Endpoint string
}

func (p *Publisher) Publish(ctx context.Context, _ string, target string, msg queue.Message) error {
	return p.Runtime.Publish(ctx, p.Endpoint, target, msg.Body)
}
func (p *Publisher) Ready(ctx context.Context) error { return p.Runtime.Ready(ctx) }
func (p *Publisher) Close() error                    { return nil }
func (r *Runtime) Ready(ctx context.Context) error {
	if err := r.store.Ready(ctx); err != nil {
		return err
	}
	var same bool
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if e := r.store.pool.QueryRow(ctx, `SELECT key_hash=$2 FROM hookway_events.namespaces WHERE namespace=$1`, r.cfg.Namespace, r.keyHash).Scan(&same); e != nil || !same {
		return ErrUnavailable
	}
	return nil
}
func (r *Runtime) Publish(ctx context.Context, endpoint, name string, value any) error {
	e, ok := r.endpoints[endpoint][name]
	if !ok {
		return invalidOccurrence("MCP event not exposed")
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxEventBytes {
		return invalidOccurrence("invalid or oversized MCP occurrence")
	}
	var occurrence map[string]any
	if flowconfig.DecodeMCPJSON(raw, &occurrence) != nil || occurrence == nil {
		return invalidOccurrence("invalid MCP occurrence")
	}
	for k := range occurrence {
		if k != "eventId" && k != "name" && k != "timestamp" && k != "data" && k != "cursor" {
			return invalidOccurrence("unexpected MCP occurrence field")
		}
	}
	id, ok := occurrence["eventId"].(string)
	if !ok || strings.TrimSpace(id) == "" || len(id) > 1024 || strings.ContainsAny(id, "\r\n\x00") {
		return invalidOccurrence("invalid MCP event ID")
	}
	stamp, ok := occurrence["timestamp"].(string)
	if !ok {
		return invalidOccurrence("invalid MCP event timestamp")
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		return invalidOccurrence("invalid MCP event timestamp")
	}
	data, ok := occurrence["data"].(map[string]any)
	if !ok || occurrence["name"] != name || e.payload.Validate(data) != nil || occurrence["cursor"] != nil {
		return invalidOccurrence("invalid MCP event payload")
	}
	occurrence["cursor"] = nil
	raw, err = json.Marshal(occurrence)
	if err != nil || len(raw) > MaxEventBytes {
		return invalidOccurrence("MCP event exceeds 256 KiB")
	}
	canon, err := canonical(data)
	if err != nil {
		return invalidOccurrence("invalid MCP event data")
	}
	hash := sha256.Sum256(canon)
	noSubscribers := false
	err = r.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var previous []byte
		er := tx.QueryRow(ctx, `SELECT data_hash FROM hookway_events.occurrences WHERE namespace=$1 AND endpoint=$2 AND event=$3 AND event_id=$4`, r.cfg.Namespace, endpoint, name, id).Scan(&previous)
		if er == nil {
			if !bytes.Equal(previous, hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(er, pgx.ErrNoRows) {
			return er
		}
		rows, er := tx.Query(ctx, `SELECT id,principal,arguments FROM hookway_events.subscriptions WHERE namespace=$1 AND endpoint=$2 AND event=$3 AND active AND expires_at>now()`, r.cfg.Namespace, endpoint, name)
		if er != nil {
			return er
		}
		ids := []string{}
		for rows.Next() {
			var sub string
			var pr, args []byte
			if er = rows.Scan(&sub, &pr, &args); er != nil {
				rows.Close()
				return er
			}
			var p flowconfig.EventPrincipal
			var a map[string]any
			if json.Unmarshal(pr, &p) != nil || flowconfig.DecodeMCPJSON(args, &a) != nil {
				rows.Close()
				return ErrUnavailable
			}
			if r.authorized(ctx, endpoint, p, e) && validArguments(e, a) && matches(e, a, data) {
				ids = append(ids, sub)
			}
		}
		er = rows.Err()
		rows.Close()
		if er != nil {
			return er
		}
		if len(ids) == 0 {
			noSubscribers = true
			return nil
		}
		var pending int
		var stored int64
		if er = tx.QueryRow(ctx, `SELECT count(*) FROM hookway_events.deliveries WHERE namespace=$1 AND state='pending'`, r.cfg.Namespace).Scan(&pending); er != nil {
			return er
		}
		if er = tx.QueryRow(ctx, `SELECT COALESCE(sum(octet_length(body)),0) FROM hookway_events.occurrences WHERE namespace=$1`, r.cfg.Namespace).Scan(&stored); er != nil {
			return er
		}
		if pending+len(ids) > r.cfg.MaxPending || stored+int64(len(raw)) > r.cfg.MaxBytes {
			return ErrCapacity
		}
		if _, er = tx.Exec(ctx, `INSERT INTO hookway_events.occurrences(namespace,endpoint,event,event_id,body,data_hash) VALUES($1,$2,$3,$4,$5,$6)`, r.cfg.Namespace, endpoint, name, id, raw, hash[:]); er != nil {
			return er
		}
		for _, sub := range ids {
			if _, er = tx.Exec(ctx, `INSERT INTO hookway_events.deliveries(namespace,endpoint,event,event_id,subscription,generation) SELECT $1,$2,$3,$4,$5,generation FROM hookway_events.subscriptions WHERE namespace=$1 AND id=$5`, r.cfg.Namespace, endpoint, name, id, sub); er != nil {
				return er
			}
		}
		return nil
	})
	if err != nil {
		outcome := "store_unavailable"
		if errors.Is(err, ErrCapacity) {
			outcome = "capacity"
		} else if errors.Is(err, ErrConflict) {
			outcome = "identity_conflict"
		}
		r.metric("publish", outcome)
		if errors.Is(err, ErrConflict) {
			return graph.NodeError{Status: 409, Err: err}
		}
		return err
	}
	if noSubscribers {
		r.metric("publish", "no_subscribers")
	} else {
		r.metric("publish", "accepted")
	}
	return nil
}

func invalidOccurrence(message string) error {
	return graph.NodeError{Status: 400, Err: errors.New(message)}
}
