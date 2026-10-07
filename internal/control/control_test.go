package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

const testToken = "control-token-0123456789abcdefghij"

type fakeRedriver struct {
	mu      sync.Mutex
	calls   int
	ctx     context.Context
	ctxErr  error // ctx.Err() during the call; the handler cancels ctx after
	req     replaywire.DeliverRequest
	entered chan struct{} // when set, Redrive signals it on entry
	release chan struct{} // when set, Redrive waits for it
	result  Result
	err     error
}

func (f *fakeRedriver) Redrive(ctx context.Context, req replaywire.DeliverRequest, _ deliverysnapshot.Snapshot) (Result, error) {
	f.mu.Lock()
	f.calls++
	f.ctx, f.ctxErr, f.req = ctx, ctx.Err(), req
	entered, release := f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return f.result, f.err
}

func (f *fakeRedriver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeMetrics struct {
	mu       sync.Mutex
	runs     map[string]int
	inflight int64
}

func (m *fakeMetrics) ControlRun(flow, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[flow+"/"+outcome]++
}

func (m *fakeMetrics) SetControlInflight(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inflight = n
}

func (m *fakeMetrics) get(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[key]
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func testSnapshot(t *testing.T) []byte {
	t.Helper()
	dto, err := capture.SnapshotFromEnvelope(graph.Envelope{Body: map[string]any{"a": 1.0}, BodyFormat: "json", Vars: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &dto})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validRequest(snapshot []byte) replaywire.DeliverRequest {
	return replaywire.DeliverRequest{RunID: "run-1", RecordID: "fail-1", Kind: replaywire.KindFailure, ReplayMode: replaywire.ReplayResume, Flow: "orders",
		NodeID: "out", NodeType: "publish", SnapshotSHA256: digest(snapshot), SnapshotVersion: deliverysnapshot.Version,
		DeadlineMS: 5000, InboundStatus: 202, MaxRecordBytes: 1 << 20}
}

type part struct {
	name string
	data []byte
}

func multipartBody(t *testing.T, parts ...part) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		pw, err := w.CreateFormField(p.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write(p.data)
	}
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func requestJSON(t *testing.T, req replaywire.DeliverRequest) []byte {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func post(body []byte, contentType string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, replaywire.DeliverPath, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Authorization", "Bearer "+testToken)
	return r
}

func deliverRequest(t *testing.T, req replaywire.DeliverRequest, snapshot []byte) *http.Request {
	t.Helper()
	body, ct := multipartBody(t, part{replaywire.PartRequest, requestJSON(t, req)}, part{replaywire.PartSnapshot, snapshot})
	return post(body, ct)
}

func newTestServer(r Redriver, m *fakeMetrics) *Server {
	return New(Options{Token: testToken, MaxConcurrent: 2, MaxRecordBytes: 1 << 20, Flows: []string{"orders"},
		Redriver: r, Metrics: m, Logger: slog.New(slog.DiscardHandler), CoreVersion: "v1", CoreInstance: "core-0"})
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.HTTPServer().Handler.ServeHTTP(rec, r)
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for header, want := range map[string]string{
		"Content-Type": "application/problem+json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem body: %q", rec.Body.String())
	}
	if p.Code == replaywire.NotStartedCode && rec.Header().Get("Retry-After") != "1" {
		t.Errorf("not_started Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
	}
	return p.Code
}

func readResponse(t *testing.T, rec *httptest.ResponseRecorder) (replaywire.DeliverResult, map[string][]byte) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("content type %q: %v", rec.Header().Get("Content-Type"), err)
	}
	mr := multipart.NewReader(rec.Body, params["boundary"])
	parts := map[string][]byte{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(p)
		parts[p.FormName()] = data
	}
	var result replaywire.DeliverResult
	dec := json.NewDecoder(bytes.NewReader(parts[replaywire.PartResult]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		t.Fatalf("result part: %v", err)
	}
	return result, parts
}

var at = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

// childRecord is a valid downstream failure of validRequest's run.
func childRecord(i int, snapshot, payload []byte) capture.Record {
	m := replaywire.RecordMetadata{ID: fmt.Sprintf("01926d3c-aaaa-7b3a-9c1e-%012x", i), Kind: replaywire.KindFailure, ReplayMode: replaywire.ReplayResume,
		Flow: "orders", NodeID: "after", NodeType: "publish", EventID: "evt-1", ReceivedAt: at, OccurredAt: at,
		InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, Attempts: 1}
	if snapshot == nil {
		m.NotRetryableReason = replaywire.NotRetryableTooLarge
		m.PayloadUnavailableReason = replaywire.NotRetryableTooLarge
		return capture.Record{Metadata: m}
	}
	m.Retryable = true
	m.Snapshot = &replaywire.ArtifactInfo{Size: int64(len(snapshot)), SHA256: digest(snapshot), Version: 1}
	m.Payload = &replaywire.ArtifactInfo{Size: int64(len(payload)), SHA256: digest(payload)}
	return capture.Record{Metadata: m, Snapshot: snapshot, Payload: payload}
}

func deliveredResult(children ...capture.Record) Result {
	return Result{Nodes: []replaywire.NodeOutcome{{NodeID: "out", Outcome: replaywire.NodeDelivered, Attempts: 1},
		{NodeID: "after", Outcome: replaywire.NodeFailed, ErrorClass: replaywire.ErrorClass5xx, Attempts: 1}},
		Children: children, NodeConfigHash: "node-hash", DestinationHash: "dest-hash"}
}

func TestDeliverAnswersWithTheResult(t *testing.T) {
	snap := testSnapshot(t)
	full := childRecord(0, []byte("child-snapshot"), []byte("child-payload"))
	metaOnly := childRecord(1, nil, nil)
	r := &fakeRedriver{result: deliveredResult(full, metaOnly)}
	m := &fakeMetrics{runs: map[string]int{}}
	rec := serve(newTestServer(r, m), deliverRequest(t, validRequest(snap), snap))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	result, parts := readResponse(t, rec)
	if err := result.Validate("out"); err != nil {
		t.Fatalf("result invalid: %v", err)
	}
	if result.CoreVersion != "v1" || result.CoreInstance != "core-0" || result.NodeConfigHash != "node-hash" || len(result.Children) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if string(parts[replaywire.ChildSnapshotPart(0)]) != "child-snapshot" || string(parts[replaywire.ChildPayloadPart(0)]) != "child-payload" {
		t.Fatalf("child 0 parts = %v", parts)
	}
	if _, ok := parts[replaywire.ChildSnapshotPart(1)]; ok {
		t.Fatal("a metadata-only child carried parts")
	}
	if r.req.RunID != "run-1" || m.get("orders/succeeded") != 1 {
		t.Fatalf("redriver got %+v; runs %v", r.req, m.runs)
	}
}

func TestDeliverRequiresTheControlToken(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult()}
	s := newTestServer(r, &fakeMetrics{runs: map[string]int{}})
	for name, header := range map[string]string{"missing": "", "wrong": "Bearer " + strings.Repeat("x", len(testToken)),
		"basic": "Basic " + testToken, "prefix": "Bearer " + testToken[:10]} {
		req := deliverRequest(t, validRequest(snap), snap)
		req.Header.Set("Authorization", header)
		rec := serve(s, req)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), testToken) {
			t.Fatalf("%s: status %d body %q", name, rec.Code, rec.Body.String())
		}
	}
	if r.count() != 0 {
		t.Fatal("an unauthenticated request reached the redriver")
	}
}

func TestDeliverRejectsDuplicateAuthorization(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult()}
	s := newTestServer(r, &fakeMetrics{runs: map[string]int{}})
	for _, headers := range [][]string{
		{"Bearer " + testToken, "Bearer wrong"},
		{"Bearer wrong", "Bearer " + testToken},
		{"Bearer " + testToken, "Bearer " + testToken},
		{"Bearer " + testToken, ""},
		{"Bearer " + testToken + ", Bearer wrong"},
	} {
		req := deliverRequest(t, validRequest(snap), snap)
		req.Header["Authorization"] = headers
		rec := serve(s, req)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), testToken) {
			t.Fatalf("duplicate control credentials: %d %q", rec.Code, rec.Body.String())
		}
	}
	if r.count() != 0 {
		t.Fatal("duplicate Authorization reached the redriver")
	}
}

func TestDeliverRefusesBeforeStarting(t *testing.T) {
	snap := testSnapshot(t)
	good := requestJSON(t, validRequest(snap))
	mutated := func(f func(*replaywire.DeliverRequest)) []byte {
		req := validRequest(snap)
		f(&req)
		return requestJSON(t, req)
	}
	full, ct := multipartBody(t, part{replaywire.PartRequest, good}, part{replaywire.PartSnapshot, snap})
	cases := map[string]struct {
		req    *http.Request
		status int
	}{
		"not multipart":   {post([]byte(`{}`), "application/json"), 400},
		"truncated body":  {post(full[:len(full)/2], ct), 400},
		"no snapshot":     {post(multipartBody(t, part{replaywire.PartRequest, good})), 400},
		"no request":      {post(multipartBody(t, part{replaywire.PartSnapshot, snap})), 400},
		"unknown part":    {post(multipartBody(t, part{replaywire.PartRequest, good}, part{replaywire.PartSnapshot, snap}, part{"extra", nil})), 400},
		"duplicate part":  {post(multipartBody(t, part{replaywire.PartRequest, good}, part{replaywire.PartRequest, good}, part{replaywire.PartSnapshot, snap})), 400},
		"unknown field":   {post(multipartBody(t, part{replaywire.PartRequest, []byte(`{"run_id":"r","bogus":1}`)}, part{replaywire.PartSnapshot, snap})), 400},
		"trailing json":   {post(multipartBody(t, part{replaywire.PartRequest, append(append([]byte{}, good...), []byte(` {}`)...)}, part{replaywire.PartSnapshot, snap})), 400},
		"inspect kind":    {post(multipartBody(t, part{replaywire.PartRequest, mutated(func(r *replaywire.DeliverRequest) { r.Kind = replaywire.KindInspection })}, part{replaywire.PartSnapshot, snap})), 400},
		"bad kind":        {post(multipartBody(t, part{replaywire.PartRequest, mutated(func(r *replaywire.DeliverRequest) { r.Kind = "replay" })}, part{replaywire.PartSnapshot, snap})), 400},
		"no deadline":     {post(multipartBody(t, part{replaywire.PartRequest, mutated(func(r *replaywire.DeliverRequest) { r.DeadlineMS = 0 })}, part{replaywire.PartSnapshot, snap})), 400},
		"huge deadline":   {post(multipartBody(t, part{replaywire.PartRequest, mutated(func(r *replaywire.DeliverRequest) { r.DeadlineMS = 1 << 62 })}, part{replaywire.PartSnapshot, snap})), 400},
		"refused inbound": {post(multipartBody(t, part{replaywire.PartRequest, mutated(func(r *replaywire.DeliverRequest) { r.InboundStatus = 503 })}, part{replaywire.PartSnapshot, snap})), 400},
		"publish_only without target": {post(multipartBody(t, part{replaywire.PartRequest, mutated(func(r *replaywire.DeliverRequest) {
			r.ReplayMode, r.DestinationID = replaywire.ReplayPublishOnly, "notify"
		})}, part{replaywire.PartSnapshot, snap})), 400},
		"snapshot over the limit": {post(multipartBody(t, part{replaywire.PartRequest, good}, part{replaywire.PartSnapshot, bytes.Repeat([]byte("x"), 1<<20+1)})), 413},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := &fakeRedriver{result: deliveredResult()}
			rec := serve(newTestServer(r, &fakeMetrics{runs: map[string]int{}}), c.req)
			if rec.Code != c.status || r.count() != 0 {
				t.Fatalf("status %d (%s) after %d redrives; want %d and none", rec.Code, rec.Body.String(), r.count(), c.status)
			}
		})
	}
}

func TestDeliverChecksTheSnapshotBeforeRedriving(t *testing.T) {
	snap := testSnapshot(t)
	garbage := []byte("not a snapshot")
	msgSnap, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindPublishOnly, Message: &deliverysnapshot.Message{ID: "evt-1"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		req      replaywire.DeliverRequest
		snapshot []byte
		status   int
		code     string
	}{
		"newer version": {func() replaywire.DeliverRequest {
			r := validRequest(snap)
			r.SnapshotVersion = deliverysnapshot.Version + 1
			return r
		}(), snap, 422, replaywire.BlockUnsupportedSnapshotVersion},
		"digest mismatch": {func() replaywire.DeliverRequest {
			r := validRequest(snap)
			r.SnapshotSHA256 = digest([]byte("other"))
			return r
		}(), snap, 422, replaywire.BlockHashMismatch},
		"undecodable":   {validRequest(garbage), garbage, 400, "malformed_snapshot"},
		"kind mismatch": {validRequest(msgSnap), msgSnap, 400, "malformed_snapshot"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := &fakeRedriver{result: deliveredResult()}
			rec := serve(newTestServer(r, &fakeMetrics{runs: map[string]int{}}), deliverRequest(t, c.req, c.snapshot))
			if rec.Code != c.status || problemCode(t, rec) != c.code || r.count() != 0 {
				t.Fatalf("status %d code %q after %d redrives; want %d %q and none", rec.Code, problemCode(t, rec), r.count(), c.status, c.code)
			}
		})
	}
}

func TestDeliverMapsRedriverErrors(t *testing.T) {
	snap := testSnapshot(t)
	cases := map[string]struct {
		err     error
		status  int
		code    string
		outcome string
	}{
		"blocked":   {Blockf(replaywire.BlockNodeMissing, "node %q is gone", "out"), 422, replaywire.BlockNodeMissing, OutcomeBlocked},
		"malformed": {fmt.Errorf("%w: bad value", ErrMalformedSnapshot), 400, "malformed_snapshot", OutcomeRejected},
		"internal":  {errors.New("boom"), 500, "internal", OutcomeError},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := &fakeMetrics{runs: map[string]int{}}
			rec := serve(newTestServer(&fakeRedriver{err: c.err}, m), deliverRequest(t, validRequest(snap), snap))
			if rec.Code != c.status || problemCode(t, rec) != c.code || m.get("orders/"+c.outcome) != 1 {
				t.Fatalf("status %d code %q runs %v", rec.Code, problemCode(t, rec), m.runs)
			}
		})
	}
}

func TestDeliverIsBusyAtMaxConcurrent(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult(), entered: make(chan struct{}, 2), release: make(chan struct{})}
	m := &fakeMetrics{runs: map[string]int{}}
	s := newTestServer(r, m)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serve(s, deliverRequest(t, validRequest(snap), snap))
		}()
	}
	<-r.entered
	<-r.entered
	rec := serve(s, deliverRequest(t, validRequest(snap), snap))
	close(r.release)
	wg.Wait()
	if rec.Code != http.StatusServiceUnavailable || problemCode(t, rec) != replaywire.NotStartedCode || r.count() != 2 {
		t.Fatalf("third request: %d %q after %d redrives", rec.Code, rec.Body.String(), r.count())
	}
	// Refused before the body is read, so the flow is not known yet.
	if m.get("unknown/not_started") != 1 {
		t.Fatalf("runs = %v", m.runs)
	}
}

func TestDeliverWhileDrainingIsNotStarted(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult()}
	s := newTestServer(r, &fakeMetrics{runs: map[string]int{}})
	// Shutdown on the listener drains it, whether or not it ever served.
	// net/http runs shutdown hooks in goroutines, so wait for the flag.
	if err := s.HTTPServer().Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !s.draining.Load() {
		if time.Now().After(deadline) {
			t.Fatal("Shutdown never drained the listener")
		}
		time.Sleep(time.Millisecond)
	}
	for range 3 {
		rec := serve(s, deliverRequest(t, validRequest(snap), snap))
		if rec.Code != http.StatusServiceUnavailable || problemCode(t, rec) != replaywire.NotStartedCode {
			t.Fatalf("draining listener answered %d %q", rec.Code, rec.Body.String())
		}
	}
	if r.count() != 0 {
		t.Fatal("a draining listener started a redrive")
	}
}

func TestDeliverBoundsTheResponse(t *testing.T) {
	snap := testSnapshot(t)
	var many []capture.Record
	for i := range replaywire.MaxChildren + 6 {
		many = append(many, childRecord(i, []byte("s"), []byte("p")))
	}
	t.Run("too many children cannot falsely resolve the parent", func(t *testing.T) {
		m := &fakeMetrics{runs: map[string]int{}}
		rec := serve(newTestServer(&fakeRedriver{result: deliveredResult(many...)}, m),
			deliverRequest(t, validRequest(snap), snap))
		if rec.Code != http.StatusInternalServerError || problemCode(t, rec) != "internal" || m.get("orders/error") != 1 {
			t.Fatalf("status %d, body %q, runs %v; an incomplete result must not resolve the parent", rec.Code, rec.Body.String(), m.runs)
		}
	})
	t.Run("an artifact over replay's limit is metadata-only", func(t *testing.T) {
		big := childRecord(0, bytes.Repeat([]byte("s"), 2048), []byte("p"))
		req := validRequest(snap)
		req.MaxRecordBytes = 1024
		rec := serve(newTestServer(&fakeRedriver{result: deliveredResult(big)}, &fakeMetrics{runs: map[string]int{}}), deliverRequest(t, req, snap))
		result, parts := readResponse(t, rec)
		if c := result.Children[0]; c.Retryable || c.NotRetryableReason != replaywire.NotRetryableTooLarge || len(parts) != 1 {
			t.Fatalf("child = %+v, parts %d", c, len(parts))
		}
	})
	t.Run("the response budget makes later children metadata-only", func(t *testing.T) {
		s := newTestServer(&fakeRedriver{result: deliveredResult(childRecord(0, []byte("0123456789"), []byte("p")),
			childRecord(1, []byte("0123456789"), []byte("p")))}, &fakeMetrics{runs: map[string]int{}})
		s.budget = 15
		result, parts := readResponse(t, serve(s, deliverRequest(t, validRequest(snap), snap)))
		if !result.Children[0].Retryable || result.Children[1].Retryable || len(parts) != 3 {
			t.Fatalf("children = %+v, parts %d", result.Children, len(parts))
		}
	})
	t.Run("an invalid result is an error, not a 200", func(t *testing.T) {
		bad := deliveredResult()
		bad.Nodes[0].ErrorMessage = "nul\x00"
		m := &fakeMetrics{runs: map[string]int{}}
		rec := serve(newTestServer(&fakeRedriver{result: bad}, m), deliverRequest(t, validRequest(snap), snap))
		if rec.Code != http.StatusInternalServerError || m.get("orders/error") != 1 {
			t.Fatalf("status %d runs %v", rec.Code, m.runs)
		}
	})
}

func TestDeliverRunsDetachedWithTheRequestedDeadline(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult()}
	req := deliverRequest(t, validRequest(snap), snap)
	ctx, cancel := context.WithCancel(req.Context())
	cancel() // the caller went away: work already accepted still runs to its deadline
	start := time.Now()
	serve(newTestServer(r, &fakeMetrics{runs: map[string]int{}}), req.WithContext(ctx))
	deadline, ok := r.ctx.Deadline()
	if r.ctxErr != nil || !ok || deadline.Sub(start) > 5*time.Second+time.Second || deadline.Sub(start) < 4*time.Second {
		t.Fatalf("redrive ctx err %v deadline %v (in %v)", r.ctx.Err(), deadline, deadline.Sub(start))
	}
}

func TestDeliverCountsRunsByConfiguredFlow(t *testing.T) {
	snap := testSnapshot(t)
	m := &fakeMetrics{runs: map[string]int{}}
	failed := deliveredResult()
	failed.Nodes = failed.Nodes[:1]
	failed.Nodes[0].Outcome = replaywire.NodeFailed
	s := newTestServer(&fakeRedriver{result: failed}, m)
	serve(s, deliverRequest(t, validRequest(snap), snap))
	other := validRequest(snap)
	other.Flow = "not-configured"
	serve(s, deliverRequest(t, other, snap))
	if m.get("orders/failed") != 1 || m.get("unknown/failed") != 1 {
		t.Fatalf("runs = %v", m.runs)
	}
}

func TestDeliverServesOnlyTheDeliverRoute(t *testing.T) {
	s := newTestServer(&fakeRedriver{}, &fakeMetrics{runs: map[string]int{}})
	get := httptest.NewRequestWithContext(context.Background(), http.MethodGet, replaywire.DeliverPath, nil)
	get.Header.Set("Authorization", "Bearer "+testToken)
	if rec := serve(s, get); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rec.Code)
	}
	other := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/records", nil)
	if rec := serve(s, other); rec.Code != http.StatusNotFound {
		t.Fatalf("other path = %d", rec.Code)
	}
}

// Shutdown drains, then waits for in-flight redrives even past its context:
// they run on detached contexts bounded by their own deadlines, and the
// publishers they deliver through close only after Shutdown returns.
func TestShutdownWaitsForInFlightRedrivesPastItsContext(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	s := newTestServer(r, &fakeMetrics{runs: map[string]int{}})
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.HTTPServer().Serve(l) }()
	body, ct := multipartBody(t, part{replaywire.PartRequest, requestJSON(t, validRequest(snap))}, part{replaywire.PartSnapshot, snap})
	status := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+l.Addr().String()+replaywire.DeliverPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-r.entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned %v while a redrive was still running", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(r.release)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown = %v after the redrive finished", err)
	}
	if code := <-status; code != http.StatusOK {
		t.Fatalf("the in-flight redrive answered %d", code)
	}
}

// A request whose body finishes arriving after draining began still has not
// started: it answers not_started.
func TestDeliverRechecksDrainingAfterReadingTheBody(t *testing.T) {
	snap := testSnapshot(t)
	r := &fakeRedriver{result: deliveredResult()}
	s := newTestServer(r, &fakeMetrics{runs: map[string]int{}})
	body, ct := multipartBody(t, part{replaywire.PartRequest, requestJSON(t, validRequest(snap))}, part{replaywire.PartSnapshot, snap})
	pr, pw := io.Pipe()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, replaywire.DeliverPath, pr)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.HTTPServer().Handler.ServeHTTP(rec, req)
		close(done)
	}()
	_, _ = pw.Write(body[:len(body)/2]) // returns once the handler is reading the body
	s.Drain()
	_, _ = pw.Write(body[len(body)/2:])
	_ = pw.Close()
	<-done
	if rec.Code != http.StatusServiceUnavailable || problemCode(t, rec) != replaywire.NotStartedCode || r.count() != 0 {
		t.Fatalf("status %d %q after %d redrives; want 503 not_started and none", rec.Code, rec.Body.String(), r.count())
	}
}
