package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

const controlToken = "control-token-0123456789abcdefghij"

type coreRequest struct {
	req      replaywire.DeliverRequest
	snapshot []byte
	auth     string
	path     string
}

// fakeCore parses each deliver request and hands it to respond.
func fakeCore(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, got coreRequest)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		got := coreRequest{auth: r.Header.Get("Authorization"), path: r.URL.Path}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("request is not multipart: %v", err)
			return
		}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("read part: %v", err)
				return
			}
			data, _ := io.ReadAll(p)
			switch p.FormName() {
			case replaywire.PartRequest:
				if err := json.Unmarshal(data, &got.req); err != nil {
					t.Errorf("request part: %v", err)
				}
			case replaywire.PartSnapshot:
				got.snapshot = data
			default:
				t.Errorf("unexpected request part %q", p.FormName())
			}
		}
		respond(w, r, got)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

type respPart struct {
	name string
	data []byte
}

func writeResult(t *testing.T, w http.ResponseWriter, result any, parts ...respPart) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	writeParts(t, w, append([]respPart{{replaywire.PartResult, raw}}, parts...)...)
}

func writeParts(t *testing.T, w http.ResponseWriter, parts ...respPart) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		pw, err := mw.CreateFormField(p.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write(p.data)
	}
	_ = mw.Close()
	w.Header().Set("Content-Type", mw.FormDataContentType())
	_, _ = w.Write(buf.Bytes())
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"about:blank","status":%d,"code":%q}`, status, code)
}

func delivered(nodeOutcome string) replaywire.DeliverResult {
	return replaywire.DeliverResult{Nodes: []replaywire.NodeOutcome{{NodeID: "out_s3", Outcome: nodeOutcome, Attempts: 1,
		ErrorMessage: map[bool]string{true: "s3: 503", false: ""}[nodeOutcome == replaywire.NodeFailed]}},
		CoreVersion: "v1", CoreInstance: "core-0"}
}

func newTestExecutor(t *testing.T, url string, m *metrics.Metrics) *Executor {
	t.Helper()
	e, err := NewExecutor(ExecutorOptions{ControlURL: url, Token: controlToken, ExecutionTimeout: 30 * time.Second, MaxRecordBytes: 1 << 20, Metrics: m})
	if err != nil {
		t.Fatal(err)
	}
	e.backoff = time.Millisecond
	return e
}

func TestDeliverSendsTheRequest(t *testing.T) {
	var got coreRequest
	srv, _ := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, r coreRequest) {
		got = r
		writeResult(t, w, delivered(replaywire.NodeDelivered))
	})
	out := newTestExecutor(t, srv.URL+"/", metrics.New()).Deliver(context.Background(), testRun(), testSnapshot)
	if out.Status != store.RunSucceeded {
		t.Fatalf("outcome = %+v", out)
	}
	want := replaywire.DeliverRequest{RunID: testRunID, RecordID: testRecordID, Kind: replaywire.KindFailure, ReplayMode: replaywire.ReplayResume, Flow: "orders",
		NodeID: "out_s3", NodeType: "publish", DestinationID: "archive", DestinationType: "s3", RenderedTarget: "uploads",
		NodeConfigHash: "node-hash", DestinationHash: "dest-hash", SnapshotSHA256: digest(testSnapshot), SnapshotVersion: 1, DeadlineMS: 25000,
		InboundStatus: 202, MaxRecordBytes: 1 << 20}
	if got.req != want || !bytes.Equal(got.snapshot, testSnapshot) || got.auth != "Bearer "+controlToken || got.path != "/internal/replay/v1/deliver" {
		t.Fatalf("core got %+v\nwant request %+v", got, want)
	}
}

func TestDeliverDefiniteOutcomes(t *testing.T) {
	cases := map[string]struct {
		respond        func(t *testing.T, w http.ResponseWriter)
		status, reason string
		call           string
	}{
		"delivered": {func(t *testing.T, w http.ResponseWriter) { writeResult(t, w, delivered(replaywire.NodeDelivered)) }, store.RunSucceeded, "", metrics.CallOK},
		"handled":   {func(t *testing.T, w http.ResponseWriter) { writeResult(t, w, delivered(replaywire.NodeHandled)) }, store.RunSucceeded, "", metrics.CallOK},
		"node failed": {func(t *testing.T, w http.ResponseWriter) { writeResult(t, w, delivered(replaywire.NodeFailed)) },
			store.RunFailed, ReasonNodeFailed, metrics.CallOK},
		"blocked":       {func(_ *testing.T, w http.ResponseWriter) { writeProblem(w, 422, replaywire.BlockFlowMissing) }, store.RunBlocked, replaywire.BlockFlowMissing, metrics.CallBlocked},
		"blocked oddly": {func(_ *testing.T, w http.ResponseWriter) { writeProblem(w, 422, "Not A Code") }, store.RunBlocked, "unknown_block_reason", metrics.CallBlocked},
		"unauthorized":  {func(_ *testing.T, w http.ResponseWriter) { writeProblem(w, 401, "unauthorized") }, store.RunFailed, ReasonExecutorAuth, metrics.CallAuth},
		"forbidden":     {func(_ *testing.T, w http.ResponseWriter) { writeProblem(w, 403, "forbidden") }, store.RunFailed, ReasonExecutorAuth, metrics.CallAuth},
		"not found":     {func(_ *testing.T, w http.ResponseWriter) { w.WriteHeader(404) }, store.RunFailed, ReasonExecutorRejected, metrics.CallRejected},
		"redirect": {func(_ *testing.T, w http.ResponseWriter) {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(307)
		}, store.RunFailed, ReasonExecutorRejected, metrics.CallRejected},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, _ coreRequest) { c.respond(t, w) })
			m := metrics.New()
			out := newTestExecutor(t, srv.URL, m).Deliver(context.Background(), testRun(), testSnapshot)
			if out.Status != c.status || out.Reason != c.reason || out.Call != c.call || calls.Load() != 1 {
				t.Fatalf("outcome %+v after %d calls; want %s/%s/%s after 1", out, calls.Load(), c.status, c.reason, c.call)
			}
			if got := metricValue(t, m, "hookway_replay_executor_calls_total", prometheus.Labels{"outcome": c.call}); got != 1 {
				t.Fatalf("executor_calls_total{%s} = %v", c.call, got)
			}
		})
	}
}

func TestDeliverReturnsChildren(t *testing.T) {
	childSnap, childPay := []byte("child-snapshot"), []byte("child-payload")
	const fullID, metaID = "01926d3c-aaaa-7b3a-9c1e-2f4a5b6c7d8e", "01926d3c-bbbb-7b3a-9c1e-2f4a5b6c7d8e"
	srv, _ := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, _ coreRequest) {
		result := delivered(replaywire.NodeDelivered)
		result.Nodes = append(result.Nodes, replaywire.NodeOutcome{NodeID: "out_lambda", Outcome: replaywire.NodeFailed})
		result.Children = []replaywire.RecordMetadata{childMetadata(fullID, childSnap, childPay), childMetadata(metaID, nil, nil)}
		writeResult(t, w, result, respPart{replaywire.ChildSnapshotPart(0), childSnap}, respPart{replaywire.ChildPayloadPart(0), childPay})
	})
	out := newTestExecutor(t, srv.URL, metrics.New()).Deliver(context.Background(), testRun(), testSnapshot)
	if out.Status != store.RunSucceeded || len(out.Children) != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if c := out.Children[0]; c.Metadata.ID != fullID || !bytes.Equal(c.Snapshot, childSnap) || !bytes.Equal(c.Payload, childPay) {
		t.Fatalf("child 0 = %+v", c)
	}
	if c := out.Children[1]; c.Metadata.ID != metaID || c.Snapshot != nil || c.Payload != nil {
		t.Fatalf("child 1 = %+v", c)
	}
}

func TestDeliverRetriesWhileCoreHasNotStarted(t *testing.T) {
	var n atomic.Int32
	srv, calls := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, _ coreRequest) {
		if n.Add(1) <= 2 {
			writeProblem(w, 503, replaywire.NotStartedCode)
			return
		}
		writeResult(t, w, delivered(replaywire.NodeDelivered))
	})
	m := metrics.New()
	out := newTestExecutor(t, srv.URL, m).Deliver(context.Background(), testRun(), testSnapshot)
	if out.Status != store.RunSucceeded || calls.Load() != 3 {
		t.Fatalf("outcome %+v after %d calls", out, calls.Load())
	}
	if metricValue(t, m, "hookway_replay_executor_calls_total", prometheus.Labels{"outcome": metrics.CallUnavailable}) != 2 {
		t.Fatal("each not_started attempt must count as an unavailable call")
	}
}

func TestDeliverGivesUpAfterThreeUnavailableAttempts(t *testing.T) {
	srv, calls := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, _ coreRequest) {
		writeProblem(w, 503, replaywire.NotStartedCode)
	})
	out := newTestExecutor(t, srv.URL, metrics.New()).Deliver(context.Background(), testRun(), testSnapshot)
	if out.Status != store.RunFailed || out.Reason != ReasonExecutorUnavailable || calls.Load() != 3 {
		t.Fatalf("outcome %+v after %d calls", out, calls.Load())
	}
}

func TestDeliverDialFailureIsUnavailable(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	_ = l.Close()
	out := newTestExecutor(t, url, metrics.New()).Deliver(context.Background(), testRun(), testSnapshot)
	if out.Status != store.RunFailed || out.Reason != ReasonExecutorUnavailable {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestExecutorTransportDetailsRedactCredentialsWithoutChangingClassification(t *testing.T) {
	for _, written := range []bool{false, true} {
		t.Run(fmt.Sprintf("request-written=%t", written), func(t *testing.T) {
			srv, calls := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, _ coreRequest) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			})
			if !written {
				srv.Close()
			}
			endpoint := strings.Replace(srv.URL, "http://", "http://user:USERSECRET@", 1) + `/PATHSECRET?api_key=quoted"QUERYSECRET`
			out := newTestExecutor(t, endpoint, metrics.New()).Deliver(t.Context(), testRun(), testSnapshot)
			wantStatus, wantReason, wantCall := store.RunFailed, ReasonExecutorUnavailable, metrics.CallUnavailable
			if written {
				wantStatus, wantReason, wantCall = store.RunUnknown, ReasonAmbiguous, metrics.CallAmbiguous
				if calls.Load() != 1 {
					t.Fatal("an ambiguous written request was resent")
				}
			}
			if out.Status != wantStatus || out.Reason != wantReason || out.Call != wantCall {
				t.Fatalf("transport failure classification changed: %+v", out)
			}
			for _, marker := range []string{"USERSECRET", "PATHSECRET", "QUERYSECRET", controlToken} {
				if strings.Contains(out.Detail, marker) {
					t.Fatalf("stored transport detail retained %s", marker)
				}
			}
			if !strings.Contains(out.Detail, srv.URL) || (!strings.Contains(out.Detail, "connection refused") && !strings.Contains(out.Detail, "EOF")) {
				t.Fatal("safe diagnostic lost host or transport failure class")
			}
		})
	}
}

func TestExecutorLocalTLSProxyVerifiesCAAndPreservesControlRequest(t *testing.T) {
	core, calls := fakeCore(t, func(w http.ResponseWriter, r *http.Request, got coreRequest) {
		if r.TLS != nil || got.auth != "Bearer "+controlToken || !bytes.Equal(got.snapshot, testSnapshot) || got.req.RunID != testRun().Run.RunID {
			t.Error("TLS termination changed the HTTP backend's control authentication/snapshot/run")
		}
		writeResult(t, w, delivered(replaywire.NodeDelivered))
	})
	upstream, err := url.Parse(core.URL)
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
			before := calls.Load()
			e, err := NewExecutor(ExecutorOptions{ControlURL: proxy.URL, Token: controlToken, TLS: tc.tls, ExecutionTimeout: 30 * time.Second, MaxRecordBytes: 1 << 20, Metrics: metrics.New()})
			if err != nil {
				t.Fatal(err)
			}
			e.backoff = time.Millisecond
			out := e.Deliver(t.Context(), testRun(), testSnapshot)
			if tc.trusted {
				if out.Status != store.RunSucceeded || out.Call != metrics.CallOK || calls.Load() != before+1 {
					t.Fatalf("verified TLS proxy did not deliver once: %+v calls=%d", out, calls.Load()-before)
				}
			} else if out.Status != store.RunFailed || out.Reason != ReasonExecutorUnavailable || out.Call != metrics.CallUnavailable || calls.Load() != before {
				t.Fatalf("TLS refusal crossed the control boundary or changed failure semantics: %+v calls=%d", out, calls.Load()-before)
			}
		})
	}
}

func TestDeliverAmbiguousOutcomesAreNeverResent(t *testing.T) {
	foreign := delivered(replaywire.NodeDelivered)
	foreignChild := childMetadata("01926d3c-cccc-7b3a-9c1e-2f4a5b6c7d8e", nil, nil)
	foreignChild.EventID = "evt-other"
	foreign.Children = []replaywire.RecordMetadata{foreignChild}
	mismatch := delivered(replaywire.NodeDelivered)
	mismatch.Children = []replaywire.RecordMetadata{childMetadata("01926d3c-dddd-7b3a-9c1e-2f4a5b6c7d8e", []byte("a"), []byte("b"))}
	big := delivered(replaywire.NodeDelivered)
	bigSnap := bytes.Repeat([]byte("s"), 64)
	big.Children = []replaywire.RecordMetadata{childMetadata("01926d3c-eeee-7b3a-9c1e-2f4a5b6c7d8e", bigSnap, []byte("p"))}
	cases := map[string]func(t *testing.T, w http.ResponseWriter, r *http.Request){
		"500":                     func(_ *testing.T, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"503 without not_started": func(_ *testing.T, w http.ResponseWriter, _ *http.Request) { writeProblem(w, 503, "overloaded") },
		"timeout after the request was written": func(_ *testing.T, _ http.ResponseWriter, _ *http.Request) {
			time.Sleep(500 * time.Millisecond)
		},
		"reset after the request was written": func(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		},
		"200 not multipart": func(_ *testing.T, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) },
		"200 bad json": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			writeParts(t, w, respPart{replaywire.PartResult, []byte(`{"nodes": [`)})
		},
		"200 unknown field": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			writeResult(t, w, map[string]any{"nodes": delivered(replaywire.NodeDelivered).Nodes, "surprise": 1})
		},
		"200 without result": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			_ = mw.Close()
			w.Header().Set("Content-Type", mw.FormDataContentType())
			_, _ = w.Write(buf.Bytes())
		},
		"200 resume node missing": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			writeResult(t, w, replaywire.DeliverResult{Nodes: []replaywire.NodeOutcome{{NodeID: "other", Outcome: replaywire.NodeDelivered}}})
		},
		"200 foreign child": func(t *testing.T, w http.ResponseWriter, _ *http.Request) { writeResult(t, w, foreign) },
		"200 child digest mismatch": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			writeResult(t, w, mismatch, respPart{replaywire.ChildSnapshotPart(0), []byte("x")}, respPart{replaywire.ChildPayloadPart(0), []byte("b")})
		},
		"200 unexpected part": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			writeResult(t, w, delivered(replaywire.NodeDelivered), respPart{"extra", []byte("x")})
		},
		"200 oversized part": func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
			writeResult(t, w, big, respPart{replaywire.ChildSnapshotPart(0), bigSnap}, respPart{replaywire.ChildPayloadPart(0), []byte("p")})
		},
	}
	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := fakeCore(t, func(w http.ResponseWriter, r *http.Request, _ coreRequest) { respond(t, w, r) })
			e := newTestExecutor(t, srv.URL, metrics.New())
			e.timeout, e.maxRecordBytes = 150*time.Millisecond, 32
			out := e.Deliver(context.Background(), testRun(), testSnapshot)
			if out.Status != store.RunUnknown || out.Reason != ReasonAmbiguous || out.Call != metrics.CallAmbiguous || calls.Load() != 1 {
				t.Fatalf("outcome %+v after %d calls; want unknown/ambiguous after exactly 1", out, calls.Load())
			}
		})
	}
}

func TestNewExecutorRejectsBadTLS(t *testing.T) {
	_, err := NewExecutor(ExecutorOptions{ControlURL: "https://core", Token: controlToken, ExecutionTimeout: 30 * time.Second,
		TLS: tlsfiles.Files{CACert: "/nonexistent/ca.pem"}})
	if err == nil || !strings.Contains(err.Error(), "core tls") {
		t.Fatalf("NewExecutor = %v", err)
	}
}

// HTTP/2's transport resends a request by itself after some stream resets —
// a resend the executor cannot see. The control client must speak HTTP/1.1.
func TestDeliverNeverUsesHTTP2(t *testing.T) {
	var proto atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto.Store(int64(r.ProtoMajor))
		writeResult(t, w, delivered(replaywire.NodeDelivered))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	e, err := NewExecutor(ExecutorOptions{ControlURL: srv.URL, Token: controlToken, ExecutionTimeout: 30 * time.Second,
		MaxRecordBytes: 1 << 20, TLS: tlsfiles.Files{InsecureSkipVerify: true}, Metrics: metrics.New()})
	if err != nil {
		t.Fatal(err)
	}
	if out := e.Deliver(context.Background(), testRun(), testSnapshot); out.Status != store.RunSucceeded || proto.Load() != 1 {
		t.Fatalf("outcome %+v over HTTP/%d, want success over HTTP/1", out, proto.Load())
	}
}

func TestDeliverRejectsChildrenThatCannotBelongToTheRun(t *testing.T) {
	childSnap, childPay := []byte("child-snapshot"), []byte("child-payload")
	cases := map[string]struct {
		run   func() store.ClaimedRun
		child func() replaywire.RecordMetadata
	}{
		"publish_only run": {
			run: func() store.ClaimedRun {
				r := testRun()
				r.Record.ReplayMode = replaywire.ReplayPublishOnly
				return r
			},
			child: func() replaywire.RecordMetadata {
				return childMetadata("01926d3c-aaaa-7b3a-9c1e-2f4a5b6c7d8e", childSnap, childPay)
			},
		},
		"child reuses the record id": {
			run:   testRun,
			child: func() replaywire.RecordMetadata { return childMetadata(testRecordID, childSnap, childPay) },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := fakeCore(t, func(w http.ResponseWriter, _ *http.Request, _ coreRequest) {
				result := delivered(replaywire.NodeDelivered)
				result.Nodes = append(result.Nodes, replaywire.NodeOutcome{NodeID: "out_lambda", Outcome: replaywire.NodeFailed})
				result.Children = []replaywire.RecordMetadata{c.child()}
				writeResult(t, w, result, respPart{replaywire.ChildSnapshotPart(0), childSnap}, respPart{replaywire.ChildPayloadPart(0), childPay})
			})
			out := newTestExecutor(t, srv.URL, metrics.New()).Deliver(context.Background(), c.run(), testSnapshot)
			if out.Status != store.RunUnknown || out.Reason != ReasonAmbiguous {
				t.Fatalf("outcome = %+v, want ambiguous", out)
			}
		})
	}
}

func TestExecutorDropsIdleConnectionsBeforeCore(t *testing.T) {
	e := newTestExecutor(t, "http://127.0.0.1:1", metrics.New())
	tr, ok := e.client.Transport.(*http.Transport)
	if !ok || tr.IdleConnTimeout != replaywire.ControlClientIdleTimeout {
		t.Fatalf("transport idle timeout = %v, want %v", tr.IdleConnTimeout, replaywire.ControlClientIdleTimeout)
	}
}

func TestExecutorRejectsInspectionBeforeNetwork(t *testing.T) {
	core, calls := fakeCore(t, func(http.ResponseWriter, *http.Request, coreRequest) { t.Error("inspection reached core") })
	e, err := NewExecutor(ExecutorOptions{ControlURL: core.URL, Token: controlToken, ExecutionTimeout: 30 * time.Second, Metrics: metrics.New()})
	if err != nil {
		t.Fatal(err)
	}
	outcome := e.Deliver(t.Context(), store.ClaimedRun{Record: store.Record{RecordMetadata: replaywire.RecordMetadata{Kind: replaywire.KindInspection, Retryable: true}}}, []byte("forged"))
	if outcome.Status != store.RunBlocked || calls.Load() != 0 {
		t.Fatalf("outcome: %+v calls=%d", outcome, calls.Load())
	}
}
