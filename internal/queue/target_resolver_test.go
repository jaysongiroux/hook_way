package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type resolvedState struct{ name string }

func newTestResolver(max int) *targetResolver[resolvedState] {
	r := newTargetResolver[resolvedState](max, nil)
	r.addStatic("orders", resolvedState{name: "orders"})
	r.addTemplate(mustMatcher("{env}-orders", map[string]string{"env": "^tenant-[a-z0-9]+$"}), resolvedState{name: "tmpl"})
	return r
}

func cloneProvision(calls *int32) func(context.Context, resolvedState, string) (resolvedState, error) {
	return func(_ context.Context, base resolvedState, rendered string) (resolvedState, error) {
		atomic.AddInt32(calls, 1)
		return resolvedState{name: base.name + ":" + rendered}, nil
	}
}

func TestTargetResolverStaticNeedsNoProvision(t *testing.T) {
	var calls int32
	r := newTestResolver(0)
	got, err := r.resolve(context.Background(), "orders", cloneProvision(&calls))
	if err != nil || got.name != "orders" || calls != 0 {
		t.Fatalf("static: %v %v calls=%d", got, err, calls)
	}
}

func TestTargetResolverUnknownTarget(t *testing.T) {
	r := newTestResolver(0)
	if _, err := r.resolve(context.Background(), "nope", cloneProvision(new(int32))); !errors.Is(err, errTargetNotConfigured) {
		t.Fatalf("expected errTargetNotConfigured, got %v", err)
	}
	if _, err := r.resolve(context.Background(), "prod-orders", cloneProvision(new(int32))); !errors.Is(err, errTargetNotConfigured) {
		t.Fatalf("pattern mismatch must not resolve, got %v", err)
	}
}

func TestTargetResolverProvisionsOnceUnderConcurrency(t *testing.T) {
	var calls int32
	r := newTestResolver(0)
	var wg sync.WaitGroup
	results := make([]resolvedState, 50)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := r.resolve(context.Background(), "tenant-a-orders", cloneProvision(&calls))
			if err != nil {
				t.Errorf("resolve: %v", err)
			}
			results[i] = s
		}(i)
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("expected exactly one provision call, got %d", calls)
	}
	for _, s := range results {
		if s.name != "tmpl:tenant-a-orders" {
			t.Fatalf("bad state %v", s)
		}
	}
	// Cached: a later resolve does not provision again.
	if _, err := r.resolve(context.Background(), "tenant-a-orders", cloneProvision(&calls)); err != nil || calls != 1 {
		t.Fatalf("cached hit re-provisioned: err=%v calls=%d", err, calls)
	}
}

func TestTargetResolverProvisionErrorNotCached(t *testing.T) {
	r := newTestResolver(0)
	boom := errors.New("boom")
	failing := func(context.Context, resolvedState, string) (resolvedState, error) {
		return resolvedState{}, boom
	}
	if _, err := r.resolve(context.Background(), "tenant-b-orders", failing); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	var calls int32
	if s, err := r.resolve(context.Background(), "tenant-b-orders", cloneProvision(&calls)); err != nil || s.name != "tmpl:tenant-b-orders" || calls != 1 {
		t.Fatalf("retry after error should provision: %v %v calls=%d", s, err, calls)
	}
}

func TestTargetResolverEvictsOldest(t *testing.T) {
	var calls int32
	r := newTestResolver(2)
	for _, tgt := range []string{"tenant-b-orders", "tenant-c-orders", "tenant-a-orders"} {
		if _, err := r.resolve(context.Background(), tgt, cloneProvision(&calls)); err != nil {
			t.Fatal(err)
		}
	}
	if r.dynamicLen() != 2 {
		t.Fatalf("expected cache capped at 2, got %d", r.dynamicLen())
	}
	if _, ok := r.lookup("tenant-b-orders"); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := r.lookup("tenant-a-orders"); !ok {
		t.Fatal("newest entry should remain")
	}
}
