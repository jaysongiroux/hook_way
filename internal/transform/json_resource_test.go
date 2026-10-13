package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
)

func TestDecodeJSONPreflightBoundsDuplicateValues(t *testing.T) {
	// A post-decode map contains only one field, hiding how many values the
	// decoder allocated before overwriting them. Wire size is below 20 MiB.
	raw := []byte("{" + strings.Repeat(`"a":0,`, jsonbudget.DefaultMaxNodes-1) + `"a":0}`)
	if _, err := DecodeJSON(raw); !errors.Is(err, jsonbudget.ErrLimit) {
		t.Fatalf("duplicate values bypassed preflight: %v", err)
	}
	if allocations := testing.AllocsPerRun(3, func() { _, _ = DecodeJSON(raw) }); allocations > 16 {
		t.Fatalf("rejected input allocated a decoded tree: %.0f objects", allocations)
	}
}

func TestJSONSchemaPreflightKeepsExistingDepthPolicy(t *testing.T) {
	schema := map[string]any{"const": strings.Repeat("x", 8)}
	for range 65 {
		schema = map[string]any{"properties": map[string]any{"child": schema}}
	}
	if _, err := NewValidator(schema, nil); !errors.Is(err, jsonbudget.ErrLimit) {
		t.Fatalf("deep schema did not fail preflight: %v", err)
	}
}
