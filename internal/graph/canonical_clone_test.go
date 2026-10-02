package graph

import (
	"context"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
)

func TestCloneSharesOnlyImmutableCanonicalMetadata(t *testing.T) {
	raw := []byte(`{ "x":1 }`)
	rep, err := jsoncanon.Prepare(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	input := Envelope{V: Verified{Raw: RawRequest{Body: raw}, OriginalJSON: rep}}
	out, err := clonePayload(input)
	if err != nil {
		t.Fatal(err)
	}
	clone := out.(Envelope)
	if clone.V.OriginalJSON != rep {
		t.Fatal("immutable metadata lost")
	}
	data, match, err := clone.V.OriginalJSON.Bytes(context.Background(), clone.V.Raw.Body)
	if err != nil || !match {
		t.Fatal(err)
	}
	data[0] = 'x'
	clone.V.Raw.Body[3] = 'y'
	if _, match, _ = rep.Bytes(context.Background(), clone.V.Raw.Body); match {
		t.Fatal("changed branch accepted old cache")
	}
	data, match, err = rep.Bytes(context.Background(), input.V.Raw.Body)
	if err != nil || !match || string(data) != `{"x":1}` {
		t.Fatal("branch mutation escaped", err)
	}
}
