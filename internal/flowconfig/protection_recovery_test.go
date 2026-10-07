package flowconfig

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

// Receipt counts are separate from graph outcomes: an error after dispatch is
// not evidence that the destination failed to receive the event.
type recoveryEffectPublisher struct {
	mu         sync.Mutex
	calls      map[string]int
	errors     map[string]error
	panicAt    string
	resolveErr error
}

func (p *recoveryEffectPublisher) Resolve(string) (Publisher, error) {
	return p, p.resolveErr
}

func (p *recoveryEffectPublisher) Publish(_ context.Context, _, target string, _ graph.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[target]++
	if target == p.panicAt {
		panic("destination receipt recorded before publisher panic")
	}
	return p.errors[target]
}

func TestGuardReleasedAfterValidationFailureWithoutEffects(t *testing.T) {
	pub := &recoveryEffectPublisher{calls: map[string]int{}}
	src := strings.Replace(guardedEffectYAML,
		"      - {id: out, type: publish, destination: primary, target: events}",
		"      - {id: check, type: json_validate, schema: {type: array}}\n      - {id: out, type: publish, destination: primary, target: events}", 1)
	src = strings.Replace(src, "env>out]", "env>check, check>out]", 1)
	flow := effectFlow(t, src, RegistryOptions{Publishers: pub})
	for attempt := 0; attempt < 2; attempt++ {
		term, err := runEffectFlow(flow)
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 400 || term.SideEffects || !graph.NeedsCompensation(term, err) {
			t.Fatalf("attempt %d: expected validation failure with releasable guard, terminal=%+v err=%v", attempt, term, err)
		}
		if len(term.Compensations) != 1 || len(term.Deliveries) != 1 || !term.Deliveries[0].Unattempted {
			t.Fatalf("attempt %d: unexpected ownership or delivery state: %+v", attempt, term)
		}
		if err := graph.RunCompensations(context.Background(), term, err, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if len(pub.calls) != 0 {
		t.Fatalf("validation dispatched downstream: %v", pub.calls)
	}
}

func TestGuardRecoveryUsesDispatchEvidenceNotPublishError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		partial    bool
		resolveErr error
		panicAt    string
		wantCalls  int
		retained   bool
	}{
		{name: "known_pre_dispatch_failure", resolveErr: errors.New("publisher unavailable"), wantCalls: 0},
		{name: "uncertain_receipt", wantCalls: 1, retained: true},
		{name: "panic_after_receipt", panicAt: "events", wantCalls: 1, retained: true},
		{name: "partial_stop", partial: true, wantCalls: 2, retained: true},
		{name: "partial_skip", partial: true, mode: "skip_failed", wantCalls: 3, retained: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub := &recoveryEffectPublisher{calls: map[string]int{}, errors: map[string]error{"events": errors.New("receipt accepted, confirmation lost")}, resolveErr: tc.resolveErr, panicAt: tc.panicAt}
			src := guardedEffectYAML
			if tc.partial {
				delete(pub.errors, "events")
				pub.errors["second"] = errors.New("second destination confirmation lost")
				src = strings.Replace(src, "    edges:", "      - {id: second, type: publish, destination: primary, target: second}\n      - {id: third, type: publish, destination: primary, target: third}\n    edges:", 1)
				src = strings.Replace(src, "env>out]", "env>out, env>second, env>third]", 1)
			}
			if tc.mode != "" {
				src = strings.Replace(src, "  demo:", "  demo:\n    delivery_failure_policy: "+tc.mode, 1)
			}
			flow := effectFlow(t, src, RegistryOptions{Publishers: pub})
			term, err := runEffectFlow(flow)
			if term.SideEffects != tc.retained || graph.NeedsCompensation(term, err) == tc.retained {
				t.Fatalf("wrong compensation decision: terminal=%+v err=%v", term, err)
			}
			if tc.mode == "skip_failed" {
				if err != nil {
					t.Fatalf("partial skip_failed should succeed: %v", err)
				}
			} else if err == nil {
				t.Fatal("expected visible failure")
			}
			if tc.partial {
				outcomes := map[string]graph.Delivery{}
				for _, d := range term.Deliveries {
					outcomes[d.NodeID] = d
				}
				if len(outcomes) != 3 || outcomes["out"].Err != nil || outcomes["second"].Err == nil || outcomes["third"].Unattempted != (tc.mode != "skip_failed") {
					t.Fatalf("partial outcomes conflated: %+v", term.Deliveries)
				}
			}
			if err := graph.RunCompensations(context.Background(), term, err, time.Second); err != nil {
				t.Fatal(err)
			}
			_, retryErr := runEffectFlow(flow)
			var ne graph.NodeError
			if !errors.As(retryErr, &ne) || (ne.Status == 409) != tc.retained {
				t.Fatalf("wrong retry protection result: %v", retryErr)
			}
			total := 0
			for _, n := range pub.calls {
				total += n
			}
			if total != tc.wantCalls {
				t.Fatalf("receipts=%v total=%d want=%d", pub.calls, total, tc.wantCalls)
			}
		})
	}
}

func TestGuardRetainedWhenOutboundReceiptLosesHTTPResponse(t *testing.T) {
	var receipts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "{}" {
			t.Errorf("receipt body=%q err=%v", body, err)
		}
		receipts.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // effect accepted, no HTTP response reaches the core.
	}))
	defer upstream.Close()
	src := strings.Replace(guardedEffectYAML,
		"      - {id: out, type: publish, destination: primary, target: events}",
		"      - {id: up, type: http_request, url: '"+upstream.URL+"', timeout: 1s}\n      - {id: done, type: respond, status: 200}", 1)
	src = strings.Replace(src, "env>out]", "env>up, up>done]", 1)
	flow := effectFlow(t, src)
	term, err := runEffectFlow(flow)
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 502 || !term.SideEffects || graph.NeedsCompensation(term, err) || receipts.Load() != 1 {
		t.Fatalf("lost response wrongly classified: terminal=%+v err=%v receipts=%d", term, err, receipts.Load())
	}
	if err := graph.RunCompensations(context.Background(), term, err, time.Second); err != nil {
		t.Fatal(err)
	}
	_, err = runEffectFlow(flow)
	if !errors.As(err, &ne) || ne.Status != 409 || receipts.Load() != 1 {
		t.Fatalf("retry repeated possible HTTP effect: err=%v receipts=%d", err, receipts.Load())
	}
}
