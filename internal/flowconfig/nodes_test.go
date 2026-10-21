package flowconfig

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

// Keep expected ports independent of the production catalog. Each node also owns
// its valid options fixture, shared by the contract, schema, and runtime tests.
const mcpNodeFixture = `path: /mcp
public_url: http://localhost/mcp
auth: {no_auth: true}
tools:
  - name: test_tool
    description: Test native tool.
    read_only: true
    access: {anonymous: true}
    input_schema: {type: object}
    output_schema: {type: object}
`

var nodeFixtures = map[string]struct {
	in, out  graph.PortType
	detached bool
	yaml     string
}{
	"mcp_trigger":    {graph.PortNone, graph.PortEnvelope, false, mcpNodeFixture},
	"mcp_result":     {graph.PortEnvelope, graph.PortTerminal, false, "source: body\n"},
	"http_trigger":   {graph.PortNone, graph.PortRequestHead, false, "path: /webhooks/{id}\nmethods: [POST]\n"},
	"loop_guard":     {graph.PortRequestHead, graph.PortRequestHead, false, "max_hops: 3\n"},
	"ip_allowlist":   {graph.PortRequestHead, graph.PortRequestHead, false, "cidrs: [10.0.0.0/8]\n"},
	"rate_limit":     {graph.PortRequestHead, graph.PortRequestHead, false, "store: test\nrpm: 60\nburst: 10\nkey_by: source_ip\n"},
	"read_body":      {graph.PortRequestHead, graph.PortRawRequest, false, "max_bytes: 1024\n"},
	"auth":           {graph.PortRawRequest, graph.PortVerified, false, "scheme: hmac_sha256_hex\nsecret_ref: {provider: env, name: TEST_SECRET, refresh: 15m, overlap: 5m}\nsource: {kind: header, key: X-Signature}\ntimestamp_tolerance: 5m\n"},
	"no_auth":        {graph.PortRawRequest, graph.PortVerified, false, ""},
	"replay_guard":   {graph.PortVerified, graph.PortVerified, false, "store: test\nttl: 5m\nmax_entries: 10000\nkey: '{id}'\n"},
	"envelope":       {graph.PortVerified, graph.PortEnvelope, false, "body_format: json\n"},
	"filter":         {graph.PortEnvelope, graph.PortEnvelope, false, "conditions: [{source: body, path: event, operator: eq, value: created}]\non_no_match: drop\n"},
	"inspect":        {graph.PortEnvelope, graph.PortEnvelope, false, ""},
	"sample":         {graph.PortEnvelope, graph.PortEnvelope, false, "rate: 0.2\n"},
	"register_route": {graph.PortEnvelope, graph.PortEnvelope, false, "mode: after_create\nstore: primary\nprofile: sandbox\nenvironment_variable: route\nid_source: upstream\nid_path: created_id\nid_type: record_a\n"},
	"route":          {graph.PortEnvelope, graph.PortEnvelope, false, "profile: sandbox\nfixed: demo\n"},
	"launchdarkly":   {graph.PortEnvelope, graph.PortEnvelope, false, "connection: production\nname: rollout\ncontext: {key: {value: tenant}}\nflags: {new_processor: {key: use-new-processor, type: boolean, fallback: false}}\n"},
	"lookup":         {graph.PortEnvelope, graph.PortEnvelope, false, "provider: dynamodb\nkey: '{body.id}'\nname: route\ntable: routing\ncache: {ttl: 5m, max_entries: 10000}\nfallback: shared\nnegative_ttl: 30s\n"},
	"batch":          {graph.PortEnvelope, graph.PortTerminal, false, "mode: shared\nprofile: sandbox\nenvironment_path: environment\nevent_id_path: event_id\nchild:\n  entry: node.send\n  nodes: [{id: node.send, type: publish, destination: primary, target: sample}]\n  edges: []\n"},
	"event":          {graph.PortEnvelope, graph.PortEnvelope, false, "event: issue.opened\nevent_id_path: issue_id\n"},
	"json_validate":  {graph.PortEnvelope, graph.PortEnvelope, false, "schema: {type: object}\n"},
	"transform":      {graph.PortEnvelope, graph.PortEnvelope, false, "kind: changed_fields\nfrom: body.previous\nto: body.current\nname: changed_fields\n"},
	"code":           {graph.PortEnvelope, graph.PortEnvelope, false, "script: 'return {body: input.body}'\ntimeout: 1s\nmax_input_bytes: 1048576\nmax_output_bytes: 1048576\nmax_call_stack: 1000\n"},
	"switch":         {graph.PortEnvelope, graph.PortEnvelope, false, "cases: [{port: matched, match: any, conditions: [{source: body, path: event, operator: eq, value: created}]}]\ndefault: unmatched\n"},
	"if_else":        {graph.PortEnvelope, graph.PortEnvelope, false, "match: all\nconditions: [{source: body, path: event, operator: eq, value: created}]\n"},
	"http_request":   {graph.PortEnvelope, graph.PortEnvelope, false, "url: https://example.test/{rest}\nforward_method: true\nforward_query: true\nforward_headers: [X-Request-ID]\ntimeout: 5s\nheaders: {X-Service: hookway}\nerror_on_status: [5xx]\n"},
	"publish":        {graph.PortEnvelope, graph.PortEnvelope, true, "destination: primary\ntarget: '{route}-queue'\ncreate: true\nmessage_attributes: {event: '{body.event}'}\n"},
	"respond":        {graph.PortEnvelope, graph.PortTerminal, false, "passthrough: true\n"},
}

func TestRegistryCatalogExact(t *testing.T) {
	r, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Types(); len(got) != len(nodeFixtures) {
		t.Fatalf("registered %d types, want %d: %v", len(got), len(nodeFixtures), got)
	}
	for name, want := range nodeFixtures {
		s, ok := r.Lookup(name)
		if !ok || s.In != want.in || s.Out != want.out || s.Detached != want.detached {
			t.Errorf("%s = %+v, found=%v, want %s -> %s detached=%v", name, s, ok, want.in, want.out, want.detached)
		}
	}
}

func TestPublishIsOnlyDetachedType(t *testing.T) {
	r, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range r.Types() {
		s, _ := r.Lookup(name)
		if s.Detached != (name == "publish") {
			t.Errorf("%s detached=%v", name, s.Detached)
		}
		if s.SuccessContinuation != (name == "publish") {
			t.Errorf("%s SuccessContinuation=%v", name, s.SuccessContinuation)
		}
	}
}

func TestEveryOptionContractRoundTripsAndRejectsUnknownFields(t *testing.T) {
	defs := catalog(RegistryOptions{ValidateOnly: true})
	if len(defs) != len(nodeFixtures) {
		t.Fatalf("definitions=%d, want %d", len(defs), len(nodeFixtures))
	}
	for _, def := range defs {
		t.Run(def.name, func(t *testing.T) {
			fixture, ok := nodeFixtures[def.name]
			if !ok {
				t.Fatal("missing node fixture")
			}
			nc := nodeConfigFromYAML(t, def.name, fixture.yaml)
			if _, err := def.spec().New(nc); err != nil {
				t.Fatalf("valid options rejected: %v", err)
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

			bad := nodeConfigFromYAML(t, def.name, fixture.yaml+"unexpected_option: true\n")
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
		{"json_validate", ""}, {"json_validate", "schema: {type: invalid}\n"},
		{"filter", ""}, {"lookup", "provider: dynamodb\n"}, {"transform", "kind: changed_fields\n"},
		{"code", ""}, {"switch", ""}, {"http_request", ""}, {"publish", "destination: primary\n"},
		{"respond", ""}, {"respond", "status: 200\npassthrough: true\n"},
	}
	r, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{}))
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

// TestEveryNodeTypeAcceptsDescription proves description is node identity and
// not a per-type option: every registered type must build with one, even
// though each option struct decodes with KnownFields(true). The companion
// assertion is that the description is absent from Options -- if it leaked,
// New would have failed the first half of this test.
func TestEveryNodeTypeAcceptsDescription(t *testing.T) {
	defs := catalog(RegistryOptions{ValidateOnly: true})
	if len(defs) != len(nodeFixtures) {
		t.Fatalf("definitions=%d, want %d", len(defs), len(nodeFixtures))
	}
	const text = "Explains what this node does."
	for _, def := range defs {
		t.Run(def.name, func(t *testing.T) {
			fixture, ok := nodeFixtures[def.name]
			if !ok {
				t.Fatal("missing node fixture")
			}
			nc := nodeConfigFromYAML(t, def.name, "description: "+text+"\n"+fixture.yaml)
			if nc.Description != text {
				t.Fatalf("Description = %q, want %q", nc.Description, text)
			}
			if _, err := def.spec().New(nc); err != nil {
				t.Fatalf("described node rejected: %v", err)
			}
		})
	}
}

func TestHTTPRequestRetryValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "absent retry block is valid",
			yaml: "url: https://example.test/hook\n",
		},
		{
			name: "full retry block is valid",
			yaml: "url: https://example.test/hook\nretry:\n  max_attempts: 4\n  initial_backoff: 500ms\n  max_backoff: 10s\n",
		},
		{
			name: "explicit single attempt is valid",
			yaml: "url: https://example.test/hook\nretry:\n  max_attempts: 1\n",
		},
		{
			name:    "negative attempts rejected",
			yaml:    "url: https://example.test/hook\nretry:\n  max_attempts: -1\n",
			wantErr: "retry.max_attempts",
		},
		{
			name:    "attempts above the ceiling rejected",
			yaml:    "url: https://example.test/hook\nretry:\n  max_attempts: 50\n",
			wantErr: "retry.max_attempts",
		},
		{
			name:    "backoff without attempts rejected as a silent no-op",
			yaml:    "url: https://example.test/hook\nretry:\n  initial_backoff: 1s\n",
			wantErr: "max_attempts",
		},
		{
			name:    "unparseable backoff rejected",
			yaml:    "url: https://example.test/hook\nretry:\n  max_attempts: 3\n  initial_backoff: soon\n",
			wantErr: "retry.initial_backoff",
		},
		{
			name:    "zero backoff rejected",
			yaml:    "url: https://example.test/hook\nretry:\n  max_attempts: 3\n  initial_backoff: 0s\n",
			wantErr: "retry.initial_backoff",
		},
		{
			name:    "initial greater than max rejected",
			yaml:    "url: https://example.test/hook\nretry:\n  max_attempts: 3\n  initial_backoff: 30s\n  max_backoff: 5s\n",
			wantErr: "must not exceed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{}))
			if err != nil {
				t.Fatal(err)
			}
			spec, _ := reg.Lookup("http_request")
			_, err = spec.New(nodeConfigFromYAML(t, "http_request", tc.yaml))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestEnvelopeMultipartValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "multipart format with no option block is valid",
			yaml: "body_format: multipart\n",
		},
		{
			name: "full option block is valid",
			yaml: "body_format: multipart\nmultipart:\n  max_parts: 4\n  max_part_bytes: 2048\n  include_content: false\n",
		},
		{
			name:    "options without the multipart format are rejected",
			yaml:    "body_format: json\nmultipart:\n  max_parts: 4\n",
			wantErr: "body_format: multipart",
		},
		{
			name:    "negative max_parts rejected",
			yaml:    "body_format: multipart\nmultipart:\n  max_parts: -1\n",
			wantErr: "multipart.max_parts",
		},
		{
			name:    "max_parts above the ceiling rejected",
			yaml:    "body_format: multipart\nmultipart:\n  max_parts: 500\n",
			wantErr: "multipart.max_parts",
		},
		{
			name:    "negative max_part_bytes rejected",
			yaml:    "body_format: multipart\nmultipart:\n  max_part_bytes: -1\n",
			wantErr: "multipart.max_part_bytes",
		},
		{
			name:    "unknown body_format still rejected",
			yaml:    "body_format: nonsense\n",
			wantErr: "unknown body_format",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{}))
			if err != nil {
				t.Fatal(err)
			}
			spec, _ := reg.Lookup("envelope")
			_, err = spec.New(nodeConfigFromYAML(t, "envelope", tc.yaml))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestMultipartOptionDefaults(t *testing.T) {
	// An empty block must produce the documented defaults, and include_content
	// must default to true — a parse that silently dropped file bytes would be
	// a surprising default.
	lim := MultipartOptions{}.limits()
	if lim.MaxParts != 16 {
		t.Fatalf("MaxParts = %d, want 16", lim.MaxParts)
	}
	if lim.MaxPartBytes != 1<<20 {
		t.Fatalf("MaxPartBytes = %d, want %d", lim.MaxPartBytes, 1<<20)
	}
	if !lim.IncludeContent {
		t.Fatal("IncludeContent = false, want true by default")
	}

	// An explicit false must survive, which is why the field is a *bool: a
	// plain bool cannot distinguish "unset" from "explicitly false".
	no := false
	if (MultipartOptions{IncludeContent: &no}).limits().IncludeContent {
		t.Fatal("include_content: false was ignored")
	}
	if got := (MultipartOptions{MaxParts: 3, MaxPartBytes: 99}).limits(); got.MaxParts != 3 || got.MaxPartBytes != 99 {
		t.Fatalf("explicit limits = %+v, want MaxParts 3 and MaxPartBytes 99", got)
	}
}
