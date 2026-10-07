package acceptance

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

// The destination ledger and PostgreSQL connection are independent of the
// terminated processes. A closed listener alone is not evidence of completion.
func terminationSystem(t *testing.T) (*system, *store.Store) {
	t.Helper()
	core, replay := binaries(t)
	db := replaytest.Database(t)
	env := []string{"REPLAY_DATABASE_URL=" + db, "REPLAY_CAPTURE_TOKEN=" + captureToken,
		"REPLAY_CONTROL_TOKEN=" + controlToken, "REPLAY_OPERATOR_TOKEN=" + operatorToken,
		"ORDERS_WEBHOOK_SECRET=" + webhookSecret}
	s := &system{orders: newSink(t), audit: newSink(t)}
	httpAddr, controlAddr, metricsAddr, replayAddr := freeAddr(t), freeAddr(t), freeAddr(t), freeAddr(t)
	s.coreURL, s.replayURL = "http://"+httpAddr, "http://"+replayAddr
	dir := t.TempDir()
	coreSource := fmt.Sprintf(coreYAML, httpAddr, metricsAddr, s.replayURL, controlAddr, s.orders.srv.URL, s.audit.srv.URL)
	coreSource = strings.Replace(coreSource, "max_body_bytes: 16777216}", "max_body_bytes: 16777216, publish_timeout: 4s, shutdown_delay: 2s, shutdown_timeout: 5s, shutdown_total_timeout: 15s}", 1)
	replaySource := fmt.Sprintf(replayYAML, replayAddr, freeAddr(t), "http://"+controlAddr)
	replaySource = strings.Replace(replaySource, "concurrency: 4", "concurrency: 1", 1)
	corePath, replayPath := filepath.Join(dir, "core.yaml"), filepath.Join(dir, "replay.yaml")
	writeConfig(t, corePath, coreSource)
	writeConfig(t, replayPath, replaySource)
	t.Logf("termination config core_sha256=%x replay_sha256=%x core withdrawal=2s HTTP=5s total=15s; replay concurrency=1 execution=30s lease=60s finalization_reserve=10s; no custom OS resource limits", sha256.Sum256([]byte(coreSource)), sha256.Sum256([]byte(replaySource)))
	cmd := exec.CommandContext(t.Context(), replay, "migrate", "--config", replayPath) // #nosec G204 -- repository-built binary and fixture configuration
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("termination fixture migrate: %v\n%s", err, out)
	}
	s.replays = []*process{startProcess(t, "termination-replay", replay, env, "serve", "--config", replayPath)}
	s.cores = []*process{startProcess(t, "termination-core", core, env, "--config", corePath)}
	waitReady(t, "termination replay", s.replayURL+"/readyz")
	waitReady(t, "termination core", s.coreURL+"/readyz")
	st, err := store.Open(t.Context(), store.Options{URL: db, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return s, st
}

func terminationFailures(t *testing.T, s *system, n int) []failureItem {
	t.Helper()
	var result struct {
		Items []failureItem `json:"items"`
	}
	waitFor(t, "termination fixture failure capture", 10*time.Second, func() bool {
		return s.api(t, http.MethodGet, "/v1/records?kind=failure&delivery_status=open&dismissed=false&node_id=audit&limit=100", nil, &result) == http.StatusOK && len(result.Items) == n
	})
	return result.Items
}

func terminationRetry(t *testing.T, s *system, recordID, key string) string {
	t.Helper()
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if code := s.api(t, http.MethodPost, "/v1/records/"+recordID+"/retries", map[string]any{}, &accepted, "Idempotency-Key", key); code != http.StatusAccepted || accepted.RunID == "" {
		t.Fatalf("termination retry accepted=%d %+v", code, accepted)
	}
	return accepted.RunID
}

func terminationSignal(t *testing.T, p *process) time.Time {
	t.Helper()
	started := time.Now()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	return started
}

func terminationExit(t *testing.T, p *process, started time.Time, budget time.Duration) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(budget):
		t.Fatalf("%s exceeded declared termination budget %s", p.name, budget)
	}
	if !p.cmd.ProcessState.Success() {
		t.Fatalf("%s failed shutdown: %s", p.name, p.out.String())
	}
	t.Logf("SIGTERM %s successful exit elapsed=%s budget=%s", p.name, time.Since(started), budget)
}

func TestAcceptance_SIGTERMDrainsIngressRedriveAndFinalCapture(t *testing.T) {
	s, st := terminationSystem(t)
	s.audit.failing.Store(true)
	if code := s.post(t, []byte(`{"event_id":"termination-redrive"}`)); code != http.StatusAccepted {
		t.Fatal(code)
	}
	record := terminationFailures(t, s, 1)[0]
	redriveAccepted, releaseRedrive := make(chan struct{}), make(chan struct{})
	var redriveOnce sync.Once
	s.audit.mu.Lock()
	s.audit.accepted = func(ctx context.Context) {
		redriveOnce.Do(func() { close(redriveAccepted) })
		select {
		case <-releaseRedrive:
		case <-ctx.Done():
		}
	}
	s.audit.mu.Unlock()
	t.Cleanup(func() { onceRelease(releaseRedrive) })
	s.audit.failing.Store(false)
	runID := terminationRetry(t, s, record.ID, "termination-redrive")
	waitFor(t, "admitted redrive independently accepted", 5*time.Second, func() bool {
		select {
		case <-redriveAccepted:
			return true
		default:
			return false
		}
	})
	s.audit.failing.Store(true)
	ingressAccepted, releaseIngress := make(chan struct{}), make(chan struct{})
	var ingressOnce sync.Once
	s.orders.mu.Lock()
	s.orders.accepted = func(ctx context.Context) {
		blocked := false
		ingressOnce.Do(func() { blocked = true; close(ingressAccepted) })
		if blocked {
			select {
			case <-releaseIngress:
			case <-ctx.Done():
			}
		}
	}
	s.orders.mu.Unlock()
	t.Cleanup(func() { onceRelease(releaseIngress) })
	response := make(chan int, 1)
	errCh := make(chan error, 1)
	go func() {
		code, err := s.postResult([]byte(`{"event_id":"termination-ingress"}`))
		response <- code
		errCh <- err
	}()
	waitFor(t, "admitted ingress independently accepted", 5*time.Second, func() bool {
		select {
		case <-ingressAccepted:
			return true
		default:
			return false
		}
	})
	started := terminationSignal(t, s.cores[0])
	waitFor(t, "live readiness withdrawal", time.Second, func() bool {
		code, body, err := get(s.coreURL + "/readyz")
		return err == nil && code == http.StatusServiceUnavailable && strings.Contains(string(body), "withdrawing")
	})
	t.Logf("SIGTERM live readiness withdrawal observed=%s", time.Since(started))
	if code, _, err := get(s.coreURL + "/healthz"); err != nil || code != http.StatusOK {
		t.Fatalf("withdrawal liveness=%d %v", code, err)
	}
	if code := s.post(t, []byte(`{"event_id":"termination-withdrawal"}`)); code != http.StatusAccepted {
		t.Fatalf("withdrawal admitted HTTP=%d", code)
	}
	waitFor(t, "listener enters drain", 4*time.Second, func() bool { _, _, err := get(s.coreURL + "/readyz"); return err != nil })
	select {
	case <-s.cores[0].done:
		t.Fatal("core closed resources beneath admitted ingress/redrive")
	default:
	}
	close(releaseIngress)
	close(releaseRedrive)
	if code := <-response; code != http.StatusAccepted {
		t.Fatalf("admitted ingress HTTP=%d", code)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	terminationExit(t, s.cores[0], started, 15*time.Second)
	waitFor(t, "admitted redrive finalized", 5*time.Second, func() bool {
		run, err := st.GetRun(t.Context(), runID)
		return err == nil && run.Status == store.RunSucceeded
	})
	terminationFailures(t, s, 2) // both final successful inbound responses retained their failed branch
	s.orders.assertLedger(t, "termination-redrive", receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
	s.audit.assertLedger(t, "termination-redrive", receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	for _, id := range []string{"termination-ingress", "termination-withdrawal"} {
		s.orders.assertLedger(t, id, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
		s.audit.assertLedger(t, id, receiptLedger{Attempts: 1})
	}
	t.Log("reconciliation: three inbound202; redrive only failed branch; final two failed branches captured before successful core exit; replay stayed available for capture cleanup")
}

func TestAcceptance_SIGTERMReplayStopsClaimsAndFinalizesLease(t *testing.T) {
	s, st := terminationSystem(t)
	s.audit.failing.Store(true)
	for i := range 3 {
		if code := s.post(t, []byte(fmt.Sprintf(`{"event_id":"termination-worker-%d"}`, i))); code != http.StatusAccepted {
			t.Fatal(code)
		}
	}
	records := terminationFailures(t, s, 3)
	received, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.audit.mu.Lock()
	s.audit.accepted = func(ctx context.Context) {
		once.Do(func() { close(received) })
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	s.audit.mu.Unlock()
	t.Cleanup(func() { onceRelease(release) })
	s.audit.failing.Store(false)
	active := terminationRetry(t, s, records[0].ID, "termination-active")
	waitFor(t, "worker admitted receipt", 5*time.Second, func() bool {
		select {
		case <-received:
			return true
		default:
			return false
		}
	})
	queued := []string{terminationRetry(t, s, records[1].ID, "termination-queued-1"), terminationRetry(t, s, records[2].ID, "termination-queued-2")}
	started := terminationSignal(t, s.replays[0])
	waitFor(t, "replay received termination", 2*time.Second, func() bool { return strings.Contains(s.replays[0].out.String(), "shutdown signal received") })
	select {
	case <-s.replays[0].done:
		t.Fatal("replay exited before admitted lease completed")
	default:
	}
	close(release)
	terminationExit(t, s.replays[0], started, 70*time.Second)
	run, err := st.GetRun(t.Context(), active)
	if err != nil || run.Status != store.RunSucceeded {
		t.Fatalf("admitted lease finalization=%+v %v", run, err)
	}
	for _, id := range queued {
		run, err := st.GetRun(t.Context(), id)
		if err != nil || run.Status != store.RunQueued || run.WorkerID != "" {
			t.Fatalf("post-SIGTERM claimed queued run=%+v %v", run, err)
		}
	}
	for i := range 3 {
		event := fmt.Sprintf("termination-worker-%d", i)
		s.orders.assertLedger(t, event, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
	}
	// A replacement worker may now explicitly resume the persisted backlog.
	p := s.replays[0]
	s.replays[0] = startProcess(t, "termination-replay-replacement", p.cmd.Path, p.cmd.Env, p.cmd.Args[1:]...)
	waitReady(t, "replacement replay", s.replayURL+"/readyz")
	waitFor(t, "replacement finishes durable queued runs", 10*time.Second, func() bool {
		for _, id := range queued {
			run, err := st.GetRun(t.Context(), id)
			if err != nil || run.Status != store.RunSucceeded {
				return false
			}
		}
		return true
	})
	for i := range 3 {
		s.audit.assertLedger(t, fmt.Sprintf("termination-worker-%d", i), receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	}
	t.Log("replay stop reconciliation: one admitted lease succeeded/finalized; two remained queued/unclaimed; replacement processed backlog once; good siblings never reran")
}
