package flowconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/protection"
)

var ErrProtectionConfiguration = errors.New("invalid protection configuration")

func authTrustScope(o AuthOptions) string {
	raw, _ := json.Marshal([]string{o.CredentialID, defaultAuthScheme(o.Scheme), o.ExpectedIssuer, o.ExpectedAudience, o.TenantClaim})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func protectionPolicyID(flow, node, shared string) string {
	if shared != "" {
		return shared
	}
	id := "flow/" + flow + "/node/" + node
	if len(id) <= 255 {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return "flow-node/" + hex.EncodeToString(sum[:])
}
func ratePolicy(flow, id string, o RateLimitOptions) protection.Policy {
	stage, key, strategy := o.Stage, o.KeyBy, o.Strategy
	if stage == "" {
		stage = "request_head"
	}
	if key == "" {
		key = "source_ip"
	}
	if strings.HasPrefix(key, "header.") {
		key = "header." + strings.ToLower(strings.TrimPrefix(key, "header."))
	}
	if strategy == "" {
		strategy = protection.TokenBucket
	}
	cap := protection.DefaultRateLimitMaxKeys
	if o.MaxKeys != nil {
		cap = *o.MaxKeys
	}
	p := protection.Policy{Kind: protection.RateLimit, ID: protectionPolicyID(flow, id, o.PolicyID), KeyScope: stage + "/" + key, Strategy: strategy, MaxEntries: int64(cap)}
	if strategy == protection.TokenBucket {
		p.RPM = int64(o.RPM)
		p.Burst = int64(o.Burst)
		if p.Burst == 0 {
			p.Burst = p.RPM
		}
	} else {
		p.Limit = int64(o.Limit)
		p.Window, _ = time.ParseDuration(o.Window)
	}
	return p
}
func guardPolicy(flow, id string, o ReplayGuardOptions) protection.Policy {
	ttl := 5 * time.Minute
	if o.TTL != "" {
		ttl, _ = time.ParseDuration(o.TTL)
	}
	cap := o.MaxEntries
	if cap == 0 {
		cap = 1024
	}
	key := o.Key
	if template, err := parseGuardKey(key); err == nil {
		for _, name := range template.Names() {
			if strings.HasPrefix(name, "header.") {
				key = strings.ReplaceAll(key, "{"+name+"}", "{header."+strings.ToLower(strings.TrimPrefix(name, "header."))+"}")
			}
		}
	}
	raw, _ := json.Marshal([]string{"verified", key})
	sum := sha256.Sum256(raw)
	return protection.Policy{Kind: protection.ReplayGuard, ID: protectionPolicyID(flow, id, o.PolicyID), KeyScope: "guard/" + hex.EncodeToString(sum[:]), TTL: ttl, MaxEntries: int64(cap)}
}
func validateRateLimit(o RateLimitOptions) error {
	if o.Store == "" {
		return errors.New("store is required; select a named state_stores backend")
	}
	if o.Stage != "" && o.Stage != "request_head" && o.Stage != "verified" {
		return errors.New("stage must be request_head or verified")
	}
	key := o.KeyBy
	if key == "" {
		key = "source_ip"
	}
	if o.Stage == "verified" {
		if key != "credential_id" && key != "jwt_subject" && key != "jwt_tenant" {
			return errors.New("verified stage key_by must be credential_id, jwt_subject or jwt_tenant")
		}
	} else if key != "source_ip" && (!strings.HasPrefix(key, "header.") || !validAuthHeader(strings.TrimPrefix(key, "header."))) {
		return errors.New("request_head key_by must be source_ip or header.<trusted-header>")
	}
	switch o.Strategy {
	case "", protection.TokenBucket:
		if o.RPM < 1 || o.Burst < 0 || o.Limit != 0 || o.Window != "" {
			return errors.New("token_bucket requires positive rpm and optional burst; limit/window are sliding_window_counter settings")
		}
	case protection.SlidingWindowCounter:
		if o.Limit < 1 || o.Window == "" || o.RPM != 0 || o.Burst != 0 {
			return errors.New("sliding_window_counter requires limit/window and does not accept rpm/burst")
		}
	default:
		return errors.New("strategy must be token_bucket or sliding_window_counter")
	}
	if ratePolicy("validation", "node", o).Validate() != nil {
		return errors.New("rate limit policy ID, numeric limits, capacity or window outside supported bounds")
	}
	return nil
}

func nodeProtectionPolicy(store string, p protection.Policy, opts RegistryOptions) (protection.Policy, protection.Store, error) {
	if opts.ValidateOnly {
		return p, nil, nil
	}
	if opts.StateStores != nil {
		if _, ok := opts.StateStores[store]; !ok {
			return p, nil, errors.New("protection store is not configured")
		}
	}
	for _, configured := range opts.ProtectionPolicies[store] {
		if configured.ID == p.ID {
			p = configured
			break
		}
	}
	if opts.Protection == nil {
		return p, nil, errors.New("protection store resolver is required")
	}
	bound := opts.Protection(store)
	if bound == nil {
		return p, nil, errors.New("protection store is not bound")
	}
	return p, bound, nil
}

// ProtectionPolicies resolves deterministic policy identities without store IO.
// Explicit shared IDs must agree within a backend namespace, including key trust.
func ProtectionPolicies(cfg Config) (map[string][]protection.Policy, error) {
	out := map[string][]protection.Policy{}
	seen := map[string]protection.Policy{}
	for _, store := range sortedKeys(cfg.StateStores) {
		if !graph.ValidID(store) || len(store) > 255 {
			return nil, errors.New("state store names must be bounded printable IDs")
		}
		if err := cfg.StateStores[store].Validate(); err != nil {
			return nil, fmt.Errorf("state_stores.%s: %w", store, err)
		}
	}
	for _, flow := range sortedKeys(cfg.Flows) {
		fc := cfg.Flows[flow]
		for _, nc := range ExpandedNodes(fc) {
			var p protection.Policy
			var store string
			switch nc.Type {
			case "rate_limit":
				o, err := decodeOptions[RateLimitOptions](nc)
				if err != nil {
					return nil, err
				}
				if err = validateRateLimit(o); err != nil {
					return nil, fmt.Errorf("flows.%s.nodes.%s: %w", flow, nc.ID, err)
				}
				store = o.Store
				p = ratePolicy(flow, nc.ID, o)
				if o.Stage == "verified" {
					scope, err := verifiedPolicyScope(fc, nc.ID, o.KeyBy)
					if err != nil {
						return nil, fmt.Errorf("flows.%s.nodes.%s: %w", flow, nc.ID, err)
					}
					p.KeyScope += "/" + scope
				}
			case "replay_guard":
				o, err := decodeOptions[ReplayGuardOptions](nc)
				if err != nil {
					return nil, err
				}
				if err = validateReplayGuard(o); err != nil {
					return nil, fmt.Errorf("flows.%s.nodes.%s: %w", flow, nc.ID, err)
				}
				store = o.Store
				p = guardPolicy(flow, nc.ID, o)
			default:
				continue
			}
			backend, ok := cfg.StateStores[store]
			if !ok {
				return nil, fmt.Errorf("flows.%s.nodes.%s: store %q is absent from state_stores", flow, nc.ID, store)
			}
			if err := p.Validate(); err != nil {
				return nil, fmt.Errorf("flows.%s.nodes.%s: invalid protection policy", flow, nc.ID)
			}
			// A namespace shared by differently named stores must also agree.
			backendID := protection.PolicyKey(backend.Namespace, p.ID)
			if prior, ok := seen[backendID]; ok && prior.Fingerprint() != p.Fingerprint() {
				return nil, fmt.Errorf("flows.%s.nodes.%s: shared policy %q has incompatible protection settings or identity trust", flow, nc.ID, p.ID)
			}
			seen[backendID] = p
			duplicate := false
			for _, existing := range out[store] {
				if existing.ID == p.ID {
					duplicate = true
				}
			}
			if !duplicate {
				out[store] = append(out[store], p)
			}
		}
	}
	for store := range out {
		sort.Slice(out[store], func(i, j int) bool { return out[store][i].ID < out[store][j].ID })
	}
	return out, nil
}
func verifiedPolicyScope(fc graph.FlowConfig, node, key string) (string, error) {
	nodes := map[string]graph.NodeConfig{}
	parents := map[string][]string{}
	for _, n := range fc.Nodes {
		nodes[n.ID] = n
	}
	edges, _ := graph.ParseEdges(fc.Edges)
	for _, e := range edges {
		parents[e.To] = append(parents[e.To], e.From)
	}
	visited := map[string]bool{}
	scopes := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		n := nodes[id]
		if n.Type == "auth" {
			o, err := decodeOptions[AuthOptions](n)
			if err != nil {
				return err
			}
			if o.CredentialID == "" {
				return errors.New("verified quota requires an upstream auth credential_id stable across rotation")
			}
			if key == "jwt_subject" || key == "jwt_tenant" {
				if !auth.SchemeAllowedForAuthType(auth.AuthTypeJWT, defaultAuthScheme(o.Scheme)) || o.ExpectedIssuer == "" || o.ExpectedAudience == "" {
					return errors.New("JWT quota requires upstream JWT auth with credential_id, expected_issuer and expected_audience")
				}
				if key == "jwt_tenant" && o.TenantClaim == "" {
					return errors.New("jwt_tenant quota requires an upstream auth tenant_claim selector")
				}
			}
			scopes[authTrustScope(o)] = true
			return nil
		}
		if n.Type == "no_auth" || len(parents[id]) == 0 {
			return errors.New("verified quota must follow authenticated identity; no_auth does not supply one")
		}
		for _, parent := range parents[id] {
			if err := visit(parent); err != nil {
				return err
			}
		}
		return nil
	}
	for _, parent := range parents[node] {
		if err := visit(parent); err != nil {
			return "", err
		}
	}
	if len(scopes) == 0 {
		return "", errors.New("verified quota requires an upstream auth node")
	}
	items := sortedKeys(scopes)
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func ProtectionConfigurationIssues(cfg Config) []config.ValidationIssue {
	_, err := ProtectionPolicies(cfg)
	if err == nil {
		return nil
	}
	return []config.ValidationIssue{cfgIssue("state_stores", err.Error(), "select explicit stores and matching policy identities; authenticated quota keys require configured identity trust")}
}
