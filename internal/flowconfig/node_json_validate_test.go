package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

func TestJSONValidateAndLegacyRuntimeShareExactImmutableBehavior(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "json_validate", true: "legacy"}[legacy], func(t *testing.T) {
			schema := transform.JSONSchema{"type": "object", "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string"}, "missing": map[string]any{"default": "injected"}}}
			coercions := []transform.ScalarCoercion{{Path: "id", Type: "string"}}
			var node graph.Node
			var err error
			if legacy {
				node, err = newTransformNode("validate", TransformOptions{Kind: "json_schema", JSONSchema: schema, Coercions: coercions}, nil)
			} else {
				node, err = newJSONValidateNode("validate", JSONValidateOptions{Schema: schema, Coercions: coercions})
			}
			if err != nil {
				t.Fatal(err)
			}
			raw := []byte(`{"id":9007199254740993,"extra":{"keep":true}}`)
			env := graph.Envelope{Body: map[string]any{"id": json.Number("9007199254740993"), "extra": map[string]any{"keep": true}}, BodyFormat: "json", V: graph.Verified{Raw: graph.RawRequest{Body: raw}}, Vars: map[string]string{"unchanged": "yes"}}
			before, _ := json.Marshal(env.Body)
			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			out := result.(graph.Envelope)
			if out.Body.(map[string]any)["id"] != "9007199254740993" || !bytes.Equal(out.V.Raw.Body, raw) || out.Vars["unchanged"] != "yes" {
				t.Fatalf("runtime changed retained data: %#v", out)
			}
			if _, ok := out.Body.(map[string]any)["missing"]; ok {
				t.Fatal("default injected")
			}
			after, _ := json.Marshal(env.Body)
			if !bytes.Equal(before, after) {
				t.Fatal("input mutated")
			}
			_, err = node.Run(context.Background(), graph.Envelope{Body: map[string]any{}, BodyFormat: "json"})
			var ne graph.NodeError
			var validation *transform.ValidationError
			if !errors.As(err, &ne) || ne.Status != 400 || !errors.As(err, &validation) || len(validation.Issues) == 0 {
				t.Fatalf("schema rejection not 400 with issues: %v", err)
			}
		})
	}
}
func TestJSONValidateRejectsNonJSONAndAllowsJSONNull(t *testing.T) {
	n, err := newJSONValidateNode("validate", JSONValidateOptions{Schema: transform.JSONSchema{"type": []string{"object", "null"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []graph.Envelope{
		{BodyFormat: "json", V: graph.Verified{Raw: graph.RawRequest{Body: []byte("malformed")}}},
		{BodyFormat: "json"}, {BodyFormat: "raw"}, {BodyFormat: "xml", Body: transform.Document{}},
		{BodyFormat: "multipart", Body: map[string]any{}}, {Body: []byte("raw")},
	} {
		_, err := n.Run(context.Background(), env)
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 400 {
			t.Fatalf("nonJSON status: %v", err)
		}
	}
	if _, err := n.Run(context.Background(), graph.Envelope{BodyFormat: "json", V: graph.Verified{Raw: graph.RawRequest{Body: []byte("null")}}}); err != nil {
		t.Fatalf("JSON null rejected: %v", err)
	}
	if _, err := n.Run(context.Background(), graph.RequestHead{}); err == nil {
		t.Fatal("wrong phase accepted")
	}
}
func TestJSONSchemaConstructionErrorsNotDiscarded(t *testing.T) {
	if _, err := newTransformNode("bad", TransformOptions{Kind: "json_schema", JSONSchema: transform.JSONSchema{"type": "bogus"}}, nil); err == nil {
		t.Fatal("compiler error discarded")
	}
	if _, err := newJSONValidateNode("bad", JSONValidateOptions{Schema: transform.JSONSchema{"$ref": "https://example.invalid/external"}}); err == nil {
		t.Fatal("external reference accepted")
	}
}
func TestJSONValidateYAMLSchemaConstraintsRemainExact(t *testing.T) {
	n := mustCatalogNode(t, "json_validate", "schema: {type: object, properties: {id: {type: integer, minimum: 9007199254740993}}}\n", RegistryOptions{})
	_, err := n.Run(context.Background(), graph.Envelope{Body: map[string]any{"id": json.Number("9007199254740992")}})
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 400 {
		t.Fatalf("YAML minimum rounded: %v", err)
	}
	if _, err := n.Run(context.Background(), graph.Envelope{Body: map[string]any{"id": json.Number("9007199254740993")}}); err != nil {
		t.Fatal(err)
	}
}
