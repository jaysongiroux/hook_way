package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

func TestGuardHeaderIdentityCaseDuplicatesAndPathSeparation(t *testing.T) {
	n := mustReplayGuard(t, "key: '{header.X-Event-ID}/{path}'\nttl: 1m\n")
	v := graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"x-event-id": []string{"event"}}, PathVars: map[string]string{"path": "route", "header.X-Event-ID": "cannot-shadow-header"}}}}
	key, err := n.reservationKey(v)
	if err != nil || key != "event/route" {
		t.Fatalf("%s %v", key, err)
	}
	for _, head := range []graph.RequestHead{
		{Header: http.Header{"X-Event-Id": []string{"a", "b"}}, PathVars: map[string]string{"path": "route"}},
		{Header: http.Header{"X-Event-Id": []string{"a"}, "x-event-id": []string{"b"}}, PathVars: map[string]string{"path": "route"}},
		{PathVars: map[string]string{"path": "route", "header.X-Event-ID": "cannot-shadow-header"}},
		{Header: http.Header{"X-Event-Id": []string{"event"}}},
	} {
		_, err := n.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Head: head}})
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 400 {
			t.Fatalf("ambiguous/unbound key accepted: %v", err)
		}
	}
	if _, err := vars.Parse("{header.X-Event-ID}"); err == nil {
		t.Fatal("guard parser widened standard vars parser")
	}
	for _, key := range []string{"{header.}", "{header.Bad Header}", "{bad-name}"} {
		if err := validateReplayGuard(ReplayGuardOptions{Store: "test", Key: key}); err == nil {
			t.Fatalf("invalid guard key accepted: %s", key)
		}
	}
	a := guardPolicy("flow", "guard", ReplayGuardOptions{Store: "test", Key: "{header.X-Event-ID}"})
	b := guardPolicy("flow", "guard", ReplayGuardOptions{Store: "test", Key: "{header.x-event-id}"})
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("HTTP header casing changed canonical policy")
	}
}
