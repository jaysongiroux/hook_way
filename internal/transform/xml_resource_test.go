package transform

import (
	"fmt"
	"strings"
	"testing"
)

func namespacePressureXML() []byte {
	var b strings.Builder
	b.WriteString("<root")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, ` xmlns:p%d="urn:%d"`, i, i)
	}
	b.WriteByte('>')
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "<p%d:child/>", i)
	}
	b.WriteString("</root>")
	return []byte(b.String())
}

func TestXMLNamespaceInheritanceAndShadowing(t *testing.T) {
	doc, err := ParseXMLWithLimits([]byte(`<root xmlns="urn:outer" xmlns:p="urn:parent"><p:a><b xmlns="" xmlns:p="urn:child"><p:c p:id="1"/></b></p:a><p:d/></root>`), XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Root.Children[0]
	b := a.Children[0]
	c := b.Children[0]
	d := doc.Root.Children[1]
	if doc.Root.Name.Space != "urn:outer" || a.Name.Space != "urn:parent" || b.Name.Space != "" || c.Name.Space != "urn:child" || c.Attr[0].Name.Space != "urn:child" || d.Name.Space != "urn:parent" {
		t.Fatal("namespace inheritance, reset or sibling isolation changed")
	}
	out, err := SerializeXML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseXMLWithLimits(out, XMLLimits{}); err != nil {
		t.Fatalf("namespace roundtrip: %v", err)
	}
}

func BenchmarkXMLInheritedNamespacePressure(b *testing.B) {
	raw := namespacePressureXML()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ParseXMLWithLimits(raw, XMLLimits{}); err != nil {
			b.Fatal(err)
		}
	}
}
