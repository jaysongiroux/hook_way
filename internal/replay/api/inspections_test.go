package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func inspectionMetadata() replaywire.RecordMetadata {
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	return replaywire.RecordMetadata{ID: testID, Kind: replaywire.KindInspection, Flow: "orders", NodeID: "look", NodeType: "inspect", EventID: "evt", ReceivedAt: at, OccurredAt: at, InboundStatus: 503, NotRetryableReason: replaywire.NotRetryableInspectionOnly, Payload: &replaywire.ArtifactInfo{Size: int64(len(testPayload)), SHA256: digest(testPayload), ContentType: "application/json"}}
}

func TestInspectionIngestPayloadWithoutSnapshot(t *testing.T) {
	var got store.NewRecord
	fs := notFoundStore()
	fs.ingest = func(f store.NewRecord) (store.IngestResult, error) { got = f; return store.Created, nil }
	h := newHarness(t, fs)
	body, headers := multipartBody(t, metadataPart(t, inspectionMetadata()), part{replaywire.PartPayload, testPayload})
	rec := h.do(t, "POST", "/v1/records", captureToken, body, headers)
	if rec.Code != 201 || got.Metadata.Kind != "inspection" || got.Snapshot != nil || got.Payload == nil {
		t.Fatalf("ingest: %d %s %+v", rec.Code, rec.Body.String(), got)
	}
	for _, mutate := range []func(*replaywire.RecordMetadata){
		func(m *replaywire.RecordMetadata) { m.Retryable = true },
		func(m *replaywire.RecordMetadata) { m.Snapshot = testMetadata().Snapshot },
		func(m *replaywire.RecordMetadata) { m.ErrorClass = "other" },
		func(m *replaywire.RecordMetadata) { m.OccurredAt = time.Time{} },
		func(m *replaywire.RecordMetadata) { m.NotRetryableReason = "unsupported" },
	} {
		m := inspectionMetadata()
		mutate(&m)
		body, headers := multipartBody(t, metadataPart(t, m), part{replaywire.PartPayload, testPayload})
		if rec := h.do(t, "POST", "/v1/records", captureToken, body, headers); rec.Code != http.StatusBadRequest {
			t.Fatalf("forged inspection: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestInspectionViewsAndFiltering(t *testing.T) {
	f := store.Record{RecordMetadata: inspectionMetadata(), ExpiresAt: time.Now().Add(time.Hour)}
	f.Retryable = true // kind takes precedence even if corrupted data is presented.
	f.OccurredAt = time.Now()
	view := summaryOf(f)
	if view.Actions.Retry.Allowed || view.Actions.Resolve.Allowed || !view.Actions.Dismiss.Allowed {
		t.Fatal(view.Actions)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "occurred_at") || strings.Contains(string(data), "delivery_status") || strings.Contains(string(data), "inspected_at") {
		t.Fatal(string(data))
	}
	filter, err := parseFilter(url.Values{"kind": {"inspection"}})
	if err != nil || filter.Kind != "inspection" {
		t.Fatalf("filter: %+v %v", filter, err)
	}
}

func TestInspectionIngestRejectsPresentFailureOnlyFields(t *testing.T) {
	for _, field := range replaywire.InspectionForbiddenMetadataFields() {
		t.Run(field, func(t *testing.T) {
			fs := notFoundStore()
			fs.ingest = func(store.NewRecord) (store.IngestResult, error) {
				t.Fatal("invalid inspection reached storage")
				return 0, nil
			}
			h := newHarness(t, fs)
			var metadata map[string]any
			if err := json.Unmarshal(metadataPart(t, inspectionMetadata()).data, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata[field] = nil
			raw, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			body, headers := multipartBody(t, part{replaywire.PartMetadata, raw}, part{replaywire.PartPayload, testPayload})
			response := h.do(t, "POST", "/v1/records", captureToken, body, headers)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_metadata") {
				t.Fatalf("accepted %s: %d %s", field, response.Code, response.Body.String())
			}
		})
	}
}
