package filter

import (
	"testing"

	"github.com/jasongiroux/hookway/internal/transform"
)

// doc parses XML for a test, failing the test on malformed input rather than
// returning a zero Document that would make an assertion pass for the wrong
// reason.
func doc(t *testing.T, raw string) transform.Document {
	t.Helper()
	d, err := transform.ParseXML([]byte(raw))
	if err != nil {
		t.Fatalf("ParseXML(%q) = %v", raw, err)
	}
	return d
}

const sampleXML = `<MESSAGE xmlns:ns="http://example.test/ns">
  <LOAN id="L1" ns:kind="primary">
    <Amount>500.25</Amount>
    <Borrower><Name>Ada</Name></Borrower>
  </LOAN>
  <LOAN id="L2"><Amount>999</Amount></LOAN>
  <Empty></Empty>
</MESSAGE>`

func TestWalkXMLPathResolvesNestedElementText(t *testing.T) {
	d := doc(t, sampleXML)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"LOAN.Amount", "500.25"},
		{"LOAN.Borrower.Name", "Ada"},
	} {
		got, ok := walkXMLPath(d, tc.path)
		if !ok {
			t.Fatalf("walkXMLPath(%q) not found, want %q", tc.path, tc.want)
		}
		if got != tc.want {
			t.Fatalf("walkXMLPath(%q) = %v, want %q", tc.path, got, tc.want)
		}
	}
}

func TestWalkXMLPathReadsAttributes(t *testing.T) {
	d := doc(t, sampleXML)
	got, ok := walkXMLPath(d, "LOAN.@id")
	if !ok || got != "L1" {
		t.Fatalf("walkXMLPath(\"LOAN.@id\") = %v, %v; want \"L1\", true", got, ok)
	}
	// A namespace-prefixed attribute is addressed by its local name, because a
	// vendor may change the prefix without changing the document's meaning.
	got, ok = walkXMLPath(d, "LOAN.@kind")
	if !ok || got != "primary" {
		t.Fatalf("walkXMLPath(\"LOAN.@kind\") = %v, %v; want \"primary\", true", got, ok)
	}
}

func TestWalkXMLPathFirstRepeatedElementWins(t *testing.T) {
	d := doc(t, sampleXML)
	got, _ := walkXMLPath(d, "LOAN.Amount")
	if got != "500.25" {
		t.Fatalf("walkXMLPath resolved a later sibling: got %v, want the first LOAN", got)
	}
}

func TestWalkXMLPathMissingPathIsNotFound(t *testing.T) {
	d := doc(t, sampleXML)
	for _, path := range []string{"", "NOPE", "LOAN.NOPE", "LOAN.Amount.Deeper", "LOAN.@nope", "@nope"} {
		if got, ok := walkXMLPath(d, path); ok {
			t.Fatalf("walkXMLPath(%q) = %v, true; want not found", path, got)
		}
	}
}

func TestWalkXMLPathEmptyElementResolvesToEmptyString(t *testing.T) {
	d := doc(t, sampleXML)
	got, ok := walkXMLPath(d, "Empty")
	if !ok {
		t.Fatal("walkXMLPath(\"Empty\") not found; an element that exists must resolve")
	}
	if got != "" {
		t.Fatalf("walkXMLPath(\"Empty\") = %v, want \"\"", got)
	}
}

func TestWalkXMLPathNilRootIsNotFound(t *testing.T) {
	if got, ok := walkXMLPath(transform.Document{}, "LOAN"); ok {
		t.Fatalf("walkXMLPath on an empty Document = %v, true; want not found", got)
	}
}
