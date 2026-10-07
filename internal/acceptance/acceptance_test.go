package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

const (
	captureToken  = "capture-token-0123456789abcdefghijkl"
	controlToken  = "control-token-0123456789abcdefghij"
	operatorToken = "operator-token-0123456789abcdefghi"
	webhookSecret = "orders-webhook-secret" //nolint:gosec // G101: test fixture
)

// client bounds every call, so a hung process fails the test (and its
// cleanups stop the children) instead of running into the suite timeout.
var client = &http.Client{Timeout: 30 * time.Second}

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := replaytest.Main(m)
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// binaries builds hookway and hookway-replay once per test binary.
func binaries(t *testing.T) (core, replay string) {
	t.Helper()
	if os.Getenv("HOOKWAY_ACCEPTANCE") != "1" {
		t.Skip("set HOOKWAY_ACCEPTANCE=1 (make replay-acceptance-test) to run the end-to-end suite")
	}
	if supplied := os.Getenv("HOOKWAY_ACCEPTANCE_BIN_DIR"); supplied != "" {
		for _, name := range []string{"hookway", "hookway-replay"} {
			info, err := os.Stat(filepath.Join(supplied, name)) // #nosec G703 -- trusted test operator explicitly selects prebuilt binaries; names are fixed, with no request input
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("supplied acceptance binary %s must be an executable regular file: %v", name, err)
			}
		}
		return filepath.Join(supplied, "hookway"), filepath.Join(supplied, "hookway-replay")
	}
	root := repoRoot(t)
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "hookway-acceptance-")
		if buildErr != nil {
			return
		}
		for _, name := range []string{"hookway", "hookway-replay"} {
			cmd := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(binDir, name), "./cmd/"+name) // #nosec G204 -- builds this repository's own binaries
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("go build ./cmd/%s: %w\n%s", name, err, out)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(binDir, "hookway"), filepath.Join(binDir, "hookway-replay")
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

// output is a goroutine-safe copy of a child process's output.
type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

type process struct {
	name string
	cmd  *exec.Cmd
	out  *output
	once sync.Once
	done chan struct{}
}

func startProcess(t *testing.T, name, bin string, env []string, args ...string) *process {
	t.Helper()
	p := &process{name: name, out: &output{}, done: make(chan struct{})}
	p.cmd = exec.CommandContext(context.Background(), bin, args...) // #nosec G204 -- runs this repository's own binaries
	p.cmd.Env = append(os.Environ(), env...)
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	go func() {
		_ = p.cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			t.Logf("--- %s output ---\n%s", name, p.out.String())
		}
	})
	return p
}

// stop sends SIGINT — a graceful drain — and kills the process after 20 s.
func (p *process) stop() {
	p.once.Do(func() {
		_ = p.cmd.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(20 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
}

func get(u string, headers ...string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitReady(t *testing.T, what, u string) {
	t.Helper()
	waitFor(t, what+" at "+u, 30*time.Second, func() bool {
		code, _, err := get(u)
		return err == nil && code == http.StatusOK
	})
}

// roundRobin stands in for Compose's service DNS: one address that spreads
// requests across replicas.
func roundRobin(t *testing.T, backends ...string) string {
	t.Helper()
	targets := make([]*url.URL, len(backends))
	for i, b := range backends {
		u, err := url.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		targets[i] = u
	}
	var n atomic.Uint64
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(targets[n.Add(1)%uint64(len(targets))])
	}, ErrorLog: log.New(io.Discard, "", 0)} // a stopped backend is expected here
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)
	return srv.URL
}

// sink is a destination that counts every envelope id it accepted.
type sink struct {
	failing  atomic.Bool
	mu       sync.Mutex
	ids      map[string]int
	ledger   map[string]receiptLedger
	accepted func(context.Context)
	srv      *httptest.Server
}

func newSink(t *testing.T) *sink {
	t.Helper()
	s := &sink{ids: map[string]int{}, ledger: map[string]receiptLedger{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			ID   string `json:"id"`
			Body struct {
				EventID string `json:"event_id"`
			} `json:"body"`
		}
		_ = json.Unmarshal(body, &env)
		s.mu.Lock()
		entry := s.ledger[env.Body.EventID]
		entry.Attempts++
		if s.failing.Load() {
			s.ledger[env.Body.EventID] = entry
			s.mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		entry.Receipts++
		// The synthetic consumer atomically deduplicates its business outcome by
		// provider identity, not by Hookway's per-inbound envelope ID.
		if entry.ConsumerOutcomes == 0 {
			entry.ConsumerOutcomes++
		}
		s.ledger[env.Body.EventID] = entry
		s.ids[env.ID]++
		accepted := s.accepted
		s.mu.Unlock()
		if accepted != nil {
			accepted(r.Context())
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) counts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.ids))
	for k, v := range s.ids {
		out[k] = v
	}
	return out
}

const coreYAML = `
server: {addr: "%s", max_body_bytes: 16777216}
metrics: {enabled: true, path: /metrics, addr: "%s", runtime_metrics: true}
replay:
  url: "%s"
  capture_token: "${REPLAY_CAPTURE_TOKEN}"
  control: {addr: "%s", token: "${REPLAY_CONTROL_TOKEN}"}
destinations:
  orders_api: {type: webhook, webhook: {url: "%s", delivery_mode: sync, retry: {max_attempts: 1}}}
  audit_api: {type: webhook, webhook: {url: "%s", delivery_mode: sync, retry: {max_attempts: 1}}}
flows:
  orders:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 16777216}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "${ORDERS_WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope}
      - {id: orders, type: publish, destination: orders_api, target: created}
      - {id: audit, type: publish, destination: audit_api, target: created}
    edges: [in>body, body>auth, auth>env, env>orders, env>audit]
`

const replayYAML = `
server: {addr: "%s"}
metrics: {enabled: true, path: /metrics, addr: "%s"}
database: {url: "${REPLAY_DATABASE_URL}", max_conns: 10}
core: {control_url: "%s", control_token: "${REPLAY_CONTROL_TOKEN}"}
retention: {ttl: 1h, max_records: 10000, max_bytes: 1073741824, max_record_bytes: 16777216, gc_interval: 15m}
workers: {concurrency: 4, execution_timeout: 30s, lease: 60s}
keys:
  - {name: core, token: "${REPLAY_CAPTURE_TOKEN}", scopes: [capture]}
  - {name: operator, token: "${REPLAY_OPERATOR_TOKEN}", scopes: [read, payload, retry, metrics]}
`

type system struct {
	coreURL, replayURL string
	coreReady          []string
	coreMetrics        []string
	cores, replays     []*process
	orders, audit      *sink
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { // #nosec G703 -- callers provide test-owned temporary config paths, never request input
		t.Fatal(err)
	}
}

// startSystem runs two replay replicas (after one migration) and two core
// replicas, wired to each other through round-robin proxies.
func startSystem(t *testing.T) *system {
	return startSystemWithProtection(t, "", "")
}

func startSystemWithProtection(t *testing.T, backend, redisURL string) *system {
	t.Helper()
	coreBin, replayBin := binaries(t)
	env := []string{
		"REPLAY_CAPTURE_TOKEN=" + captureToken, "REPLAY_CONTROL_TOKEN=" + controlToken,
		"REPLAY_OPERATOR_TOKEN=" + operatorToken, "ORDERS_WEBHOOK_SECRET=" + webhookSecret,
		"REPLAY_DATABASE_URL=" + replaytest.Database(t),
	}
	if backend == "redis" {
		env = append(env, "PROTECTION_REDIS_URL="+redisURL)
	}
	dir := t.TempDir()
	s := &system{orders: newSink(t), audit: newSink(t)}
	type coreAddrs struct{ http, metrics, control string }
	type replayAddrs struct{ api, metrics string }
	cores := []coreAddrs{{freeAddr(t), freeAddr(t), freeAddr(t)}, {freeAddr(t), freeAddr(t), freeAddr(t)}}
	replays := []replayAddrs{{freeAddr(t), freeAddr(t)}, {freeAddr(t), freeAddr(t)}}
	s.replayURL = roundRobin(t, "http://"+replays[0].api, "http://"+replays[1].api)
	controlURL := roundRobin(t, "http://"+cores[0].control, "http://"+cores[1].control)
	s.coreURL = roundRobin(t, "http://"+cores[0].http, "http://"+cores[1].http)

	for i, ra := range replays {
		path := filepath.Join(dir, fmt.Sprintf("replay-%d.yaml", i))
		replaySource := fmt.Sprintf(replayYAML, ra.api, ra.metrics, controlURL)
		writeConfig(t, path, replaySource)
		if i == 0 {
			cmd := exec.CommandContext(context.Background(), replayBin, "migrate", "--config", path) // #nosec G204 -- this repository's binary
			cmd.Env = append(os.Environ(), env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("migrate: %v\n%s", err, out)
			}
		}
		s.replays = append(s.replays, startProcess(t, fmt.Sprintf("replay-%d", i), replayBin, env, "serve", "--config", path))
	}
	for i, ca := range cores {
		path := filepath.Join(dir, fmt.Sprintf("core-%d.yaml", i))
		coreSource := fmt.Sprintf(coreYAML, ca.http, ca.metrics, s.replayURL, ca.control, s.orders.srv.URL, s.audit.srv.URL)
		if backend != "" {
			coreSource = protectionConfig(coreSource, backend)
		}
		writeConfig(t, path, coreSource)
		if backend == "postgres" && i == 0 {
			cmd := exec.CommandContext(t.Context(), coreBin, "state-store", "migrate", "--config", path, "--store", "cluster") // #nosec G204 -- repository-built binary and test-owned config
			cmd.Env = append(os.Environ(), env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("protection migrate: %v\n%s", err, out)
			}
		}
		s.cores = append(s.cores, startProcess(t, fmt.Sprintf("core-%d", i), coreBin, env, "--config", path))
		s.coreReady = append(s.coreReady, "http://"+ca.http+"/readyz")
		s.coreMetrics = append(s.coreMetrics, "http://"+ca.metrics+"/metrics")
	}
	for _, ra := range replays {
		waitReady(t, "replay", "http://"+ra.api+"/readyz")
	}
	for _, u := range s.coreReady {
		waitReady(t, "core", u)
	}
	if backend == "" {
		if code, _, err := get(s.replayURL + "/console/"); err != nil || code != http.StatusNotFound {
			t.Fatalf("disabled console route: %d %v", code, err)
		}
	}
	return s
}

// post sends one signed webhook through the core proxy and returns its status.
func (s *system) post(t *testing.T, body []byte) int {
	t.Helper()
	status, err := s.postResult(body)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func (s *system) postResult(body []byte) (int, error) {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.coreURL+"/webhooks/orders", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_, readErr := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, readErr
}

// api calls the replay API as the operator.
func (s *system) api(t *testing.T, method, path string, body, into any, headers ...string) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.replayURL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// metric sums a series' samples on a core replica's /metrics; name may
// include a full label set. A series that is not exported fails the test, so
// an assertion on it can never pass vacuously.
func metric(t *testing.T, u, name string) float64 {
	t.Helper()
	code, body, err := get(u)
	if err != nil || code != http.StatusOK {
		t.Fatalf("GET %s = %d, %v", u, code, err)
	}
	var sum float64
	found := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, name+" ") && !strings.HasPrefix(line, name+"{") {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			sum += v
			found = true
		}
	}
	if !found {
		t.Fatalf("%s exports no %s series", u, name)
	}
	return sum
}

type failureItem struct {
	ID             string `json:"id"`
	EventID        string `json:"event_id"`
	NodeID         string `json:"node_id"`
	DeliveryStatus string `json:"delivery_status"`
}

// Spec acceptance: an outage under a partial skip_failed failure is
// captured; after the fix, one batch retry delivers each record exactly once
// and never reruns the sibling that had succeeded.
func TestAcceptance_OutageThenBatchRetryDeliversOnce(t *testing.T) {
	s := startSystem(t)
	s.audit.failing.Store(true)
	const n = 20
	for i := range n {
		if code := s.post(t, []byte(fmt.Sprintf(`{"order":%d,"event_id":"fanout-%d"}`, i, i))); code != http.StatusAccepted {
			t.Fatalf("event %d answered %d", i, code)
		}
	}
	var open struct {
		Items []failureItem `json:"items"`
	}
	waitFor(t, "every failed audit publish captured", 30*time.Second, func() bool {
		open.Items = nil
		return s.api(t, http.MethodGet, "/v1/records?kind=failure&delivery_status=open&dismissed=false&node_id=audit&limit=500", nil, &open) == http.StatusOK &&
			len(open.Items) == n
	})
	if orders := s.orders.counts(); len(orders) != n {
		t.Fatalf("orders sink saw %d events, want %d", len(orders), n)
	}

	s.audit.failing.Store(false) // the destination is fixed
	ids := make([]string, 0, n)
	events := map[string]bool{}
	for _, it := range open.Items {
		ids = append(ids, it.ID)
		events[it.EventID] = true
	}
	var batch struct {
		Results []struct {
			RecordID string `json:"record_id"`
			Status   int    `json:"status"`
			RunID    string `json:"run_id"`
			Code     string `json:"code"`
		} `json:"results"`
	}
	if code := s.api(t, http.MethodPost, "/v1/retries", map[string]any{"record_ids": ids}, &batch, "Idempotency-Key", "acceptance-batch-1"); code != http.StatusOK {
		t.Fatalf("batch retry answered %d", code)
	}
	for _, r := range batch.Results {
		if r.Status != http.StatusAccepted || r.RunID == "" {
			t.Fatalf("batch item %+v", r)
		}
		runID := r.RunID
		waitFor(t, "run "+runID+" to succeed", 60*time.Second, func() bool {
			var run struct {
				Status string `json:"status"`
			}
			s.api(t, http.MethodGet, "/v1/retries/"+runID, nil, &run)
			if run.Status == "failed" || run.Status == "unknown" || run.Status == "blocked" {
				t.Fatalf("run %s finished %s", runID, run.Status)
			}
			return run.Status == "succeeded"
		})
	}
	audit := s.audit.counts()
	if len(audit) != n {
		t.Fatalf("audit sink saw %d events after the retry, want %d", len(audit), n)
	}
	for id, count := range audit {
		if !events[id] || count != 1 {
			t.Errorf("audit sink got event %s %d times (captured: %v)", id, count, events[id])
		}
	}
	for id, count := range s.orders.counts() {
		if count != 1 {
			t.Errorf("orders sink got event %s %d times; the retry must not rerun the sibling that succeeded", id, count)
		}
	}
	var resolved struct {
		Items []failureItem `json:"items"`
	}
	s.api(t, http.MethodGet, "/v1/records?kind=failure&delivery_status=resolved&dismissed=false&limit=500", nil, &resolved)
	if len(resolved.Items) != n {
		t.Fatalf("%d records resolved, want %d", len(resolved.Items), n)
	}
	for i := range n {
		event := fmt.Sprintf("fanout-%d", i)
		s.orders.assertLedger(t, event, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
		s.audit.assertLedger(t, event, receiptLedger{Attempts: 2, Receipts: 1, ConsumerOutcomes: 1})
	}
}

// Spec acceptance: with the replay service down, core keeps answering
// promptly, stays ready, and keeps its capture memory bounded.
func TestAcceptance_ReplayDownLeavesCoreBoundedAndReady(t *testing.T) {
	s := startSystem(t)
	for _, p := range s.replays {
		p.stop()
	}
	// Hold capture transport while the actual replay services are stopped. A
	// fast reverse-proxy 502 otherwise releases each upload after retries, making
	// serial offered volume unrelated to simultaneous buffer occupancy.
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseUploads := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseUploads() // also releases before child-process test cleanups
	entered := make(chan struct{}, 8)
	target, err := url.Parse(s.replayURL)
	if err != nil {
		t.Fatal(err)
	}
	forward := httputil.NewSingleHostReverseProxy(target)
	forward.ErrorLog = log.New(io.Discard, "", 0)
	transport := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			forward.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(transport.Close)
	for i, old := range s.cores {
		old.stop()
		path := old.cmd.Args[len(old.cmd.Args)-1]
		original, err := os.ReadFile(path) // #nosec G304 -- fixture-owned configuration
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(original), fmt.Sprintf("url: %q", s.replayURL), fmt.Sprintf("url: %q", transport.URL), 1)
		if updated == string(original) {
			t.Fatal("fixture did not retarget capture transport")
		}
		writeConfig(t, path, updated)
		s.cores[i] = startProcess(t, old.name+"-capture-outage", old.cmd.Path, old.cmd.Env, old.cmd.Args[1:]...)
		waitReady(t, "core with held capture transport", s.coreReady[i])
	}
	t.Cleanup(releaseUploads) // release before stopping the replacement cores
	s.audit.failing.Store(true)
	// Each core receives 100 failures. A queued 512 KiB body is charged at
	// least 3*512 KiB+64 KiB, so each bounded wave offers >150 MiB against that
	// core's 64 MiB buffer. Eight ingress workers plus the transport barrier
	// retain simultaneous captures instead of relying on a slow serial workload.
	const count, concurrency = 200, 8
	pad := strings.Repeat("a", 512<<10)
	type observation struct {
		index    int
		code     int
		err      error
		duration time.Duration
	}
	post := func(i int) observation {
		body := []byte(fmt.Sprintf(`{"event_id":"replay-down-%d","pad":"%s"}`, i, pad))
		start := time.Now()
		core := strings.TrimSuffix(s.coreReady[i%len(s.cores)], "/readyz")
		code, err := signedRequest(core+"/webhooks/orders", fmt.Sprintf("replay-down-%d", i), body)
		return observation{index: i, code: code, err: err, duration: time.Since(start)}
	}
	var slowest time.Duration
	// One upload from each distinct core must actually enter the fault transport
	// before admitting the wave; readiness/buffer assertions cannot pass vacuously.
	for i := range len(s.cores) {
		result := post(i)
		if result.err != nil || result.code != http.StatusAccepted {
			t.Fatalf("event %d answered %d: %v", i, result.code, result.err)
		}
		slowest = max(slowest, result.duration)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("capture never entered the held outage transport")
		}
		if metric(t, s.coreMetrics[i], "hookway_capture_buffer_bytes") <= 0 {
			t.Fatalf("core %d did not retain its blocked upload", i)
		}
	}
	jobs := make(chan int, count)
	results := make(chan observation, count)
	for range concurrency {
		go func() {
			for i := range jobs {
				results <- post(i)
			}
		}()
	}
	for i := len(s.cores); i < count; i++ {
		jobs <- i
	}
	close(jobs)
	for received := len(s.cores); received < count; received++ {
		result := <-results
		if result.err != nil || result.code != http.StatusAccepted {
			t.Fatalf("event %d answered %d with replay down: %v", result.index, result.code, result.err)
		}
		slowest = max(slowest, result.duration)
		if received%25 == 0 {
			for j, u := range s.coreReady {
				if code, _, err := get(u); err != nil || code != http.StatusOK {
					t.Fatalf("core %d readiness = %d, %v with replay down", j, code, err)
				}
				if buf := metric(t, s.coreMetrics[j], "hookway_capture_buffer_bytes"); buf > 64<<20 {
					t.Fatalf("core %d buffers %v bytes, over the 64 MiB bound", j, buf)
				}
			}
		}
	}
	if slowest > 3*time.Second {
		t.Fatalf("the slowest request took %v; capture must never hold a response", slowest)
	}
	var full float64
	for j, u := range s.coreMetrics {
		full += metric(t, u, `hookway_capture_dropped_total{reason="buffer_full"}`)
		// The process collector exports resident memory only on Linux; the
		// buffer gauge above is the bound everywhere.
		if runtime.GOOS == "linux" {
			if rss := metric(t, u, "process_resident_memory_bytes"); rss > 512<<20 {
				t.Errorf("core %d resident memory %v bytes", j, rss)
			}
		}
	}
	if full == 0 {
		t.Fatal("the capture buffer never filled; the test did not exercise the bound")
	}
	for i := range 200 {
		event := fmt.Sprintf("replay-down-%d", i)
		s.orders.assertLedger(t, event, receiptLedger{Attempts: 1, Receipts: 1, ConsumerOutcomes: 1})
		s.audit.assertLedger(t, event, receiptLedger{Attempts: 1})
	}
	t.Log("replay outage reconciliation sender_attempts=200 HTTP202=200 good_destination_receipts=200 failed_destination_receipts=0 capture_buffer_full_drops>0; capture unavailable without changing ingress contract")
}
