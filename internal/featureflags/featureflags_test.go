package featureflags

import (
	"errors"
	"math"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
)

func TestNormalizeBoundsJSONAndCopiesValues(t *testing.T) {
	input := map[string]any{"nested": []any{map[string]any{"limit": 2}}}
	value, err := Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	value.(map[string]any)["nested"].([]any)[0].(map[string]any)["limit"] = 3.0
	if input["nested"].([]any)[0].(map[string]any)["limit"] != 2 {
		t.Fatal("normalized JSON aliases its input")
	}
	var deep any = true
	for range jsonbudget.DefaultMaxDepth + 1 {
		deep = map[string]any{"child": deep}
	}
	if _, err := Normalize(deep); !errors.Is(err, jsonbudget.ErrLimit) {
		t.Fatalf("unbounded JSON depth accepted: %v", err)
	}
	if _, err := Normalize(make([]any, jsonbudget.DefaultMaxNodes)); !errors.Is(err, jsonbudget.ErrLimit) {
		t.Fatalf("unbounded JSON node count accepted: %v", err)
	}
	for _, invalid := range []any{math.NaN(), math.Inf(1), func() {}} {
		if _, err := Normalize(invalid); err == nil {
			t.Fatal("non-JSON value accepted")
		}
	}
	if value, err := Normalize(nil); err != nil || value != nil {
		t.Fatal("explicit JSON null did not survive")
	}
}
