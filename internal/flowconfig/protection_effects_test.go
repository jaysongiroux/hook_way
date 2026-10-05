package flowconfig

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

const guardedEffectYAML = `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1024}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: guard, type: replay_guard, store: test, ttl: 1m}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: events}
    edges: [in>body, body>auth, auth>guard, guard>env, env>out]
`

func effectFlow(t *testing.T, src string) *graph.Flow {
	t.Helper()
	cfg, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	reg, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{FlowID: "demo"}))
	if err != nil {
		t.Fatal(err)
	}
	flow, err := graph.Compile("demo", cfg.Flows["demo"], reg)
	if err != nil {
		t.Fatal(err)
	}
	return flow
}
func runEffectFlow(flow *graph.Flow) (graph.Terminal, error) {
	ctx := WithBodyReader(context.Background(), io.NopCloser(strings.NewReader("{}")))
	return flow.Run(ctx, graph.RequestHead{}, graph.RunOptions{PublishTimeout: time.Second})
}

func TestGuardCompensationOwnsOutputAndReleasesKnownPreDispatchFailure(t *testing.T) {
	flow := effectFlow(t, guardedEffectYAML)
	term, err := runEffectFlow(flow)
	if err == nil || term.SideEffects || len(term.Compensations) != 1 || !graph.NeedsCompensation(term, err) {
		t.Fatalf("%+v %v", term, err)
	}
	saved := term.Compensations[0].Input.(graph.Verified)
	if saved.Reservations["guard"].Owner == "" {
		t.Fatal("executor did not capture reservation owner from successful output")
	}
	if err := graph.RunCompensations(context.Background(), term, err, time.Second); err != nil {
		t.Fatal(err)
	}
	_, err = runEffectFlow(flow)
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 503 {
		t.Fatalf("known local failure did not release: %v", err)
	}
}

func TestGuardRetainedAfterEarlierHTTPRequestAndLaterLocalFailure(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte("{}")) }))
	defer upstream.Close()
	src := strings.Replace(guardedEffectYAML, "      - {id: out, type: publish, destination: primary, target: events}", "      - {id: up, type: http_request, url: '"+upstream.URL+"', timeout: 1s}\n      - {id: bad, type: json_validate, schema: {type: array}}\n      - {id: done, type: respond, status: 200}", 1)
	src = strings.Replace(src, "env>out]", "env>up, up>bad, bad>done]", 1)
	flow := effectFlow(t, src)
	term, err := runEffectFlow(flow)
	if err == nil || !term.SideEffects || calls != 1 || graph.NeedsCompensation(term, err) {
		t.Fatalf("effects missing: %+v %v calls=%d", term, err, calls)
	}
	if err := graph.RunCompensations(context.Background(), term, err, time.Second); err != nil {
		t.Fatal(err)
	}
	_, err = runEffectFlow(flow)
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 409 || calls != 1 {
		t.Fatalf("guard released after HTTP effect: %v calls=%d", err, calls)
	}
}
