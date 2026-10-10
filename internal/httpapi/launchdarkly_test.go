package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

type testFlagClient struct{ calls, closed int }

func (c *testFlagClient) Close() error { c.closed++; return nil }
func (c *testFlagClient) Evaluate(featureflags.Context, string, string, any) featureflags.Result {
	c.calls++
	return featureflags.Result{Value: true}
}
func TestLaunchDarklyBindingSharesClientAndRetainsPartialResources(t *testing.T) {
	client := &testFlagClient{}
	constructions := 0
	factory := func(c flowconfig.LaunchDarklyConnection, status func(string)) (flagClient, error) {
		constructions++
		if c.SDKKeyEnv == "FAIL" {
			return nil, errors.New("missing SDK key")
		}
		status("VALID")
		return client, nil
	}
	bind, closers, err := bindLaunchDarklyWithFactory(map[string]flowconfig.LaunchDarklyConnection{"production": {SDKKeyEnv: "LD_KEY"}}, metrics.New(), factory)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		bind("production")(featureflags.Context{}, "flag", "boolean", false)
	}
	if constructions != 1 || client.calls != 10 || bind("missing") != nil || len(closers) != 1 {
		t.Fatal("client was not shared per connection")
	}
	_ = closers[0].Close()
	if client.closed != 1 {
		t.Fatal("client did not close")
	}
	client.closed = 0
	_, closers, err = bindLaunchDarklyWithFactory(map[string]flowconfig.LaunchDarklyConnection{"a": {SDKKeyEnv: "LD_KEY"}, "b": {SDKKeyEnv: "FAIL"}}, metrics.New(), factory)
	if err == nil || len(closers) != 1 {
		t.Fatal("partial startup lost resources")
	}
	for _, c := range closers {
		_ = c.Close()
	}
	if client.closed != 1 {
		t.Fatal("partial client did not close")
	}
}

// Exercise the checked-in example without credentials or external services.
func TestLaunchDarklyTenantRolloutExample(t *testing.T) {
	data, err := os.ReadFile("../../examples/launchdarkly.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := loadFlowCfg(t, string(data))
	for _, mode := range []string{"enabled", "disabled", "disconnected", "invalid_context"} {
		t.Run(mode, func(t *testing.T) {
			evaluations := 0
			bind := func(connection string) featureflags.Evaluator {
				if connection != "production" {
					t.Fatalf("connection = %q", connection)
				}
				return func(ctx featureflags.Context, key, flagType string, fallback any) featureflags.Result {
					evaluations++
					if ctx.Kind != "organization" || ctx.Key != "acme" || ctx.Attributes["plan"] != "enterprise" {
						t.Fatalf("context = %#v", ctx)
					}
					if mode == "disconnected" {
						return featureflags.Result{Value: fallback, UsedFallback: true, Reason: "ERROR", ErrorKind: "CLIENT_NOT_READY"}
					}
					values := map[string]any{"use-new-processor": mode == "enabled", "processor-name": "v2", "rate-limit": float64(250), "tenant.settings": map[string]any{"nested": map[string]any{"enabled": true}}}
					value, exists := values[key]
					if !exists || !featureflags.Matches(flagType, value) {
						t.Fatalf("unexpected evaluation: %s %s", key, flagType)
					}
					return featureflags.Result{Value: value, Reason: "FALLTHROUGH"}
				}
			}
			srv, err := newFlowServer(cfg, &fakePublisher{}, nil, nil, flowconfig.RegistryOptions{LaunchDarkly: bind})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Close() })
			body := `{"tenant":{"id":"acme","plan":"enterprise"}}`
			if mode == "invalid_context" {
				body = `{"tenant":{"id":42,"plan":"enterprise"}}`
			}
			req := httptest.NewRequest(http.MethodPost, "/webhooks/rollout", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, req)
			want := "existing processor: v2"
			if mode == "enabled" {
				want = "new processor: v2"
			}
			if mode == "disconnected" || mode == "invalid_context" {
				want = "existing processor: legacy"
			}
			if response.Code != http.StatusOK || response.Body.String() != want {
				t.Fatalf("response = %d %q; want %q", response.Code, response.Body.String(), want)
			}
			wantCalls := 4
			if mode == "invalid_context" {
				wantCalls = 0
			}
			if evaluations != wantCalls {
				t.Fatalf("evaluations = %d, want %d", evaluations, wantCalls)
			}
		})
	}
}

func TestLaunchDarklyDownstreamRedriveRetainsOriginalDecision(t *testing.T) {
	data, err := os.ReadFile("../../examples/launchdarkly.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := loadFlowCfg(t, string(data))
	srv, err := newFlowServer(cfg, &fakePublisher{}, nil, nil, flowconfig.RegistryOptions{LaunchDarkly: func(string) featureflags.Evaluator {
		return func(featureflags.Context, string, string, any) featureflags.Result {
			t.Error("upstream feature flags were reevaluated on downstream replay")
			return featureflags.Result{Value: false, Reason: "OFF"}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	env := graph.Envelope{Body: map[string]any{"tenant": map[string]any{"id": "acme"}}, BodyFormat: "json", Vars: map[string]string{"processor": "original"}, Flags: map[string]any{"rollout": map[string]any{"new_processor": map[string]any{"value": true, "used_fallback": false, "reason": "FALLTHROUGH"}}}}
	dto, err := capture.SnapshotFromEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &dto})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := decode(t, raw)
	result, err := srv.Redrive(context.Background(), replaywire.DeliverRequest{Flow: "tenant_rollout", NodeID: "choose", NodeType: "if_else", ReplayMode: replaywire.ReplayResume, DeadlineMS: 5000}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got := outcomes(result)
	if got["choose"] != replaywire.NodeDelivered || got["new"] != replaywire.NodeDelivered || len(got) != 2 {
		t.Fatalf("original decision was lost: %v", got)
	}
}
