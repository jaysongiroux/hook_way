package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// Observe the broker independently of core. Losing a sender response does not
// undo a confirmed publication; provider retries may create another envelope.
// Consumer idempotency uses the signed body's stable event ID, not that envelope.
func TestAcceptance_BrokerOutageAndLostAcknowledgementReceipts(t *testing.T) {
	coreBin, _ := binaries(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	broker, err := tcrabbitmq.Run(ctx, "rabbitmq:3-management")
	testenv.RequireContainer(t, err, "rabbitmq")
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(broker) })
	brokerURL, err := broker.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	config := filepath.Join(t.TempDir(), "broker.yaml")
	writeConfig(t, config, fmt.Sprintf(`
server: {addr: %q, max_body_bytes: 1048576, publish_timeout: 1s, write_timeout: 5s}
destinations:
  events:
    type: rabbitmq
    rabbitmq: {url: "${RABBITMQ_URL}", exchange: receipt-events, exchange_type: direct, durable: true, declare_queues: true, publish_timeout: 1s, retry: {max_attempts: 1}}
flows:
  orders:
    delivery_failure_policy: stop_on_failure
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "${ORDERS_WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: events, target: orders, create: true}
    edges: [in>body, body>auth, auth>env, env>out]
`, addr))
	startProcess(t, "broker-receipt-core", coreBin, []string{"RABBITMQ_URL=" + brokerURL, "ORDERS_WEBHOOK_SECRET=" + webhookSecret}, "--config", config)
	coreURL := "http://" + addr
	waitReady(t, "broker receipt core", coreURL+"/readyz")

	// Forward exactly once, wait for the confirmed core response, then discard
	// it at the sender boundary. No retrying ReverseProxy transport is involved.
	confirmed := make(chan int, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme, r.URL.Host = "http", addr
		r.RequestURI = ""
		resp, callErr := client.Do(r.Clone(r.Context())) // #nosec G704 -- URL authority is overwritten with the test-owned loopback core; input cannot choose the destination.
		if callErr != nil {
			http.Error(w, callErr.Error(), http.StatusBadGateway)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		confirmed <- resp.StatusCode
		conn, _, hijackErr := w.(http.Hijacker).Hijack()
		if hijackErr == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(proxy.Close)
	const event = "broker-lost-reply"
	body := []byte(`{"event_id":"` + event + `"}`)
	sender := &system{coreURL: proxy.URL}
	status, callErr := sender.postResult(body)
	if callErr == nil || status != 0 {
		t.Fatalf("sender should observe no HTTP acknowledgement: status=%d err=%v", status, callErr)
	}
	select {
	case status := <-confirmed:
		if status != http.StatusAccepted {
			t.Fatalf("core response before disconnect = %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not observe confirmed core acknowledgement")
	}
	ledger := map[string]receiptLedger{}
	envelopes := map[string]bool{}
	consumeBrokerReceipts(t, brokerURL, 1, ledger, envelopes)
	if code := (&system{coreURL: coreURL}).post(t, body); code != http.StatusAccepted {
		t.Fatalf("provider retry response = %d", code)
	}
	consumeBrokerReceipts(t, brokerURL, 1, ledger, envelopes)
	if got := ledger[event]; got != (receiptLedger{Attempts: 2, Receipts: 2, ConsumerOutcomes: 1}) || len(envelopes) != 2 {
		t.Fatalf("lost reply reconciliation event=%s ledger=%+v unique_envelopes=%d", event, got, len(envelopes))
	}
	t.Logf("provider_event=%s sender_attempts=2 observed_acks=1 core_202s=2 broker_receipts=2 consumer_outcomes=1", event)

	// Stop the broker application, not its container: mapped ports and retained
	// durable queues stay unchanged. Wait for readiness to detect the disconnect
	// before posting, so this is a definite pre-write outage, not a lost confirm.
	brokerApp(t, ctx, broker, "stop_app")
	waitFor(t, "broker disconnect reflected in readiness", 10*time.Second, func() bool {
		code, _, getErr := get(coreURL + "/readyz")
		return getErr == nil && code == http.StatusServiceUnavailable
	})
	const outageEvent = "broker-outage-retry"
	outageBody := []byte(`{"event_id":"` + outageEvent + `"}`)
	if code := (&system{coreURL: coreURL}).post(t, outageBody); code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable broker acknowledged %d", code)
	}
	brokerApp(t, ctx, broker, "start_app")
	waitReady(t, "broker recovery", coreURL+"/readyz")
	if code := (&system{coreURL: coreURL}).post(t, outageBody); code != http.StatusAccepted {
		t.Fatalf("recovered broker response = %d", code)
	}
	consumeBrokerReceipts(t, brokerURL, 1, ledger, envelopes)
	if got := ledger[outageEvent]; got != (receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1}) {
		t.Fatalf("outage reconciliation event=%s ledger=%+v", outageEvent, got)
	}
	t.Logf("provider_event=%s sender_attempts=2 observed_503s=1 observed_202s=1 broker_receipts=1 consumer_outcomes=1", outageEvent)
}

func brokerApp(t *testing.T, ctx context.Context, broker *tcrabbitmq.RabbitMQContainer, action string) {
	t.Helper()
	code, out, err := broker.Exec(ctx, []string{"rabbitmqctl", action})
	if err != nil {
		t.Fatalf("rabbitmqctl %s: %v", action, err)
	}
	if code != 0 {
		body, _ := io.ReadAll(out)
		t.Fatalf("rabbitmqctl %s exit %d: %s", action, code, body)
	}
}

func consumeBrokerReceipts(t *testing.T, brokerURL string, count int, ledger map[string]receiptLedger, envelopes map[string]bool) {
	t.Helper()
	conn, err := amqp.DialConfig(brokerURL, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
		c, dialErr := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(t.Context(), network, addr)
		if dialErr == nil {
			dialErr = c.SetDeadline(time.Now().Add(10 * time.Second))
			if dialErr != nil {
				_ = c.Close()
			}
		}
		return c, dialErr
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	// Passive declaration independently checks that core created a durable queue.
	if _, err := ch.QueueDeclarePassive("orders", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	for range count {
		msg, ok, err := ch.Get("orders", false)
		if err != nil || !ok {
			t.Fatalf("independent broker receipt missing: ok=%t err=%v", ok, err)
		}
		var envelope struct {
			ID   string `json:"id"`
			Body struct {
				EventID string `json:"event_id"`
			} `json:"body"`
		}
		if err := json.Unmarshal(msg.Body, &envelope); err != nil || envelope.ID == "" || envelope.Body.EventID == "" || msg.DeliveryMode != amqp.Persistent {
			t.Fatalf("invalid persistent receipt: envelope=%+v mode=%d err=%v", envelope, msg.DeliveryMode, err)
		}
		entry := ledger[envelope.Body.EventID]
		entry.Attempts++ // observed consumer delivery, not inferred broker publish writes
		entry.Receipts++
		if entry.ConsumerOutcomes == 0 {
			entry.ConsumerOutcomes++ // synthetic dedup model, not a real business transaction
		}
		ledger[envelope.Body.EventID] = entry
		envelopes[envelope.ID] = true
		if err := msg.Ack(false); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := ch.Get("orders", false); err != nil || ok {
		t.Fatalf("unaccounted broker receipt: present=%t err=%v", ok, err)
	}
}
