package flowconfig

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/ratelimit"
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
	id      string
	keyBy   string
	limiter *ratelimit.Limiter
}

func newRateLimit(id string, opts RateLimitOptions) graph.Node {
	burst := opts.Burst
	if burst <= 0 {
		burst = opts.RPM
	}
	keyBy := opts.KeyBy
	if keyBy == "" {
		keyBy = "source_ip"
	}
	return rateLimitNode{
		id: id, keyBy: keyBy,
		limiter: ratelimit.New(true, opts.RPM, burst),
	}
}

func (n rateLimitNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	head, err := requireHead(n.id, "rate_limit", in)
	if err != nil {
		return nil, err
	}
	key := head.SourceIP
	if n.keyBy != "source_ip" && strings.HasPrefix(n.keyBy, "header.") {
		key = head.Header.Get(strings.TrimPrefix(n.keyBy, "header."))
	}
	if key == "" {
		key = head.SourceIP
	}
	if !n.limiter.Allow(key, time.Now()) {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusTooManyRequests,
			Err: fmt.Errorf("rate limited"),
		}
	}
	return head, nil
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
