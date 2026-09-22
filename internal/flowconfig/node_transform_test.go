package flowconfig

import (
	"context"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

func TestTransformChangedFieldsBindsJSONArrayVar(t *testing.T) {
	node := newTransformNode("diff", TransformOptions{
		Kind: "changed_fields",
		From: "body.data.previousObject",
		To:   "body.data.object",
		Name: "diff",
	}, nil)
	out, err := node.Run(context.Background(), graph.Envelope{
		Body: map[string]any{
			"data": map[string]any{
				"previousObject": map[string]any{"status": map[string]any{"code": "A"}, "n": 1},
				"object":         map[string]any{"status": map[string]any{"code": "B"}, "n": 1},
			},
		},
		Vars: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.(graph.Envelope).Vars["diff"]
	if got == "" || got[0] != '[' {
		t.Fatalf("diff var = %q, want JSON array", got)
	}
	if !strings.Contains(got, `"status"`) || !strings.Contains(got, `"status.code"`) {
		t.Fatalf("diff = %s, want status promotion", got)
	}
}

func TestTransformFieldMapCopiesPath(t *testing.T) {
	node := newTransformNode("map", TransformOptions{
		Kind: "field_map",
		Mappings: []FieldMappingOptions{
			{From: "body.data.object.id", To: "vars.application_id"},
			{From: "body.data.object.id", To: "body.mapped.id"},
		},
	}, nil)
	out, err := node.Run(context.Background(), graph.Envelope{
		Body: map[string]any{"data": map[string]any{"object": map[string]any{"id": "abc"}}},
		Vars: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	if env.Vars["application_id"] != "abc" {
		t.Fatalf("vars = %#v", env.Vars)
	}
	mapped, _ := env.Body.(map[string]any)["mapped"].(map[string]any)
	if mapped["id"] != "abc" {
		t.Fatalf("body = %#v", env.Body)
	}
}

func TestTransformXMLPruneRewritesEnvelopeBody(t *testing.T) {
	doc, err := transform.ParseXML([]byte(`<ROOT><EMPTY/><KEEP>ok</KEEP></ROOT>`))
	if err != nil {
		t.Fatal(err)
	}
	node := newTransformNode("prune", TransformOptions{Kind: "xml_prune"}, nil)
	out, err := node.Run(context.Background(), graph.Envelope{Body: doc})
	if err != nil {
		t.Fatal(err)
	}
	got, err := transform.SerializeXML(out.(graph.Envelope).Body.(transform.Document))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "EMPTY") || !strings.Contains(string(got), "KEEP") {
		t.Fatalf("pruned = %s", got)
	}
}

func TestTransformXMLPruneRejectsJSONBody(t *testing.T) {
	node := newTransformNode("prune", TransformOptions{Kind: "xml_prune"}, nil)
	_, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"a": 1}})
	if err == nil || !strings.Contains(err.Error(), "not XML") {
		t.Fatalf("err = %v, want not XML", err)
	}
}
