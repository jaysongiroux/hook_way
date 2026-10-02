package jsoncanon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUnchangedProofAndJavaScriptFallback(t *testing.T) {
	cases := []struct {
		input string
		fast  bool
	}{
		{` { "b":2, "a":[0.5,true,null,"<>&é😀  "],"__proto__":{"constructor":1} } `, true},
		{`{"n":9007199254740991,"m":-9007199254740991}`, true},
		{`{"n":9007199254740993}`, false}, {`{"n":-0}`, false},
		{`{"n":1.00}`, false}, {`{"n":1e400}`, false}, {`{"n":1e21}`, false},
		{`{"n":1e+21}`, true}, {`{"n":1e-7}`, true},
		{`{"10":1,"2":2}`, false}, {`{"0":0}`, false},
		{`{"01":1,"4294967295":2}`, true},
		{`{"a":1,"a":2}`, false}, {`{"x":{"a":1},"y":{"a":2}}`, true},
		{`{"x":"\u00e9"}`, false}, {`{"\u0061":1,"a":2}`, false},
		{`{"x":"\ud800"}`, false}, {`"line\nnext"`, false},
	}
	// These characters remain literal in JSON.stringify, unlike Go's HTML escaping.
	cases[0].input = strings.ReplaceAll(strings.ReplaceAll(cases[0].input, `\u2028`, "\u2028"), `\u2029`, "\u2029")
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			ctx := context.Background()
			_, fast, err := compactUnchanged(ctx, []byte(c.input))
			if err != nil || fast != c.fast {
				t.Fatalf("fast=%v err=%v; want %v", fast, err, c.fast)
			}
			got, err := Stringify(ctx, []byte(c.input))
			if err != nil {
				t.Fatal(err)
			}
			want, err := stringifyVM(ctx, []byte(c.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("got %s; reference %s", got, want)
			}
		})
	}
	body, err := os.ReadFile("../testdata/record-update.json")
	if err != nil {
		t.Fatal(err)
	}
	out, fast, err := compactUnchanged(context.Background(), body)
	if err != nil || !fast {
		t.Fatalf("representative event: fast=%v err=%v", fast, err)
	}
	reference, err := stringifyVM(context.Background(), body)
	if err != nil || string(out) != string(reference) {
		t.Fatalf("representative event changed: %v", err)
	}
}

func FuzzUnchangedMatchesJavaScript(f *testing.F) {
	for _, input := range []string{`{}`, ` [ 0.5,true,null,"é" ] `, `{"a":1,"a":2}`, `{"2":2,"1":1}`, `{"a":-0}`, `{"x":"\ud800"}`} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 || !json.Valid([]byte(input)) || !utf8.ValidString(input) {
			t.Skip()
		}
		got, fast, err := compactUnchanged(context.Background(), []byte(input))
		if err != nil || !fast {
			return
		}
		want, err := stringifyVM(context.Background(), []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("input %s: fast %s, reference %s", input, got, want)
		}
	})
}
