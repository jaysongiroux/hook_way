package capture

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

// roundTripValue sends v through the converter and the real codec (JSON and
// gzip), so the test covers every layer a snapshot crosses.
func roundTripValue(t *testing.T, v any) any {
	t.Helper()
	encoded, err := valueFromAny(v, 0)
	if err != nil {
		t.Fatalf("valueFromAny(%#v) = %v", v, err)
	}
	data, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &deliverysnapshot.Envelope{Body: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := deliverysnapshot.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := anyFromValue(s.Envelope.Body, 0)
	if err != nil {
		t.Fatalf("anyFromValue = %v", err)
	}
	return out
}

func TestValueRoundTripIsExact(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<?xml version="1.0"?><o xmlns:a="urn:a" a:id="7"><i> 1 </i><e/></o>`), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]any{
		"nil":            nil,
		"bool":           true,
		"string":         "héllo",
		"invalid utf-8":  "caf\xe9 \xff",
		"nested invalid": map[string]any{"filename": "caf\xe9.txt", "list": []any{"\xc3"}},
		"json number":    json.Number("12345678901234567890"),
		"bytes":          []byte{0, 1, 255},
		"empty bytes":    []byte{},
		"nil bytes":      []byte(nil),
		"string map":     map[string]string{"a": "b"},
		"empty strmap":   map[string]string{},
		"nil strmap":     map[string]string(nil),
		"strings":        []string{"x", ""},
		"empty strings":  []string{},
		"nil strings":    []string(nil),
		"int kinds":      []any{int(-1), int8(-8), int16(-16), int32(-32), int64(1 << 60)},
		"uint kinds":     []any{uint(1), uint8(8), uint16(16), uint32(32), uint64(math.MaxUint64)},
		"float kinds":    []any{float32(1.5), float64(0.1), math.Inf(1), math.Inf(-1), math.MaxFloat64, math.SmallestNonzeroFloat64},
		"xml document":   doc,
		"nested": map[string]any{
			"size":  int64(5),
			"list":  []any{1.0, "x", nil, []any(nil), map[string]any{}, map[string]any(nil)},
			"files": map[string]any{"document": map[string]any{"content": "aGk=", "size": int64(2)}},
		},
		"empty map":  map[string]any{},
		"nil map":    map[string]any(nil),
		"empty list": []any{},
		"nil list":   []any(nil),
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			if got := roundTripValue(t, v); !reflect.DeepEqual(got, v) {
				t.Fatalf("round trip = %#v (%T), want %#v (%T)", got, got, v, v)
			}
		})
	}
}

func TestValueNamedScalarsDecodeToTheirKind(t *testing.T) {
	type label string
	if got := roundTripValue(t, label("x")); got != "x" {
		t.Fatalf("round trip = %#v (%T), want the base string", got, got)
	}
}

func TestValuePointerDocumentDecodesAsAValue(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte("<a/>"), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTripValue(t, &doc); !reflect.DeepEqual(got, doc) {
		t.Fatalf("round trip = %#v, want the Document value (the executor's clone does the same)", got)
	}
}

func TestValueRejectsUnsupportedTypes(t *testing.T) {
	for name, v := range map[string]any{
		"struct":      struct{ A int }{1},
		"func":        func() {},
		"int map":     map[int]string{1: "a"},
		"nil pointer": (*transform.Document)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := valueFromAny(v, 0); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("valueFromAny = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestValueRejectsExcessiveNesting(t *testing.T) {
	var v any = "leaf"
	for range maxValueDepth + 5 {
		v = []any{v}
	}
	if _, err := valueFromAny(v, 0); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("valueFromAny = %v, want a nesting error", err)
	}
}

func TestAnyFromValueRejectsUnknownTypes(t *testing.T) {
	if _, err := anyFromValue(&deliverysnapshot.Value{Type: "complex128"}, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("anyFromValue = %v, want ErrUnsupported", err)
	}
	if _, err := anyFromValue(nil, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("anyFromValue(nil) = %v, want ErrUnsupported", err)
	}
}

func TestXMLSnapshotMixedContentAndChildIdentity(t *testing.T) {
	raw := `<?xml version="1.0"?><!--before--><R xmlns="urn:sample" xmlns:p="urn:other" key="plain" p:key="namespaced">before<C/>between<?note value?><!--comment--><p:C/>after</R><!--after-->`
	doc, err := transform.ParseXMLWithLimits([]byte(raw), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	got := roundTripValue(t, doc).(transform.Document)
	if !reflect.DeepEqual(got, doc) {
		t.Fatal("XML snapshot lost tree metadata")
	}
	if got.Root.Content[1].Element != got.Root.Children[0] {
		t.Fatal("content child alias lost")
	}
	encoded, err := transform.SerializeXML(got)
	if err != nil || string(encoded) != raw {
		t.Fatalf("snapshot %s: %v", encoded, err)
	}
	index := 9
	malformed := &deliverysnapshot.Value{Type: deliverysnapshot.TypeXML, XML: &deliverysnapshot.XMLDocument{Root: &deliverysnapshot.XMLElement{Content: []deliverysnapshot.XMLContent{{Child: &index}}}}}
	if _, err := anyFromValue(malformed, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invalid child reference accepted: %v", err)
	}
}
