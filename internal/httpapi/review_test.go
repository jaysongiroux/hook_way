package httpapi

import (
	"context"
	"encoding/json"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewCodeBodyReachesPublisher(t *testing.T) {
	src := strings.Replace(testFlowYAML, "      - {id: out,", `      - {id: code, type: code, script: 'return {body: {changed: true}}'}
      - {id: out,`, 1)
	src = strings.Replace(src, "env>out]", "env>code, code>out]", 1)
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, src), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	capture := &fakeReplayCapturer{}
	srv.SetReplayCapturer(capture)
	body := `{"original":true}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	got := pub.published[0].msg.Body.(map[string]any)
	if got["changed"] != true || got["original"] != nil {
		t.Fatalf("published original body: %#v", got)
	}
	captured := capture.capturedEvents()
	if len(captured) != 1 || captured[0].Envelope.Body.(map[string]any)["changed"] != true {
		t.Fatalf("replay retained original body: %+v", captured)
	}
}

func TestReviewBrokenFlowIsNotReady(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	fc := cfg.Flows["github"]
	fc.Edges = []string{"in>env"}
	cfg.Flows["github"] = fc
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.readiness.probeOnce(context.Background())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "flow_compile") {
		t.Fatalf("broken flow ready: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReviewStandaloneLookupFailsBeforeTraffic(t *testing.T) {
	cfg := loadFlowCfg(t, lookupFlowYAML)
	if srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil); err == nil {
		_ = srv.Close()
		t.Fatal("unbound lookup accepted")
	}
}

func TestReviewCustomAuthCredentialsStayOutOfCodeAndCapture(t *testing.T) {
	for _, source := range []string{"header", "query"} {
		t.Run(source, func(t *testing.T) {
			src := strings.Replace(testFlowYAML, "scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256", "scheme: exact, secret: credential, source: {kind: "+source+", key: X-Partner}", 1)
			src = strings.Replace(src, "      - {id: out,", `      - {id: code, type: code, script: 'return {body: input.headers}'}
      - {id: out,`, 1)
			src = strings.Replace(src, "env>out]", "env>code, code>out]", 1)
			pub := &fakePublisher{}
			srv, err := NewFlowServer(loadFlowCfg(t, src), pub, slog.Default(), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Close() })
			capture := &fakeReplayCapturer{}
			srv.SetReplayCapturer(capture)
			path := "/webhooks/github"
			if source == "query" {
				path += "?X-Partner=credential"
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"ok":true}`))
			if source == "header" {
				req.Header.Set("X-Partner", "credential")
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != 202 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			data, err := json.Marshal(capture.capturedEvents())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "credential") {
				t.Fatalf("credential leaked: %s", data)
			}
		})
	}
}

func TestReviewDynamicTargetWorksThroughRealFactory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer upstream.Close()
	src := strings.ReplaceAll(testFlowYAML, "https://example.com/hooks", upstream.URL)
	src = strings.Replace(src, `url: "`+upstream.URL+`"`, `url: "`+upstream.URL+`"`+"\n      delivery_mode: sync", 1)
	src = strings.Replace(src, "path: /webhooks/github", `path: "/webhooks/{route}"`, 1)
	src = strings.Replace(src, "target: github-webhooks", "target: '{route}-webhooks'", 1)
	cfg := loadFlowCfg(t, src)
	pub, err := queue.NewFactory(queue.FactoryOptions{Metrics: metrics.New(), Logger: slog.Default()}).Create(context.Background(), cfg.ServingConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	srv, err := NewFlowServer(cfg, pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	capture := &fakeReplayCapturer{}
	srv.SetReplayCapturer(capture)
	body := `{"ok":true}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/tenant", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	outcomes := capture.recordedOutcomes()
	if len(outcomes) != 1 || outcomes[0].deliveries[0].Target != "tenant-webhooks" {
		t.Fatalf("replay target is not rendered: %+v", outcomes)
	}
}
