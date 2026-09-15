package graph

import (
	"net/http"
	"net/url"
	"testing"
)

func TestPayloadsReportTheirPortType(t *testing.T) {
	cases := []struct {
		name string
		p    Payload
		want PortType
	}{
		{"RequestHead", RequestHead{}, PortRequestHead},
		{"RawRequest", RawRequest{}, PortRawRequest},
		{"Verified", Verified{}, PortVerified},
		{"Envelope", Envelope{}, PortEnvelope},
		{"Terminal", Terminal{}, PortTerminal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.PortType(); got != tc.want {
				t.Errorf("PortType() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestRawRequestNestsHeadByField pins the structural choice that backs the
// strict-equality rule: RawRequest holds its RequestHead in a named field
// rather than embedding it, so Go's own assignability rules cannot be used to
// pass a RawRequest where a RequestHead is expected either.
func TestRawRequestNestsHeadByField(t *testing.T) {
	raw := RawRequest{
		Head: RequestHead{Method: http.MethodPost, Path: "/webhooks/github"},
		Body: []byte(`{"a":1}`),
	}
	if raw.Head.Method != http.MethodPost {
		t.Errorf("Head.Method = %q, want POST", raw.Head.Method)
	}
	var _ Payload = raw
}

func TestRequestHeadCarriesQueryForQueryParamAuth(t *testing.T) {
	// Qualia authenticates from the query string (gaps doc A5), so the
	// pre-body phase must carry query values.
	head := RequestHead{Query: url.Values{"authID": []string{"secret"}}}
	if got := head.Query.Get("authID"); got != "secret" {
		t.Errorf("Query.Get(authID) = %q, want %q", got, "secret")
	}
}
