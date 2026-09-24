package tworeplica

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replay/worker"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func generous() store.Limits {
	return store.Limits{TTL: time.Hour, MaxRecords: 100000, MaxBytes: 1 << 30}
}

// replicas opens two stores — two pools, as two replicas would have — on one
// fresh database.
func replicas(t *testing.T, limits store.Limits) (a, b *store.Store, dbURL string) {
	t.Helper()
	dbURL = replaytest.Database(t)
	ctx := context.Background()
	open := func() *store.Store {
		s, err := store.Open(ctx, store.Options{URL: dbURL, MaxConns: 8, Limits: limits})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	a = open()
	if _, err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return a, open(), dbURL
}

var seq atomic.Int64

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// record is a valid, retryable resume record with its own artifacts.
func record() store.NewFailure {
	n := seq.Add(1)
	snap, pay := []byte(fmt.Sprintf("snapshot-%d", n)), []byte(fmt.Sprintf("payload-%d", n))
	at := time.Now().UTC().Truncate(time.Microsecond)
	m := replaywire.FailureMetadata{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: replaywire.KindResume, Flow: "orders", NodeID: "out",
		NodeType: "publish", DestinationID: "archive", DestinationType: "s3", Target: "uploads",
		RenderedTarget: "uploads", EventID: fmt.Sprintf("evt-%d", n), ReceivedAt: at, FailedAt: at,
		InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, Attempts: 1, Retryable: true,
		Snapshot: &replaywire.ArtifactInfo{Size: int64(len(snap)), SHA256: digest(snap), Version: 1},
		Payload:  &replaywire.ArtifactInfo{Size: int64(len(pay)), SHA256: digest(pay)},
	}
	return store.NewFailure{Metadata: m, Snapshot: snap, Payload: pay, Actor: "core"}
}

func ingest(t *testing.T, s *store.Store) string {
	t.Helper()
	f := record()
	if _, err := s.Ingest(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	return f.Metadata.ID
}

func retry(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	res, err := s.RequestRetry(context.Background(), store.RetryRequest{FailureID: id, Actor: "ops", IdempotencyKey: "retry-" + id})
	if err != nil {
		t.Fatal(err)
	}
	return res.RunID
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// countingDeliverer succeeds every run and counts deliveries per run id.
type countingDeliverer struct {
	mu    sync.Mutex
	calls map[string]int
}

func (d *countingDeliverer) Deliver(_ context.Context, run store.ClaimedRun, _ []byte) worker.Outcome {
	d.mu.Lock()
	d.calls[run.Run.RunID]++
	d.mu.Unlock()
	time.Sleep(5 * time.Millisecond) // widen the window a double claim would need
	return worker.Outcome{Status: store.RunSucceeded, Call: metrics.CallOK, Result: &replaywire.DeliverResult{
		Nodes: []replaywire.NodeOutcome{{NodeID: run.Failure.NodeID, Outcome: replaywire.NodeDelivered, Attempts: 1}}}}
}

// runWorker runs a worker until the test ends and waits for it to stop.
func runWorker(t *testing.T, o worker.Options) {
	t.Helper()
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Metrics == nil {
		o.Metrics = metrics.New()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := worker.New(o)
	go func() {
		w.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func TestConcurrentRetryRequestsCreateOneRun(t *testing.T) {
	a, b, _ := replicas(t, generous())
	id := ingest(t, a)
	var created, active atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		s := a
		if i%2 == 1 {
			s = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RequestRetry(context.Background(), store.RetryRequest{FailureID: id, Actor: "ops", IdempotencyKey: fmt.Sprintf("k-%d", i)})
			switch {
			case err == nil:
				created.Add(1)
			case errors.Is(err, store.ErrRunActive):
				active.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 || active.Load() != 19 {
		t.Fatalf("created %d runs, %d refused as active; want 1 and 19", created.Load(), active.Load())
	}
}

func TestEachRunIsDeliveredOnceAcrossReplicas(t *testing.T) {
	a, b, _ := replicas(t, generous())
	const n = 40
	runs := map[string]bool{}
	for i := range n {
		s := a
		if i%2 == 1 {
			s = b
		}
		runs[retry(t, s, ingest(t, s))] = true
	}
	d := &countingDeliverer{calls: map[string]int{}}
	for _, s := range []*store.Store{a, b} {
		runWorker(t, worker.Options{Store: s, Deliverer: d, ID: fmt.Sprintf("w-%p", s), Concurrency: 4,
			Lease: time.Minute, PollInterval: 20 * time.Millisecond, ExpireInterval: time.Second})
	}
	waitFor(t, "every record resolved", 30*time.Second, func() bool {
		st, err := a.Stats(context.Background())
		return err == nil && st.RecordsByStatus[store.StatusResolved] == n
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	for run := range runs {
		if d.calls[run] != 1 {
			t.Errorf("run %s was delivered %d times", run, d.calls[run])
		}
	}
	if len(d.calls) != n {
		t.Errorf("%d runs delivered, want %d", len(d.calls), n)
	}
}

type neverDeliverer struct{}

func (neverDeliverer) Deliver(context.Context, store.ClaimedRun, []byte) worker.Outcome {
	panic("no run is queued for this worker")
}

// A replica that dies mid-run never finalizes; another replica's lease check
// turns the run unknown, and the dead one's late report changes nothing.
func TestADeadReplicasRunExpiresToUnknownOnTheOther(t *testing.T) {
	a, b, _ := replicas(t, generous())
	id := ingest(t, a)
	runID := retry(t, a, id)
	claimed, err := a.ClaimRuns(context.Background(), "dead", 1, 300*time.Millisecond)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	runWorker(t, worker.Options{Store: b, Deliverer: neverDeliverer{}, ID: "b", Concurrency: 1, Lease: time.Minute,
		PollInterval: 50 * time.Millisecond, ExpireInterval: 100 * time.Millisecond})
	waitFor(t, "the run to expire", 10*time.Second, func() bool {
		r, err := b.GetRun(context.Background(), runID)
		return err == nil && r.Status == store.RunUnknown
	})
	if d, err := b.Get(context.Background(), id); err != nil || d.Status != store.StatusUnknown {
		t.Fatalf("record = %+v, %v; want unknown", d, err)
	}
	res, err := a.FinalizeRun(context.Background(), store.RunOutcome{RunID: runID, Status: store.RunSucceeded,
		Result: json.RawMessage(`{"nodes":[]}`), Actor: "worker:dead"})
	if err != nil || !res.Late {
		t.Fatalf("late finalize = %+v, %v; want Late", res, err)
	}
	if d, _ := b.Get(context.Background(), id); d.Status != store.StatusUnknown {
		t.Fatalf("a late result changed the record to %s", d.Status)
	}
}

func TestGCOnOneReplicaSkipsARunActiveOnTheOther(t *testing.T) {
	a, b, _ := replicas(t, store.Limits{TTL: 50 * time.Millisecond, MaxRecords: 1000, MaxBytes: 1 << 30})
	id := ingest(t, a)
	runID := retry(t, a, id)
	if claimed, err := a.ClaimRuns(context.Background(), "a", 1, time.Minute); err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	time.Sleep(100 * time.Millisecond) // the record is past its expiry
	if res, err := b.Sweep(context.Background(), 500, time.Minute); err != nil || res.Records != 0 {
		t.Fatalf("sweep during the run = %+v, %v; want nothing deleted", res, err)
	}
	if _, err := a.FinalizeRun(context.Background(), store.RunOutcome{RunID: runID, Status: store.RunSucceeded,
		Result: json.RawMessage(`{"nodes":[]}`), Actor: "worker:a"}); err != nil {
		t.Fatal(err)
	}
	if res, err := b.Sweep(context.Background(), 500, time.Minute); err != nil || res.Records != 1 {
		t.Fatalf("sweep after the run = %+v, %v; want the record deleted", res, err)
	}
	if _, err := a.Get(context.Background(), id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get after GC = %v, want ErrNotFound", err)
	}
}

func TestCorruptedArtifactsAreDetectedOnEveryReplica(t *testing.T) {
	a, b, dbURL := replicas(t, generous())
	id := ingest(t, a)
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(context.Background(), `UPDATE failure_artifacts SET payload = $1, snapshot = $1`, []byte("tampered")); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*store.Store{"a": a, "b": b} {
		if _, err := s.Payload(context.Background(), id, "ops"); !errors.Is(err, store.ErrCorrupt) {
			t.Errorf("replica %s Payload = %v, want ErrCorrupt", name, err)
		}
		if _, err := s.Snapshot(context.Background(), id); !errors.Is(err, store.ErrCorrupt) {
			t.Errorf("replica %s Snapshot = %v, want ErrCorrupt", name, err)
		}
	}
}

func TestCapacityHoldsAcrossReplicas(t *testing.T) {
	a, b, _ := replicas(t, store.Limits{TTL: time.Hour, MaxRecords: 10, MaxBytes: 1 << 30})
	var created, refused atomic.Int32
	var wg sync.WaitGroup
	for i := range 40 {
		s := a
		if i%2 == 1 {
			s = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := s.Ingest(context.Background(), record()); {
			case err == nil:
				created.Add(1)
			case errors.Is(err, store.ErrCapacity):
				refused.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	st, err := a.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if created.Load() != 10 || refused.Load() != 30 || st.UsageRecords != 10 {
		t.Fatalf("created %d, refused %d, usage %d; want 10, 30, 10", created.Load(), refused.Load(), st.UsageRecords)
	}
}
