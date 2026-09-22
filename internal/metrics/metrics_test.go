package metrics

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestRabbitMQReliabilityMetricsAreExposed(t *testing.T) {
	m := New()

	m.RabbitMQReconnect()
	m.RabbitMQPublishConfirm()
	m.RabbitMQPublishRetry()
	m.RabbitMQDeadLetterPublish()
	m.RabbitMQUnroutable("billing", "billing-events")
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
# HELP hookway_rabbitmq_unroutable_total RabbitMQ mandatory publishes returned as unroutable.
# TYPE hookway_rabbitmq_unroutable_total counter
hookway_rabbitmq_unroutable_total{destination_queue_id="billing",queue="billing-events"} 1
`),
		"hookway_rabbitmq_reconnects_total",
		"hookway_rabbitmq_confirms_total",
		"hookway_rabbitmq_retries_total",
		"hookway_rabbitmq_dead_letter_publish_total",
		"hookway_rabbitmq_unroutable_total",
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
# HELP hookway_publish_total Publish attempts to a configured destination.
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

func TestWebhookDeliveryMetricsAreExposed(t *testing.T) {
	m := New()

	m.WebhookDelivery("notify", "orders", "5xx")
	m.WebhookRetry("notify", "orders")
	m.WebhookDropped("notify", "orders", "queue_full")
	m.WebhookDeadLetter("notify", "orders", "4xx")
	m.WebhookDeadLetterFailure("notify", "orders")
	m.WebhookQueueDepth("notify", 7)
	m.WebhookQueueCapacity("notify", 1000)

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_webhook_dropped_total Webhook deliveries dropped before delivery, by reason (queue_full|engine_closing).
# TYPE hookway_webhook_dropped_total counter
hookway_webhook_dropped_total{destination_queue_id="notify",queue="orders",reason="queue_full"} 1
# HELP hookway_webhook_dead_letter_total Terminally-failed webhook deliveries redirected to a dead-letter destination.
# TYPE hookway_webhook_dead_letter_total counter
hookway_webhook_dead_letter_total{destination_queue_id="notify",outcome="4xx",queue="orders"} 1
# HELP hookway_webhook_dead_letter_failures_total Dead-letter redirects that themselves failed.
# TYPE hookway_webhook_dead_letter_failures_total counter
hookway_webhook_dead_letter_failures_total{destination_queue_id="notify",queue="orders"} 1
# HELP hookway_webhook_queue_depth Current webhook delivery queue depth.
# TYPE hookway_webhook_queue_depth gauge
hookway_webhook_queue_depth{destination_queue_id="notify"} 7
# HELP hookway_webhook_queue_capacity Configured webhook delivery queue capacity.
# TYPE hookway_webhook_queue_capacity gauge
hookway_webhook_queue_capacity{destination_queue_id="notify"} 1000
`),
		"hookway_webhook_dropped_total",
		"hookway_webhook_dead_letter_total",
		"hookway_webhook_dead_letter_failures_total",
		"hookway_webhook_queue_depth",
		"hookway_webhook_queue_capacity",
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
		Destinations: map[string]config.DestinationConfig{
			"billing": {Type: "rabbitmq"},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {
				Enabled: true,
				Path:    "/webhooks/paddle/billing",
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "billing",
					Target:      "billing-events",
				}},
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
# HELP hookway_messages_published_total Webhook messages successfully published to a destination.
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

func TestRegisterConfigExposesEndpointAndDestinationInfo(t *testing.T) {
	m := New()
	cfg := config.Config{
		Destinations: map[string]config.DestinationConfig{
			"activity": {Type: "rabbitmq"},
			"billing":  {Type: "rabbitmq"},
		},
		Endpoints: map[string]config.EndpointConfig{
			"example_fanout": {
				Enabled: true,
				Path:    "/webhooks/example",
				Destinations: []config.EndpointDestinationConfig{
					{Destination: "billing", Target: "example-webhooks"},
					{Destination: "activity", Target: "example-audit-webhooks"},
				},
			},
		},
	}

	m.RegisterConfig(cfg)

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_destination_info Configured message queue destination (value is always 1).
# TYPE hookway_destination_info gauge
hookway_destination_info{destination_queue_id="activity",type="rabbitmq"} 1
hookway_destination_info{destination_queue_id="billing",type="rabbitmq"} 1
# HELP hookway_endpoint_info Enabled webhook endpoint (value is always 1).
# TYPE hookway_endpoint_info gauge
hookway_endpoint_info{destination_queue_id="activity",endpoint="example_fanout",path="/webhooks/example",queue="example-audit-webhooks"} 1
hookway_endpoint_info{destination_queue_id="billing",endpoint="example_fanout",path="/webhooks/example",queue="example-webhooks"} 1
`),
		"hookway_endpoint_info",
		"hookway_destination_info",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPanicMetricCountsByLocation(t *testing.T) {
	m := New()

	m.Panic("handler")
	m.Panic("handler")
	m.Panic("fanout")
	m.Panic("webhook_worker")

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_panics_total Panics recovered by Hookway, by the location they were recovered in.
# TYPE hookway_panics_total counter
hookway_panics_total{location="fanout"} 1
hookway_panics_total{location="handler"} 2
hookway_panics_total{location="webhook_worker"} 1
`),
		"hookway_panics_total",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSetBuildInfoRegistersGaugeSetToOne(t *testing.T) {
	m := New()

	m.SetBuildInfo("1.2.3", "abc1234")

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_build_info Build version and commit Hookway was compiled from (value is always 1).
# TYPE hookway_build_info gauge
hookway_build_info{commit="abc1234",version="1.2.3"} 1
`),
		"hookway_build_info",
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReplayCaptureMetricsAreExposed(t *testing.T) {
	m := New()

	m.ReplayCaptured("paddle_billing")
	m.ReplayCaptured("paddle_billing")
	m.ReplayCaptureFailure("insert")
	m.ReplayCaptureFailure("update")
	m.ReplayCaptureFailure("update")

	err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_replay_captured_total Webhook events captured into the replay store.
# TYPE hookway_replay_captured_total counter
hookway_replay_captured_total{endpoint="paddle_billing"} 2
# HELP hookway_replay_capture_failures_total Replay store capture/outcome-update failures (including recovered panics), by operation.
# TYPE hookway_replay_capture_failures_total counter
hookway_replay_capture_failures_total{op="insert"} 1
hookway_replay_capture_failures_total{op="update"} 2
`),
		"hookway_replay_captured_total", "hookway_replay_capture_failures_total",
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
		RabbitMQUnroutable(destinationQueueID, queue string)
		ConfigLoadFailure(string)
	} = New()
	var _ = prometheus.Gauge(nil)
}
