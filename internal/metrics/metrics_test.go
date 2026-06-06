package metrics

import (
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
