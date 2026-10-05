// Package replayresult owns the result produced by a replay worker. It depends
// only on the portable wire package; persisted historical results remain raw JSON.
package replayresult

import "github.com/jaysongiroux/hook_way/internal/replaywire"

const ReasonLeaseExpired = "lease_expired"

// Result is a newly produced run result. Readers must preserve unknown historical
// members rather than decoding and re-encoding stored results through this type.
type Result struct {
	Reason  string                    `json:"reason,omitempty"`
	Detail  string                    `json:"detail,omitempty"`
	Deliver *replaywire.DeliverResult `json:"deliver,omitempty"`
}
