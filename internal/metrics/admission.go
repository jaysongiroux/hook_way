package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Admission owns one flow server's contribution to a shared registry. Only two
// fixed modes are exposed; reloads do not leave behind server identity labels.
type Admission struct {
	mu         sync.Mutex
	active     int
	closed     bool
	registered bool
	capacity   float64
	requests   prometheus.Gauge
	servers    prometheus.Gauge
	total      prometheus.Gauge
}

// NewAdmission registers capacity only after a flow server compiles successfully.
// Nonpositive capacity is unlimited and contributes no finite slots.
func (m *Metrics) NewAdmission(capacity int) *Admission {
	mode := "unlimited"
	if capacity > 0 {
		mode = "bounded"
	} else {
		capacity = 0
	}
	a := &Admission{
		registered: true, capacity: float64(capacity), total: m.admissionCapacity,
		requests: m.admitted.WithLabelValues(mode), servers: m.admissionServers.WithLabelValues(mode),
	}
	a.total.Add(a.capacity)
	a.servers.Inc()
	return a
}

// Enter records accepted work after the actual admission semaphore succeeds.
// Once closed, new flow work must be rejected. Pair each true result with Leave.
func (a *Admission) Enter() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	a.active++
	a.requests.Inc()
	return true
}

func (a *Admission) Leave() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
	a.requests.Dec()
	a.unregisterIfDrained()
}

// Close retires the owner once, retaining capacity until admitted work leaves.
// This accounts for overlapping old/new servers and does not drain HTTP itself.
func (a *Admission) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.unregisterIfDrained()
}

func (a *Admission) unregisterIfDrained() {
	if a.closed && a.active == 0 && a.registered {
		a.total.Sub(a.capacity)
		a.servers.Dec()
		a.registered = false
	}
}
