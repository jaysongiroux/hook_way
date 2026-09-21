package flowconfig

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
)

type httpTriggerNode struct {
	id   string
	opts HTTPTriggerOptions
}

func newHTTPTrigger(id string, opts HTTPTriggerOptions) graph.Node {
	return httpTriggerNode{id: id, opts: opts}
}

func (n httpTriggerNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	head, ok := in.(graph.RequestHead)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("http_trigger %q: expected RequestHead, got %T", n.id, in),
		}
	}
	return head, nil
}

// HeadFromRequest builds the seed payload for a flow. The trigger itself does
// not read the body; gates wired after it stay provably pre-buffering.
func HeadFromRequest(r *http.Request, pathVars map[string]string, sourceIP string) graph.RequestHead {
	vars := map[string]string{}
	for k, v := range pathVars {
		vars[k] = v
	}
	return graph.RequestHead{
		Method:      r.Method,
		Path:        r.URL.Path,
		PathVars:    vars,
		Query:       r.URL.Query(),
		Header:      r.Header.Clone(),
		SourceIP:    sourceIP,
		Hops:        parseHopCount(r.Header.Get(config.LoopHopsHeader)),
		RequestID:   requestIDFrom(r),
		TraceID:     traceIDFrom(r),
		Traceparent: r.Header.Get("Traceparent"),
	}
}

func requestIDFrom(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Request-ID")); id != "" {
		return id
	}
	return uuid.NewString()
}

func traceIDFrom(r *http.Request) string {
	if traceparent := r.Header.Get("Traceparent"); traceparent != "" {
		parts := strings.Split(traceparent, "-")
		if len(parts) >= 2 && isValidTraceID(parts[1]) {
			return parts[1]
		}
	}
	if id := strings.TrimSpace(r.Header.Get("X-Trace-ID")); isValidTraceID(id) {
		return id
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func isValidTraceID(id string) bool {
	if len(id) != 32 || !isHex(id) {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] != '0' {
			return true
		}
	}
	return false
}

func parseHopCount(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
