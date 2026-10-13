package transform

import (
	"strings"
	"testing"
	"time"
)

func sampleXMLAdapter() XMLAdapterOptions {
	return XMLAdapterOptions{Link: XMLSelector{Local: "LINK", Namespace: "urn:sample"}, Entity: XMLSelector{Local: "ENTITY", Namespace: "urn:sample"}, ExtensionItem: XMLSelector{Local: "ITEM", Namespace: "urn:sample:extension"}, ItemName: XMLSelector{Local: "NAME", Namespace: "urn:sample:extension"}, ItemValue: XMLSelector{Local: "VALUE", Namespace: "urn:sample:extension"}, SequenceAttribute: "SequenceIndex", PlaceholderTexts: []string{"PlaceholderA", "PlaceholderB"}, OptionalItems: []string{"OptionalFieldA", "OptionalFieldB"}}
}
func TestXMLMixedContentNamespaceCloneAndProlog(t *testing.T) {
	raw := `<?xml version="1.0"?><!--before--><?sample test?><ROOT xmlns="urn:sample" xmlns:s="urn:sample" xmlns:a="urn:attr" a:id="x">before<s:A>inside<!--keep--><?sample more?></s:A>after<B xmlns="">plain</B></ROOT><!--after-->`
	d, err := ParseXMLWithLimits([]byte(raw), XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	copy := d.Clone()
	out, err := SerializeXML(copy)
	if err != nil || string(out) != raw {
		t.Fatalf("mixed-content roundtrip: %s %v", out, err)
	}
	copy.Root.Children = nil
	out, _ = SerializeXML(copy)
	if strings.Contains(string(out), "<s:A>") || !strings.Contains(string(out), "beforeafter") {
		t.Fatal(string(out))
	}
	again, _ := SerializeXML(d)
	if string(again) != raw {
		t.Fatal("clone mutated input")
	}
	for _, invalid := range []string{`<R><A></R></A>`, `<p:R/>`, `<R a="1" a="2"/>`, `<!DOCTYPE R SYSTEM "https://example.invalid/x"><R/>`, `<R xmlns:p=""/>`} {
		if _, err := ParseXMLWithLimits([]byte(invalid), XMLLimits{}); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}
func TestOrderedXMLRulesAndNoLateCascade(t *testing.T) {
	raw := `<ROOT xmlns="urn:sample" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:e="urn:sample:extension"><TOP><EMPTY a="1"/><SPACE> </SPACE><COMMENT><!--node--></COMMENT><WRAP><INNER> </INNER></WRAP><ENTITY SequenceIndex="8" xlink:label="keep"><TEXT>Real</TEXT></ENTITY><ENTITY SequenceIndex="9" xlink:label="gone"><TEXT>PlaceholderA</TEXT></ENTITY><ENTITY SequenceIndex="10"><TEXT>PlaceholderB</TEXT></ENTITY><BOX><ENTITY><TEXT>PlaceholderA</TEXT></ENTITY></BOX><ITEMS><e:ITEM><e:NAME>OptionalFieldA</e:NAME><e:VALUE> </e:VALUE></e:ITEM><e:ITEM><e:NAME>Other</e:NAME><e:VALUE> </e:VALUE></e:ITEM></ITEMS><LINK xlink:from="keep" xlink:to="gone"/><LINK from="keep"/><LINK/><e:ENTITY>other namespace</e:ENTITY></TOP><DIRECT> </DIRECT></ROOT>`
	d, err := ParseXMLWithLimits([]byte(raw), XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	var counts []int
	var order []string
	out, err := PruneOrdered(d, sampleXMLAdapter(), func(name string, _ int, _, after int, _ time.Duration) {
		order = append(order, name)
		counts = append(counts, after)
	})
	if err != nil {
		t.Fatal(err)
	}
	rawOut, _ := SerializeXML(out)
	got := string(rawOut)
	for _, bad := range []string{"<EMPTY", "<SPACE", "<COMMENT", "<WRAP", "label=\"gone\"", "PlaceholderA", "PlaceholderB", "OptionalFieldA", "to=\"gone\""} {
		if strings.Contains(got, bad) {
			t.Fatalf("retained %s: %s", bad, got)
		}
	}
	for _, want := range []string{`SequenceIndex="1"`, `from="keep"`, `<LINK/>`, `<DIRECT> </DIRECT>`, `<e:ENTITY>other namespace</e:ENTITY>`, `Other`, `<BOX`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
	if len(counts) != 5 || strings.Join(order, ",") != "empty_nodes,empty_cascade,placeholder_entities,blank_optional_items,dangling_links" {
		t.Fatal(counts, order)
	}
	original, _ := SerializeXML(d)
	if string(original) != raw {
		t.Fatal("input changed")
	}
	// Rule 2 removes truly empty nodes but leaves whitespace/comment child nodes;
	// rule 3 removes those leaves and cascades while retaining root children.
	if counts[0]-counts[1] < 3 {
		t.Fatal("empty versus whitespace/comment cascade was lost", counts)
	}
}

func TestXMLTwelveMiBBoundAndMixedContentNodeBudget(t *testing.T) {
	const limit = 12 << 20
	raw := []byte("<R>" + strings.Repeat("x", limit-len("<R></R>")) + "</R>")
	doc, err := ParseXMLWithLimits(raw, XMLLimits{MaxBytes: limit, MaxDepth: 1, MaxNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := SerializeXML(doc.Clone())
	if err != nil || string(serialized) != string(raw) {
		t.Fatal("12 MiB clone/serialization differs", err)
	}
	if _, err := ParseXMLWithLimits(append(raw, ' '), XMLLimits{MaxBytes: limit}); err == nil {
		t.Fatal("oversize XML accepted")
	}
	if _, err := ParseXMLWithLimits([]byte(`<R><!--1--><!--2--></R>`), XMLLimits{MaxNodes: 2}); err == nil {
		t.Fatal("non-element nodes escaped budget")
	}
}
