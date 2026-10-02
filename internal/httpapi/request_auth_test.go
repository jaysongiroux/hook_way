package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

func TestRequestAuthenticationGatesAllDownstreamWork(t *testing.T) {
	var upstreamCalls, lookupCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	src := fmt.Sprintf(`server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {broker: {type: sqs, sqs: {region: us-east-1}}}
flows:
  sample:
    responses:
      auth_error: {status: 403, body: '{"error":"invalid signature"}'}
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/sample, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_headers_dot, secret: synthetic, signature_header: X-Sample-Signature, timestamp_header: X-Sample-Timestamp, timestamp_unit: milliseconds, timestamp_policy: age, body_representation: json_js}
      - {id: env, type: envelope, body_format: json}
      - {id: route, type: lookup, provider: dynamodb, table: synthetic-routing, key: sample, name: route}
      - {id: upstream, type: http_request, url: %s}
      - {id: out, type: publish, destination: broker, target: sample, wire_body: raw}
    edges: [in>body, body>auth, auth>env, env>route, route>upstream, upstream>out]
`, upstream.URL)
	pub := &fakePublisher{}
	srv, err := newFlowServer(loadFlowCfg(t, src), pub, nil, nil, flowconfig.RegistryOptions{
		Lookups: func(flowconfig.LookupOptions) flowconfig.LookupFunc {
			return func(context.Context, string) (string, bool, error) { lookupCalls.Add(1); return "sample", true, nil }
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	raw := `{ "b": 2, "a": 1 }`
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte("synthetic"))
	mac.Write([]byte(ts + "." + `{"b":2,"a":1}`))
	sig := hex.EncodeToString(mac.Sum(nil))
	for _, tc := range []struct {
		name, signature, timestamp, body, reason string
		duplicate                                bool
	}{
		{"missing", "", ts, raw, "missing_header", false},
		{"bad timestamp", sig, "bad", raw, "malformed_timestamp", false},
		{"bad signature", "bad", ts, raw, "malformed_signature", false},
		{"duplicate", sig, ts, raw, "duplicate_header", true},
		{"bad JSON", sig, ts, "not JSON", "invalid_body", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), "POST", "/webhooks/sample", strings.NewReader(tc.body))
			req.Header.Set("X-Sample-Signature", tc.signature)
			req.Header.Set("X-Sample-Timestamp", tc.timestamp)
			if tc.duplicate {
				req.Header.Add("X-Sample-Signature", sig)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != 403 || len(pub.published) != 0 || upstreamCalls.Load() != 0 || lookupCalls.Load() != 0 {
				t.Fatalf("failed auth reached downstream: %d %d %d", rec.Code, len(pub.published), upstreamCalls.Load())
			}
			if strings.Contains(rec.Body.String(), "synthetic") || strings.Contains(rec.Body.String(), sig) {
				t.Fatal("credential leak")
			}
			metrics := httptest.NewRecorder()
			promhttp.HandlerFor(srv.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(metrics, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
			if !strings.Contains(metrics.Body.String(), `hookway_auth_failures_total{endpoint="sample",reason="`+tc.reason+`"} 1`) {
				t.Fatalf("missing reason %s", tc.reason)
			}
		})
	}
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/sample", strings.NewReader(raw))
	req.Header.Set("X-Sample-Signature", sig)
	req.Header.Set("X-Sample-Timestamp", ts)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 202 || len(pub.published) != 1 || upstreamCalls.Load() != 1 || lookupCalls.Load() != 1 || string(pub.published[0].msg.Publish.Body) != raw {
		t.Fatalf("valid auth did not preserve body and publish: %d %+v", rec.Code, pub.published)
	}
}

func TestBadInitialKeyFailsServingButOfflineSecretRefsDoNotFetch(t *testing.T) {
	src := `server: {addr: ":8080", max_body_bytes: 1024}
flows:
  sample:
    nodes:
      - {id: in, type: http_trigger, path: /sample}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: ecdsa_p256_sha256_headers, secret_ref: {provider: env, name: SAMPLE_BAD_KEY}, signature_header: X-Sample-Signature, timestamp_header: X-Sample-Timestamp, timestamp_unit: seconds}
      - {id: env, type: envelope, body_format: raw}
      - {id: done, type: respond, status: 200}
    edges: [in>body, body>auth, auth>env, env>done]
`
	t.Setenv("SAMPLE_BAD_KEY", "sensitive-invalid-key")
	cfg, issues := flowconfig.LoadBytes([]byte(src), "test", false)
	for _, issue := range issues {
		if issue.Severity != "warning" {
			t.Fatal(issue)
		}
	}
	srv, err := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
	if srv != nil {
		_ = srv.Close()
	}
	if err == nil || strings.Contains(err.Error(), "sensitive-invalid-key") {
		t.Fatalf("startup failed to safely reject key: %v", err)
	}
}
