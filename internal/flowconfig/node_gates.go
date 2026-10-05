package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/protection"
)

type loopGuardNode struct {
	id      string
	maxHops int
}

func newLoopGuard(id string, opts LoopGuardOptions) graph.Node {
	maxHops := opts.MaxHops
	if maxHops <= 0 {
		maxHops = 3
	}
	return loopGuardNode{id: id, maxHops: maxHops}
}

func (n loopGuardNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	head, err := requireHead(n.id, "loop_guard", in)
	if err != nil {
		return nil, err
	}
	if head.Hops >= n.maxHops {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err: fmt.Errorf("loop detected: hops %d >= max_hops %d", head.Hops, n.maxHops),
		}
	}
	return head, nil
}

type ipAllowlistNode struct {
	id    string
	cidrs []*net.IPNet
	ips   []net.IP
}

func newIPAllowlist(id string, opts IPAllowlistOptions) graph.Node {
	n := ipAllowlistNode{id: id}
	for _, entry := range opts.CIDRs {
		if ip := net.ParseIP(entry); ip != nil {
			n.ips = append(n.ips, ip)
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			n.cidrs = append(n.cidrs, network)
		}
	}
	return n
}

func (n ipAllowlistNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	head, err := requireHead(n.id, "ip_allowlist", in)
	if err != nil {
		return nil, err
	}
	if !n.allowed(head.SourceIP) {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusForbidden,
			Err: fmt.Errorf("source IP %s is not allowlisted", head.SourceIP),
		}
	}
	return head, nil
}

func (n ipAllowlistNode) allowed(sourceIP string) bool {
	if len(n.cidrs) == 0 && len(n.ips) == 0 {
		return false
	}
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return false
	}
	for _, allowed := range n.ips {
		if allowed.Equal(ip) {
			return true
		}
	}
	for _, network := range n.cidrs {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

type rateLimitNode struct {
	id, keyBy, stage string
	policy           protection.Policy
	store            protection.Store
	timeout          time.Duration
}

func newRateLimit(id string, o RateLimitOptions) graph.Node {
	key := o.KeyBy
	if key == "" {
		key = "source_ip"
	}
	stage := o.Stage
	if stage == "" {
		stage = "request_head"
	}
	return rateLimitNode{id: id, keyBy: key, stage: stage, policy: ratePolicy("", id, o), timeout: 250 * time.Millisecond}
}
func bindRateLimit(id string, o RateLimitOptions, opts RegistryOptions) (graph.Node, error) {
	if opts.ValidateOnly {
		return newRateLimit(id, o), nil
	}
	p, store, err := nodeProtectionPolicy(o.Store, ratePolicy(opts.FlowID, id, o), opts)
	if err != nil {
		return nil, err
	}
	if o.Stage == "verified" && p.KeyScope == "verified/"+o.KeyBy {
		return nil, errors.New("verified protection policy requires validated authentication trust binding")
	}
	n := newRateLimit(id, o).(rateLimitNode)
	n.policy, n.store = p, store
	if cfg, ok := opts.StateStores[o.Store]; ok {
		n.timeout = cfg.Defaults().Timeout
	}
	return n, nil
}
func (n rateLimitNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	var key string
	if n.stage == "verified" {
		v, ok := in.(graph.Verified)
		if !ok {
			return nil, graph.NodeError{NodeID: n.id, Status: 500, Err: errors.New("rate_limit expected Verified")}
		}
		switch n.keyBy {
		case "credential_id":
			key = v.CredentialID
		case "jwt_subject":
			key = v.Subject
		case "jwt_tenant":
			key = v.Tenant
		}
		if v.CredentialID == "" || v.TrustScope == "" {
			key = ""
		}
		if key != "" {
			key = v.TrustScope + "/" + key
		}
	} else {
		head, err := requireHead(n.id, "rate_limit", in)
		if err != nil {
			return nil, err
		}
		key = head.SourceIP
		if strings.HasPrefix(n.keyBy, "header.") {
			key, _ = protectionHeaderValue(head.Header, strings.TrimPrefix(n.keyBy, "header."))
		}
	}
	if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\x00\r\n") {
		return nil, protectionNodeError(n.id, protection.ErrMissingKey)
	}
	if n.store == nil {
		return nil, protectionNodeError(n.id, protection.ErrUnavailable)
	}
	opctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	decision, err := n.store.Allow(opctx, n.policy, key)
	if err != nil {
		return nil, protectionNodeError(n.id, err)
	}
	if !decision.Allowed {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusTooManyRequests, Reason: "rate_limit_exhausted", Err: errors.New("rate quota exhausted")}
	}
	return in, nil
}
func protectionNodeError(id string, err error) graph.NodeError {
	status, reason := http.StatusServiceUnavailable, "protection_unavailable"
	if errors.Is(err, protection.ErrMissingKey) {
		status, reason = http.StatusBadRequest, "protection_key_missing"
	}
	if errors.Is(err, protection.ErrCapacity) {
		reason = "protection_capacity"
	}
	if errors.Is(err, protection.ErrPolicyConflict) {
		reason = "protection_policy_conflict"
	}
	return graph.NodeError{NodeID: id, Status: status, Reason: reason, AbortRecovery: status == 503, Err: errors.New(strings.ReplaceAll(reason, "_", " "))}
}

func requireHead(id, kind string, in graph.Payload) (graph.RequestHead, error) {
	head, ok := in.(graph.RequestHead)
	if !ok {
		return graph.RequestHead{}, graph.NodeError{
			NodeID: id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("%s %q: expected RequestHead, got %T", kind, id, in),
		}
	}
	return head, nil
}

func (rateLimitNode) AdmissionGuard() {}
