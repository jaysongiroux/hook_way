package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func contractDocument(t *testing.T) map[string]any {
	t.Helper()
	data, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
func assertContractBody(t *testing.T, doc map[string]any, name string, body []byte) {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	const location = "https://hookway.invalid/openapi.json"
	if err := c.AddResource(location, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(location + "#/components/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err = json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(value); err != nil {
		t.Fatalf("%s response violates contract: %v\n%s", name, err, body)
	}
}
func TestReplayContractResponsesAndRouting(t *testing.T) {
	doc := contractDocument(t)
	now := time.Now().UTC()
	fs := &fakeStore{
		list: func(store.Filter) (store.Page, error) { return store.Page{}, nil },
		get: func(string) (store.RecordDetail, error) {
			return store.RecordDetail{Record: store.Record{RecordMetadata: replaywire.RecordMetadata{ID: testID, Kind: "failure", ReplayMode: "resume"}, DeliveryStatus: store.StatusOpen}}, nil
		},
		requestRetry: func(store.RetryRequest) (store.RetryResult, error) { return store.RetryResult{RunID: testRunID}, nil },
		dismiss: func(string, string) (store.Record, error) {
			return store.Record{RecordMetadata: testMetadata(), DeliveryStatus: store.StatusOpen, DismissedAt: &now}, nil
		},
		getRun: func(string) (store.Run, error) {
			return store.Run{RunID: testRunID, RecordID: testID, Status: store.RunQueued}, nil
		},
		payload: func(string, string) (store.Payload, error) {
			return store.Payload{Bytes: []byte{0, 255}, Filename: "payload.bin"}, nil
		}, ready: func() error { return nil },
	}
	h := newHarness(t, fs)
	for _, tc := range []struct {
		method, path, body, component string
		status                        int
	}{
		{"GET", "/v1/records", "", "RecordList", 200},
		{"GET", "/v1/records/" + testID, "", "RecordDetail", 200},
		{"POST", "/v1/records/" + testID + "/retries", "", "RetryResponse", 202},
		{"POST", "/v1/retries", `{"record_ids":["` + testID + `"]}`, "BatchRetryResponse", 200},
		{"POST", "/v1/records/" + testID + "/dismiss", "", "RecordSummary", 200},
		{"GET", "/v1/retries/" + testRunID, "", "Run", 200},
	} {
		t.Run(tc.component, func(t *testing.T) {
			rec := h.do(t, tc.method, tc.path, opsToken, strings.NewReader(tc.body), http.Header{"Idempotency-Key": []string{"contract-test"}})
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			assertContractBody(t, doc, tc.component, rec.Body.Bytes())
		})
	}
	for _, route := range Routes() {
		h := newHarness(t, fs)
		wrong := h.do(t, "DELETE", strings.NewReplacer("{id}", testID, "{run_id}", testRunID).Replace(route.Path), "", nil, nil)
		if wrong.Code != 405 || !strings.HasPrefix(wrong.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("method rejection changed for %s: %d %s", route.Pattern(), wrong.Code, wrong.Body.String())
		}
		if route.Scope == "" {
			continue
		}
		rec := h.do(t, route.Method, strings.NewReplacer("{id}", testID, "{run_id}", testRunID).Replace(route.Path), "", nil, nil)
		if rec.Code != 401 {
			t.Fatalf("unregistered/unguarded %s: %d", route.Pattern(), rec.Code)
		}
		assertContractBody(t, doc, "Problem", rec.Body.Bytes())
	}
}
func TestRunContractPreservesHistoricalResultsAndNull(t *testing.T) {
	doc := contractDocument(t)
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"future_key":{"new":true},"reason":"future_reason"}`), json.RawMessage(`[1,"historic"]`)} {
		data, err := json.Marshal(runOf(store.Run{Result: raw, Status: store.RunQueued}))
		if err != nil {
			t.Fatal(err)
		}
		assertContractBody(t, doc, "Run", data)
		var decoded map[string]json.RawMessage
		if err = json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		want := string(raw)
		if raw == nil {
			want = "null"
		}
		if string(decoded["result"]) != want {
			t.Fatalf("result rewritten: %s != %s", decoded["result"], want)
		}
	}
}
func TestReplayContractConstraintsMatchHandlers(t *testing.T) {
	doc := contractDocument(t)
	paths := doc["paths"].(map[string]any)
	for _, r := range Routes() {
		operation := paths[r.Path].(map[string]any)[strings.ToLower(r.Method)].(map[string]any)
		if operation["operationId"] != r.OperationID {
			t.Fatal(r.Pattern())
		}
		if r.Scope != "" && operation["x-required-scope"] != r.Scope {
			t.Fatal(r.Pattern())
		}
	}
	// Compile every DTO component, catching invalid JSON Schema or broken refs.
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("https://hookway.invalid/spec", doc); err != nil {
		t.Fatal(err)
	}
	for name := range doc["components"].(map[string]any)["schemas"].(map[string]any) {
		if _, err := c.Compile("https://hookway.invalid/spec#/components/schemas/" + name); err != nil {
			t.Fatal(name, err)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	codes := schemas["Problem"].(map[string]any)["x-code-statuses"].(map[string]any)
	for _, e := range storeErrors {
		if codes[e.code] != float64(e.status) {
			t.Fatalf("error table drift: %s", e.code)
		}
	}
}

func TestReplayContractNullRetryBody(t *testing.T) {
	doc := contractDocument(t)
	h := newHarness(t, &fakeStore{requestRetry: func(req store.RetryRequest) (store.RetryResult, error) {
		if req.AcknowledgeDuplicateRisk {
			t.Fatal("null changed acknowledgement")
		}
		return store.RetryResult{RunID: testRunID}, nil
	}})
	rec := h.do(t, "POST", "/v1/records/"+testID+"/retries", opsToken, strings.NewReader("null"), http.Header{"Idempotency-Key": []string{"null-body"}})
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	assertContractBody(t, doc, "RetryResponse", rec.Body.Bytes())
	requestSchema := doc["paths"].(map[string]any)["/v1/records/{id}/retries"].(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("https://hookway.invalid/null-request", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("https://hookway.invalid/null-request#/paths/~1v1~1records~1{id}~1retries/post/requestBody/content/application~1json/schema")
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(nil); err != nil {
		t.Fatalf("single retry request schema excludes null: %v (%v)", err, requestSchema)
	}
	rec = h.do(t, "POST", "/v1/retries", opsToken, strings.NewReader("null"), http.Header{"Idempotency-Key": []string{"batch-null"}})
	if rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	assertContractBody(t, doc, "Problem", rec.Body.Bytes())
}

func TestReplayContractCaptureBatchAndBinaryTransport(t *testing.T) {
	doc := contractDocument(t)
	fs := &fakeStore{ingest: func(store.NewRecord) (store.IngestResult, error) { return store.Created, nil }, requestRetry: func(req store.RetryRequest) (store.RetryResult, error) {
		if req.RecordID == "missing" {
			return store.RetryResult{}, store.ErrNotFound
		}
		return store.RetryResult{RunID: testRunID}, nil
	}, payload: func(string, string) (store.Payload, error) {
		return store.Payload{Bytes: []byte{0, 255, 13, 10}, ContentType: "image/png"}, nil
	}}
	h := newHarness(t, fs)
	for _, status := range []int{201, 200} {
		if status == 200 {
			fs.ingest = func(store.NewRecord) (store.IngestResult, error) { return store.Duplicate, nil }
		}
		body, header := multipartBody(t, metadataPart(t, testMetadata()), part{replaywire.PartSnapshot, testSnapshot}, part{replaywire.PartPayload, testPayload})
		rec := h.do(t, "POST", "/v1/records", captureToken, body, header)
		if rec.Code != status {
			t.Fatalf("capture %d: %d %s", status, rec.Code, rec.Body.String())
		}
		assertContractBody(t, doc, "CaptureResponse", rec.Body.Bytes())
	}
	rec := h.do(t, "POST", "/v1/retries", opsToken, strings.NewReader(`{"record_ids":["`+testID+`","missing"]}`), http.Header{"Idempotency-Key": []string{"mixed"}})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	assertContractBody(t, doc, "BatchRetryResponse", rec.Body.Bytes())
	var batch batchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Results[0].Status != 202 || batch.Results[1].Status != 404 {
		t.Fatal(batch)
	}
	rec = h.do(t, "POST", "/v1/records/"+testID+"/retries", opsToken, strings.NewReader(`{"unknown":true}`), http.Header{"Idempotency-Key": []string{"strict"}})
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	assertContractBody(t, doc, "Problem", rec.Body.Bytes())
	rec = h.do(t, "GET", "/v1/records/"+testID+"/payload", opsToken, nil, nil)
	if rec.Code != 200 || rec.Body.String() != string([]byte{0, 255, 13, 10}) || rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Header().Get("X-Hookway-Payload-Content-Type") != "image/png" {
		t.Fatalf("binary payload transport changed: %d %q %v", rec.Code, rec.Body.Bytes(), rec.Header())
	}
}
