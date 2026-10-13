package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

var ErrRoutingConfiguration = errors.New("routing configuration")

type RouteCandidate struct {
	Source  string `yaml:"source" json:"source" jsonschema:"required,enum=body,enum=query,enum=header,enum=variable"`
	Path    string `yaml:"path" json:"path" jsonschema:"required"`
	IDType  string `yaml:"id_type,omitempty" json:"id_type,omitempty"`
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
}
type RouteOptions struct {
	Profile    string           `yaml:"profile" json:"profile" jsonschema:"required"`
	Store      string           `yaml:"store,omitempty" json:"store,omitempty"`
	Candidates []RouteCandidate `yaml:"candidates,omitempty" json:"candidates,omitempty"`
	Fixed      string           `yaml:"fixed,omitempty" json:"fixed,omitempty"`
	Name       string           `yaml:"name,omitempty" json:"name,omitempty"`
}
type routeNode struct {
	id       string
	opts     RouteOptions
	registry *routing.Registry
	store    routing.Reader
	patterns []*regexp.Regexp
	observe  func(string, string)
}

func validateRoute(o RouteOptions) error {
	if o.Profile == "" {
		return errors.New("profile is required")
	}
	if o.Name != "" && !graph.ValidID(o.Name) {
		return errors.New("invalid route variable name")
	}
	if o.Fixed != "" {
		if len(o.Candidates) != 0 || o.Store != "" {
			return errors.New("fixed route cannot have candidates or a store")
		}
		return nil
	}
	if len(o.Candidates) == 0 || len(o.Candidates) > 16 {
		return errors.New("route requires 1..16 candidates")
	}
	for _, c := range o.Candidates {
		switch c.Source {
		case "body", "query", "header", "variable":
		default:
			return errors.New("invalid route candidate source")
		}
		if c.Path == "" || len(c.Path) > 256 {
			return errors.New("candidate path is required and bounded")
		}
		if o.Store != "" {
			if err := routing.ValidateKey(routing.Key{ID: "check", IDType: c.IDType}); err != nil {
				return errors.New("store candidate requires a valid id_type")
			}
		} else if c.IDType != "" {
			return errors.New("id_type requires a store")
		}
		if c.Pattern != "" {
			if len(c.Pattern) > 512 {
				return errors.New("candidate pattern too long")
			}
			if _, err := regexp.Compile("^(?:" + c.Pattern + ")$"); err != nil {
				return fmt.Errorf("candidate pattern: %w", err)
			}
		}
	}
	return nil
}
func newRouteNode(id string, o RouteOptions, opts RegistryOptions) (graph.Node, error) {
	n := &routeNode{id: id, opts: o, registry: opts.RoutingRegistry, store: opts.RoutingStores[o.Store], observe: opts.RoutingObservation}
	if !opts.ValidateOnly {
		if n.registry == nil || !n.registry.HasProfile(o.Profile) {
			return nil, fmt.Errorf("%w: route profile is not bound", ErrRoutingConfiguration)
		}
		if o.Store != "" && n.store == nil {
			return nil, fmt.Errorf("%w: route store is not bound", ErrRoutingConfiguration)
		}
		if o.Fixed != "" {
			if _, err := n.registry.Admit(o.Fixed, o.Profile); err != nil {
				return nil, fmt.Errorf("%w: fixed environment is not admitted", ErrRoutingConfiguration)
			}
		}
	}
	for _, c := range o.Candidates {
		var p *regexp.Regexp
		if c.Pattern != "" {
			p = regexp.MustCompile("^(?:" + c.Pattern + ")$")
		}
		n.patterns = append(n.patterns, p)
	}
	return n, nil
}
func (n *routeNode) fail(reason string, status int, causes ...error) error {
	if n.observe != nil {
		n.observe(n.id, reason)
	}
	failure := error(errors.New(reason))
	if len(causes) > 0 {
		failure = fmt.Errorf("%s: %w", reason, causes[0])
	}
	return graph.NodeError{NodeID: n.id, Reason: reason, Status: status, Err: failure}
}
func (n *routeNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, n.fail("key_invalid", 500)
	}
	if n.registry == nil {
		return nil, n.fail("store_error", 503)
	}
	name := n.opts.Fixed
	if name == "" {
		value, index, err := n.extract(env)
		if err != nil {
			return nil, n.fail("key_invalid", 400)
		}
		name = value
		if n.opts.Store != "" {
			key := routing.Key{ID: value, IDType: n.opts.Candidates[index].IDType}
			if routing.ValidateKey(key) != nil {
				return nil, n.fail("key_invalid", 400)
			}
			if n.store == nil {
				return nil, n.fail("store_error", 503)
			}
			r, found, err := n.store.Get(ctx, key)
			if err != nil {
				return nil, n.fail("store_error", 503, err)
			}
			if !found {
				return nil, n.fail("miss", 400)
			}
			name = r.Environment
		}
	}
	b, err := n.registry.Admit(name, n.opts.Profile)
	if err != nil {
		return nil, n.fail("environment_denied", http.StatusForbidden)
	}
	v := maps.Clone(env.Vars)
	if v == nil {
		v = map[string]string{}
	}
	key := n.opts.Name
	if key == "" {
		key = "route"
	}
	v[key] = b.Name
	v[key+"_prefix"] = b.DestinationPrefix
	v[key+"_backend_host"] = b.BackendHost
	v[key+"_account_id"] = b.AccountID
	v[key+"_credential_mode"] = b.CredentialMode
	env.Vars = v
	if n.observe != nil {
		n.observe(n.id, "hit")
	}
	return env, nil
}
func (n *routeNode) extract(env graph.Envelope) (string, int, error) {
	for i, c := range n.opts.Candidates {
		value, found, err := (requestSelector{Source: c.Source, Path: c.Path}).read(env)
		if err != nil {
			return "", 0, err
		}
		if !found || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return "", 0, errors.New("routing identifier must be a string")
		}
		if text == "" {
			continue
		}
		if len(text) > 512 {
			return "", 0, errors.New("routing identifier too long")
		}
		if n.patterns[i] != nil && !n.patterns[i].MatchString(text) {
			continue
		}
		return text, i, nil
	}
	return "", 0, errors.New("no usable routing identifier")
}
