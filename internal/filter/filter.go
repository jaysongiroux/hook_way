// Package filter evaluates chains of conditions against a webhook's headers
// and parsed body — JSON objects by dotted key, XML documents by dotted element
// name with "@" for attributes. A chain combines its conditions with AND
// (the default) or OR. Chains are compiled once (regex included) from config
// and evaluated per request, independent of the httpapi and queue packages so
// both endpoint- and route-level filtering can share them.
package filter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/transform"
)

// Source values.
const (
	SourceHeader = "header"
	SourceBody   = "body"
	SourceFlags  = "flags"
)

// Operators.
const (
	OpEq        = "eq"
	OpNe        = "ne"
	OpGt        = "gt"
	OpGte       = "gte"
	OpLt        = "lt"
	OpLte       = "lte"
	OpContains  = "contains"
	OpPrefix    = "prefix"
	OpSuffix    = "suffix"
	OpRegex     = "regex"
	OpExists    = "exists"
	OpNotExists = "not_exists"
)

// Spec is the plain, uncompiled form of one condition, mirroring the config YAML.
type Spec struct {
	Source   string // "header" | "body"
	Path     string // header name, or dotted body path e.g. "repository.private"
	Operator string
	Value    any // raw YAML scalar: string | int | float64 | bool | nil
}

// Issue is a compile/validation problem with one spec, addressable back to a
// config field so callers can map it into their own validation errors.
type Issue struct {
	Index      int    // index into the specs slice
	Field      string // "source" | "path" | "operator" | "value"
	Message    string
	Suggestion string
}

// kind is the compiled comparison mode for a condition.
type kind int

const (
	kindString kind = iota
	kindNumber
	kindBool
	kindRegex
	kindExists
	kindNotExists
)

type condition struct {
	source   string
	path     string
	operator string
	kind     kind

	strVal  string
	numVal  float64
	boolVal bool
	re      *regexp.Regexp
}

// Mode is the boolean combinator over a chain's flat condition list. One level
// only: there are no nested groups.
type Mode int

const (
	// MatchAll requires every condition. It is the zero value, so a Chain
	// built without an explicit mode behaves as it always has.
	MatchAll Mode = iota
	// MatchAny requires at least one condition.
	MatchAny
)

// Chain is a compiled chain of conditions combined per its Mode. The zero value
// is an empty chain that matches everything.
type Chain struct {
	conds        []condition
	mode         Mode
	matchNothing bool
}

// operatorList is the canonical set surfaced in validation suggestions.
const operatorList = "use eq, ne, gt, gte, lt, lte, contains, prefix, suffix, regex, exists, or not_exists"

// Compile validates and compiles an AND-chain.
func Compile(specs []Spec) (Chain, []Issue) { return CompileMode(specs, MatchAll) }

// CompileMode validates and compiles a chain under the given combinator. Regex
// values are compiled here, once. On any problem it returns a nil-safe zero
// Chain plus one Issue per bad spec; a non-empty Issue slice means the chain
// must not be used.
func CompileMode(specs []Spec, mode Mode) (Chain, []Issue) {
	var issues []Issue
	conds := make([]condition, 0, len(specs))
	for i, spec := range specs {
		cond, specIssues := compileOne(i, spec)
		if len(specIssues) > 0 {
			issues = append(issues, specIssues...)
			continue
		}
		conds = append(conds, cond)
	}
	if len(issues) > 0 {
		return Chain{}, issues
	}
	return Chain{conds: conds, mode: mode}, nil
}

// MatchNothing returns a chain that rejects every input. Used as a fail-closed
// fallback when a chain unexpectedly fails to compile at runtime.
func MatchNothing() Chain {
	return Chain{matchNothing: true}
}

// Empty reports whether the chain has no conditions (matches everything). A
// fail-closed MatchNothing chain is not empty.
func (c Chain) Empty() bool { return !c.matchNothing && len(c.conds) == 0 }

// Match evaluates the chain under its mode. The second return is the index of
// the condition that decided the outcome, or -1 when no single condition did:
// under MatchAll that is the first failing condition on a false result, and
// under MatchAny the first satisfying condition on a true result. An empty
// chain matches under either mode.
func (c Chain) Match(headers http.Header, body any) (bool, int) {
	return c.MatchWithFlags(headers, body, nil)
}

// MatchWithFlags adds typed metadata without changing legacy body/header matching.
func (c Chain) MatchWithFlags(headers http.Header, body any, flags map[string]any) (bool, int) {
	if c.matchNothing {
		return false, 0
	}
	if c.mode == MatchAny && len(c.conds) > 0 {
		for i, cond := range c.conds {
			if cond.eval(headers, body, flags) {
				return true, i
			}
		}
		return false, -1
	}
	for i, cond := range c.conds {
		if !cond.eval(headers, body, flags) {
			return false, i
		}
	}
	return true, -1
}

// Describe returns the source, path, and operator of a condition for log fields.
// Out-of-range indexes return empty strings.
func (c Chain) Describe(index int) (source, path, operator string) {
	if index < 0 || index >= len(c.conds) {
		return "", "", ""
	}
	cond := c.conds[index]
	return cond.source, cond.path, cond.operator
}

func compileOne(index int, spec Spec) (condition, []Issue) {
	cond := condition{source: spec.Source, path: spec.Path, operator: spec.Operator}

	switch spec.Source {
	case SourceHeader, SourceBody, SourceFlags:
	default:
		return condition{}, []Issue{{index, "source", fmt.Sprintf("unknown filter source %q", spec.Source), "use header, body or flags"}}
	}
	if strings.TrimSpace(spec.Path) == "" {
		return condition{}, []Issue{{index, "path", "filter path is required", "set path to a header name or dotted body path such as repository.private"}}
	}

	switch spec.Operator {
	case OpExists, OpNotExists:
		if spec.Value != nil {
			return condition{}, []Issue{{index, "value", fmt.Sprintf("operator %q does not take a value", spec.Operator), "remove value, or use eq/ne to compare"}}
		}
		if spec.Operator == OpExists {
			cond.kind = kindExists
		} else {
			cond.kind = kindNotExists
		}
		return cond, nil

	case OpEq, OpNe:
		return compileTyped(index, spec, cond)

	case OpGt, OpGte, OpLt, OpLte:
		num, ok := toFloat(spec.Value)
		if !ok {
			return condition{}, []Issue{{index, "value", fmt.Sprintf("operator %q requires a numeric value", spec.Operator), "set value to a number"}}
		}
		cond.kind = kindNumber
		cond.numVal = num
		return cond, nil

	case OpContains, OpPrefix, OpSuffix:
		str, ok := spec.Value.(string)
		if !ok {
			return condition{}, []Issue{{index, "value", fmt.Sprintf("operator %q requires a string value", spec.Operator), "set value to a string"}}
		}
		cond.kind = kindString
		cond.strVal = str
		return cond, nil

	case OpRegex:
		str, ok := spec.Value.(string)
		if !ok {
			return condition{}, []Issue{{index, "value", "operator \"regex\" requires a string value", "set value to a regular expression"}}
		}
		re, err := regexp.Compile(str)
		if err != nil {
			return condition{}, []Issue{{index, "value", fmt.Sprintf("invalid regex: %s", err.Error()), "fix the Go RE2 pattern; see https://pkg.go.dev/regexp/syntax"}}
		}
		cond.kind = kindRegex
		cond.re = re
		return cond, nil

	default:
		return condition{}, []Issue{{index, "operator", fmt.Sprintf("unknown filter operator %q", spec.Operator), operatorList}}
	}
}

// compileTyped picks the comparison kind for eq/ne from the config value's type.
func compileTyped(index int, spec Spec, cond condition) (condition, []Issue) {
	switch v := spec.Value.(type) {
	case string:
		cond.kind = kindString
		cond.strVal = v
	case bool:
		cond.kind = kindBool
		cond.boolVal = v
	case int, int64, float64, json.Number:
		num, _ := toFloat(spec.Value)
		cond.kind = kindNumber
		cond.numVal = num
	case nil:
		return condition{}, []Issue{{index, "value", fmt.Sprintf("operator %q requires a value", spec.Operator), "set value to a string, number, or boolean"}}
	default:
		return condition{}, []Issue{{index, "value", fmt.Sprintf("operator %q does not support values of this type", spec.Operator), "use a string, number, or boolean value"}}
	}
	return cond, nil
}

func (c condition) eval(headers http.Header, body any, flags map[string]any) bool {
	actual, found := resolve(c.source, c.path, headers, body)
	if c.source == SourceFlags {
		actual, found = walkBodyPath(flags, c.path)
	}

	switch c.kind {
	case kindExists:
		return found
	case kindNotExists:
		return !found
	}
	if !found {
		return false
	}

	switch c.kind {
	case kindString:
		s, ok := actual.(string)
		if !ok {
			return false
		}
		switch c.operator {
		case OpEq:
			return s == c.strVal
		case OpNe:
			return s != c.strVal
		case OpContains:
			return strings.Contains(s, c.strVal)
		case OpPrefix:
			return strings.HasPrefix(s, c.strVal)
		case OpSuffix:
			return strings.HasSuffix(s, c.strVal)
		}
		return false

	case kindNumber:
		n, ok := toFloat(actual)
		if !ok {
			return false
		}
		switch c.operator {
		case OpEq:
			return n == c.numVal
		case OpNe:
			return n != c.numVal
		case OpGt:
			return n > c.numVal
		case OpGte:
			return n >= c.numVal
		case OpLt:
			return n < c.numVal
		case OpLte:
			return n <= c.numVal
		}
		return false

	case kindBool:
		b, ok := toBool(actual)
		if !ok {
			return false
		}
		switch c.operator {
		case OpEq:
			return b == c.boolVal
		case OpNe:
			return b != c.boolVal
		}
		return false

	case kindRegex:
		s, ok := actual.(string)
		if !ok {
			return false
		}
		return c.re.MatchString(s)
	}
	return false
}

// resolve reads the value at source/path. found reports whether a value is
// present (JSON null counts as present).
func resolve(source, path string, headers http.Header, body any) (any, bool) {
	switch source {
	case SourceHeader:
		if headers == nil {
			return nil, false
		}
		if len(headers.Values(path)) == 0 {
			return nil, false
		}
		return headers.Get(path), true
	case SourceBody:
		// An XML envelope puts a transform.Document on the body, which
		// walkBodyPath cannot traverse: it asserts map[string]any and would
		// report every path as missing, silently sending every event down the
		// on_no_match path. Both shapes appear in practice — node_transform
		// and node_code accept a pointer as well as a value.
		switch xmlBody := body.(type) {
		case transform.Document:
			return walkXMLPath(xmlBody, path)
		case *transform.Document:
			if xmlBody == nil {
				return nil, false
			}
			return walkXMLPath(*xmlBody, path)
		}
		return walkBodyPath(body, path)
	}
	return nil, false
}

// walkBodyPath walks a dotted path (e.g. "customer.id") into a JSON-decoded body,
// returning the leaf value and whether it was found. Only nested maps are
// traversed; array indexing is not supported.
func walkBodyPath(body any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	current := body
	for segment := range strings.SplitSeq(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// toFloat coerces config values (int/float/json.Number) and actual body/header
// values (float64/json.Number/string) into a float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// toBool coerces actual body/header values into a bool. Body booleans compare
// directly; strings (always the case for headers) parse via strconv.ParseBool.
func toBool(v any) (bool, bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		parsed, err := strconv.ParseBool(b)
		return parsed, err == nil
	}
	return false, false
}
