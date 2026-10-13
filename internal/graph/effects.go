package graph

import (
	"context"
	"sync/atomic"
)

type sideEffectKey struct{}

// MarkSideEffect records that an outbound mutation may have reached its
// destination. Call immediately before dispatch; the evidence is monotonic and
// shared with child executions, including when the node returns an error.
func MarkSideEffect(ctx context.Context) {
	if state, ok := ctx.Value(sideEffectKey{}).(*atomic.Bool); ok {
		state.Store(true)
	}
}
func effectContext(ctx context.Context) (context.Context, *atomic.Bool) {
	if state, ok := ctx.Value(sideEffectKey{}).(*atomic.Bool); ok {
		return ctx, state
	}
	state := &atomic.Bool{}
	return context.WithValue(ctx, sideEffectKey{}, state), state
}

// SideEffectTracker marks trusted nodes which report dispatch explicitly.
// Unclassified detached destinations are conservatively treated as uncertain.
type SideEffectTracker interface{ TracksSideEffects() }

func potentialSideEffects(term Terminal) bool {
	if term.SideEffects {
		return true
	}
	for _, d := range term.Deliveries {
		if !d.Unattempted && !d.NoEffect {
			return true
		}
	}
	return false
}

// AdmissionGuard marks fail-closed gate recovery paths, which may only produce
// local responses and cannot dispatch outbound work.
type AdmissionGuard interface{ AdmissionGuard() }
