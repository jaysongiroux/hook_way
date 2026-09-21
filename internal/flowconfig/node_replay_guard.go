package flowconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/replay"
	"github.com/jasongiroux/hookway/internal/vars"
)

type replayGuardNode struct {
	id    string
	opts  ReplayGuardOptions
	cache *replay.Cache
	key   vars.Template
}

func newReplayGuard(id string, opts ReplayGuardOptions) (graph.Node, error) {
	ttl := 5 * time.Minute
	if opts.TTL != "" {
		if d, err := time.ParseDuration(opts.TTL); err == nil && d > 0 {
			ttl = d
		}
	}
	maxEntries := opts.MaxEntries
	tpl, err := vars.Parse(opts.Key)
	if err != nil {
		return nil, err
	}
	return &replayGuardNode{
		id: id, opts: opts, cache: replay.NewCache(ttl, maxEntries), key: tpl,
	}, nil
}

func (n *replayGuardNode) reservationKey(v graph.Verified) (string, error) {
	if n.opts.Key == "" {
		sum := sha256.Sum256(v.Raw.Body)
		return hex.EncodeToString(sum[:]), nil
	}
	rendered, err := n.key.Render(headerVars(v.Raw.Head))
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func headerVars(head graph.RequestHead) map[string]string {
	out := map[string]string{}
	for k, v := range head.PathVars {
		out[k] = v
	}
	for k, vs := range head.Header {
		if len(vs) > 0 {
			out[k] = vs[0]
			out["header."+k] = vs[0]
		}
	}
	return out
}

func (n *replayGuardNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	v, ok := in.(graph.Verified)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("replay_guard %q: expected Verified, got %T", n.id, in),
		}
	}
	key, err := n.reservationKey(v)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	if !n.cache.Accept(n.id, key, time.Now()) {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusConflict,
			Err: fmt.Errorf("replay detected"),
		}
	}
	return v, nil
}

func (n *replayGuardNode) Compensate(_ context.Context, in graph.Payload, _ error) error {
	v, ok := in.(graph.Verified)
	if !ok {
		return fmt.Errorf("replay_guard: compensate got %T, want graph.Verified", in)
	}
	key, err := n.reservationKey(v)
	if err != nil {
		return err
	}
	n.cache.Release(n.id, key)
	return nil
}
