package bridge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/cmd/hookway-wasm/bridge"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	replayconfigapi "github.com/jaysongiroux/hook_way/internal/replay/configapi"
)

// Validate must be a byte-identical pass-through of configapi.Validate for any
// input — that is the whole correctness guarantee (browser == CLI).
func TestValidateMatchesConfigapi(t *testing.T) {
	inputs := []string{
		"",                    // empty
		"endpoints: {}",       // structurally sparse
		"not: valid: yaml: [", // malformed YAML
		"server:\n  addr: \":8080\"\ndestinations:\n  primary:\n    type: rabbitmq\n    rabbitmq:\n      url: \"${RABBITMQ_URL}\"\n      exchange: x\n      exchange_type: direct\nendpoints:\n  github:\n    enabled: true\n    path: /webhooks/github\n    destinations:\n      - destination: primary\n        target: q\n    auth:\n      type: hmac_sha256\n      scheme: hmac_sha256_hex\n      secret: \"${S}\"\n      signature_header: X-Sig\n",
	}
	for _, in := range inputs {
		want, err := json.Marshal(configapi.Validate([]byte(in)))
		if err != nil {
			t.Fatalf("marshal reference: %v", err)
		}
		got, err := bridge.Validate(in)
		if err != nil {
			t.Fatalf("bridge.Validate(%q): %v", in, err)
		}
		if got != string(want) {
			t.Errorf("mismatch for %q\n got: %s\nwant: %s", in, got, want)
		}
	}
}

// Malformed YAML must surface at least one issue (sanity: the pass-through is
// wired to a real validator, not a stub returning empty).
func TestValidateMalformedHasIssues(t *testing.T) {
	got, err := bridge.Validate("not: valid: yaml: [")
	if err != nil {
		t.Fatalf("bridge.Validate: %v", err)
	}
	var resp configapi.ValidateResponse
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("bridge output is not valid JSON: %v", err)
	}
	if len(resp.Issues) == 0 {
		t.Fatalf("expected issues for malformed YAML, got none")
	}
}

func TestSchemaMatchesConfig(t *testing.T) {
	want, err := config.SchemaJSON()
	if err != nil {
		t.Fatalf("config.SchemaJSON: %v", err)
	}
	got, err := bridge.Schema()
	if err != nil {
		t.Fatalf("bridge.Schema: %v", err)
	}
	if got != string(want) {
		t.Errorf("schema mismatch")
	}
	if !strings.Contains(got, "destinations") {
		t.Errorf("schema missing expected property 'destinations'")
	}
}

func TestNodeSchemasMatchFlowconfig(t *testing.T) {
	want, err := json.MarshalIndent(flowconfig.NodeSchemas(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got, err := bridge.NodeSchemas()
	if err != nil {
		t.Fatalf("bridge.NodeSchemas: %v", err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
		t.Errorf("node schema mismatch")
	}
	if !strings.Contains(got, "http_trigger") || !strings.Contains(got, "publish") {
		t.Errorf("node schema missing catalog entries")
	}
}

func TestReplayValidateAndSchemaMatchConfigapi(t *testing.T) {
	doc := `database: {url: "${REPLAY_DATABASE_URL}"}
core: {control_url: "http://hookway:9092", control_token: "${REPLAY_CONTROL_TOKEN}"}
keys: [{name: core, token: "${REPLAY_CAPTURE_TOKEN}", scopes: [capture]}]
`
	want, err := json.Marshal(replayconfigapi.Validate([]byte(doc)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := bridge.ReplayValidate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("replay validation mismatch\n got: %s\nwant: %s", got, want)
	}

	schema, err := bridge.ReplaySchema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, `"retention"`) || !strings.Contains(schema, `"keys"`) {
		t.Fatalf("replay schema missing sections: %s", schema)
	}
}
