package flowconfig

import (
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/transform"
)

// hardXML exercises every feature a naive element-name-to-key mapping would
// destroy: an attribute, a namespaced attribute, two siblings with the same
// name, and mixed content (text alongside child elements).
const hardXML = `<MESSAGE xmlns:ns="http://example.test/ns"><ORDER id="O1" ns:kind="primary">lead text<Amount>500</Amount><Amount>600</Amount></ORDER></MESSAGE>`

func TestXMLScriptRoundTripPreservesDocument(t *testing.T) {
	original, err := transform.ParseXMLWithLimits([]byte(hardXML), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	back, err := scriptToXML(xmlToScript(original))
	if err != nil {
		t.Fatalf("scriptToXML(xmlToScript(doc)) = %v", err)
	}
	want, err := transform.SerializeXML(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transform.SerializeXML(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip changed the document:\n got: %s\nwant: %s", got, want)
	}
}

func TestXMLToScriptShapeIsPlainJSONValues(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(hardXML), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	out := xmlToScript(doc)
	root, ok := out["root"].(map[string]any)
	if !ok {
		t.Fatalf("root = %T, want map[string]any", out["root"])
	}
	if root["name"] != "MESSAGE" {
		t.Fatalf("root name = %v, want MESSAGE", root["name"])
	}
	children, ok := root["children"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("root children = %#v, want one element", root["children"])
	}
	order := children[0].(map[string]any)
	attr, ok := order["attr"].(map[string]any)
	if !ok {
		t.Fatalf("attr = %T, want map[string]any", order["attr"])
	}
	if attr["id"] != "O1" || attr["kind"] != "primary" {
		t.Fatalf("attr = %#v, want id=O1 kind=primary", attr)
	}
	if order["text"] != "lead text" {
		t.Fatalf("text = %v, want \"lead text\"", order["text"])
	}
	// Two same-named siblings must both survive as separate children.
	orderKids := order["children"].([]any)
	if len(orderKids) != 2 {
		t.Fatalf("ORDER children = %d, want 2 (repeated siblings must not collapse)", len(orderKids))
	}
}

func TestScriptToXMLRejectsMalformedShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
	}{
		{"not an object", "just a string"},
		{"no root", map[string]any{"declaration": "<?xml?>"}},
		{"root is not an object", map[string]any{"root": 42}},
		{"element has no name", map[string]any{"root": map[string]any{"text": "x"}}},
		{"element name is not a string", map[string]any{"root": map[string]any{"name": 7}}},
		{"children is not a list", map[string]any{"root": map[string]any{"name": "A", "children": "nope"}}},
		{"child is not an object", map[string]any{"root": map[string]any{"name": "A", "children": []any{"nope"}}}},
		{"attr is not an object", map[string]any{"root": map[string]any{"name": "A", "attr": []any{"nope"}}}},
		{"attrns is not an object", map[string]any{"root": map[string]any{"name": "A", "attrns": []any{"nope"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := scriptToXML(tc.in); err == nil {
				t.Fatalf("scriptToXML(%#v) = nil error, want a rejection", tc.in)
			}
		})
	}
}

func TestScriptToXMLAcceptsAMinimalElement(t *testing.T) {
	doc, err := scriptToXML(map[string]any{"root": map[string]any{"name": "A"}})
	if err != nil {
		t.Fatalf("scriptToXML(minimal) = %v", err)
	}
	if doc.Root == nil || doc.Root.Name.Local != "A" {
		t.Fatalf("root = %#v, want an element named A", doc.Root)
	}
}

// A script builds a tree from scratch; goja hands back float64 for numbers, so
// a non-string text value must be stringified rather than rejected.
func TestScriptToXMLStringifiesScalarText(t *testing.T) {
	doc, err := scriptToXML(map[string]any{"root": map[string]any{"name": "A", "text": float64(500)}})
	if err != nil {
		t.Fatalf("scriptToXML = %v", err)
	}
	if doc.Root.Text != "500" {
		t.Fatalf("text = %q, want \"500\"", doc.Root.Text)
	}
}

// TestXMLScriptCarriesAttributeNamespaces pins the reason attrns exists. attr
// is keyed by local name so a script need not know the vendor's prefix, which
// on its own cannot tell ns:kind from kind and cannot represent the xmlns:ns
// declaration at all -- a read-and-return script would strip both.
func TestXMLScriptCarriesAttributeNamespaces(t *testing.T) {
	original, err := transform.ParseXMLWithLimits([]byte(hardXML), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	root := xmlToScript(original)["root"].(map[string]any)
	// The xmlns:ns declaration is itself an attribute, in the xmlns space.
	if got := root["attrns"].(map[string]any)["ns"]; got != "xmlns" {
		t.Fatalf("root attrns[ns] = %v, want \"xmlns\"", got)
	}
	order := root["children"].([]any)[0].(map[string]any)
	attrns := order["attrns"].(map[string]any)
	if got := attrns["kind"]; got != "http://example.test/ns" {
		t.Fatalf("ORDER attrns[kind] = %v, want the namespace URI", got)
	}
	// An unnamespaced attribute must not gain an entry, or a script that
	// copied attrns wholesale would namespace a plain attribute.
	if _, present := attrns["id"]; present {
		t.Fatalf("attrns = %#v, want no entry for the unnamespaced id", attrns)
	}
}

// A script assigning a plain attribute leaves attrns alone, and the attribute
// must come back unnamespaced rather than inheriting a sibling's namespace.
func TestScriptToXMLLeavesAttributesWithoutAttrnsUnnamespaced(t *testing.T) {
	doc, err := scriptToXML(map[string]any{"root": map[string]any{
		"name":   "A",
		"attr":   map[string]any{"plain": "x", "spaced": "y"},
		"attrns": map[string]any{"spaced": "http://example.test/ns"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range doc.Root.Attr {
		switch a.Name.Local {
		case "plain":
			if a.Name.Space != "" {
				t.Fatalf("plain attribute gained namespace %q", a.Name.Space)
			}
		case "spaced":
			if a.Name.Space != "http://example.test/ns" {
				t.Fatalf("spaced attribute space = %q, want the URI", a.Name.Space)
			}
		}
	}
}

func TestXMLScriptPreservesMixedContentAndDuplicateLocalAttributes(t *testing.T) {
	raw := `<?xml version="1.0"?><!--before--><R xmlns="urn:sample" xmlns:p="urn:other" key="plain" p:key="namespaced">before<C/>between<?note value?><!--comment--><p:C/>after</R><!--after-->`
	original, err := transform.ParseXMLWithLimits([]byte(raw), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	script := xmlToScript(original)
	roundtrip, err := scriptToXML(script)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := transform.SerializeXML(roundtrip)
	if err != nil || string(encoded) != raw {
		t.Fatalf("roundtrip %s: %v", encoded, err)
	}
	root := script["root"].(map[string]any)
	// The ordered representation can edit a namespaced attribute without collapsing its plain sibling.
	attrs := root["attributes"].([]any)
	for _, a := range attrs {
		attr := a.(map[string]any)
		if attr["name"] == "key" && attr["ns"] == "urn:other" {
			attr["value"] = "changed"
		}
	}
	root["text"] = "updated"
	edited, err := scriptToXML(script)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = transform.SerializeXML(edited)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `key="plain" p:key="changed"`) || !strings.Contains(string(encoded), `updated<C/><?note value?><!--comment--><p:C/>`) {
		t.Fatalf("edit lost content: %s", encoded)
	}
	if original.Root.Text != "beforebetweenafter" {
		t.Fatal("script mutated original")
	}
}

func TestXMLScriptOrderedTextEditsSurvive(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<R>before<C/>after</R>`), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	script := xmlToScript(doc)
	root := script["root"].(map[string]any)
	root["content"].([]any)[2].(map[string]any)["text"] = "changed"
	edited, err := scriptToXML(script)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := transform.SerializeXML(edited)
	if err != nil || string(wire) != `<R>before<C/>changed</R>` || edited.Root.Text != "beforechanged" {
		t.Fatalf("ordered edit lost: %s, %v", wire, err)
	}
}

func TestXMLScriptRejectsMarkupInNamesAndContent(t *testing.T) {
	for _, edit := range []func(map[string]any){
		func(root map[string]any) { root["name"] = `R><Injected` },
		func(root map[string]any) { root["attr"].(map[string]any)[`key\" injected=\"value`] = "value" },
		func(root map[string]any) { root["prefix"] = `p><Injected` },
		func(root map[string]any) {
			root["content"] = []any{map[string]any{"kind": "comment", "text": "--><Injected/><!--"}}
		},
		func(root map[string]any) {
			root["content"] = []any{map[string]any{"kind": "pi", "target": "note", "text": "?><Injected/>"}}
		},
	} {
		doc, err := transform.ParseXMLWithLimits([]byte(`<R key="value"/>`), transform.XMLLimits{})
		if err != nil {
			t.Fatal(err)
		}
		script := xmlToScript(doc)
		edit(script["root"].(map[string]any))
		edited, err := scriptToXML(script)
		if err == nil {
			_, err = transform.SerializeXML(edited)
		}
		if err == nil {
			t.Fatal("accepted markup injection")
		}
	}
}
