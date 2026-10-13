package acceptance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func startReceiptCore(t *testing.T, destination *sink, mode string, timeout ...time.Duration) (*process, string) {
	t.Helper()
	core, _ := binaries(t)
	addr := freeAddr(t)
	path := filepath.Join(t.TempDir(), "core.yaml")
	deliveryTimeout := 10 * time.Second
	if len(timeout) > 0 {
		deliveryTimeout = timeout[0]
	}
	writeConfig(t, path, fmt.Sprintf(`
state_stores: {local: {namespace: receipt-fixture, memory: {}}}
server: {addr: "%s", max_body_bytes: 65536, read_timeout: 5s, write_timeout: 5s, publish_timeout: 4s, shutdown_timeout: 5s, fanout_concurrency: 8}
destinations:
  receipt: {type: webhook, webhook: {url: "%s", delivery_mode: %s, timeout: "%s", max_queue: 8, drain_timeout: 5s, retry: {max_attempts: 1}}}
flows:
  receipt:
    nodes:
      - {id: in, type: http_trigger, path: /receipt, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "${ORDERS_WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256}
      - {id: guard, type: replay_guard, store: local, key: '{header.X-Event-ID}', ttl: 5m}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: receipt, target: accepted}
    edges: [in>body, body>auth, auth>guard, guard>env, env>out]
`, addr, destination.srv.URL, mode, deliveryTimeout))
	p := startProcess(t, "receipt-core-"+mode, core, []string{"ORDERS_WEBHOOK_SECRET=" + webhookSecret}, "--config", path)
	u := "http://" + addr
	waitReady(t, "receipt core", u+"/readyz")
	return p, u
}

// A receipt is committed by the independent destination before the caller loses
// its connection. I10 permits publication to complete; the retry must not assume
// that the disconnected attempt did no work.
func TestAcceptance_CallerDisconnectAfterReceiptRetainsGuard(t *testing.T) {
	binaries(t)
	destination := newSink(t)
	received, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	destination.accepted = func(ctx context.Context) {
		once.Do(func() { close(received) })
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { onceRelease(release) })
	p, u := startReceiptCore(t, destination, "sync")
	body := []byte(`{"event_id":"caller-lost","order":"accepted"}`)
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", strings.TrimPrefix(u, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "POST /receipt HTTP/1.1\r\nHost: fixture\r\nContent-Type: application/json\r\nX-Event-ID: caller-lost\r\nX-Hub-Signature-256: sha256=%s\r\nContent-Length: %d\r\n\r\n%s", hex.EncodeToString(mac.Sum(nil)), len(body), body); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("destination did not independently accept request")
	}
	_ = conn.Close() // no status was observed; destination evidence remains available
	close(release)
	// The handler emits this completion record only after publication and any
	// compensation. A retry during the still-active request would prove merely
	// in-flight exclusion, not that the completed uncertain effect kept its guard.
	// No other flow request is sent to this fixture before this barrier.
	waitFor(t, "original disconnected flow completion", 5*time.Second, func() bool {
		return strings.Contains(p.out.String(), "flow completed")
	})
	if code, err := signedRequest(u+"/receipt", "caller-lost", body); err != nil || code != 409 {
		t.Fatalf("disconnected sender retry = %d %v", code, err)
	}
	destination.assertLedger(t, "caller-lost", receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
	select {
	case <-p.done:
		t.Fatal("core exited on caller disconnect")
	default:
	}
	t.Log("sender reconciliation event=caller-lost attempts=2 HTTP=[unobserved,409]; destination committed once; no claim that first HTTP succeeded")
}

// Cleanup and the test are the only owners of these fixture barriers.
func onceRelease(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func TestAcceptance_DownstreamTimeoutAfterCommitRetainsGuard(t *testing.T) {
	binaries(t)
	destination := newSink(t)
	destination.accepted = func(ctx context.Context) { <-ctx.Done() }
	_, u := startReceiptCore(t, destination, "sync", 250*time.Millisecond)
	body := []byte(`{"event_id":"destination-timeout"}`)
	if code, err := signedRequest(u+"/receipt", "destination-timeout", body); err != nil || code != 503 {
		t.Fatalf("accepted downstream with lost response = %d %v", code, err)
	}
	if code, err := signedRequest(u+"/receipt", "destination-timeout", body); err != nil || code != 409 {
		t.Fatalf("uncertain downstream retry = %d %v", code, err)
	}
	destination.assertLedger(t, "destination-timeout", receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
	t.Log("sender reconciliation event=destination-timeout attempts=2 HTTP=[503,409]; independent receipt proves destination commit despite unsuccessful HTTP acknowledgement")
}

func TestAcceptance_AsyncAcknowledgementGracefulDrainAndForcedLoss(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(fmt.Sprintf("forced=%t", forced), func(t *testing.T) {
			binaries(t)
			destination := newSink(t)
			received, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			destination.accepted = func(ctx context.Context) {
				once.Do(func() { close(received) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			t.Cleanup(func() { onceRelease(release) })
			p, u := startReceiptCore(t, destination, "async")
			// The native destination has eight workers. Hold all eight responses
			// before admitting two additional jobs into its volatile queue.
			const workers, offered = 8, 10
			for i := range offered {
				event := fmt.Sprintf("async-%d", i)
				if code, err := signedRequest(u+"/receipt", event, []byte(fmt.Sprintf(`{"event_id":%q}`, event))); err != nil || code != 202 {
					t.Fatal(code, err)
				}
				if i < workers {
					waitFor(t, "async worker receipt", 5*time.Second, func() bool {
						destination.mu.Lock()
						defer destination.mu.Unlock()
						return destination.ledger[event].Receipts == 1
					})
				}
			}
			for i := range workers {
				destination.assertLedger(t, fmt.Sprintf("async-%d", i), receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
			}
			for i := workers; i < offered; i++ {
				destination.assertLedger(t, fmt.Sprintf("async-%d", i), receiptLedger{})
			}
			if forced {
				p.once.Do(func() { _ = p.cmd.Process.Kill(); <-p.done })
				close(release)
			} else {
				stopped := make(chan struct{})
				go func() { p.stop(); close(stopped) }()
				// First withdraw HTTP admission; then let the independent destination
				// return and the async worker drain the already acknowledged jobs.
				waitFor(t, "shutdown admission withdrawal", 5*time.Second, func() bool { code, _, err := get(u + "/readyz"); return err != nil || code != 200 })
				close(release)
				select {
				case <-stopped:
				case <-time.After(10 * time.Second):
					t.Fatal("graceful stop did not drain")
				}
				if !p.cmd.ProcessState.Success() {
					t.Fatalf("graceful core failed: %s", p.out.String())
				}
			}
			// Replace the native process with the same configuration; receipt state
			// stays outside it. A fresh successful event proves recovery, not replay
			// of the two volatile jobs lost by SIGKILL.
			replacement := startProcess(t, "receipt-core-replacement", p.cmd.Path, p.cmd.Env, p.cmd.Args[1:]...)
			waitReady(t, "replacement receipt core", u+"/readyz")
			if code, err := signedRequest(u+"/receipt", "recovery", []byte(`{"event_id":"recovery"}`)); err != nil || code != 202 {
				t.Fatal(code, err)
			}
			waitFor(t, "replacement delivery receipt", 5*time.Second, func() bool {
				destination.mu.Lock()
				defer destination.mu.Unlock()
				return destination.ledger["recovery"].Receipts == 1
			})
			replacement.stop()
			for i := range offered {
				want := receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1}
				if forced && i >= workers {
					want = receiptLedger{}
				}
				destination.assertLedger(t, fmt.Sprintf("async-%d", i), want)
			}
			destination.assertLedger(t, "recovery", receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
			t.Logf("sender reconciliation forced=%t attempts=11 acknowledged=11 volatile_queued_loss=%d; eight pending destination responses are not treated as absent effects", forced, map[bool]int{true: 2, false: 0}[forced])
		})
	}
}

func TestAcceptance_RetentionFullDoesNotChangeHealthyIngress(t *testing.T) {
	s := startSystem(t)
	for i, p := range s.replays {
		p.stop()
		path := p.cmd.Args[len(p.cmd.Args)-1]
		src, err := os.ReadFile(path) // #nosec G304 -- fixture-owned temporary configuration
		if err != nil {
			t.Fatal(err)
		}
		writeConfig(t, path, strings.Replace(string(src), "max_records: 10000", "max_records: 1", 1))
		s.replays[i] = startProcess(t, p.name+"-limited", p.cmd.Path, p.cmd.Env, p.cmd.Args[1:]...)
	}
	waitReady(t, "limited replay", s.replayURL+"/readyz")
	s.audit.failing.Store(true)
	for i := range 2 {
		event := fmt.Sprintf("retention-%d", i)
		if code := s.post(t, []byte(fmt.Sprintf(`{"event_id":%q}`, event))); code != http.StatusAccepted {
			t.Fatalf("capacity event HTTP %d", code)
		}
	}
	waitFor(t, "observable replay capacity rejection", 10*time.Second, func() bool {
		var drops float64
		for _, u := range s.coreMetrics {
			drops += metric(t, u, `hookway_capture_dropped_total{reason="capacity"}`)
		}
		return drops >= 1
	})
	var open struct {
		Items []failureItem `json:"items"`
	}
	if code := s.api(t, http.MethodGet, "/v1/records?kind=failure&delivery_status=open&dismissed=false", nil, &open); code != 200 || len(open.Items) != 1 {
		t.Fatalf("retained records %d %+v", code, open.Items)
	}
	for _, u := range s.coreReady {
		if code, _, err := get(u); err != nil || code != 200 {
			t.Fatalf("full replay changed core readiness %d %v", code, err)
		}
	}
	for i := range 2 {
		event := fmt.Sprintf("retention-%d", i)
		s.orders.assertLedger(t, event, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
		s.audit.assertLedger(t, event, receiptLedger{Attempts: 1})
	}
	t.Log("retention reconciliation sender_attempts=2 HTTP202=2 good_destination_receipts=2 failed_destination_receipts=0 retained_failures=1 capacity_drops>=1; no durable capture guarantee")
}
