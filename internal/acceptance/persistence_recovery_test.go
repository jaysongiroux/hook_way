package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
	"gopkg.in/yaml.v3"
)

func recoveryDatabaseURL(t *testing.T, p *process) string {
	t.Helper()
	for _, env := range p.cmd.Env {
		if value, ok := strings.CutPrefix(env, "REPLAY_DATABASE_URL="); ok {
			return value
		}
	}
	t.Fatal("missing test database")
	return ""
}

func recoveryOpenStore(t *testing.T, database string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), store.Options{URL: database, MaxConns: 4, Limits: store.Limits{TTL: time.Hour, MaxRecords: 10000, MaxBytes: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func recoveryStopSystem(t *testing.T, s *system) {
	t.Helper()
	for _, p := range append(append([]*process{}, s.replays...), s.cores...) {
		p.stop()
		if !p.cmd.ProcessState.Success() {
			t.Fatalf("%s did not stop cleanly: %s", p.name, p.out.String())
		}
	}
}

func recoveryRestartSystem(t *testing.T, s *system, database string) {
	t.Helper()
	restart := func(p *process) *process {
		env := append([]string(nil), p.cmd.Env...)
		for i, value := range env {
			if strings.HasPrefix(value, "REPLAY_DATABASE_URL=") {
				env[i] = "REPLAY_DATABASE_URL=" + database
			}
		}
		return startProcess(t, p.name+"-recovered", p.cmd.Path, env, p.cmd.Args[1:]...)
	}
	for i, p := range s.cores {
		s.cores[i] = restart(p)
	}
	for _, endpoint := range s.coreReady {
		waitReady(t, "recovered core", endpoint)
	}
	for i, p := range s.replays {
		s.replays[i] = restart(p)
		data, err := os.ReadFile(p.cmd.Args[len(p.cmd.Args)-1]) // #nosec G304 -- fixture-owned configuration
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Server struct{ Addr string }
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		waitReady(t, "recovered replay", "http://"+cfg.Server.Addr+"/readyz")
	}
}

// Canonical JSON includes every column (including bytea artifacts, action
// request hashes, audit identities, policy update state, owner and expiry).
// Fixed table names are fixture inputs, never operator/request SQL.
func recoveryLogicalState(t *testing.T, database string) map[string][]byte {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	result := map[string][]byte{}
	for _, table := range []string{"records", "record_artifacts", "retry_runs", "record_events", "delivery_resolutions", "usage", "goose_db_version", "hookway_protection.policies", "hookway_protection.entries", "hookway_protection.goose_db_version"} {
		query := fmt.Sprintf("SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text), '[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS row_data FROM %s t) rows", table)
		var raw string
		if err := conn.QueryRow(t.Context(), query).Scan(&raw); err != nil {
			t.Fatalf("logical table %s: %v", table, err)
		}
		result[table] = []byte(raw)
		var contents []json.RawMessage
		if err := json.Unmarshal(result[table], &contents); err != nil {
			t.Fatal(err)
		}
		t.Logf("logical checkpoint table=%s rows=%d sha256=%x", table, len(contents), sha256.Sum256(result[table]))
	}
	var version string
	if err := conn.QueryRow(t.Context(), "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("backup database PostgreSQL=%s", version)
	return result
}

// A database rewind can turn a completed run back into queued. The restored
// database is kept isolated: reconciliation uses independently preserved sink
// receipts BEFORE starting native replay workers. No synthetic claim below
// executes an HTTP delivery; it records an explicit operator recovery decision.
func TestAcceptance_CoupledRestoreReconcilesPostSnapshotDelivery(t *testing.T) {
	s := startSystemWithProtection(t, "postgres", "")
	for _, p := range append(append([]*process{}, s.cores...), s.replays...) {
		configBytes, err := os.ReadFile(p.cmd.Args[len(p.cmd.Args)-1]) // #nosec G304 -- fixture-owned configuration
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("recovery config component=%s sha256=%x", p.name, sha256.Sum256(configBytes))
	}
	s.audit.failing.Store(true)
	const delivered, confirmed, eligible = "snapshot-delivered", "snapshot-confirmed", "snapshot-eligible"
	for _, event := range []string{delivered, confirmed, eligible} {
		body := []byte(fmt.Sprintf(`{"order":"recovery","event_id":%q}`, event))
		if code, err := signedRequest(s.coreURL+"/protected", event, body); err != nil || code != http.StatusAccepted {
			t.Fatal(code, err)
		}
	}
	for range 2 {
		if code, err := signedRequest(s.coreURL+"/quota", "", []byte(`{}`)); err != nil || code != http.StatusAccepted {
			t.Fatal(code, err)
		}
	}
	var captured struct{ Items []failureItem }
	waitFor(t, "three recovery records", 30*time.Second, func() bool {
		return s.api(t, http.MethodGet, "/v1/records?flow=protected&delivery_status=open", nil, &captured) == 200 && len(captured.Items) == 3
	})
	recoveryStopSystem(t, s)
	source := recoveryDatabaseURL(t, s.replays[0])
	sourceStore := recoveryOpenStore(t, source)
	ids := map[string]string{}
	artifactDigests := map[string][32]byte{}
	payloads := map[string][]byte{}
	for _, failure := range captured.Items {
		payload, err := sourceStore.Payload(t.Context(), failure.ID, "recovery-reader")
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Body struct {
				EventID string `json:"event_id"`
			}
		}
		if err := json.Unmarshal(payload.Bytes, &envelope); err != nil || envelope.Body.EventID == "" {
			t.Fatalf("provider identity missing from payload: %v", err)
		}
		ids[envelope.Body.EventID] = failure.ID
		payloads[envelope.Body.EventID] = payload.Bytes
		artifactDigests[failure.ID] = sha256.Sum256(payload.Bytes)
	}
	// Include retained retry+resolution receipts in the backup, not empty tables.
	confirmedRetry := store.RetryRequest{RecordID: ids[confirmed], Actor: "recovery-operator", IdempotencyKey: "confirmed-before-snapshot"}
	confirmedRun, err := sourceStore.RequestRetry(t.Context(), confirmedRetry)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := sourceStore.ClaimRuns(t.Context(), "synthetic-uncertain-result", 1, time.Minute)
	if err != nil || len(claims) != 1 || claims[0].Run.RunID != confirmedRun.RunID {
		t.Fatal(claims, err)
	}
	if _, err := sourceStore.FinalizeRun(t.Context(), store.RunOutcome{RunID: confirmedRun.RunID, Status: store.RunUnknown, Actor: "synthetic-uncertain-result", Result: json.RawMessage(`{"reason":"executor_unknown"}`)}); err != nil {
		t.Fatal(err)
	}
	// The fixture makes an actual independent destination HTTP delivery, but does
	// not pretend that the uncertain synthetic adapter result proved delivery.
	s.audit.failing.Store(false)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.audit.srv.URL, bytes.NewReader(payloads[confirmed]))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	s.audit.assertLedger(t, confirmed, receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	s.audit.failing.Store(true)
	resolve := store.ResolveRequest{RecordID: ids[confirmed], Actor: "recovery-operator", IdempotencyKey: "resolution-before-snapshot", ResolutionInput: uicontract.ResolutionInput{Note: "Independent destination receipt confirms delivery for retained action backup fixture.", Reference: "fixture://confirmed-receipt"}}
	resolution, err := sourceStore.ResolveUnknown(t.Context(), resolve)
	if err != nil || !resolution.Created {
		t.Fatal(resolution, err)
	}
	queuedRequest := store.RetryRequest{RecordID: ids[delivered], Actor: "recovery-operator", IdempotencyKey: "queued-at-snapshot"}
	queuedRun, err := sourceStore.RequestRetry(t.Context(), queuedRequest)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := recoveryLogicalState(t, source)
	backupStarted := time.Now()
	restored := replaytest.BackupRestore(t, source)
	backupCompleted := time.Now()
	if restored == source {
		t.Fatal("restore overwrote the source database")
	}
	for table, got := range recoveryLogicalState(t, restored) {
		if !bytes.Equal(checkpoint[table], got) {
			t.Fatalf("restore changed logical state in %s", table)
		}
	}
	// Simulate real deliveries after the retained checkpoint on the SOURCE.
	// No process, control listener or worker is pointed at the restored DB yet.
	s.audit.failing.Store(false)
	recoveryRestartSystem(t, s, source)
	waitFor(t, "post-snapshot real delivery", 30*time.Second, func() bool {
		run, err := sourceStore.GetRun(t.Context(), queuedRun.RunID)
		return err == nil && run.Status == store.RunSucceeded
	})
	recoveryStopSystem(t, s)
	postBackupInterval := time.Since(backupCompleted)
	checkpointInterval := time.Since(backupStarted)
	s.orders.assertLedger(t, delivered, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
	s.audit.assertLedger(t, delivered, receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	targetStore := recoveryOpenStore(t, restored)
	staleRun, err := targetStore.GetRun(t.Context(), queuedRun.RunID)
	if err != nil || staleRun.Status != store.RunQueued {
		t.Fatal(staleRun, err)
	}
	// Demonstrate the concrete stale-queued duplicate risk before resuming.
	if current, err := sourceStore.GetRun(t.Context(), queuedRun.RunID); err != nil || current.Status != store.RunSucceeded {
		t.Fatal(current, err)
	}
	repeated, err := targetStore.RequestRetry(t.Context(), confirmedRetry)
	if err != nil || repeated.Created || repeated.RunID != confirmedRun.RunID {
		t.Fatalf("restored retry receipt changed: %+v %v", repeated, err)
	}
	repeatedResolution, err := targetStore.ResolveUnknown(t.Context(), resolve)
	if err != nil || repeatedResolution.Created || repeatedResolution.Resolution != resolution.Resolution {
		t.Fatalf("restored resolution receipt changed: %+v %v", repeatedResolution, err)
	}
	changed := resolve
	changed.Note = "Different evidence must not replace retained receipt."
	if _, err := targetStore.ResolveUnknown(t.Context(), changed); !errors.Is(err, store.ErrIdempotencyKeyMismatch) {
		t.Fatal("changed restored action did not reject", err)
	}
	for id, expected := range artifactDigests {
		payload, err := targetStore.Payload(t.Context(), id, "recovery-verifier")
		if err != nil || sha256.Sum256(payload.Bytes) != expected {
			t.Fatalf("restored payload digest mismatch: %v", err)
		}
		if _, err := targetStore.Snapshot(t.Context(), id); err != nil {
			t.Fatalf("restored snapshot verification: %v", err)
		}
	}
	// Fixture-only direct store transaction: adopt the queued run without executing
	// it, mark uncertain, and attach the independently verified receipt. Production
	// has no API to cancel queued work; keep services stopped and perform an audited
	// release-specific DBA reconciliation, never blindly start workers.
	claims, err = targetStore.ClaimRuns(t.Context(), "recovery-reconciliation-no-execution", 1, time.Minute)
	if err != nil || len(claims) != 1 || claims[0].Run.RunID != queuedRun.RunID {
		t.Fatal(claims, err)
	}
	if _, err := targetStore.FinalizeRun(t.Context(), store.RunOutcome{RunID: queuedRun.RunID, Status: store.RunUnknown, Actor: "recovery-reconciliation-no-execution", Result: json.RawMessage(`{"reason":"executor_unknown"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := targetStore.ResolveUnknown(t.Context(), store.ResolveRequest{RecordID: ids[delivered], Actor: "recovery-operator", IdempotencyKey: "post-snapshot-reconciliation", ResolutionInput: uicontract.ResolutionInput{Note: "Preserved source run succeeded and independent destination receipt confirms snapshot-delivered; do not repeat delivery.", Reference: "fixture://snapshot-delivered-receipt"}}); err != nil {
		t.Fatal(err)
	}
	stats, err := targetStore.Stats(t.Context())
	if err != nil || stats.RunsByStatus[store.RunQueued] != 0 || stats.RunsByStatus[store.RunRunning] != 0 {
		t.Fatalf("unsafe resume state %+v %v", stats, err)
	}
	recoveryRestartSystem(t, s, restored)
	if code, err := signedRequest(s.coreURL+"/protected", delivered, []byte(fmt.Sprintf(`{"order":"recovery","event_id":%q}`, delivered))); err != nil || code != 409 {
		t.Fatalf("restored guard lost owner/expiry history: %d %v", code, err)
	}
	allowed := 0
	for range 10 {
		code, err := signedRequest(s.coreURL+"/quota", "", []byte(`{}`))
		if err != nil || (code != 202 && code != 429) {
			t.Fatal(code, err)
		}
		if code == 202 {
			allowed++
		}
	}
	if allowed != 8 {
		t.Fatalf("restored quota history remaining=%d want8", allowed)
	}
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if code := s.api(t, http.MethodPost, "/v1/records/"+ids[eligible]+"/retries", map[string]any{}, &accepted, "Idempotency-Key", "explicit-safe-resume"); code != 202 {
		t.Fatal(code)
	}
	waitFor(t, "eligible destination-only recovery", 30*time.Second, func() bool {
		run, err := targetStore.GetRun(t.Context(), accepted.RunID)
		return err == nil && run.Status == store.RunSucceeded
	})
	s.orders.assertLedger(t, eligible, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
	s.audit.assertLedger(t, eligible, receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	s.audit.assertLedger(t, delivered, receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	t.Logf("recovery measured_total=%s checkpoint_to_preserved_source_stop=%s post_backup_to_source_stop=%s; zero record captures lost, one post-snapshot run completion rewound; source retained; restored queued risk reconciled before explicit worker resume; no agreed RPO/RTO target", time.Since(backupStarted), checkpointInterval, postBackupInterval)
}
