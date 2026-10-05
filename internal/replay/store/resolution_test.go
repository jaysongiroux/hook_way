package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

func resolveRequest(id, key string) ResolveRequest {
	return ResolveRequest{FailureID: id, Actor: "incident-ops", IdempotencyKey: key, ResolutionInput: uicontract.ResolutionInput{Note: "Destination logs confirm event receipt.\nChecked trace req-1.", Reference: "INC-42"}}
}

func unknownFailure(t *testing.T, s *Store) (NewFailure, string) {
	t.Helper()
	f := testFailure(t, nil)
	run := queueRun(t, s, f)
	claim(t, s)
	if _, err := s.FinalizeRun(t.Context(), RunOutcome{RunID: run, Status: RunUnknown, Result: json.RawMessage(`{"reason":"connection_lost","evidence":"original"}`), Actor: "worker"}); err != nil {
		t.Fatal(err)
	}
	return f, run
}

func TestResolveUnknownPreservesEvidenceAndIsIdempotent(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f, run := unknownFailure(t, s)
	before, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	records, bytes := usage(t, s)
	first, err := s.ResolveUnknown(t.Context(), resolveRequest(f.Metadata.ID, "receipt-1"))
	if err != nil || !first.Created {
		t.Fatalf("%+v %v", first, err)
	}
	again, err := s.ResolveUnknown(t.Context(), resolveRequest(f.Metadata.ID, "receipt-1"))
	if err != nil || again.Created || !reflect.DeepEqual(first.Resolution, again.Resolution) {
		t.Fatalf("%+v %v", again, err)
	}
	after, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != StatusResolved || after.ResolutionSource != ResolutionOperator || after.Resolution == nil || after.Resolution.Actor != "incident-ops" || !reflect.DeepEqual(before.Runs, after.Runs) || !after.ExpiresAt.Equal(before.ExpiresAt) || !reflect.DeepEqual(before.FailureMetadata, after.FailureMetadata) || !after.HasArtifacts {
		t.Fatalf("changed evidence: %+v", after)
	}
	if len(after.History) != len(before.History)+1 || after.History[len(after.History)-1].Event != EventResolved {
		t.Fatal(after.History)
	}
	if r, b := usage(t, s); r != records || b != bytes {
		t.Fatalf("usage %d %d", r, b)
	}
	if data, err := s.Snapshot(t.Context(), f.Metadata.ID); err != nil || string(data) != string(f.Snapshot) {
		t.Fatalf("snapshot %q %v", data, err)
	}
	if _, err := retry(s, f.Metadata.ID, "another-retry", true); !errors.Is(err, ErrNotRetryable) {
		t.Fatal(err)
	}
	if _, err := s.Dismiss(t.Context(), f.Metadata.ID, "ops"); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
	late := json.RawMessage(`{"reason":"late_acceptance","upstream_request_id":"receipt-9"}`)
	final, err := s.FinalizeRun(t.Context(), RunOutcome{RunID: run, Status: RunSucceeded, Result: late, Actor: "worker"})
	if err != nil || !final.Late {
		t.Fatalf("%+v %v", final, err)
	}
	after, err = s.Get(t.Context(), f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ResolutionSource != ResolutionOperator || !reflect.DeepEqual(before.Runs, after.Runs) {
		t.Fatal(after)
	}
	var detail struct {
		Result json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(after.History[len(after.History)-1].Detail, &detail); err != nil {
		t.Fatalf("late evidence: %s %v", detail.Result, err)
	}
	jsonEqual(t, detail.Result, string(late))
}

func TestResolveUnknownRejectsConflictsAndInvalidStates(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f, _ := unknownFailure(t, s)
	req := resolveRequest(f.Metadata.ID, "receipt-1")
	if _, err := s.ResolveUnknown(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	changed := req
	changed.Note = "Different evidence"
	if _, err := s.ResolveUnknown(t.Context(), changed); !errors.Is(err, ErrIdempotencyKeyMismatch) {
		t.Fatal(err)
	}
	changed = req
	changed.IdempotencyKey = "different"
	if _, err := s.ResolveUnknown(t.Context(), changed); !errors.Is(err, ErrResolutionConflict) {
		t.Fatal(err)
	}
	changed = req
	changed.Actor = "other-ops"
	if _, err := s.ResolveUnknown(t.Context(), changed); !errors.Is(err, ErrResolutionConflict) {
		t.Fatal(err)
	}
	other, _ := unknownFailure(t, s)
	changed = req
	changed.FailureID = other.Metadata.ID
	if _, err := s.ResolveUnknown(t.Context(), changed); !errors.Is(err, ErrIdempotencyKeyMismatch) {
		t.Fatal(err)
	}
	for _, status := range []string{StatusOpen, StatusRetrying, StatusResolved, StatusDismissed} {
		t.Run(status, func(t *testing.T) {
			f := testFailure(t, nil)
			mustIngest(t, s, f)
			setStatus(t, s, f.Metadata.ID, status)
			_, err := s.ResolveUnknown(t.Context(), resolveRequest(f.Metadata.ID, "state-"+status))
			want := ErrNotResolvable
			if status == StatusRetrying {
				want = ErrRunActive
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
	invalid := resolveRequest(other.Metadata.ID, "invalid")
	invalid.Note = "\x00"
	if _, err := s.ResolveUnknown(t.Context(), invalid); !errors.Is(err, ErrInvalidResolution) {
		t.Fatal(err)
	}
	invalid.FailureID = "malformed"
	if _, err := s.ResolveUnknown(t.Context(), invalid); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestResolveRequiresLeaseExpiryAndRetainsOriginalUnknown(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	run := queueRun(t, s, f)
	req := resolveRequest(f.Metadata.ID, "lease-receipt")
	if _, err := s.ResolveUnknown(t.Context(), req); !errors.Is(err, ErrRunActive) {
		t.Fatal(err)
	}
	claim(t, s)
	s.now = func() time.Time { return testNow.Add(2 * time.Minute) }
	// A passed clock deadline alone does not fence a worker; expire its lease first.
	if _, err := s.ResolveUnknown(t.Context(), req); !errors.Is(err, ErrRunActive) {
		t.Fatal(err)
	}
	expired, err := s.ExpireLeases(t.Context(), "lease-reaper")
	if err != nil || len(expired) != 1 || expired[0] != run {
		t.Fatalf("%v %v", expired, err)
	}
	if _, err := s.ResolveUnknown(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	d, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil || d.Runs[0].Status != RunUnknown {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestResolveRetentionAndMetadataOnly(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, metadataOnly)
	mustIngest(t, s, f)
	setStatus(t, s, f.Metadata.ID, StatusUnknown)
	req := resolveRequest(f.Metadata.ID, "metadata-receipt")
	if _, err := s.ResolveUnknown(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return testNow.Add(25 * time.Hour) }
	// A successful receipt remains repeatable while retained, even past logical expiry.
	if _, err := s.ResolveUnknown(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sweep(t.Context(), 100, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveUnknown(t.Context(), req); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM failure_resolutions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	g := testFailure(t, nil)
	mustIngest(t, s, g)
	setStatus(t, s, g.Metadata.ID, StatusUnknown)
	s.now = func() time.Time { return testNow.Add(50 * time.Hour) }
	if _, err := s.ResolveUnknown(t.Context(), resolveRequest(g.Metadata.ID, "expired")); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
}

func TestConcurrentResolveRetryDismissSerialize(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	replica := openTestStore(t, s.pool.Config().ConnString(), defaultLimits())
	replica.now = s.now
	for range 12 {
		f, _ := unknownFailure(t, s)
		gate := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 3)
		for i := range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				switch i {
				case 0:
					_, errs[i] = replica.ResolveUnknown(context.Background(), resolveRequest(f.Metadata.ID, "race-"+f.Metadata.ID))
				case 1:
					_, errs[i] = s.RequestRetry(context.Background(), RetryRequest{FailureID: f.Metadata.ID, Actor: "ops", IdempotencyKey: "race-" + f.Metadata.ID, AcknowledgeDuplicateRisk: true})
				case 2:
					_, errs[i] = s.Dismiss(context.Background(), f.Metadata.ID, "ops")
				}
			}()
		}
		close(gate)
		wg.Wait()
		successes := 0
		for _, err := range errs {
			if err == nil {
				successes++
			} else if !errors.Is(err, ErrRunActive) && !errors.Is(err, ErrNotResolvable) && !errors.Is(err, ErrNotRetryable) && !errors.Is(err, ErrInvalidState) {
				t.Fatal(err)
			}
		}
		if successes != 1 {
			t.Fatalf("successes %d errors %v", successes, errs)
		}
		d, err := s.Get(t.Context(), f.Metadata.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status == StatusRetrying {
			claim(t, s)
			if _, err := s.FinalizeRun(t.Context(), RunOutcome{RunID: d.Runs[0].RunID, Status: RunFailed, Actor: "worker"}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestConcurrentResolutionsSameKeyAndCrossRecord(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f, _ := unknownFailure(t, s)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ResolveUnknown(context.Background(), resolveRequest(f.Metadata.ID, "same")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	d, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for _, h := range d.History {
		if h.Event == EventResolved {
			events++
		}
	}
	if events != 1 {
		t.Fatal(events)
	}
	errs := make([]error, 2)
	records := make([]NewFailure, 2)
	for i := range records {
		records[i], _ = unknownFailure(t, s)
	}
	gate := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, errs[i] = s.ResolveUnknown(context.Background(), resolveRequest(records[i].Metadata.ID, "cross-record"))
		}()
	}
	close(gate)
	wg.Wait()
	succeeded, mismatched := 0, 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrIdempotencyKeyMismatch) {
			mismatched++
		}
	}
	if succeeded != 1 || mismatched != 1 {
		t.Fatalf("%v", errs)
	}
}

func TestResolutionMigrationPreservesExistingEvidence(t *testing.T) {
	s := openTestStore(t, replaytest.Database(t), defaultLimits())
	if _, err := s.migrations.UpTo(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	// Insert via the old schema using the current ingest writer (new column has a default).
	s.now = func() time.Time { return testNow }
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	setStatus(t, s, f.Metadata.ID, StatusResolved)
	if err := s.Ready(t.Context()); err == nil {
		t.Fatal("old schema admitted")
	}
	versions, err := s.Migrate(t.Context())
	if err != nil || fmt.Sprint(versions) != "[2]" {
		t.Fatalf("%v %v", versions, err)
	}
	d, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil || d.ResolutionSource != ResolutionRetry || d.Resolution != nil || !d.HasArtifacts {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestResolutionAuditFailureRollsBackAndDowngradePreservesEvidence(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f, _ := unknownFailure(t, s)
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE failure_events ADD CONSTRAINT reject_resolution_test CHECK(event != 'operator_resolved')`); err != nil {
		t.Fatal(err)
	}
	req := resolveRequest(f.Metadata.ID, "audit-1")
	if _, err := s.ResolveUnknown(t.Context(), req); err == nil {
		t.Fatal("resolution committed without its audit")
	}
	d, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil || d.Status != StatusUnknown || d.Resolution != nil || d.ResolutionSource != "" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := s.pool.Exec(t.Context(), `ALTER TABLE failure_events DROP CONSTRAINT reject_resolution_test`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveUnknown(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.migrations.Down(t.Context()); err == nil {
		t.Fatal("downgrade erased operator audit evidence")
	}
	if version, err := s.SchemaVersion(t.Context()); err != nil || version != 2 {
		t.Fatalf("%d %v", version, err)
	}
	d, err = s.Get(t.Context(), f.Metadata.ID)
	if err != nil || d.Resolution == nil || d.ResolutionSource != ResolutionOperator {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestConcurrentConflictingResolutionKeysCreateOneAudit(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f, _ := unknownFailure(t, s)
	gate := make(chan struct{})
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, errs[i] = s.ResolveUnknown(context.Background(), resolveRequest(f.Metadata.ID, fmt.Sprintf("conflict-%d", i)))
		}()
	}
	close(gate)
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrResolutionConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal(errs)
	}
	d, err := s.Get(t.Context(), f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for _, e := range d.History {
		if e.Event == EventResolved {
			events++
		}
	}
	if events != 1 {
		t.Fatal(events)
	}
}
