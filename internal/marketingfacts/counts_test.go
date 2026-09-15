// Package marketingfacts contains no production code. It exists for the tests
// below, which pin the counts that the public overview page headlines to the
// code that actually produces them.
//
// web/src/marketing/stats.ts quotes these numbers. A TypeScript test can check
// that the strings are present, but only Go can tell whether they are still
// true. If one of these fails, the site has started making a false claim:
// update the number in stats.ts and here together.
package marketingfacts

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jasongiroux/hookway/internal/auth"
)

// readRepoFile reads a path relative to the repository root.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func uniqueMatches(t *testing.T, re *regexp.Regexp, src string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		found[m[1]] = true
	}
	return found
}

func TestQuotedMetricCountMatchesDeclarations(t *testing.T) {
	const quoted = 45
	src := readRepoFile(t, "internal/metrics/metrics.go")
	names := uniqueMatches(t, regexp.MustCompile(`"(hookway_[a-z0-9_]+)"`), src)
	if len(names) != quoted {
		t.Fatalf("metrics.go declares %d distinct metrics but web/src/marketing/stats.ts quotes %d", len(names), quoted)
	}
}

func TestQuotedSignatureSchemeCountMatchesRegistry(t *testing.T) {
	const quoted = 11
	if got := len(auth.Schemes()); got != quoted {
		t.Fatalf("auth registry has %d schemes but web/src/marketing/stats.ts quotes %d", got, quoted)
	}
}

func TestQuotedFilterOperatorCountMatchesConstants(t *testing.T) {
	const quoted = 12
	src := readRepoFile(t, "internal/filter/filter.go")
	// Op followed by an upper-case letter, which matches the operator constants
	// (OpEq, OpNotExists) without matching the Operator struct field.
	ops := uniqueMatches(t, regexp.MustCompile(`\b(Op[A-Z][A-Za-z]*)\s+=`), src)
	if len(ops) != quoted {
		t.Fatalf("filter.go declares %d operators but web/src/marketing/stats.ts quotes %d: %v", len(ops), quoted, ops)
	}
}
