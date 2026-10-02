package flowconfig

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"

	"github.com/jaysongiroux/hook_way/internal/auth"
)

func TestFormAuthCatalogMatchesRegistry(t *testing.T) {
	schemas := NodeSchemas()
	node := schemas["auth"]
	property, ok := node.Schema.Properties.Get("scheme")
	if !ok {
		t.Fatal("auth scheme property missing")
	}
	var want []any
	for _, scheme := range auth.Schemes() {
		if scheme == auth.SchemeNoAuth {
			continue
		}
		want = append(want, scheme)
		capability, ok := node.AuthSchemes[scheme]
		if !ok {
			t.Fatalf("missing capabilities for %s", scheme)
		}
		if capability.RequestSignature != auth.IsRequestScheme(scheme) || capability.SeparateTimestamp != auth.SeparateTimestamp(scheme) || capability.JWT != auth.SchemeAllowedForAuthType(auth.AuthTypeJWT, scheme) {
			t.Errorf("capabilities disagree with registry for %s: %+v", scheme, capability)
		}
	}
	if !reflect.DeepEqual(property.Enum, want) {
		t.Errorf("scheme enum=%v want registry=%v", property.Enum, want)
	}
	if len(node.AuthSchemes) != len(want) {
		t.Errorf("capabilities have %d entries, want %d", len(node.AuthSchemes), len(want))
	}
	if _, ok := node.AuthSchemes[auth.SchemeNoAuth]; ok {
		t.Error("no_auth must remain its own node")
	}
	for name, other := range schemas {
		if name != "auth" && other.AuthSchemes != nil {
			t.Errorf("%s unexpectedly carries auth metadata", name)
		}
	}
	for scheme, format := range map[string]string{
		auth.SchemeJWTHS256: "shared_secret", auth.SchemeJWTRS256: "pem", auth.SchemeJWTES256: "pem", auth.SchemeECDSAP256SHA256Headers: "base64_der", auth.SchemeExact: "shared_secret", auth.SchemeBasic: "shared_secret",
	} {
		if got := node.AuthSchemes[scheme].KeyFormat; got != format {
			t.Errorf("%s key format=%q want %q", scheme, got, format)
		}
	}
	encoded, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["auth_schemes"]; !ok {
		t.Fatal("auth capabilities not serialized")
	}
	encoded, err = json.Marshal(schemas["no_auth"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "auth_schemes") {
		t.Fatal("non-auth metadata must be omitted")
	}
}

func TestFormDefaultsMatchRuntimeContracts(t *testing.T) {
	schemas := NodeSchemas()
	multipart := (MultipartOptions{}).limits()
	batch := (BatchOptions{}).normalized()
	initial, max := publishBackoffs(&PublishRetryOptions{})
	for _, tc := range []struct {
		node, path string
		want       any
	}{
		{"loop_guard", "max_hops", newLoopGuard("schema-test", LoopGuardOptions{}).(loopGuardNode).maxHops},
		{"rate_limit", "key_by", newRateLimit("schema-test", RateLimitOptions{RPM: 60}).(rateLimitNode).keyBy},
		{"filter", "on_no_match", newFilterNode("schema-test", FilterOptions{}).(filterNode).onNoMatch},
		{"envelope", "body_format", newEnvelopeNode("schema-test", EnvelopeOptions{}, nil, nil).(envelopeNode).format},
		{"envelope", "multipart.include_content", multipart.IncludeContent},
		{"envelope", "multipart.max_parts", multipart.MaxParts},
		{"envelope", "multipart.max_part_bytes", multipart.MaxPartBytes},
		{"code", "timeout", defaultCodeTimeout.String()},
		{"code", "max_call_stack", defaultCodeStack},
		{"code", "max_input_bytes", defaultCodeInputSize},
		{"code", "max_output_bytes", defaultCodeOutputSize},
		{"http_request", "timeout", defaultHTTPRequestTimeout.String()},
		{"publish", "retry.initial_backoff", initial.String()},
		{"publish", "retry.max_backoff", max.String()},
		{"batch", "max_children", batch.MaxChildren},
		{"batch", "max_inflight", batch.MaxInflight},
		{"batch", "timeout", batch.Timeout},
	} {
		t.Run(tc.node+"/"+tc.path, func(t *testing.T) {
			property := formSchemaAt(t, schemas[tc.node].Schema, tc.path)
			if !reflect.DeepEqual(property.Default, tc.want) {
				t.Errorf("default=%v (%T), want runtime=%v (%T)", property.Default, property.Default, tc.want, tc.want)
			}
		})
	}
	for _, tc := range []struct{ node, path string }{{"rate_limit", "burst"}, {"batch", "concurrency"}, {"envelope", "multipart.max_total_bytes"}} {
		if got := formSchemaAt(t, schemas[tc.node].Schema, tc.path).Default; got != nil {
			t.Errorf("%s.%s has context-dependent default %v", tc.node, tc.path, got)
		}
	}
	falseValue := false
	if actual := (MultipartOptions{IncludeContent: &falseValue}).limits().IncludeContent; actual {
		t.Fatal("false remains an explicit runtime override")
	}
}

func formSchemaAt(t *testing.T, schema *jsonschema.Schema, path string) *jsonschema.Schema {
	t.Helper()
	for _, part := range strings.Split(path, ".") {
		property, ok := schema.Properties.Get(part)
		if !ok {
			t.Fatalf("missing property %s", path)
		}
		schema = property
	}
	return schema
}

func TestFormAuthSchemeOrderIsStable(t *testing.T) {
	scheme, _ := NodeSchemas()["auth"].Schema.Properties.Get("scheme")
	var names []string
	for _, name := range scheme.Enum {
		names = append(names, name.(string))
	}
	if !slices.IsSorted(names) {
		t.Fatalf("scheme enum is not stable: %v", names)
	}
}
