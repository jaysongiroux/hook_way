package vars

import (
	"strings"
	"testing"
)

func TestCustomNamesKeepStandardParserStrict(t *testing.T) {
	raw := "prefix/{header.X-Event-ID}/{path}"
	if _, err := Parse(raw); err == nil {
		t.Fatal("standard names became permissive")
	}
	tpl, err := ParseWithNames(raw, func(name string) bool { return ValidName(name) || strings.HasPrefix(name, "header.") })
	if err != nil {
		t.Fatal(err)
	}
	got, err := tpl.Render(map[string]string{"header.X-Event-ID": "event", "path": "route"})
	if err != nil || got != "prefix/event/route" {
		t.Fatalf("%s %v", got, err)
	}
	for _, raw := range []string{"{nested{header.X}}", "{header.X", "header.X}", "{bad-name}"} {
		if _, err := ParseWithNames(raw, ValidName); err == nil {
			t.Fatalf("bad syntax accepted: %s", raw)
		}
	}
	if _, err := ParseWithNames("{bad-name}", nil); err == nil {
		t.Fatal("nil name validator did not use strict defaults")
	}
}
