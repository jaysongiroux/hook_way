package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestFlowPolicyHEADResponseHasNoBody(t *testing.T) {
	src := strings.Replace(testFlowYAML, "methods: [POST]", "methods: [POST, HEAD]", 1)
	src = strings.Replace(src, "  github:\n", "  github:\n    responses:\n      published: {status: 200, body: 'acknowledged', headers: {X-Result: success}}\n", 1)
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/webhooks/github", strings.NewReader(`{"x":1}`))
	req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(`{"x":1}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("X-Result") != "success" {
		t.Fatalf("HEAD response: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}

func TestRouteResponsePreservesFailedDeliveryAccounting(t *testing.T) {
	src := strings.Replace(testFlowYAML, "  github:\n", `  github:
    responses:
      published: {status: 200, body: '{"status":"ok"}', headers: {Content-Type: application/json}}
      publish_error: {status: 200, body: '{"status":"acknowledged"}'}
      auth_error: {status: 400, body: '{"error":"invalid credentials"}'}
      body_error: {status: 400, body: '{"error":"invalid JSON"}'}
`, 1)
	for _, tc := range []struct {
		name      string
		err       error
		signature bool
		body      string
		status    int
		outcome   string
		sends     int
	}{
		{"success", nil, true, `{"x":1}`, 200, "published", 1},
		{"acknowledged failure", errors.New("private broker diagnostic"), true, `{"x":1}`, 200, "publish_error", 1},
		{"auth before envelope", nil, false, `{"x":1}`, 400, "auth_error", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub := &fakePublisher{err: tc.err}
			srv, err := NewFlowServer(loadFlowCfg(t, src), pub, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			req := httptest.NewRequestWithContext(context.Background(), "POST", "/webhooks/github", strings.NewReader(tc.body))
			if tc.signature {
				req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(tc.body)))
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.status || len(pub.published) != tc.sends || strings.Contains(rec.Body.String(), "private") {
				t.Fatalf("response=%d %s sends=%d", rec.Code, rec.Body.String(), len(pub.published))
			}
			metrics := httptest.NewRecorder()
			promhttp.HandlerFor(srv.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(metrics, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
			expected := `hookway_flow_outcomes_total{flow="github",outcome="` + tc.outcome + `"} 1`
			if !strings.Contains(metrics.Body.String(), expected) {
				t.Fatalf("missing outcome %s", expected)
			}
			if tc.err != nil && strings.Contains(metrics.Body.String(), `hookway_messages_published_total{`) {
				t.Fatal("acknowledged failure counted as published")
			}
		})
	}
}

func TestSyntheticFIFOIngressStableAcrossServers(t *testing.T) {
	src := `server: {addr: ":8080", max_body_bytes: 1048576}
destinations:
  broker: {type: sqs, sqs: {region: us-east-1}}
flows:
  sample:
    responses:
      published: {status: 200, body: '{"status":"ok"}', headers: {Content-Type: application/json}}
      auth_error: {status: 400, body: '{"error":"invalid credentials"}'}
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/sample, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: exact, secret: synthetic-secret, signature_header: Authorization, remove_prefix: "Api-Key "}
      - {id: env, type: envelope, body_format: json}
      - id: out
        type: publish
        destination: broker
        target: synthetic-events.fifo
        wire_body: json_js
        fifo:
          group_id: {source: json_js_sha256, hash_length: 10}
          deduplication_id: {source: json_js_sha256, hash_length: 10}
    edges: [in>body, body>auth, auth>env, env>out]
`
	pubs := []*fakePublisher{{}, {}}
	for i, token := range []string{"synthetic-secret", "Api-Key synthetic-secret"} {
		srv, err := NewFlowServer(loadFlowCfg(t, src), pubs[i], nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = srv.Close() }()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/sample", bytes.NewBufferString(` { "b": 2, "a": 1 } `))
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 200 || len(pubs[i].published) != 1 {
			t.Fatalf("response %d %s", rec.Code, rec.Body.String())
		}
	}
	first, second := pubs[0].published[0].msg, pubs[1].published[0].msg
	sum := sha256.Sum256([]byte(`{"b":2,"a":1}`))
	id := hex.EncodeToString(sum[:])[:10]
	if string(first.Publish.Body) != `{"b":2,"a":1}` || first.Publish.FIFO.GroupID != id || first.Publish.FIFO.DeduplicationID != id || second.Publish.FIFO.DeduplicationID != id || first.RequestID == second.RequestID {
		t.Fatalf("unstable publication: %+v %+v", first.Publish, second.Publish)
	}
	if strings.Contains(string(first.Publish.Body), "synthetic-secret") {
		t.Fatal("credential leaked into body")
	}
}

func TestFlowOutcomeDeliveryAndDropClassification(t *testing.T) {
	mf := mountedFlow{nodeTypes: map[string]string{"body": "read_body"}}
	success := graph.Delivery{NodeID: "a"}
	failure := graph.Delivery{NodeID: "b", Err: errors.New("failed")}
	cases := []struct {
		term                   graph.Terminal
		err                    error
		dropped, publishFailed bool
		want                   string
	}{
		{graph.Terminal{}, nil, true, false, "dropped"},
		{graph.Terminal{Deliveries: []graph.Delivery{success, failure}}, nil, false, false, "partial_failure"},
		{graph.Terminal{Deliveries: []graph.Delivery{failure}}, nil, false, true, "publish_error"},
		{graph.Terminal{}, graph.NodeError{NodeID: "body", Status: 413, Err: errors.New("large")}, false, false, "payload_too_large"},
	}
	for _, c := range cases {
		if got := flowOutcome(mf, c.term, c.err, c.dropped, c.publishFailed); got != c.want {
			t.Errorf("outcome=%s want %s", got, c.want)
		}
	}
}
