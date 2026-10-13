package config

import (
	"strings"
	"testing"
	"time"
)

// temporalDestYAML builds a shared config with one temporal destination.
// destLines are extra indented lines under `temporal:`.
func temporalDestYAML(destLines string) string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  orchestrator:
    type: temporal
    temporal:
` + destLines + "\n"
}

const temporalMinimalDest = "      host_port: \"localhost:7233\"\n      namespace: \"default\""

// temporalRouteConfig builds a Config with one temporal destination and one
// endpoint route carrying the given temporal_options. Route-option validation
// is reachable only through validationIssues.
func temporalRouteConfig(target string, opts TemporalEndpointOptions) Config {
	return endpointConfigFor(
		map[string]DestinationConfig{"orchestrator": {Type: "temporal", Temporal: TemporalConfig{
			HostPort: "localhost:7233", Namespace: "default",
			ConnectTimeout: 10 * time.Second, StartTimeout: 5 * time.Second,
		}}},
		EndpointConfig{Enabled: true, Path: "/webhooks/events", Auth: AuthConfig{Type: "no_auth", Scheme: "no_auth"},
			Destinations: []EndpointDestinationConfig{{Destination: "orchestrator", Target: target, TemporalOptions: opts}}})
}

func TestLoadParsesTemporalDestination(t *testing.T) {
	t.Setenv("TEMPORAL_ADDRESS", "frontend.ecs-ht-temporal:7233")
	body := temporalDestYAML(`      host_port: "${TEMPORAL_ADDRESS}"
      namespace: "prod"
      api_key: "secret"
      connect_timeout: "3s"
      start_timeout: "2s"
      tls:
        enabled: true
        ca_cert: "/etc/hookway/ca.pem"
        client_cert: "/etc/hookway/c.pem"
        client_key: "/etc/hookway/k.pem"
        server_name: "temporal.internal"
        insecure_skip_verify: true`)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	tc := cfg.Destinations["orchestrator"].Temporal
	if tc.HostPort != "frontend.ecs-ht-temporal:7233" || tc.Namespace != "prod" || tc.APIKey != "secret" {
		t.Fatalf("bad connection fields: %#v", tc)
	}
	if tc.ConnectTimeout != 3*time.Second || tc.StartTimeout != 2*time.Second {
		t.Fatalf("bad timeouts: %v %v", tc.ConnectTimeout, tc.StartTimeout)
	}
	if !tc.TLS.Enabled || tc.TLS.CACert != "/etc/hookway/ca.pem" || tc.TLS.ClientCert != "/etc/hookway/c.pem" ||
		tc.TLS.ClientKey != "/etc/hookway/k.pem" || tc.TLS.ServerName != "temporal.internal" || !tc.TLS.InsecureSkipVerify {
		t.Fatalf("bad tls: %#v", tc.TLS)
	}
}

func TestLoadDefaultsTemporalTimeouts(t *testing.T) {
	cfg, err := loadSharedYAML(t, temporalDestYAML(temporalMinimalDest))
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	tc := cfg.Destinations["orchestrator"].Temporal
	if tc.ConnectTimeout != 10*time.Second {
		t.Fatalf("connect_timeout default: got %v want 10s", tc.ConnectTimeout)
	}
	if tc.StartTimeout != 5*time.Second {
		t.Fatalf("start_timeout default: got %v want 5s", tc.StartTimeout)
	}
	if tc.TLS.Enabled {
		t.Fatal("tls should default to disabled")
	}
}

func TestLoadRejectsInvalidTemporalDestination(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // substring of the field path in the error
	}{
		{
			name: "missing host_port",
			body: temporalDestYAML("      namespace: \"default\""),
			want: "destinations.orchestrator.temporal.host_port",
		},
		{
			name: "missing namespace",
			body: temporalDestYAML("      host_port: \"localhost:7233\""),
			want: "destinations.orchestrator.temporal.namespace",
		},
		{
			name: "api_key without tls",
			body: temporalDestYAML(temporalMinimalDest + "\n      api_key: \"k\"\n      tls:\n        enabled: false"),
			want: "destinations.orchestrator.temporal.api_key",
		},
		{
			name: "half mTLS",
			body: temporalDestYAML(temporalMinimalDest + "\n      tls:\n        enabled: true\n        client_cert: \"/c.pem\""),
			want: "destinations.orchestrator.temporal.tls.client_cert",
		},
		{
			name: "bad connect_timeout",
			body: temporalDestYAML(temporalMinimalDest + "\n      connect_timeout: \"soon\""),
			want: "destinations.orchestrator.temporal.connect_timeout",
		},
		{
			name: "zero start_timeout",
			body: temporalDestYAML(temporalMinimalDest + "\n      start_timeout: \"0s\""),
			want: "destinations.orchestrator.temporal.start_timeout",
		},
		{
			name: "webhook dead_letter to temporal",
			body: webhookWithTemporalDeadLetterBody(),
			want: "webhook.dead_letter.destination",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadSharedYAML(t, tc.body)
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateTemporalRouteOptions(t *testing.T) {
	minimal := TemporalEndpointOptions{WorkflowType: "OrderWorkflow"}
	with := func(mutate func(*TemporalEndpointOptions)) TemporalEndpointOptions {
		o := minimal
		mutate(&o)
		return o
	}
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"missing workflow_type", temporalRouteConfig("order-events", TemporalEndpointOptions{Input: "body"}), "temporal_options.workflow_type"},
		{"no temporal_options at all", temporalRouteConfig("order-events", TemporalEndpointOptions{}), "temporal_options.workflow_type"},
		{"bad input", temporalRouteConfig("order-events", with(func(o *TemporalEndpointOptions) { o.Input = "headers" })), "temporal_options.input"},
		{"workflow_id static without value", temporalRouteConfig("order-events", with(func(o *TemporalEndpointOptions) {
			o.WorkflowID = TemporalWorkflowIDConfig{Source: "static"}
		})), "temporal_options.workflow_id.value"},
		{"workflow_id body without path", temporalRouteConfig("order-events", with(func(o *TemporalEndpointOptions) {
			o.WorkflowID = TemporalWorkflowIDConfig{Source: "body"}
		})), "temporal_options.workflow_id.path"},
		{"workflow_id bad source", temporalRouteConfig("order-events", with(func(o *TemporalEndpointOptions) {
			o.WorkflowID = TemporalWorkflowIDConfig{Source: "random"}
		})), "temporal_options.workflow_id.source"},
		{"bad id_reuse_policy", temporalRouteConfig("order-events", with(func(o *TemporalEndpointOptions) {
			o.IDReusePolicy = "whatever"
		})), "temporal_options.id_reuse_policy"},
		{"run_timeout exceeds execution_timeout", temporalRouteConfig("order-events", with(func(o *TemporalEndpointOptions) {
			o.ExecutionTimeout, o.RunTimeout = time.Hour, 2*time.Hour
		})), "temporal_options.run_timeout"},
		{"fifo target", temporalRouteConfig("order-events.fifo", minimal), "target"},
		{"temporal_options on non-temporal dest", endpointConfigFor(
			map[string]DestinationConfig{"events": {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}}},
			EndpointConfig{Enabled: true, Path: "/webhooks/events", Auth: AuthConfig{Type: "no_auth", Scheme: "no_auth"},
				Destinations: []EndpointDestinationConfig{{Destination: "events", Target: "billing-events", TemporalOptions: minimal}}}),
			"temporal_options"},
		{"shared target with differing options", temporalSharedTargetConfig("OrderWorkflow", "OtherWorkflow"), "temporal_options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ConfigValidationError{Issues: validationIssues(tc.cfg)}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateAcceptsTemporalSharedTargetWithSameOptions(t *testing.T) {
	if issues := validationIssues(temporalSharedTargetConfig("OrderWorkflow", "OrderWorkflow")); len(issues) != 0 {
		t.Fatalf("shared temporal target with identical options rejected: %#v", issues)
	}
}

func TestValidateAcceptsFullTemporalRouteOptions(t *testing.T) {
	cfg := temporalRouteConfig("order-events", TemporalEndpointOptions{
		WorkflowType:     "OrderWorkflow",
		Input:            "body",
		WorkflowID:       TemporalWorkflowIDConfig{Source: "body", Path: "order.id", Prefix: "order-"},
		IDReusePolicy:    "reject_duplicate",
		ExecutionTimeout: 24 * time.Hour, RunTimeout: time.Hour, TaskTimeout: 10 * time.Second,
	})
	if issues := validationIssues(cfg); len(issues) != 0 {
		t.Fatalf("valid temporal_options rejected: %#v", issues)
	}
}

func webhookWithTemporalDeadLetterBody() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  orchestrator:
    type: temporal
    temporal:
      host_port: "localhost:7233"
      namespace: "default"
  downstream:
    type: webhook
    webhook:
      url: "https://example.com/hooks"
      delivery_mode: "async"
      dead_letter:
        destination: "orchestrator"
        target: "dlq"
`
}

func temporalSharedTargetConfig(typeA, typeB string) Config {
	route := func(workflowType string) []EndpointDestinationConfig {
		return []EndpointDestinationConfig{{
			Destination: "orchestrator", Target: "order-events",
			TemporalOptions: TemporalEndpointOptions{WorkflowType: workflowType},
		}}
	}
	return Config{
		Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: map[string]DestinationConfig{"orchestrator": {Type: "temporal", Temporal: TemporalConfig{
			HostPort: "localhost:7233", Namespace: "default",
			ConnectTimeout: 10 * time.Second, StartTimeout: 5 * time.Second,
		}}},
		Endpoints: map[string]EndpointConfig{
			"a": {Enabled: true, Path: "/webhooks/a", Auth: AuthConfig{Type: "no_auth", Scheme: "no_auth"}, Destinations: route(typeA)},
			"b": {Enabled: true, Path: "/webhooks/b", Auth: AuthConfig{Type: "no_auth", Scheme: "no_auth"}, Destinations: route(typeB)},
		},
	}
}
