package httpapi

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// overloadLog bounds admission-refusal logging per configured flow. Metrics
// still count every refusal. Only one request may enter the log writer, and
// other rejected requests never wait for it, even if that writer is blocked.
// There are no background workers or retained request/header/body references.
type overloadLog struct {
	pending atomic.Int64
	busy    atomic.Bool
	// Protected by busy; time.Time preserves the monotonic clock.
	next time.Time
}

func (o *overloadLog) record(logger *slog.Logger, flow string) {
	o.pending.Add(1)
	if !o.busy.CompareAndSwap(false, true) {
		return
	}
	defer o.busy.Store(false)
	now := time.Now()
	if now.Before(o.next) {
		return
	}
	o.next = now.Add(time.Second)
	count := o.pending.Swap(0)
	logger.Error("admission refused requests", "flow", flow, "status", 503,
		"rejected_requests", count, "detail", "inspect admission metrics for exact counts and outcomes")
}
