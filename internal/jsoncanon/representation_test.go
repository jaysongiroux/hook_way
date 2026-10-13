package jsoncanon

import (
	"context"
	"sync"
	"testing"
)

func TestRepresentationRequiresExactSourceAndNeverSharesWritableBytes(t *testing.T) {
	ctx := context.Background()
	raw := []byte(`{ "b":2,"1":1,"a":9007199254740993,"u":"\ud800","b":3 }`)
	want, err := Stringify(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	representation, err := Prepare(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, match, err := representation.Bytes(ctx, raw)
			if err != nil || !match || string(data) != string(want) {
				t.Error("representation changed", err)
				return
			}
			data[0] = 'x' // Must not corrupt other branches or the representation.
		}()
	}
	wg.Wait()
	for _, source := range [][]byte{[]byte(`{"b":3,"1":1,"a":9007199254740992,"u":"\ud800"}`), []byte(`null`)} {
		if _, match, err := representation.Bytes(ctx, source); match || err != nil {
			t.Fatal("different source reused", err)
		}
	}
	raw[3] = 'c' // Same-sized mutation must invalidate the exact source binding.
	if _, match, _ := representation.Bytes(ctx, raw); match {
		t.Fatal("mutated source reused")
	}
}

func TestRepresentationCancellationAndInvalidInput(t *testing.T) {
	raw := []byte(`{"x":1}`)
	r, err := Prepare(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, match, err := r.Bytes(ctx, raw); err == nil || match {
		t.Fatal("cache ignored cancellation")
	}
	if _, err := Prepare(ctx, raw); err == nil {
		t.Fatal("preparation ignored cancellation")
	}
	for _, input := range [][]byte{[]byte(`broken`), make([]byte, MaxBytes+1)} {
		if _, err := Prepare(context.Background(), input); err == nil {
			t.Fatal("invalid input prepared")
		}
	}
}
