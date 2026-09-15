package transform

import (
	"strings"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/graph"
)

const pipelineXML = `<?xml version="1.0" encoding="UTF-8"?>
<MESSAGE xmlns:xlink="http://www.w3.org/1999/xlink">
  <DEAL>
    <EMPTY/>
    <KEEP>ok</KEEP>
    <WRAPPER><EMPTY2/></WRAPPER>
    <PARTY xlink:label="P1">
      <INDIVIDUAL><NAME>Ada</NAME></INDIVIDUAL>
    </PARTY>
    <PARTY Hardcoded="true" xlink:label="P2">
      <ROLES><ROLE><PARTY_ROLE_TYPE>Other</PARTY_ROLE_TYPE></ROLE></ROLES>
    </PARTY>
    <PARTY xlink:label="P3">
      <ROLES><ROLE><PARTY_ROLE_TYPE>Other</PARTY_ROLE_TYPE></ROLE></ROLES>
    </PARTY>
    <PARTY xlink:label="P4">
      <INDIVIDUAL><NAME>Bob</NAME></INDIVIDUAL>
    </PARTY>
    <GENERAL_PARTNER><NAME/></GENERAL_PARTNER>
    <GENERAL_PARTNER><NAME>GP</NAME></GENERAL_PARTNER>
    <RELATIONSHIP xlink:from="P1" xlink:to="P2"/>
    <RELATIONSHIP xlink:from="P1" xlink:to="P3"/>
    <RELATIONSHIP xlink:from="P1" xlink:to="P4"/>
    <RELATIONSHIP xlink:from="P1" xlink:to="GONE"/>
  </DEAL>
</MESSAGE>`

func TestDocumentImplementsBodyCloner(t *testing.T) {
	var _ graph.BodyCloner = Document{}
}

func TestParseXMLRoundTripPreservesXLink(t *testing.T) {
	doc, err := ParseXML([]byte(pipelineXML))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := SerializeXML(doc)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, `xlink:from="P1"`) || !strings.Contains(got, `xlink:to="P4"`) {
		t.Fatalf("serialized XML lost xlink prefixes:\n%s", got)
	}
	if !strings.Contains(got, `xmlns:xlink="http://www.w3.org/1999/xlink"`) {
		t.Fatalf("serialized XML lost xmlns:xlink:\n%s", got)
	}
}

func TestDocumentCloneIsIndependent(t *testing.T) {
	doc, err := ParseXML([]byte(`<ROOT><A>1</A></ROOT>`))
	if err != nil {
		t.Fatal(err)
	}
	clonedAny, err := doc.CloneBody()
	if err != nil {
		t.Fatal(err)
	}
	cloned := clonedAny.(Document)
	cloned.Root.Children[0].Text = "mutated"
	if doc.Root.Children[0].Text == "mutated" {
		t.Fatal("clone aliased the original tree")
	}
}

func TestXMLPruneFullPipeline(t *testing.T) {
	doc, err := ParseXML([]byte(pipelineXML))
	if err != nil {
		t.Fatal(err)
	}
	origCount := CountElements(doc)
	type step struct {
		rule   string
		before int
		after  int
		d      time.Duration
	}
	var steps []step
	out, err := Prune(doc, nil, func(rule string, index, before, after int, d time.Duration) {
		if index != len(steps) {
			t.Errorf("index = %d, want %d", index, len(steps))
		}
		steps = append(steps, step{rule, before, after, d})
	})
	if err != nil {
		t.Fatal(err)
	}
	if CountElements(doc) != origCount {
		t.Fatalf("input mutated: %d -> %d", origCount, CountElements(doc))
	}
	if len(steps) != len(DefaultXMLPruneRules) {
		t.Fatalf("logged %d rules, want %d", len(steps), len(DefaultXMLPruneRules))
	}
	for i, rule := range DefaultXMLPruneRules {
		if steps[i].rule != rule {
			t.Errorf("steps[%d].rule = %s, want %s", i, steps[i].rule, rule)
		}
		if steps[i].d < 0 {
			t.Errorf("steps[%d] negative duration", i)
		}
	}
	if steps[0].after >= steps[0].before {
		t.Fatalf("rule 1 did not remove empty leaves: %d -> %d", steps[0].before, steps[0].after)
	}
	if steps[1].after >= steps[1].before {
		t.Fatalf("rule 2 did not remove empty parents: %d -> %d", steps[1].before, steps[1].after)
	}
	if steps[2].after >= steps[2].before {
		t.Fatalf("rule 3 did not remove hardcoded parties: %d -> %d", steps[2].before, steps[2].after)
	}
	if steps[3].after >= steps[3].before {
		t.Fatalf("rule 4 did not remove blank general partners: %d -> %d", steps[3].before, steps[3].after)
	}
	if steps[4].after >= steps[4].before {
		t.Fatalf("rule 5 did not prune orphaned relationships: %d -> %d", steps[4].before, steps[4].after)
	}

	raw, err := SerializeXML(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"Ada", "Bob", "GP", `xlink:label="P1"`, `xlink:label="P4"`, `xlink:from="P1"`, `xlink:to="P4"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, banned := range []string{"<EMPTY", "WRAPPER", `label="P2"`, `label="P3"`, `to="P2"`, `to="P3"`, `to="GONE"`} {
		if strings.Contains(got, banned) {
			t.Errorf("prune left %q in:\n%s", banned, got)
		}
	}
	if strings.Count(got, "<RELATIONSHIP") != 1 {
		t.Errorf("RELATIONSHIP count = %d, want 1 (P1->P4):\n%s", strings.Count(got, "<RELATIONSHIP"), got)
	}
}

func TestXMLPruneEmptyParentDependsOnEmptyLeaves(t *testing.T) {
	doc, err := ParseXML([]byte(`<ROOT><WRAPPER><EMPTY/></WRAPPER><KEEP>x</KEEP></ROOT>`))
	if err != nil {
		t.Fatal(err)
	}
	afterLeaves, err := Prune(doc, []string{RuleRemoveEmptySelfClosingElements}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := SerializeXML(afterLeaves)
	if !strings.Contains(string(raw), "WRAPPER") {
		t.Fatalf("rule 1 must not steal emptied parents:\n%s", raw)
	}
	afterParents, err := Prune(afterLeaves, []string{RuleRemoveEmptyParents}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = SerializeXML(afterParents)
	if strings.Contains(string(raw), "WRAPPER") {
		t.Fatalf("rule 2 must remove emptied parents:\n%s", raw)
	}
	if !strings.Contains(string(raw), "KEEP") {
		t.Fatalf("KEEP dropped:\n%s", raw)
	}
}

func TestXMLPruneUnknownRuleFailsClosed(t *testing.T) {
	doc, err := ParseXML([]byte(`<ROOT><A/></ROOT>`))
	if err != nil {
		t.Fatal(err)
	}
	orig := CountElements(doc)
	_, err = Prune(doc, []string{RuleRemoveEmptySelfClosingElements, "not_a_rule"}, nil)
	if err == nil || !strings.Contains(err.Error(), "not_a_rule") {
		t.Fatalf("err = %v, want unknown rule", err)
	}
	if CountElements(doc) != orig {
		t.Fatal("failed prune mutated input")
	}
}

func TestParseXMLRejectsNonUTF8Charset(t *testing.T) {
	_, err := ParseXML([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?><ROOT/>`))
	if err == nil || !strings.Contains(err.Error(), "charset") {
		t.Fatalf("err = %v, want charset failure", err)
	}
}

func TestParseXMLRejectsMalformed(t *testing.T) {
	_, err := ParseXML([]byte(`<ROOT><UNCLOSED>`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}
