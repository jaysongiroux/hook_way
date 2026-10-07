// This test-only service backs browser tests with the production API and a real,
// disposable PostgreSQL database. No fixture route is compiled into a binary.
package consolebrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaysongiroux/hook_way/internal/replay/api"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestMain(m *testing.M)  { os.Exit(replaytest.Main(m)) }
func fixtureID(n int) string { return fmt.Sprintf("01926d3c-8d6f-7b3a-9c1e-%012d", n) }
func artifact(data []byte, version int, contentType string) *replaywire.ArtifactInfo {
	sum := sha256.Sum256(data)
	return &replaywire.ArtifactInfo{Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Version: version, ContentType: contentType}
}

func TestConsoleBrowserFixture(t *testing.T) {
	if os.Getenv("HOOKWAY_CONSOLE_FIXTURE") != "1" {
		t.Skip("browser fixture requires explicit opt-in")
	}
	ctx := context.Background()
	database := replaytest.Database(t)
	st, err := store.Open(ctx, store.Options{URL: database, Limits: store.Limits{TTL: time.Hour, MaxRecords: 1000, MaxBytes: 8 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	seed := func() error {
		if _, err := pool.Exec(ctx, "TRUNCATE records CASCADE; UPDATE usage SET records=0, bytes=0"); err != nil {
			return err
		}
		now := time.Now().UTC()
		makeFailure := func(n int) store.NewRecord {
			payload := []byte(`{"event":"order.created","amount":9007199254740993,"decimal":0.1234567890123456789,"unsafe":"<script>window.payloadExecuted=true</script>"}`)
			if n == 7 {
				payload = []byte(strings.Repeat("x", 80*1024))
			}
			if n == 8 {
				payload = []byte{0, 1, 2, 255}
			}
			if n == 68 {
				payload = []byte(`{"body":{"fields":{"account":"synthetic"},"files":{"document":{"filename":"receipt.pdf","content_type":"application/pdf","size":4,"content":"AAEC/w=="},"notes":{"filename":"notes.txt","content_type":"text/plain","size":12,"content_omitted":true}}}}`)
			}
			snapshot := []byte("browser-fixture-snapshot")
			f := store.NewRecord{Metadata: replaywire.RecordMetadata{ID: fixtureID(n), Kind: replaywire.KindFailure, ReplayMode: replaywire.ReplayPublishOnly, Flow: "orders", NodeID: "publish_receipt", NodeType: "publish", DestinationID: "receipt-webhook", DestinationType: "webhook", Target: "https://destination.example.test/hooks/orders", RenderedTarget: "https://destination.example.test/hooks/orders", EventID: fmt.Sprintf("evt-demo-%d", n), OccurredAt: now.Add(-time.Duration(n) * time.Minute), ReceivedAt: now.Add(-time.Duration(n)*time.Minute - time.Second), InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, ErrorMessage: "Destination returned HTTP 503. No receipt was confirmed.", Attempts: 2, Retryable: true, Snapshot: artifact(snapshot, 1, ""), Payload: artifact(payload, 0, "application/json")}, Snapshot: snapshot, Payload: payload, Actor: "demo-capture"}
			if n == 4 || n == 100 {
				f.Metadata.ReplayMode = replaywire.ReplayResume
			}
			if n == 8 {
				f.Metadata.Payload.ContentType = "application/octet-stream"
				f.Metadata.Payload.Filename = "original.bin"
			}
			if n == 6 {
				f.Snapshot = nil
				f.Payload = nil
				f.Metadata.Snapshot = nil
				f.Metadata.Payload = nil
				f.Metadata.Retryable = false
				f.Metadata.NotRetryableReason = replaywire.NotRetryableTooLarge
				f.Metadata.PayloadUnavailableReason = replaywire.NotRetryableTooLarge
			}
			if n > 8 {
				f.Metadata.Flow = "archive"
			}
			return f
		}
		for n := 1; n <= 68; n++ {
			f := makeFailure(n)
			if err := f.Metadata.Validate(); err != nil {
				return err
			}
			if _, err := st.Ingest(ctx, f); err != nil {
				return err
			}
			if n >= 2 && n <= 4 {
				run, err := st.RequestRetry(ctx, store.RetryRequest{RecordID: f.Metadata.ID, Actor: "demo-operator", IdempotencyKey: fmt.Sprintf("fixture-%d", n)})
				if err != nil {
					return err
				}
				claimed, err := st.ClaimRuns(ctx, "demo-worker", 1, time.Minute)
				if err != nil || len(claimed) != 1 {
					return fmt.Errorf("claim: %w", err)
				}
				state := map[int]string{2: store.RunBlocked, 3: store.RunUnknown, 4: store.RunSucceeded}[n]
				outcome := store.RunOutcome{RunID: run.RunID, Status: state, Actor: "demo-worker", Result: json.RawMessage(`{"reason":"configuration_changed","detail":"Check destination configuration before retrying."}`)}
				if n == 3 {
					outcome.Result = json.RawMessage(`{"reason":"response_lost","detail":"Destination may have received the request."}`)
				}
				if n == 4 {
					outcome.Result = json.RawMessage(`{"delivered":true}`)
					outcome.Children = []store.NewRecord{makeFailure(100)}
				}
				if _, err := st.FinalizeRun(ctx, outcome); err != nil {
					return err
				}
			}
			if n == 5 {
				if _, err := st.Dismiss(ctx, f.Metadata.ID, "demo-operator"); err != nil {
					return err
				}
			}
		}
		for n := 201; n <= 210; n++ {
			body := replaywire.InspectionBody{ContentType: "application/json", Encoding: "utf8", Content: `{"event":"order.created","amount":9007199254740993,"decimal":0.1234567890123456789,"unsafe":"<script>window.payloadExecuted=true</script>"}`}
			switch n {
			case 202:
				body = replaywire.InspectionBody{ContentType: "application/octet-stream", Encoding: "base64", Content: "AAEC/w=="}
			case 204:
				body.Content = strings.Repeat("x", 1100*1024)
			case 205:
				body.Content = ""
			case 206:
				body = replaywire.InspectionBody{ContentType: "application/xml", Encoding: "utf8", Content: `<order><id>42</id><script>window.payloadExecuted=true</script></order>`}
			case 207:
				body.Content = strings.Repeat("x", 80*1024)
			case 209:
				body.Content = `{"fields":{"account":"synthetic"},"files":{"document":{"filename":"receipt.pdf","content_type":"application/pdf","size":4,"content":"AAEC/w=="},"document#2":{"filename":"second receipt.pdf","content_type":"application/pdf","size":0,"content":""},"notes":{"filename":"notes.txt","content_type":"text/plain","size":12,"content_omitted":true}}}`
			case 210:
				// Larger than the text preview limit: file downloads must not be truncated.
				body.Content = fmt.Sprintf(`{"fields":{},"files":{"large":{"filename":"large.txt","content_type":"text/plain","size":%d,"content":"%s"}}}`, 900*1024, strings.Repeat("eHh4", 300*1024))
			}
			payload, err := json.Marshal(replaywire.InspectionPayload{Version: replaywire.InspectionPayloadVersion, Body: body, Headers: http.Header{"authorization": {"[REDACTED]"}, "content-type": {body.ContentType}}, Variables: map[string]string{"tenant": "north", "order_id": "42"}, Method: "POST", Path: "/webhooks/orders/42"})
			if err != nil {
				return err
			}
			observed := now.Add(-time.Duration(n-200) * time.Minute)
			f := store.NewRecord{Metadata: replaywire.RecordMetadata{ID: fixtureID(n), Kind: replaywire.KindInspection, Flow: "orders", NodeID: "inspect_sample", NodeType: "inspect", EventID: fmt.Sprintf("evt-inspect-%d", n), RequestID: "request-inspect-42", TraceID: "trace-inspect-42", OccurredAt: observed, ReceivedAt: observed.Add(-time.Second), InboundStatus: 503, Retryable: false, NotRetryableReason: replaywire.NotRetryableInspectionOnly, Payload: artifact(payload, 0, "application/json")}, Payload: payload, Actor: "demo-capture"}
			if n == 203 {
				f.Metadata.Payload = nil
				f.Payload = nil
				f.Metadata.PayloadUnavailableReason = replaywire.NotRetryableTooLarge
			}
			if err := f.Metadata.Validate(); err != nil {
				return err
			}
			if _, err := st.Ingest(ctx, f); err != nil {
				return err
			}
			if n == 208 {
				if _, err := st.Dismiss(ctx, f.Metadata.ID, "demo-operator"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := seed(); err != nil {
		t.Fatal(err)
	}
	m := metrics.New()
	a := auth.New([]auth.Key{
		// #nosec G101 -- Public synthetic token in a loopback-only test executable, never production.
		{Name: "demo-operator", Token: "console-ops-token-0123456789abcdefghijkl", Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeRetry, auth.ScopePayload}},
		{Name: "demo-reader", Token: "console-read-token-0123456789abcdefghijkl", Scopes: []auth.Scope{auth.ScopeRead}},
		{Name: "demo-recovery", Token: "console-retry-token-0123456789abcdefghijkl", Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeRetry}},
	}, m.AuthFailure)
	production := api.New(api.Options{Store: st, Auth: a, Metrics: m, MaxRecordBytes: 1 << 20, AllowedOrigins: []string{"http://127.0.0.1:5183"}})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /test/reset", func(w http.ResponseWriter, r *http.Request) {
		if err := seed(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	})
	mux.Handle("/", production)
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:5184")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	stopped, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, 1)
	go func() { failures <- server.Serve(listener) }()
	select {
	case <-stopped.Done():
		_ = server.Close()
	case err := <-failures:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}
}
