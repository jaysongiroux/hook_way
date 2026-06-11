package metrics

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/config"
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

func TestWebhookMetricsIncludeDestinationQueueID(t *testing.T) {
	m := New()

	m.HTTP("paddle_billing", "billing", "billing-events", 202)
	m.Publish("paddle_billing", "billing", "billing-events", "success", 0)
	m.RateLimited("paddle_billing", "billing", "billing-events")
	m.Saturated("paddle_billing", "billing", "billing-events")
	m.BodyBytes("paddle_billing", "billing", "billing-events", 512)

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_http_requests_total HTTP requests handled by Hookway.
# TYPE hookway_http_requests_total counter
hookway_http_requests_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events",status="202"} 1
# HELP hookway_publish_total RabbitMQ publish attempts.
# TYPE hookway_publish_total counter
hookway_publish_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events",status="success"} 1
# HELP hookway_rate_limited_total Rate limited webhook requests.
# TYPE hookway_rate_limited_total counter
hookway_rate_limited_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events"} 1
# HELP hookway_saturated_total Webhook requests rejected because the in-flight limit was reached.
# TYPE hookway_saturated_total counter
hookway_saturated_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events"} 1
`),
		"hookway_http_requests_total",
		"hookway_publish_total",
		"hookway_rate_limited_total",
		"hookway_saturated_total",
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

func TestMessagesEndpointsAndDestinationsMetricsAreExposed(t *testing.T) {
	m := New()

	m.MessagePublished("paddle_billing", "billing", "billing-events")
	m.Forbidden("paddle_billing", "billing", "billing-events")
	m.ReplayRejected("paddle_billing", "billing", "billing-events")
	m.RequestDuration("paddle_billing", "billing", "billing-events", 202, 25*time.Millisecond)

	m.RegisterConfig(config.Config{
		MessageQueues: map[string]config.MessageQueueConfig{
			"billing": {Type: "rabbitmq"},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {
				Enabled:            true,
				Path:               "/webhooks/paddle/billing",
				DestinationQueueID: "billing",
				Queue:              "billing-events",
			},
		},
	})

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_destination_info Configured message queue destination (value is always 1).
# TYPE hookway_destination_info gauge
hookway_destination_info{destination_queue_id="billing",type="rabbitmq"} 1
# HELP hookway_endpoint_info Enabled webhook endpoint (value is always 1).
# TYPE hookway_endpoint_info gauge
hookway_endpoint_info{destination_queue_id="billing",endpoint="paddle_billing",path="/webhooks/paddle/billing",queue="billing-events"} 1
# HELP hookway_forbidden_total Webhook requests rejected by the endpoint IP allowlist.
# TYPE hookway_forbidden_total counter
hookway_forbidden_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events"} 1
# HELP hookway_messages_published_total Webhook messages successfully published to RabbitMQ.
# TYPE hookway_messages_published_total counter
hookway_messages_published_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events"} 1
# HELP hookway_replay_rejected_total Webhook requests rejected as signature replays.
# TYPE hookway_replay_rejected_total counter
hookway_replay_rejected_total{destination_queue_id="billing",endpoint="paddle_billing",queue="billing-events"} 1
`),
		"hookway_messages_published_total",
		"hookway_forbidden_total",
		"hookway_replay_rejected_total",
		"hookway_endpoint_info",
		"hookway_destination_info",
	)
	if err != nil {
		t.Fatal(err)
	}

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var requestDurationCount uint64
	for _, fam := range families {
		if fam.GetName() != "hookway_request_duration_seconds" {
			continue
		}
		for _, metric := range fam.GetMetric() {
			if metric.GetHistogram() != nil {
				requestDurationCount = metric.GetHistogram().GetSampleCount()
			}
		}
	}
	if requestDurationCount != 1 {
		t.Fatalf("expected 1 request duration sample, got %d", requestDurationCount)
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
