package queue

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type fakeTemporalStart struct {
	opts    temporalStartOptions
	input   any
	timeout bool // ctx had a deadline
}

type fakeTemporalOps struct {
	starts    []fakeTemporalStart
	startErr  error
	healthErr error
	closed    int
}

func (f *fakeTemporalOps) StartWorkflow(ctx context.Context, opts temporalStartOptions, input any) (string, error) {
	_, hasDeadline := ctx.Deadline()
	f.starts = append(f.starts, fakeTemporalStart{opts: opts, input: input, timeout: hasDeadline})
	if f.startErr != nil {
		return "", f.startErr
	}
	return "run-1", nil
}

func (f *fakeTemporalOps) CheckHealth(ctx context.Context) error { return f.healthErr }
func (f *fakeTemporalOps) Close() error                          { f.closed++; return nil }

func temporalTestMessage() Message {
	return Message{
		ID:       "evt-1",
		Endpoint: "orders",
		Headers:  map[string][]string{"X-Account": {"acct-9"}},
		Body:     map[string]any{"order": map[string]any{"id": "o-1"}},
	}
}

func newTestTemporalPublisher(fake *fakeTemporalOps, targets ...TemporalTarget) (*TemporalPublisher, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewTemporalPublisher(fake, TemporalPublisherConfig{DestinationID: "orchestrator", Logger: logger, Targets: targets}), &buf
}

func TestTemporalPublisher_StartsWorkflowWithEnvelope(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "order-events", WorkflowType: "OrderWorkflow"})
	msg := temporalTestMessage()
	if err := pub.Publish(context.Background(), "orchestrator", "order-events", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.starts) != 1 {
		t.Fatalf("expected 1 start, got %d", len(fake.starts))
	}
	s := fake.starts[0]
	if s.opts.TaskQueue != "order-events" || s.opts.WorkflowType != "OrderWorkflow" {
		t.Fatalf("bad start options: %#v", s.opts)
	}
	if s.opts.WorkflowID != "evt-1" {
		t.Fatalf("workflow id should default to event id, got %q", s.opts.WorkflowID)
	}
	got, ok := s.input.(Message)
	if !ok || got.ID != "evt-1" {
		t.Fatalf("expected envelope Message input, got %#v", s.input)
	}
	if !s.timeout {
		t.Fatal("expected start_timeout deadline on context")
	}
}

func TestTemporalPublisher_BodyInput(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "q", WorkflowType: "W", Input: "body"})
	if err := pub.Publish(context.Background(), "orchestrator", "q", temporalTestMessage()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body, ok := fake.starts[0].input.(map[string]any)
	if !ok || body["order"] == nil {
		t.Fatalf("expected decoded body input, got %#v", fake.starts[0].input)
	}
}

func TestTemporalPublisher_BodyInputFallsBackToEnvelopeForRawBody(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, buf := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "q", WorkflowType: "W", Input: "body"})
	raw := "aGVsbG8="
	msg := Message{ID: "evt-raw", Endpoint: "orders", RawBodyBase64: &raw}
	if err := pub.Publish(context.Background(), "orchestrator", "q", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, ok := fake.starts[0].input.(Message); !ok {
		t.Fatalf("expected envelope fallback, got %#v", fake.starts[0].input)
	}
	if line := lastQueueLogLine(t, buf, "temporal body input unavailable for non-JSON body; sending envelope"); line == nil || line["event_id"] != "evt-raw" {
		t.Fatalf("expected warn log with event_id, got %v", line)
	}
}

func TestResolveWorkflowIDSources(t *testing.T) {
	msg := temporalTestMessage()
	cases := []struct {
		name string
		s    WorkflowIDSettings
		want string
	}{
		{"unset falls back to event id", WorkflowIDSettings{}, "evt-1"},
		{"prefix only", WorkflowIDSettings{Prefix: "p-"}, "p-evt-1"},
		{"static", WorkflowIDSettings{Source: "static", Value: "fixed"}, "fixed"},
		{"endpoint", WorkflowIDSettings{Source: "endpoint"}, "orders"},
		{"header", WorkflowIDSettings{Source: "header", Path: "X-Account", Prefix: "acct:"}, "acct:acct-9"},
		{"body with prefix", WorkflowIDSettings{Source: "body", Path: "order.id", Prefix: "order-"}, "order-o-1"},
		{"body miss falls back to event id with prefix", WorkflowIDSettings{Source: "body", Path: "nope", Prefix: "order-"}, "order-evt-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveWorkflowID(msg, tc.s)
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v want %q", got, err, tc.want)
			}
		})
	}
}

func TestResolveWorkflowIDPrefixTemplate(t *testing.T) {
	msg := temporalTestMessage()
	msg.Variables = map[string]string{"envName": "tenant-a"}
	s, err := WorkflowIDSettings{Source: "body", Path: "order.id", Prefix: "{envName}-aan-"}.withParsedPrefix()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveWorkflowID(msg, s)
	if err != nil || got != "tenant-a-aan-o-1" {
		t.Fatalf("templated prefix: %q %v", got, err)
	}
	msg.Variables = nil
	if _, err := resolveWorkflowID(msg, s); err == nil {
		t.Fatal("unbound prefix placeholder must error")
	}
	if _, err := (WorkflowIDSettings{Prefix: "{bad"}).withParsedPrefix(); err == nil {
		t.Fatal("malformed prefix must error")
	}
}

func TestTemporalPublisher_PrefixTemplateUnboundFailsPublish(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "q", WorkflowType: "W", WorkflowID: WorkflowIDSettings{Prefix: "{envName}-"}})
	if err := pub.Publish(context.Background(), "orchestrator", "q", temporalTestMessage()); err == nil || len(fake.starts) != 0 {
		t.Fatalf("expected error and no start, got err=%v starts=%d", err, len(fake.starts))
	}
}

func TestTemporalPublisher_PassesTimeoutsAndPolicy(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{
		TaskQueue: "q", WorkflowType: "W", IDReusePolicy: "reject_duplicate",
		ExecutionTimeout: 24 * time.Hour, RunTimeout: time.Hour, TaskTimeout: 10 * time.Second,
	})
	if err := pub.Publish(context.Background(), "orchestrator", "q", temporalTestMessage()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	o := fake.starts[0].opts
	if o.IDReusePolicy != "reject_duplicate" || o.ExecutionTimeout != 24*time.Hour || o.RunTimeout != time.Hour || o.TaskTimeout != 10*time.Second {
		t.Fatalf("options not passed through: %#v", o)
	}
}

func TestTemporalPublisher_SwallowsAlreadyStarted(t *testing.T) {
	fake := &fakeTemporalOps{startErr: errTemporalAlreadyStarted}
	pub, buf := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "q", WorkflowType: "W"})
	if err := pub.Publish(context.Background(), "orchestrator", "q", temporalTestMessage()); err != nil {
		t.Fatalf("expected nil for already-started, got %v", err)
	}
	if line := lastQueueLogLine(t, buf, "skipped duplicate workflow start"); line == nil || line["workflow_id"] != "evt-1" {
		t.Fatalf("expected debug log, got %v", line)
	}
}

func TestTemporalPublisher_PropagatesOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeTemporalOps{startErr: boom}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "q", WorkflowType: "W"})
	err := pub.Publish(context.Background(), "orchestrator", "q", temporalTestMessage())
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom, got %v", err)
	}
}

func TestTemporalPublisher_UnknownTarget(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "q", WorkflowType: "W"})
	if err := pub.Publish(context.Background(), "orchestrator", "other", temporalTestMessage()); err == nil {
		t.Fatal("expected error for unknown target")
	}
	if len(fake.starts) != 0 {
		t.Fatal("no start should be attempted for unknown target")
	}
}

func TestTemporalPublisher_ReadyAndClose(t *testing.T) {
	healthErr := errors.New("unhealthy")
	fake := &fakeTemporalOps{healthErr: healthErr}
	pub, _ := newTestTemporalPublisher(fake)
	if err := pub.Ready(context.Background()); !errors.Is(err, healthErr) {
		t.Fatalf("Ready should surface health error, got %v", err)
	}
	if err := pub.Close(); err != nil || fake.closed != 1 {
		t.Fatalf("Close: err=%v closed=%d", err, fake.closed)
	}
}
