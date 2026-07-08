package filter

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCompileIssues(t *testing.T) {
	cases := []struct {
		name      string
		spec      Spec
		wantField string
	}{
		{"unknown source", Spec{Source: "query", Path: "x", Operator: OpEq, Value: "y"}, "source"},
		{"empty path", Spec{Source: SourceBody, Path: "", Operator: OpEq, Value: "y"}, "path"},
		{"unknown operator", Spec{Source: SourceBody, Path: "x", Operator: "matches", Value: "y"}, "operator"},
		{"eq missing value", Spec{Source: SourceBody, Path: "x", Operator: OpEq}, "value"},
		{"gt non-numeric value", Spec{Source: SourceBody, Path: "x", Operator: OpGt, Value: "hi"}, "value"},
		{"gt bool value", Spec{Source: SourceBody, Path: "x", Operator: OpGt, Value: true}, "value"},
		{"prefix non-string value", Spec{Source: SourceBody, Path: "x", Operator: OpPrefix, Value: 5}, "value"},
		{"regex non-string value", Spec{Source: SourceBody, Path: "x", Operator: OpRegex, Value: 5}, "value"},
		{"invalid regex", Spec{Source: SourceBody, Path: "x", Operator: OpRegex, Value: "["}, "value"},
		{"exists with value", Spec{Source: SourceBody, Path: "x", Operator: OpExists, Value: "y"}, "value"},
		{"not_exists with value", Spec{Source: SourceBody, Path: "x", Operator: OpNotExists, Value: 1}, "value"},
		{"eq map value", Spec{Source: SourceBody, Path: "x", Operator: OpEq, Value: map[string]any{"a": 1}}, "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := Compile([]Spec{tc.spec})
			if len(issues) != 1 {
				t.Fatalf("want 1 issue, got %d: %+v", len(issues), issues)
			}
			if issues[0].Field != tc.wantField {
				t.Fatalf("want field %q, got %q (%s)", tc.wantField, issues[0].Field, issues[0].Message)
			}
			if issues[0].Index != 0 {
				t.Fatalf("want index 0, got %d", issues[0].Index)
			}
		})
	}
}

func TestCompileIssueIndex(t *testing.T) {
	_, issues := Compile([]Spec{
		{Source: SourceBody, Path: "a", Operator: OpExists},
		{Source: SourceBody, Path: "b", Operator: "bogus", Value: 1},
	})
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(issues))
	}
	if issues[0].Index != 1 {
		t.Fatalf("want index 1, got %d", issues[0].Index)
	}
}

func fixtureBody(t *testing.T) any {
	t.Helper()
	raw := `{
		"ref": "refs/heads/main",
		"repository": {"private": false, "size": 120, "name": "hookway"},
		"count": "42",
		"nul": null
	}`
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return v
}

func TestMatchOperators(t *testing.T) {
	headers := http.Header{}
	headers.Add("X-GitHub-Event", "push")
	headers.Add("X-Count", "42")
	headers.Add("X-Flag", "true")
	headers.Add("X-Multi", "first")
	headers.Add("X-Multi", "second")

	body := fixtureBody(t)

	cases := []struct {
		name string
		spec Spec
		want bool
	}{
		// header string
		{"header eq match", Spec{SourceHeader, "X-GitHub-Event", OpEq, "push"}, true},
		{"header eq case-insensitive name", Spec{SourceHeader, "x-github-event", OpEq, "push"}, true},
		{"header eq no match", Spec{SourceHeader, "X-GitHub-Event", OpEq, "pull"}, false},
		{"header ne match", Spec{SourceHeader, "X-GitHub-Event", OpNe, "pull"}, true},
		{"header contains", Spec{SourceHeader, "X-GitHub-Event", OpContains, "us"}, true},
		{"header prefix", Spec{SourceHeader, "X-GitHub-Event", OpPrefix, "pu"}, true},
		{"header suffix", Spec{SourceHeader, "X-GitHub-Event", OpSuffix, "sh"}, true},
		{"header multi first value wins", Spec{SourceHeader, "X-Multi", OpEq, "first"}, true},
		// header number (parsed from string)
		{"header number eq", Spec{SourceHeader, "X-Count", OpEq, 42}, true},
		{"header number gt", Spec{SourceHeader, "X-Count", OpGt, 40}, true},
		{"header number lt false", Spec{SourceHeader, "X-Count", OpLt, 40}, false},
		{"header non-numeric fails number op", Spec{SourceHeader, "X-GitHub-Event", OpGt, 1}, false},
		// header bool (parsed from string)
		{"header bool eq", Spec{SourceHeader, "X-Flag", OpEq, true}, true},
		{"header bool ne", Spec{SourceHeader, "X-Flag", OpNe, false}, true},
		// header regex
		{"header regex", Spec{SourceHeader, "X-GitHub-Event", OpRegex, "^pu.h$"}, true},
		// header exists
		{"header exists", Spec{SourceHeader, "X-GitHub-Event", OpExists, nil}, true},
		{"header not_exists", Spec{SourceHeader, "X-Absent", OpNotExists, nil}, true},
		{"header exists false", Spec{SourceHeader, "X-Absent", OpExists, nil}, false},

		// body string
		{"body eq", Spec{SourceBody, "ref", OpEq, "refs/heads/main"}, true},
		{"body prefix", Spec{SourceBody, "ref", OpPrefix, "refs/heads/"}, true},
		{"body regex anchored", Spec{SourceBody, "ref", OpRegex, `^refs/heads/\w+$`}, true},
		{"body nested string", Spec{SourceBody, "repository.name", OpEq, "hookway"}, true},
		// body number
		{"body number eq", Spec{SourceBody, "repository.size", OpEq, 120}, true},
		{"body number gte", Spec{SourceBody, "repository.size", OpGte, 120}, true},
		{"body number lt", Spec{SourceBody, "repository.size", OpLt, 200}, true},
		{"body string coerced to number", Spec{SourceBody, "count", OpEq, 42}, true},
		{"body number vs string config fails", Spec{SourceBody, "repository.size", OpEq, "120"}, false},
		// body bool
		{"body bool eq", Spec{SourceBody, "repository.private", OpEq, false}, true},
		{"body bool ne", Spec{SourceBody, "repository.private", OpNe, true}, true},
		// ne fail-closed
		{"body ne on missing fails", Spec{SourceBody, "absent", OpNe, "x"}, false},
		{"body ne on type mismatch fails", Spec{SourceBody, "repository.size", OpNe, "x"}, false},
		// exists / null
		{"body exists", Spec{SourceBody, "repository.private", OpExists, nil}, true},
		{"body exists on null passes", Spec{SourceBody, "nul", OpExists, nil}, true},
		{"body not_exists on missing", Spec{SourceBody, "missing", OpNotExists, nil}, true},
		{"body not_exists on null fails", Spec{SourceBody, "nul", OpNotExists, nil}, false},
		{"body eq on null fails", Spec{SourceBody, "nul", OpEq, "x"}, false},
		// missing nested intermediate
		{"body deep missing", Spec{SourceBody, "repository.owner.login", OpEq, "x"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain, issues := Compile([]Spec{tc.spec})
			if len(issues) != 0 {
				t.Fatalf("unexpected compile issues: %+v", issues)
			}
			got, _ := chain.Match(headers, body)
			if got != tc.want {
				t.Fatalf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchNilBody(t *testing.T) {
	chain, issues := Compile([]Spec{{SourceBody, "ref", OpEq, "x"}})
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if ok, _ := chain.Match(nil, nil); ok {
		t.Fatal("body eq on nil body should fail")
	}

	notExists, _ := Compile([]Spec{{SourceBody, "ref", OpNotExists, nil}})
	if ok, _ := notExists.Match(nil, nil); !ok {
		t.Fatal("not_exists on nil body should pass")
	}
}

func TestEmptyChainMatches(t *testing.T) {
	var chain Chain
	if !chain.Empty() {
		t.Fatal("zero chain should be empty")
	}
	if ok, _ := chain.Match(nil, nil); !ok {
		t.Fatal("empty chain should match everything")
	}
}

func TestMatchNothing(t *testing.T) {
	chain := MatchNothing()
	if chain.Empty() {
		t.Fatal("MatchNothing chain should not report empty")
	}
	if ok, _ := chain.Match(http.Header{}, map[string]any{"a": 1}); ok {
		t.Fatal("MatchNothing should reject everything")
	}
}

func TestMatchReturnsFailingIndex(t *testing.T) {
	chain, issues := Compile([]Spec{
		{SourceBody, "ref", OpEq, "refs/heads/main"},
		{SourceBody, "repository.private", OpEq, true}, // this one fails
		{SourceBody, "count", OpEq, 42},
	})
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	ok, idx := chain.Match(nil, fixtureBody(t))
	if ok {
		t.Fatal("chain should not match")
	}
	if idx != 1 {
		t.Fatalf("want failing index 1, got %d", idx)
	}
	source, path, op := chain.Describe(idx)
	if source != SourceBody || path != "repository.private" || op != OpEq {
		t.Fatalf("Describe = %q %q %q", source, path, op)
	}
}
