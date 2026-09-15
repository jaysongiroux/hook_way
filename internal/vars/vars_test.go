package vars

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
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

func TestResolve(t *testing.T) {
	var body any
	if err := json.Unmarshal([]byte(`{"env":"qa-gm3","n":{"count":3,"ok":true},"list":[1]}`), &body); err != nil {
		t.Fatal(err)
	}
	in := Input{
		Headers:  http.Header{"X-Env": {"  staging-portal "}},
		Body:     body,
		Query:    url.Values{"env": {"portal", "ignored"}},
		PathVars: map[string]string{"envName": "qa-gm4"},
	}
	envs := []string{"qa-gm3", "staging-portal", "portal"}
	cases := []struct {
		name       string
		spec       Spec
		want       string
		unresolved bool
	}{
		{"header trimmed", mustSpec(t, "e", "header", "X-Env", "", envs, ""), "staging-portal", false},
		{"header missing", mustSpec(t, "e", "header", "X-Nope", "", envs, ""), "", true},
		{"body string", mustSpec(t, "e", "body", "env", "", envs, ""), "qa-gm3", false},
		{"body number", mustSpec(t, "e", "body", "n.count", "^[0-9]+$", nil, ""), "3", false},
		{"body bool", mustSpec(t, "e", "body", "n.ok", "^(true|false)$", nil, ""), "true", false},
		{"body array unsupported", mustSpec(t, "e", "body", "list", "^.+$", nil, ""), "", true},
		{"body missing", mustSpec(t, "e", "body", "nope", "^.+$", nil, ""), "", true},
		{"query first value", mustSpec(t, "e", "query", "env", "", envs, ""), "portal", false},
		{"query missing", mustSpec(t, "e", "query", "zzz", "", envs, ""), "", true},
		{"path", mustSpec(t, "envName", "path", "", "^qa-gm[0-9]+$", nil, ""), "qa-gm4", false},
		{"path not accepted by values", mustSpec(t, "envName", "path", "", "", envs, ""), "", true},
		{"default on missing", mustSpec(t, "e", "header", "X-Nope", "", envs, "portal"), "portal", false},
		{"default on mismatch", mustSpec(t, "e", "query", "env", "^qa-.*$", nil, "qa-gm1"), "qa-gm1", false},
		{"enumerated is case sensitive", mustSpec(t, "e", "header", "X-Env", "", []string{"Staging-Portal"}, ""), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Resolve(context.Background(), []Spec{tc.spec}, in)
			if tc.unresolved {
				if !reflect.DeepEqual(res.Unresolved, []string{tc.spec.Name}) || res.Values[tc.spec.Name] != "" {
					t.Fatalf("expected unresolved, got %#v", res)
				}
				return
			}
			if len(res.Unresolved) != 0 || res.Values[tc.spec.Name] != tc.want {
				t.Fatalf("got %#v want %q", res, tc.want)
			}
		})
	}
	// Non-JSON body: body source is unresolved, others still work.
	res := Resolve(context.Background(), []Spec{mustSpec(t, "b", "body", "env", "^.+$", nil, ""), mustSpec(t, "q", "query", "env", "", envs, "")}, Input{Body: nil, Query: in.Query})
	if !reflect.DeepEqual(res.Unresolved, []string{"b"}) || res.Values["q"] != "portal" {
		t.Fatalf("nil body: %#v", res)
	}
	// Unresolved list is sorted.
	res = Resolve(context.Background(), []Spec{mustSpec(t, "zeta", "header", "X-1", "^.+$", nil, ""), mustSpec(t, "alpha", "header", "X-2", "^.+$", nil, "")}, Input{})
	if !reflect.DeepEqual(res.Unresolved, []string{"alpha", "zeta"}) {
		t.Fatalf("sorted: %v", res.Unresolved)
	}
	// Over-long values are rejected even if the pattern matches.
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	res = Resolve(context.Background(), []Spec{mustSpec(t, "l", "header", "X-L", "^a+$", nil, "")}, Input{Headers: http.Header{"X-L": {string(long)}}})
	if len(res.Unresolved) != 1 {
		t.Fatalf("expected length cap rejection, got %#v", res)
	}
}

func TestPathPattern(t *testing.T) {
	p, err := ParsePath("/webhooks/{envName}/digifi")
	if err != nil {
		t.Fatal(err)
	}
	if p.IsLiteral() || !reflect.DeepEqual(p.Names(), []string{"envName"}) {
		t.Fatalf("names: %v literal=%v", p.Names(), p.IsLiteral())
	}
	if vars, ok := p.Match("/webhooks/qa-gm3/digifi"); !ok || vars["envName"] != "qa-gm3" {
		t.Fatalf("match: %v %v", vars, ok)
	}
	for _, bad := range []string{"/webhooks/qa-gm3/digifi/", "/webhooks//digifi", "/webhooks/qa-gm3/other", "/webhooks/qa-gm3", "/webhooks/a/b/digifi"} {
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
	m, err := CompileMatcher("{env}-orders-{tenant}.fifo", map[string]string{"env": "^qa-gm[0-9]+$", "tenant": "^[a-z]+$"})
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsTemplate() || m.Raw() != "{env}-orders-{tenant}.fifo" {
		t.Fatalf("matcher meta: %v %q", m.IsTemplate(), m.Raw())
	}
	for _, ok := range []string{"qa-gm3-orders-acme.fifo", "qa-gm12-orders-z.fifo"} {
		if !m.Match(ok) {
			t.Errorf("%q should match", ok)
		}
	}
	for _, bad := range []string{"qa-gm3-orders-acme", "qa-gmX-orders-acme.fifo", "qa-gm3-orders-ACME.fifo", "xqa-gm3-orders-acme.fifo", "qa-gm3-orders-acme.fifox"} {
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

type fakeLookup struct {
	values map[string]string
	err    error
	calls  int
	last   string
}

func (f *fakeLookup) Lookup(_ context.Context, key string) (string, bool, error) {
	f.calls++
	f.last = key
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.values[key]
	return v, ok, nil
}

func TestResolveLookupSource(t *testing.T) {
	var body any
	_ = json.Unmarshal([]byte(`{"data":{"object":{"applicationId":"abc123"}}}`), &body)
	in := Input{Body: body, Headers: http.Header{"X-Id": {"  hdr-9 "}}}
	provider := &fakeLookup{values: map[string]string{"abc123": " qa-gm3 ", "hdr-9": "portal"}}
	spec := Spec{Name: "env", Source: SourceLookup, KeySource: SourceBody, KeyPath: "data.object.applicationId", Lookup: provider}

	res := Resolve(context.Background(), []Spec{spec}, in)
	if res.Values["env"] != "qa-gm3" || len(res.Unresolved) != 0 || len(res.Errors) != 0 || provider.last != "abc123" {
		t.Fatalf("hit: %#v last=%q", res, provider.last)
	}
	// Header-sourced key is trimmed before the call.
	hdr := spec
	hdr.KeySource, hdr.KeyPath = SourceHeader, "X-Id"
	if res := Resolve(context.Background(), []Spec{hdr}, in); res.Values["env"] != "portal" || provider.last != "hdr-9" {
		t.Fatalf("header key: %#v last=%q", res, provider.last)
	}
	// Miss -> unresolved, no error recorded.
	miss := &fakeLookup{values: map[string]string{}}
	m := spec
	m.Lookup = miss
	if res := Resolve(context.Background(), []Spec{m}, in); len(res.Unresolved) != 1 || len(res.Errors) != 0 {
		t.Fatalf("miss: %#v", res)
	}
	// Miss with default -> default.
	m.Default = "portal"
	if res := Resolve(context.Background(), []Spec{m}, in); res.Values["env"] != "portal" || len(res.Unresolved) != 0 {
		t.Fatalf("miss default: %#v", res)
	}
	// Provider error -> unresolved plus Errors entry.
	e := spec
	e.Lookup = &fakeLookup{err: errors.New("throttled")}
	if res := Resolve(context.Background(), []Spec{e}, in); len(res.Unresolved) != 1 || res.Errors["env"] == nil {
		t.Fatalf("error: %#v", res)
	}
	// Empty key skips the provider entirely.
	calls := provider.calls
	empty := spec
	empty.KeyPath = "data.missing"
	if res := Resolve(context.Background(), []Spec{empty}, in); len(res.Unresolved) != 1 || provider.calls != calls {
		t.Fatalf("empty key must not call provider: %#v calls=%d", res, provider.calls)
	}
	// Looked-up value must still pass values/pattern.
	strict := spec
	strict.Values = []string{"portal"}
	if res := Resolve(context.Background(), []Spec{strict}, in); len(res.Unresolved) != 1 {
		t.Fatalf("value rejected by values: %#v", res)
	}
	// No provider wired is an error, not a panic.
	np := spec
	np.Lookup = nil
	if res := Resolve(context.Background(), []Spec{np}, in); res.Errors["env"] == nil {
		t.Fatalf("nil provider: %#v", res)
	}
}
