package config

import (
	"strings"
	"testing"
	"time"
)

func temporalConfigBody(destLines, routeLines string) string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  orchestrator:
    type: temporal
    temporal:
` + destLines + `
endpoints:
  events:
    enabled: true
    path: "/webhooks/events"
    destinations:
      - destination: "orchestrator"
        target: "order-events"
` + routeLines + `
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}

const temporalMinimalDest = "      host_port: \"localhost:7233\"\n      namespace: \"default\""
const temporalMinimalRoute = "        temporal_options:\n          workflow_type: \"OrderWorkflow\""

func TestLoadParsesTemporalDestination(t *testing.T) {
	t.Setenv("TEMPORAL_ADDRESS", "frontend.ecs-ht-temporal:7233")
	body := temporalConfigBody(`      host_port: "${TEMPORAL_ADDRESS}"
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
        insecure_skip_verify: true`, `        temporal_options:
          workflow_type: "OrderWorkflow"
          input: "body"
          workflow_id:
            source: "body"
            path: "order.id"
            prefix: "order-"
          id_reuse_policy: "reject_duplicate"
          execution_timeout: "24h"
          run_timeout: "1h"
          task_timeout: "10s"`)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
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
	route := cfg.Endpoints["events"].Destinations[0]
	opts := route.TemporalOptions
	if opts.WorkflowType != "OrderWorkflow" || opts.Input != "body" || opts.IDReusePolicy != "reject_duplicate" {
		t.Fatalf("bad route options: %#v", opts)
	}
	if opts.WorkflowID != (TemporalWorkflowIDConfig{Source: "body", Path: "order.id", Prefix: "order-"}) {
		t.Fatalf("bad workflow_id: %#v", opts.WorkflowID)
	}
	if opts.ExecutionTimeout != 24*time.Hour || opts.RunTimeout != time.Hour || opts.TaskTimeout != 10*time.Second {
		t.Fatalf("bad route timeouts: %#v", opts)
	}
}

func TestLoadDefaultsTemporalTimeouts(t *testing.T) {
	cfg, err := Load(writeConfig(t, temporalConfigBody(temporalMinimalDest, temporalMinimalRoute)))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
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
	opts := cfg.Endpoints["events"].Destinations[0].TemporalOptions
	if opts.Input != "" || opts.IDReusePolicy != "" || opts.WorkflowID != (TemporalWorkflowIDConfig{}) {
		t.Fatalf("route options should stay zero when omitted: %#v", opts)
	}
}

func TestLoadRejectsInvalidTemporal(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // substring of the field path in the error
	}{
		{
			name: "missing host_port",
			body: temporalConfigBody("      namespace: \"default\"", temporalMinimalRoute),
			want: "destinations.orchestrator.temporal.host_port",
		},
		{
			name: "missing namespace",
			body: temporalConfigBody("      host_port: \"localhost:7233\"", temporalMinimalRoute),
			want: "destinations.orchestrator.temporal.namespace",
		},
		{
			name: "api_key without tls",
			body: temporalConfigBody(temporalMinimalDest+"\n      api_key: \"k\"\n      tls:\n        enabled: false", temporalMinimalRoute),
			want: "destinations.orchestrator.temporal.api_key",
		},
		{
			name: "half mTLS",
			body: temporalConfigBody(temporalMinimalDest+"\n      tls:\n        enabled: true\n        client_cert: \"/c.pem\"", temporalMinimalRoute),
			want: "destinations.orchestrator.temporal.tls.client_cert",
		},
		{
			name: "bad connect_timeout",
			body: temporalConfigBody(temporalMinimalDest+"\n      connect_timeout: \"soon\"", temporalMinimalRoute),
			want: "destinations.orchestrator.temporal.connect_timeout",
		},
		{
			name: "zero start_timeout",
			body: temporalConfigBody(temporalMinimalDest+"\n      start_timeout: \"0s\"", temporalMinimalRoute),
			want: "destinations.orchestrator.temporal.start_timeout",
		},
		{
			name: "missing workflow_type",
			body: temporalConfigBody(temporalMinimalDest, "        temporal_options:\n          input: \"body\""),
			want: "temporal_options.workflow_type",
		},
		{
			name: "no temporal_options at all",
			body: temporalConfigBody(temporalMinimalDest, ""),
			want: "temporal_options.workflow_type",
		},
		{
			name: "bad input",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          input: \"headers\""),
			want: "temporal_options.input",
		},
		{
			name: "workflow_id static without value",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          workflow_id:\n            source: \"static\""),
			want: "temporal_options.workflow_id.value",
		},
		{
			name: "workflow_id body without path",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          workflow_id:\n            source: \"body\""),
			want: "temporal_options.workflow_id.path",
		},
		{
			name: "workflow_id bad source",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          workflow_id:\n            source: \"random\""),
			want: "temporal_options.workflow_id.source",
		},
		{
			name: "bad id_reuse_policy",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          id_reuse_policy: \"whatever\""),
			want: "temporal_options.id_reuse_policy",
		},
		{
			name: "run_timeout exceeds execution_timeout",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          execution_timeout: \"1h\"\n          run_timeout: \"2h\""),
			want: "temporal_options.run_timeout",
		},
		{
			name: "bad execution_timeout",
			body: temporalConfigBody(temporalMinimalDest, temporalMinimalRoute+"\n          execution_timeout: \"-1h\""),
			want: "temporal_options.execution_timeout",
		},
		{
			name: "temporal_options on non-temporal dest",
			body: rabbitWithTemporalOptionsBody(),
			want: "temporal_options",
		},
		{
			name: "fifo target",
			body: strings.Replace(temporalConfigBody(temporalMinimalDest, temporalMinimalRoute), "order-events", "order-events.fifo", 1),
			want: "target",
		},
		{
			name: "webhook dead_letter to temporal",
			body: webhookWithTemporalDeadLetterBody(),
			want: "webhook.dead_letter.destination",
		},
		{
			name: "shared target with differing options",
			body: temporalSharedTargetBody("OrderWorkflow", "OtherWorkflow"),
			want: "temporal_options",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadAcceptsTemporalSharedTargetWithSameOptions(t *testing.T) {
	if _, err := Load(writeConfig(t, temporalSharedTargetBody("OrderWorkflow", "OrderWorkflow"))); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
}

func rabbitWithTemporalOptionsBody() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  events:
    type: rabbitmq
    rabbitmq:
      url: "amqp://x"
      exchange: "e"
      exchange_type: "direct"
endpoints:
  events:
    enabled: true
    path: "/webhooks/events"
    destinations:
      - destination: "events"
        target: "billing-events"
        temporal_options:
          workflow_type: "OrderWorkflow"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
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
endpoints:
  events:
    enabled: true
    path: "/webhooks/events"
    destinations:
      - destination: "downstream"
        target: "hooks"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}

func temporalSharedTargetBody(typeA, typeB string) string {
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
endpoints:
  a:
    enabled: true
    path: "/webhooks/a"
    destinations:
      - destination: "orchestrator"
        target: "order-events"
        temporal_options:
          workflow_type: "` + typeA + `"
    auth:
      type: "no_auth"
      scheme: "no_auth"
  b:
    enabled: true
    path: "/webhooks/b"
    destinations:
      - destination: "orchestrator"
        target: "order-events"
        temporal_options:
          workflow_type: "` + typeB + `"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}
