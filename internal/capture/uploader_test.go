package capture

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
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
	meta  replaywire.RecordMetadata
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

func TestUploaderRefusesRedirectsWithoutForwardingCredentialsOrArtifacts(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, sameOrigin := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/same-origin=%t", status, sameOrigin), func(t *testing.T) {
				var leaked, calls atomic.Int32
				other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					leaked.Add(1)
					w.WriteHeader(http.StatusCreated)
				}))
				defer other.Close()
				rec := testRecord(t)
				if len(rec.Snapshot) == 0 || len(rec.Payload) == 0 {
					t.Fatal("redirect regression requires both snapshot and payload artifacts")
				}
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/redirected" {
						leaked.Add(1)
						w.WriteHeader(http.StatusCreated)
						return
					}
					calls.Add(1)
					body, _ := io.ReadAll(r.Body)
					if r.Header.Get("Authorization") != "Bearer "+testToken || !bytes.Contains(body, rec.Snapshot) || !bytes.Contains(body, rec.Payload) {
						t.Error("configured replay receiver did not receive credentials/artifacts")
					}
					target := other.URL
					if sameOrigin {
						target = "/redirected"
					}
					w.Header().Set("Location", target)
					w.WriteHeader(status)
				}))
				defer source.Close()
				m := newFakeMetrics()
				u := newTestUploader(t, source.URL, m)
				u.submit(pendingFor(rec))
				waitUntil(t, "redirect rejection", func() bool { return m.count("dropped", DropRejected) == 1 })
				if calls.Load() != 1 || leaked.Load() != 0 || m.count("uploaded", "orders") != 0 {
					t.Fatal("redirect forwarded or falsely accepted the captured record")
				}
			})
		}
	}
}

func TestUploaderTransportFailureLogsSafeDiagnostic(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u := newTestUploader(t, source.URL+`/PATHSECRET?api_key=quoted"QUERYSECRET`, newFakeMetrics())
	source.Close()
	var logs bytes.Buffer
	u.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	u.submit(pendingFor(testRecord(t)))
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"PATHSECRET", "QUERYSECRET", testToken} {
		if strings.Contains(logs.String(), marker) {
			t.Fatalf("capture failure log retained %s", marker)
		}
	}
	if !strings.Contains(logs.String(), source.URL) || !strings.Contains(logs.String(), "connection refused") {
		t.Fatal("capture diagnostic lost its destination or transport failure class")
	}
}

func TestUploaderLocalTLSProxyVerifiesCAAndPreservesArtifacts(t *testing.T) {
	backend, received := fakeReplay(t, http.StatusCreated)
	upstream, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(upstream))
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "proxy-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		tls     tlsfiles.Files
		trusted bool
	}{
		{"configured CA", tlsfiles.Files{CACert: ca}, true},
		{"untrusted certificate", tlsfiles.Files{}, false},
		{"wrong server name", tlsfiles.Files{CACert: ca, ServerName: "wrong.local.test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(received())
			m := newFakeMetrics()
			u, err := NewUploader(UploaderOptions{URL: proxy.URL, Token: testToken, TLS: tc.tls, Metrics: m, Logger: slog.New(slog.DiscardHandler)})
			if err != nil {
				t.Fatal(err)
			}
			u.backoff = time.Millisecond
			t.Cleanup(func() { _ = u.Close() })
			rec := testRecord(t)
			u.submit(pendingFor(rec))
			if tc.trusted {
				waitUntil(t, "verified TLS upload", func() bool { return m.count("uploaded", "orders") == 1 })
				got := received()
				if len(got) != before+1 || got[before].auth != "Bearer "+testToken || got[before].meta.ID != rec.Metadata.ID ||
					!bytes.Equal(got[before].parts[replaywire.PartSnapshot], rec.Snapshot) || !bytes.Equal(got[before].parts[replaywire.PartPayload], rec.Payload) {
					t.Fatal("TLS termination changed capture authentication or artifacts")
				}
			} else {
				waitUntil(t, "certificate failure", func() bool { return m.count("dropped", DropUploadFailed) == 1 })
				if len(received()) != before || m.count("uploaded", "orders") != 0 {
					t.Fatal("untrusted/mismatched TLS certificate reached HTTP replay backend")
				}
			}
		})
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
