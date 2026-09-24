package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

func testRecord() store.Failure {
	at := time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC)
	return store.Failure{FailureMetadata: testMetadata(), Status: store.StatusOpen, RootID: testID,
		CapturedAt: at, ExpiresAt: at.Add(168 * time.Hour), HasArtifacts: true}
}

func TestListPassesTheFilter(t *testing.T) {
	var got store.Filter
	fs := notFoundStore()
	fs.list = func(f store.Filter) (store.Page, error) {
		got = f
		return store.Page{Items: []store.Failure{testRecord()}, NextCursor: "next"}, nil
	}
	h := newHarness(t, fs)
	q := "flow=orders&node_id=out&destination_id=archive&status=open&kind=resume&retryable=true" +
		"&since=2026-09-23T00:00:00Z&until=2026-09-24T00:00:00Z&limit=20&cursor=abc"
	rec := h.do(t, "GET", "/v1/failures?"+q, readToken, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	yes := true
	want := store.Filter{Flow: "orders", NodeID: "out", DestinationID: "archive", Status: "open", Kind: "resume", Retryable: &yes,
		Since: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), Limit: 20, Cursor: "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter = %+v\nwant     %+v", got, want)
	}
	var resp struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	decode(t, rec, &resp)
	if len(resp.Items) != 1 || resp.Items[0]["id"] != testID || resp.Items[0]["status"] != "open" || resp.NextCursor != "next" {
		t.Fatalf("response = %+v", resp)
	}
	if _, leaks := resp.Items[0]["error_message"]; leaks {
		t.Fatal("list summaries must not carry the detail-only fields")
	}
}

func TestListEmptyPageIsAnArray(t *testing.T) {
	h := newHarness(t, notFoundStore())
	rec := h.do(t, "GET", "/v1/failures", readToken, nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestListRejectsBadParameters(t *testing.T) {
	fs := notFoundStore()
	fs.list = func(f store.Filter) (store.Page, error) {
		if f.Cursor == "bad" {
			return store.Page{}, store.ErrBadCursor
		}
		t.Fatal("store reached with a bad parameter")
		return store.Page{}, nil
	}
	h := newHarness(t, fs)
	for query, code := range map[string]string{
		"bogus=1": "invalid_parameter", "flow=a&flow=b": "invalid_parameter", "status=weird": "invalid_parameter",
		"kind=weird": "invalid_parameter", "retryable=yes": "invalid_parameter", "since=yesterday": "invalid_parameter",
		"limit=0": "invalid_parameter", "limit=501": "invalid_parameter", "limit=ten": "invalid_parameter",
		"cursor=bad": "invalid_cursor",
		"flow=%00":   "invalid_parameter", "node_id=caf%E9": "invalid_parameter",
		"destination_id=" + strings.Repeat("d", 1025): "invalid_parameter",
	} {
		rec := h.do(t, "GET", "/v1/failures?"+query, readToken, nil, nil)
		if rec.Code != 400 || problemCode(t, rec) != code {
			t.Errorf("%s: status %d code %q, want 400 %s", query, rec.Code, problemCode(t, rec), code)
		}
	}
}

func TestGetReturnsTheDetail(t *testing.T) {
	fs := notFoundStore()
	fs.get = func(id string) (store.Detail, error) {
		if id != testID {
			t.Fatalf("id = %q", id)
		}
		return store.Detail{
			Failure:  testRecord(),
			Runs:     []store.Run{{RunID: testRunID, FailureID: testID, Status: store.RunQueued, RequestedBy: "ops", IdempotencyKey: "k", CreatedAt: time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC)}},
			History:  []store.Event{{Event: store.EventCaptured, Actor: "core-capture", At: time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC), Detail: json.RawMessage(`{}`)}},
			Children: []string{},
		}, nil
	}
	h := newHarness(t, fs)
	rec := h.do(t, "GET", "/v1/failures/"+testID, readToken, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	var d map[string]any
	decode(t, rec, &d)
	artifacts := d["artifacts"].(map[string]any)
	payload := artifacts["payload"].(map[string]any)
	if d["id"] != testID || artifacts["stored"] != true || payload["sha256"] != digest(testPayload) || payload["content_type"] != "application/json" {
		t.Fatalf("detail = %v", d)
	}
	runs, history, children := d["runs"].([]any), d["history"].([]any), d["children"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["result"] != nil || len(history) != 1 || len(children) != 0 {
		t.Fatalf("runs %v history %v children %v", runs, history, children)
	}
}

func TestGetNotFound(t *testing.T) {
	h := newHarness(t, notFoundStore())
	if rec := h.do(t, "GET", "/v1/failures/"+testID, readToken, nil, nil); rec.Code != 404 || problemCode(t, rec) != "not_found" {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestPayloadDownload(t *testing.T) {
	var actor string
	fs := notFoundStore()
	fs.payload = func(id, a string) (store.Payload, error) {
		actor = a
		return store.Payload{Bytes: testPayload, ContentType: "application/json", Filename: "café.json"}, nil
	}
	h := newHarness(t, fs)
	rec := h.do(t, "GET", "/v1/failures/"+testID+"/payload", opsToken, nil, nil)
	if rec.Code != 200 || rec.Body.String() != string(testPayload) || actor != "ops" {
		t.Fatalf("status %d body %q actor %q", rec.Code, rec.Body.String(), actor)
	}
	for header, want := range map[string]string{
		"Content-Type":                   "application/octet-stream",
		"Content-Disposition":            "attachment; filename*=utf-8''caf%C3%A9.json",
		"Cache-Control":                  "no-store",
		"X-Content-Type-Options":         "nosniff",
		"X-Hookway-Payload-Content-Type": "application/json",
		"Content-Length":                 "19",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestPayloadErrors(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrNotFound, 404, "not_found"},
		{store.ErrNoPayload, 404, "payload_not_captured"},
		{store.ErrGone, 410, "payload_gone"},
	} {
		fs := notFoundStore()
		fs.payload = func(string, string) (store.Payload, error) { return store.Payload{}, c.err }
		h := newHarness(t, fs)
		rec := h.do(t, "GET", "/v1/failures/"+testID+"/payload", opsToken, nil, nil)
		if rec.Code != c.status || problemCode(t, rec) != c.code {
			t.Errorf("%v: status %d code %q", c.err, rec.Code, problemCode(t, rec))
		}
	}
}

func TestPayloadCorruptIsA500(t *testing.T) {
	fs := notFoundStore()
	fs.payload = func(string, string) (store.Payload, error) { return store.Payload{}, store.ErrCorrupt }
	h := newHarness(t, fs)
	rec := h.do(t, "GET", "/v1/failures/"+testID+"/payload", opsToken, nil, nil)
	if rec.Code != 500 || problemCode(t, rec) != "payload_corrupt" {
		t.Fatalf("status %d code %q", rec.Code, problemCode(t, rec))
	}
	fams, _ := h.metrics.Registry().Gather()
	for _, f := range fams {
		if f.GetName() == "hookway_replay_payload_downloads_total" {
			for _, m := range f.GetMetric() {
				if m.GetLabel()[0].GetValue() == "corrupt" && m.GetCounter().GetValue() == 1 {
					return
				}
			}
		}
	}
	t.Fatal("payload_downloads_total{corrupt} not incremented")
}

func TestContentDisposition(t *testing.T) {
	for filename, want := range map[string]string{
		"report.pdf": `attachment; filename=report.pdf`,
		"":           `attachment; filename=` + testID + `.bin`,
		"a b.txt":    `attachment; filename="a b.txt"`,
		`..\x.txt`:   `attachment; filename=x.txt`,
	} {
		if got := contentDisposition(testID, filename); got != want {
			t.Errorf("contentDisposition(%q) = %q, want %q", filename, got, want)
		}
	}
}

func TestDismiss(t *testing.T) {
	fs := notFoundStore()
	fs.dismiss = func(id, actor string) (store.Failure, error) {
		f := testRecord()
		f.Status, f.HasArtifacts = store.StatusDismissed, false
		return f, nil
	}
	h := newHarness(t, fs)
	rec := h.do(t, "POST", "/v1/failures/"+testID+"/dismiss", opsToken, nil, nil)
	var resp map[string]any
	decode(t, rec, &resp)
	if rec.Code != 200 || resp["status"] != "dismissed" {
		t.Fatalf("status %d body %v", rec.Code, resp)
	}
	fs.dismiss = func(string, string) (store.Failure, error) { return store.Failure{}, store.ErrInvalidState }
	if rec := h.do(t, "POST", "/v1/failures/"+testID+"/dismiss", opsToken, nil, nil); rec.Code != 422 || problemCode(t, rec) != "not_dismissable" {
		t.Fatalf("status %d", rec.Code)
	}
}
