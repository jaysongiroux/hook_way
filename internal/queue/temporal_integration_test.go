package queue

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// startTemporal boots the Temporal CLI dev server (single container, SQLite,
// namespace "default" pre-registered) and returns its frontend host:port. It
// self-skips when Docker is unavailable, mirroring startKafka.
func startTemporal(t *testing.T) string {
	t.Helper()
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "temporalio/temporal:1.8.3",
			Cmd:          []string{"server", "start-dev", "--ip", "0.0.0.0", "--port", "7233", "--headless", "--log-level", "warn"},
			ExposedPorts: []string{"7233/tcp"},
			WaitingFor:   wait.ForListeningPort("7233/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		testenv.RequireContainer(t, err, "temporal")
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "7233/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, port.Port())
}

// dialTemporalWithRetry dials until the dev server answers its system-info
// RPC (which a successful Dial performs), so a returned ops is known-ready.
func dialTemporalWithRetry(t *testing.T, hostPort string) *sdkTemporalOps {
	t.Helper()
	cfg := config.TemporalConfig{HostPort: hostPort, Namespace: "default", ConnectTimeout: 5 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ops, err := newTemporalOps(context.Background(), "orchestrator", cfg, slog.Default())
		if err == nil {
			return ops
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	t.Fatalf("temporal dev server never became ready: %v", lastErr)
	return nil
}

// TestTemporalIntegrationStartWorkflowRoundTrip starts workflows through the
// real SDK against a dev server and verifies, with a raw client, the workflow
// id/type/task queue and that the input decodes back to the envelope (or the
// body). A second publish of the same event must be a no-op that leaves the
// original run in place.
func TestTemporalIntegrationStartWorkflowRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	hostPort := startTemporal(t)
	ops := dialTemporalWithRetry(t, hostPort)
	t.Cleanup(func() { _ = ops.Close() })
	ctx := context.Background()

	// Use the same standalone YAML decoder and destination projection as the
	// server and exported builder configs, rather than injecting target options.
	cfg, issues := flowconfig.LoadBytes([]byte(fmt.Sprintf(`
server: {addr: ":8080", max_body_bytes: 1048576}
destinations:
  orchestrator:
    type: temporal
    temporal: {host_port: %q, namespace: default}
flows:
  orders:
    nodes:
      - {id: in, type: http_trigger, path: /orders}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - id: out
        type: publish
        destination: orchestrator
        target: order-events
        temporal:
          workflow_type: OrderWorkflow
          id_reuse_policy: reject_duplicate
          workflow_id: {source: body, path: account.id, prefix: acct-}
          execution_timeout: 1h
          run_timeout: 30m
          task_timeout: 2m
      - id: audit
        type: publish
        destination: orchestrator
        target: audit-events
        temporal: {workflow_type: AuditWorkflow, input: body}
    edges: [in>body, body>auth, auth>env, env>out, env>audit]
`, hostPort)), "temporal integration", false)
	if len(issues) != 0 {
		t.Fatalf("standalone config: %+v", issues)
	}
	targets := collectTemporalTargets(cfg.ServingConfig(), "orchestrator")
	pub := NewTemporalPublisher(ops, TemporalPublisherConfig{DestinationID: "orchestrator", StartTimeout: 10 * time.Second, Targets: targets})
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID: "evt-1", Endpoint: "ep", ReceivedAt: time.Now().UTC(),
		Body: map[string]any{"account": map[string]any{"id": "acct-7"}},
	}
	if err := pub.Publish(ctx, "orchestrator", "order-events", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	raw, err := client.DialContext(ctx, client.Options{HostPort: hostPort, Namespace: "default"})
	if err != nil {
		t.Fatalf("raw dial: %v", err)
	}
	t.Cleanup(raw.Close)

	desc, err := raw.DescribeWorkflowExecution(ctx, "acct-acct-7", "")
	if err != nil {
		t.Fatalf("DescribeWorkflowExecution: %v", err)
	}
	info := desc.GetWorkflowExecutionInfo()
	if info.GetType().GetName() != "OrderWorkflow" || info.GetTaskQueue() != "order-events" {
		t.Fatalf("unexpected execution info: type=%q queue=%q", info.GetType().GetName(), info.GetTaskQueue())
	}
	settings := desc.GetExecutionConfig()
	if settings.GetWorkflowExecutionTimeout().AsDuration() != time.Hour || settings.GetWorkflowRunTimeout().AsDuration() != 30*time.Minute || settings.GetDefaultWorkflowTaskTimeout().AsDuration() != 2*time.Minute {
		t.Fatalf("workflow deadlines did not reach Temporal: %+v", settings)
	}
	firstRunID := info.GetExecution().GetRunId()

	var decoded Message
	decodeFirstInput(t, raw, "acct-acct-7", &decoded)
	if decoded.ID != "evt-1" || decoded.Endpoint != "ep" {
		t.Fatalf("envelope did not round-trip: %#v", decoded)
	}

	// Redelivery of the same event: already-started is swallowed and the
	// original run is untouched.
	if err := pub.Publish(ctx, "orchestrator", "order-events", msg); err != nil {
		t.Fatalf("second Publish should be a no-op, got %v", err)
	}
	desc, err = raw.DescribeWorkflowExecution(ctx, "acct-acct-7", "")
	if err != nil {
		t.Fatalf("DescribeWorkflowExecution (2): %v", err)
	}
	if got := desc.GetWorkflowExecutionInfo().GetExecution().GetRunId(); got != firstRunID {
		t.Fatalf("run id changed on redelivery: %q -> %q", firstRunID, got)
	}

	// Body mode: workflow id defaults to the event id and the input is the
	// bare body object.
	bodyMsg := Message{ID: "evt-2", Endpoint: "ep", Body: map[string]any{"hello": "world"}}
	if err := pub.Publish(ctx, "orchestrator", "audit-events", bodyMsg); err != nil {
		t.Fatalf("Publish body: %v", err)
	}
	var body map[string]any
	decodeFirstInput(t, raw, "evt-2", &body)
	if body["hello"] != "world" || body["id"] != nil {
		t.Fatalf("body did not round-trip as a bare object: %#v", body)
	}
}

// decodeFirstInput reads the WorkflowExecutionStarted event of workflowID and
// decodes its single input payload into out via the default data converter.
func decodeFirstInput(t *testing.T, c client.Client, workflowID string, out any) {
	t.Helper()
	iter := c.GetWorkflowHistory(context.Background(), workflowID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	if !iter.HasNext() {
		t.Fatalf("no history for %q", workflowID)
	}
	ev, err := iter.Next()
	if err != nil {
		t.Fatalf("history next: %v", err)
	}
	started := ev.GetWorkflowExecutionStartedEventAttributes()
	if started == nil {
		t.Fatalf("first event is not WorkflowExecutionStarted: %v", ev.GetEventType())
	}
	if err := converter.GetDefaultDataConverter().FromPayloads(started.GetInput(), out); err != nil {
		t.Fatalf("decode input: %v", err)
	}
}
