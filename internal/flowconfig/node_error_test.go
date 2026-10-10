package flowconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

// TestCatalogNodeRuntimeErrors requires every catalog type to have a Run()
// failure with a well-typed payload (or a wrong-type payload for nodes that
// have no semantic failure). A new node type that is not listed here fails
// this test so error coverage cannot drift.
func TestCatalogNodeRuntimeErrors(t *testing.T) {
	cases := catalogRuntimeErrors(t)
	for name := range nodeFixtures {
		if _, ok := cases[name]; !ok {
			t.Errorf("no runtime error case for %s", name)
		}
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := nodeFixtures[name]; !ok {
				t.Fatalf("%s is not in the catalog", name)
			}
			err := fn(t)
			if err == nil {
				t.Fatal("Run() = nil, want an error")
			}
			var ne graph.NodeError
			if !errors.As(err, &ne) {
				t.Fatalf("error %T %v is not a NodeError", err, err)
			}
			if ne.Status < 400 {
				t.Fatalf("NodeError status %d, want 4xx or 5xx", ne.Status)
			}
		})
	}
}

func catalogRuntimeErrors(t *testing.T) map[string]func(*testing.T) error {
	t.Helper()
	return map[string]func(*testing.T) error{
		"launchdarkly": func(t *testing.T) error {
			n := mustCatalogNode(t, "launchdarkly", nodeFixtures["launchdarkly"].yaml, RegistryOptions{})
			_, err := n.Run(context.Background(), graph.RequestHead{})
			return err
		},
		"batch": func(t *testing.T) error {
			n := mustCatalogNode(t, "batch", nodeFixtures["batch"].yaml, RegistryOptions{ValidateOnly: true})
			_, err := n.Run(context.Background(), graph.RequestHead{})
			return err
		},
		"register_route": func(t *testing.T) error {
			n := mustCatalogNode(t, "register_route", "mode: direct\nstore: primary\nprofile: sandbox\nenvironment_variable: route\nid_source: body\nid_path: id\nid_type: record_a\n", RegistryOptions{ValidateOnly: true})
			_, err := n.Run(context.Background(), graph.Envelope{})
			return err
		},
		"route": func(t *testing.T) error {
			n := mustCatalogNode(t, "route", "profile: sandbox\ncandidates: [{source: body, path: environment}]\n", RegistryOptions{RoutingRegistry: routeTestRegistry(t)})
			_, err := n.Run(context.Background(), graph.Envelope{Body: map[string]any{"environment": "unknown"}})
			return err
		},
		"http_trigger": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "http_trigger", nodeFixtures["http_trigger"].yaml, RegistryOptions{}).
				Run(context.Background(), graph.Envelope{})
			return err
		},
		"loop_guard": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "loop_guard", "max_hops: 1\n", RegistryOptions{}).
				Run(context.Background(), graph.RequestHead{Hops: 1})
			return err
		},
		"ip_allowlist": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "ip_allowlist", "cidrs: [10.0.0.0/8]\n", RegistryOptions{}).
				Run(context.Background(), graph.RequestHead{SourceIP: "203.0.113.7"})
			return err
		},
		"rate_limit": func(t *testing.T) error {
			node := mustCatalogNode(t, "rate_limit", "store: test\nrpm: 60\nburst: 1\nkey_by: source_ip\n", RegistryOptions{})
			head := graph.RequestHead{SourceIP: "1.1.1.1"}
			if _, err := node.Run(context.Background(), head); err != nil {
				t.Fatalf("first request: %v", err)
			}
			_, err := node.Run(context.Background(), head)
			return err
		},
		"read_body": func(t *testing.T) error {
			node := mustCatalogNode(t, "read_body", "max_bytes: 4\n", RegistryOptions{})
			ctx := WithBodyReader(context.Background(), strings.NewReader("too-big"))
			_, err := node.Run(ctx, graph.RequestHead{})
			return err
		},
		"auth": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "auth", "scheme: hmac_sha256_hex\nsecret: s\nsignature_header: X-S\n", RegistryOptions{}).
				Run(context.Background(), graph.RawRequest{
					Head: graph.RequestHead{Header: http.Header{"X-S": []string{"sha256=deadbeef"}}},
					Body: []byte(`{}`),
				})
			return err
		},
		"no_auth": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "no_auth", "", RegistryOptions{}).
				Run(context.Background(), graph.RequestHead{})
			return err
		},
		"replay_guard": func(t *testing.T) error {
			node := mustCatalogNode(t, "replay_guard", "store: test\nttl: 1m\nmax_entries: 100\n", RegistryOptions{})
			in := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"id":1}`)}}
			if _, err := node.Run(context.Background(), in); err != nil {
				t.Fatalf("first accept: %v", err)
			}
			_, err := node.Run(context.Background(), in)
			return err
		},
		"envelope": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "envelope", "body_format: xml\n", RegistryOptions{}).
				Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: []byte("<a>")}})
			return err
		},
		"filter": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "filter",
				"conditions: [{source: body, path: event, operator: eq, value: created}]\non_no_match: reject\n",
				RegistryOptions{}).
				Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "deleted"}})
			return err
		},
		"inspect": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "inspect", "", RegistryOptions{}).Run(context.Background(), graph.RequestHead{})
			return err
		},
		"sample": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "sample", "rate: 0.2\n", RegistryOptions{}).
				Run(context.Background(), graph.RequestHead{})
			return err
		},
		"lookup": func(t *testing.T) error {
			resolve := func(context.Context, string) (string, bool, error) { return "", false, nil }
			_, err := mustCatalogNode(t, "lookup", "provider: dynamodb\nkey: '{id}'\nname: route\n", RegistryOptions{
				Lookups: func(LookupOptions) LookupFunc { return resolve },
			}).Run(context.Background(), graph.Envelope{Vars: map[string]string{"id": "missing"}})
			return err
		},
		"json_validate": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "json_validate", "schema: {type: object, required: [id]}\n", RegistryOptions{}).Run(context.Background(), graph.Envelope{Body: map[string]any{}})
			return err
		},
		"transform": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "transform", "kind: xml_prune\n", RegistryOptions{}).
				Run(context.Background(), graph.Envelope{Body: map[string]any{"a": 1}})
			return err
		},
		"code": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "code", "script: 'return 1'\ntimeout: 1s\n", RegistryOptions{}).
				Run(context.Background(), graph.Envelope{Body: map[string]any{}})
			return err
		},
		"switch": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "switch",
				"cases: [{port: matched, when: {source: body, path: event, operator: eq, value: created}}]\n",
				RegistryOptions{}).
				Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "other"}})
			return err
		},
		// if_else has no semantic failure: a condition that does not hold is
		// the false branch, not an error. Only a mistyped payload fails.
		"if_else": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "if_else",
				"conditions: [{source: body, path: event, operator: eq, value: created}]\n",
				RegistryOptions{}).
				Run(context.Background(), graph.RequestHead{})
			return err
		},
		"http_request": func(t *testing.T) error {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			}))
			t.Cleanup(upstream.Close)
			_, err := mustCatalogNode(t, "http_request", "url: "+upstream.URL+"\nerror_on_status: [5xx]\n", RegistryOptions{}).
				Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost}}}})
			return err
		},
		"publish": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "publish", "destination: primary\ntarget: t\n", RegistryOptions{
				Publishers: staticResolver{pub: &fakePublisher{err: errors.New("broker down")}},
			}).Run(context.Background(), graph.Envelope{})
			return err
		},
		"respond": func(t *testing.T) error {
			_, err := mustCatalogNode(t, "respond", "passthrough: true\n", RegistryOptions{}).
				Run(context.Background(), graph.Envelope{})
			return err
		},
	}
}

func mustCatalogNode(t *testing.T, typ, body string, opts RegistryOptions) graph.Node {
	t.Helper()
	reg, err := NewRegistryWithOptions(protectionTestOptions(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := reg.Lookup(typ)
	if !ok {
		t.Fatalf("unknown type %s", typ)
	}
	node, err := spec.New(nodeConfigFromYAML(t, typ, body))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestOnErrorRecoversEveryFailingNode(t *testing.T) {
	schemas := NodeSchemas()
	cases := onErrorRecoveryCases(t)
	for name := range nodeFixtures {
		if !schemas[name].OnError {
			continue
		}
		if _, ok := cases[name]; !ok {
			t.Errorf("no on_error recovery case for %s", name)
		}
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			fn(t)
		})
	}
}

func onErrorRecoveryCases(t *testing.T) map[string]func(*testing.T) {
	t.Helper()
	return map[string]func(*testing.T){
		"batch": func(t *testing.T) {
			src := `server: {addr: ":8080", max_body_bytes: 1024}
routing:
  profiles: {sandbox: {account_id: "000000000000", credential_mode: sandbox}}
  environments: {preview: {profiles: [sandbox], accounts: ["000000000000"], destination_prefix: preview}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /demo}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - id: fail
        type: batch
        mode: dedicated
        profile: sandbox
        environment_path: environment
        receiving_environment: preview
        event_id_path: event_id
        child:
          entry: fail.send
          nodes: [{id: fail.send, type: publish, destination: primary, target: sample}]
          edges: []
      - {id: fb, type: respond, status: 200, body: recovered}
    edges: [in>body, body>auth, auth>env, env>fail, fail:on_error>fb]
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
`
			assertRecovered(t, mustCompileDemo(t, src, RegistryOptions{ValidateOnly: true}), []byte(`{}`), "10.0.0.1", "")
		},
		"register_route": func(t *testing.T) {
			src := envelopeFailYAML(`{id: fail, type: register_route, mode: direct, store: primary, profile: sandbox, environment_variable: route, id_source: body, id_path: id, id_type: record_a}`, respondOKNode, "env>fail, fail>ok, fail:on_error>fb")
			src = "routing:\n  stores: {primary: {table: sample}}\n  profiles: {sandbox: {account_id: \"000000000000\", credential_mode: sandbox}}\n  environments: {}\n" + src
			assertRecovered(t, mustCompileDemo(t, src, RegistryOptions{ValidateOnly: true}), []byte(`{}`), "10.0.0.1", "")
		},
		"route": func(t *testing.T) {
			src := envelopeFailYAML(`{id: fail, type: route, profile: sandbox, candidates: [{source: body, path: environment}]}`, respondOKNode, "env>fail, fail>ok, fail:on_error>fb")
			src = "routing:\n  profiles: {sandbox: {account_id: \"000000000000\", credential_mode: sandbox}}\n  environments: {preview: {profiles: [sandbox], accounts: [\"000000000000\"], destination_prefix: preview}}\n" + src
			assertRecovered(t, mustCompileDemo(t, src, RegistryOptions{RoutingRegistry: routeTestRegistry(t)}), []byte(`{"environment":"unknown"}`), "10.0.0.1", "")
		},
		"loop_guard": func(t *testing.T) {
			flow := mustCompileDemo(t, requestHeadFailYAML(`{id: fail, type: loop_guard, max_hops: 1}`), RegistryOptions{})
			assertRecovered(t, flow, []byte(`{}`), "10.0.0.1", "1")
		},
		"ip_allowlist": func(t *testing.T) {
			flow := mustCompileDemo(t, requestHeadFailYAML(`{id: fail, type: ip_allowlist, cidrs: ["192.0.2.0/24"]}`), RegistryOptions{})
			assertRecovered(t, flow, []byte(`{}`), "10.0.0.1", "")
		},
		"rate_limit": func(t *testing.T) {
			flow := mustCompileDemo(t, requestHeadFailYAML(`{id: fail, type: rate_limit, store: test, rpm: 60, burst: 1, key_by: source_ip}`), RegistryOptions{})
			term, err := runDemo(t, flow, []byte(`{}`), "10.0.0.1", "")
			if err != nil {
				t.Fatalf("first request should pass: %v", err)
			}
			if term.Response == nil || string(term.Response.Body) != "ok" {
				t.Fatalf("first response = %+v, want ok", term.Response)
			}
			assertRecovered(t, flow, []byte(`{}`), "10.0.0.1", "")
		},
		"auth": func(t *testing.T) {
			src := `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: fail, type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env, type: envelope, body_format: json}
      - {id: ok, type: respond, status: 200, body: ok}
      - {id: na, type: no_auth}
      - {id: env2, type: envelope, body_format: json}
      - {id: fb, type: respond, status: 200, body: recovered}
    edges: [in>body, body>fail, fail>env, env>ok, fail:on_error>na, na>env2, env2>fb]
`
			assertRecovered(t, mustCompileDemo(t, src, RegistryOptions{}), []byte(`{}`), "10.0.0.1", "")
		},
		"replay_guard": func(t *testing.T) {
			src := `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: fail, type: replay_guard, store: test, ttl: 1m, max_entries: 100}
      - {id: env, type: envelope, body_format: json}
      - {id: ok, type: respond, status: 200, body: ok}
      - {id: env2, type: envelope, body_format: json}
      - {id: fb, type: respond, status: 200, body: recovered}
    edges: [in>body, body>auth, auth>fail, fail>env, env>ok, fail:on_error>env2, env2>fb]
`
			flow := mustCompileDemo(t, src, RegistryOptions{})
			term, err := runDemo(t, flow, []byte(`{"id":1}`), "10.0.0.1", "")
			if err != nil {
				t.Fatalf("first request should pass: %v", err)
			}
			if term.Response == nil || string(term.Response.Body) != "ok" {
				t.Fatalf("first response = %+v, want ok", term.Response)
			}
			assertRecovered(t, flow, []byte(`{"id":1}`), "10.0.0.1", "")
		},
		"envelope": func(t *testing.T) {
			src := `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: fail, type: envelope, body_format: xml}
      - {id: ok, type: respond, status: 200, body: ok}
      - {id: env2, type: envelope, body_format: json}
      - {id: fb, type: respond, status: 200, body: recovered}
    edges: [in>body, body>auth, auth>fail, fail>ok, fail:on_error>env2, env2>fb]
`
			assertRecovered(t, mustCompileDemo(t, src, RegistryOptions{}), []byte("<a>"), "10.0.0.1", "")
		},
		"filter": func(t *testing.T) {
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: filter, on_no_match: reject, conditions: [{source: body, path: event, operator: eq, value: created}]}`,
				respondOKNode,
				"env>fail, fail>ok, fail:on_error>fb",
			), RegistryOptions{}), []byte(`{"event":"deleted"}`), "10.0.0.1", "")
		},
		"lookup": func(t *testing.T) {
			opts := RegistryOptions{Lookups: func(LookupOptions) LookupFunc {
				return func(context.Context, string) (string, bool, error) { return "", false, nil }
			}}
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: lookup, provider: dynamodb, key: '{body.id}', name: route}`,
				respondOKNode,
				"env>fail, fail>ok, fail:on_error>fb",
			), opts), []byte(`{"id":"missing"}`), "10.0.0.1", "")
		},
		"json_validate": func(t *testing.T) {
			src := envelopeFailYAML(`{id: fail, type: json_validate, schema: {type: object, required: [id]}}`, respondOKNode, "env>fail, fail>ok, fail:on_error>fb")
			assertRecovered(t, mustCompileDemo(t, src, RegistryOptions{}), []byte(`{}`), "10.0.0.1", "")
		},
		"transform": func(t *testing.T) {
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: transform, kind: xml_prune}`,
				respondOKNode,
				"env>fail, fail>ok, fail:on_error>fb",
			), RegistryOptions{}), []byte(`{"a":1}`), "10.0.0.1", "")
		},
		"code": func(t *testing.T) {
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: code, script: "return 1", timeout: 1s}`,
				respondOKNode,
				"env>fail, fail>ok, fail:on_error>fb",
			), RegistryOptions{}), []byte(`{}`), "10.0.0.1", "")
		},
		"switch": func(t *testing.T) {
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: switch, cases: [{port: matched, when: {source: body, path: event, operator: eq, value: created}}]}`,
				respondOKNode,
				"env>fail, fail:matched>ok, fail:on_error>fb",
			), RegistryOptions{}), []byte(`{"event":"other"}`), "10.0.0.1", "")
		},
		"http_request": func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			}))
			t.Cleanup(upstream.Close)
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				fmt.Sprintf(`{id: fail, type: http_request, url: %q, error_on_status: [5xx]}`, upstream.URL),
				respondOKNode,
				"env>fail, fail>ok, fail:on_error>fb",
			), RegistryOptions{}), []byte(`{}`), "10.0.0.1", "")
		},
		"publish": func(t *testing.T) {
			opts := RegistryOptions{Publishers: staticResolver{pub: &fakePublisher{err: errors.New("broker down")}}}
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: publish, destination: primary, target: t}`,
				"",
				"env>fail, fail:on_error>fb",
			), opts), []byte(`{}`), "10.0.0.1", "")
		},
		"respond": func(t *testing.T) {
			assertRecovered(t, mustCompileDemo(t, envelopeFailYAML(
				`{id: fail, type: respond, passthrough: true}`,
				"",
				"env>fail, fail:on_error>fb",
			), RegistryOptions{}), []byte(`{}`), "10.0.0.1", "")
		},
	}
}

func requestHeadFailYAML(failNode string) string {
	return fmt.Sprintf(`
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - %s
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: ok, type: respond, status: 200, body: ok}
      - {id: body2, type: read_body, max_bytes: 1048576}
      - {id: na2, type: no_auth}
      - {id: env2, type: envelope, body_format: json}
      - {id: fb, type: respond, status: 200, body: recovered}
    edges: [in>fail, fail>body, body>auth, auth>env, env>ok, fail:on_error>body2, body2>na2, na2>env2, env2>fb]
`, failNode)
}

const respondOKNode = "      - {id: ok, type: respond, status: 200, body: ok}"

func envelopeFailYAML(failNode, extraNodes, edges string) string {
	if extraNodes != "" {
		extraNodes = extraNodes + "\n"
	}
	return fmt.Sprintf(`
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - %s
%s      - {id: fb, type: respond, status: 200, body: recovered}
    edges: [in>body, body>auth, auth>env, %s]
`, failNode, extraNodes, edges)
}

func mustCompileDemo(t *testing.T, src string, opts RegistryOptions) *graph.Flow {
	t.Helper()
	cfg, issues := LoadBytes([]byte(src), "test", false)
	for _, iss := range issues {
		if iss.EffectiveSeverity() == config.SeverityError {
			t.Fatalf("LoadBytes issues: %v", issues)
		}
	}
	fc, ok := cfg.Flows["demo"]
	if !ok {
		t.Fatal("missing demo flow")
	}
	reg, err := NewRegistryWithOptions(protectionTestOptions(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	flow, err := graph.Compile("demo", fc, reg)
	if err != nil {
		t.Fatal(err)
	}
	return flow
}

func runDemo(t *testing.T, flow *graph.Flow, body []byte, srcIP, hops string) (graph.Terminal, error) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/w", bytes.NewReader(body))
	if hops != "" {
		req.Header.Set(config.LoopHopsHeader, hops)
	}
	ctx := WithBodyReader(req.Context(), io.NopCloser(bytes.NewReader(body)))
	return flow.Run(ctx, HeadFromRequest(req, nil, srcIP), graph.RunOptions{PublishTimeout: time.Second})
}

func assertRecovered(t *testing.T, flow *graph.Flow, body []byte, srcIP, hops string) {
	t.Helper()
	term, err := runDemo(t, flow, body, srcIP, hops)
	if err != nil {
		t.Fatalf("on_error should recover: %v", err)
	}
	if term.Response == nil || string(term.Response.Body) != "recovered" {
		t.Fatalf("response = %+v, want body recovered", term.Response)
	}
}
