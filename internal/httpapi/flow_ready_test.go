package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasongiroux/hookway/internal/queue"
)

const twoFlowYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1024
destinations:
  alpha:
    type: webhook
    webhook: {url: "https://alpha.test"}
  beta:
    type: webhook
    webhook: {url: "https://beta.test"}
flows:
  flow_alpha:
    nodes:
      - {id: in, type: http_trigger, path: /a, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: alpha, target: a}
    edges: [in>body, body>auth, auth>env, env>out]
  flow_beta:
    nodes:
      - {id: in, type: http_trigger, path: /b, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: beta, target: b}
    edges: [in>body, body>auth, auth>env, env>out]
`

func TestFlowReadyKeepsHealthyFlowWhenPeerDestinationIsDown(t *testing.T) {
	alpha := &countingReadyPublisher{}
	beta := &countingReadyPublisher{fakePublisher: fakePublisher{readyErr: errors.New("beta down")}}
	pub := queue.NewRoutingPublisher(map[string]queue.Publisher{"alpha": alpha, "beta": beta})
	srv, err := NewFlowServer(loadFlowCfg(t, twoFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.readiness.probeOnce(context.Background())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/ready = %d %s, want 200 so the healthy flow stays in rotation", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Flows  map[string]struct {
			Ready   bool     `json:"ready"`
			Unready []string `json:"unready"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Flows["flow_alpha"].Ready {
		t.Fatalf("flow_alpha unready: %+v", body.Flows["flow_alpha"])
	}
	if body.Flows["flow_beta"].Ready {
		t.Fatal("flow_beta should be unready")
	}

	health := httptest.NewRecorder()
	srv.ServeHTTP(health, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200 (liveness never reflects destinations)", health.Code)
	}
}

func TestReadyProbeCountEqualsUniqueDestinations(t *testing.T) {
	shared := &countingReadyPublisher{}
	other := &countingReadyPublisher{}
	pub := queue.NewRoutingPublisher(map[string]queue.Publisher{"alpha": shared, "beta": other})
	src := twoFlowYAML + `
  flow_shared:
    nodes:
      - {id: in, type: http_trigger, path: /s, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: alpha, target: s}
    edges: [in>body, body>auth, auth>env, env>out]
`
	srv, err := NewFlowServer(loadFlowCfg(t, src), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.readiness.probeOnce(context.Background())
	if shared.readyCalls.Load() != 1 || other.readyCalls.Load() != 1 {
		t.Fatalf("Ready calls alpha=%d beta=%d, want 1 each (unique dests, not flows×dests)",
			shared.readyCalls.Load(), other.readyCalls.Load())
	}
	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("/ready = %d", rec.Code)
		}
	}
	if shared.readyCalls.Load() != 1 || other.readyCalls.Load() != 1 {
		t.Fatalf("probes grew with /ready traffic: alpha=%d beta=%d", shared.readyCalls.Load(), other.readyCalls.Load())
	}
}

func TestReadyRequireAllReturns503WhenAnyFlowIsUnready(t *testing.T) {
	alpha := &countingReadyPublisher{}
	beta := &countingReadyPublisher{fakePublisher: fakePublisher{readyErr: errors.New("down")}}
	pub := queue.NewRoutingPublisher(map[string]queue.Publisher{"alpha": alpha, "beta": beta})
	src := twoFlowYAML
	cfg := loadFlowCfg(t, src)
	cfg.Server.ReadinessRequireAll = true
	srv, err := NewFlowServer(cfg, pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.readiness.probeOnce(context.Background())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready require_all = %d, want 503", rec.Code)
	}
}

func TestHealthzIgnoresDestinationState(t *testing.T) {
	down := &countingReadyPublisher{fakePublisher: fakePublisher{readyErr: errors.New("down")}}
	pub := queue.NewRoutingPublisher(map[string]queue.Publisher{"alpha": down, "beta": down})
	srv, err := NewFlowServer(loadFlowCfg(t, twoFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.readiness.probeOnce(context.Background())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", rec.Code)
	}
}
