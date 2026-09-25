package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

func keyHeader(key string) http.Header { return http.Header{"Idempotency-Key": {key}} }

func TestRetryRequiresAValidIdempotencyKey(t *testing.T) {
	h := newHarness(t, notFoundStore())
	if rec := h.do(t, "POST", "/v1/failures/"+testID+"/retries", opsToken, nil, nil); rec.Code != 400 || problemCode(t, rec) != "idempotency_key_required" {
		t.Fatalf("no key: status %d", rec.Code)
	}
	if rec := h.do(t, "POST", "/v1/failures/"+testID+"/retries", opsToken, nil, keyHeader("has space")); rec.Code != 400 || problemCode(t, rec) != "invalid_request" {
		t.Fatalf("bad key: status %d", rec.Code)
	}
}

func TestRetryQueuesARun(t *testing.T) {
	var got store.RetryRequest
	fs := notFoundStore()
	fs.requestRetry = func(r store.RetryRequest) (store.RetryResult, error) {
		got = r
		return store.RetryResult{RunID: testRunID, Created: true}, nil
	}
	h := newHarness(t, fs)
	rec := h.do(t, "POST", "/v1/failures/"+testID+"/retries", opsToken, strings.NewReader(`{"acknowledge_duplicate_risk": true}`), keyHeader("key-1"))
	var resp map[string]string
	decode(t, rec, &resp)
	if rec.Code != http.StatusAccepted || resp["run_id"] != testRunID {
		t.Fatalf("status %d body %v", rec.Code, resp)
	}
	want := store.RetryRequest{FailureID: testID, Actor: "ops", IdempotencyKey: "key-1", AcknowledgeDuplicateRisk: true}
	if got != want {
		t.Fatalf("request = %+v, want %+v", got, want)
	}
}

func TestRetryAcceptsAnEmptyBody(t *testing.T) {
	var got store.RetryRequest
	fs := notFoundStore()
	fs.requestRetry = func(r store.RetryRequest) (store.RetryResult, error) {
		got = r
		return store.RetryResult{RunID: testRunID}, nil
	}
	h := newHarness(t, fs)
	if rec := h.do(t, "POST", "/v1/failures/"+testID+"/retries", opsToken, nil, keyHeader("key-1")); rec.Code != http.StatusAccepted || got.AcknowledgeDuplicateRisk {
		t.Fatalf("status %d request %+v", rec.Code, got)
	}
}

func TestRetryRejectsUnknownBodyFields(t *testing.T) {
	h := newHarness(t, notFoundStore())
	rec := h.do(t, "POST", "/v1/failures/"+testID+"/retries", opsToken, strings.NewReader(`{"ack": true}`), keyHeader("key-1"))
	if rec.Code != 400 || problemCode(t, rec) != "invalid_request" {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestRetryMapsStoreErrors(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrNotFound, 404, "not_found"},
		{store.ErrRunActive, 409, "run_active"},
		{store.ErrNotRetryable, 422, "not_retryable"},
		{store.ErrDuplicateRiskUnacknowledged, 422, "duplicate_risk_unacknowledged"},
		{store.ErrIdempotencyKeyMismatch, 422, "idempotency_key_mismatch"},
	} {
		fs := notFoundStore()
		fs.requestRetry = func(store.RetryRequest) (store.RetryResult, error) { return store.RetryResult{}, c.err }
		h := newHarness(t, fs)
		rec := h.do(t, "POST", "/v1/failures/"+testID+"/retries", opsToken, nil, keyHeader("key-1"))
		if rec.Code != c.status || problemCode(t, rec) != c.code {
			t.Errorf("%v: status %d code %q", c.err, rec.Code, problemCode(t, rec))
		}
	}
}

func TestBatchRetry(t *testing.T) {
	ids := []string{"01926d3c-0001-7b3a-9c1e-2f4a5b6c7d8e", "01926d3c-0002-7b3a-9c1e-2f4a5b6c7d8e", "01926d3c-0003-7b3a-9c1e-2f4a5b6c7d8e"}
	var keys []string
	fs := notFoundStore()
	fs.requestRetry = func(r store.RetryRequest) (store.RetryResult, error) {
		keys = append(keys, r.IdempotencyKey)
		switch r.FailureID {
		case ids[0]:
			return store.RetryResult{RunID: testRunID, Created: true}, nil
		case ids[1]:
			return store.RetryResult{}, store.ErrRunActive
		}
		return store.RetryResult{}, store.ErrNotFound
	}
	h := newHarness(t, fs)
	body := `{"failure_ids": ["` + strings.Join(ids, `","`) + `"]}`
	rec := h.do(t, "POST", "/v1/retries", opsToken, strings.NewReader(body), keyHeader("batch-1"))
	var resp struct {
		Results []struct {
			FailureID string `json:"failure_id"`
			Status    int    `json:"status"`
			RunID     string `json:"run_id"`
			Code      string `json:"code"`
		} `json:"results"`
	}
	decode(t, rec, &resp)
	if rec.Code != 200 || len(resp.Results) != 3 {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if r := resp.Results[0]; r.Status != 202 || r.RunID != testRunID {
		t.Errorf("item 0 = %+v", r)
	}
	if r := resp.Results[1]; r.Status != 409 || r.Code != "run_active" {
		t.Errorf("item 1 = %+v", r)
	}
	if r := resp.Results[2]; r.Status != 404 || r.Code != "not_found" {
		t.Errorf("item 2 = %+v", r)
	}
	for i, k := range keys {
		if k != "batch-1:"+ids[i] {
			t.Errorf("item %d key = %q, want batch-1:<id>", i, k)
		}
	}
}

func TestBatchRetryRejects(t *testing.T) {
	h := newHarness(t, notFoundStore())
	many := make([]string, 101)
	for i := range many {
		many[i] = `"` + testID + `"`
	}
	for name, body := range map[string]string{
		"missing body": "", "empty list": `{"failure_ids": []}`, "too many": `{"failure_ids": [` + strings.Join(many, ",") + `]}`,
		"duplicates": `{"failure_ids": ["` + testID + `","` + testID + `"]}`, "unknown field": `{"ids": []}`,
	} {
		rec := h.do(t, "POST", "/v1/retries", opsToken, strings.NewReader(body), keyHeader("batch-1"))
		if rec.Code != 400 || problemCode(t, rec) != "invalid_request" {
			t.Errorf("%s: status %d code %q", name, rec.Code, problemCode(t, rec))
		}
	}
}

func TestGetRun(t *testing.T) {
	fs := notFoundStore()
	fs.getRun = func(id string) (store.Run, error) {
		return store.Run{RunID: id, FailureID: testID, Status: store.RunQueued, RequestedBy: "ops", IdempotencyKey: "k",
			CreatedAt: time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC)}, nil
	}
	h := newHarness(t, fs)
	rec := h.do(t, "GET", "/v1/retries/"+testRunID, readToken, nil, nil)
	var resp map[string]any
	decode(t, rec, &resp)
	if rec.Code != 200 || resp["run_id"] != testRunID || resp["status"] != "queued" || resp["result"] != nil || resp["finished_at"] != nil {
		t.Fatalf("status %d body %v", rec.Code, resp)
	}
}
