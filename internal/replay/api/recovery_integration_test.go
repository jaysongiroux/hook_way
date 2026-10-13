package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

// Use the normal API/auth harness with separate pools and actual HTTP listeners.
// The database, listeners and synthetic receipts are owned by this test only.
func recoveryReplicas(t *testing.T) (*store.Store, *store.Store, *harness, *harness) {
	t.Helper()
	url := replaytest.Database(t)
	open := func() *store.Store {
		s, err := store.Open(t.Context(), store.Options{URL: url, MaxConns: 4,
			Limits: store.Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	a := open()
	if _, err := a.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	b := open()
	return a, b,
		newHarness(t, notFoundStore(), func(o *Options) { o.Store = a }),
		newHarness(t, notFoundStore(), func(o *Options) { o.Store = b })
}

func recoveryRecord(t *testing.T, s *store.Store) string {
	t.Helper()
	m := testMetadata()
	m.ID = uuid.Must(uuid.NewV7()).String()
	if _, err := s.Ingest(t.Context(), store.NewRecord{Metadata: m, Snapshot: testSnapshot, Payload: testPayload, Actor: "core-capture"}); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

type recoveryHTTPResult struct {
	status int
	body   []byte
	err    error
}

// No testing calls here: competing requests run this helper in goroutines.
func recoveryPost(client *http.Client, target, key, body string) recoveryHTTPResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return recoveryHTTPResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+opsToken)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return recoveryHTTPResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return recoveryHTTPResult{status: resp.StatusCode, body: data, err: err}
}

// The API completes and commits normally, but no response bytes reach the
// caller. Losing the reply after committing is different from canceling before
// the transaction commits. Returning a synthetic 500 would not prove it.
func lostRecoveryReply(t *testing.T, handler http.Handler) (*httptest.Server, <-chan recoveryHTTPResult) {
	t.Helper()
	committed := make(chan recoveryHTTPResult, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		committed <- recoveryHTTPResult{status: rec.Code, body: append([]byte(nil), rec.Body.Bytes()...)}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	return srv, committed
}

func recoveryCommitted(t *testing.T, reply <-chan recoveryHTTPResult) recoveryHTTPResult {
	t.Helper()
	select {
	case result := <-reply:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("no committed response was recorded before the connection was lost")
		return recoveryHTTPResult{}
	}
}

func TestRecoveryLostSubmissionReplyPreservesOriginalReceiptAcrossReplicas(t *testing.T) {
	s, replica, a, b := recoveryReplicas(t)
	id := recoveryRecord(t, s)
	normal := httptest.NewServer(b.handler)
	t.Cleanup(normal.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	lost, committed := lostRecoveryReply(t, a.handler)
	path := "/v1/records/" + id + "/retries"
	key, body := "lost-retry", `{"acknowledge_duplicate_risk":false}`
	if got := recoveryPost(client, lost.URL+path, key, body); got.err == nil {
		t.Fatalf("caller received a reply: %+v", got)
	}
	first := recoveryCommitted(t, committed)
	again := recoveryPost(client, normal.URL+path, key, body)
	if first.status != 202 || again.err != nil || again.status != 202 || string(first.body) != string(again.body) {
		t.Fatalf("original/repeated receipts: %+v / %+v", first, again)
	}
	changed := recoveryPost(client, normal.URL+path, key, `{"acknowledge_duplicate_risk":true}`)
	if changed.err != nil || changed.status != 422 || !strings.Contains(string(changed.body), "idempotency_key_mismatch") {
		t.Fatalf("changed retry input: %+v", changed)
	}
	var receipt retryResponse
	if err := json.Unmarshal(first.body, &receipt); err != nil {
		t.Fatal(err)
	}
	claims, err := replica.ClaimRuns(t.Context(), "worker", 1, time.Minute)
	if err != nil || len(claims) != 1 || claims[0].Run.RunID != receipt.RunID {
		t.Fatalf("claims %+v %v", claims, err)
	}
	if _, err := replica.FinalizeRun(t.Context(), store.RunOutcome{RunID: receipt.RunID, Status: store.RunUnknown,
		Result: json.RawMessage(`{"reason":"response_lost","receipt":"fixture-42"}`), Actor: "worker"}); err != nil {
		t.Fatal(err)
	}
	resolutionPath := "/v1/records/" + id + "/resolve"
	resolutionBody := `{"note":"Independent synthetic receipt fixture-42 confirms acceptance","reference":"fixture-42"}`
	lostResolution, resolutionCommitted := lostRecoveryReply(t, a.handler)
	if got := recoveryPost(client, lostResolution.URL+resolutionPath, "lost-resolution", resolutionBody); got.err == nil {
		t.Fatalf("caller received a resolution reply: %+v", got)
	}
	resolution := recoveryCommitted(t, resolutionCommitted)
	repeated := recoveryPost(client, normal.URL+resolutionPath, "lost-resolution", resolutionBody)
	if resolution.status != 200 || repeated.err != nil || repeated.status != 200 || string(resolution.body) != string(repeated.body) {
		t.Fatalf("resolution receipts: %+v / %+v", resolution, repeated)
	}
	changed = recoveryPost(client, normal.URL+resolutionPath, "lost-resolution", `{"note":"Different evidence"}`)
	if changed.err != nil || changed.status != 422 || !strings.Contains(string(changed.body), "idempotency_key_mismatch") {
		t.Fatalf("changed resolution evidence: %+v", changed)
	}
	// Even after resolution, a repeat of the earlier retry is only its receipt.
	again = recoveryPost(client, normal.URL+path, key, body)
	if again.err != nil || again.status != 202 || string(again.body) != string(first.body) {
		t.Fatalf("old retry receipt after resolution: %+v", again)
	}
	newRetry := recoveryPost(client, normal.URL+path, "new-after-resolution", body)
	if newRetry.err != nil || newRetry.status != 422 || !strings.Contains(string(newRetry.body), "not_retryable") {
		t.Fatalf("new retry after resolution: %+v", newRetry)
	}
	detail, err := s.Get(t.Context(), id)
	if err != nil || detail.DeliveryStatus != store.StatusResolved || detail.ResolutionSource != store.ResolutionOperator || len(detail.Runs) != 1 || detail.Runs[0].Status != store.RunUnknown {
		t.Fatalf("final state %+v %v", detail, err)
	}
	requested, resolved := 0, 0
	for _, e := range detail.History {
		if e.Event == store.EventRunRequested {
			requested++
		}
		if e.Event == store.EventResolved {
			resolved++
		}
	}
	if requested != 1 || resolved != 1 {
		t.Fatalf("audit events requested=%d resolved=%d", requested, resolved)
	}
}

func TestRecoveryCompetingHTTPActionsAcrossReplicasHaveOneWinner(t *testing.T) {
	s, replica, a, b := recoveryReplicas(t)
	left, right := httptest.NewServer(a.handler), httptest.NewServer(b.handler)
	t.Cleanup(left.Close)
	t.Cleanup(right.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	for round := range 8 {
		id := recoveryRecord(t, s)
		original, err := s.RequestRetry(t.Context(), store.RetryRequest{RecordID: id, Actor: "ops", IdempotencyKey: "original-" + id})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := s.ClaimRuns(t.Context(), "worker", 1, time.Minute)
		if err != nil || len(claims) != 1 || claims[0].Run.RunID != original.RunID {
			t.Fatalf("claim %+v %v", claims, err)
		}
		if _, err := s.FinalizeRun(t.Context(), store.RunOutcome{RunID: original.RunID, Status: store.RunUnknown, Actor: "worker"}); err != nil {
			t.Fatal(err)
		}
		before, err := s.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		requests := []struct{ target, key, body string }{
			{left.URL + "/v1/records/" + id + "/retries", "race-retry-" + id, `{"acknowledge_duplicate_risk":true}`},
			{right.URL + "/v1/records/" + id + "/resolve", "race-resolve-" + id, `{"note":"Synthetic confirmed receipt"}`},
			{right.URL + "/v1/records/" + id + "/dismiss", "", ""},
		}
		results := make([]recoveryHTTPResult, len(requests))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, req := range requests {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i] = recoveryPost(client, req.target, req.key, req.body)
			}()
		}
		close(start)
		wg.Wait()
		winner := -1
		for i, result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.status == 200 || result.status == 202 {
				if winner != -1 {
					t.Fatalf("round %d: multiple winners %+v", round, results)
				}
				winner = i
			} else if result.status != 409 && result.status != 422 {
				t.Fatalf("round %d: unexpected result %+v", round, result)
			}
		}
		after, err := replica.Get(t.Context(), id)
		if winner == -1 || err != nil || len(after.History) != len(before.History)+1 {
			t.Fatalf("round %d winner %d detail %+v error %v", round, winner, after, err)
		}
		var winnerName string
		switch winner {
		case 0:
			winnerName = "retry"
			if after.DeliveryStatus != store.StatusRetrying || len(after.Runs) != 2 || after.DismissedAt != nil {
				t.Fatal(after)
			}
			// Finish the winner so the next round's fixture claim stays isolated.
			claimed, err := replica.ClaimRuns(t.Context(), "fixture-worker", 1, time.Minute)
			if err != nil || len(claimed) != 1 || claimed[0].Record.ID != id {
				t.Fatalf("%+v %v", claimed, err)
			}
			if _, err := replica.FinalizeRun(t.Context(), store.RunOutcome{RunID: claimed[0].Run.RunID, Status: store.RunFailed, Actor: "fixture-worker"}); err != nil {
				t.Fatal(err)
			}
		case 1:
			winnerName = "resolve"
			if after.DeliveryStatus != store.StatusResolved || after.ResolutionSource != store.ResolutionOperator || len(after.Runs) != 1 {
				t.Fatal(after)
			}
		case 2:
			winnerName = "dismiss"
			if after.DismissedAt == nil || after.DeliveryStatus != store.StatusUnknown || after.HasArtifacts || len(after.Runs) != 1 {
				t.Fatal(after)
			}
		}
		t.Logf("round=%d winner=%s statuses=%d/%d/%d", round, winnerName, results[0].status, results[1].status, results[2].status)
	}
}
