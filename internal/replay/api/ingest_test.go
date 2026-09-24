package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

type part struct {
	name string
	data []byte
}

func multipartBody(t *testing.T, parts ...part) (*bytes.Buffer, http.Header) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+p.name+`"`)
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(p.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, http.Header{"Content-Type": {w.FormDataContentType()}}
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var (
	testSnapshot = []byte("gzip-snapshot-bytes")
	testPayload  = []byte(`{"event":"created"}`)
)

func testMetadata() replaywire.FailureMetadata {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	return replaywire.FailureMetadata{
		ID: testID, Kind: replaywire.KindResume, Flow: "orders", NodeID: "out", NodeType: "publish",
		DestinationID: "archive", DestinationType: "s3", Target: "uploads", RenderedTarget: "uploads",
		EventID: "evt-1", ReceivedAt: at, FailedAt: at.Add(time.Second), InboundStatus: 202,
		ErrorClass: replaywire.ErrorClass5xx, Attempts: 1, Retryable: true,
		Snapshot: &replaywire.ArtifactInfo{Size: int64(len(testSnapshot)), SHA256: digest(testSnapshot), Version: 1},
		Payload:  &replaywire.ArtifactInfo{Size: int64(len(testPayload)), SHA256: digest(testPayload), ContentType: "application/json"},
	}
}

func metadataPart(t *testing.T, m replaywire.FailureMetadata) part {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return part{replaywire.PartMetadata, b}
}

func captureCount(h *harness, outcome string) float64 {
	fams, _ := h.metrics.Registry().Gather()
	for _, f := range fams {
		if f.GetName() != "hookway_replay_captures_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "outcome" && l.GetValue() == outcome {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func TestIngestCreatesARecord(t *testing.T) {
	var got store.NewFailure
	fs := notFoundStore()
	fs.ingest = func(nf store.NewFailure) (store.IngestResult, error) { got = nf; return store.Created, nil }
	h := newHarness(t, fs)
	body, header := multipartBody(t, metadataPart(t, testMetadata()), part{replaywire.PartSnapshot, testSnapshot}, part{replaywire.PartPayload, testPayload})
	rec := h.do(t, "POST", "/v1/failures", captureToken, body, header)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	decode(t, rec, &resp)
	if resp["id"] != testID {
		t.Fatalf("response = %v", resp)
	}
	if !reflect.DeepEqual(got.Metadata, testMetadata()) || !bytes.Equal(got.Snapshot, testSnapshot) || !bytes.Equal(got.Payload, testPayload) || got.Actor != "core-capture" {
		t.Fatalf("store got %+v", got)
	}
	if captureCount(h, "created") != 1 {
		t.Fatal("captures_total{created} not incremented")
	}
}

func TestIngestDuplicateIs200(t *testing.T) {
	fs := notFoundStore()
	fs.ingest = func(store.NewFailure) (store.IngestResult, error) { return store.Duplicate, nil }
	h := newHarness(t, fs)
	body, header := multipartBody(t, metadataPart(t, testMetadata()), part{replaywire.PartSnapshot, testSnapshot}, part{replaywire.PartPayload, testPayload})
	if rec := h.do(t, "POST", "/v1/failures", captureToken, body, header); rec.Code != http.StatusOK || captureCount(h, "duplicate") != 1 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestIngestMetadataOnly(t *testing.T) {
	var got store.NewFailure
	fs := notFoundStore()
	fs.ingest = func(nf store.NewFailure) (store.IngestResult, error) { got = nf; return store.Created, nil }
	h := newHarness(t, fs)
	m := testMetadata()
	m.Retryable, m.NotRetryableReason, m.Snapshot, m.Payload = false, replaywire.NotRetryableTooLarge, nil, nil
	body, header := multipartBody(t, metadataPart(t, m))
	if rec := h.do(t, "POST", "/v1/failures", captureToken, body, header); rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if got.Snapshot != nil || got.Payload != nil {
		t.Fatalf("artifacts = %q %q, want none", got.Snapshot, got.Payload)
	}
}

func TestIngestMapsStoreErrors(t *testing.T) {
	for _, c := range []struct {
		err           error
		status        int
		code, outcome string
	}{
		{store.ErrConflict, http.StatusConflict, "conflict", "conflict"},
		{store.ErrCapacity, http.StatusInsufficientStorage, "capacity_exceeded", "capacity"},
	} {
		fs := notFoundStore()
		fs.ingest = func(store.NewFailure) (store.IngestResult, error) { return 0, c.err }
		h := newHarness(t, fs)
		body, header := multipartBody(t, metadataPart(t, testMetadata()), part{replaywire.PartSnapshot, testSnapshot}, part{replaywire.PartPayload, testPayload})
		rec := h.do(t, "POST", "/v1/failures", captureToken, body, header)
		if rec.Code != c.status || problemCode(t, rec) != c.code || captureCount(h, c.outcome) != 1 {
			t.Errorf("%v: status %d code %q", c.err, rec.Code, problemCode(t, rec))
		}
	}
}

func TestIngestRejects(t *testing.T) {
	good := func(t *testing.T) []part {
		return []part{metadataPart(t, testMetadata()), {replaywire.PartSnapshot, testSnapshot}, {replaywire.PartPayload, testPayload}}
	}
	withMeta := func(mutate func(*replaywire.FailureMetadata)) func(t *testing.T) []part {
		return func(t *testing.T) []part {
			m := testMetadata()
			mutate(&m)
			return []part{metadataPart(t, m), {replaywire.PartSnapshot, testSnapshot}, {replaywire.PartPayload, testPayload}}
		}
	}
	cases := map[string]struct {
		parts   func(t *testing.T) []part
		status  int
		code    string
		outcome string
	}{
		"missing metadata": {func(*testing.T) []part {
			return []part{{replaywire.PartSnapshot, testSnapshot}, {replaywire.PartPayload, testPayload}}
		}, 400, "invalid_metadata", "invalid"},
		"metadata not json": {func(*testing.T) []part { return []part{{replaywire.PartMetadata, []byte("{")}} }, 400, "invalid_metadata", "invalid"},
		"metadata unknown field": {func(*testing.T) []part {
			return []part{{replaywire.PartMetadata, []byte(`{"id":"x","bogus":1}`)}}
		}, 400, "invalid_metadata", "invalid"},
		"metadata trailing data": {func(t *testing.T) []part {
			p := metadataPart(t, testMetadata())
			p.data = append(p.data, []byte(" {}")...)
			return []part{p}
		}, 400, "invalid_metadata", "invalid"},
		"metadata invalid": {withMeta(func(m *replaywire.FailureMetadata) { m.InboundStatus = 503 }), 400, "invalid_metadata", "invalid"},
		"repeated part":    {func(t *testing.T) []part { return append(good(t), part{replaywire.PartPayload, testPayload}) }, 400, "invalid_request", "invalid"},
		"unexpected part":  {func(t *testing.T) []part { return append(good(t), part{"extra", []byte("x")}) }, 400, "invalid_request", "invalid"},
		"snapshot too large": {func(t *testing.T) []part {
			return []part{metadataPart(t, testMetadata()), {replaywire.PartSnapshot, bytes.Repeat([]byte("s"), 1025)}}
		}, 413, "record_too_large", "too_large"},
		"size mismatch": {withMeta(func(m *replaywire.FailureMetadata) { m.Payload.Size++ }), 400, "artifact_mismatch", "invalid"},
		"digest mismatch": {withMeta(func(m *replaywire.FailureMetadata) {
			m.Snapshot.SHA256 = strings.Repeat("0", 64)
		}), 400, "artifact_mismatch", "invalid"},
		"declared part missing": {func(t *testing.T) []part {
			return []part{metadataPart(t, testMetadata()), {replaywire.PartSnapshot, testSnapshot}}
		}, 400, "artifact_mismatch", "invalid"},
		"undeclared part present": {func(t *testing.T) []part {
			m := testMetadata()
			m.Retryable, m.NotRetryableReason, m.Snapshot, m.Payload = false, replaywire.NotRetryableTooLarge, nil, nil
			return []part{metadataPart(t, m), {replaywire.PartSnapshot, testSnapshot}}
		}, 400, "artifact_mismatch", "invalid"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fs := notFoundStore()
			fs.ingest = func(store.NewFailure) (store.IngestResult, error) { t.Fatal("store reached"); return 0, nil }
			h := newHarness(t, fs)
			body, header := multipartBody(t, c.parts(t)...)
			rec := h.do(t, "POST", "/v1/failures", captureToken, body, header)
			if rec.Code != c.status || problemCode(t, rec) != c.code {
				t.Fatalf("status %d code %q, want %d %q (body %q)", rec.Code, problemCode(t, rec), c.status, c.code, rec.Body.String())
			}
			if captureCount(h, c.outcome) != 1 {
				t.Fatalf("captures_total{%s} not incremented", c.outcome)
			}
		})
	}
}

func TestIngestRejectsANonMultipartBody(t *testing.T) {
	h := newHarness(t, notFoundStore())
	rec := h.do(t, "POST", "/v1/failures", captureToken, strings.NewReader("{}"), http.Header{"Content-Type": {"application/json"}})
	if rec.Code != 400 || problemCode(t, rec) != "invalid_request" {
		t.Fatalf("status %d code %q", rec.Code, problemCode(t, rec))
	}
}

func TestIngestRejectsAnOversizedBody(t *testing.T) {
	h := newHarness(t, notFoundStore())
	body, header := multipartBody(t, metadataPart(t, testMetadata()))
	// A preamble before the first boundary still counts toward the body
	// limit. Short lines, so the multipart reader reads it rather than
	// failing on a line longer than its buffer.
	padded := bytes.NewBuffer(append(bytes.Repeat([]byte("preamble\r\n"), 220000), body.Bytes()...))
	rec := h.do(t, "POST", "/v1/failures", captureToken, padded, header)
	if rec.Code != http.StatusRequestEntityTooLarge || problemCode(t, rec) != "request_too_large" {
		t.Fatalf("status %d code %q", rec.Code, problemCode(t, rec))
	}
}
