package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cancellationNode struct{ entered chan struct{} }

func (n cancellationNode) Run(ctx context.Context, _ Payload) (Payload, error) {
	close(n.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCancellableExecutionRetainsCallerCancellationForDelivery(t *testing.T) {
	for _, cancellable := range []bool{false, true} {
		t.Run(map[bool]string{false: "webhook_detached", true: "mcp_cancellable"}[cancellable], func(t *testing.T) {
			entered := make(chan struct{})
			reg := NewRegistry()
			for _, spec := range []NodeSpec{
				{Type: "root", In: PortNone, Out: PortEnvelope, New: func(NodeConfig) (Node, error) { return &fnNode{out: Envelope{}}, nil }},
				{Type: "delivery", In: PortEnvelope, Out: PortTerminal, Detached: true, New: func(NodeConfig) (Node, error) { return cancellationNode{entered}, nil }},
			} {
				if err := reg.Register(spec); err != nil {
					t.Fatal(err)
				}
			}
			f := mustCompile(t, flowConfig([][2]string{{"in", "root"}, {"out", "delivery"}}, "in>out"), reg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := f.Run(ctx, Envelope{}, RunOptions{Cancellable: cancellable, PublishTimeout: 50 * time.Millisecond})
				done <- err
			}()
			<-entered
			cancel()
			select {
			case err := <-done:
				want := context.DeadlineExceeded
				if cancellable {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("got %v want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("execution leaked after cancellation/deadline")
			}
		})
	}
}
