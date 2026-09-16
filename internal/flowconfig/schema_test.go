package flowconfig

import (
	"encoding/json"
	"testing"

	"github.com/invopop/jsonschema"
)

func TestNodeSchemasMatchCatalogAndRoundTrip(t *testing.T) {
	schemas := NodeSchemas()
	if len(schemas) != len(wantedCatalog) {
		t.Fatalf("schemas=%d, want %d", len(schemas), len(wantedCatalog))
	}
	for name, want := range wantedCatalog {
		got, ok := schemas[name]
		if !ok || got.Input != want.in.String() || got.Output != want.out.String() || got.Detached != want.detached || got.OnError != allowsOnError(name, want.in) || got.Schema == nil {
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
