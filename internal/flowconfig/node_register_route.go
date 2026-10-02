package flowconfig

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/retry"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

// RegisterRouteOptions writes only routing metadata. after_create preserves
// known upstream success even when extraction or registration fails.
type RegisterRouteOptions struct {
	RequireHeaders      []string `yaml:"require_headers,omitempty" json:"require_headers,omitempty"`
	Mode                string   `yaml:"mode" json:"mode" jsonschema:"required,enum=after_create,enum=direct"`
	Store               string   `yaml:"store" json:"store" jsonschema:"required"`
	Profile             string   `yaml:"profile" json:"profile" jsonschema:"required"`
	EnvironmentVariable string   `yaml:"environment_variable" json:"environment_variable" jsonschema:"required"`
	IDSource            string   `yaml:"id_source" json:"id_source" jsonschema:"required,enum=upstream,enum=body,enum=variable"`
	IDPath              string   `yaml:"id_path" json:"id_path" jsonschema:"required"`
	IDType              string   `yaml:"id_type" json:"id_type" jsonschema:"required"`
	Pattern             string   `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	MaxAttempts         int      `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty" jsonschema:"minimum=1,maximum=5"`
	Timeout             string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Backoff             string   `yaml:"backoff,omitempty" json:"backoff,omitempty"`
}
type registerRouteNode struct {
	id               string
	opts             RegisterRouteOptions
	registry         *routing.Registry
	writer           routing.Writer
	observe          func(string, string)
	logger           *slog.Logger
	pattern          *regexp.Regexp
	timeout, backoff time.Duration
}

func validateRegisterRoute(o RegisterRouteOptions) error {
	if len(o.RequireHeaders) > 32 || (len(o.RequireHeaders) > 0 && o.Mode != "direct") {
		return errors.New("registration require_headers is bounded and direct-only")
	}
	for _, h := range o.RequireHeaders {
		if !validAuthHeader(h) {
			return errors.New("invalid registration required header")
		}
	}
	if o.Mode != "after_create" && o.Mode != "direct" {
		return errors.New("registration mode must be after_create or direct")
	}
	if o.Store == "" || o.Profile == "" || !graph.ValidID(o.EnvironmentVariable) || o.IDPath == "" || len(o.IDPath) > 256 {
		return errors.New("store, profile, environment_variable and bounded id_path are required")
	}
	if o.IDSource != "upstream" && o.IDSource != "body" && o.IDSource != "variable" {
		return errors.New("invalid registration id_source")
	}
	if (o.Mode == "after_create") != (o.IDSource == "upstream") {
		return errors.New("after_create requires upstream id_source; direct requires body or variable")
	}
	if routing.ValidateKey(routing.Key{ID: "check", IDType: o.IDType}) != nil {
		return errors.New("invalid registration id_type")
	}
	if o.MaxAttempts < 0 || o.MaxAttempts > 5 {
		return errors.New("max_attempts must be 1..5")
	}
	for _, v := range []struct {
		value string
		max   time.Duration
	}{{o.Timeout, 30 * time.Second}, {o.Backoff, time.Second}} {
		if v.value != "" {
			d, e := time.ParseDuration(v.value)
			if e != nil || d <= 0 || d > v.max {
				return errors.New("registration duration outside bounds")
			}
		}
	}
	if o.Pattern != "" {
		if len(o.Pattern) > 512 {
			return errors.New("registration pattern too long")
		}
		if _, err := regexp.Compile("^(?:" + o.Pattern + ")$"); err != nil {
			return errors.New("invalid registration pattern")
		}
	}
	return nil
}
func newRegisterRouteNode(id string, o RegisterRouteOptions, opts RegistryOptions) (graph.Node, error) {
	writer, _ := opts.RoutingStores[o.Store].(routing.Writer)
	if !opts.ValidateOnly && (writer == nil || opts.RoutingRegistry == nil || !opts.RoutingRegistry.HasProfile(o.Profile)) {
		return nil, ErrRoutingConfiguration
	}
	n := registerRouteNode{id: id, opts: o, registry: opts.RoutingRegistry, writer: writer, observe: opts.RoutingObservation, logger: opts.Logger, timeout: 3 * time.Second, backoff: 50 * time.Millisecond}
	if o.Timeout != "" {
		n.timeout, _ = time.ParseDuration(o.Timeout)
	}
	if o.Backoff != "" {
		n.backoff, _ = time.ParseDuration(o.Backoff)
	}
	if o.Pattern != "" {
		n.pattern = regexp.MustCompile("^(?:" + o.Pattern + ")$")
	}
	return n, nil
}
func (n registerRouteNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: 500, Err: errors.New("registration requires envelope")}
	}
	known := n.opts.Mode == "after_create"
	for _, h := range n.opts.RequireHeaders {
		values := env.V.Raw.Head.Header.Values(h)
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return n.outcome(ctx, env, false, "unregistered", "registration_key_invalid", routing.Record{}, 400)
		}
	}
	if known && (env.Upstream == nil || (env.Upstream.Status != 200 && env.Upstream.Status != 201)) {
		return env, nil
	}
	var value any
	var found bool
	if known {
		if env.Upstream.SelectionError == "" {
			value, found = env.Upstream.Values[n.opts.IDPath]
		}
	} else {
		value, found, _ = (requestSelector{Source: n.opts.IDSource, Path: n.opts.IDPath}).read(env)
	}
	id, valid := value.(string)
	key := routing.Key{ID: id, IDType: n.opts.IDType}
	if !found || !valid || routing.ValidateKey(key) != nil || (n.pattern != nil && !n.pattern.MatchString(id)) {
		return n.outcome(ctx, env, known, "unregistered", "registration_key_invalid", routing.Record{}, 400)
	}
	if n.registry == nil {
		return n.outcome(ctx, env, known, "pending", "registration_pending", routing.Record{Key: key}, 503)
	}
	b, err := n.registry.Admit(env.Vars[n.opts.EnvironmentVariable], n.opts.Profile)
	if err != nil {
		return n.outcome(ctx, env, known, "pending", "registration_environment_denied", routing.Record{Key: key}, 403)
	}
	record := routing.Record{Key: key, Environment: b.Name, CreatedAt: time.Now().Unix()}
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	attempts := max(1, n.opts.MaxAttempts)
	for i := 0; i < attempts; i++ {
		if n.writer == nil {
			err = errors.New("writer unavailable")
		} else {
			err = n.writer.Put(ctx, record)
		}
		if err == nil {
			return n.outcome(ctx, env, known, "registered", "registration_registered", record, 0)
		}
		if i+1 >= attempts || retry.Sleep(ctx, n.backoff) != nil {
			break
		}
	}
	return n.outcome(ctx, env, known, "pending", "registration_pending", record, 503)
}
func (n registerRouteNode) outcome(ctx context.Context, env graph.Envelope, known bool, status, result string, record routing.Record, httpStatus int) (graph.Payload, error) {
	if n.observe != nil {
		n.observe(n.id, result)
	}
	if status != "registered" && n.logger != nil {
		// Deliberately omit upstream bytes and raw errors, which can contain secrets.
		n.logger.WarnContext(ctx, "routing registration recovery", "request_id", env.V.Raw.Head.RequestID, "node", n.id, "store", n.opts.Store, "profile", n.opts.Profile, "result", result, "id", record.ID, "id_type", n.opts.IDType, "env_name", record.Environment, "created_at", record.CreatedAt)
	}
	if !known && httpStatus != 0 {
		return nil, graph.NodeError{NodeID: n.id, Status: httpStatus, Reason: result, Err: errors.New(result)}
	}
	env.Vars = maps.Clone(env.Vars)
	if env.Vars == nil {
		env.Vars = map[string]string{}
	}
	env.Vars["registration_status"] = status
	if status == "registered" {
		env.Vars["registration_id"] = record.ID
	}
	if known {
		upstream := *env.Upstream
		upstream.Header = env.Upstream.Header.Clone()
		if upstream.Header == nil {
			upstream.Header = http.Header{}
		}
		upstream.Header.Set("X-Routing-Registration", status)
		env.Upstream = &upstream
	}
	return env, nil
}
