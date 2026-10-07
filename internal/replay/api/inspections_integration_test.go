package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestInspectionHTTPAgainstPostgres(t *testing.T) {
	db, err := store.Open(t.Context(), store.Options{URL: replaytest.Database(t), MaxConns: 4, Limits: store.Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, notFoundStore(), func(o *Options) { o.Store = db })
	payload := []byte(`{"version":1,"body":{"content_type":"application/json","encoding":"utf8","content":"{\"id\":9007199254740993}"},"headers":{},"variables":{},"method":"POST","path":"/events"}`)
	m := inspectionMetadata()
	m.Payload = &replaywire.ArtifactInfo{Size: int64(len(payload)), SHA256: digest(payload), ContentType: "application/json"}
	t.Logf("POST /v1/records inspection metadata example: %s", metadataPart(t, m).data)
	for _, status := range []int{201, 200} {
		body, headers := multipartBody(t, metadataPart(t, m), part{replaywire.PartPayload, payload})
		if rec := h.do(t, "POST", "/v1/records", captureToken, body, headers); rec.Code != status {
			t.Fatalf("ingest %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := h.do(t, "GET", "/v1/records", readToken, nil, nil)
	var list RecordList
	decode(t, rec, &list)
	if rec.Code != 200 || len(list.Items) != 1 {
		t.Fatalf("failure list: %d %s", rec.Code, rec.Body.String())
	}
	// A generic unfiltered list contains both capture purposes.
	failure := testMetadata()
	failure.ID = "01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8f"
	failure.OccurredAt = m.OccurredAt
	t.Logf("POST /v1/records failure metadata example: %s", metadataPart(t, failure).data)
	failureBody, failureHeaders := multipartBody(t, metadataPart(t, failure), part{replaywire.PartSnapshot, testSnapshot}, part{replaywire.PartPayload, testPayload})
	if response := h.do(t, "POST", "/v1/records", captureToken, failureBody, failureHeaders); response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	for query, count := range map[string]int{"": 2, "?kind=failure": 1, "?replay_mode=resume": 1, "?delivery_status=open": 1, "?dismissed=false": 2, "?dismissed=true": 0} {
		response := h.do(t, "GET", "/v1/records"+query, readToken, nil, nil)
		var records RecordList
		decode(t, response, &records)
		if response.Code != 200 || len(records.Items) != count {
			t.Fatalf("mixed list %s: %d %s", query, response.Code, response.Body.String())
		}
		if query == "" {
			t.Logf("GET /v1/records example: %s", response.Body.String())
		}
	}
	rec = h.do(t, "GET", "/v1/records?kind=inspection", readToken, nil, nil)
	decode(t, rec, &list)
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0].Actions.Retry.Allowed {
		t.Fatalf("inspections: %d %s", rec.Code, rec.Body.String())
	}
	rec = h.do(t, "GET", "/v1/records/"+testID, readToken, nil, nil)
	var detail RecordDetail
	decode(t, rec, &detail)
	if rec.Code != 200 || detail.OccurredAt.IsZero() || detail.Artifacts.Snapshot != nil || !detail.Artifacts.Stored || detail.InboundStatus != 503 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	path := "/v1/records/" + testID + "/payload"
	if rec := h.do(t, "GET", path, readToken, nil, nil); rec.Code != 403 {
		t.Fatalf("read key disclosed payload: %d", rec.Code)
	}
	if rec := h.do(t, "GET", path, opsToken, nil, nil); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	example, _ := json.Marshal(detail)
	t.Logf("GET /v1/records/{id} inspection example: %s", example)
	headers := http.Header{"Idempotency-Key": {"inspect-single"}}
	if rec := h.do(t, "POST", "/v1/records/"+testID+"/retries", opsToken, nil, headers); rec.Code != 422 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
	headers.Set("Idempotency-Key", "inspect-batch")
	rec = h.do(t, "POST", "/v1/retries", opsToken, strings.NewReader(`{"record_ids":["`+testID+`"]}`), headers)
	var batch batchResponse
	decode(t, rec, &batch)
	if rec.Code != 200 || len(batch.Results) != 1 || batch.Results[0].Status != 422 {
		t.Fatalf("batch: %d %s", rec.Code, rec.Body.String())
	}
	headers.Set("Idempotency-Key", "inspect-resolve")
	if response := h.do(t, "POST", "/v1/records/"+testID+"/resolve", opsToken, strings.NewReader(`{"note":"checked"}`), headers); response.Code != 422 {
		t.Fatal(response.Code, response.Body.String())
	}
	if rec := h.do(t, "POST", "/v1/records/"+testID+"/dismiss", opsToken, nil, nil); rec.Code != 200 {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(t, "GET", path, opsToken, nil, nil); rec.Code != 410 {
		t.Fatalf("dismissed payload: %d", rec.Code)
	}
	t.Logf("inspection API example: kind=%s, retryable=%v, reason=%s, inbound_status=%d, stored=%v, snapshot=%v", detail.Kind, detail.Retryable, detail.NotRetryableReason, detail.InboundStatus, detail.Artifacts.Stored, detail.Artifacts.Snapshot)
}
