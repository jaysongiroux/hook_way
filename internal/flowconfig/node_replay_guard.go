package flowconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

type replayGuardNode struct {
	id      string
	opts    ReplayGuardOptions
	store   protection.Store
	policy  protection.Policy
	timeout time.Duration
	key     vars.Template
}

func newReplayGuard(id string, o ReplayGuardOptions, options ...RegistryOptions) (graph.Node, error) {
	opts := RegistryOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.ValidateOnly {
		return &replayGuardNode{id: id, opts: o, policy: guardPolicy(opts.FlowID, id, o)}, nil
	}
	tpl, err := parseGuardKey(o.Key)
	if err != nil {
		return nil, err
	}
	p, store, err := nodeProtectionPolicy(o.Store, guardPolicy(opts.FlowID, id, o), opts)
	if err != nil {
		return nil, err
	}
	timeout := 250 * time.Millisecond
	if cfg, ok := opts.StateStores[o.Store]; ok {
		timeout = cfg.Defaults().Timeout
	}
	return &replayGuardNode{id: id, opts: o, store: store, policy: p, timeout: timeout, key: tpl}, nil
}

func (n *replayGuardNode) reservationKey(v graph.Verified) (string, error) {
	if n.opts.Key == "" {
		sum := sha256.Sum256(v.Raw.Body)
		return hex.EncodeToString(sum[:]), nil
	}
	values := map[string]string{}
	for _, name := range n.key.Names() {
		if strings.HasPrefix(name, "header.") {
			header := strings.TrimPrefix(name, "header.")
			value, err := protectionHeaderValue(v.Raw.Head.Header, header)
			if err != nil {
				return "", err
			}
			values[name] = value
		} else {
			values[name] = v.Raw.Head.PathVars[name]
		}
		if len(values[name]) > 4096 {
			return "", errors.New("guard identity exceeds supported bounds")
		}
	}
	rendered, err := n.key.Render(values)
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func protectionHeaderValue(header http.Header, name string) (string, error) {
	// EqualFold includes direct Header maps as well as net/http's canonical
	// maps, and detects duplicate spellings instead of selecting an identity.
	var selected []string
	for candidate, values := range header {
		if strings.EqualFold(candidate, name) {
			selected = append(selected, values...)
		}
	}
	if len(selected) != 1 || selected[0] == "" || len(selected[0]) > 4096 {
		return "", errors.New("protection header identity is missing or ambiguous")
	}
	return selected[0], nil
}

func parseGuardKey(raw string) (vars.Template, error) {
	return vars.ParseWithNames(raw, func(name string) bool {
		return vars.ValidName(name) || (strings.HasPrefix(name, "header.") && validAuthHeader(strings.TrimPrefix(name, "header.")))
	})
}

func (n *replayGuardNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	v, ok := in.(graph.Verified)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: 500, Err: errors.New("replay_guard expected Verified")}
	}
	key, err := n.reservationKey(v)
	if err != nil || key == "" || len(key) > 4096 || strings.ContainsAny(key, "\x00\r\n") {
		return nil, protectionNodeError(n.id, protection.ErrMissingKey)
	}
	if n.store == nil {
		return nil, protectionNodeError(n.id, protection.ErrUnavailable)
	}
	opctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	reservation, err := n.store.Reserve(opctx, n.policy, key)
	if err != nil {
		return nil, protectionNodeError(n.id, err)
	}
	if !reservation.Accepted {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusConflict, Reason: "replay_duplicate", Err: errors.New("duplicate reservation")}
	}
	if reservation.Owner == "" {
		return nil, protectionNodeError(n.id, protection.ErrUnavailable)
	}
	v.Reservations = maps.Clone(v.Reservations)
	if v.Reservations == nil {
		v.Reservations = map[string]graph.GuardReservation{}
	}
	v.Reservations[n.id] = graph.GuardReservation{Key: key, Owner: reservation.Owner}
	return v, nil
}
func (n *replayGuardNode) CompensationInput(_ graph.Payload, out graph.Payload) (graph.Payload, error) {
	v, ok := out.(graph.Verified)
	if !ok {
		return nil, errors.New("replay guard compensation output is missing")
	}
	if v.Reservations[n.id].Owner == "" {
		return nil, errors.New("replay guard compensation owner is missing")
	}
	return v, nil
}
func (n *replayGuardNode) Compensate(ctx context.Context, in graph.Payload, _ error) error {
	v, ok := in.(graph.Verified)
	if !ok {
		return errors.New("replay guard compensation requires Verified")
	}
	reservation := v.Reservations[n.id]
	if reservation.Owner == "" {
		return errors.New("replay guard compensation owner is missing")
	}
	opctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	if err := n.store.Release(opctx, n.policy, reservation.Key, reservation.Owner); err != nil {
		return protectionNodeError(n.id, err)
	}
	return nil
}

func (*replayGuardNode) AdmissionGuard() {}
