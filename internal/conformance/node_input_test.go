package conformance

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

// TestI19_NodesDoNotMutateTheirInput runs every node type on a representative
// input and compares that input with a freshly built copy afterwards. A node
// may return a changed payload, but it must never write into the maps,
// slices, or documents of the payload it was given: failure capture keeps the
// failed node's input without copying it (docs/invariants.md I19).
func TestI19_NodesDoNotMutateTheirInput(t *testing.T) {
	cases := nodeInputCases(t)
	reg, err := flowconfig.NewRegistryWithOptions(flowconfig.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range reg.Types() {
		if typ == "http_trigger" {
			continue // the flow root: it has no input payload
		}
		covered := false
		for name := range cases {
			if name == typ || strings.HasPrefix(name, typ+"/") {
				covered = true
			}
		}
		if !covered {
			t.Errorf("no input-immutability case for node type %q", typ)
		}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			node := buildCatalogNode(t, c.typ, c.options, c.opts)
			ctx := context.Background()
			if c.ctx != nil {
				ctx = c.ctx()
			}
			in := c.input()
			if _, err := node.Run(ctx, in); err != nil {
				t.Fatalf("Run() = %v; the case must exercise the success path", err)
			}
			if want := c.input(); !reflect.DeepEqual(in, want) {
				t.Fatalf("%s mutated its input:\n got  %#v\n want %#v", name, in, want)
			}
		})
	}
}

type nodeInputCase struct {
	typ     string
	options string
	opts    flowconfig.RegistryOptions
	ctx     func() context.Context
	input   func() graph.Payload
}

func nodeInputCases(t *testing.T) map[string]nodeInputCase {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)
	body := []byte(`{"event":"created","id":"42"}`)
	lookups := func(flowconfig.LookupOptions) flowconfig.LookupFunc {
		return func(context.Context, string) (string, bool, error) { return "r1", true, nil }
	}
	registry, err := routing.NewRegistry(routing.Config{Profiles: map[string]routing.Profile{"sample": {AccountID: "000000000000", CredentialMode: "sample"}}, Environments: map[string]routing.Environment{"sample": {Profiles: []string{"sample"}, Accounts: []string{"000000000000"}, DestinationPrefix: "sample"}}})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]nodeInputCase{
		"batch": {typ: "batch", options: "mode: shared\nprofile: sample\nenvironment_path: environment\nevent_id_path: event_id\nchild:\n  entry: node.send\n  nodes: [{id: node.send, type: publish, destination: primary, target: sample}]\n  edges: []\n", opts: flowconfig.RegistryOptions{RoutingRegistry: registry}, input: func() graph.Payload { e := inputEnvelope(); e.Body = []any{}; return e }},
		"register_route": {typ: "register_route", options: "mode: after_create\nstore: primary\nprofile: sample\nenvironment_variable: route\nid_source: upstream\nid_path: created_id\nid_type: record_a\n", opts: flowconfig.RegistryOptions{ValidateOnly: true, RoutingRegistry: registry}, input: func() graph.Payload {
			e := inputEnvelope()
			e.Upstream = &graph.UpstreamResponse{Status: 201, Header: http.Header{}, Body: []byte(`{"id":"42"}`), Values: map[string]string{"created_id": "42"}}
			return e
		}},
		"route":        {typ: "route", options: "profile: sample\nfixed: sample\n", opts: flowconfig.RegistryOptions{RoutingRegistry: registry}, input: func() graph.Payload { return inputEnvelope() }},
		"loop_guard":   {typ: "loop_guard", options: "max_hops: 3\n", input: func() graph.Payload { return inputHead() }},
		"ip_allowlist": {typ: "ip_allowlist", options: "cidrs: [10.0.0.0/8]\n", input: func() graph.Payload { return inputHead() }},
		"rate_limit":   {typ: "rate_limit", options: "rpm: 600\nburst: 10\nkey_by: source_ip\n", input: func() graph.Payload { return inputHead() }},
		"read_body": {
			typ: "read_body", options: "max_bytes: 1024\n",
			ctx: func() context.Context {
				return flowconfig.WithBodyReader(context.Background(), strings.NewReader(string(body)))
			},
			input: func() graph.Payload { return inputHead() },
		},
		"auth": {
			typ:     "auth",
			options: "scheme: hmac_sha256_hex\nsecret: " + testSecret + "\nsignature_header: X-Hub-Signature-256\n",
			input: func() graph.Payload {
				head := inputHead()
				head.Header.Set("X-Hub-Signature-256", githubHMAC(body))
				return graph.RawRequest{Head: head, Body: append([]byte(nil), body...)}
			},
		},
		"no_auth": {typ: "no_auth", input: func() graph.Payload {
			return graph.RawRequest{Head: inputHead(), Body: append([]byte(nil), body...)}
		}},
		"replay_guard": {typ: "replay_guard", options: "ttl: 1m\nmax_entries: 100\n", input: func() graph.Payload {
			return graph.Verified{Raw: graph.RawRequest{Head: inputHead(), Body: append([]byte(nil), body...)}, Identity: "hmac"}
		}},
		"envelope": {typ: "envelope", options: "body_format: json\n", input: func() graph.Payload {
			return graph.Verified{Raw: graph.RawRequest{Head: inputHead(), Body: append([]byte(nil), body...)}, Identity: "hmac"}
		}},
		"filter": {
			typ:     "filter",
			options: "conditions: [{source: body, path: event, operator: eq, value: created}]\non_no_match: drop\n",
			input:   func() graph.Payload { return inputEnvelope() },
		},
		"lookup": {
			typ: "lookup", options: "provider: dynamodb\nkey: '{body.id}'\nname: route\ntable: routing\n",
			opts:  flowconfig.RegistryOptions{Lookups: lookups},
			input: func() graph.Payload { return inputEnvelope() },
		},
		"transform/changed_fields": {
			typ: "transform", options: "kind: changed_fields\nfrom: body.previous\nto: body.current\nname: changed_fields\n",
			input: func() graph.Payload { return inputEnvelope() },
		},
		"transform/field_map": {
			typ:     "transform",
			options: "kind: field_map\nmappings: [{from: body.event, to: vars.event}, {from: body.event, to: body.copy}]\n",
			input:   func() graph.Payload { return inputEnvelope() },
		},
		"transform/xml_prune": {
			typ: "transform", options: "kind: xml_prune\n",
			input: func() graph.Payload {
				doc, err := transform.ParseXMLWithLimits([]byte("<order><id> 42 </id><note></note></order>"), transform.XMLLimits{})
				if err != nil {
					t.Fatal(err)
				}
				env := inputEnvelope()
				env.Body, env.BodyFormat = doc, "xml"
				return env
			},
		},
		"code": {
			typ: "code",
			// The script writes into input on purpose: the code node must hand
			// the script copies, never the envelope's own maps.
			options: "script: 'input.body.event = \"mutated\"; input.vars.tenant = \"mutated\"; " +
				"input.headers[\"X-Request-Id\"] = \"mutated\"; return {body: {seen: input.body.event}, vars: {k: \"v2\"}}'\ntimeout: 1s\n",
			input: func() graph.Payload { return inputEnvelope() },
		},
		"switch": {
			typ:     "switch",
			options: "cases: [{port: matched, when: {source: body, path: event, operator: eq, value: created}}]\ndefault: unmatched\n",
			input:   func() graph.Payload { return inputEnvelope() },
		},
		"if_else": {
			typ:     "if_else",
			options: "conditions: [{source: body, path: event, operator: eq, value: created}]\n",
			input:   func() graph.Payload { return inputEnvelope() },
		},
		"http_request": {
			typ:     "http_request",
			options: "url: " + upstream.URL + "\nforward_headers: [X-Request-Id]\n",
			input:   func() graph.Payload { return inputEnvelope() },
		},
		"publish": {
			typ: "publish", options: "destination: primary\ntarget: '{route}-queue'\n",
			opts:  flowconfig.RegistryOptions{Publishers: inputCaseResolver{}},
			input: func() graph.Payload { return inputEnvelope() },
		},
		"respond": {
			typ: "respond", options: "passthrough: true\n",
			input: func() graph.Payload {
				env := inputEnvelope()
				env.Upstream = &graph.UpstreamResponse{
					Status: http.StatusOK,
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   []byte(`{"ok":true}`),
				}
				return env
			},
		},
	}
}

func inputHead() graph.RequestHead {
	return graph.RequestHead{
		Method:   http.MethodPost,
		Path:     "/webhooks/github",
		PathVars: map[string]string{"id": "1"},
		Query:    url.Values{"q": {"1"}},
		Header: http.Header{
			"Content-Type": {"application/json"},
			"X-Request-Id": {"req-1"},
		},
		SourceIP:  "10.1.2.3",
		RequestID: "req-1",
		TraceID:   "trace-1",
		EventID:   "evt-1",
	}
}

func inputEnvelope() graph.Envelope {
	return graph.Envelope{
		V: graph.Verified{
			Raw:      graph.RawRequest{Head: inputHead(), Body: []byte(`{"event":"created","id":"42"}`)},
			Identity: "hmac",
		},
		Body: map[string]any{
			"event":    "created",
			"id":       "42",
			"previous": map[string]any{"a": 1.0},
			"current":  map[string]any{"a": 2.0},
		},
		BodyFormat: "json",
		Vars:       map[string]string{"tenant": "acme", "route": "orders"},
	}
}

func buildCatalogNode(t *testing.T, typ, options string, opts flowconfig.RegistryOptions) graph.Node {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	reg, err := flowconfig.NewRegistryWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := reg.Lookup(typ)
	if !ok {
		t.Fatalf("unknown node type %s", typ)
	}
	var nc graph.NodeConfig
	if err := yaml.Unmarshal([]byte("id: node\ntype: "+typ+"\n"+options), &nc); err != nil {
		t.Fatal(err)
	}
	node, err := spec.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

type inputCaseResolver struct{}

func (inputCaseResolver) Resolve(string) (flowconfig.Publisher, error) {
	return inputCasePublisher{}, nil
}

type inputCasePublisher struct{}

func (inputCasePublisher) Publish(context.Context, string, string, graph.Envelope) error { return nil }
