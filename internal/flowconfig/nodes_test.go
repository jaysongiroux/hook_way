package flowconfig

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
	"gopkg.in/yaml.v3"
)

var wantedCatalog = map[string]struct {
	in, out  graph.PortType
	detached bool
}{
	"http_trigger": {graph.PortNone, graph.PortRequestHead, false},
	"loop_guard":   {graph.PortRequestHead, graph.PortRequestHead, false},
	"ip_allowlist": {graph.PortRequestHead, graph.PortRequestHead, false},
	"rate_limit":   {graph.PortRequestHead, graph.PortRequestHead, false},
	"read_body":    {graph.PortRequestHead, graph.PortRawRequest, false},
	"auth":         {graph.PortRawRequest, graph.PortVerified, false},
	"no_auth":      {graph.PortRawRequest, graph.PortVerified, false},
	"replay_guard": {graph.PortVerified, graph.PortVerified, false},
	"envelope":     {graph.PortVerified, graph.PortEnvelope, false},
	"filter":       {graph.PortEnvelope, graph.PortEnvelope, false},
	"lookup":       {graph.PortEnvelope, graph.PortEnvelope, false},
	"transform":    {graph.PortEnvelope, graph.PortEnvelope, false},
	"code":         {graph.PortEnvelope, graph.PortEnvelope, false},
	"switch":       {graph.PortEnvelope, graph.PortEnvelope, false},
	"http_request": {graph.PortEnvelope, graph.PortEnvelope, false},
	"publish":      {graph.PortEnvelope, graph.PortTerminal, true},
	"respond":      {graph.PortEnvelope, graph.PortTerminal, false},
}

var validNodeYAML = map[string]string{
	"http_trigger": "path: /webhooks/{id}\nmethods: [POST]\n",
	"loop_guard":   "max_hops: 3\n",
	"ip_allowlist": "cidrs: [10.0.0.0/8]\n",
	"rate_limit":   "rpm: 60\nburst: 10\nkey_by: source_ip\n",
	"read_body":    "max_bytes: 1024\n",
	"auth":         "scheme: hmac_sha256_hex\nsecret_ref: {provider: env, name: TEST_SECRET, refresh: 15m, overlap: 5m}\nsource: {kind: header, key: X-Signature}\ntimestamp_tolerance: 5m\n",
	"no_auth":      "",
	"replay_guard": "ttl: 5m\nmax_entries: 10000\nkey: '{header.X-Idempotency-Key}'\n",
	"envelope":     "body_format: json\n",
	"filter":       "conditions: [{source: body, path: event, operator: eq, value: created}]\non_no_match: drop\n",
	"lookup":       "provider: dynamodb\nkey: '{body.id}'\nname: route\ntable: routing\ncache: {ttl: 5m, max_entries: 10000}\nfallback: shared\nnegative_ttl: 30s\n",
	"transform":    "kind: changed_fields\nfrom: body.previous\nto: body.current\nname: changed_fields\n",
	"code":         "script: 'return input.body'\ntimeout: 1s\nmax_output_bytes: 1048576\nmax_call_stack: 1000\n",
	"switch":       "cases: [{port: matched, when: {source: body, path: event, operator: eq, value: created}}]\ndefault: unmatched\n",
	"http_request": "url: https://example.test/{rest}\nforward_method: true\nforward_query: true\nforward_headers: [X-Request-ID]\ntimeout: 5s\nheaders: {X-Service: hookway}\nerror_on_status: [5xx]\n",
	"publish":      "destination: primary\ntarget: '{route}-queue'\ncreate: true\nmessage_attributes: {event: '{body.event}'}\n",
	"respond":      "passthrough: true\n",
}

func TestRegistryCatalogExact(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Types(); len(got) != len(wantedCatalog) {
		t.Fatalf("registered %d types, want %d: %v", len(got), len(wantedCatalog), got)
	}
	for name, want := range wantedCatalog {
		s, ok := r.Lookup(name)
		if !ok || s.In != want.in || s.Out != want.out || s.Detached != want.detached {
			t.Errorf("%s = %+v, found=%v, want %s -> %s detached=%v", name, s, ok, want.in, want.out, want.detached)
		}
	}
}

func TestPublishIsOnlyDetachedType(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range r.Types() {
		s, _ := r.Lookup(name)
		if s.Detached != (name == "publish") {
			t.Errorf("%s detached=%v", name, s.Detached)
		}
	}
}

func TestEveryOptionContractRoundTripsAndRejectsUnknownFields(t *testing.T) {
	if len(nodeDefinitions) != len(wantedCatalog) {
		t.Fatalf("definitions=%d, want %d", len(nodeDefinitions), len(wantedCatalog))
	}
	for _, def := range nodeDefinitions {
		t.Run(def.name, func(t *testing.T) {
			nc := nodeConfigFromYAML(t, def.name, validNodeYAML[def.name])
			node, err := def.spec().New(nc)
			if err != nil {
				t.Fatalf("valid options rejected: %v", err)
			}
			if _, err := node.Run(context.Background(), zeroPayload(def.in)); !errors.Is(err, errNotImplemented) || !strings.Contains(err.Error(), def.name) {
				t.Fatalf("stub error = %v", err)
			}

			first := def.newOptions()
			if err := decodeOptionsInto(nc, first); err != nil {
				t.Fatal(err)
			}
			encoded, err := yaml.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			second := def.newOptions()
			if err := yaml.Unmarshal(encoded, second); err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("round trip: %v; first=%#v second=%#v", err, first, second)
			}

			bad := nodeConfigFromYAML(t, def.name, validNodeYAML[def.name]+"unexpected_option: true\n")
			if _, err := def.spec().New(bad); err == nil {
				t.Fatal("unknown option accepted")
			}
		})
	}
}

func TestSettledRequiredAndExclusiveOptions(t *testing.T) {
	cases := []struct{ name, body string }{
		{"http_trigger", ""}, {"ip_allowlist", ""}, {"rate_limit", ""}, {"read_body", ""},
		{"auth", "scheme: hmac_sha256_hex\n"},
		{"auth", "scheme: hmac_sha256_hex\nsecret: one\nsecret_ref: {provider: env, name: TWO}\n"},
		{"filter", ""}, {"lookup", "provider: dynamodb\n"}, {"transform", "kind: changed_fields\n"},
		{"code", ""}, {"switch", ""}, {"http_request", ""}, {"publish", "destination: primary\n"},
		{"respond", ""}, {"respond", "status: 200\npassthrough: true\n"},
	}
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		t.Run(tc.name+string(rune('a'+i)), func(t *testing.T) {
			s, _ := r.Lookup(tc.name)
			if _, err := s.New(nodeConfigFromYAML(t, tc.name, tc.body)); err == nil {
				t.Fatal("invalid options accepted")
			}
		})
	}
}

func nodeConfigFromYAML(t *testing.T, nodeType, body string) graph.NodeConfig {
	t.Helper()
	var nc graph.NodeConfig
	src := "id: node\ntype: " + nodeType + "\n" + body
	if err := yaml.Unmarshal([]byte(src), &nc); err != nil {
		t.Fatal(err)
	}
	return nc
}

func zeroPayload(port graph.PortType) graph.Payload {
	switch port {
	case graph.PortRequestHead:
		return graph.RequestHead{}
	case graph.PortRawRequest:
		return graph.RawRequest{}
	case graph.PortVerified:
		return graph.Verified{}
	case graph.PortEnvelope:
		return graph.Envelope{}
	default:
		return graph.RequestHead{}
	}
}
