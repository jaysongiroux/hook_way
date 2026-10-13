package control

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
)

func TestDeliverReadsLegacySnapshotWithoutFlags(t *testing.T) {
	snapshot, err := deliverysnapshot.Decode(testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Version = 1
	snapshot.Envelope.Flags = nil
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := validRequest(compressed.Bytes())
	request.SnapshotVersion = 1
	redriver := &fakeRedriver{result: deliveredResult()}
	response := serve(newTestServer(redriver, &fakeMetrics{runs: map[string]int{}}), deliverRequest(t, request, compressed.Bytes()))
	if response.Code != http.StatusOK || redriver.count() != 1 {
		t.Fatalf("legacy replay: %d %s, calls %d", response.Code, response.Body.String(), redriver.count())
	}
	request.SnapshotVersion = 2
	response = serve(newTestServer(redriver, &fakeMetrics{runs: map[string]int{}}), deliverRequest(t, request, compressed.Bytes()))
	if response.Code != http.StatusBadRequest || redriver.count() != 1 {
		t.Fatal("snapshot metadata version mismatch was accepted")
	}
}
