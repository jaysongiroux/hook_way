package composee2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The stack reads these from the environment of `docker compose`, so the test
// also proves the compose file passes them through.
const (
	webhookSecret = "e2e-github-webhook-secret"              //nolint:gosec // G101: test fixture
	captureToken  = "e2e-capture-token-0123456789abcdefghij" //nolint:gosec // G101: test fixture
	controlToken  = "e2e-control-token-0123456789abcdefghij" //nolint:gosec // G101: test fixture
	operatorToken = "e2e-operator-token-0123456789abcdefghi" //nolint:gosec // G101: test fixture
	metricsToken  = "e2e-metrics-token-0123456789abcdefghij" //nolint:gosec // G101: test fixture
)

// client bounds every call, so a hung service fails the test (and its cleanup
// tears the stack down) instead of running into the suite timeout.
var client = &http.Client{Timeout: 30 * time.Second}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// command runs one command to completion and returns its combined output.
func command(dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed go and docker invocations
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRun(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	out, err := command(dir, env, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// dockerArch is the architecture the images must be built for.
func dockerArch(t *testing.T) string {
	t.Helper()
	out, err := command("", nil, "docker", "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}")
	if err != nil {
		t.Fatalf("docker is not available: %v\n%s", err, out)
	}
	osName, arch, _ := strings.Cut(strings.TrimSpace(out), "/")
	if osName != "linux" {
		t.Fatalf("the Docker daemon runs %s containers; the stack needs linux", osName)
	}
	return arch
}

// buildImage compiles one command for linux on the host and packages it with
// the matching stage of testdata/Dockerfile.
func buildImage(t *testing.T, root, dir, arch, pkg, binary, target, tag string) {
	t.Helper()
	ctxDir := filepath.Join(dir, target)
	flags := []string{"build", "-trimpath", "-o", filepath.Join(ctxDir, binary)}
	if pkg != "./internal/composee2e/testdata/sink" {
		flags = append(flags, "-ldflags", "-X main.version=e2e -X main.commit=e2e")
	}
	mustRun(t, root, []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + arch}, "go", append(flags, pkg)...)
	mustRun(t, root, nil, "docker", "build", "--file", filepath.Join(root, "internal", "composee2e", "testdata", "Dockerfile"),
		"--target", target, "--tag", tag, ctxDir)
}

// override points the stack at the test's images and publishes every port it
// talks to on a random loopback port, so the run cannot collide with a
// `make dev-up` already on 8080.
func override(images map[string]string) string {
	var b strings.Builder
	b.WriteString("services:\n")
	ports := map[string]string{"rabbitmq": "15672", "hookway": "8080", "replay": "8080", "notify-sink": "8080"}
	for _, svc := range []string{"rabbitmq", "hookway", "notify-sink", "postgres", "migrate", "replay"} {
		image, port := images[svc], ports[svc]
		if image == "" && port == "" {
			continue
		}
		fmt.Fprintf(&b, "  %s:\n", svc)
		if image != "" {
			fmt.Fprintf(&b, "    image: %s\n    pull_policy: never\n", image)
		}
		if port != "" {
			fmt.Fprintf(&b, "    ports: !override\n      - \"127.0.0.1::%s\"\n", port)
		}
	}
	return b.String()
}

type stack struct {
	root string
	args []string // docker compose -p … -f … -f …
	env  []string
}

func (s *stack) run(args ...string) (string, error) {
	return command(s.root, s.env, "docker", append(slices.Clone(s.args), args...)...)
}

func (s *stack) compose(t *testing.T, args ...string) string {
	t.Helper()
	out, err := s.run(args...)
	if err != nil {
		t.Fatalf("docker compose %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// url is the loopback address a service's container port is published on.
// It is looked up on every call: a restarted container gets a new host port.
func (s *stack) url(t *testing.T, service string, port int) string {
	t.Helper()
	out := strings.TrimSpace(s.compose(t, "port", service, strconv.Itoa(port)))
	addr, _, _ := strings.Cut(out, "\n")
	return "http://" + addr
}

// up deploys examples/docker-compose.yaml and waits until core, the replay
// service, and the sink answer. Cleanup tears everything down, volumes and
// images included, and prints the stack's logs when the test failed.
func up(t *testing.T) *stack {
	t.Helper()
	if os.Getenv("HOOKWAY_COMPOSE_E2E") != "1" {
		t.Skip("set HOOKWAY_COMPOSE_E2E=1 (make compose-e2e-test) to deploy the dev stack in Docker")
	}
	mode := os.Getenv("HOOKWAY_COMPOSE_E2E_BUILD")
	if mode == "" {
		mode = "binaries"
	}
	if mode != "binaries" && mode != "dockerfile" {
		t.Fatalf("HOOKWAY_COMPOSE_E2E_BUILD=%q; want binaries or dockerfile", mode)
	}
	root := repoRoot(t)
	arch := dockerArch(t)
	dir := t.TempDir()
	id := strconv.FormatInt(time.Now().UnixNano(), 36)

	images := map[string]string{"notify-sink": "hookway-e2e-sink:" + id}
	buildImage(t, root, dir, arch, "./internal/composee2e/testdata/sink", "sink", "sink", images["notify-sink"])
	if mode == "binaries" {
		images["hookway"] = "hookway-e2e-core:" + id
		images["replay"] = "hookway-e2e-replay:" + id
		images["migrate"] = images["replay"]
		buildImage(t, root, dir, arch, "./cmd/hookway", "hookway", "core", images["hookway"])
		buildImage(t, root, dir, arch, "./cmd/hookway-replay", "hookway-replay", "replay", images["replay"])
	}
	overridePath := filepath.Join(dir, "override.yaml")
	if err := os.WriteFile(overridePath, []byte(override(images)), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &stack{
		root: root,
		args: []string{"compose", "--project-name", "hookway-e2e-" + id,
			"--file", filepath.Join(root, "examples", "docker-compose.yaml"), "--file", overridePath},
		env: []string{
			"GITHUB_WEBHOOK_SECRET=" + webhookSecret,
			// A developer's shell may point RABBITMQ_URL at localhost for `make run`.
			"RABBITMQ_URL=amqp://guest:guest@rabbitmq:5672/",
			"REPLAY_CAPTURE_TOKEN=" + captureToken,
			"REPLAY_CONTROL_TOKEN=" + controlToken,
			"REPLAY_OPERATOR_TOKEN=" + operatorToken,
			"REPLAY_METRICS_TOKEN=" + metricsToken,
		},
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := s.run("logs", "--no-color", "--timestamps")
			t.Logf("--- docker compose logs ---\n%s", out)
		}
		down := []string{"down", "--volumes", "--remove-orphans", "--timeout", "10"}
		if mode == "dockerfile" {
			down = append(down, "--rmi", "local")
		}
		if out, err := s.run(down...); err != nil {
			t.Errorf("docker compose down: %v\n%s", err, out)
		}
		for _, image := range []string{images["notify-sink"], images["hookway"], images["replay"]} {
			if image != "" {
				_, _ = command(root, nil, "docker", "image", "rm", "--force", image)
			}
		}
	})
	if mode == "dockerfile" {
		s.compose(t, "build")
	}
	s.compose(t, "up", "--detach", "--no-build")
	for _, probe := range []struct{ service, path string }{
		{"hookway", "/readyz"}, {"replay", "/readyz"}, {"notify-sink", "/received"},
	} {
		waitFor(t, probe.service+" to answer "+probe.path, 3*time.Minute, func() bool {
			code, _, err := do(http.MethodGet, s.url(t, probe.service, 8080)+probe.path, nil)
			return err == nil && code == http.StatusOK
		})
	}
	return s
}

func do(method, u string, body []byte, headers ...string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, u, bytes.NewReader(body))
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
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// postGitHub sends one webhook signed the way GitHub signs it and returns the
// status core answered.
func (s *stack) postGitHub(t *testing.T, body string) int {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(body))
	code, _, err := do(http.MethodPost, s.url(t, "hookway", 8080)+"/webhooks/github", []byte(body),
		"Content-Type", "application/json", "X-GitHub-Event", "issues",
		"X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// received is how many times the sink accepted each envelope id since it
// last started.
func (s *stack) received(t *testing.T) map[string]int {
	t.Helper()
	code, raw, err := do(http.MethodGet, s.url(t, "notify-sink", 8080)+"/received", nil)
	if err != nil || code != http.StatusOK {
		t.Fatalf("GET /received = %d, %v", code, err)
	}
	got := map[string]int{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// api calls the replay API as the operator and returns the status; a JSON
// response is decoded into into when it is not nil.
func (s *stack) api(t *testing.T, method, path string, body, into any, headers ...string) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	headers = append(headers, "Authorization", "Bearer "+operatorToken, "Content-Type", "application/json")
	code, resp, err := do(method, s.url(t, "replay", 8080)+path, raw, headers...)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if into != nil {
		if err := json.Unmarshal(resp, into); err != nil {
			t.Fatalf("%s %s = %d: decode: %v", method, path, code, err)
		}
	}
	return code, resp
}

// rabbitMessages takes every message off a queue through the RabbitMQ
// management API and returns their envelope ids.
func (s *stack) rabbitMessages(t *testing.T, queue string) []string {
	t.Helper()
	body := []byte(`{"count":100,"ackmode":"ack_requeue_false","encoding":"auto"}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		s.url(t, "rabbitmq", 15672)+"/api/queues/%2F/"+queue+"/get", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("guest", "guest")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var msgs []struct {
		Payload string `json:"payload"`
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("RabbitMQ get %s = %d", queue, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		var env struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(m.Payload), &env); err != nil {
			t.Fatalf("RabbitMQ message is not a JSON envelope: %v", err)
		}
		ids = append(ids, env.ID)
	}
	return ids
}

type failure struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Flow          string `json:"flow"`
	NodeID        string `json:"node_id"`
	DestinationID string `json:"destination_id"`
	EventID       string `json:"event_id"`
	ErrorClass    string `json:"error_class"`
	Retryable     bool   `json:"retryable"`
}

// The dev stack keeps a delivery that failed after the sender got its 202,
// and one retry through the replay API delivers it without re-sending to the
// destination that already had the event.
func TestDevStackRecordsAMissedDeliveryAndReplaysIt(t *testing.T) {
	s := up(t)

	// Both destinations are up: the event reaches RabbitMQ and the sink.
	if code := s.postGitHub(t, `{"action":"opened","issue":{"number":1}}`); code != http.StatusAccepted {
		t.Fatalf("first webhook answered %d, want 202", code)
	}
	var first string
	waitFor(t, "the sink to receive the first event", time.Minute, func() bool {
		got := s.received(t)
		for id := range got {
			first = id
		}
		return len(got) == 1
	})

	// The sink goes down. RabbitMQ still gets the next event, so the sender
	// still gets its 202, and it will not send the event again.
	s.compose(t, "stop", "notify-sink")
	if code := s.postGitHub(t, `{"action":"closed","issue":{"number":1}}`); code != http.StatusAccepted {
		t.Fatalf("webhook during the outage answered %d, want 202", code)
	}

	// The replay service has the missed delivery, and only that one.
	var open struct {
		Items []failure `json:"items"`
	}
	waitFor(t, "the missed delivery in the replay service", time.Minute, func() bool {
		open.Items = nil
		code, _ := s.api(t, http.MethodGet, "/v1/failures?status=open", nil, &open)
		return code == http.StatusOK && len(open.Items) > 0
	})
	if len(open.Items) != 1 {
		t.Fatalf("open records = %+v, want only the missed notify delivery", open.Items)
	}
	missed := open.Items[0]
	if missed.Flow != "github_events" || missed.NodeID != "notify" || missed.DestinationID != "notify" ||
		!missed.Retryable || missed.ErrorClass == "" || missed.EventID == "" || missed.EventID == first {
		t.Fatalf("record = %+v (first event %s)", missed, first)
	}
	var detail struct {
		InboundStatus int `json:"inbound_status"`
		Artifacts     struct {
			Stored bool `json:"stored"`
		} `json:"artifacts"`
	}
	s.api(t, http.MethodGet, "/v1/failures/"+missed.ID, nil, &detail)
	if detail.InboundStatus != http.StatusAccepted || !detail.Artifacts.Stored {
		t.Fatalf("record detail = %+v; want the 202 the sender got and a stored payload", detail)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if code, _ := s.api(t, http.MethodGet, "/v1/failures/"+missed.ID+"/payload", nil, &payload); code != http.StatusOK || payload.ID != missed.EventID {
		t.Fatalf("payload = %d %+v; want the envelope of event %s", code, payload, missed.EventID)
	}

	// The sink is back; one retry delivers the missed event.
	s.compose(t, "start", "notify-sink")
	waitFor(t, "the sink to come back", time.Minute, func() bool {
		code, _, err := do(http.MethodGet, s.url(t, "notify-sink", 8080)+"/received", nil)
		return err == nil && code == http.StatusOK
	})
	var run struct {
		RunID string `json:"run_id"`
	}
	if code, raw := s.api(t, http.MethodPost, "/v1/failures/"+missed.ID+"/retries", map[string]any{}, &run,
		"Idempotency-Key", "e2e-retry-1"); code != http.StatusAccepted || run.RunID == "" {
		t.Fatalf("retry answered %d %s", code, raw)
	}
	waitFor(t, "run "+run.RunID+" to succeed", 2*time.Minute, func() bool {
		var r struct {
			Status string `json:"status"`
		}
		_, raw := s.api(t, http.MethodGet, "/v1/retries/"+run.RunID, nil, &r)
		if r.Status == "failed" || r.Status == "blocked" || r.Status == "unknown" {
			t.Fatalf("run %s finished %s: %s", run.RunID, r.Status, raw)
		}
		return r.Status == "succeeded"
	})
	if got := s.received(t); len(got) != 1 || got[missed.EventID] != 1 {
		t.Fatalf("sink received %v after the retry, want event %s once", got, missed.EventID)
	}
	var after failure
	s.api(t, http.MethodGet, "/v1/failures/"+missed.ID, nil, &after)
	if after.Status != "resolved" {
		t.Fatalf("record is %s after the retry, want resolved", after.Status)
	}

	// RabbitMQ got each event once: the retry re-ran only the missed delivery.
	got := s.rabbitMessages(t, "github-webhooks")
	want := []string{first, missed.EventID}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("RabbitMQ has events %v, want %v once each", got, want)
	}
}

// runtimeOf returns, per named stage, the instructions that shape a running
// container: its user, binaries, healthcheck, entrypoint, and command.
func runtimeOf(t *testing.T, path string) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string][]string{}
	stage := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "FROM":
			stage = ""
			if len(f) == 4 && strings.EqualFold(f[2], "AS") {
				stage = f[3]
			}
		case "USER", "HEALTHCHECK", "ENTRYPOINT", "CMD":
			stages[stage] = append(stages[stage], strings.Join(f, " "))
		case "COPY":
			if dst := f[len(f)-1]; strings.HasPrefix(dst, "/usr/local/bin/") {
				stages[stage] = append(stages[stage], "COPY → "+dst)
			}
		}
	}
	return stages
}

// The e2e images stand in for the release images, so a container started
// from either must behave the same way.
func TestE2EImagesMatchTheReleaseImages(t *testing.T) {
	release := runtimeOf(t, filepath.Join(repoRoot(t), "Dockerfile"))
	e2e := runtimeOf(t, filepath.Join("testdata", "Dockerfile"))
	for _, stage := range []string{"core", "replay"} {
		if len(release[stage]) == 0 {
			t.Fatalf("the release Dockerfile has no %s stage", stage)
		}
		if got, want := strings.Join(e2e[stage], "\n"), strings.Join(release[stage], "\n"); got != want {
			t.Errorf("testdata/Dockerfile %s stage drifted from the release image:\n%s\nwant:\n%s", stage, got, want)
		}
	}
}
