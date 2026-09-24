package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestMain(m *testing.M) { os.Exit(replaytest.Main(m)) }

// syncBuffer is a bytes.Buffer safe for the logger and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// call sends one request and returns its status and body; the body is read
// and closed here.
func call(t *testing.T, method, url, token string, body io.Reader, header http.Header) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func TestServeRefusesAnUnmigratedDatabase(t *testing.T) {
	path := writeConfig(t, testConfig(replaytest.Database(t), freeAddr(t), ""))
	if code, stdout, _ := runCmd("serve", "--config", path); code != 1 || !strings.Contains(stdout, "run hookway-replay migrate") {
		t.Fatalf("serve = %d %q", code, stdout)
	}
}

func TestServeEndToEnd(t *testing.T) {
	addr := freeAddr(t)
	path := writeConfig(t, testConfig(replaytest.Database(t), addr, "metrics: {enabled: true, path: /metrics}\n"))
	if code, stdout, stderr := runCmd("migrate", "--config", path); code != 0 || !strings.Contains(stdout, "schema is at version 1") {
		t.Fatalf("migrate = %d %q %q", code, stdout, stderr)
	}
	if code, stdout, _ := runCmd("migrate", "--config", path); code != 0 || !strings.Contains(stdout, "schema is current at version 1") {
		t.Fatalf("second migrate = %d %q", code, stdout)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var logs syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve", "--config", path}, &logs, &logs) }()
	base := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("service never became ready; logs:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	id := "01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8e"
	snapshot, payload := []byte("snapshot-bytes"), []byte(`{"event":"created"}`)
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	meta, _ := json.Marshal(replaywire.FailureMetadata{
		ID: id, Kind: replaywire.KindResume, Flow: "orders", NodeID: "out", NodeType: "publish", EventID: "evt-1",
		ReceivedAt: at, FailedAt: at, InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, Retryable: true,
		Snapshot: &replaywire.ArtifactInfo{Size: int64(len(snapshot)), SHA256: sum(snapshot), Version: 1},
		Payload:  &replaywire.ArtifactInfo{Size: int64(len(payload)), SHA256: sum(payload), ContentType: "application/json"},
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range []struct {
		name string
		data []byte
	}{{replaywire.PartMetadata, meta}, {replaywire.PartSnapshot, snapshot}, {replaywire.PartPayload, payload}} {
		fw, _ := mw.CreateFormField(p.name)
		_, _ = fw.Write(p.data)
	}
	_ = mw.Close()
	if status, data := call(t, "POST", base+"/v1/failures", captureToken, &body, http.Header{"Content-Type": {mw.FormDataContentType()}}); status != 201 {
		t.Fatalf("ingest = %d %s", status, data)
	}
	if status, data := call(t, "GET", base+"/v1/failures?flow=orders", opsToken, nil, nil); status != 200 || !strings.Contains(string(data), id) {
		t.Fatalf("list = %d %s", status, data)
	}
	if status, data := call(t, "GET", base+"/v1/failures/"+id+"/payload", opsToken, nil, nil); status != 200 || !bytes.Equal(data, payload) {
		t.Fatalf("payload = %d %q", status, data)
	}
	status, data := call(t, "POST", base+"/v1/failures/"+id+"/retries", opsToken, nil, http.Header{"Idempotency-Key": {"e2e-1"}})
	var queued struct {
		RunID string `json:"run_id"`
	}
	if status != 202 || json.Unmarshal(data, &queued) != nil || queued.RunID == "" {
		t.Fatalf("retry = %d %s", status, data)
	}
	if status, data := call(t, "GET", base+"/v1/retries/"+queued.RunID, opsToken, nil, nil); status != 200 || !strings.Contains(string(data), `"status":"queued"`) {
		t.Fatalf("run = %d %s", status, data)
	}
	if status, _ := call(t, "GET", base+"/metrics", "", nil, nil); status != 401 {
		t.Fatalf("metrics without a token = %d, want 401 (a key has the metrics scope)", status)
	}
	if status, data := call(t, "GET", base+"/metrics", opsToken, nil, nil); status != 200 ||
		!strings.Contains(string(data), `hookway_replay_captures_total{outcome="created"} 1`) ||
		!strings.Contains(string(data), `hookway_replay_db_pool_max_connections`) {
		t.Fatalf("metrics = %d\n%s", status, data)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit = %d; logs:\n%s", code, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
	if strings.Contains(logs.String(), "snapshot-bytes") {
		t.Fatal("logs contain artifact bytes")
	}
}

// postFailure ingests a retryable record for event evt-1 of flow orders.
func postFailure(t *testing.T, base, id string) {
	t.Helper()
	snapshot, payload := []byte("snapshot-"+id), []byte(`{"event":"created"}`)
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	meta, _ := json.Marshal(replaywire.FailureMetadata{
		ID: id, Kind: replaywire.KindResume, Flow: "orders", NodeID: "out_s3", NodeType: "publish", EventID: "evt-1",
		ReceivedAt: at, FailedAt: at, InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, Retryable: true,
		Snapshot: &replaywire.ArtifactInfo{Size: int64(len(snapshot)), SHA256: sum(snapshot), Version: 1},
		Payload:  &replaywire.ArtifactInfo{Size: int64(len(payload)), SHA256: sum(payload), ContentType: "application/json"},
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range []struct {
		name string
		data []byte
	}{{replaywire.PartMetadata, meta}, {replaywire.PartSnapshot, snapshot}, {replaywire.PartPayload, payload}} {
		fw, _ := mw.CreateFormField(p.name)
		_, _ = fw.Write(p.data)
	}
	_ = mw.Close()
	if status, data := call(t, "POST", base+"/v1/failures", captureToken, &body, http.Header{"Content-Type": {mw.FormDataContentType()}}); status != 201 {
		t.Fatalf("ingest %s = %d %s", id, status, data)
	}
}

// fakeCoreControl answers deliver calls: blocked for blockedID, and for any
// other record a delivered resume node with one downstream child failure.
func fakeCoreControl(t *testing.T, blockedID, childID string) string {
	t.Helper()
	childSnap, childPay := []byte("child-snapshot"), []byte(`{"child":true}`)
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlToken || r.URL.Path != "/internal/replay/v1/deliver" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req replaywire.DeliverRequest
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			if p.FormName() == replaywire.PartRequest {
				_ = json.Unmarshal(data, &req)
			}
		}
		if req.FailureID == blockedID {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"flow_missing"}`))
			return
		}
		at := time.Date(2026, 9, 23, 12, 0, 5, 0, time.UTC)
		result := replaywire.DeliverResult{
			Nodes: []replaywire.NodeOutcome{{NodeID: req.NodeID, Outcome: replaywire.NodeDelivered, Attempts: 1},
				{NodeID: "out_lambda", Outcome: replaywire.NodeFailed, ErrorClass: replaywire.ErrorClass5xx}},
			Children: []replaywire.FailureMetadata{{
				ID: childID, Kind: replaywire.KindResume, Flow: req.Flow, NodeID: "out_lambda", NodeType: "publish", EventID: "evt-1",
				ReceivedAt: at, FailedAt: at, InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, Retryable: true,
				Snapshot: &replaywire.ArtifactInfo{Size: int64(len(childSnap)), SHA256: sum(childSnap), Version: 1},
				Payload:  &replaywire.ArtifactInfo{Size: int64(len(childPay)), SHA256: sum(childPay), ContentType: "application/json"},
			}},
			CoreVersion: "test", CoreInstance: "core-0",
		}
		raw, _ := json.Marshal(result)
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, p := range []struct {
			name string
			data []byte
		}{{replaywire.PartResult, raw}, {replaywire.ChildSnapshotPart(0), childSnap}, {replaywire.ChildPayloadPart(0), childPay}} {
			fw, _ := mw.CreateFormField(p.name)
			_, _ = fw.Write(p.data)
		}
		_ = mw.Close()
		w.Header().Set("Content-Type", mw.FormDataContentType())
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// tryGet is call without failing the test, for polling a server that may
// not be listening yet.
func tryGet(url, token string) (int, []byte) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return 0, nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func waitForJSON(t *testing.T, url, token, want string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, data := tryGet(url, token)
		if status == 200 && strings.Contains(string(data), want) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s never contained %s; last %d %s", url, want, status, data)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestServeRedrivesThroughCore(t *testing.T) {
	const okID, blockedID, childID = "01926d3c-0001-7b3a-9c1e-2f4a5b6c7d8e", "01926d3c-0002-7b3a-9c1e-2f4a5b6c7d8e", "01926d3c-0003-7b3a-9c1e-2f4a5b6c7d8e"
	addr := freeAddr(t)
	core := fakeCoreControl(t, blockedID, childID)
	cfg := strings.Replace(testConfig(replaytest.Database(t), addr, "metrics: {enabled: true, path: /metrics}\n"+
		"workers: {concurrency: 2, execution_timeout: 10s, lease: 30s}\n"),
		`control_url: "http://core-control:9092"`, `control_url: "`+core+`"`, 1)
	path := writeConfig(t, cfg)
	if code, _, stderr := runCmd("migrate", "--config", path); code != 0 {
		t.Fatalf("migrate = %d %q", code, stderr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var logs syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve", "--config", path}, &logs, &logs) }()
	base := "http://" + addr
	waitForJSON(t, base+"/readyz", "", "ready")

	postFailure(t, base, okID)
	postFailure(t, base, blockedID)
	for _, id := range []string{okID, blockedID} {
		if status, data := call(t, "POST", base+"/v1/failures/"+id+"/retries", opsToken, nil, http.Header{"Idempotency-Key": {"k-" + id}}); status != 202 {
			t.Fatalf("retry %s = %d %s", id, status, data)
		}
	}
	waitForJSON(t, base+"/v1/failures/"+okID, opsToken, `"status":"resolved"`)
	detail := waitForJSON(t, base+"/v1/failures/"+okID, opsToken, childID)
	if !strings.Contains(detail, `"status":"succeeded"`) {
		t.Fatalf("resolved record's run did not succeed: %s", detail)
	}
	child := waitForJSON(t, base+"/v1/failures/"+childID, opsToken, `"parent_id":"`+okID+`"`)
	if !strings.Contains(child, `"status":"open"`) || !strings.Contains(child, `"root_id":"`+okID+`"`) {
		t.Fatalf("child = %s", child)
	}
	blocked := waitForJSON(t, base+"/v1/failures/"+blockedID, opsToken, `"status":"blocked"`)
	if !strings.Contains(blocked, `"status":"open"`) || !strings.Contains(blocked, "flow_missing") {
		t.Fatalf("blocked record = %s", blocked)
	}
	metricsBody := waitForJSON(t, base+"/metrics", opsToken, `hookway_replay_children_created_total 1`)
	for _, want := range []string{`hookway_replay_runs_total{status="succeeded"} 1`, `hookway_replay_runs_total{status="blocked"} 1`,
		`hookway_replay_executor_calls_total{outcome="ok"} 1`} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("metrics lack %s", want)
		}
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exit = %d; logs:\n%s", code, logs.String())
	}
}

func TestGCCommandDeletesExpiredRecords(t *testing.T) {
	ctx := context.Background()
	db := replaytest.Database(t)
	path := writeConfig(t, testConfig(db, freeAddr(t), "retention: {ttl: 1s}\n"))
	if code, _, stderr := runCmd("migrate", "--config", path); code != 0 {
		t.Fatalf("migrate = %d %q", code, stderr)
	}
	st, err := store.Open(ctx, store.Options{URL: db, Limits: store.Limits{TTL: time.Second, MaxRecords: 10, MaxBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	if _, err := st.Ingest(ctx, store.NewFailure{Metadata: replaywire.FailureMetadata{
		ID: "01926d3c-0004-7b3a-9c1e-2f4a5b6c7d8e", Kind: replaywire.KindResume, Flow: "orders", NodeID: "out", NodeType: "publish",
		EventID: "evt-1", ReceivedAt: at, FailedAt: at, InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx,
		NotRetryableReason: replaywire.NotRetryableTooLarge,
	}, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if code, stdout, stderr := runCmd("gc", "--config", path); code != 0 || !strings.Contains(stdout, "gc deleted 1 records") {
		t.Fatalf("gc = %d %q %q", code, stdout, stderr)
	}
	if code, stdout, _ := runCmd("gc", "--config", path); code != 0 || !strings.Contains(stdout, "gc deleted 0 records") {
		t.Fatalf("second gc = %d %q", code, stdout)
	}
}
