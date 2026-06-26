package queue

import (
	"net/http"
	"testing"
)

func TestResolveFIFOGroupIDSources(t *testing.T) {
	msg := Message{
		Endpoint:       "billing",
		IdempotencyKey: "idem-1",
		Headers:        http.Header{"X-Tenant": []string{"acme"}},
		Body:           map[string]any{"customer": map[string]any{"id": "cust-42", "num": float64(12345), "active": true}},
	}
	cases := []struct {
		name    string
		s       FIFOSettings
		want    string
	}{
		{"static", FIFOSettings{GroupIDSource: "static", GroupIDValue: "fixed"}, "fixed"},
		{"endpoint", FIFOSettings{GroupIDSource: "endpoint"}, "billing"},
		{"header", FIFOSettings{GroupIDSource: "header", GroupIDPath: "X-Tenant"}, "acme"},
		{"body", FIFOSettings{GroupIDSource: "body", GroupIDPath: "customer.id"}, "cust-42"},
		{"body-missing-fallback-endpoint", FIFOSettings{GroupIDSource: "body", GroupIDPath: "nope.here", GroupIDFallback: "endpoint"}, "billing"},
		{"body-missing-fallback-literal", FIFOSettings{GroupIDSource: "body", GroupIDPath: "nope", GroupIDFallback: "default-group"}, "default-group"},
		{"body-numeric-id", FIFOSettings{GroupIDSource: "body", GroupIDPath: "customer.num"}, "12345"},
		{"body-bool-flag", FIFOSettings{GroupIDSource: "body", GroupIDPath: "customer.active"}, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gid, _, _ := resolveFIFO(msg, tc.s)
			if gid != tc.want {
				t.Fatalf("group id = %q, want %q", gid, tc.want)
			}
		})
	}
}

func TestResolveFIFODedup(t *testing.T) {
	withKey := Message{IdempotencyKey: "idem-1"}
	noKey := Message{}

	_, dedup, content := resolveFIFO(withKey, FIFOSettings{GroupIDSource: "endpoint", DedupUseIdempotencyKey: true, DedupContentBasedFallback: true})
	if dedup != "idem-1" || content {
		t.Fatalf("dedup=%q content=%v, want idem-1/false", dedup, content)
	}

	_, dedup, content = resolveFIFO(noKey, FIFOSettings{GroupIDSource: "endpoint", DedupUseIdempotencyKey: true, DedupContentBasedFallback: true})
	if dedup != "" || !content {
		t.Fatalf("dedup=%q content=%v, want empty/true", dedup, content)
	}

	_, dedup, content = resolveFIFO(noKey, FIFOSettings{GroupIDSource: "endpoint", DedupUseIdempotencyKey: true, DedupContentBasedFallback: false})
	if dedup != "" || content {
		t.Fatalf("dedup=%q content=%v, want empty/false", dedup, content)
	}
}
