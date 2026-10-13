package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func mustReplayGuard(t *testing.T, body string) *replayGuardNode {
	t.Helper()
	reg, err := NewRegistryWithOptions(protectionTestOptions(t, RegistryOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("replay_guard")
	node, err := spec.New(nodeConfigFromYAML(t, "replay_guard", "store: test\n"+body))
	if err != nil {
		t.Fatal(err)
	}
	return node.(*replayGuardNode)
}

func TestReplayGuardKeyIsStableAcrossRunAndCompensate(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1m\nmax_entries: 100\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"id":1}`)}}
	runKey, err := n.reservationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	compKey, err := n.reservationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	if runKey == "" || runKey != compKey {
		t.Fatalf("keys differ: run=%q compensate=%q", runKey, compKey)
	}
}

func TestReplayGuardRejectsDuplicateWithinTTL(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1m\nmax_entries: 100\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"id":1}`)}}
	if _, err := n.Run(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	_, err := n.Run(context.Background(), v)
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusConflict {
		t.Fatalf("duplicate = %v, want 409", err)
	}
}

func TestReplayGuardCompensateReleasesReservation(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1m\nmax_entries: 100\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"id":1}`)}}
	out, err := n.Run(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	v = out.(graph.Verified)
	if err := n.Compensate(context.Background(), v, errors.New("publish failed")); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Run(context.Background(), v); err != nil {
		t.Fatalf("retry after compensate: %v", err)
	}
}

func TestReplayGuardAcceptsAfterTTL(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1ms\nmax_entries: 100\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"id":1}`)}}
	if _, err := n.Run(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := n.Run(context.Background(), v); err != nil {
		t.Fatalf("after TTL: %v", err)
	}
}

func TestReplayGuardStaleCompensationCannotReleaseReplacement(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 250ms\nmax_entries: 100\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"event_id":"stable-provider-event"}`)}}
	first, err := n.Run(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	firstOwner := first.(graph.Verified).Reservations[n.id].Owner
	time.Sleep(275 * time.Millisecond)
	replacement, err := n.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("expired reservation did not admit replacement: %v", err)
	}
	replacementOwner := replacement.(graph.Verified).Reservations[n.id].Owner
	if firstOwner == "" || replacementOwner == "" || firstOwner == replacementOwner {
		t.Fatal("reservations did not receive independent owner tokens")
	}
	if err := n.Compensate(context.Background(), first, errors.New("old validation finally failed")); err != nil {
		t.Fatal(err)
	}
	_, err = n.Run(context.Background(), v)
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != http.StatusConflict {
		t.Fatalf("stale compensation released new owner: %v", err)
	}
	if err := n.Compensate(context.Background(), replacement, errors.New("new validation failed")); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Run(context.Background(), v); err != nil {
		t.Fatalf("current owner could not release its reservation: %v", err)
	}
	if len(v.Reservations) != 0 || first.(graph.Verified).Reservations[n.id].Owner != firstOwner {
		t.Fatal("request-local reservation ownership was mutated")
	}
}
