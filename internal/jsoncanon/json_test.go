package jsoncanon

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Expected strings are JSON.stringify(JSON.parse(input)) outputs from Node.
func TestJavaScriptRepresentation(t *testing.T) {
	cases := [][2]string{
		{` { "b": 2, "a": 1 } `, `{"b":2,"a":1}`},
		{`{"10":1,"2":2,"b":3,"a":4}`, `{"2":2,"10":1,"b":3,"a":4}`},
		{`{"n":9007199254740993,"negative":-0,"big":1e21,"small":1e-7}`, `{"n":9007199254740992,"negative":0,"big":1e+21,"small":1e-7}`},
		{`{"u":"\u00e9","html":"<>&","pair":"\ud83d\ude00","lone":"\ud800"}`, "{\"u\":\"é\",\"html\":\"<>&\",\"pair\":\"😀\",\"lone\":\"\\ud800\"}"},
		{`{"\ud800":1,"__proto__":{"a":1},"constructor":2}`, `{"\ud800":1,"__proto__":{"a":1},"constructor":2}`},
		{`{"a":1,"a":2,"b":3}`, `{"a":2,"b":3}`},
		{`[true,null,1.00,1e400]`, `[true,null,1,null]`},
	}
	for _, c := range cases {
		got, err := Stringify(context.Background(), []byte(c[0]))
		if err != nil || string(got) != c[1] {
			t.Errorf("Stringify(%s) = %s, %v; want %s", c[0], got, err, c[1])
		}
	}
}

func TestRejectInvalidAndBoundedInputs(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"x":`), {0xff}, []byte(strings.Repeat(" ", MaxBytes+1)), []byte(`{} {}`)} {
		if _, err := Stringify(context.Background(), raw); err == nil {
			t.Fatal("accepted invalid JSON or oversized input")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Stringify(ctx, []byte(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
}

func TestDepthAndLargeBodyBounds(t *testing.T) {
	if _, err := Stringify(context.Background(), []byte(strings.Repeat("[", maxDepth+1)+"0"+strings.Repeat("]", maxDepth+1))); err == nil {
		t.Fatal("accepted excessive depth")
	}
	input := []byte(`{"data":"` + strings.Repeat("x", 900000) + `"}`)
	got, err := Stringify(context.Background(), input)
	if err != nil || string(got) != string(input) {
		t.Fatalf("large broker body changed: len=%d err=%v", len(got), err)
	}
}

func TestLiteralAndEscapedPropertiesHaveSameJavaScriptSemantics(t *testing.T) {
	cases := [][2]string{
		{`{"a":1,"\u0061":2,"b":3}`, `{"a":2,"b":3}`},
		{`{"\u0061":1,"a":2,"b":3}`, `{"a":2,"b":3}`},
		{`{"__proto__":{"x":1},"\u005f_proto__":{"x":2},"constructor":3}`, `{"__proto__":{"x":2},"constructor":3}`},
		{`{"é":"😀","\u00e9":"\ud83d\ude00","\ud800":"\udfff","plain":"line\nnext"}`, `{"é":"😀","\ud800":"\udfff","plain":"line\nnext"}`},
		{`{"10":10,"2":2,"\u0031":1,"01":4,"4294967295":5}`, `{"1":1,"2":2,"10":10,"01":4,"4294967295":5}`},
	}
	for _, c := range cases {
		out, err := Stringify(context.Background(), []byte(c[0]))
		if err != nil || string(out) != c[1] {
			t.Fatalf("input %s: got %s, %v; want %s", c[0], out, err, c[1])
		}
	}
}
