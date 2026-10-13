package flowconfig

import (
	"fmt"
	"testing"
)

// TestUnsafePathVarRejectsURLDelimitersAndControlChars is a security
// regression test. net/http hands handlers an already percent-decoded
// URL.Path, so "%23" arrives as "#" and "%3F" as "?" inside a captured path
// variable. Rendering those into an upstream URL template lets url.Parse
// re-interpret them as a fragment or query delimiter, which truncates the
// mandatory literal suffix the operator's template exists to enforce.
func TestUnsafePathVarRejectsURLDelimitersAndControlChars(t *testing.T) {
	cases := []struct {
		value  string
		unsafe bool
	}{
		{"acme#", true},
		{"a?b", true},
		{"a\rb", true},
		{"a\nb", true},
		{"a\x00b", true},
		{"a\\b", true},
		{"..", true},
		{"a/../b", true},
		{"a..b", false},
		{"..hidden", false},
		{"api/v1/thing", false},
		{"acme", false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.value), func(t *testing.T) {
			name, got := UnsafePathVar(map[string]string{"tenant": tc.value})
			if got != tc.unsafe {
				t.Fatalf("UnsafePathVar(%q) = (%q, %v), want unsafe=%v", tc.value, name, got, tc.unsafe)
			}
		})
	}
}

func TestUnsafePathVarDetectsTraversal(t *testing.T) {
	cases := []struct {
		name   string
		vars   map[string]string
		unsafe bool
	}{
		{"plain segment", map[string]string{"rest": "api/v1/thing"}, false},
		{"empty", map[string]string{"rest": ""}, false},
		{"dotfile is not traversal", map[string]string{"rest": "a/..hidden/b"}, false},
		{"dots inside a name are not traversal", map[string]string{"rest": "a..b"}, false},
		{"single dot is not traversal", map[string]string{"rest": "a/./b"}, false},
		{"leading traversal", map[string]string{"rest": "../admin"}, true},
		{"nested traversal", map[string]string{"rest": "a/../../admin"}, true},
		{"trailing traversal", map[string]string{"rest": "a/.."}, true},
		{"bare traversal", map[string]string{"rest": ".."}, true},
		{"traversal in a non-greedy var", map[string]string{"tenant": ".."}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, got := UnsafePathVar(tc.vars)
			if got != tc.unsafe {
				t.Fatalf("UnsafePathVar(%v) = (%q, %v), want unsafe=%v", tc.vars, name, got, tc.unsafe)
			}
		})
	}
}
