package capture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

func TestInspectionBodyFormatsAndLimits(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<root><id>1</id></root>`), transform.XMLLimits{MaxBytes: 1024, MaxDepth: 20, MaxNodes: 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		env               graph.Envelope
		encoding, content string
	}{
		{"json precision", graph.Envelope{BodyFormat: "json", Body: map[string]any{"id": json.Number("9007199254740993")}}, "utf8", `{"id":9007199254740993}`},
		{"null", graph.Envelope{BodyFormat: "json"}, "utf8", `null`},
		{"empty raw", graph.Envelope{BodyFormat: "raw"}, "utf8", ""},
		{"raw", graph.Envelope{BodyFormat: "raw", V: graph.Verified{Raw: graph.RawRequest{Body: []byte("hello")}}}, "utf8", "hello"},
		{"binary", graph.Envelope{BodyFormat: "raw", V: graph.Verified{Raw: graph.RawRequest{Body: []byte{0, 255}}}}, "base64", base64.StdEncoding.EncodeToString([]byte{0, 255})},
		{"xml", graph.Envelope{BodyFormat: "xml", Body: doc}, "utf8", "<root>"},
		{"multipart", graph.Envelope{BodyFormat: "multipart", Body: map[string]any{"parts": []any{map[string]any{"content": "YQ=="}}}}, "utf8", `"content":"YQ=="`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBuilder(t, 1<<20)
			rec := b.FromInspection("orders", "out", testHead, tc.env)
			rec.Metadata.InboundStatus = 503
			if err := rec.Metadata.Validate(); err != nil {
				t.Fatal(err)
			}
			if rec.Snapshot != nil || rec.Metadata.Retryable {
				t.Fatal("inspection has executable state")
			}
			var payload replaywire.InspectionPayload
			if err := json.Unmarshal(rec.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Body.Encoding != tc.encoding || !strings.Contains(payload.Body.Content, tc.content) {
				t.Fatalf("body: %+v", payload.Body)
			}
			rec = testBuilder(t, 8).FromInspection("orders", "out", testHead, tc.env)
			if rec.Metadata.PayloadUnavailableReason != replaywire.NotRetryableTooLarge || rec.Payload != nil {
				t.Fatal("oversize capture retained payload")
			}
		})
	}
}

func TestInspectionRedactionAndImmutableCaptureAfterNon2xx(t *testing.T) {
	var mu sync.Mutex
	var records []Record
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		var rec Record
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "metadata":
				if err := json.Unmarshal(data, &rec.Metadata); err != nil {
					t.Error(err)
				}
			case "payload":
				rec.Payload = data
			default:
				t.Errorf("unexpected part %s", part.FormName())
			}
		}
		mu.Lock()
		records = append(records, rec)
		mu.Unlock()
		w.WriteHeader(201)
	}))
	defer server.Close()
	cfg := captureConfig(t, server.URL, "    capture_failures: false")
	flow := cfg.Flows["orders"]
	flow.Nodes = append(flow.Nodes, graph.NodeConfig{ID: "look", Type: "inspect"})
	cfg.Flows["orders"] = flow
	cfg.Security.RedactedHeaderDenylist = []string{"X-Partner"}
	cfg.Credentials = map[string]flowconfig.CredentialProfile{"external": {Kind: "static", Headers: map[string]flowconfig.CredentialHeader{"X-Cred": {}}}}
	c, err := New(Options{Config: cfg, Metrics: newFakeMetrics()})
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope()
	env.Body = map[string]any{"id": json.Number("9007199254740993")}
	env.BodyFormat = "json"
	env.Vars = map[string]string{"route": "before"}
	env.V.Raw.Head.Header = http.Header{"Authorization": {"secret"}, "X-Partner": {"private"}, "Content-Type": {"application/json"}, "X-Cred": {"customcredential"}}
	req := c.Begin("orders", testHead)
	req.OnInspection("look", env)
	env.Body.(map[string]any)["id"] = "after"
	env.Vars["route"] = "after"
	env.V.Raw.Head.Header.Set("Content-Type", "changed")
	mu.Lock()
	count := len(records)
	mu.Unlock()
	if count != 0 {
		t.Fatal("uploaded before settlement")
	}
	req.Settle(503)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records=%d", len(records))
	}
	rec := records[0]
	if rec.Metadata.InboundStatus != 503 || rec.Metadata.Kind != "inspection" {
		t.Fatal(rec.Metadata)
	}
	var payload replaywire.InspectionPayload
	if err := json.Unmarshal(rec.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	// Captured headers follow the wire policy's lowercase keys, rather than
	// http.Header's canonical-key lookup convention.
	if payload.Body.Content != `{"id":9007199254740993}` || payload.Variables["route"] != "before" || map[string][]string(payload.Headers)["content-type"][0] != "application/json" {
		t.Fatalf("mutable snapshot: %+v", payload)
	}
	if bytes.Contains(rec.Payload, []byte("secret")) || bytes.Contains(rec.Payload, []byte("private")) || bytes.Contains(rec.Payload, []byte("customcredential")) {
		t.Fatalf("credentials leaked")
	}
}

func TestPendingInspectionBufferBoundAndCloseBeforeSettlement(t *testing.T) {
	m := newFakeMetrics()
	c := newTestCapturer(t, "http://127.0.0.1:1", "    capture_failures: false", m)
	c.inspectEnabled["orders"] = true
	req := c.Begin("orders", testHead)
	for range maxPendingRecords + 10 {
		req.OnInspection("look", testEnvelope())
	}
	if m.count("dropped", DropBufferFull) != 10 {
		t.Fatal("buffer drops not counted")
	}
	if len(req.inspections) != maxPendingRecords {
		t.Fatalf("pending=%d", len(req.inspections))
	}
	if c.uploader.records != maxPendingRecords || c.uploader.bytes > maxPendingBytes {
		t.Fatal("reservation exceeded limits")
	}
	_ = c.Close()
	req.Settle(202)
	if m.count("dropped", DropShutdown) != maxPendingRecords {
		t.Fatal("shutdown drops not counted")
	}
	if c.uploader.records != 0 || c.uploader.bytes != 0 {
		t.Fatal("shutdown leaked reservations")
	}
}

func TestLargePendingInspectionsHitByteBudgetBeforeRecordLimit(t *testing.T) {
	m := newFakeMetrics()
	c := newTestCapturer(t, "http://127.0.0.1:1", "    capture_failures: false", m)
	defer func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	}()
	c.inspectEnabled["orders"] = true
	env := testEnvelope()
	env.BodyFormat = "raw"
	env.V.Raw.Body = bytes.Repeat([]byte("x"), 4<<20)
	req := c.Begin("orders", testHead)
	for range 20 {
		req.OnInspection("look", env)
	}
	if n := len(req.inspections); n == 0 || n >= 20 || n >= maxPendingRecords || m.count("dropped", DropBufferFull) != 20-n {
		t.Fatalf("byte budget did not bound capture: pending=%d dropped=%d", n, m.count("dropped", DropBufferFull))
	}
	if c.uploader.bytes > maxPendingBytes {
		t.Fatalf("pending memory exceeded %d bytes: %d", maxPendingBytes, c.uploader.bytes)
	}
	_ = c.Close()
	req.Settle(202)
	if c.uploader.records != 0 || c.uploader.bytes != 0 {
		t.Fatal("large inspection reservations leaked at shutdown")
	}
}

func (m *fakeMetrics) InspectionUploaded(flow string)        { m.CaptureUploaded(flow) }
func (m *fakeMetrics) InspectionDropped(flow, reason string) { m.CaptureDropped(flow, reason) }
