package eventdelivery

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"

	"github.com/jaysongiroux/hook_way/internal/secretref"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const MaxEventBytes = 256 << 10

type Observer interface {
	EventDelivery(string, string)
	EventDeliveryState(int64, int64, bool)
}
type event struct {
	opts           flowconfig.MCPEventOptions
	definition     flowconfig.EventDefinition
	input, payload *jsonschema.Schema
}
type Runtime struct {
	store       *Store
	cfg         flowconfig.EventDeliveryConfig
	cipher      cipher.AEAD
	keyHash     []byte
	endpoints   map[string]map[string]event
	client      *http.Client
	observe     Observer
	mu          sync.RWMutex
	authorizers map[string]func(context.Context, flowconfig.EventPrincipal) bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	once        sync.Once
	verified    map[string]time.Time
}

func New(ctx context.Context, c flowconfig.Config, observe Observer) (*Runtime, error) {
	if c.EventDelivery == nil {
		return nil, nil
	}
	s, err := Open(ctx, *c.EventDelivery)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Runtime, error) { _ = s.Close(); return nil, err }
	if err = s.Ready(ctx); err != nil {
		return fail(errors.New("event schema incompatible; run hookway event-store migrate"))
	}
	ref := c.EventDelivery.EncryptionKey
	resolver, err := secretref.New(secretref.Ref{Provider: ref.Provider, Name: ref.Name, JSONField: ref.JSONField, Selection: ref.Selection}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		return fail(errors.New("event encryption key unavailable"))
	}
	secrets, err := resolver.Secrets(ctx)
	_ = resolver.Close()
	if err != nil || len(secrets) != 1 {
		return fail(errors.New("event encryption key unavailable"))
	}
	key, err := base64.StdEncoding.DecodeString(string(secrets[0]))
	if err != nil || len(key) != 32 {
		return fail(errors.New("event encryption key must encode 32 bytes in base64"))
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	hash := sha256.Sum256(key)
	clear(key)
	r := &Runtime{store: s, cfg: s.cfg, cipher: aead, keyHash: hash[:], endpoints: map[string]map[string]event{}, authorizers: map[string]func(context.Context, flowconfig.EventPrincipal) bool{}, observe: observe, client: flowconfig.NewCredentialHTTPClient(true)}
	for endpoint, f := range c.Flows {
		if o, ok := flowconfig.MCPOptions(f); ok {
			r.endpoints[endpoint] = map[string]event{}
			for _, opt := range o.Events {
				d, ok := c.Events[opt.Event]
				if !ok {
					return fail(errors.New("event definition missing"))
				}
				input, e := flowconfig.CompileMCPSchema(opt.InputSchema)
				if e != nil {
					return fail(e)
				}
				payload, e := flowconfig.CompileMCPSchema(d.PayloadSchema)
				if e != nil {
					return fail(e)
				}
				r.endpoints[endpoint][opt.Event] = event{opt, d, input, payload}
			}
		}
	}
	err = s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO hookway_events.namespaces(namespace,key_hash) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.cfg.Namespace, r.keyHash)
		if e != nil {
			return e
		}
		var h []byte
		if e = tx.QueryRow(ctx, `SELECT key_hash FROM hookway_events.namespaces WHERE namespace=$1`, r.cfg.Namespace).Scan(&h); e != nil {
			return e
		}
		if subtle.ConstantTimeCompare(h, r.keyHash) != 1 {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return fail(errors.New("event namespace key mismatch or store unavailable"))
	}
	return r, nil
}
func (r *Runtime) BindEventAuthorizer(endpoint string, f func(context.Context, flowconfig.EventPrincipal) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorizers[endpoint] = f
}
func (r *Runtime) authorized(ctx context.Context, endpoint string, p flowconfig.EventPrincipal, e event) bool {
	if !flowconfig.EventAllowed(p, e.opts.Access) {
		return false
	}
	r.mu.RLock()
	f := r.authorizers[endpoint]
	r.mu.RUnlock()
	return f != nil && f(ctx, p)
}
func (r *Runtime) metric(operation, outcome string) {
	if r.observe != nil {
		r.observe.EventDelivery(operation, outcome)
	}
}
func (r *Runtime) seal(id string, secret []byte) ([]byte, error) {
	nonce := make([]byte, r.cipher.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	return r.cipher.Seal(nonce, nonce, secret, []byte(r.cfg.Namespace+"\x00"+id)), nil
}
func (r *Runtime) openSecret(id string, v []byte) ([]byte, error) {
	n := r.cipher.NonceSize()
	if len(v) < n {
		return nil, ErrUnavailable
	}
	return r.cipher.Open(nil, v[:n], v[n:], []byte(r.cfg.Namespace+"\x00"+id))
}
func rpcError(code int, message, reason string) error {
	return &flowconfig.EventRPCError{Code: code, Message: message, Reason: reason}
}
func strictJSON(raw []byte, out any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if len(raw) > 64<<10 {
		return errors.New("parameters exceed limit")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return errors.New("expected parameter object")
	}
	delete(envelope, "_meta")
	raw, _ = json.Marshal(envelope)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return errors.New("invalid parameters")
	}
	return nil
}

type delivery struct {
	Mode   string `json:"mode"`
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}
type subscribeParams struct {
	Name      string          `json:"name"`
	Arguments map[string]any  `json:"arguments"`
	Delivery  delivery        `json:"delivery"`
	Cursor    json.RawMessage `json:"cursor"`
	TTL       json.RawMessage `json:"ttlMs"`
	MaxAge    json.RawMessage `json:"maxAgeMs"`
}

func (r *Runtime) subscriptionID(endpoint string, p flowconfig.EventPrincipal, q subscribeParams) (string, error) {
	args, e := canonical(q.Arguments)
	if e != nil {
		return "", e
	}
	b, _ := json.Marshal([]string{r.cfg.Namespace, endpoint, p.ID, q.Delivery.URL, q.Name, string(args)})
	h := sha256.Sum256(b)
	return "sub_" + hex.EncodeToString(h[:]), nil
}
func validCallback(value string) bool {
	u, e := url.Parse(value)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && len(value) <= 4096
}
func (r *Runtime) EventCall(ctx context.Context, endpoint, method string, p flowconfig.EventPrincipal, raw json.RawMessage) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if method == "events/list" {
		var q struct {
			Cursor string `json:"cursor,omitempty"`
		}
		if strictJSON(raw, &q) != nil || q.Cursor != "" {
			return nil, rpcError(-32602, "InvalidParams", "")
		}
		out := []any{}
		names := []string{}
		for n := range r.endpoints[endpoint] {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			e := r.endpoints[endpoint][n]
			if r.authorized(ctx, endpoint, p, e) {
				out = append(out, map[string]any{"name": n, "description": e.definition.Description, "inputSchema": e.opts.InputSchema, "payloadSchema": e.definition.PayloadSchema, "delivery": []string{"webhook"}})
			}
		}
		return map[string]any{"events": out}, nil
	}
	var q subscribeParams
	if strictJSON(raw, &q) != nil || !validCallback(q.Delivery.URL) || q.Name == "" {
		return nil, rpcError(-32602, "InvalidParams", "")
	}
	if len(q.Cursor) > 0 && string(q.Cursor) != "null" {
		return nil, rpcError(-32026, "Unsupported", "cursor")
	}
	if q.Delivery.Mode != "webhook" {
		return nil, rpcError(-32026, "Unsupported", "deliveryMode")
	}
	e, ok := r.endpoints[endpoint][q.Name]
	if !ok {
		return nil, rpcError(-32023, "NotFound", "event")
	}
	if !r.authorized(ctx, endpoint, p, e) {
		return nil, rpcError(-32024, "Forbidden", "")
	}
	if q.Arguments == nil {
		q.Arguments = map[string]any{}
	}
	if !validArguments(e, q.Arguments) {
		return nil, rpcError(-32602, "InvalidParams", "")
	}
	id, err := r.subscriptionID(endpoint, p, q)
	if err != nil {
		return nil, rpcError(-32602, "InvalidParams", "")
	}
	if method == "events/unsubscribe" {
		err = r.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE hookway_events.subscriptions SET active=false,version=version+1 WHERE namespace=$1 AND id=$2 AND owner=$3`, r.cfg.Namespace, id, p.ID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE hookway_events.deliveries SET state='cancelled',finished_at=now(),outcome='unsubscribe' WHERE namespace=$1 AND subscription=$2 AND state='pending'`, r.cfg.Namespace, id)
			return err
		})
		if err != nil {
			return nil, rpcError(-32603, "Event store unavailable", "")
		}
		r.metric("unsubscribe", "ok")
		return map[string]any{}, nil
	}
	if method != "events/subscribe" {
		return nil, rpcError(-32601, "Method not found", "")
	}
	secret, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(q.Delivery.Secret, "whsec_"))
	if err != nil || !strings.HasPrefix(q.Delivery.Secret, "whsec_") || len(secret) < 24 || len(secret) > 64 {
		return nil, rpcError(-32602, "InvalidParams", "")
	}
	defer clear(secret)
	ttl := time.Hour
	if len(q.TTL) > 0 && string(q.TTL) != "null" {
		var ms int64
		if json.Unmarshal(q.TTL, &ms) != nil || ms <= 0 {
			return nil, rpcError(-32602, "InvalidParams", "")
		}
		ms = min(ms, int64((24*time.Hour)/time.Millisecond))
		ttl = max(time.Duration(ms)*time.Millisecond, time.Minute)
	}
	expiry := time.Now().Add(ttl)
	if !p.ExpiresAt.IsZero() && p.ExpiresAt.Before(expiry) {
		expiry = p.ExpiresAt
	}
	if !time.Now().Before(expiry) {
		return nil, rpcError(-32024, "Forbidden", "")
	}
	var observedVersion, observedGeneration int64
	var blocked *time.Time
	var monitoringActive bool
	var monitoringExpiry time.Time
	err = r.store.pool.QueryRow(ctx, `SELECT version,generation,revoked_at,active,expires_at FROM hookway_events.subscriptions WHERE namespace=$1 AND id=$2`, r.cfg.Namespace, id).Scan(&observedVersion, &observedGeneration, &blocked, &monitoringActive, &monitoringExpiry)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcError(-32603, "Event store unavailable", "")
	}
	if blocked != nil {
		return nil, rpcError(-32024, "Forbidden", "revoked")
	}
	if !monitoringActive || !monitoringExpiry.After(time.Now()) {
		observedGeneration++
	}
	verificationOwner := p.ID + "\x00interval:" + strconv.FormatInt(observedGeneration, 10)
	if err = r.verifyCallback(ctx, verificationOwner, q.Delivery.URL, id, secret); err != nil {
		r.metric("subscribe", "callback_failed")
		return nil, err
	}
	sealed, err := r.seal(id, secret)
	if err != nil {
		return nil, rpcError(-32603, "Event encryption unavailable", "")
	}
	args, _ := json.Marshal(q.Arguments)
	principal, _ := json.Marshal(p)
	err = r.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hookway_events.subscriptions WHERE namespace=$1 AND active AND expires_at>now() AND id<>$2`, r.cfg.Namespace, id).Scan(&count); err != nil {
			return err
		}
		if count >= r.cfg.MaxSubscriptions {
			return ErrCapacity
		}
		var previous []byte
		var oldCipher []byte
		var active bool
		var until time.Time
		var revoked *time.Time
		var currentVersion int64
		err := tx.QueryRow(ctx, `SELECT secret,active,expires_at,revoked_at,version FROM hookway_events.subscriptions WHERE namespace=$1 AND id=$2`, r.cfg.Namespace, id).Scan(&oldCipher, &active, &until, &revoked, &currentVersion)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if currentVersion != observedVersion {
			return eventops.Fail(409, "subscription_changed")
		}
		if revoked != nil {
			return eventops.Fail(403, "subscription_revoked")
		}
		if active && time.Now().Before(until) {
			old, e := r.openSecret(id, oldCipher)
			if e != nil {
				return e
			}
			defer clear(old)
			if !bytes.Equal(old, secret) {
				previous = oldCipher
			} else {
				sealed = oldCipher
			}
		}
		// Expired/inactive subscriptions start a new monitoring interval, never recover old jobs.
		if !active || !time.Now().Before(until) {
			if _, err = tx.Exec(ctx, `UPDATE hookway_events.deliveries SET state='cancelled',finished_at=now(),outcome='expired' WHERE namespace=$1 AND subscription=$2 AND state='pending'`, r.cfg.Namespace, id); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO hookway_events.subscriptions(namespace,id,endpoint,principal,owner,event,arguments,callback,secret,previous_secret,overlap_until,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now()+interval '5 minutes',$11) ON CONFLICT(namespace,id) DO UPDATE SET generation=CASE WHEN hookway_events.subscriptions.active AND hookway_events.subscriptions.expires_at>now() THEN hookway_events.subscriptions.generation ELSE hookway_events.subscriptions.generation+1 END, version=hookway_events.subscriptions.version+1,principal=excluded.principal,secret=excluded.secret,previous_secret=CASE WHEN $10::bytea IS NOT NULL THEN excluded.previous_secret WHEN hookway_events.subscriptions.secret=excluded.secret THEN hookway_events.subscriptions.previous_secret ELSE NULL END,overlap_until=CASE WHEN $10::bytea IS NOT NULL THEN excluded.overlap_until ELSE hookway_events.subscriptions.overlap_until END,expires_at=excluded.expires_at,active=true`, r.cfg.Namespace, id, endpoint, principal, p.ID, q.Name, args, q.Delivery.URL, sealed, previous, expiry)
		return err
	})
	if err != nil {
		outcome := "store_unavailable"
		if errors.Is(err, ErrCapacity) {
			outcome = "capacity"
		}
		r.metric("subscribe", outcome)
		var operationError *eventops.Error
		if errors.As(err, &operationError) {
			return nil, rpcError(-32024, "Forbidden", "revoked")
		}
		if errors.Is(err, ErrCapacity) {
			return nil, rpcError(-32025, "ResourceExhausted", "subscriptions")
		}
		return nil, rpcError(-32603, "Event store unavailable", "")
	}
	r.metric("subscribe", "ok")
	return map[string]any{"id": id, "refreshBefore": expiry.UTC().Format(time.RFC3339Nano), "cursor": nil, "truncated": false}, nil
}
