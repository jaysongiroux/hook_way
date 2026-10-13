package acceptance

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcceptance_NativeDiagnosticStormRemainsSampled(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs native core")
	}
	coreBin, _ := binaries(t)
	var failing atomic.Bool
	failing.Store(true)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(sink.Close)
	addr, metricsAddr := freeAddr(t), freeAddr(t)
	path := filepath.Join(t.TempDir(), "telemetry.yaml")
	writeConfig(t, path, fmt.Sprintf(`
server: {addr: %q, max_body_bytes: 1048576}
metrics: {enabled: true, addr: %q, path: /metrics}
destinations:
  events: {type: webhook, webhook: {url: %q, delivery_mode: sync, retry: {max_attempts: 1}}}
flows:
  events:
    nodes:
      - {id: in, type: http_trigger, path: '/events/{customer}', methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, signature_header: X-Hub-Signature-256, secret: "${ORDERS_WEBHOOK_SECRET}"}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: events, target: '{customer}'}
    edges: [in>body, body>auth, auth>env, env>out]
`, addr, metricsAddr, sink.URL))
	p := startProcess(t, "telemetry-storm-core", coreBin, []string{"ORDERS_WEBHOOK_SECRET=" + webhookSecret, "HOOKWAY_LOG_LEVEL=info"}, "serve", "--config", path)
	base := "http://" + addr
	waitReady(t, "telemetry core", base+"/readyz")
	start := time.Now()
	const offered = 1000
	for i := range offered {
		body := []byte(fmt.Sprintf(`{"id":"event-marker-%d","payload":"PAYLOADSECRET"}`, i))
		mac := hmac.New(sha256.New, []byte(webhookSecret))
		_, _ = mac.Write(body)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fmt.Sprintf("%s/events/customer-marker-%d?token=QUERYSECRET", base, i), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("Authorization", "HEADERSECRET")
		req.Header.Set("X-Request-ID", fmt.Sprintf("request-marker-%d", i))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("storm request%d status%d", i, resp.StatusCode)
		}
	}
	elapsed := time.Since(start)
	_, scrape, err := get("http://" + metricsAddr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metricsText := string(scrape)
	if !strings.Contains(metricsText, `hookway_http_requests_total{destination_queue_id="",endpoint="events",queue="",status="503"} 1000`) || !strings.Contains(metricsText, "hookway_logs_suppressed_total ") {
		t.Fatal("full failure outcomes or suppression metric unavailable")
	}
	for _, marker := range []string{"event-marker-", "customer-marker-", "request-marker-", "SECRET"} {
		if strings.Contains(metricsText, marker) {
			t.Fatalf("untrusted value became metric label: %s", marker)
		}
	}
	logs := p.out.String()
	for _, marker := range []string{"PAYLOADSECRET", "QUERYSECRET", "HEADERSECRET", webhookSecret} {
		if strings.Contains(logs, marker) {
			t.Fatal("synthetic secret or payload marker leaked")
		}
	}
	counts := map[string]int{}
	decoder := json.NewDecoder(strings.NewReader(logs))
	for decoder.More() {
		var entry struct {
			Message string `json:"msg"`
			Path    string `json:"path"`
		}
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		counts[entry.Message]++
		if entry.Message == "request handled" && entry.Path != "/events/{customer}" {
			t.Fatal("raw caller path leaked")
		}
	}
	windowBound := 10 * (int(elapsed/time.Second) + 2)
	for _, category := range []string{"request handled", "flow completed", "node failed", "webhook delivery failed (attempts exhausted)"} {
		if counts[category] == 0 || counts[category] > windowBound {
			t.Fatalf("category%s records%d bound%d", category, counts[category], windowBound)
		}
	}
	if counts["repeated runtime logs suppressed"] == 0 || !strings.Contains(logs, "request-marker-0") {
		t.Fatal("missing suppression summary or first request correlation")
	}
	failing.Store(false)
	body := []byte(`{"recovered":true}`)
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(body)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/events/recovered", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("recovered status%d", resp.StatusCode)
	}
	p.stop()
	if !strings.Contains(p.out.String(), "hookway stopped") {
		t.Fatal("sampling hid lifecycle completion")
	}
	t.Logf("native offered=%d failures=1000 recovery=202 elapsed=%s total_log_bytes=%d category_bound=%d records=%v; monitoring stack absent, metrics available", offered, elapsed, len(logs), windowBound, counts)
}
