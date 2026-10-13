package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

func routeTestConfig() routing.Config {
	return routing.Config{Profiles: map[string]routing.Profile{"sandbox": {AccountID: "000000000000", CredentialMode: "sandbox"}, "production": {AccountID: "111111111111", CredentialMode: "production"}}, Environments: map[string]routing.Environment{"preview": {Aliases: []string{"staging"}, Profiles: []string{"sandbox"}, Accounts: []string{"000000000000"}, DestinationPrefix: "preview"}, "production": {Profiles: []string{"production"}, Accounts: []string{"111111111111"}, DestinationPrefix: "prod"}}}
}
func routeTestRegistry(t *testing.T) *routing.Registry {
	t.Helper()
	r, err := routing.NewRegistry(routeTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type routeReader func(context.Context, routing.Key) (routing.Record, bool, error)

func (f routeReader) Get(c context.Context, k routing.Key) (routing.Record, bool, error) {
	return f(c, k)
}
func TestRouteExtractionPriorityAndClassification(t *testing.T) {
	opts := RouteOptions{Profile: "sandbox", Store: "primary", Candidates: []RouteCandidate{{Source: "body", Path: "record_a", IDType: "record_a"}, {Source: "query", Path: "record_a", IDType: "record_a"}, {Source: "body", Path: "record_b", IDType: "record_b"}, {Source: "query", Path: "record_b", IDType: "record_b"}}}
	var got routing.Key
	reg := RegistryOptions{RoutingRegistry: routeTestRegistry(t), RoutingStores: map[string]routing.Reader{"primary": routeReader(func(_ context.Context, k routing.Key) (routing.Record, bool, error) {
		got = k
		return routing.Record{Environment: "preview"}, true, nil
	})}}
	n, err := newRouteNode("route", opts, reg)
	if err != nil {
		t.Fatal(err)
	}
	env := graph.Envelope{Body: map[string]any{"record_b": "body-b"}, Vars: map[string]string{"previous": "unchanged"}, V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Query: url.Values{"record_a": []string{"query-a"}}}}}}
	out, err := n.Run(context.Background(), env)
	if err != nil || got != (routing.Key{ID: "query-a", IDType: "record_a"}) {
		t.Fatal(got, err)
	}
	if out.(graph.Envelope).Vars["route"] != "preview" || len(env.Vars) != 1 {
		t.Fatal("route mutated input or failed to bind")
	}
	opts.Candidates = []RouteCandidate{{Source: "body", Path: "correlation_id", IDType: "record_a", Pattern: `[a-fA-F0-9]{24}`}, {Source: "body", Path: "correlation_id", IDType: "record_c", Pattern: `[A-Z]{2}[0-9]{6}`}}
	n, _ = newRouteNode("route", opts, reg)
	for _, tc := range []struct{ id, typ string }{{"abcdefabcdefabcdefabcdef", "record_a"}, {"AB123456", "record_c"}} {
		env.Body = map[string]any{"correlation_id": tc.id}
		if _, err := n.Run(context.Background(), env); err != nil || got.IDType != tc.typ {
			t.Fatal(got, err)
		}
	}
	env.Body = map[string]any{"correlation_id": "bad"}
	if _, err := n.Run(context.Background(), env); err == nil {
		t.Fatal("malformed identifier routed")
	}
}
func TestRouteBoundedFailureReasonsAndFixedOverride(t *testing.T) {
	reg := routeTestRegistry(t)
	opts := RouteOptions{Profile: "sandbox", Store: "primary", Candidates: []RouteCandidate{{Source: "body", Path: "id", IDType: "record_a"}}}
	for _, tc := range []struct {
		body        any
		found       bool
		backend     error
		env, reason string
		status      int
	}{{map[string]any{"id": "abc"}, false, nil, "", "miss", 400}, {map[string]any{"id": "abc"}, false, errors.New("outage"), "", "store_error", 503}, {map[string]any{"id": "abc"}, true, nil, "production", "environment_denied", 403}, {map[string]any{"id": 42}, true, nil, "preview", "key_invalid", 400}, {map[string]any{}, false, nil, "", "key_invalid", 400}} {
		observed := ""
		n, err := newRouteNode("route", opts, RegistryOptions{RoutingRegistry: reg, RoutingObservation: func(_, r string) { observed = r }, RoutingStores: map[string]routing.Reader{"primary": routeReader(func(context.Context, routing.Key) (routing.Record, bool, error) {
			return routing.Record{Environment: tc.env}, tc.found, tc.backend
		})}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = n.Run(context.Background(), graph.Envelope{Body: tc.body})
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != tc.status || ne.Reason != tc.reason || observed != tc.reason {
			t.Fatal(err, observed)
		}
	}
	n, err := newRouteNode("fixed", RouteOptions{Profile: "sandbox", Fixed: "preview"}, RegistryOptions{RoutingRegistry: reg})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Run(context.Background(), graph.Envelope{Body: map[string]any{"environment": "production"}, V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Query: url.Values{"environment": []string{"production"}}, Header: http.Header{"X-Env-Name": []string{"production"}}}}}})
	if err != nil || out.(graph.Envelope).Vars["route"] != "preview" {
		t.Fatal(out, err)
	}
}
func TestDirectRoutePriorityDuplicateAndRevocation(t *testing.T) {
	reg := routeTestRegistry(t)
	n, _ := newRouteNode("r", RouteOptions{Profile: "sandbox", Candidates: []RouteCandidate{{Source: "body", Path: "x_env_name"}, {Source: "query", Path: "x_env_name"}, {Source: "header", Path: "X-Env-Name"}}}, RegistryOptions{RoutingRegistry: reg})
	env := graph.Envelope{Body: map[string]any{"x_env_name": "staging"}, V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Query: url.Values{"x_env_name": []string{"production"}}, Header: http.Header{"X-Env-Name": []string{"production"}}}}}}
	out, err := n.Run(context.Background(), env)
	if err != nil || out.(graph.Envelope).Vars["route"] != "preview" {
		t.Fatal(out, err)
	}
	env.Body = map[string]any{"x_env_name": ""}
	env.V.Raw.Head.Query = url.Values{"x_env_name": []string{"preview", "production"}}
	if _, err := n.Run(context.Background(), env); err == nil {
		t.Fatal("duplicate query accepted")
	}
	c := routeTestConfig()
	delete(c.Environments, "preview")
	if err := reg.Replace(c); err != nil {
		t.Fatal(err)
	}
	env.Body = map[string]any{"x_env_name": "preview"}
	if _, err := n.Run(context.Background(), env); err == nil {
		t.Fatal("revoked name routed")
	}
}
func TestConcurrentRouteDoesNotShareRequestVars(t *testing.T) {
	n, _ := newRouteNode("r", RouteOptions{Profile: "sandbox", Fixed: "preview"}, RegistryOptions{RoutingRegistry: routeTestRegistry(t)})
	env := graph.Envelope{Vars: map[string]string{"existing": "one"}}
	var w sync.WaitGroup
	for i := 0; i < 30; i++ {
		w.Add(1)
		go func() {
			defer w.Done()
			o, err := n.Run(context.Background(), env)
			if err != nil {
				t.Error(err)
			}
			o.(graph.Envelope).Vars["existing"] = "changed"
		}()
	}
	w.Wait()
	if !reflect.DeepEqual(env.Vars, map[string]string{"existing": "one"}) {
		t.Fatal(env.Vars)
	}
}

func TestRoutingConfigValidationRejectsBindingsAndUnknownFields(t *testing.T) {
	const src = `server: {addr: ":8080", max_body_bytes: 1024}
routing:
  profiles: {sandbox: {account_id: "000000000000", credential_mode: sandbox}}
  environments: {preview: {profiles: [sandbox], accounts: ["000000000000"], destination_prefix: preview}}
flows:
  sample:
    nodes:
      - {id: in, type: http_trigger, path: /sample}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: r, type: route, profile: sandbox, fixed: preview}
      - {id: done, type: respond, status: 200}
    edges: [in>body, body>auth, auth>env, env>r, r>done]
`
	if _, issues := LoadBytes([]byte(src), "test", false); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, replace := range []struct{ from, to string }{{"fixed: preview", "fixed: unknown"}, {"profile: sandbox", "profile: missing"}, {"fixed: preview", "store: missing, candidates: [{source: body, path: id, id_type: record_a}]"}, {"credential_mode: sandbox", "credential_mode: sandbox, typo: true"}, {"credential_mode: sandbox", "credential_mode: sandbox, account_id: bad"}} {
		if _, issues := LoadBytes([]byte(strings.Replace(src, replace.from, replace.to, 1)), "test", false); len(issues) == 0 {
			t.Fatal("invalid config accepted", replace)
		}
	}
}
