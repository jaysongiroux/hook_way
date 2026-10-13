package jsonbudget

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCheckSemanticDepthAndValues(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		depth, nodes int
		want         error
	}{
		{`null`, 1, 1, nil}, {`[]`, 1, 1, nil}, {`{}`, 1, 1, nil},
		{`[0]`, 1, 2, nil}, {`[0]`, 1, 1, ErrLimit},
		{`[[0]]`, 1, 3, ErrLimit}, {`[[]]`, 1, 2, nil},
		{`{"a":1,"b":[]}`, 1, 3, nil},
		{`{"a":1,"a":2}`, 1, 2, ErrLimit},
		{`{"a":1,"a":2}`, 1, 3, nil},
		{`{"a":{"b":true}}`, 1, 3, ErrLimit},
		{`{"a\":":["[{},:]",-1.25e+8,false,null]}`, 1, 6, ErrLimit},
		{`{"a\":":["[{},:]",-1.25e+8,false,null]}`, 2, 6, nil},
		{`{"a":`, 1, 1, ErrInvalid}, {`{} []`, 1, 1, ErrInvalid},
	} {
		if got := Check([]byte(tc.raw), Limits{MaxDepth: tc.depth, MaxNodes: tc.nodes}); !errors.Is(got, tc.want) {
			t.Errorf("%s depth%d nodes%d: %v want%v", tc.raw, tc.depth, tc.nodes, got, tc.want)
		}
	}
}

func TestDefaultBoundaryRejectsBeforeTreeAllocation(t *testing.T) {
	deep := []byte(strings.Repeat("[", DefaultMaxDepth+1) + "0" + strings.Repeat("]", DefaultMaxDepth+1))
	wide := []byte("[" + strings.Repeat("0,", DefaultMaxNodes-1) + "0]")
	for _, raw := range [][]byte{deep, wide} {
		if err := Check(raw, Limits{}); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
		// The standard library may drop its pooled syntax scanner under race
		// instrumentation. Allow its fixed scratch allocations, not allocations
		// scaling with a million values or a decoded tree.
		if allocations := testing.AllocsPerRun(3, func() { _ = Check(raw, Limits{}) }); allocations > 16 {
			t.Fatalf("preflight allocated %.0f objects", allocations)
		}
	}
}

func FuzzCheck(f *testing.F) {
	for _, raw := range []string{`null`, `[]`, `{"a":[1,true,"\\\"",null]}`, `{"a":1,"a":2}`, `-1.25e+8`, `[[[]]]`, `"\\\\"`, `{"a":`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		err := Check(raw, Limits{MaxDepth: 10000, MaxNodes: len(raw) + 1})
		if json.Valid(raw) != (err == nil) {
			t.Fatalf("syntax disagreement: %v", err)
		}
	})
}
