package acceptance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/testenv"
	dockerclient "github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// These profiles and thresholds are fixed before the run: 40 requests, 8
// workers, one shared quota identity; 10 admissions, zero unaccounted errors,
// p99 <= 2s. They are local regression limits, not production sizing promises.
func protectionConfig(src, backend string) string {
	store := `postgres: {url: "${REPLAY_DATABASE_URL}", max_conns: 10}`
	if backend == "redis" {
		store = `redis: {url: "${PROTECTION_REDIS_URL}", pool_size: 10}`
	}
	src = "state_stores:\n  cluster:\n    namespace: acceptance\n    timeout: 1s\n    startup_timeout: 10s\n    cleanup: {interval: 1s, batch_size: 100, budget: 1s}\n    " + store + "\n" + src
	flows := `
  quota:
    nodes:
      - {id: in, type: http_trigger, path: /quota, methods: [POST]}
      - {id: gate, type: rate_limit, store: cluster, policy_id: acceptance-quota, strategy: sliding_window_counter, limit: 10, window: 1h}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: public, type: no_auth}
      - {id: env, type: envelope}
      - {id: done, type: respond, status: 202}
    edges: [in>gate, gate>body, body>public, public>env, env>done]
  protected:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /protected, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "${ORDERS_WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256, credential_id: acceptance-partner}
      - {id: quota, type: rate_limit, stage: verified, key_by: credential_id, store: cluster, policy_id: acceptance-partner, rpm: 6000, burst: 1000}
      - {id: guard, type: replay_guard, store: cluster, key: '{header.X-Event-ID}', ttl: 5m}
      - {id: env, type: envelope, body_format: json}
      - {id: shape, type: json_validate, schema: {type: object, required: [order], properties: {order: {type: string}, event_id: {type: string}}, additionalProperties: false}}
      - {id: orders, type: publish, destination: orders_api, target: created}
      - {id: audit, type: publish, destination: audit_api, target: created}
    edges: [in>body, body>auth, auth>quota, quota>guard, guard>env, env>shape, shape>orders, env>audit]
`
	// Both destinations must receive only validated data.
	flows = strings.ReplaceAll(flows, "env>audit", "shape>audit")
	return strings.Replace(src, "flows:\n", "flows:\n"+flows, 1)
}

func signedRequest(url, event string, body []byte) (int, error) {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Event-ID", event)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, err
}

func TestAcceptance_SharedProtectionAndReplay(t *testing.T) {
	binaries(t)
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var redisURL, containerID string
			if backend == "redis" {
				c, err := tcredis.Run(t.Context(), "redis:7-alpine")
				testenv.RequireContainer(t, err, "redis")
				t.Cleanup(func() { _ = c.Terminate(context.Background()) })
				containerID = c.GetContainerID()
				redisURL, err = c.ConnectionString(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			s := startSystemWithProtection(t, backend, redisURL)
			// A genuine concurrent workload through both native core processes.
			const count, concurrency = 40, 8
			statuses := make([]int, count)
			errors := make([]error, count)
			durations := make([]time.Duration, count)
			jobs := make(chan int)
			var wg sync.WaitGroup
			for range concurrency {
				wg.Go(func() {
					for i := range jobs {
						start := time.Now()
						statuses[i], errors[i] = signedRequest(s.coreURL+"/quota", "", []byte(`{}`))
						durations[i] = time.Since(start)
					}
				})
			}
			started := time.Now()
			for i := range count {
				jobs <- i
			}
			close(jobs)
			wg.Wait()
			admitted := 0
			for i, status := range statuses {
				if errors[i] != nil {
					t.Fatal(errors[i])
				}
				switch status {
				case 202:
					admitted++
				case 429:
				default:
					t.Fatalf("quota status%d", status)
				}
			}
			if admitted != 10 {
				t.Fatalf("two native replicas admitted%d, want10", admitted)
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			p99 := durations[count-1]
			if p99 > 2*time.Second {
				t.Fatalf("local p99%s exceeds2s", p99)
			}
			t.Logf("integrated quota backend=%s requests=%d concurrency=%d admitted=%d elapsed=%s p95=%s p99=%s", backend, count, concurrency, admitted, time.Since(started), durations[count*95/100], p99)
			// Invalid shape must not reach either sink, and pre-effect compensation
			// must permit the corrected delivery under the same duplicate key.
			if code, err := signedRequest(s.coreURL+"/protected", "fixed-event", []byte(`{"wrong":true}`)); err != nil || code != 400 {
				t.Fatalf("invalid shape%d %v", code, err)
			}
			if len(s.orders.counts()) != 0 || len(s.audit.counts()) != 0 {
				t.Fatal("invalid JSON reached a destination")
			}
			s.audit.failing.Store(true)
			body := []byte(`{"order":"protected-replay","event_id":"fixed-event"}`)
			const duplicateAttempts = 8
			duplicateStatuses := make([]int, duplicateAttempts)
			duplicateErrors := make([]error, duplicateAttempts)
			startDuplicates := make(chan struct{})
			for i := range duplicateAttempts {
				wg.Go(func() {
					<-startDuplicates
					url := strings.TrimSuffix(s.coreReady[i%len(s.coreReady)], "/readyz")
					duplicateStatuses[i], duplicateErrors[i] = signedRequest(url+"/protected", "fixed-event", body)
				})
			}
			close(startDuplicates)
			wg.Wait()
			accepted := 0
			for i, code := range duplicateStatuses {
				if duplicateErrors[i] != nil {
					t.Fatal(duplicateErrors[i])
				}
				if code == 202 {
					accepted++
				} else if code != 409 {
					t.Fatalf("duplicate wave status %d", code)
				}
			}
			if accepted != 1 {
				t.Fatalf("duplicate wave accepted %d, want 1", accepted)
			}
			t.Logf("duplicate reconciliation backend=%s sender_attempts=%d accepted=1 rejected=7 statuses=%v", backend, duplicateAttempts, duplicateStatuses)
			s.orders.assertLedger(t, "fixed-event", receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
			s.audit.assertLedger(t, "fixed-event", receiptLedger{Attempts: 1})
			if code, err := signedRequest(s.coreURL+"/protected", "fixed-event", body); err != nil || code != 409 {
				t.Fatalf("duplicate across replicas%d %v", code, err)
			}
			var open struct {
				Items []failureItem `json:"items"`
			}
			waitFor(t, "protected failure captured", 30*time.Second, func() bool {
				return s.api(t, http.MethodGet, "/v1/records?flow=protected&kind=failure&delivery_status=open&dismissed=false", nil, &open) == 200 && len(open.Items) == 1
			})
			failure := open.Items[0]
			s.audit.failing.Store(false)
			var retry struct {
				RunID string `json:"run_id"`
			}
			if code := s.api(t, http.MethodPost, "/v1/records/"+failure.ID+"/retries", map[string]any{}, &retry, "Idempotency-Key", "protected-retry"); code != 202 {
				t.Fatal(code)
			}
			waitFor(t, "destination-only protected retry", 30*time.Second, func() bool {
				var r struct {
					Status string `json:"status"`
				}
				s.api(t, http.MethodGet, "/v1/retries/"+retry.RunID, nil, &r)
				return r.Status == "succeeded"
			})
			for name, receipts := range map[string]map[string]int{"orders": s.orders.counts(), "audit": s.audit.counts()} {
				if len(receipts) != 1 || receipts[failure.EventID] != 1 {
					t.Fatalf("%s receipts%v", name, receipts)
				}
			}
			s.orders.assertLedger(t, "fixed-event", receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
			s.audit.assertLedger(t, "fixed-event", receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
			if code, err := signedRequest(s.coreURL+"/protected", "fixed-event", body); err != nil || code != 409 {
				t.Fatalf("replay reset guard%d %v", code, err)
			}
			// The native replay binary exposes APIs only; UI and fixture routes
			// are absent from the production executable.
			for _, path := range []string{"/", "/console", "/console/", "/console/assets/main.js", "/console/hookway.wasm", "/console/wasm_exec.js", "/test/reset"} {
				if code, _, err := get(s.replayURL + path); err != nil || code != 404 {
					t.Fatalf("unexpected native route %s: %d %v", path, code, err)
				}
			}
			// Simulate a backend outage without changing quotas or losing state. Native
			// processes must stay alive, withdraw readiness, reject protected traffic.
			docker, err := testcontainers.NewDockerClientWithOpts(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = docker.Close() }()
			if backend == "postgres" {
				containerID = replaytest.ContainerID(t)
			}
			if containerID == "" {
				t.Fatal("test backend container not found")
			}
			if _, err = docker.ContainerPause(t.Context(), containerID, dockerclient.ContainerPauseOptions{}); err != nil {
				t.Fatal(err)
			}
			paused := true
			t.Cleanup(func() {
				if paused {
					_, _ = docker.ContainerUnpause(context.Background(), containerID, dockerclient.ContainerUnpauseOptions{})
				}
			})
			for _, u := range s.coreReady {
				waitFor(t, "native readiness withdrawal", 15*time.Second, func() bool { code, _, err := get(u); return err == nil && code == 503 })
				code, _, err := get(strings.TrimSuffix(u, "/readyz") + "/healthz")
				if err != nil || code != 200 {
					t.Fatalf("liveness%d %v", code, err)
				}
			}
			if code, err := signedRequest(s.coreURL+"/protected", "outage-event", body); err != nil || code != 503 {
				t.Fatalf("outage admitted%d %v", code, err)
			}
			if _, err = docker.ContainerUnpause(t.Context(), containerID, dockerclient.ContainerUnpauseOptions{}); err != nil {
				t.Fatal(err)
			}
			paused = false
			for _, u := range s.coreReady {
				waitReady(t, "recovered native core", u)
			}
			if code, err := signedRequest(s.coreURL+"/protected", "fixed-event", body); err != nil || code != 409 {
				t.Fatalf("outage lost duplicate state%d %v", code, err)
			}
			// Explicit current-version policy maintenance through the actual CLI.
			p := s.cores[0]
			out, err := exec.CommandContext(t.Context(), p.cmd.Path, "--help").CombinedOutput() // #nosec G204 -- repository-built binary from the test fixture
			if err != nil || !bytes.Contains(out, []byte("state-store")) {
				t.Fatal("state-store CLI unavailable")
			}
			cmd := exec.CommandContext(t.Context(), p.cmd.Path, "state-store", "status", "--config", p.cmd.Args[len(p.cmd.Args)-1], "--store", "cluster") // #nosec G204 -- repository-built binary and test-owned config
			cmd.Env = p.cmd.Env
			out, err = cmd.Output()
			if err != nil || !json.Valid(out) {
				t.Fatalf("state-store status failed:%v %s", err, out)
			}
			var status struct {
				Policies []struct {
					ID     string `json:"id"`
					Actual struct {
						Fingerprint string `json:"fingerprint"`
					} `json:"actual"`
				} `json:"policies"`
			}
			if err = json.Unmarshal(out, &status); err != nil {
				t.Fatal(err)
			}
			fingerprint := ""
			for _, policy := range status.Policies {
				if policy.ID == "acceptance-quota" {
					fingerprint = policy.Actual.Fingerprint
				}
			}
			if len(fingerprint) != 64 {
				t.Fatal("missing current quota fingerprint")
			}
			desired := filepath.Join(t.TempDir(), "desired.yaml")
			original, err := os.ReadFile(p.cmd.Args[len(p.cmd.Args)-1])
			if err != nil {
				t.Fatal(err)
			}
			writeConfig(t, desired, strings.Replace(string(original), "limit: 10, window: 1h", "limit: 20, window: 1h", 1))
			update := exec.CommandContext(t.Context(), p.cmd.Path, "state-store", "policy-update", "--config", desired, "--store", "cluster", "--policy", "acceptance-quota", "--expected-fingerprint", fingerprint) // #nosec G204 -- test-owned binary/config; parsed policy fingerprint is not shell code
			update.Env = p.cmd.Env
			if out, err := update.CombinedOutput(); err != nil {
				t.Fatalf("policy update %v %s", err, out)
			}
			if code, err := signedRequest(s.coreURL+"/quota", "", []byte(`{}`)); err != nil || code != 503 {
				t.Fatalf("old config admitted after policy update %d %v", code, err)
			}
			for i, old := range s.cores {
				old.stop()
				path := old.cmd.Args[len(old.cmd.Args)-1]
				src, err := os.ReadFile(path) // #nosec G304 -- reads only the config created in this test temporary directory
				if err != nil {
					t.Fatal(err)
				}
				writeConfig(t, path, strings.Replace(string(src), "limit: 10, window: 1h", "limit: 20, window: 1h", 1))
				s.cores[i] = startProcess(t, old.name+"-policy-update", old.cmd.Path, old.cmd.Env, old.cmd.Args[1:]...)
			}
			for _, u := range s.coreReady {
				waitReady(t, "updated core", u)
			}
			admitted = 0
			for range 20 {
				code, err := signedRequest(s.coreURL+"/quota", "", []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				if code == 202 {
					admitted++
				} else if code != 429 {
					t.Fatal(code)
				}
			}
			if admitted != 10 {
				t.Fatalf("policy update reset consumed quota: admitted%d, want10", admitted)
			}
			if code, err := signedRequest(s.coreURL+"/protected", "fixed-event", body); err != nil || code != 409 {
				t.Fatalf("core replacement lost guard%d %v", code, err)
			}

		})
	}
}
