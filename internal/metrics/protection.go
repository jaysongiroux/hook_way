package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

func (m *Metrics) initProtection() {
	labels := []string{"store", "policy", "kind", "strategy", "operation", "result"}
	m.protectionOperations = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_protection_operations_total", Help: "Protection decisions by configured policy and bounded outcome; never request identities."}, labels)
	m.protectionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "hookway_protection_operation_duration_seconds", Help: "Protection operation latency, including backend coordination.", Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}}, labels)
	m.protectionReady = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hookway_protection_store_ready", Help: "Whether a configured protection store and all its policies passed their last verification."}, []string{"store"})
	m.protectionCleanup = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_protection_cleanup_total", Help: "Cleanup passes by configured store and bounded result."}, []string{"store", "result"})
	m.protectionDeleted = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_protection_expired_entries_deleted_total", Help: "Expired protection entries physically removed by configured store."}, []string{"store"})
	m.protectionCleanupDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "hookway_protection_cleanup_duration_seconds", Help: "Bounded expired-state cleanup duration."}, []string{"store"})
	m.protectionPool = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hookway_protection_pool_connections", Help: "Protection client pool connections in use or idle, by configured store."}, []string{"store", "state"})
	m.protectionPoolWait = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_protection_pool_wait_seconds_total", Help: "Cumulative connection acquisition wait measured by protection clients."}, []string{"store"})
	m.registry.MustRegister(m.protectionPool, m.protectionPoolWait, m.protectionOperations, m.protectionDuration, m.protectionReady, m.protectionCleanup, m.protectionDeleted, m.protectionCleanupDuration)
}

func (m *Metrics) ProtectionOperation(store, policy, kind, strategy, operation, result string, elapsed time.Duration) {
	labels := []string{store, policy, kind, strategy, operation, result}
	m.protectionOperations.WithLabelValues(labels...).Inc()
	m.protectionDuration.WithLabelValues(labels...).Observe(elapsed.Seconds())
}
func (m *Metrics) ProtectionReady(store string, ready bool) {
	value := 0.0
	if ready {
		value = 1
	}
	m.protectionReady.WithLabelValues(store).Set(value)
}
func (m *Metrics) ProtectionCleanup(store string, deleted int64, ok bool, elapsed time.Duration) {
	result := "error"
	if ok {
		result = "success"
	}
	m.protectionCleanup.WithLabelValues(store, result).Inc()
	m.protectionDeleted.WithLabelValues(store).Add(float64(deleted))
	m.protectionCleanupDuration.WithLabelValues(store).Observe(elapsed.Seconds())
}

func (m *Metrics) ProtectionPool(store string, inUse, idle int64, wait time.Duration) {
	m.protectionPool.WithLabelValues(store, "in_use").Set(float64(inUse))
	m.protectionPool.WithLabelValues(store, "idle").Set(float64(idle))
	if wait > 0 {
		m.protectionPoolWait.WithLabelValues(store).Add(wait.Seconds())
	}
}
