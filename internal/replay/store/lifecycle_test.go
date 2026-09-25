package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func queueRun(t *testing.T, s *Store, f NewFailure) string {
	t.Helper()
	mustIngest(t, s, f)
	res, err := retry(s, f.Metadata.ID, "key-"+f.Metadata.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return res.RunID
}

func claim(t *testing.T, s *Store) ClaimedRun {
	t.Helper()
	claimed, err := s.ClaimRuns(context.Background(), "w1", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimRuns = %v, %v", claimed, err)
	}
	return claimed[0]
}

// childOf builds a downstream failure of parent's event.
func childOf(t *testing.T, parent NewFailure, mutate func(*NewFailure)) NewFailure {
	t.Helper()
	return testFailure(t, func(c *NewFailure) {
		c.Metadata.NodeID, c.Metadata.EventID, c.Actor = "out_lambda", parent.Metadata.EventID, ""
		if mutate != nil {
			mutate(c)
		}
	})
}

func TestClaimRunsMovesTheOldestQueuedRunsToRunning(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	var runIDs []string
	for i := range 3 {
		s.now = func() time.Time { return testNow.Add(time.Duration(i) * time.Second) }
		runIDs = append(runIDs, queueRun(t, s, testFailure(t, nil)))
	}
	s.now = func() time.Time { return testNow.Add(time.Minute) }
	claimed, err := s.ClaimRuns(ctx, "worker-1", 2, time.Minute)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimRuns = %v, %v", claimed, err)
	}
	for i, c := range claimed {
		if c.Run.RunID != runIDs[i] || c.Run.Status != RunRunning || c.Run.WorkerID != "worker-1" ||
			c.Run.LeaseExpiresAt == nil || !c.Run.LeaseExpiresAt.Equal(testNow.Add(2*time.Minute)) || c.Failure.ID != c.Run.FailureID {
			t.Fatalf("claimed[%d] = %+v", i, c)
		}
	}
	rest, err := s.ClaimRuns(ctx, "worker-2", 5, time.Minute)
	if err != nil || len(rest) != 1 || rest[0].Run.RunID != runIDs[2] {
		t.Fatalf("second claim = %v, %v", rest, err)
	}
	if none, err := s.ClaimRuns(ctx, "worker-2", 5, time.Minute); err != nil || len(none) != 0 {
		t.Fatalf("third claim = %v, %v", none, err)
	}
}

func TestConcurrentClaimsAreDisjoint(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	for range 20 {
		queueRun(t, s, testFailure(t, nil))
	}
	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				claimed, err := s.ClaimRuns(ctx, fmt.Sprintf("w%d", w), 3, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if len(claimed) == 0 {
					return
				}
				mu.Lock()
				for _, c := range claimed {
					got[c.Run.RunID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(got) != 20 {
		t.Fatalf("claimed %d distinct runs, want 20", len(got))
	}
	for id, n := range got {
		if n != 1 {
			t.Fatalf("run %s claimed %d times", id, n)
		}
	}
}

func TestSnapshotIsVerified(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	if data, err := s.Snapshot(ctx, f.Metadata.ID); err != nil || string(data) != string(f.Snapshot) {
		t.Fatalf("Snapshot = %q, %v", data, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE failure_artifacts SET snapshot = 'tampered' WHERE failure_id = $1`, f.Metadata.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(ctx, f.Metadata.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered Snapshot = %v, want ErrCorrupt", err)
	}
	g := testFailure(t, metadataOnly)
	mustIngest(t, s, g)
	if _, err := s.Snapshot(ctx, g.Metadata.ID); !errors.Is(err, ErrGone) {
		t.Fatalf("metadata-only Snapshot = %v, want ErrGone", err)
	}
	if _, err := s.Snapshot(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed id = %v, want ErrNotFound", err)
	}
}

func jsonEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("result %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("result = %s, want %s", got, want)
	}
}

func TestFinalizeSucceededResolvesAndCreatesChildren(t *testing.T) {
	ctx := context.Background()
	// MaxRecords 1: children are never rejected for capacity.
	s := newTestStore(t, Limits{TTL: 24 * time.Hour, MaxRecords: 1, MaxBytes: 1 << 30})
	parent := testFailure(t, nil)
	runID := queueRun(t, s, parent)
	claim(t, s)
	child, metaOnly := childOf(t, parent, nil), childOf(t, parent, metadataOnly)
	s.now = func() time.Time { return testNow.Add(time.Hour) }
	res, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: RunSucceeded, Result: json.RawMessage(`{"deliver":{}}`),
		Children: []NewFailure{child, metaOnly}, Actor: "worker:w1"})
	if err != nil || res.Late || len(res.ChildIDs) != 2 {
		t.Fatalf("FinalizeRun = %+v, %v", res, err)
	}
	d, _ := s.Get(ctx, parent.Metadata.ID)
	want := []string{child.Metadata.ID, metaOnly.Metadata.ID}
	sort.Strings(want)
	gotChildren := append([]string(nil), d.Children...)
	sort.Strings(gotChildren)
	if d.Status != StatusResolved || !reflect.DeepEqual(gotChildren, want) {
		t.Fatalf("parent status %q children %v", d.Status, d.Children)
	}
	if n := len(d.History); n < 3 || d.History[n-2].Event != EventRunFinished || d.History[n-1].Event != EventChildrenCreated || d.History[n-1].Actor != "worker:w1" {
		t.Fatalf("parent history = %+v", d.History)
	}
	if r := d.Runs[0]; r.Status != RunSucceeded || r.FinishedAt == nil || !r.FinishedAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("run = %+v", r)
	}
	jsonEqual(t, d.Runs[0].Result, `{"deliver":{}}`)
	c, err := s.Get(ctx, child.Metadata.ID)
	if err != nil || c.ParentID != parent.Metadata.ID || c.RootID != parent.Metadata.ID || c.Status != StatusOpen ||
		!c.ExpiresAt.Equal(d.ExpiresAt) || !c.CapturedAt.Equal(testNow.Add(time.Hour)) || c.History[0].Actor != "worker:w1" {
		t.Fatalf("child = %+v, %v", c, err)
	}
	wantBytes := int64(len(parent.Snapshot) + len(parent.Payload) + len(child.Snapshot) + len(child.Payload))
	if r, b := usage(t, s); r != 3 || b != wantBytes {
		t.Fatalf("usage = %d/%d, want 3/%d", r, b, wantBytes)
	}
}

func TestFinalizeGrandchildKeepsTheRoot(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	root := testFailure(t, nil)
	runID := queueRun(t, s, root)
	claim(t, s)
	child := childOf(t, root, nil)
	if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: RunSucceeded, Children: []NewFailure{child}, Actor: "w"}); err != nil {
		t.Fatal(err)
	}
	childRun, err := retry(s, child.Metadata.ID, "child-key", false)
	if err != nil {
		t.Fatal(err)
	}
	claim(t, s)
	grandchild := childOf(t, root, func(g *NewFailure) { g.Metadata.NodeID = "out_final" })
	if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: childRun.RunID, Status: RunSucceeded, Children: []NewFailure{grandchild}, Actor: "w"}); err != nil {
		t.Fatal(err)
	}
	g, _ := s.Get(ctx, grandchild.Metadata.ID)
	r, _ := s.Get(ctx, root.Metadata.ID)
	if g.ParentID != child.Metadata.ID || g.RootID != root.Metadata.ID || !g.ExpiresAt.Equal(r.ExpiresAt) {
		t.Fatalf("grandchild parent %q root %q expires %v (root expires %v)", g.ParentID, g.RootID, g.ExpiresAt, r.ExpiresAt)
	}
}

func TestFinalizeMovesTheRecord(t *testing.T) {
	for status, want := range map[string]string{RunFailed: StatusOpen, RunBlocked: StatusOpen, RunUnknown: StatusUnknown} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t, defaultLimits())
			f := testFailure(t, nil)
			runID := queueRun(t, s, f)
			claim(t, s)
			if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: status, Result: json.RawMessage(`{"reason":"x"}`), Actor: "w"}); err != nil {
				t.Fatal(err)
			}
			d, _ := s.Get(ctx, f.Metadata.ID)
			if d.Status != want || d.Runs[0].Status != status {
				t.Fatalf("record %q run %q, want %q %q", d.Status, d.Runs[0].Status, want, status)
			}
		})
	}
}

func TestFinalizeAfterLeaseExpiryIsLate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	runID := queueRun(t, s, f)
	claim(t, s)
	s.now = func() time.Time { return testNow.Add(2 * time.Minute) }
	expired, err := s.ExpireLeases(ctx, "worker:w2")
	if err != nil || !reflect.DeepEqual(expired, []string{runID}) {
		t.Fatalf("ExpireLeases = %v, %v", expired, err)
	}
	r0, b0 := usage(t, s)
	res, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: RunSucceeded, Children: []NewFailure{childOf(t, f, nil)}, Actor: "worker:w1"})
	if err != nil || !res.Late || len(res.ChildIDs) != 0 {
		t.Fatalf("late FinalizeRun = %+v, %v", res, err)
	}
	d, _ := s.Get(ctx, f.Metadata.ID)
	if d.Status != StatusUnknown || d.Runs[0].Status != RunUnknown || len(d.Children) != 0 {
		t.Fatalf("record %q run %q children %v", d.Status, d.Runs[0].Status, d.Children)
	}
	jsonEqual(t, d.Runs[0].Result, `{"reason":"lease_expired"}`)
	events := []string{}
	for _, e := range d.History {
		events = append(events, e.Event)
	}
	if n := len(events); n < 2 || events[n-2] != EventRunFinished || events[n-1] != EventLateResult {
		t.Fatalf("history = %v", events)
	}
	if r, b := usage(t, s); r != r0 || b != b0 {
		t.Fatalf("usage changed on a late result: %d/%d → %d/%d", r0, b0, r, b)
	}
}

func TestFinalizeRejectsChildArtifactsThatDoNotMatchTheirSizes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	runID := queueRun(t, s, f)
	claim(t, s)
	child := childOf(t, f, nil)
	child.Payload = append(child.Payload, 'x') // one byte more than declared
	if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: RunSucceeded, Children: []NewFailure{child}, Actor: "w"}); err == nil {
		t.Fatal("a child whose bytes differ from its declared size was stored")
	}
	if d, _ := s.Get(ctx, f.Metadata.ID); d.Runs[0].Status != RunRunning {
		t.Fatalf("a rejected finalize changed the run: %q", d.Runs[0].Status)
	}
}

func TestFinalizeRejectsInvalidOutcomes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	runID := queueRun(t, s, f)
	claim(t, s)
	if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: RunFailed, Children: []NewFailure{childOf(t, f, nil)}}); err == nil {
		t.Fatal("a failed run created children")
	}
	if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: "exploded"}); err == nil {
		t.Fatal("an unknown status was accepted")
	}
	if d, _ := s.Get(ctx, f.Metadata.ID); d.Runs[0].Status != RunRunning {
		t.Fatalf("a rejected finalize changed the run: %q", d.Runs[0].Status)
	}
}

func TestExpireLeasesLeavesLiveLeases(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	queueRun(t, s, f)
	claim(t, s)
	s.now = func() time.Time { return testNow.Add(30 * time.Second) }
	if expired, err := s.ExpireLeases(ctx, "w"); err != nil || len(expired) != 0 {
		t.Fatalf("ExpireLeases = %v, %v", expired, err)
	}
	if d, _ := s.Get(ctx, f.Metadata.ID); d.Status != StatusRetrying || d.Runs[0].Status != RunRunning {
		t.Fatalf("record %q run %q", d.Status, d.Runs[0].Status)
	}
}

func TestStatsCountsRuns(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	for i := range 3 {
		s.now = func() time.Time { return testNow.Add(time.Duration(i) * 10 * time.Second) }
		queueRun(t, s, testFailure(t, nil))
	}
	claim(t, s) // the oldest (created at testNow)
	s.now = func() time.Time { return testNow.Add(time.Minute) }
	st, err := s.Stats(ctx)
	if err != nil || st.RunsByStatus[RunQueued] != 2 || st.RunsByStatus[RunRunning] != 1 || st.OldestQueuedAge != 50*time.Second {
		t.Fatalf("stats = %+v, %v", st, err)
	}
}
