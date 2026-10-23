package bridge_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/cmd/hookway-wasm/bridge"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	replayconfigapi "github.com/jaysongiroux/hook_way/internal/replay/configapi"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
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

func TestSampleValidationMatchesNative(t *testing.T) {
	fixture, err := os.ReadFile("../../../examples/sample.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []string{"0", "1", "0.235", "missing", "null", "-0.01", "1.01", ".nan", ".inf", "'0.2'"} {
		input := strings.Replace(string(fixture), "rate: 0.2", "rate: "+rate, 1)
		if rate == "missing" {
			input = strings.Replace(string(fixture), ", rate: 0.2", "", 1)
		}
		want, err := json.Marshal(configapi.Validate([]byte(input)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := bridge.Validate(input)
		if err != nil || got != string(want) {
			t.Fatalf("rate=%s: bridge=%s native=%s error=%v", rate, got, want, err)
		}
		valid := rate == "0" || rate == "1" || rate == "0.235"
		hasError := false
		for _, issue := range configapi.Validate([]byte(input)).Issues {
			hasError = hasError || issue.Severity == "error"
		}
		if hasError == valid {
			t.Fatalf("rate=%s valid=%v response=%s", rate, valid, got)
		}
	}
}

func TestNamedEventsValidationMatchesNative(t *testing.T) {
	fixture, err := os.ReadFile("../../../examples/adapters/named-events.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(string(fixture), "${WEBHOOK_URL}", "https://receiver.example.test")
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"valid", source, true},
		{"missing reference", strings.Replace(source, "event: issue.opened", "event: missing", 1), false},
		{"external reference", strings.Replace(source, "title: {type: string, minLength: 1}", "title: {$ref: 'https://example.invalid/schema'}", 1), false},
		{"unknown field", strings.Replace(source, "description: A new issue was opened.", "description: Issue\n    unknown: true", 1), false},
		{"exact numeric schema", strings.Replace(source, "additionalProperties: false", "additionalProperties: false\n      maxProperties: 9007199254740993", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := configapi.Validate([]byte(tc.source))
			want, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			got, err := bridge.Validate(tc.source)
			if err != nil || got != string(want) {
				t.Fatalf("bridge=%s native=%s error=%v", got, want, err)
			}
			valid := true
			for _, issue := range response.Issues {
				valid = valid && issue.Severity == "warning"
			}
			if valid != tc.valid {
				t.Fatalf("valid=%v: %s", valid, got)
			}
		})
	}
}

func TestSchemaMatchesConfig(t *testing.T) {
	want, err := configapi.Schema()
	if err != nil {
		t.Fatalf("configapi.Schema: %v", err)
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

func TestContractInfoAndReplayActionInput(t *testing.T) {
	got, err := bridge.ContractInfo()
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(uicontract.Info())
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("contract mismatch: %s", got)
	}

	got, err = bridge.ReplayActionValidate(`{"idempotency_key":"bad key","record_ids":["unknown","unknown"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var result uicontract.ReplayActionResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 2 {
		t.Fatalf("unexpected action issues: %+v", result.Issues)
	}
	if _, err := bridge.ReplayActionValidate("{"); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestJSONSampleValidateMatchesSharedContract(t *testing.T) {
	for _, raw := range []string{
		`{"schema":{"type":"integer"},"sample":"9007199254740993"}`,
		`{"schema":{"type":"integer"},"sample":"\"private-value\""}`,
		`{"schema":{"$ref":"https://private-value.invalid/schema"},"sample":"{}"}`,
	} {
		input, err := uicontract.DecodeJSONSampleInput([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(uicontract.ValidateJSONSample(input))
		if err != nil {
			t.Fatal(err)
		}
		got, err := bridge.JSONSampleValidate(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("JSON sample contract drift: %s", got)
		}
	}
	for _, raw := range []string{"{", `{"schema":{},"sample":"{}","unknown":"private-value"}`} {
		if _, err := bridge.JSONSampleValidate(raw); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("malformed request: %v", err)
		}
	}
}

func TestRequestSignatureProtocolsValidateOffline(t *testing.T) {
	original, err := os.ReadFile("../../../examples/auth/request-signatures.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		src   string
		valid bool
	}{
		{string(original), true},
		{strings.Replace(string(original), "hmac_sha256_headers_dot", "unknown", 1), false},
		{strings.Replace(string(original), "timestamp_unit: milliseconds", "timestamp_unit: invalid", 1), false},
	} {
		got, err := bridge.Validate(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		var result configapi.ValidateResponse
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatal(err)
		}
		valid := true
		for _, issue := range result.Issues {
			if issue.Severity != "warning" {
				valid = false
			}
		}
		if valid != tc.valid {
			t.Fatalf("valid=%v: %s", valid, got)
		}
	}
}

func TestRoutingAdmissionOfflineAndUnboundProfileRejected(t *testing.T) {
	for _, example := range []string{"shared", "admission"} {
		source, err := os.ReadFile("../../../examples/routing/" + example + ".yaml") // #nosec G304 -- Controlled fixture name or test-owned temporary path.
		if err != nil {
			t.Fatal(err)
		}
		result, err := bridge.Validate(string(source))
		if err != nil {
			t.Fatal(err)
		}
		var response configapi.ValidateResponse
		if err := json.Unmarshal([]byte(result), &response); err != nil {
			t.Fatal(err)
		}
		for _, issue := range response.Issues {
			if issue.Severity != "warning" {
				t.Fatal(issue)
			}
		}
		result, err = bridge.Validate(strings.ReplaceAll(string(source), "profile: sandbox", "profile: missing"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(result), &response); err != nil {
			t.Fatal(err)
		}
		failed := false
		for _, issue := range response.Issues {
			if issue.Severity != "warning" {
				failed = true
			}
		}
		if !failed {
			t.Fatal("unbound profile accepted")
		}
	}
}

func TestProxyContractOfflineValidation(t *testing.T) {
	for _, example := range []string{"request-contract", "admitted"} {
		source, err := os.ReadFile("../../../examples/proxy/" + example + ".yaml") // #nosec G304 -- Controlled fixture name or test-owned temporary path.
		if err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []bool{false, true} {
			input := string(source)
			if invalid {
				input = strings.ReplaceAll(input, "max_response_bytes: 20971520", "max_response_bytes: 67108865")
			}
			result, err := bridge.Validate(input)
			if err != nil {
				t.Fatal(err)
			}
			var response configapi.ValidateResponse
			if err := json.Unmarshal([]byte(result), &response); err != nil {
				t.Fatal(err)
			}
			failed := false
			for _, issue := range response.Issues {
				failed = failed || issue.Severity != "warning"
			}
			if failed != invalid {
				t.Fatalf("%s invalid=%v: %s", example, invalid, result)
			}
		}
	}
}

func TestResolutionWASMMatchesPureContract(t *testing.T) {
	for _, input := range []uicontract.ReplayActionInput{
		{Action: "resolve", IdempotencyKey: "receipt-1", Note: "Verified upstream receipt", Reference: "INC-42"},
		{Action: "resolve", IdempotencyKey: "receipt-1", Note: "\x00"},
	} {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := bridge.ReplayActionValidate(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(uicontract.ValidateReplayAction(input))
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("%s != %s", got, want)
		}
	}
}
