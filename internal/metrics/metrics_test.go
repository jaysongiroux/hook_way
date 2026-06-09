package metrics

import (
	"runtime"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRabbitMQReliabilityMetricsAreExposed(t *testing.T) {
	m := New()

	m.RabbitMQReconnect()
	m.RabbitMQPublishConfirm()
	m.RabbitMQPublishRetry()
	m.RabbitMQDeadLetterPublish()
	m.ConfigLoadFailure("validation")

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_config_load_failures_total Config load failures before serving traffic.
# TYPE hookway_config_load_failures_total counter
hookway_config_load_failures_total{reason="validation"} 1
# HELP hookway_rabbitmq_confirms_total RabbitMQ publish confirms.
# TYPE hookway_rabbitmq_confirms_total counter
hookway_rabbitmq_confirms_total 1
# HELP hookway_rabbitmq_dead_letter_publish_total RabbitMQ dead-letter publish attempts.
# TYPE hookway_rabbitmq_dead_letter_publish_total counter
hookway_rabbitmq_dead_letter_publish_total 1
# HELP hookway_rabbitmq_reconnects_total RabbitMQ reconnects.
# TYPE hookway_rabbitmq_reconnects_total counter
hookway_rabbitmq_reconnects_total 1
# HELP hookway_rabbitmq_retries_total RabbitMQ publish retries.
# TYPE hookway_rabbitmq_retries_total counter
hookway_rabbitmq_retries_total 1
`),
		"hookway_rabbitmq_reconnects_total",
		"hookway_rabbitmq_confirms_total",
		"hookway_rabbitmq_retries_total",
		"hookway_rabbitmq_dead_letter_publish_total",
		"hookway_config_load_failures_total",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnableRuntimeCollectorsExposesGoAndProcessMetrics(t *testing.T) {
	m := New()
	m.EnableRuntimeCollectors()
	m.EnableRuntimeCollectors() // idempotent: must not panic on re-register

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, fam := range families {
		present[fam.GetName()] = true
	}
	// Go runtime metrics are available on every platform.
	if !present["go_goroutines"] {
		t.Fatal("expected go_goroutines to be exposed")
	}
	// The process collector reads /proc, so process_* metrics only materialise
	// on Linux (e.g. the production container); skip the assertion elsewhere.
	if runtime.GOOS == "linux" {
		for _, want := range []string{"process_cpu_seconds_total", "process_resident_memory_bytes"} {
			if !present[want] {
				t.Fatalf("expected runtime metric %q to be exposed", want)
			}
		}
	}
}

func TestMetricsImplementsRabbitMQMetricsHook(t *testing.T) {
	var _ interface {
		RabbitMQReconnect()
		RabbitMQPublishConfirm()
		RabbitMQPublishRetry()
		RabbitMQDeadLetterPublish()
		ConfigLoadFailure(string)
	} = New()
	var _ = prometheus.Gauge(nil)
}
