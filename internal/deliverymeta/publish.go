// Package deliverymeta holds frozen per-publish metadata shared by flows,
// broker adapters and failure snapshots. It has no SDK dependencies.
package deliverymeta

import "slices"

type Publish struct {
	GroupID          string `json:"group_id,omitempty"`
	DelaySeconds     *int32 `json:"delay_seconds,omitempty"`
	Subject          string `json:"subject,omitempty"`
	MessageStructure string `json:"message_structure,omitempty"`
	NodeID           string `json:"node_id"`
	Destination      string `json:"destination"`
	Target           string `json:"target"`
	WireBody         string `json:"wire_body"`
	Body             []byte `json:"body"`
	FIFO             *FIFO  `json:"fifo,omitempty"`
	Retry            *Retry `json:"retry,omitempty"`
}

type FIFO struct {
	GroupID         string `json:"group_id"`
	DeduplicationID string `json:"deduplication_id"`
	ContentBased    bool   `json:"content_based"`
}

type Retry struct {
	MaxAttempts    int   `json:"max_attempts"`
	InitialBackoff int64 `json:"initial_backoff_ns"`
	MaxBackoff     int64 `json:"max_backoff_ns"`
}

func Clone(p *Publish) *Publish {
	if p == nil {
		return nil
	}
	out := *p
	out.Body = slices.Clone(p.Body)
	if p.DelaySeconds != nil {
		d := *p.DelaySeconds
		out.DelaySeconds = &d
	}
	if p.FIFO != nil {
		f := *p.FIFO
		out.FIFO = &f
	}
	if p.Retry != nil {
		r := *p.Retry
		out.Retry = &r
	}
	return &out
}

// ValidFIFOIdentity matches the broker's 1..128 ASCII alphanumeric and
// punctuation constraint. Spaces and non-ASCII characters are not legal IDs.
func ValidFIFOIdentity(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range []byte(id) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
