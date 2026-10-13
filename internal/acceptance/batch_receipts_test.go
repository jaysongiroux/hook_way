package acceptance

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Ingress batch children are not operator batch-redrive runs. Their parent
// response reports child outcomes; repeating a parent can repeat good children.
// Keep receipt evidence outside the native core, grouped by stable child IDs.
func TestAcceptance_IngressBatchChildFailureReceipts(t *testing.T) {
	coreBin, _ := binaries(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"shared", "dedicated"} {
		t.Run(mode, func(t *testing.T) {
			var recovered atomic.Bool
			var mu sync.Mutex
			ledger := map[string]receiptLedger{}
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost { // The configured destination readiness probe.
					w.WriteHeader(http.StatusOK)
					return
				}
				body, readErr := io.ReadAll(r.Body)
				var child struct {
					ID   string `json:"id"`
					Body struct {
						EventID string `json:"event_id"`
					} `json:"body"`
				}
				if readErr != nil || json.Unmarshal(body, &child) != nil || child.Body.EventID == "" || child.ID != child.Body.EventID {
					t.Errorf("destination did not receive an isolated child: body=%q err=%v", body, readErr)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				entry := ledger[child.Body.EventID]
				entry.Attempts++
				failed := child.Body.EventID == "batch-bad" && !recovered.Load()
				if !failed {
					entry.Receipts++
					// A synthetic mutex-protected idempotent consumer model, not a
					// test of a production consumer's durable transaction.
					if entry.ConsumerOutcomes == 0 {
						entry.ConsumerOutcomes++
					}
				}
				ledger[child.Body.EventID] = entry
				mu.Unlock()
				if failed {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(sink.Close)

			// Keep the maintained adapter example's routing, authentication,
			// responses and child schema. Only destination infrastructure and
			// SQS-only publication fields differ for this local receipt fixture.
			raw, err := os.ReadFile(filepath.Join(repoRoot(t), "examples", "adapters", "batch-"+mode+".yaml")) // #nosec G304 -- fixed repository fixture names
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			addr := freeAddr(t)
			cfg["server"].(map[string]any)["addr"] = addr
			cfg["destinations"] = map[string]any{"broker": map[string]any{"type": "webhook", "webhook": map[string]any{"url": sink.URL + "/children", "delivery_mode": "sync", "retry": map[string]any{"max_attempts": 1}}}}
			flow := cfg["flows"].(map[string]any)["sample"].(map[string]any)
			for _, rawNode := range flow["nodes"].([]any) {
				node := rawNode.(map[string]any)
				if node["type"] != "batch" {
					continue
				}
				for _, rawChild := range node["child"].(map[string]any)["nodes"].([]any) {
					child := rawChild.(map[string]any)
					delete(child, "fifo")
					delete(child, "create")
					delete(child, "wire_body")
				}
			}
			encoded, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("mode=%s fixture_config_sha256=%x", mode, sha256.Sum256(encoded))
			path := filepath.Join(t.TempDir(), "batch-"+mode+".yaml")
			writeConfig(t, path, string(encoded))
			startProcess(t, "batch-receipt-"+mode, coreBin, []string{"SAMPLE_ECDSA_PUBLIC_KEY=" + base64.StdEncoding.EncodeToString(der)}, "--config", path)
			base := "http://" + addr
			waitReady(t, "ingress batch "+mode, base+"/readyz")

			body := []byte(`[{"environment":"preview","event_id":"batch-good"},{"environment":"preview","event_id":"batch-bad"},{"environment":"preview","event_id":"batch-last"}]`)
			post := func() (int, map[string]any) {
				t.Helper()
				stamp := strconv.FormatInt(time.Now().Unix(), 10)
				hash := sha256.Sum256(append([]byte(stamp), body...))
				sig, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
				if err != nil {
					t.Fatal(err)
				}
				endpoint := "/webhooks/sample"
				if mode == "shared" {
					endpoint = "/webhooks/shared/sample"
				}
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+endpoint, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Sample-Timestamp", stamp)
				req.Header.Set("X-Sample-Signature", base64.StdEncoding.EncodeToString(sig))
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				var counts map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
					t.Fatal(err)
				}
				return resp.StatusCode, counts
			}
			assertLedger := func(bad receiptLedger, good receiptLedger) {
				t.Helper()
				mu.Lock()
				defer mu.Unlock()
				want := map[string]receiptLedger{"batch-good": good, "batch-bad": bad, "batch-last": good}
				if !reflect.DeepEqual(ledger, want) {
					t.Fatalf("mode=%s child receipts=%+v want=%+v", mode, ledger, want)
				}
			}
			status, counts := post()
			wantStatus := http.StatusOK
			wantCounts := map[string]any{"message": "Processed", "primary": float64(2), "dropped": float64(1)}
			if mode == "dedicated" {
				wantStatus = http.StatusBadGateway
				wantCounts["skipped"], wantCounts["unattempted"] = float64(0), float64(0)
			}
			if status != wantStatus || !reflect.DeepEqual(counts, wantCounts) {
				t.Fatalf("mode=%s status=%d counts=%v want_status=%d want_counts=%v", mode, status, counts, wantStatus, wantCounts)
			}
			assertLedger(receiptLedger{Attempts: 1}, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
			t.Logf("mode=%s parent_attempt=1 response=%d child_attempts=3 accepted_receipts=2 failed_attempts=1 modeled_consumer_outcomes=2", mode, status)

			recovered.Store(true)
			status, counts = post()
			wantCounts["primary"], wantCounts["dropped"] = float64(3), float64(0)
			if status != http.StatusOK || !reflect.DeepEqual(counts, wantCounts) {
				t.Fatalf("recovered parent mode=%s status=%d counts=%v want=%v", mode, status, counts, wantCounts)
			}
			assertLedger(receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1}, receiptLedger{Attempts: 2, Receipts: 2, ConsumerOutcomes: 1})
			t.Logf("mode=%s parent_attempts=2 child_attempts=6 accepted_receipts=5 failed_attempts=1 modeled_consumer_outcomes=3; good child repeats require idempotency", mode)
		})
	}
}
