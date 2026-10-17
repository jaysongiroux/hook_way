package flowconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestNodeSchemasMatchCatalogAndRoundTrip(t *testing.T) {
	schemas := NodeSchemas()
	if len(schemas) != len(nodeFixtures) {
		t.Fatalf("schemas=%d, want %d", len(schemas), len(nodeFixtures))
	}
	for name, want := range nodeFixtures {
		got, ok := schemas[name]
		if !ok || got.Input != want.in.String() || got.Output != want.out.String() || got.Detached != want.detached || got.OnError != (name != "http_trigger" && name != "mcp_trigger" && name != "mcp_result" && name != "read_body" && name != "no_auth" && name != "if_else" && name != "sample" && name != "inspect" && name != "launchdarkly") || got.Schema == nil {
			t.Errorf("%s schema metadata = %+v, found=%v", name, got, ok)
		}
		if got.Schema != nil && got.Schema.Type != "object" {
			t.Errorf("%s schema type=%q", name, got.Schema.Type)
		}
	}
	b, err := json.Marshal(schemas)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]NodeSchema
	if err := json.Unmarshal(b, &roundTrip); err != nil || len(roundTrip) != len(schemas) {
		t.Fatalf("round trip: %v (%d entries)", err, len(roundTrip))
	}
}

func TestAuthSchemaExposesStringDurationsAndSecretRef(t *testing.T) {
	auth := NodeSchemas()["auth"].Schema
	assertRequired(t, auth, "scheme")
	if propertyType(auth, "timestamp_tolerance") != "string" {
		t.Fatal("timestamp_tolerance must be authored as a duration string")
	}
	secretRef, ok := auth.Properties.Get("secret_ref")
	if !ok || secretRef.Type != "object" || propertyType(secretRef, "refresh") != "string" || propertyType(secretRef, "overlap") != "string" {
		t.Fatalf("secret_ref duration schema = %+v", secretRef)
	}
	assertRequired(t, secretRef, "provider", "name")
	for _, tc := range []struct{ node, field string }{{"replay_guard", "ttl"}, {"code", "timeout"}, {"http_request", "timeout"}} {
		if got := propertyType(NodeSchemas()[tc.node].Schema, tc.field); got != "string" {
			t.Errorf("%s.%s type=%q, want string", tc.node, tc.field, got)
		}
	}
}

func TestNodeSchemasExposeRepresentativeRequiredFields(t *testing.T) {
	schemas := NodeSchemas()
	assertRequired(t, schemas["http_trigger"].Schema, "path")
	assertRequired(t, schemas["read_body"].Schema, "max_bytes")
	assertRequired(t, schemas["publish"].Schema, "destination", "target")
}

func assertRequired(t *testing.T, schema *jsonschema.Schema, fields ...string) {
	t.Helper()
	want := make(map[string]bool, len(fields))
	for _, field := range fields {
		want[field] = true
	}
	for _, field := range schema.Required {
		delete(want, field)
	}
	if len(want) != 0 {
		t.Fatalf("schema required=%v, missing=%v", schema.Required, want)
	}
}

func propertyType(schema *jsonschema.Schema, field string) string {
	if schema == nil || schema.Properties == nil {
		return ""
	}
	property, ok := schema.Properties.Get(field)
	if !ok || property == nil {
		return ""
	}
	return property.Type
}

// Keep these expectations independent of the catalog: generated authoring
// metadata must not silently widen the runtime's child admission rules.
func TestBatchChildSchemaMatchesAdmission(t *testing.T) {
	order := map[string]int{"transform": 1, "code": 2, "filter": 3, "if_else": 4, "switch": 5, "publish": 6, "sample": 7}
	schemas := NodeSchemas()
	types := []string{"unknown"}
	for name := range nodeFixtures {
		types = append(types, name)
	}
	for _, name := range types {
		t.Run(name, func(t *testing.T) {
			want := order[name]
			if schemas[name].BatchChildOrder != want {
				t.Fatalf("batch order=%d, want %d", schemas[name].BatchChildOrder, want)
			}
			opts := batchTestOptions("shared")
			if name != "publish" {
				opts.Child.Nodes = append(opts.Child.Nodes, map[string]any{"id": "batch.child", "type": name})
			}
			err := validateBatch(opts)
			if want > 0 && err != nil {
				t.Fatalf("allowed child rejected: %v", err)
			}
			if want == 0 && (err == nil || !strings.Contains(err.Error(), "batch children support only")) {
				t.Fatalf("forbidden child admission: %v", err)
			}
		})
	}
}

func TestStagePortsMatchSchemaAndPreserveFallback(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := reg.Lookup("rate_limit")
	if !ok || spec.ResolvePorts == nil {
		t.Fatal("rate_limit must resolve option-dependent ports")
	}
	stages := NodeSchemas()["rate_limit"].StagePorts
	if len(stages) != 2 {
		t.Fatalf("stage ports=%v", stages)
	}
	for _, stage := range []string{"", "request_head", "verified", "unsupported"} {
		want := graph.PortRequestHead
		if stage == "verified" {
			want = graph.PortVerified
		}
		nc := nodeConfigFromYAML(t, "rate_limit", "stage: "+stage+"\n")
		in, out, err := spec.ResolvePorts(nc)
		if err != nil || in != want || out != want {
			t.Fatalf("stage %q ports=(%v, %v), err=%v", stage, in, out, err)
		}
		if metadata, ok := stages[stage]; ok && (metadata.Input != in.String() || metadata.Output != out.String()) {
			t.Fatalf("stage %q schema=%v, runtime=(%v, %v)", stage, metadata, in, out)
		}
	}
	if _, _, err := spec.ResolvePorts(nodeConfigFromYAML(t, "rate_limit", "stage: [verified]\n")); err == nil {
		t.Fatal("malformed stage must retain strict decoding")
	}
}

func TestMultipleInputsCapabilityIsPublishOnly(t *testing.T) {
	schemas := NodeSchemas()
	reg, err := NewRegistryWithOptions(RegistryOptions{ValidateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, schema := range schemas {
		spec, ok := reg.Lookup(name)
		want := name == "publish"
		if !ok || schema.AllowMultipleInputs != want || spec.AllowMultipleInputs != want {
			t.Fatalf("%s capability: schema=%v registry=%v", name, schema.AllowMultipleInputs, spec.AllowMultipleInputs)
		}
	}
}
