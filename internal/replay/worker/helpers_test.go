package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

const (
	testRecordID = "01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8e"
	testRunID    = "01926d3c-9999-7b3a-9c1e-2f4a5b6c7d8e"
)

var testSnapshot = []byte("gzip-snapshot-bytes")

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func artifact(b []byte, version int) *replaywire.ArtifactInfo {
	return &replaywire.ArtifactInfo{Size: int64(len(b)), SHA256: digest(b), Version: version}
}

func testRun() store.ClaimedRun {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	return store.ClaimedRun{
		Run: store.Run{RunID: testRunID, RecordID: testRecordID, Status: store.RunRunning},
		Record: store.Record{
			RecordMetadata: replaywire.RecordMetadata{
				ID: testRecordID, Kind: replaywire.KindFailure, ReplayMode: replaywire.ReplayResume, Flow: "orders", NodeID: "out_s3", NodeType: "publish",
				DestinationID: "archive", DestinationType: "s3", Target: "uploads", RenderedTarget: "uploads",
				EventID: "evt-1", ReceivedAt: at, OccurredAt: at, InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx,
				Retryable: true, NodeConfigHash: "node-hash", DestinationHash: "dest-hash",
				Snapshot: artifact(testSnapshot, 1), Payload: artifact([]byte("{}"), 0),
			},
			DeliveryStatus: store.StatusRetrying, RootID: testRecordID,
		},
	}
}

// childMetadata is a valid downstream failure of testRun's event.
func childMetadata(id string, snapshot, payload []byte) replaywire.RecordMetadata {
	m := testRun().Record.RecordMetadata
	m.ID, m.NodeID, m.Snapshot, m.Payload = id, "out_lambda", nil, nil
	if snapshot == nil {
		m.Retryable, m.NotRetryableReason = false, replaywire.NotRetryableTooLarge
		m.PayloadUnavailableReason = replaywire.NotRetryableTooLarge
		return m
	}
	m.Snapshot, m.Payload = artifact(snapshot, 1), artifact(payload, 0)
	return m
}

// metricValue reads one series from the registry, or 0 when it is absent.
func metricValue(t *testing.T, m *metrics.Metrics, name string, labels prometheus.Labels) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	series:
		for _, s := range f.GetMetric() {
			for _, l := range s.GetLabel() {
				if want, ok := labels[l.GetName()]; ok && want != l.GetValue() {
					continue series
				}
			}
			if s.GetCounter() != nil {
				return s.GetCounter().GetValue()
			}
			return s.GetGauge().GetValue()
		}
	}
	return 0
}
