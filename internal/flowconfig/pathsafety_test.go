package flowconfig

import "testing"

func TestUnsafePathVarDetectsTraversal(t *testing.T) {
	cases := []struct {
		name  string
		vars  map[string]string
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
