package vars

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestParseTemplate(t *testing.T) {
	cases := []struct {
		in    string
		names []string
		lit   string
		err   bool
	}{
		{"orders", nil, "orders", false},
		{"{env}-orders", []string{"env"}, "-orders", false},
		{"{env}-{tenant}.fifo", []string{"env", "tenant"}, "-.fifo", false},
		{"{a}{b}", []string{"a", "b"}, "", false},
		{"{env}-{env}", []string{"env"}, "-", false},
		{"{", nil, "", true},
		{"}", nil, "", true},
		{"{}", nil, "", true},
		{"{a{b}}", nil, "", true},
		{"{9x}", nil, "", true},
		{"{a-b}", nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			tpl, err := Parse(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(tpl.Names(), tc.names) {
				t.Fatalf("names: got %v want %v", tpl.Names(), tc.names)
			}
			if tpl.Literal() != tc.lit {
				t.Fatalf("literal: got %q want %q", tpl.Literal(), tc.lit)
			}
			if tpl.HasPlaceholders() != (len(tc.names) > 0) {
				t.Fatalf("HasPlaceholders mismatch")
			}
			if tpl.String() != tc.in {
				t.Fatalf("String round-trip: got %q want %q", tpl.String(), tc.in)
			}
		})
	}
}

func TestParseTreatsEnvReferencesAsLiteral(t *testing.T) {
	tpl, err := Parse("https://${HOST}/hooks/{env}")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tpl.Names(), []string{"env"}) || tpl.Literal() != "https://${HOST}/hooks/" {
		t.Fatalf("env ref must stay literal: names=%v literal=%q", tpl.Names(), tpl.Literal())
	}
	if tpl.String() != "https://${HOST}/hooks/{env}" {
		t.Fatalf("round trip: %q", tpl.String())
	}
	if _, err := Parse("x${HOST"); err == nil {
		t.Fatal("unclosed ${ must error")
	}
	if got, err := Parse("cost$5"); err != nil || got.Literal() != "cost$5" {
		t.Fatalf("bare $ is literal: %v %q", err, got.Literal())
	}
}

func TestTemplateRender(t *testing.T) {
	tpl, err := Parse("{env}-{tenant}-events")
	if err != nil {
		t.Fatal(err)
	}
	got, err := tpl.Render(map[string]string{"env": "qa", "tenant": "acme"})
	if err != nil || got != "qa-acme-events" {
		t.Fatalf("Render: %q %v", got, err)
	}
	_, err = tpl.Render(map[string]string{"env": "qa"})
	var unbound ErrUnbound
	if !errors.As(err, &unbound) || unbound.Name != "tenant" {
		t.Fatalf("expected ErrUnbound{tenant}, got %v", err)
	}
	if _, err := tpl.Render(map[string]string{"env": "qa", "tenant": ""}); err == nil {
		t.Fatal("empty value must be unbound")
	}
	partial := tpl.RenderPartial(map[string]string{"env": "qa"})
	if partial.String() != "qa-{tenant}-events" || !reflect.DeepEqual(partial.Names(), []string{"tenant"}) {
		t.Fatalf("RenderPartial: %q %v", partial.String(), partial.Names())
	}
	static := Template{}
	if s, err := static.Render(nil); err != nil || s != "" {
		t.Fatalf("zero template renders empty: %q %v", s, err)
	}
}

func mustSpec(t *testing.T, name, source, path, pattern string, values []string, def string) Spec {
	t.Helper()
	var re *regexp.Regexp
	if pattern != "" {
		re = regexp.MustCompile(pattern)
	}
	if path == "" {
		path = name
	}
	return Spec{Name: name, Source: Source(source), Path: path, Values: values, Pattern: re, Default: def}
}

func TestSpecAccepts(t *testing.T) {
	envs := []string{"tenant-a", "staging", "prod"}
	long := strings.Repeat("a", MaxValueLen+1)
	cases := []struct {
		name string
		spec Spec
		in   string
		want bool
	}{
		{"enumerated hit", mustSpec(t, "e", "header", "X-Env", "", envs, ""), "staging", true},
		{"enumerated miss", mustSpec(t, "e", "header", "X-Env", "", envs, ""), "other", false},
		{"enumerated is case sensitive", mustSpec(t, "e", "header", "X-Env", "", []string{"Staging"}, ""), "staging", false},
		{"pattern hit", mustSpec(t, "e", "body", "n.count", "^[0-9]+$", nil, ""), "3", true},
		{"pattern miss", mustSpec(t, "e", "body", "n.count", "^[0-9]+$", nil, ""), "three", false},
		{"empty value never accepted", mustSpec(t, "e", "header", "X-Env", "^.*$", nil, ""), "", false},
		{"over-long value rejected even when the pattern matches", mustSpec(t, "l", "header", "X-L", "^a+$", nil, ""), long, false},
		{"open-set lookup accepts any value", Spec{Name: "e", Source: SourceLookup}, "anything", true},
		{"closed spec accepts nothing", Spec{Name: "e", Source: SourceHeader}, "anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.Accepts(tc.in); got != tc.want {
				t.Fatalf("Accepts(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	if !mustSpec(t, "e", "header", "X-Env", "", envs, "").Enumerated() {
		t.Fatal("a spec with values is enumerated")
	}
	if mustSpec(t, "e", "header", "X-Env", "^.+$", nil, "").Enumerated() {
		t.Fatal("a pattern spec is not enumerated")
	}
}

func TestPathPattern(t *testing.T) {
	p, err := ParsePath("/webhooks/{envName}/vendor")
	if err != nil {
		t.Fatal(err)
	}
	if p.IsLiteral() || !reflect.DeepEqual(p.Names(), []string{"envName"}) {
		t.Fatalf("names: %v literal=%v", p.Names(), p.IsLiteral())
	}
	if vars, ok := p.Match("/webhooks/tenant-a/vendor"); !ok || vars["envName"] != "tenant-a" {
		t.Fatalf("match: %v %v", vars, ok)
	}
	for _, bad := range []string{"/webhooks/tenant-a/vendor/", "/webhooks//vendor", "/webhooks/tenant-a/other", "/webhooks/tenant-a", "/webhooks/a/b/vendor"} {
		if _, ok := p.Match(bad); ok {
			t.Fatalf("%q should not match", bad)
		}
	}
	lit, err := ParsePath("/webhooks/orders")
	if err != nil || !lit.IsLiteral() {
		t.Fatalf("literal: %v %v", lit, err)
	}
	if _, ok := lit.Match("/webhooks/orders"); !ok {
		t.Fatal("literal exact match failed")
	}
	for _, bad := range []string{"webhooks/x", "/webhooks/a{env}/x", "/webhooks/{env}/{env}", "/webhooks/{}/x", "/webhooks/{9}/x", "/a//b"} {
		if _, err := ParsePath(bad); err == nil {
			t.Fatalf("%q should fail to parse", bad)
		}
	}
}

func TestPathPatternGreedyRest(t *testing.T) {
	p, err := ParsePath("/proxy/{rest...}")
	if err != nil {
		t.Fatal(err)
	}
	if p.IsLiteral() || !reflect.DeepEqual(p.Names(), []string{"rest"}) {
		t.Fatalf("names: %v literal=%v", p.Names(), p.IsLiteral())
	}
	vars, ok := p.Match("/proxy/v1/orders/123")
	if !ok || vars["rest"] != "v1/orders/123" {
		t.Fatalf("greedy match: %v %v", vars, ok)
	}
	if _, ok := p.Match("/proxy"); ok {
		t.Fatal("/proxy should not match a greedy remainder that requires a capture")
	}
	if _, err := ParsePath("/proxy/{rest...}/tail"); err == nil {
		t.Fatal("greedy placeholder must be the final segment")
	}
}

func TestPathOverlap(t *testing.T) {
	mk := func(s string) PathPattern {
		p, err := ParsePath(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/c", false},
		{"/a/{x}", "/a/b", true},
		{"/a/{x}", "/a/{y}", true},
		{"/a/{x}/c", "/a/b/d", false},
		{"/a/{x}", "/a/{x}/c", true}, // parent/child
		{"/a/b", "/a/b/c", true},     // existing parent/child rule
		{"/a/{x}/c", "/a/b", true},   // child under placeholder parent
		{"/a/b/c", "/a/{x}/d", false},
	}
	for _, tc := range cases {
		if got := Overlap(mk(tc.a), mk(tc.b)); got != tc.want {
			t.Errorf("Overlap(%q,%q)=%v want %v", tc.a, tc.b, got, tc.want)
		}
		if got := Overlap(mk(tc.b), mk(tc.a)); got != tc.want {
			t.Errorf("Overlap(%q,%q)=%v want %v (symmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestMatcher(t *testing.T) {
	m, err := CompileMatcher("{env}-orders-{tenant}.fifo", map[string]string{"env": "^tenant-[a-z0-9]+$", "tenant": "^[a-z]+$"})
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsTemplate() || m.Raw() != "{env}-orders-{tenant}.fifo" {
		t.Fatalf("matcher meta: %v %q", m.IsTemplate(), m.Raw())
	}
	for _, ok := range []string{"tenant-a-orders-acme.fifo", "tenant-12-orders-z.fifo"} {
		if !m.Match(ok) {
			t.Errorf("%q should match", ok)
		}
	}
	for _, bad := range []string{"tenant-a-orders-acme", "tenant-A-orders-acme.fifo", "tenant-a-orders-ACME.fifo", "xtenant-a-orders-acme.fifo", "tenant-a-orders-acme.fifox"} {
		if m.Match(bad) {
			t.Errorf("%q should not match", bad)
		}
	}
	// Adjacent placeholders resolve deterministically.
	adj, err := CompileMatcher("{a}{b}", map[string]string{"a": "^[0-9]+$", "b": "^[a-z]+$"})
	if err != nil || !adj.Match("12ab") || adj.Match("ab12") {
		t.Fatalf("adjacent: %v", err)
	}
	// Anchors inside an alternation must not survive embedding: ^qa$|^prod$
	// passes validation (starts with ^, ends with $) and both branches match.
	alt, err := CompileMatcher("{env}-events", map[string]string{"env": "^qa$|^prod$"})
	if err != nil || !alt.Match("qa-events") || !alt.Match("prod-events") || alt.Match("dev-events") || alt.Match("qa-eventsx") {
		t.Fatalf("alternation anchors: err=%v qa=%v prod=%v", err, alt.Match("qa-events"), alt.Match("prod-events"))
	}
	// Missing pattern for a placeholder is an error; zero Matcher is static.
	if _, err := CompileMatcher("{env}-x", nil); err == nil {
		t.Fatal("expected error for missing pattern source")
	}
	var static Matcher
	if static.IsTemplate() || static.Match("anything") {
		t.Fatal("zero matcher must be static and match nothing")
	}
}

func TestBodyString(t *testing.T) {
	var body any
	_ = json.Unmarshal([]byte(`{"a":{"b":"c","n":1.5,"z":null}}`), &body)
	if v, ok := BodyString(body, "a.b"); !ok || v != "c" {
		t.Fatalf("a.b: %q %v", v, ok)
	}
	if v, ok := BodyString(body, "a.n"); !ok || v != "1.5" {
		t.Fatalf("a.n: %q %v", v, ok)
	}
	if _, ok := BodyString(body, "a.z"); ok {
		t.Fatal("null must not resolve")
	}
	if _, ok := BodyString(body, ""); ok {
		t.Fatal("empty path must not resolve")
	}
	if _, ok := BodyString("string body", "a"); ok {
		t.Fatal("non-object body must not resolve")
	}
}
