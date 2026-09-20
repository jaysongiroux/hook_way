package flowconfig

import "testing"

func TestReviewRuntimeRejectsSchemaInvalidOptions(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"loop_guard", "max_hops: -1\n"},
		{"rate_limit", "rpm: 60\nburst: -1\n"},
		{"replay_guard", "max_entries: -1\n"},
		{"lookup", "provider: dynamodb\nkey: id\ncache: {max_entries: -1}\n"},
		{"code", "script: return input.body\nmax_input_bytes: -1\nmax_output_bytes: -1\nmax_call_stack: -1\n"},
		{"auth", "scheme: hmac_sha256_hex\nsecret_ref: {provider: vault, name: key}\n"},
		{"auth", "scheme: header_exact\nsecret: value\nsource: {kind: cookie, key: token}\n"},
		{"auth", "scheme: header_exact\nsecret: value\nsource: {kind: header, key: ''}\n"},
	}

	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, _ := r.Lookup(tc.name)
			if _, err := spec.New(nodeConfigFromYAML(t, tc.name, tc.body)); err == nil {
				t.Errorf("runtime accepted options rejected by the advertised schema: %s", tc.body)
			}
		})
	}
}

func TestReviewSchemasEncodeExclusiveContracts(t *testing.T) {
	schemas := NodeSchemas()
	if len(schemas["auth"].Schema.OneOf) == 0 {
		t.Error("auth schema omits the secret versus secret_ref exclusive contract")
	}
	if len(schemas["respond"].Schema.OneOf) == 0 {
		t.Error("respond schema omits the status versus passthrough exclusive contract")
	}
}
