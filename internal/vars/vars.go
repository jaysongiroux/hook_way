// Package vars implements per-request routing variables: {name} placeholder
// templates for route targets and webhook URLs, per-variable resolution from a
// request (header, body, query, path segment), endpoint path patterns, and a
// matcher that maps a rendered target back to its template.
//
// The package is stdlib-only on purpose: it is imported by internal/config,
// which is compiled into the browser WASM validator, and by internal/queue and
// internal/httpapi, so it must not depend on either of those.
package vars

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
)

// nameRE constrains placeholder / variable names. Single braces are used so
// placeholders are invisible to ${VAR} environment expansion at config load.
var nameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

// MaxValueLen caps any resolved variable value; it matches the target name
// limit so a single variable can never produce an over-long target.
const MaxValueLen = 128

// ValidName reports whether s is an acceptable variable / placeholder name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// part is one literal chunk or one placeholder of a Template.
type part struct {
	lit  string
	name string // non-empty for a placeholder
}

// Template is a string with {name} placeholders. The zero value is an empty
// literal template.
type Template struct {
	parts []part
}

// ErrUnbound is returned by Render when a placeholder has no non-empty value.
type ErrUnbound struct{ Name string }

func (e ErrUnbound) Error() string { return fmt.Sprintf("variable %q is unbound", e.Name) }

// Parse splits s into literal and placeholder parts. Braces must be balanced,
// non-nested, and every placeholder must be a valid name.
func Parse(s string) (Template, error) {
	var t Template
	var lit strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '$':
			// "${NAME}" is environment-expansion syntax (already expanded at
			// config load, or intentionally left literal by the lenient UI
			// validator). It is never a variable placeholder, so copy it through.
			if i+1 < len(s) && s[i+1] == '{' {
				end := strings.IndexByte(s[i+1:], '}')
				if end < 0 {
					return Template{}, fmt.Errorf("unclosed '${' at offset %d", i)
				}
				lit.WriteString(s[i : i+end+2])
				i += end + 2
				continue
			}
			lit.WriteByte(c)
			i++
		case '{':
			end := strings.IndexByte(s[i+1:], '}')
			if end < 0 {
				return Template{}, fmt.Errorf("unclosed '{' at offset %d", i)
			}
			name := s[i+1 : i+1+end]
			if strings.ContainsRune(name, '{') {
				return Template{}, fmt.Errorf("nested '{' in placeholder at offset %d", i)
			}
			if !ValidName(name) {
				return Template{}, fmt.Errorf("invalid placeholder name %q (use letters, digits, underscore; must not start with a digit)", name)
			}
			if lit.Len() > 0 {
				t.parts = append(t.parts, part{lit: lit.String()})
				lit.Reset()
			}
			t.parts = append(t.parts, part{name: name})
			i += end + 2
		case '}':
			return Template{}, fmt.Errorf("unmatched '}' at offset %d", i)
		default:
			lit.WriteByte(c)
			i++
		}
	}
	if lit.Len() > 0 {
		t.parts = append(t.parts, part{lit: lit.String()})
	}
	return t, nil
}

// Names returns the distinct placeholder names in first-appearance order.
func (t Template) Names() []string {
	var names []string
	seen := map[string]struct{}{}
	for _, p := range t.parts {
		if p.name == "" {
			continue
		}
		if _, ok := seen[p.name]; ok {
			continue
		}
		seen[p.name] = struct{}{}
		names = append(names, p.name)
	}
	return names
}

// HasPlaceholders reports whether the template contains any {name}.
func (t Template) HasPlaceholders() bool {
	for _, p := range t.parts {
		if p.name != "" {
			return true
		}
	}
	return false
}

// Literal returns the concatenated literal parts (everything except
// placeholders), used for charset checks of the authored template.
func (t Template) Literal() string {
	var b strings.Builder
	for _, p := range t.parts {
		b.WriteString(p.lit)
	}
	return b.String()
}

// LiteralSuffix returns the trailing literal part, or "" when the template
// ends with a placeholder.
func (t Template) LiteralSuffix() string {
	if len(t.parts) == 0 {
		return ""
	}
	return t.parts[len(t.parts)-1].lit
}

// String re-serialises the template in its source form.
func (t Template) String() string {
	var b strings.Builder
	for _, p := range t.parts {
		if p.name != "" {
			b.WriteString("{" + p.name + "}")
		} else {
			b.WriteString(p.lit)
		}
	}
	return b.String()
}

// RenderPartial substitutes the placeholders present in values (non-empty)
// and keeps the rest, returning a new Template. Used for load-time expansion
// of enumerated variables.
func (t Template) RenderPartial(values map[string]string) Template {
	var out Template
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out.parts = append(out.parts, part{lit: lit.String()})
			lit.Reset()
		}
	}
	for _, p := range t.parts {
		if p.name == "" {
			lit.WriteString(p.lit)
			continue
		}
		if v, ok := values[p.name]; ok && v != "" {
			lit.WriteString(v)
			continue
		}
		flush()
		out.parts = append(out.parts, p)
	}
	flush()
	return out
}

// Render substitutes every placeholder; a missing or empty value is an
// ErrUnbound error.
func (t Template) Render(values map[string]string) (string, error) {
	var b strings.Builder
	for _, p := range t.parts {
		if p.name == "" {
			b.WriteString(p.lit)
			continue
		}
		v := values[p.name]
		if v == "" {
			return "", ErrUnbound{Name: p.name}
		}
		b.WriteString(v)
	}
	return b.String(), nil
}

// Source names where a variable's value is read from.
type Source string

const (
	SourceHeader Source = "header"
	SourceBody   Source = "body"
	SourceQuery  Source = "query"
	SourcePath   Source = "path"
	// SourceLookup resolves the value through a Lookup provider keyed by a
	// value extracted from the request (KeySource/KeyPath).
	SourceLookup Source = "lookup"
)

// MaxLookupKeyLen caps the key handed to a Lookup; longer keys are treated as
// missing without calling the provider.
const MaxLookupKeyLen = 256

// Lookup resolves a request-derived key to a value through an external
// provider (DynamoDB, HTTP, Postgres; see internal/lookup). found=false with a
// nil error is a clean miss.
type Lookup interface {
	Lookup(ctx context.Context, key string) (value string, found bool, err error)
}

// Spec is a compiled variable declaration. Exactly one of Values or Pattern is
// set (config validation enforces this). Path is the header name, dotted body
// path, query key, or endpoint path segment name.
type Spec struct {
	Name    string
	Source  Source
	Path    string
	Values  []string
	Pattern *regexp.Regexp
	Default string
	// Lookup-source fields: where the key comes from and the provider to ask.
	KeySource Source
	KeyPath   string
	Lookup    Lookup
}

// Enumerated reports whether the variable has a fixed value set.
func (s Spec) Enumerated() bool { return len(s.Values) > 0 }

// Accepts reports whether v is a permitted value for the variable.
func (s Spec) Accepts(v string) bool {
	if v == "" || len(v) > MaxValueLen {
		return false
	}
	if s.Enumerated() {
		for _, allowed := range s.Values {
			if v == allowed {
				return true
			}
		}
		return false
	}
	if s.Pattern != nil {
		return s.Pattern.MatchString(v)
	}
	// A lookup variable may declare neither values nor pattern: the provider
	// is trusted and the rendered target is still checked for a safe name.
	return s.Source == SourceLookup
}

// Input is what a request offers to Resolve. Headers should be the unredacted
// request headers; Body the JSON-decoded body (nil when not JSON); Query the
// parsed query string (first value wins); PathVars the captures from
// PathPattern.Match.
type Input struct {
	Headers  http.Header
	Body     any
	Query    url.Values
	PathVars map[string]string
}

// Result is the outcome of Resolve: every resolved (or defaulted) value, and
// the sorted names that stayed unresolved even after Default.
type Result struct {
	Values     map[string]string
	Unresolved []string
	// Errors holds provider failures for lookup variables (by name). Such a
	// variable is also listed in Unresolved unless Default applied; callers
	// log and count the error separately from a clean miss.
	Errors map[string]error
}

// Resolve evaluates every spec against in. A raw value is trimmed, then must
// be accepted by the spec; otherwise Default applies when set, else the name
// is reported unresolved. Lookup specs extract their key from the request and
// call the provider; a provider error or a missing key behaves like a miss.
func Resolve(ctx context.Context, specs []Spec, in Input) Result {
	res := Result{Values: make(map[string]string, len(specs))}
	for _, s := range specs {
		var raw string
		if s.Source == SourceLookup {
			var err error
			raw, err = resolveLookup(ctx, s, in)
			if err != nil {
				if res.Errors == nil {
					res.Errors = map[string]error{}
				}
				res.Errors[s.Name] = err
			}
		} else {
			raw = strings.TrimSpace(readSource(in, s.Source, s.Path))
		}
		switch {
		case s.Accepts(raw):
			res.Values[s.Name] = raw
		case s.Default != "":
			res.Values[s.Name] = s.Default
		default:
			res.Unresolved = append(res.Unresolved, s.Name)
		}
	}
	sort.Strings(res.Unresolved)
	return res
}

// resolveLookup extracts the key and asks the provider. An empty or over-long
// key skips the call and reads as a miss.
func resolveLookup(ctx context.Context, s Spec, in Input) (string, error) {
	key := strings.TrimSpace(readSource(in, s.KeySource, s.KeyPath))
	if key == "" || len(key) > MaxLookupKeyLen {
		return "", nil
	}
	if s.Lookup == nil {
		return "", errors.New("no lookup provider configured")
	}
	value, found, err := s.Lookup.Lookup(ctx, key)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return strings.TrimSpace(value), nil
}

// readSource reads a raw value from the request for a non-lookup source.
func readSource(in Input, source Source, path string) string {
	switch source {
	case SourceHeader:
		if in.Headers == nil {
			return ""
		}
		return in.Headers.Get(path)
	case SourceBody:
		v, _ := BodyString(in.Body, path)
		return v
	case SourceQuery:
		if in.Query == nil {
			return ""
		}
		return in.Query.Get(path)
	case SourcePath:
		return in.PathVars[path]
	}
	return ""
}

// BodyString walks a dotted path into a JSON-decoded body and returns the leaf
// as a string when it is a string, bool, or number. Arrays, objects, null and
// missing keys yield ok=false. Array indexing is not supported.
func BodyString(body any, dotted string) (string, bool) {
	if dotted == "" {
		return "", false
	}
	current := body
	for _, segment := range strings.Split(dotted, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = m[segment]
		if !ok {
			return "", false
		}
	}
	switch v := current.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case json.Number:
		return v.String(), true
	default:
		return "", false
	}
}

// seg is one segment of a PathPattern: a literal or a {name} capture.
type seg struct {
	lit  string
	name string
}

// PathPattern is an endpoint path whose segments may be {name} placeholders.
// Matching is exact on segment count; a placeholder captures exactly one
// non-empty segment. No trailing-slash normalisation is applied, matching the
// exact-path behaviour hookway had before variables.
type PathPattern struct {
	raw  string
	segs []seg
}

// ParsePath parses p. It must start with "/", contain no empty segments, and
// any placeholder must be an entire segment with a unique valid name.
func ParsePath(p string) (PathPattern, error) {
	if p == "" || p[0] != '/' {
		return PathPattern{}, errors.New("path must start with /")
	}
	pp := PathPattern{raw: p}
	seen := map[string]struct{}{}
	for i, s := range strings.Split(p[1:], "/") {
		if s == "" {
			return PathPattern{}, fmt.Errorf("path segment %d is empty", i+1)
		}
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			name := s[1 : len(s)-1]
			if !ValidName(name) {
				return PathPattern{}, fmt.Errorf("invalid path placeholder %q", s)
			}
			if _, dup := seen[name]; dup {
				return PathPattern{}, fmt.Errorf("path placeholder {%s} appears more than once", name)
			}
			seen[name] = struct{}{}
			pp.segs = append(pp.segs, seg{name: name})
			continue
		}
		if strings.ContainsAny(s, "{}") {
			return PathPattern{}, fmt.Errorf("path segment %q mixes a placeholder with literal text; a placeholder must be a whole segment", s)
		}
		pp.segs = append(pp.segs, seg{lit: s})
	}
	return pp, nil
}

// String returns the authored path.
func (p PathPattern) String() string { return p.raw }

// Names returns the placeholder names in order.
func (p PathPattern) Names() []string {
	var names []string
	for _, s := range p.segs {
		if s.name != "" {
			names = append(names, s.name)
		}
	}
	return names
}

// IsLiteral reports whether the pattern has no placeholders.
func (p PathPattern) IsLiteral() bool { return len(p.Names()) == 0 }

// Match tests a request path and returns the captured segment values.
func (p PathPattern) Match(path string) (map[string]string, bool) {
	if path == "" || path[0] != '/' {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	if len(parts) != len(p.segs) {
		return nil, false
	}
	var captured map[string]string
	for i, s := range p.segs {
		if s.name == "" {
			if parts[i] != s.lit {
				return nil, false
			}
			continue
		}
		if parts[i] == "" {
			return nil, false
		}
		if captured == nil {
			captured = map[string]string{}
		}
		captured[s.name] = parts[i]
	}
	return captured, true
}

// Overlap reports whether a and b could both match a single request path, or
// one is a segment-prefix (parent) of the other. Two literal segments overlap
// only when equal; a placeholder overlaps any non-empty segment.
func Overlap(a, b PathPattern) bool {
	n := min(len(a.segs), len(b.segs))
	for i := 0; i < n; i++ {
		sa, sb := a.segs[i], b.segs[i]
		if sa.name != "" || sb.name != "" {
			continue
		}
		if sa.lit != sb.lit {
			return false
		}
	}
	return true
}

// Matcher maps a rendered target back to the template it came from. It is
// built from the template and each remaining placeholder's regex source; the
// zero value is a static (non-template) matcher that matches nothing.
type Matcher struct {
	raw string
	re  *regexp.Regexp
}

// CompileMatcher builds an anchored regexp from raw: literal parts are quoted
// and each placeholder becomes a named group wrapping its pattern source
// (with its ^/$ anchors stripped so anchored user patterns compose). Every
// placeholder must have a pattern.
func CompileMatcher(raw string, patterns map[string]string) (Matcher, error) {
	tpl, err := Parse(raw)
	if err != nil {
		return Matcher{}, err
	}
	var b strings.Builder
	b.WriteString("^")
	for _, p := range tpl.parts {
		if p.name == "" {
			b.WriteString(regexp.QuoteMeta(p.lit))
			continue
		}
		src, ok := patterns[p.name]
		if !ok || src == "" {
			return Matcher{}, fmt.Errorf("no pattern for placeholder {%s}", p.name)
		}
		src, err := stripAnchors(src)
		if err != nil {
			return Matcher{}, fmt.Errorf("pattern for placeholder {%s}: %w", p.name, err)
		}
		b.WriteString("(?P<" + p.name + ">(?:" + src + "))")
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return Matcher{}, fmt.Errorf("compile template %q: %w", raw, err)
	}
	return Matcher{raw: raw, re: re}, nil
}

// stripAnchors removes every text/line anchor (^ and $) from src so an
// anchored user pattern can be embedded mid-string. Only the outermost pair
// used to be trimmed, which left inner anchors in alternations such as
// ^qa$|^prod$ that can never match once a literal precedes them.
func stripAnchors(src string) (string, error) {
	re, err := syntax.Parse(src, syntax.Perl)
	if err != nil {
		return "", err
	}
	var walk func(*syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		switch r.Op {
		case syntax.OpBeginText, syntax.OpEndText, syntax.OpBeginLine, syntax.OpEndLine:
			r.Op = syntax.OpEmptyMatch
			r.Sub = nil
		}
		for _, sub := range r.Sub {
			walk(sub)
		}
	}
	walk(re)
	return re.String(), nil
}

// IsTemplate reports whether the matcher was compiled from a template.
func (m Matcher) IsTemplate() bool { return m.re != nil }

// Raw returns the authored template, or "" for the zero matcher.
func (m Matcher) Raw() string { return m.raw }

// Match reports whether rendered was produced from this template with values
// permitted by the placeholder patterns.
func (m Matcher) Match(rendered string) bool {
	return m.re != nil && m.re.MatchString(rendered)
}
