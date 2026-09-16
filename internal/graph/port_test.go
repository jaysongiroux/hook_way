package graph

import "testing"

// TestAcceptsIsStrictEquality is the load-bearing test of the typed-port design.
// RawRequest carries a RequestHead, so the intuitive choice is to make it
// assignable to RequestHead. It must not be: if a gate node (rate_limit,
// ip_allowlist, loop_guard) can be wired downstream of read_body, then
// invariants I1-I4 -- gates run before the body is buffered -- silently
// evaporate, and typed ports lose the property they were chosen for.
func TestAcceptsIsStrictEquality(t *testing.T) {
	all := []PortType{PortNone, PortRequestHead, PortRawRequest, PortVerified, PortEnvelope, PortTerminal}
	for _, in := range all {
		for _, out := range all {
			want := in == out
			if got := in.Accepts(out); got != want {
				t.Errorf("PortType(%s).Accepts(%s) = %v, want %v", in, out, got, want)
			}
		}
	}
}

// TestRawRequestIsNotARequestHead names the specific regression that
// TestAcceptsIsStrictEquality exists to prevent, so a failure reads as the
// invariant it breaks rather than as an enum mismatch.
func TestRawRequestIsNotARequestHead(t *testing.T) {
	if PortRequestHead.Accepts(PortRawRequest) {
		t.Fatal("PortRequestHead accepts PortRawRequest: a gate node could now be wired " +
			"after read_body, breaking invariants I1-I4 (gates before body buffering)")
	}
}

func TestPortTypeString(t *testing.T) {
	cases := map[PortType]string{
		PortNone:        "None",
		PortRequestHead: "RequestHead",
		PortRawRequest:  "RawRequest",
		PortVerified:    "Verified",
		PortEnvelope:    "Envelope",
		PortTerminal:    "Terminal",
		PortType(99):    "PortType(99)",
	}
	for pt, want := range cases {
		if got := pt.String(); got != want {
			t.Errorf("PortType(%d).String() = %q, want %q", pt, got, want)
		}
	}
}
