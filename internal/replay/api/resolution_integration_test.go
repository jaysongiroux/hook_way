package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

func TestMain(m *testing.M) { os.Exit(replaytest.Main(m)) }

func TestResolutionHTTPAgainstSharedPostgres(t *testing.T) {
	ctx := t.Context()
	url := replaytest.Database(t)
	open := func() *store.Store {
		s, err := store.Open(ctx, store.Options{URL: url, MaxConns: 4, Limits: store.Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	first := open()
	if _, err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second := open()
	f := store.NewRecord{Metadata: testMetadata(), Snapshot: testSnapshot, Payload: testPayload, Actor: "core-capture"}
	if _, err := first.Ingest(ctx, f); err != nil {
		t.Fatal(err)
	}
	requested, err := first.RequestRetry(ctx, store.RetryRequest{RecordID: testID, Actor: "ops", IdempotencyKey: "unknown-run"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := first.ClaimRuns(ctx, "worker", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("%+v %v", claimed, err)
	}
	if _, err := first.FinalizeRun(ctx, store.RunOutcome{RunID: requested.RunID, Status: store.RunUnknown, Result: json.RawMessage(`{"reason":"response_lost","upstream_request_id":"req-42"}`), Actor: "worker"}); err != nil {
		t.Fatal(err)
	}
	a := newHarness(t, notFoundStore(), func(opts *Options) { opts.Store = first })
	b := newHarness(t, notFoundStore(), func(opts *Options) { opts.Store = second })
	path := "/v1/records/" + testID + "/resolve"
	key := http.Header{"Idempotency-Key": []string{"investigation-42"}}
	body := `{"note":"Destination audit confirms event receipt","reference":"INC-42"}`
	denied := a.do(t, "POST", path, readToken, strings.NewReader(body), key)
	if denied.Code != 403 {
		t.Fatal(denied.Code)
	}
	accepted := a.do(t, "POST", path, opsToken, strings.NewReader(body), key)
	repeated := b.do(t, "POST", path, opsToken, strings.NewReader(body), key)
	if accepted.Code != 200 || repeated.Code != 200 || accepted.Body.String() != repeated.Body.String() {
		t.Fatalf("%d %s / %d %s", accepted.Code, accepted.Body.String(), repeated.Code, repeated.Body.String())
	}
	assertContractBody(t, contractDocument(t), "ResolutionResponse", accepted.Body.Bytes())
	detail := b.do(t, "GET", "/v1/records/"+testID, readToken, nil, nil)
	if detail.Code != 200 {
		t.Fatal(detail.Code, detail.Body.String())
	}
	assertContractBody(t, contractDocument(t), "RecordDetail", detail.Body.Bytes())
	var d RecordDetail
	decode(t, detail, &d)
	if d.ResolutionSource != store.ResolutionOperator || d.Resolution == nil || d.Resolution.Actor != "ops" || len(d.Runs) != 1 || d.Runs[0].Status != store.RunUnknown || !d.Artifacts.Stored {
		t.Fatalf("%+v", d)
	}
	payload := b.do(t, "GET", "/v1/records/"+testID+"/payload", opsToken, nil, nil)
	if payload.Code != 200 || payload.Body.String() != string(testPayload) {
		t.Fatalf("%d %q", payload.Code, payload.Body.String())
	}
	changed := b.do(t, "POST", path, opsToken, strings.NewReader(`{"note":"Changed evidence"}`), key)
	if changed.Code != 422 || problemCode(t, changed) != "idempotency_key_mismatch" {
		t.Fatal(changed.Code, changed.Body.String())
	}
	retry := b.do(t, "POST", "/v1/records/"+testID+"/retries", opsToken, strings.NewReader(`{"acknowledge_duplicate_risk":true}`), http.Header{"Idempotency-Key": []string{"new-run"}})
	if retry.Code != 422 || problemCode(t, retry) != "not_retryable" {
		t.Fatal(retry.Code, retry.Body.String())
	}
	// A worker response arriving after resolution cannot replace operator evidence.
	result, err := second.FinalizeRun(context.Background(), store.RunOutcome{RunID: requested.RunID, Status: store.RunSucceeded, Result: json.RawMessage(`{"reason":"late_receipt"}`), Actor: "worker"})
	if err != nil || !result.Late {
		t.Fatalf("%+v %v", result, err)
	}
	final, err := first.Get(ctx, testID)
	if err != nil || final.ResolutionSource != store.ResolutionOperator || final.Runs[0].Status != store.RunUnknown || final.History[len(final.History)-1].Event != store.EventLateResult {
		t.Fatalf("%+v %v", final, err)
	}
}
