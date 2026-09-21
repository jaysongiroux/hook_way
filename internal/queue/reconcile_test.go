package queue

import (
	"context"
	"testing"
)

func TestDiffAttributesOnlyComparesWantKeys(t *testing.T) {
	want := map[string]string{"VisibilityTimeout": "30", "FifoQueue": "true"}
	got := map[string]string{"VisibilityTimeout": "60", "FifoQueue": "true", "MessageRetentionPeriod": "345600"}
	mutable := map[string]bool{"VisibilityTimeout": true, "FifoQueue": false}

	diffs := diffAttributes(want, got, mutable)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %#v", diffs)
	}
	if diffs[0].name != "VisibilityTimeout" || diffs[0].want != "30" || diffs[0].got != "60" || !diffs[0].mutable {
		t.Fatalf("bad diff: %#v", diffs[0])
	}
}

func TestDiffAttributesMissingGotKeyIsDiff(t *testing.T) {
	diffs := diffAttributes(
		map[string]string{"ContentBasedDeduplication": "true"},
		map[string]string{}, // attribute absent on the existing resource
		map[string]bool{"ContentBasedDeduplication": true},
	)
	if len(diffs) != 1 || diffs[0].got != "" || !diffs[0].mutable {
		t.Fatalf("bad diff: %#v", diffs)
	}
}

func TestApplyConflictPolicyImmutableAlwaysErrors(t *testing.T) {
	for _, policy := range []ConflictPolicy{ConflictError, ConflictWarn, ConflictReconcile} {
		setCalled := false
		err := applyConflictPolicy(context.Background(), reconcileParams{
			policy: policy, destID: "d", target: "t.fifo",
			want:     map[string]string{"FifoQueue": "true"},
			got:      map[string]string{"FifoQueue": ""}, // existing is standard
			mutable:  map[string]bool{"FifoQueue": false},
			setAttrs: func(context.Context, map[string]string) error { setCalled = true; return nil },
		})
		if err == nil {
			t.Fatalf("policy %q: expected immutable error", policy)
		}
		if setCalled {
			t.Fatalf("policy %q: must not call setAttrs on immutable conflict", policy)
		}
	}
}

func TestApplyConflictPolicyMutable(t *testing.T) {
	base := func(policy ConflictPolicy, set *map[string]string) reconcileParams {
		return reconcileParams{
			policy: policy, destID: "d", target: "t",
			want:     map[string]string{"VisibilityTimeout": "30"},
			got:      map[string]string{"VisibilityTimeout": "60"},
			mutable:  map[string]bool{"VisibilityTimeout": true},
			setAttrs: func(_ context.Context, m map[string]string) error { *set = m; return nil },
		}
	}

	// error policy: fails, no set
	var s1 map[string]string
	if err := applyConflictPolicy(context.Background(), base(ConflictError, &s1)); err == nil {
		t.Fatal("error policy should fail on mutable diff")
	}
	if s1 != nil {
		t.Fatal("error policy must not set")
	}

	// warn policy: succeeds, no set
	var s2 map[string]string
	if err := applyConflictPolicy(context.Background(), base(ConflictWarn, &s2)); err != nil {
		t.Fatalf("warn policy should succeed: %v", err)
	}
	if s2 != nil {
		t.Fatal("warn policy must not set")
	}

	// reconcile policy: succeeds, sets the mutable want
	var s3 map[string]string
	if err := applyConflictPolicy(context.Background(), base(ConflictReconcile, &s3)); err != nil {
		t.Fatalf("reconcile policy should succeed: %v", err)
	}
	if s3["VisibilityTimeout"] != "30" {
		t.Fatalf("reconcile should set VisibilityTimeout=30, got %#v", s3)
	}
}

func TestApplyConflictPolicyNoDiffIsNoop(t *testing.T) {
	setCalled := false
	err := applyConflictPolicy(context.Background(), reconcileParams{
		policy: ConflictReconcile, destID: "d", target: "t",
		want:     map[string]string{"VisibilityTimeout": "30"},
		got:      map[string]string{"VisibilityTimeout": "30"},
		mutable:  map[string]bool{"VisibilityTimeout": true},
		setAttrs: func(context.Context, map[string]string) error { setCalled = true; return nil },
	})
	if err != nil || setCalled {
		t.Fatalf("no diff should be a no-op: err=%v setCalled=%v", err, setCalled)
	}
}

func TestToConflictPolicyDefault(t *testing.T) {
	if toConflictPolicy("") != ConflictReconcile {
		t.Fatal("empty should default to reconcile")
	}
	if toConflictPolicy("warn") != ConflictWarn {
		t.Fatal("warn mapping")
	}
}
