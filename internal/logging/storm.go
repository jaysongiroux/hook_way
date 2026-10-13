package logging

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Only these fixed, repetitive runtime sites are sampled. Lifecycle/configuration
// guidance and operator audit messages deliberately bypass the budget. No state
// is keyed by an error, URL, flow, customer, event or request identifier.
var stormMessages = [...]string{
	"request handled", "flow completed", "node failed", "response write failed",
	"rejected request: path variable contains a traversal segment",
	"webhook delivery retry", "webhook delivery failed (terminal)",
	"webhook delivery failed (attempts exhausted)", "webhook delivery dead-lettered",
	"webhook dead-letter dropped: no resolver configured", "webhook dead-letter dispatch failed",
	"failure capture dropped a record", "inspection capture dropped a record",
	"readiness probe failed", "readiness check failed", "state sample failed",
	"replay api request failed", "record capture accepted", "retry run finished",
	"claim retry runs failed", "expire run leases failed",
	"finalize retry run failed; its lease will expire it to unknown",
	"retry run finished after its lease expired", "gc sweep failed", "gc sweep finished",
	"dynamic target cache full; evicted oldest target",
	"panic recovered in http handler", "panic recovered in retry worker",
	"panic recovered in readiness prober", "panic recovered in webhook delivery worker",
}

const stormBurst = 10

type stormWindow struct {
	start      time.Time
	emitted    int
	suppressed uint64
	summarized bool
}

type stormState struct {
	mu      sync.Mutex
	windows [len(stormMessages)]stormWindow
	now     func() time.Time
	sink    slog.Handler
	onDrop  func()
}

type stormHandler struct {
	slog.Handler
	state *stormState
}

// StormHandler bounds repetitive runtime logs to ten records per category in a
// one-second window, plus suppression summaries. Metrics still count every
// outcome. A handler enabled at Debug bypasses sampling for explicit short-lived
// troubleshooting. It adds neither goroutines nor timers; output sink blocking
// and unrestricted debug output remain the deployment owner's responsibility.
func StormHandler(next slog.Handler, onDrop func()) slog.Handler {
	return &stormHandler{Handler: next, state: &stormState{now: time.Now, sink: next, onDrop: onDrop}}
}

func (h *stormHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.Enabled(ctx, slog.LevelDebug) {
		return h.Handler.Handle(ctx, record)
	}
	category := -1
	for i, message := range stormMessages {
		if record.Message == message {
			category = i
			break
		}
	}
	if category < 0 {
		return h.Handler.Handle(ctx, record)
	}
	now := h.state.now()
	h.state.mu.Lock()
	w := &h.state.windows[category]
	var summary uint64
	if w.start.IsZero() || now.Sub(w.start) >= time.Second {
		summary = w.suppressed
		*w = stormWindow{start: now}
	}
	allowed := w.emitted < stormBurst
	if allowed {
		w.emitted++
	} else {
		w.suppressed++
		if !w.summarized {
			summary = w.suppressed
			w.suppressed = 0
			w.summarized = true
		}
	}
	h.state.mu.Unlock()
	if !allowed && h.state.onDrop != nil {
		h.state.onDrop()
	}
	if summary > 0 {
		// The original sink excludes With/WithGroup attributes: summaries retain
		// only the fixed site name and count, never the last request's attributes.
		r := slog.NewRecord(now, record.Level, "repeated runtime logs suppressed", 0)
		r.AddAttrs(slog.String("category", stormMessages[category]), slog.Uint64("suppressed_since_last_summary", summary))
		if err := h.state.sink.Handle(ctx, r); err != nil {
			return err
		}
	}
	if allowed {
		return h.Handler.Handle(ctx, record)
	}
	return nil
}

func (h *stormHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &stormHandler{Handler: h.Handler.WithAttrs(attrs), state: h.state}
}

func (h *stormHandler) WithGroup(name string) slog.Handler {
	return &stormHandler{Handler: h.Handler.WithGroup(name), state: h.state}
}
