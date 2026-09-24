package capture

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

type fakeMetrics struct {
	mu       sync.Mutex
	uploaded map[string]int
	dropped  map[string]int
	buffer   int64
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{uploaded: map[string]int{}, dropped: map[string]int{}}
}
func (m *fakeMetrics) CaptureUploaded(flow string) { m.mu.Lock(); m.uploaded[flow]++; m.mu.Unlock() }
func (m *fakeMetrics) CaptureDropped(_, reason string) {
	m.mu.Lock()
	m.dropped[reason]++
	m.mu.Unlock()
}
func (m *fakeMetrics) SetCaptureBufferBytes(n int64) { m.mu.Lock(); m.buffer = n; m.mu.Unlock() }
func (m *fakeMetrics) count(kind, key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if kind == "uploaded" {
		return m.uploaded[key]
	}
	return m.dropped[key]
}

type upload struct {
	auth  string
	meta  replaywire.FailureMetadata
	parts map[string][]byte
}

// fakeReplay answers each upload with the next status (the last repeats).
func fakeReplay(t *testing.T, statuses ...int) (*httptest.Server, func() []upload) {
	t.Helper()
	var mu sync.Mutex
	var got []upload
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := upload{auth: r.Header.Get("Authorization"), parts: map[string][]byte{}}
		mr, err := r.MultipartReader()
		if err == nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				data, _ := io.ReadAll(p)
				u.parts[p.FormName()] = data
			}
		}
		_ = json.Unmarshal(u.parts[replaywire.PartMetadata], &u.meta)
		mu.Lock()
		got = append(got, u)
		mu.Unlock()
		i := int(n.Add(1)) - 1
		w.WriteHeader(statuses[min(i, len(statuses)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv, func() []upload {
		mu.Lock()
		defer mu.Unlock()
		return append([]upload(nil), got...)
	}
}

const testToken = "capture-token-0123456789abcdefghijkl"

func newTestUploader(t *testing.T, url string, m *fakeMetrics) *Uploader {
	t.Helper()
	u, err := NewUploader(UploaderOptions{URL: url, Token: testToken, Metrics: m, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	u.backoff = time.Millisecond
	t.Cleanup(func() { _ = u.Close() })
	return u
}

func testRecord(t *testing.T) Record {
	t.Helper()
	rec, ok := testBuilder(t, 1<<20).FromFailure("orders", testHead, 202,
		graphEvent())
	if !ok {
		t.Fatal("fixture record not built")
	}
	return rec
}

func pendingFor(rec Record) pending {
	return pending{flow: "orders", nodeID: rec.Metadata.NodeID, eventID: rec.Metadata.EventID, estimate: 1024,
		build: func() (Record, bool) { return rec, true }}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUploaderSendsTheRecord(t *testing.T) {
	srv, got := fakeReplay(t, http.StatusCreated)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL+"/", m)
	rec := testRecord(t)
	u.submit(pendingFor(rec))
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	up := got()[0]
	if up.auth != "Bearer "+testToken || up.meta.ID != rec.Metadata.ID ||
		string(up.parts[replaywire.PartSnapshot]) != string(rec.Snapshot) || string(up.parts[replaywire.PartPayload]) != string(rec.Payload) {
		t.Fatalf("upload = %+v", up)
	}
}

func TestUploaderSendsMetadataOnlyRecordsWithoutArtifacts(t *testing.T) {
	srv, got := fakeReplay(t, http.StatusCreated)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL, m)
	rec, _ := testBuilder(t, 8).FromFailure("orders", testHead, 202, graphEvent())
	u.submit(pendingFor(rec))
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	if parts := got()[0].parts; len(parts) != 1 || parts[replaywire.PartMetadata] == nil {
		t.Fatalf("parts = %v, want only metadata", parts)
	}
}

func TestUploaderRetriesWithTheSameID(t *testing.T) {
	srv, got := fakeReplay(t, 503, 502, http.StatusOK)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL, m)
	u.submit(pendingFor(testRecord(t)))
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	ups := got()
	if len(ups) != 3 || ups[0].meta.ID != ups[2].meta.ID {
		t.Fatalf("%d attempts, ids %v", len(ups), ups)
	}
}

func TestUploaderGivesUpAfterThreeAttempts(t *testing.T) {
	srv, got := fakeReplay(t, 500)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL, m)
	u.submit(pendingFor(testRecord(t)))
	waitUntil(t, "a drop", func() bool { return m.count("dropped", DropUploadFailed) == 1 })
	if len(got()) != 3 {
		t.Fatalf("%d attempts, want 3", len(got()))
	}
}

func TestUploaderDoesNotRetryDefiniteRefusals(t *testing.T) {
	for status, reason := range map[int]string{507: DropCapacity, 400: DropRejected, 401: DropRejected, 409: DropRejected, 413: DropRejected} {
		srv, got := fakeReplay(t, status)
		m := newFakeMetrics()
		u := newTestUploader(t, srv.URL, m)
		u.submit(pendingFor(testRecord(t)))
		waitUntil(t, "a drop", func() bool { return m.count("dropped", reason) == 1 })
		if len(got()) != 1 {
			t.Fatalf("%d: %d attempts, want 1", status, len(got()))
		}
	}
}

func TestUploaderDropsWhenTheBufferIsFull(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(201)
	}))
	defer srv.Close()
	defer close(release)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL, m)
	rec := testRecord(t)
	huge := pendingFor(rec) // over the byte budget on its own, with the buffer empty
	huge.estimate = maxPendingBytes + 1
	u.submit(huge)
	if got := m.count("dropped", DropBufferFull); got != 1 {
		t.Fatalf("an over-budget record was accepted (drops %d)", got)
	}
	for range maxPendingRecords + 5 {
		u.submit(pendingFor(rec))
	}
	if got := m.count("dropped", DropBufferFull); got != 6 {
		t.Fatalf("buffer_full drops = %d, want 6 (1 over budget, 5 over the record limit)", got)
	}
}

func TestUploaderCloseIsBounded(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	m := newFakeMetrics()
	u, err := NewUploader(UploaderOptions{URL: srv.URL, Token: testToken, Metrics: m, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	u.drainWait = 100 * time.Millisecond
	rec := testRecord(t)
	for range 10 {
		u.submit(pendingFor(rec))
	}
	start := time.Now()
	_ = u.Close()
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("Close took %v", took)
	}
	if m.count("dropped", DropShutdown) != 10 {
		t.Fatalf("shutdown drops = %d, want 10", m.count("dropped", DropShutdown))
	}
	u.submit(pendingFor(rec))
	if m.count("dropped", DropShutdown) != 11 {
		t.Fatal("a submit after Close was not dropped as shutdown")
	}
}

func TestUploaderRejectsInvalidMetadataWithoutSending(t *testing.T) {
	srv, got := fakeReplay(t, 201)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL, m)
	rec := testRecord(t)
	rec.Metadata.InboundStatus = 503 // never valid: replay would refuse it
	u.submit(pendingFor(rec))
	waitUntil(t, "a drop", func() bool { return m.count("dropped", DropRejected) == 1 })
	if len(got()) != 0 {
		t.Fatal("an invalid record was sent")
	}
}

// Records are built on uploader workers, off the request path; a builder
// panic must cost that one record, never the process (I15).
func TestUploaderSurvivesABuildPanic(t *testing.T) {
	srv, _ := fakeReplay(t, http.StatusCreated)
	m := newFakeMetrics()
	u := newTestUploader(t, srv.URL, m)
	for range uploadWorkers + 1 {
		u.submit(pending{flow: "orders", nodeID: "out", eventID: "evt-1", estimate: 1024,
			build: func() (Record, bool) { panic("builder bug") }})
	}
	waitUntil(t, "the panics to be dropped", func() bool { return m.count("dropped", DropRejected) == uploadWorkers+1 })
	u.submit(pendingFor(testRecord(t)))
	waitUntil(t, "an upload after the panics", func() bool { return m.count("uploaded", "orders") == 1 })
}

// An upload holds its record and the encoded multipart copy of it.
func TestInFlightUploadsAreChargedForTheirEncodedBody(t *testing.T) {
	m := newFakeMetrics()
	u := newTestUploader(t, hangingReplay(t), m)
	u.drainWait = 50 * time.Millisecond
	rec := testRecord(t)
	u.submit(pendingFor(rec))
	waitUntil(t, "the in-flight upload to be charged twice its record", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.buffer >= 2*rec.size()
	})
}

// internal/metrics pre-creates one series per drop reason; the two lists
// must stay the same.
func TestMetricsKnowsEveryDropReason(t *testing.T) {
	got := slices.Clone(metrics.CaptureDropReasons)
	want := []string{DropBufferFull, DropUploadFailed, DropCapacity, DropRejected, DropShutdown}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("metrics.CaptureDropReasons = %v, want %v", got, want)
	}
}
