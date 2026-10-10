package config

import (
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/egress"
	"github.com/jaysongiroux/hook_way/internal/filter"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

// targetLiteral constrains the literal (non-placeholder) text of a templated
// route target; the rendered whole must additionally satisfy safeName.
var targetLiteral = regexp.MustCompile(`^[a-zA-Z0-9._-]*$`)

// pgIdentifier matches a safe, unquoted-style SQL identifier for schema/table
// names. They are also quoted via pgx.Identifier.Sanitize at use, but we reject
// anything outside this character set at config time as defense in depth.
var pgIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type ValidationIssue struct {
	Source     string
	Field      string
	Message    string
	Suggestion string
	Severity   string
}

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// EffectiveSeverity preserves the historical meaning of a zero-value issue:
// validation issues are errors unless a caller explicitly marks one as
// a warning.
func (i ValidationIssue) EffectiveSeverity() string {
	if i.Severity == "" {
		return SeverityError
	}
	return i.Severity
}

type ConfigValidationError struct {
	Issues []ValidationIssue
}

func (e ConfigValidationError) Error() string {
	var b strings.Builder
	b.WriteString("config validation failed")
	for _, issue := range e.Issues {
		b.WriteString("\n- ")
		if issue.Source != "" {
			b.WriteString(issue.Source)
			b.WriteString(": ")
		}
		b.WriteString(issue.Field)
		b.WriteString(": ")
		b.WriteString(issue.Message)
		if issue.Suggestion != "" {
			b.WriteString(" Suggestion: ")
			b.WriteString(issue.Suggestion)
		}
	}
	return b.String()
}

func (e ConfigValidationError) Is(target error) bool {
	_, ok := target.(ConfigValidationError)
	return ok
}

func validationError(field, message, suggestion string) ValidationIssue {
	return ValidationIssue{Field: field, Message: message, Suggestion: suggestion}
}

// ValidationIssues validates only process-scoped configuration. The shared
// contract allows no destinations because a flow may only respond to its caller.
func (s SharedConfig) ValidationIssues() []ValidationIssue {
	return normalizeIssueSeverities(remapConnectionIssues(s.ConnectionOf, validationIssues(s.asConfig())))
}

// remapConnectionIssues re-points issues at the YAML path the operator wrote.
// validationIssues runs against the post-merge config, so a value supplied by
// a connection is reported as "destinations.events.postgres.url" even though
// the file says "connections.pg_main.url".
//
// This is not cosmetic. configapi.ValidateWithEnv downgrades a structural
// error to a warning when the field's value still contains an unresolved
// "${", and it finds that value by walking the issue's dotted path through the
// parsed YAML. A path that does not exist in the document resolves to nothing,
// no downgrade happens, and `hookway validate --json` hard-fails in CI for
// every connection-based config whose secrets are not exported -- which is
// precisely the workflow that contract exists to support.
func remapConnectionIssues(connectionOf map[string]ConnectionRef, issues []ValidationIssue) []ValidationIssue {
	if len(connectionOf) == 0 {
		return issues
	}
	for i, issue := range issues {
		owner, rest, ok := splitOwnerPath(issue.Field)
		if !ok {
			continue
		}
		ref, bound := connectionOf[owner]
		if !bound {
			continue
		}
		// rest is "<block>.<path>"; drop the block, because a connection is
		// authored flat rather than under a per-type block.
		_, path, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		// Only connection-owned fields move. A sink field such as
		// destinations.events.postgres.table stays where the operator wrote it.
		if _, owned := ownedConnPaths(ref.Type)[path]; !owned {
			continue
		}
		issues[i].Field = joinYAMLPath(joinYAMLPath("connections", ref.Name), path)
		issues[i].Message = issue.Message + fmt.Sprintf(" (via %s.connection)", owner)
	}
	return issues
}

// splitOwnerPath splits an issue field into the owner whose connection binding
// applies ("destinations.events") and the remainder.
func splitOwnerPath(field string) (owner, rest string, ok bool) {
	after, found := strings.CutPrefix(field, "destinations.")
	if !found {
		return "", "", false
	}
	id, rest, found := strings.Cut(after, ".")
	if !found {
		return "", "", false
	}
	return "destinations." + id, rest, true
}

func validationIssues(c Config) []ValidationIssue {
	var issues []ValidationIssue
	if c.Server.Addr == "" {
		issues = append(issues, validationError("server.addr", "listen address is required", "set server.addr to a host:port such as \":8080\""))
	}
	if c.Server.MaxBodyBytes <= 0 {
		issues = append(issues, validationError("server.max_body_bytes", "must be a positive integer", "set a safe byte limit, for example 1048576"))
	}
	if c.Server.MaxInflightRequests < maxInflightRequestsUnlimited {
		issues = append(issues, validationError("server.max_inflight_requests", "must be -1 (unlimited) or a positive integer", fmt.Sprintf("omit the field or set 0 to use the default of %d, or -1 to disable the limit", defaultMaxInflightRequests)))
	}
	if c.Server.MaxPendingRequests < 0 || c.Server.AdmissionWaitTimeout < 0 || (c.Server.MaxPendingRequests > 0) != (c.Server.AdmissionWaitTimeout > 0) {
		issues = append(issues, validationError("server.max_pending_requests", "waiting requires a positive max_pending_requests and admission_wait_timeout together", "omit both for immediate overload rejection"))
	}
	if c.Server.MaxPendingRequests > 0 && c.Server.MaxInflightRequests <= 0 {
		issues = append(issues, validationError("server.max_pending_requests", "waiting requires a finite max_inflight_requests", "set an execution limit such as 256"))
	}
	if c.Server.AdmissionWaitTimeout > 0 && (c.Server.AdmissionWaitTimeout >= c.Server.ReadTimeout || c.Server.AdmissionWaitTimeout >= c.Server.WriteTimeout || c.Server.AdmissionWaitTimeout >= c.Server.ShutdownTimeout) {
		issues = append(issues, validationError("server.admission_wait_timeout", "must be shorter than read, write and shutdown timeouts", "reserve time after waiting to read, process and respond"))
	}
	if c.Server.ShutdownDelay < 0 {
		issues = append(issues, validationError("server.shutdown_delay", "must be zero or a positive duration", "use zero for immediate drain or a delay such as 5s for load-balancer withdrawal"))
	}
	if c.Server.ShutdownTotalTimeout < 0 || (c.Server.ShutdownTotalTimeout > 0 && (c.Server.ShutdownDelay >= c.Server.ShutdownTotalTimeout || c.Server.ShutdownTimeout >= c.Server.ShutdownTotalTimeout-c.Server.ShutdownDelay)) {
		issues = append(issues, validationError("server.shutdown_total_timeout", "must exceed shutdown_delay plus shutdown_timeout", "reserve additional time for replay redrives, sequential asynchronous publisher drains, and replay capture cleanup"))
	}
	if c.Server.FanoutConcurrency < 0 {
		issues = append(issues, validationError("server.fanout_concurrency", "must be zero (default) or a positive integer", "set a bound such as 8"))
	}
	if c.Server.PublishTimeout > c.Server.ShutdownTimeout {
		issues = append(issues, validationError(
			"server.publish_timeout",
			fmt.Sprintf("must be less than or equal to server.shutdown_timeout (publish_timeout=%s, shutdown_timeout=%s)", c.Server.PublishTimeout, c.Server.ShutdownTimeout),
			"a synchronous publish that outlives shutdown_timeout can make an otherwise clean SIGTERM shutdown time out and report failure; lower server.publish_timeout or raise server.shutdown_timeout",
		))
	}
	if c.Server.PublishTimeout > c.Server.WriteTimeout {
		issues = append(issues, validationError(
			"server.publish_timeout",
			fmt.Sprintf("must be less than or equal to server.write_timeout (publish_timeout=%s, write_timeout=%s)", c.Server.PublishTimeout, c.Server.WriteTimeout),
			"a synchronous webhook response can't be written after write_timeout elapses, so a publish that runs longer causes the sender to see a spurious timeout and retry; lower server.publish_timeout or raise server.write_timeout",
		))
	}
	for i, header := range c.Security.RedactedHeaderDenylist {
		if !safeHeaderName.MatchString(header) {
			issues = append(issues, validationError(fmt.Sprintf("security.redacted_header_denylist[%d]", i), "header name is empty or invalid", "use HTTP header names such as \"Authorization\" or \"X-Signature\""))
		}
	}
	for i, header := range c.Security.ForwardedHeaderAllowlist {
		if !safeHeaderName.MatchString(header) {
			issues = append(issues, validationError(fmt.Sprintf("security.forwarded_header_allowlist[%d]", i), "header name is empty or invalid", "use HTTP header names such as \"X-Request-ID\" or \"Content-Type\""))
		}
	}
	for i, header := range c.Logging.HeaderAllowlist {
		if !safeHeaderName.MatchString(header) {
			issues = append(issues, validationError(fmt.Sprintf("logging.header_allowlist[%d]", i), "header name is empty or invalid", "use HTTP header names such as \"X-Request-ID\" or \"Content-Type\""))
		}
	}
	for i, entry := range c.Security.TrustedProxies {
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				issues = append(issues, validationError(fmt.Sprintf("security.trusted_proxies[%d]", i), "must be an IP address or CIDR range", "use values such as \"10.0.0.0/8\" or \"203.0.113.10\""))
			}
		}
	}
	if c.Security.LoopProtection.MaxHops < 0 {
		issues = append(issues, validationError("security.loop_protection.max_hops", "must be zero (use the default) or a positive integer", "set a small ceiling such as 3, or omit to use the default"))
	}
	for id, dest := range c.Destinations {
		fieldPrefix := "destinations." + id
		if !safeName.MatchString(id) {
			issues = append(issues, validationError(fieldPrefix, "destination id contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if dest.Type == "" {
			issues = append(issues, validationError(fieldPrefix+".type", "destination backend type is required", "set type to \"rabbitmq\", \"sqs\", \"sns\", \"s3\", \"lambda\", \"postgres\", \"mongodb\", \"kafka\", \"redis_stream\", \"redis_list\", \"redis_pubsub\", \"redis_kv\", \"webhook\", or \"temporal\""))
		} else if dest.Type != "rabbitmq" && dest.Type != "sqs" && dest.Type != "sns" && dest.Type != "s3" && dest.Type != "lambda" && dest.Type != "postgres" && dest.Type != "mongodb" && dest.Type != "kafka" &&
			dest.Type != "redis_stream" && dest.Type != "redis_list" && dest.Type != "redis_pubsub" && dest.Type != "redis_kv" && dest.Type != "webhook" && dest.Type != "temporal" {
			issues = append(issues, validationError(fieldPrefix+".type", fmt.Sprintf("%q is not supported", dest.Type), "supported values are \"rabbitmq\", \"sqs\", \"sns\", \"s3\", \"lambda\", \"postgres\", \"mongodb\", \"kafka\", \"redis_stream\", \"redis_list\", \"redis_pubsub\", \"redis_kv\", \"webhook\", and \"temporal\""))
		}
		if dest.Type == "sqs" {
			if dest.SQS.Region == "" {
				issues = append(issues, validationError(fieldPrefix+".sqs.region", "region is required for sqs destinations", "set region to an AWS region such as \"us-east-1\""))
			}
		}
		if dest.Type == "sns" {
			if dest.SNS.Region == "" {
				issues = append(issues, validationError(fieldPrefix+".sns.region", "region is required for sns destinations", "set region to an AWS region such as \"us-east-1\""))
			}
		}
		if dest.Type == "lambda" {
			if dest.Lambda.Region == "" {
				issues = append(issues, validationError(fieldPrefix+".lambda.region", "region is required for lambda destinations", "set region to an AWS region such as \"us-east-1\""))
			}
			switch dest.Lambda.InvocationType {
			case "", "Event", "RequestResponse":
				// ok
			default:
				issues = append(issues, validationError(fieldPrefix+".lambda.invocation_type", fmt.Sprintf("%q is not supported", dest.Lambda.InvocationType), "use \"Event\" or \"RequestResponse\", or omit for Event"))
			}
		}
		if dest.Type == "s3" {
			if dest.S3.Region == "" {
				issues = append(issues, validationError(fieldPrefix+".s3.region", "region is required for s3 destinations", "set region to an AWS region such as \"us-east-1\""))
			}
			if dest.S3.Bucket == "" {
				issues = append(issues, validationError(fieldPrefix+".s3.bucket", "bucket is required for s3 destinations", "set bucket to the target S3 bucket name"))
			}
			switch dest.S3.SSE {
			case "", "AES256", "aws:kms":
				// ok
			default:
				issues = append(issues, validationError(fieldPrefix+".s3.sse", fmt.Sprintf("%q is not supported", dest.S3.SSE), "use \"AES256\" or \"aws:kms\", or omit for no server-side encryption"))
			}
		}
		if dest.Type == "postgres" {
			pg := dest.Postgres
			if pg.URL == "" {
				issues = append(issues, validationError(fieldPrefix+".postgres.url", "url is required for postgres destinations", "set url to a libpq DSN such as \"postgres://user:pass@host:5432/db?sslmode=require\""))
			}
			if pg.Table == "" {
				issues = append(issues, validationError(fieldPrefix+".postgres.table", "table is required for postgres destinations", "set table to the destination table name, e.g. \"webhook_events\""))
			}
			issues = append(issues, postgresConnValidationIssues(fieldPrefix+".postgres", pg)...)
		}
		if dest.Type == "mongodb" {
			m := dest.Mongo
			if m.URI == "" {
				issues = append(issues, validationError(fieldPrefix+".mongodb.uri", "uri is required for mongodb destinations", "set uri to a mongodb connection string such as \"mongodb://host:27017\""))
			}
			if m.Database == "" {
				issues = append(issues, validationError(fieldPrefix+".mongodb.database", "database is required for mongodb destinations", "set database to the target Mongo database name"))
			} else if strings.ContainsAny(m.Database, "/\\.\"$*<>:|? ") {
				issues = append(issues, validationError(fieldPrefix+".mongodb.database", "database name contains characters not allowed by MongoDB", "remove any of / \\ . \" $ * < > : | ? or spaces"))
			}
			if (m.TLS.ClientCert == "") != (m.TLS.ClientKey == "") {
				issues = append(issues, validationError(fieldPrefix+".mongodb.tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
			}
		}
		if dest.Type == "webhook" {
			w := dest.Webhook
			if authority, err := vars.Parse(urlAuthority(w.URL)); err == nil && w.AllowPrivateTargets && !authority.HasPlaceholders() {
				issues = append(issues, validationError(fieldPrefix+".webhook.allow_private_targets", "has no effect unless the url host contains a {variable}", "omit this setting for literal operator-configured hosts; they already support local/private targets"))
			}
			if w.URL == "" {
				issues = append(issues, validationError(fieldPrefix+".webhook.url", "url is required for webhook destinations", "set url to an http(s) endpoint such as \"https://api.example.com/hooks\""))
			} else if !strings.HasPrefix(w.URL, "http://") && !strings.HasPrefix(w.URL, "https://") {
				issues = append(issues, validationError(fieldPrefix+".webhook.url", "url must be http or https", "use a URL beginning with http:// or https://"))
			} else {
				issues = append(issues, webhookURLTemplateIssues(fieldPrefix+".webhook.url", w.URL)...)
			}
			switch w.Auth.Type {
			case "", "none", "proxy":
				// ok, no extra fields
			case "api_key":
				if w.Auth.Header == "" {
					issues = append(issues, validationError(fieldPrefix+".webhook.auth.header", "header is required for api_key auth", "set auth.header, e.g. \"Authorization\""))
				}
				if w.Auth.Value == "" {
					issues = append(issues, validationError(fieldPrefix+".webhook.auth.value", "value is required for api_key auth", "set auth.value, e.g. \"Bearer ${TOKEN}\""))
				}
			case "hmac":
				if w.Auth.Secret == "" {
					issues = append(issues, validationError(fieldPrefix+".webhook.auth.secret", "secret is required for hmac auth", "set auth.secret to a shared signing secret"))
				}
				switch w.Auth.Scheme {
				case "", "hmac_sha256_ts_dot", "hmac_sha256_ts_colon", "hmac_sha256_hex":
					// ok
				default:
					issues = append(issues, validationError(fieldPrefix+".webhook.auth.scheme", fmt.Sprintf("%q is not supported", w.Auth.Scheme), "use \"hmac_sha256_ts_dot\" (default), \"hmac_sha256_ts_colon\", or \"hmac_sha256_hex\""))
				}
			default:
				issues = append(issues, validationError(fieldPrefix+".webhook.auth.type", fmt.Sprintf("%q is not supported", w.Auth.Type), "use \"none\", \"api_key\", \"hmac\", or \"proxy\""))
			}
			if w.MaxQueue < 0 {
				issues = append(issues, validationError(fieldPrefix+".webhook.max_queue", "must be zero (default) or a positive integer", "set a buffer size such as 1000"))
			}
			switch w.DeliveryMode {
			case "", "sync", "async":
				// ok
			default:
				issues = append(issues, validationError(fieldPrefix+".webhook.delivery_mode", fmt.Sprintf("%q is not supported", w.DeliveryMode), "use \"sync\" or \"async\", or omit for async"))
			}
			dlSet := w.DeadLetter.Destination != "" || w.DeadLetter.Target != ""
			if dlSet {
				if w.DeliveryMode == "sync" {
					issues = append(issues, validationError(fieldPrefix+".webhook.dead_letter", "dead_letter only applies to async delivery", "remove dead_letter or set delivery_mode to \"async\""))
				}
				if (w.DeadLetter.Destination == "") != (w.DeadLetter.Target == "") {
					issues = append(issues, validationError(fieldPrefix+".webhook.dead_letter", "dead_letter.destination and dead_letter.target must both be set", "set both, or remove the dead_letter block"))
				}
				if w.DeadLetter.Destination != "" {
					dlq, ok := c.Destinations[w.DeadLetter.Destination]
					if !ok {
						issues = append(issues, validationError(fieldPrefix+".webhook.dead_letter.destination", fmt.Sprintf("references unknown destination %q", w.DeadLetter.Destination), "set dead_letter.destination to the id of a configured non-webhook destination"))
					} else if dlq.Type == "webhook" {
						issues = append(issues, validationError(fieldPrefix+".webhook.dead_letter.destination", "dead_letter destination must not be a webhook destination", "point dead_letter.destination at a durable backend such as a rabbitmq, sqs, or s3 destination"))
					} else if dlq.Type == "temporal" {
						issues = append(issues, validationError(fieldPrefix+".webhook.dead_letter.destination", "dead_letter destination must not be a temporal destination (a dead-letter route has no workflow_type)", "point dead_letter.destination at a durable backend such as a rabbitmq, sqs, or s3 destination"))
					}
				}
			}
			if (w.TLS.ClientCert == "") != (w.TLS.ClientKey == "") {
				issues = append(issues, validationError(fieldPrefix+".webhook.tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
			}
		}
		if dest.Type == "kafka" {
			k := dest.Kafka
			if len(k.Brokers) == 0 {
				issues = append(issues, validationError(fieldPrefix+".kafka.brokers", "at least one broker is required for kafka destinations", "set brokers to a list such as [\"broker-1:9092\"]"))
			}
			switch k.Acks {
			case "", "all", "one", "none":
			default:
				issues = append(issues, validationError(fieldPrefix+".kafka.acks", fmt.Sprintf("%q is not supported", k.Acks), "use \"all\", \"one\", or \"none\""))
			}
			switch k.Compression {
			case "", "none", "gzip", "snappy", "lz4", "zstd":
			default:
				issues = append(issues, validationError(fieldPrefix+".kafka.compression", fmt.Sprintf("%q is not supported", k.Compression), "use \"none\", \"gzip\", \"snappy\", \"lz4\", or \"zstd\""))
			}
			switch k.SASL.Mechanism {
			case "", "none":
			case "plain", "scram-sha-256", "scram-sha-512":
				if k.SASL.Username == "" || k.SASL.Password == "" {
					issues = append(issues, validationError(fieldPrefix+".kafka.sasl", "username and password are required when a sasl mechanism is set", "set kafka.sasl.username and kafka.sasl.password, e.g. from ${KAFKA_USERNAME}/${KAFKA_PASSWORD}"))
				}
			default:
				issues = append(issues, validationError(fieldPrefix+".kafka.sasl.mechanism", fmt.Sprintf("%q is not supported", k.SASL.Mechanism), "use \"plain\", \"scram-sha-256\", or \"scram-sha-512\", or omit for no SASL"))
			}
			if (k.TLS.ClientCert == "") != (k.TLS.ClientKey == "") {
				issues = append(issues, validationError(fieldPrefix+".kafka.tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
			}
			if k.Retry.MaxAttempts < 0 {
				issues = append(issues, validationError(fieldPrefix+".kafka.retry.max_attempts", "must be zero (default) or a positive integer", "set a value such as 3"))
			}
		}
		if dest.Type == "temporal" {
			tc := dest.Temporal
			if tc.HostPort == "" {
				issues = append(issues, validationError(fieldPrefix+".temporal.host_port", "host_port is required for temporal destinations", "set host_port to the frontend gRPC address such as \"localhost:7233\""))
			}
			if tc.Namespace == "" {
				issues = append(issues, validationError(fieldPrefix+".temporal.namespace", "namespace is required for temporal destinations", "set namespace to the Temporal namespace such as \"default\""))
			}
			if tc.APIKey != "" && !tc.TLS.Enabled {
				issues = append(issues, validationError(fieldPrefix+".temporal.api_key", "api_key requires tls.enabled: true (the key is sent as a bearer credential)", "set temporal.tls.enabled: true, or remove api_key for an unauthenticated cluster"))
			}
			if (tc.TLS.ClientCert == "") != (tc.TLS.ClientKey == "") {
				issues = append(issues, validationError(fieldPrefix+".temporal.tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
			}
		}
		if dest.Type == "redis_stream" || dest.Type == "redis_list" || dest.Type == "redis_pubsub" || dest.Type == "redis_kv" {
			issues = append(issues, redisConfigValidationIssues(fieldPrefix+".redis", dest.Redis)...)
		}
		if dest.Type == "rabbitmq" || dest.Type == "" {
			rabbitPrefix := fieldPrefix + ".rabbitmq"
			if dest.RabbitMQ.URL == "" {
				issues = append(issues, validationError(rabbitPrefix+".url", "AMQP URL is required", "set url to ${RABBITMQ_URL} and export RABBITMQ_URL"))
			}
			if dest.RabbitMQ.Exchange == "" {
				issues = append(issues, validationError(rabbitPrefix+".exchange", "exchange name is required", "set a direct exchange such as \"hookway.webhooks\""))
			}
			if dest.RabbitMQ.ExchangeType == "" {
				issues = append(issues, validationError(rabbitPrefix+".exchange_type", "exchange type is required", "set exchange_type to \"direct\""))
			}
			if dest.RabbitMQ.PublisherChannels < 0 {
				issues = append(issues, validationError(rabbitPrefix+".publisher_channels", "must be zero (default) or a positive integer", "set publish concurrency such as 8"))
			}
			issues = append(issues, queueOptionValidationIssues(rabbitPrefix+".queue_defaults", dest.RabbitMQ.QueueDefaults)...)
			retry := dest.RabbitMQ.Retry
			if retry.MaxAttempts < 0 {
				issues = append(issues, validationError(rabbitPrefix+".retry.max_attempts", "must be zero (default) or a positive integer", "set a value such as 3"))
			}
			if retry.InitialBackoff < 0 {
				issues = append(issues, validationError(rabbitPrefix+".retry.initial_backoff", "must be zero (default) or a positive duration", "set a value such as \"100ms\""))
			}
			if retry.MaxBackoff < 0 {
				issues = append(issues, validationError(rabbitPrefix+".retry.max_backoff", "must be zero (default) or a positive duration", "set a value such as \"2s\""))
			}
			if retry.InitialBackoff > 0 && retry.MaxBackoff > 0 && retry.InitialBackoff > retry.MaxBackoff {
				issues = append(issues, validationError(rabbitPrefix+".retry.initial_backoff", "must be less than or equal to retry.max_backoff", "lower initial_backoff or raise max_backoff"))
			}
			tls := dest.RabbitMQ.TLS
			if (tls.ClientCert == "") != (tls.ClientKey == "") {
				issues = append(issues, validationError(rabbitPrefix+".tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
			}
			if dest.RabbitMQ.DeadLetter.Enabled {
				if !safeName.MatchString(dest.RabbitMQ.DeadLetter.Exchange) {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.exchange", "exchange name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
				if dest.RabbitMQ.DeadLetter.ExchangeType == "" {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.exchange_type", "exchange type is required", "set exchange_type to \"direct\""))
				}
				if dest.RabbitMQ.DeadLetter.CreateQueue && !safeName.MatchString(dest.RabbitMQ.DeadLetter.Queue) {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.queue", "queue name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
				if dest.RabbitMQ.DeadLetter.RoutingKey == "" {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.routing_key", "routing key is required", "set a routing key such as \"webhooks.dead\""))
				}
			}
		}
	}
	if c.Metrics.Enabled && (c.Metrics.Path == "" || c.Metrics.Path[0] != '/') {
		issues = append(issues, validationError("metrics.path", "must start with / when metrics are enabled", "set metrics.path to \"/metrics\""))
	}
	switch c.Logging.Level {
	case "", "debug", "info", "warn", "error":
		// ok
	default:
		issues = append(issues, validationError("logging.level", fmt.Sprintf("%q is not supported", c.Logging.Level), "use \"debug\", \"info\", \"warn\", or \"error\", or omit for the default of \"info\""))
	}
	var endpointPaths []compiledEndpointPath
	queueOptions := map[string]RabbitMQQueueOptions{}
	sqsTargetSettings := map[string]sqsRouteSettings{}
	snsTargetSettings := map[string]snsRouteSettings{}
	temporalTargetSettings := map[string]TemporalEndpointOptions{}
	endpointNames := make([]string, 0, len(c.Endpoints))
	for name := range c.Endpoints {
		endpointNames = append(endpointNames, name)
	}
	sort.Strings(endpointNames)
	for _, name := range endpointNames {
		endpoint := c.Endpoints[name]
		fieldPrefix := "endpoints." + name
		if !safeName.MatchString(name) {
			issues = append(issues, validationError(fieldPrefix, "endpoint name contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if !endpoint.Enabled {
			continue
		}
		pathPattern, pathOK, pathIssues := endpointPathIssues(fieldPrefix, endpoint)
		issues = append(issues, pathIssues...)
		issues = append(issues, variableValidationIssues(fieldPrefix, endpoint, pathPattern, c)...)
		issues = append(issues, fallbackRouteValidationIssues(fieldPrefix, endpoint, c)...)
		expanded := endpoint.DestinationRoutes()
		if len(expanded) > MaxExpandedRoutes {
			issues = append(issues, validationError(fieldPrefix+".destinations", fmt.Sprintf("variables expand to more than %d routes", MaxExpandedRoutes), "reduce the number of enumerated values or split the endpoint"))
		}
		switch endpoint.EffectiveDeliveryFailurePolicy() {
		case DeliveryFailurePolicyStopOnFailure, DeliveryFailurePolicySkipFailed:
		default:
			issues = append(issues, validationError(fieldPrefix+".delivery_failure_policy", fmt.Sprintf("%q is not supported", endpoint.DeliveryFailurePolicy), "use \"stop_on_failure\" or \"skip_failed\""))
		}
		if len(endpoint.Destinations) == 0 {
			issues = append(issues, validationError(fieldPrefix+".destinations", "at least one destination is required for enabled endpoints", "add destination objects that reference destinations entries"))
		}
		seenRoutes := map[string]struct{}{}
		// The fallback route carries no per-route options, yet the factory
		// builds one target per (destination, target) across all endpoints
		// with first-wins semantics over map iteration. Register it with
		// zero options so a shared target that sets any is reported instead
		// of provisioned nondeterministically.
		if fb, ok := endpoint.FallbackRoute(); ok {
			if dest, ok := c.Destinations[fb.Destination]; ok {
				fbField := fieldPrefix + ".on_unresolved_variable.target"
				key := fb.Destination + "\x00" + fb.Target
				switch dest.Type {
				case "sqs":
					if prev, ok := sqsTargetSettings[key]; ok && !reflect.DeepEqual(prev, sqsRouteSettings{}) {
						issues = append(issues, validationError(fbField, "shares an sqs target with a route that sets sqs_options or fifo; the fallback route carries none", "use a dedicated fallback target, or remove the options from the shared route"))
					}
					sqsTargetSettings[key] = sqsRouteSettings{}
				case "sns":
					if prev, ok := snsTargetSettings[key]; ok && !reflect.DeepEqual(prev, snsRouteSettings{}) {
						issues = append(issues, validationError(fbField, "shares an sns target with a route that sets sns_options or fifo; the fallback route carries none", "use a dedicated fallback target, or remove the options from the shared route"))
					}
					snsTargetSettings[key] = snsRouteSettings{}
				case "rabbitmq", "":
					if prev, ok := queueOptions[key]; ok && prev != (RabbitMQQueueOptions{}) {
						issues = append(issues, validationError(fbField, "shares a RabbitMQ queue with a route that sets queue_options; the fallback route carries none", "use a dedicated fallback target, or remove queue_options from the shared route"))
					}
					queueOptions[key] = RabbitMQQueueOptions{}
				}
			}
		}
		for i, destination := range endpoint.Destinations {
			field := fmt.Sprintf("%s.destinations[%d]", fieldPrefix, i)
			if destination.Destination == "" {
				issues = append(issues, validationError(field+".destination", "destination is required", "reference an existing destinations entry"))
				continue
			}
			if _, ok := c.Destinations[destination.Destination]; !ok {
				issues = append(issues, validationError(field+".destination", "destination is not configured", "reference an existing destinations entry"))
			}
			tpl, tplErr := vars.Parse(destination.Target)
			switch {
			case tplErr != nil:
				issues = append(issues, validationError(field+".target", "target template is malformed: "+tplErr.Error(), "use {variable} placeholders with balanced braces, e.g. \"{envName}-events\""))
			case tpl.HasPlaceholders():
				for _, n := range tpl.Names() {
					if _, ok := endpoint.Variables[n]; !ok {
						issues = append(issues, validationError(field+".target", fmt.Sprintf("references undeclared variable {%s}", n), "declare it under "+fieldPrefix+".variables"))
					}
				}
				if !targetLiteral.MatchString(tpl.Literal()) {
					issues = append(issues, validationError(field+".target", "target template literal text contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen around {variable} placeholders"))
				}
				// Each placeholder renders at least one character and the
				// whole must stay within the 128-character safe-name bound.
				if len(tpl.Literal())+len(tpl.Names()) > 128 {
					issues = append(issues, validationError(field+".target", "target template literal text leaves no room for variable values within 128 characters", "shorten the literal text around the {variable} placeholders"))
				}
			default:
				if !safeName.MatchString(destination.Target) {
					issues = append(issues, validationError(field+".target", "target name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
			}
			// Expanded routes for this authored route: every concrete target
			// must be a safe name, and dynamic targets are only allowed on
			// destinations that can accept a target they have never seen.
			var routeKeys []string
			duplicate := false
			for _, er := range expanded {
				if er.Index != i {
					continue
				}
				if !er.Dynamic && !safeName.MatchString(er.Target) {
					issues = append(issues, validationError(field+".target", fmt.Sprintf("expands to invalid target %q with %s", er.Target, describeBindings(er.Bindings)), "use values that keep the rendered target within 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
				if er.Dynamic {
					if d, ok := c.Destinations[destination.Destination]; ok {
						if (d.Type == "rabbitmq" || d.Type == "") && destination.Create {
							issues = append(issues, validationError(field+".create", "create is not supported on a rabbitmq route whose target has pattern variables (the queue cannot be declared up front)", "pre-declare and bind the queues, or declare the variable with values so the routes expand at startup"))
						}
					}
				}
				key := destination.Destination + "\x00" + er.Target
				if _, dup := seenRoutes[key]; dup {
					msg := "destination route is duplicated"
					if len(er.Bindings) > 0 {
						msg += " (with " + describeBindings(er.Bindings) + ")"
					}
					issues = append(issues, validationError(field, msg, "list each destination and target pair once"))
					duplicate = true
					break
				}
				seenRoutes[key] = struct{}{}
				routeKeys = append(routeKeys, key)
			}
			if duplicate {
				continue
			}
			for _, routeKey := range routeKeys {
				if previous, ok := queueOptions[routeKey]; ok && previous != destination.QueueOptions {
					issues = append(issues, validationError(field+".queue_options", "cannot differ between endpoints that share a RabbitMQ queue", "use one queue per endpoint when queue arguments need to differ"))
					break
				}
				queueOptions[routeKey] = destination.QueueOptions
			}
			issues = append(issues, queueOptionValidationIssues(field+".queue_options", destination.QueueOptions)...)

			// Type-coupling checks
			dest, destOK := c.Destinations[destination.Destination]
			if destOK {
				if dest.Type != "sqs" && (destination.SQSOptions != SQSEndpointOptions{}) {
					issues = append(issues, validationError(field+".sqs_options", "only valid for sqs destinations", "remove sqs_options or point destination at an sqs destination"))
				}
				if dest.Type != "sns" && (destination.SNSOptions != SNSEndpointOptions{}) {
					issues = append(issues, validationError(field+".sns_options", "only valid for sns destinations", "remove sns_options or point destination at an sns destination"))
				}
				if dest.Type != "rabbitmq" && (destination.QueueOptions != RabbitMQQueueOptions{}) {
					issues = append(issues, validationError(field+".queue_options", "only valid for rabbitmq destinations", "remove queue_options or point destination at a rabbitmq destination"))
				}
				isRedis := dest.Type == "redis_stream" || dest.Type == "redis_list" || dest.Type == "redis_pubsub" || dest.Type == "redis_kv"
				if !isRedis && (destination.RedisOptions != RedisEndpointOptions{}) {
					issues = append(issues, validationError(field+".redis_options", "only valid for redis_* destinations", "remove redis_options or point destination at a redis_stream, redis_list, redis_pubsub, or redis_kv destination"))
				}
				if isRedis {
					issues = append(issues, redisOptionsValidationIssues(field+".redis_options", destination.RedisOptions, dest.Type)...)
				}
				if dest.Type != "kafka" && (destination.KafkaOptions != KafkaEndpointOptions{}) {
					issues = append(issues, validationError(field+".kafka_options", "only valid for kafka destinations", "remove kafka_options or point destination at a kafka destination"))
				}
				if dest.Type == "kafka" {
					issues = append(issues, kafkaPartitionKeyValidationIssues(field+".kafka_options.partition_key", destination.KafkaOptions.PartitionKey)...)
					if destination.KafkaOptions.Partitions < 0 {
						issues = append(issues, validationError(field+".kafka_options.partitions", "must be zero (broker default) or a positive integer", "set a partition count such as 6, or omit for the broker default"))
					}
					if destination.KafkaOptions.ReplicationFactor < 0 {
						issues = append(issues, validationError(field+".kafka_options.replication_factor", "must be zero (broker default) or a positive integer", "set a replication factor such as 3, or omit for the broker default"))
					}
				}
				if dest.Type != "temporal" && (destination.TemporalOptions != TemporalEndpointOptions{}) {
					issues = append(issues, validationError(field+".temporal_options", "only valid for temporal destinations", "remove temporal_options or point destination at a temporal destination"))
				}
				if dest.Type == "temporal" {
					issues = append(issues, temporalOptionsValidationIssues(field, destination)...)
					if prefix := destination.TemporalOptions.WorkflowID.Prefix; strings.Contains(prefix, "{") {
						if tpl, err := vars.Parse(prefix); err != nil {
							issues = append(issues, validationError(field+".temporal_options.workflow_id.prefix", "prefix template is malformed: "+err.Error(), "use {variable} placeholders with balanced braces"))
						} else {
							for _, n := range tpl.Names() {
								if _, ok := endpoint.Variables[n]; !ok {
									issues = append(issues, validationError(field+".temporal_options.workflow_id.prefix", fmt.Sprintf("references undeclared variable {%s}", n), "declare it under "+fieldPrefix+".variables"))
								}
							}
						}
					}
					// Cross-endpoint consistency: the factory builds one target per
					// (destination, task queue), so routes sharing a target must agree.
					for _, routeKey := range routeKeys {
						if prev, ok := temporalTargetSettings[routeKey]; ok && prev != destination.TemporalOptions {
							issues = append(issues, validationError(field+".temporal_options", "cannot differ between endpoints that share a temporal task queue", "use one task queue per endpoint when temporal options need to differ"))
							break
						}
						temporalTargetSettings[routeKey] = destination.TemporalOptions
					}
				}
			}
			// A templated webhook URL renders from this endpoint's variables, so
			// every placeholder it uses must be declared here.
			if destOK && dest.Type == "webhook" {
				if urlTpl, err := vars.Parse(dest.Webhook.URL); err == nil {
					for _, n := range urlTpl.Names() {
						if _, ok := endpoint.Variables[n]; !ok {
							issues = append(issues, validationError(field+".destination", fmt.Sprintf("destination %q webhook.url references variable {%s} which this endpoint does not declare", destination.Destination, n), "declare the variable under "+fieldPrefix+".variables or remove the placeholder from the url"))
						}
					}
				}
			}
			if err := ValidateBrokerAttributes(dest.Type, strings.HasSuffix(destination.Target, ".fifo"), destination.BrokerAttributes); err != nil {
				issues = append(issues, validationError(field+".broker_attributes", err.Error(), ""))
			}
			// SQS cross-endpoint consistency
			if destOK && dest.Type == "sqs" {
				cur := sqsRouteSettings{opts: destination.SQSOptions, fifo: destination.FIFO, attrs: destination.BrokerAttributes}
				for _, routeKey := range routeKeys {
					if prev, ok := sqsTargetSettings[routeKey]; ok && !reflect.DeepEqual(prev, cur) {
						issues = append(issues, validationError(field+".sqs_options", "cannot differ between endpoints that share an sqs target", "use one queue per endpoint when sqs options need to differ"))
						break
					}
					sqsTargetSettings[routeKey] = cur
				}
			}
			// SNS cross-endpoint consistency
			if destOK && dest.Type == "sns" {
				cur := snsRouteSettings{opts: destination.SNSOptions, fifo: destination.FIFO, attrs: destination.BrokerAttributes}
				for _, routeKey := range routeKeys {
					if prev, ok := snsTargetSettings[routeKey]; ok && !reflect.DeepEqual(prev, cur) {
						issues = append(issues, validationError(field+".sns_options", "cannot differ between endpoints that share an sns target", "use one queue per endpoint when sns options need to differ"))
						break
					}
					snsTargetSettings[routeKey] = cur
				}
			}
			// FIX 5: SNS FIFO topics do not support Subject
			if destOK && dest.Type == "sns" && strings.HasSuffix(destination.Target, ".fifo") && destination.SNSOptions.Subject != "" {
				issues = append(issues, validationError(field+".sns_options.subject", "subject is not supported on FIFO SNS topics", "remove sns_options.subject for FIFO targets"))
			}
			// SQS option range validation
			if destOK && dest.Type == "sqs" {
				issues = append(issues, sqsOptionsValidationIssues(field+".sqs_options", destination.SQSOptions)...)
			}
			// SNS subject validation
			if destOK && dest.Type == "sns" {
				issues = append(issues, snsSubjectValidationIssues(field+".sns_options", destination.SNSOptions.Subject)...)
			}
			isFifoTarget := strings.HasSuffix(destination.Target, ".fifo")
			if destination.FIFO != nil && !isFifoTarget {
				issues = append(issues, validationError(field+".fifo", "fifo block requires a target ending in .fifo", "rename the target to end in .fifo or remove the fifo block"))
			}
			if isFifoTarget && destination.FIFO == nil {
				issues = append(issues, validationError(field+".fifo", "fifo targets require a fifo block", "add a fifo.group_id block to define ordering"))
			}
			if destination.FIFO != nil {
				gid := destination.FIFO.GroupID
				issues = append(issues, sourceValueIssues(field+".fifo.group_id", gid.Source, gid.Value, gid.Path, "group id", false, "use static, endpoint, header, or body")...)
				if !destination.FIFO.Dedup.ContentBasedFallback {
					issues = append(issues, validationError(
						field+".fifo.dedup.content_based_fallback",
						"must be true for FIFO targets so messages without an idempotency key are still deduplicated",
						"set fifo.dedup.content_based_fallback: true",
					))
				}
			}
			issues = append(issues, filterValidationIssues(field+".filters", destination.Filters)...)
		}
		if endpoint.Path == "" {
			issues = append(issues, validationError(fieldPrefix+".path", "path is required for enabled endpoints", "set a path such as \"/webhooks/billing/events\""))
		} else if endpoint.Path[0] != '/' {
			issues = append(issues, validationError(fieldPrefix+".path", "path must start with /", "set a path such as \"/webhooks/billing/events\""))
		} else if pathOK {
			for _, other := range endpointPaths {
				if other.raw == endpoint.Path {
					issues = append(issues, validationError(fieldPrefix+".path", "path duplicates endpoint "+other.name, "each enabled endpoint must use a unique path"))
					break
				}
				if vars.Overlap(other.pattern, pathPattern) {
					issues = append(issues, validationError(fieldPrefix+".path", "path overlaps endpoint "+other.name, "avoid parent/child paths such as \"/webhooks/billing\" and \"/webhooks/billing/events\", and literal segments that a {placeholder} in another endpoint would also match"))
					break
				}
			}
			endpointPaths = append(endpointPaths, compiledEndpointPath{name: name, raw: endpoint.Path, pattern: pathPattern})
		}
		if endpoint.MaxBodyBytes < 0 {
			issues = append(issues, validationError(fieldPrefix+".max_body_bytes", "must be positive when set", "omit to inherit server.max_body_bytes or set a positive byte limit"))
		}
		if endpoint.RateLimit.Enabled != nil && *endpoint.RateLimit.Enabled {
			if endpoint.RateLimit.RequestsPerMinute <= 0 {
				issues = append(issues, validationError(fieldPrefix+".rate_limit.requests_per_minute", "must be positive when endpoint rate limiting is enabled", "set a value such as 60"))
			}
			if endpoint.RateLimit.Burst <= 0 {
				issues = append(issues, validationError(fieldPrefix+".rate_limit.burst", "must be positive when endpoint rate limiting is enabled", "set a value such as 10"))
			}
			if endpoint.RateLimit.KeyBy != "ip" {
				issues = append(issues, validationError(fieldPrefix+".rate_limit.key_by", "only ip is supported for MVP", "set rate_limit.key_by to \"ip\""))
			}
		}
		if endpoint.ReplayProtection.Enabled {
			if endpoint.ReplayProtection.TTL <= 0 {
				issues = append(issues, validationError(fieldPrefix+".replay_protection.ttl", "must be a positive duration", "set a value such as \"10m\""))
			}
			if endpoint.ReplayProtection.MaxEntries <= 0 {
				issues = append(issues, validationError(fieldPrefix+".replay_protection.max_entries", "must be positive when replay protection is enabled", "set a bounded size such as 50000"))
			}
		}
		for i, entry := range endpoint.IPAllowlist {
			if net.ParseIP(entry) == nil {
				if _, _, err := net.ParseCIDR(entry); err != nil {
					issues = append(issues, validationError(fmt.Sprintf("%s.ip_allowlist[%d]", fieldPrefix, i), "must be an IP address or CIDR range", "use values such as \"203.0.113.10\" or \"198.51.100.0/24\""))
				}
			}
		}
		if endpoint.Auth.Type == "" {
			issues = append(issues, validationError(fieldPrefix+".auth.type", "auth type is required", "set auth.type to \"hmac_sha256\", \"header_exact\", \"no_auth\", or \"jwt\""))
		} else if endpoint.Auth.Type != auth.AuthTypeHMACSHA256 && endpoint.Auth.Type != auth.AuthTypeHeaderExact && endpoint.Auth.Type != auth.AuthTypeNoAuth && endpoint.Auth.Type != auth.AuthTypeJWT {
			issues = append(issues, validationError(fieldPrefix+".auth.type", fmt.Sprintf("%q is not supported", endpoint.Auth.Type), "supported values are \"hmac_sha256\", \"header_exact\", \"no_auth\", and \"jwt\""))
		}
		if !auth.SchemeAllowedForAuthType(endpoint.Auth.Type, endpoint.Auth.Scheme) {
			issues = append(issues, validationError(fieldPrefix+".auth.scheme", fmt.Sprintf("%q is not valid for auth.type %q", endpoint.Auth.Scheme, endpoint.Auth.Type), fmt.Sprintf("use one of %v", auth.Schemes())))
		}
		// no_auth disables verification entirely, so it requires neither a secret
		// nor a signature header.
		if endpoint.Auth.Type != auth.AuthTypeNoAuth {
			if endpoint.Auth.Secret == "" {
				issues = append(issues, validationError(fieldPrefix+".auth.secret", "secret is required", "reference an environment variable such as ${WEBHOOK_SECRET}"))
			}
			if endpoint.Auth.SignatureHeader == "" {
				issues = append(issues, validationError(fieldPrefix+".auth.signature_header", "signature header is required", "set the header the provider signs with, such as \"X-Signature\""))
			}
		}
		if auth.SchemeRequiresTimestamp(endpoint.Auth.Scheme) && endpoint.Auth.TimestampTolerance <= 0 {
			issues = append(issues, validationError(fieldPrefix+".auth.timestamp_tolerance", "must be a positive duration for timestamped schemes", "set a value such as \"5m\""))
		}
		if endpoint.Auth.RemoveBearer && endpoint.Auth.Type != auth.AuthTypeHeaderExact {
			issues = append(issues, validationError(fieldPrefix+".auth.remove_bearer", "only supported for header_exact auth", "set auth.type to \"header_exact\" or remove remove_bearer"))
		}
		if endpoint.Auth.AllowUnboundBody && endpoint.Auth.Type != auth.AuthTypeJWT {
			issues = append(issues, validationError(fieldPrefix+".auth.allow_unbound_body", "only supported for jwt auth", "set auth.type to \"jwt\" or remove allow_unbound_body"))
		}
		if endpoint.Auth.Type == auth.AuthTypeJWT && endpoint.Auth.BodyHashClaim == "" && !endpoint.Auth.AllowUnboundBody {
			issues = append(issues, validationError(fieldPrefix+".auth.body_hash_claim", "body_hash_claim is required for jwt auth", "set body_hash_claim to the JWT claim that carries hex(SHA-256(body)), or set allow_unbound_body: true to opt out"))
		}
		issues = append(issues, filterValidationIssues(fieldPrefix+".filters", endpoint.Filters)...)
		if endpoint.FilterResponse.Status != 0 {
			if len(endpoint.Filters) == 0 {
				issues = append(issues, validationError(fieldPrefix+".filter_response", "filter_response has no effect without filters", "add a filters chain or remove filter_response"))
			}
			if endpoint.FilterResponse.Status < 200 || endpoint.FilterResponse.Status > 599 {
				issues = append(issues, validationError(fieldPrefix+".filter_response.status", "must be a valid HTTP status code (200-599)", "use a client-error status such as 422, or omit for the default silent 202 ack"))
			}
		}
	}
	issues = append(issues, replayIssues(c)...)
	return issues
}

// filterValidationIssues validates an endpoint or route filter chain by
// compiling it and translating each compile issue into a config validation
// issue. It also enforces config-level rules that the filter package cannot know
// about, such as valid HTTP header names for header sources.
func filterValidationIssues(fieldPrefix string, filters []FilterConditionConfig) []ValidationIssue {
	if len(filters) == 0 {
		return nil
	}
	specs := make([]filter.Spec, len(filters))
	for i, f := range filters {
		specs[i] = filter.Spec{Source: f.Source, Path: f.Path, Operator: f.Operator, Value: f.Value}
	}
	var issues []ValidationIssue
	_, filterIssues := filter.Compile(specs)
	for _, issue := range filterIssues {
		issues = append(issues, validationError(fmt.Sprintf("%s[%d].%s", fieldPrefix, issue.Index, issue.Field), issue.Message, issue.Suggestion))
	}
	for i, f := range filters {
		if f.Source == filter.SourceHeader && f.Path != "" && !safeHeaderName.MatchString(f.Path) {
			issues = append(issues, validationError(fmt.Sprintf("%s[%d].path", fieldPrefix, i), "header name is invalid", "use a valid HTTP header name such as \"X-GitHub-Event\""))
		}
	}
	return issues
}

type sqsRouteSettings struct {
	attrs map[string]string
	opts  SQSEndpointOptions
	fifo  *FIFOConfig
}

type snsRouteSettings struct {
	attrs map[string]string
	opts  SNSEndpointOptions
	fifo  *FIFOConfig
}

func sqsOptionsValidationIssues(fieldPrefix string, opts SQSEndpointOptions) []ValidationIssue {
	var issues []ValidationIssue
	if opts.VisibilityTimeout > 0 {
		if opts.VisibilityTimeout%time.Second != 0 {
			issues = append(issues, validationError(
				fieldPrefix+".visibility_timeout",
				"must be a whole number of seconds (no sub-second component)",
				"use values like \"0s\", \"30s\", or \"12h\"",
			))
		} else {
			secs := int64(opts.VisibilityTimeout.Seconds())
			if secs > 43200 {
				issues = append(issues, validationError(
					fieldPrefix+".visibility_timeout",
					fmt.Sprintf("must be at most 12h (43200s); got %ds", secs),
					"SQS allows visibility timeout 0–43200s",
				))
			}
		}
	}
	if opts.MessageRetention > 0 {
		if opts.MessageRetention%time.Second != 0 {
			issues = append(issues, validationError(
				fieldPrefix+".message_retention",
				"must be a whole number of seconds (no sub-second component)",
				"use values like \"60s\", \"4d\", or \"14d\"",
			))
		} else {
			secs := int64(opts.MessageRetention.Seconds())
			if secs < 60 {
				issues = append(issues, validationError(
					fieldPrefix+".message_retention",
					fmt.Sprintf("must be at least 60s; got %ds", secs),
					"SQS allows message retention 60s–1209600s (14 days)",
				))
			} else if secs > 1209600 {
				issues = append(issues, validationError(
					fieldPrefix+".message_retention",
					fmt.Sprintf("must be at most 14d (1209600s); got %ds", secs),
					"SQS allows message retention 60s–1209600s (14 days)",
				))
			}
		}
	}
	return issues
}

func snsSubjectValidationIssues(fieldPrefix string, subject string) []ValidationIssue {
	if subject == "" {
		return nil
	}
	var issues []ValidationIssue
	if len(subject) > 100 {
		issues = append(issues, validationError(
			fieldPrefix+".subject",
			fmt.Sprintf("must be at most 100 characters; got %d", len(subject)),
			"SNS subject must be 100 ASCII characters or fewer",
		))
	}
	if len(subject) > 0 && subject[0] == ' ' {
		issues = append(issues, validationError(
			fieldPrefix+".subject",
			"must not start with a space",
			"remove leading whitespace from sns_options.subject",
		))
	}
	for _, r := range subject {
		if r < 0x20 || r > 0x7E {
			issues = append(issues, validationError(
				fieldPrefix+".subject",
				"must contain only printable ASCII characters (0x20–0x7E)",
				"remove control characters and non-ASCII characters from sns_options.subject",
			))
			break
		}
	}
	return issues
}

// compiledEndpointPath pairs an enabled endpoint with its parsed path pattern
// so later endpoints can be checked for overlap.
type compiledEndpointPath struct {
	name    string
	raw     string
	pattern vars.PathPattern
}

// endpointPathIssues parses the endpoint path as a pattern and checks that
// every {segment} placeholder is backed by a variable with source: path. It
// returns ok=false when the path could not be parsed (emptiness and the
// leading-slash rule are reported by the caller, so they are skipped here).
func endpointPathIssues(fieldPrefix string, endpoint EndpointConfig) (vars.PathPattern, bool, []ValidationIssue) {
	var issues []ValidationIssue
	if endpoint.Path == "" || endpoint.Path[0] != '/' {
		return vars.PathPattern{}, false, nil
	}
	pp, err := vars.ParsePath(endpoint.Path)
	if err != nil {
		return vars.PathPattern{}, false, append(issues, validationError(fieldPrefix+".path", "path is malformed: "+err.Error(), "use literal segments or whole-segment placeholders such as \"/webhooks/{envName}/events\""))
	}
	for _, seg := range pp.Names() {
		found := false
		for _, v := range endpoint.Variables {
			if v.Source == string(vars.SourcePath) && effectiveVariablePath(seg, v) == seg {
				found = true
				break
			}
		}
		if !found {
			issues = append(issues, validationError(fieldPrefix+".path", fmt.Sprintf("placeholder {%s} has no variable with source: path", seg), "declare "+fieldPrefix+".variables."+seg+" with source: path"))
		}
	}
	return pp, true, issues
}

// effectiveVariablePath returns the lookup key of a variable: its explicit
// path, or its own name when the path is omitted.
func effectiveVariablePath(name string, v VariableConfig) string {
	if v.Path != "" {
		return v.Path
	}
	return name
}

// variableValidationIssues validates the endpoint's variables block and that
// every declared variable is referenced by the path, a route target, or the
// webhook.url of a destination the endpoint routes to.
func variableValidationIssues(fieldPrefix string, endpoint EndpointConfig, pathPattern vars.PathPattern, c Config) []ValidationIssue {
	var issues []ValidationIssue
	if len(endpoint.Variables) == 0 {
		return nil
	}
	referenced := map[string]struct{}{}
	for _, n := range pathPattern.Names() {
		referenced[n] = struct{}{}
	}
	for _, d := range endpoint.Destinations {
		if tpl, err := vars.Parse(d.Target); err == nil {
			for _, n := range tpl.Names() {
				referenced[n] = struct{}{}
			}
		}
		if tpl, err := vars.Parse(d.TemporalOptions.WorkflowID.Prefix); err == nil {
			for _, n := range tpl.Names() {
				referenced[n] = struct{}{}
			}
		}
		if dest, ok := c.Destinations[d.Destination]; ok && dest.Type == "webhook" {
			if tpl, err := vars.Parse(dest.Webhook.URL); err == nil {
				for _, n := range tpl.Names() {
					referenced[n] = struct{}{}
				}
			}
		}
	}
	names := make([]string, 0, len(endpoint.Variables))
	for name := range endpoint.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := endpoint.Variables[name]
		field := fieldPrefix + ".variables." + name
		if !vars.ValidName(name) {
			issues = append(issues, validationError(field, "variable name contains unsupported characters", "use letters, digits, and underscore, not starting with a digit"))
		}
		if _, reserved := ReservedVariableNames[name]; reserved {
			issues = append(issues, validationError(field, "variable name is reserved (redis_kv key_template token)", "rename the variable; prefix, target, endpoint, id, and idempotency_key are reserved"))
		}
		path := effectiveVariablePath(name, v)
		isLookup := v.Source == string(vars.SourceLookup)
		switch v.Source {
		case "":
			issues = append(issues, validationError(field+".source", "source is required", "use header, body, query, path, or lookup"))
		case string(vars.SourceLookup):
			if v.Lookup == nil {
				issues = append(issues, validationError(field+".lookup", "lookup block is required when source is lookup", "add lookup: {key: {...}, provider: dynamodb|http|postgres, ...}"))
			} else {
				issues = append(issues, lookupValidationIssues(field+".lookup", *v.Lookup, pathPattern, fieldPrefix)...)
			}
		default:
			issues = append(issues, variableSourcePathIssues(field, v.Source, path, pathPattern, fieldPrefix)...)
		}
		if !isLookup && v.Lookup != nil {
			issues = append(issues, validationError(field+".lookup", "lookup is only valid when source is lookup", "set source: lookup or remove the lookup block"))
		}
		hasValues, hasPattern := len(v.Values) > 0, v.Pattern != ""
		switch {
		case hasValues && hasPattern:
			issues = append(issues, validationError(field, "values and pattern are mutually exclusive", "set either an enumerated values list or a pattern regex"))
		case !hasValues && !hasPattern && !isLookup:
			if v.Values != nil {
				issues = append(issues, validationError(field+".values", "values must list at least one value", "add the permitted values, or use pattern instead"))
			} else {
				issues = append(issues, validationError(field, "either values or pattern is required", "set values: [..] for a fixed set, or pattern: \"^...$\" for an open set"))
			}
		}
		if hasValues {
			seen := map[string]struct{}{}
			for _, val := range v.Values {
				if !safeName.MatchString(val) {
					issues = append(issues, validationError(field+".values", fmt.Sprintf("value %q contains unsupported characters", val), "use 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
				if strings.HasSuffix(val, ".fifo") {
					issues = append(issues, validationError(field+".values", fmt.Sprintf("value %q must not end in .fifo", val), "keep the .fifo suffix literal in the route target instead"))
				}
				if _, dup := seen[val]; dup {
					issues = append(issues, validationError(field+".values", fmt.Sprintf("value %q is listed more than once", val), "list each value once"))
				}
				seen[val] = struct{}{}
			}
		}
		var compiled *regexp.Regexp
		if hasPattern {
			if len(v.Pattern) > 256 {
				issues = append(issues, validationError(field+".pattern", "pattern is longer than 256 characters", "simplify the pattern"))
			}
			if !strings.HasPrefix(v.Pattern, "^") || !strings.HasSuffix(v.Pattern, "$") {
				issues = append(issues, validationError(field+".pattern", "pattern must be anchored with ^ and $", "write the pattern as \"^...$\" so it matches the whole value"))
			}
			re, err := regexp.Compile(v.Pattern)
			if err != nil {
				issues = append(issues, validationError(field+".pattern", "pattern is not a valid regular expression: "+err.Error(), "use RE2 syntax"))
			} else {
				compiled = re
			}
		}
		if v.Default != "" {
			spec := vars.Spec{Name: name, Values: v.Values, Pattern: compiled}
			if (hasValues || compiled != nil) && !spec.Accepts(v.Default) {
				issues = append(issues, validationError(field+".default", fmt.Sprintf("default %q is not an accepted value", v.Default), "use one of the values, or a value matching the pattern"))
			}
			if !safeName.MatchString(v.Default) {
				issues = append(issues, validationError(field+".default", "default contains unsupported characters", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
			}
		}
		if _, used := referenced[name]; !used {
			issues = append(issues, validationError(field, "variable is declared but never referenced", "use {"+name+"} in the endpoint path, a route target, or a webhook url, or remove the variable"))
		}
	}
	return issues
}

// variableSourcePathIssues validates a non-lookup source and its lookup key
// (header name, dotted body path, query key, or path segment).
func variableSourcePathIssues(field, source, path string, pathPattern vars.PathPattern, endpointPrefix string) []ValidationIssue {
	var issues []ValidationIssue
	switch source {
	case string(vars.SourceHeader):
		if !safeHeaderName.MatchString(path) {
			issues = append(issues, validationError(field+".path", "header name contains unsupported characters", "set path to a valid HTTP header name such as X-Env"))
		}
	case string(vars.SourceBody):
		for _, seg := range strings.Split(path, ".") {
			if seg == "" {
				issues = append(issues, validationError(field+".path", "dotted body path contains an empty segment", "use a path such as account.id"))
				break
			}
		}
	case string(vars.SourceQuery):
		// any non-empty key is acceptable
	case string(vars.SourcePath):
		found := false
		for _, seg := range pathPattern.Names() {
			if seg == path {
				found = true
				break
			}
		}
		if !found {
			issues = append(issues, validationError(field+".path", fmt.Sprintf("endpoint path has no {%s} segment", path), "add {"+path+"} as a whole segment of "+endpointPrefix+".path, or change the source"))
		}
	default:
		issues = append(issues, validationError(field+".source", fmt.Sprintf("%q is not supported", source), "use header, body, query, or path"))
	}
	return issues
}

// lookupValidationIssues validates a variable's lookup block: key extraction,
// provider selection, and the selected provider's required fields.
func lookupValidationIssues(field string, lc LookupConfig, pathPattern vars.PathPattern, endpointPrefix string) []ValidationIssue {
	var issues []ValidationIssue
	if lc.Key.Source == "" {
		issues = append(issues, validationError(field+".key.source", "key.source is required", "use header, body, query, or path"))
	} else if lc.Key.Source == string(vars.SourceLookup) {
		issues = append(issues, validationError(field+".key.source", "a lookup key cannot itself come from a lookup", "use header, body, query, or path"))
	} else {
		issues = append(issues, variableSourcePathIssues(field+".key", lc.Key.Source, lc.Key.Path, pathPattern, endpointPrefix)...)
	}
	configured := 0
	for _, set := range []bool{lc.DynamoDB != nil, lc.HTTP != nil, lc.Postgres != nil} {
		if set {
			configured++
		}
	}
	switch lc.Provider {
	case "":
		issues = append(issues, validationError(field+".provider", "provider is required", "use dynamodb, http, or postgres"))
	case "dynamodb", "http", "postgres":
		want := map[string]bool{"dynamodb": lc.DynamoDB != nil, "http": lc.HTTP != nil, "postgres": lc.Postgres != nil}
		if !want[lc.Provider] {
			issues = append(issues, validationError(field+"."+lc.Provider, fmt.Sprintf("%s block is required for provider %s", lc.Provider, lc.Provider), "add the "+lc.Provider+" settings block"))
		}
		if configured > 1 {
			issues = append(issues, validationError(field, "only the selected provider's block may be set", "remove the other provider blocks"))
		}
	default:
		issues = append(issues, validationError(field+".provider", fmt.Sprintf("%q is not supported", lc.Provider), "use dynamodb, http, or postgres"))
	}
	if d := lc.DynamoDB; d != nil && lc.Provider == "dynamodb" {
		if d.Table == "" {
			issues = append(issues, validationError(field+".dynamodb.table", "table is required", "set the DynamoDB table name"))
		}
		if d.ValueAttribute == "" {
			issues = append(issues, validationError(field+".dynamodb.value_attribute", "value_attribute is required", "set the attribute holding the variable value, e.g. env_name"))
		}
		if (d.FilterAttribute == "") != (d.FilterValue == "") {
			issues = append(issues, validationError(field+".dynamodb.filter", "filter.attribute and filter.value must both be set", "set both, or remove the filter block"))
		}
	}
	if h := lc.HTTP; h != nil && lc.Provider == "http" {
		switch {
		case h.URL == "":
			issues = append(issues, validationError(field+".http.url", "url is required", "set a URL containing {key}, e.g. https://resolver.internal/env?id={key}"))
		case !strings.HasPrefix(h.URL, "http://") && !strings.HasPrefix(h.URL, "https://"):
			issues = append(issues, validationError(field+".http.url", "url must be http or https", "use a URL beginning with http:// or https://"))
		case strings.Count(h.URL, "{key}") != 1:
			issues = append(issues, validationError(field+".http.url", "url must contain exactly one {key} placeholder", "insert {key} where the lookup key belongs"))
		case strings.Contains(urlAuthority(h.URL), "{key}"):
			// The lookup key is raw request input with no values/pattern
			// gate, so it must never pick the resolver host or port.
			issues = append(issues, validationError(field+".http.url", "{key} must not appear in the url host", "put {key} in the path or query, e.g. https://resolver.internal/env?id={key}"))
		}
		switch strings.ToUpper(h.Method) {
		case "", "GET", "POST":
		default:
			issues = append(issues, validationError(field+".http.method", fmt.Sprintf("%q is not supported", h.Method), "use GET or POST"))
		}
		if h.ValuePath == "" {
			issues = append(issues, validationError(field+".http.value_path", "value_path is required", "set the dotted JSON path of the value in the response, e.g. env_name"))
		}
		for name := range h.Headers {
			if !safeHeaderName.MatchString(name) {
				issues = append(issues, validationError(field+".http.headers", fmt.Sprintf("header name %q contains unsupported characters", name), "use a valid HTTP header name"))
			}
		}
	}
	if p := lc.Postgres; p != nil && lc.Provider == "postgres" {
		if p.URL == "" {
			issues = append(issues, validationError(field+".postgres.url", "url is required", "set a postgres:// connection string"))
		}
		if p.Query == "" {
			issues = append(issues, validationError(field+".postgres.query", "query is required", "set a SELECT with a {key} placeholder for the lookup key"))
		} else if strings.Count(p.Query, "{key}") != 1 {
			issues = append(issues, validationError(field+".postgres.query", "query must contain exactly one {key} placeholder (bound as $1, never interpolated)", "e.g. SELECT env_name FROM routing WHERE id = {key}"))
		}
	}
	if lc.Cache.MaxEntries < 0 {
		issues = append(issues, validationError(field+".cache.max_entries", "must be zero (default) or positive", "omit or set a positive number"))
	}
	return issues
}

// fallbackRouteValidationIssues validates on_unresolved_variable.
func fallbackRouteValidationIssues(fieldPrefix string, endpoint EndpointConfig, c Config) []ValidationIssue {
	fb := endpoint.OnUnresolvedVariable
	if fb == nil {
		return nil
	}
	var issues []ValidationIssue
	field := fieldPrefix + ".on_unresolved_variable"
	if len(endpoint.Variables) == 0 {
		issues = append(issues, validationError(field, "has no effect without variables", "declare variables on the endpoint or remove on_unresolved_variable"))
	}
	if fb.Destination == "" {
		issues = append(issues, validationError(field+".destination", "destination is required", "reference an existing destinations entry"))
	} else if dest, ok := c.Destinations[fb.Destination]; !ok {
		issues = append(issues, validationError(field+".destination", fmt.Sprintf("references unknown destination %q", fb.Destination), "reference an existing destinations entry"))
	} else if dest.Type == "webhook" || dest.Type == "temporal" {
		issues = append(issues, validationError(field+".destination", "fallback destination must not be a webhook or temporal destination", "point the fallback at a durable backend such as an s3, sqs, or postgres destination"))
	}
	switch {
	case fb.Target == "":
		issues = append(issues, validationError(field+".target", "target is required", "set a static target such as \"unrouted\""))
	case strings.ContainsAny(fb.Target, "{}"):
		issues = append(issues, validationError(field+".target", "target must be static (no {variable} placeholders)", "use a literal target name"))
	case !safeName.MatchString(fb.Target):
		issues = append(issues, validationError(field+".target", "target name contains unsupported characters", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
	case strings.HasSuffix(fb.Target, ".fifo"):
		issues = append(issues, validationError(field+".target", "target must not be a FIFO queue", "fallback routes carry no fifo block; use a standard target"))
	}
	return issues
}

// webhookURLTemplateIssues checks a templated webhook url. Placeholders inside
// the authority (host) are only allowed when the host ends with a literal
// dotted suffix, so a request can pick at most one label under a fixed domain
// and never the whole host.
func webhookURLTemplateIssues(field, rawURL string) []ValidationIssue {
	tpl, err := vars.Parse(rawURL)
	if err != nil {
		return []ValidationIssue{validationError(field, "url template is malformed: "+err.Error(), "use {variable} placeholders with balanced braces")}
	}
	if !tpl.HasPlaceholders() {
		return nil
	}
	authority := urlAuthority(rawURL)
	authTpl, err := vars.Parse(authority)
	if err != nil {
		return []ValidationIssue{validationError(field, "url host template is malformed: "+err.Error(), "use {variable} placeholders with balanced braces")}
	}
	if !authTpl.HasPlaceholders() {
		return nil
	}
	suffix := authTpl.LiteralSuffix()
	host := suffix
	if colon := strings.IndexByte(host, ':'); colon >= 0 {
		host = host[:colon]
	}
	if !strings.Contains(host, ".") {
		return []ValidationIssue{validationError(field, "a {variable} in the url host must be followed by a literal domain suffix", "write the host as \"{envName}-api.example.com\" so requests cannot choose an arbitrary host")}
	}
	return nil
}

// urlAuthority returns the authority (host[:port]) of a raw, possibly
// templated URL without parsing it, so placeholders survive intact.
func urlAuthority(rawURL string) string {
	return egress.Authority(rawURL)
}

// describeBindings renders enumerated bindings for error messages.
func describeBindings(b map[string]string) string {
	if len(b) == 0 {
		return "no variables"
	}
	names := make([]string, 0, len(b))
	for n := range b {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, n+"="+b[n])
	}
	return strings.Join(parts, ", ")
}

// postgresConnValidationIssues validates the fields shared by every Postgres
// connection block: schema/table identifier safety, max_conns range, and
// mutual-TLS both-or-neither. It is used by the postgres-destination path.
//
// It deliberately does NOT decide whether url or table are required; the
// caller checks those with context-specific field names and messages before
// calling this helper.
func postgresConnValidationIssues(prefix string, cfg PostgresConfig) []ValidationIssue {
	var issues []ValidationIssue
	if cfg.Table != "" && !pgIdentifier.MatchString(cfg.Table) {
		issues = append(issues, validationError(prefix+".table", "table name contains unsupported characters", "use only letters, digits, and underscores, starting with a letter or underscore"))
	}
	if cfg.Schema != "" && !pgIdentifier.MatchString(cfg.Schema) {
		issues = append(issues, validationError(prefix+".schema", "schema name contains unsupported characters", "use only letters, digits, and underscores, starting with a letter or underscore"))
	}
	if cfg.MaxConns < 0 {
		issues = append(issues, validationError(prefix+".max_conns", "must be zero (default) or a positive integer", "set a pool cap such as 10, or omit for the default"))
	}
	if (cfg.TLS.ClientCert == "") != (cfg.TLS.ClientKey == "") {
		issues = append(issues, validationError(prefix+".tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
	}
	return issues
}

// redisConfigValidationIssues validates the shared redis: connection block.
func redisConfigValidationIssues(fieldPrefix string, r RedisConfig) []ValidationIssue {
	var issues []ValidationIssue
	switch r.Mode {
	case "single", "cluster", "sentinel":
		// valid
	default:
		issues = append(issues, validationError(fieldPrefix+".mode",
			fmt.Sprintf("%q is not a supported mode", r.Mode),
			"use \"single\", \"cluster\", or \"sentinel\""))
	}
	switch r.Mode {
	case "single":
		if r.URL == "" {
			issues = append(issues, validationError(fieldPrefix+".url",
				"url is required for single mode",
				"set url to a redis:// or rediss:// URL, or use ${REDIS_URL}"))
		}
	case "cluster", "sentinel":
		if len(r.Addrs) == 0 {
			issues = append(issues, validationError(fieldPrefix+".addrs",
				fmt.Sprintf("addrs must be non-empty for %s mode", r.Mode),
				"list one or more node addresses such as [\"redis-1:6379\", \"redis-2:6379\"]"))
		}
		if r.Mode == "sentinel" && r.MasterName == "" {
			issues = append(issues, validationError(fieldPrefix+".master_name",
				"master_name is required for sentinel mode",
				"set master_name to the sentinel primary name, e.g. \"mymaster\""))
		}
	}
	if (r.TLS.ClientCert == "") != (r.TLS.ClientKey == "") {
		issues = append(issues, validationError(fieldPrefix+".tls.client_cert",
			"client_cert and client_key must both be set for mutual TLS",
			"set both paths, or neither"))
	}
	return issues
}

// redisOptionsValidationIssues validates per-endpoint redis_options field-level coupling.
func redisOptionsValidationIssues(fieldPrefix string, opts RedisEndpointOptions, destType string) []ValidationIssue {
	var issues []ValidationIssue
	// consumer_group is only valid for redis_stream
	if opts.ConsumerGroup != "" && destType != "redis_stream" {
		issues = append(issues, validationError(fieldPrefix+".consumer_group",
			fmt.Sprintf("consumer_group is only valid for redis_stream destinations; got %q", destType),
			"remove consumer_group or change the destination type to redis_stream"))
	}
	// ttl is only valid for redis_kv
	if opts.TTL != 0 && destType != "redis_kv" {
		issues = append(issues, validationError(fieldPrefix+".ttl",
			fmt.Sprintf("ttl is only valid for redis_kv destinations; got %q", destType),
			"remove ttl or change the destination type to redis_kv"))
	}
	// key_template is only valid for redis_kv
	if opts.KeyTemplate != "" && destType != "redis_kv" {
		issues = append(issues, validationError(fieldPrefix+".key_template",
			fmt.Sprintf("key_template is only valid for redis_kv destinations; got %q", destType),
			"remove key_template or change the destination type to redis_kv"))
	}
	// max_len is valid for redis_stream and redis_list
	if opts.MaxLen != 0 && destType != "redis_stream" && destType != "redis_list" {
		issues = append(issues, validationError(fieldPrefix+".max_len",
			fmt.Sprintf("max_len is only valid for redis_stream and redis_list destinations; got %q", destType),
			"remove max_len or change the destination type to redis_stream or redis_list"))
	}
	return issues
}

// kafkaPartitionKeyValidationIssues validates the per-route partition_key block.
// An empty source means "no partition key" (round-robin) and is always valid.
// temporalOptionsValidationIssues validates the per-route temporal_options
// block for a route that targets a temporal destination. field is the route
// prefix (endpoints.<name>.destinations[<i>]).
func temporalOptionsValidationIssues(field string, destination EndpointDestinationConfig) []ValidationIssue {
	return TemporalOptionsValidationIssues(field+".temporal_options", destination.Target, destination.TemporalOptions)
}

// TemporalOptionsValidationIssues is shared by flow and embedded route validation.
func TemporalOptionsValidationIssues(prefix, target string, opts TemporalEndpointOptions) []ValidationIssue {
	var issues []ValidationIssue
	if opts.WorkflowType == "" {
		issues = append(issues, validationError(prefix+".workflow_type", "workflow_type is required for temporal routes", "set temporal_options.workflow_type to the registered workflow type name"))
	}
	switch opts.Input {
	case "", "envelope", "body":
	default:
		issues = append(issues, validationError(prefix+".input", fmt.Sprintf("%q is not supported", opts.Input), "use \"envelope\" (default) or \"body\""))
	}
	issues = append(issues, sourceValueIssues(prefix+".workflow_id", opts.WorkflowID.Source, opts.WorkflowID.Value, opts.WorkflowID.Path, "workflow id", true, "use static, endpoint, header, or body, or omit to use the event id")...)
	switch opts.IDReusePolicy {
	case "", "allow_duplicate", "allow_duplicate_failed_only", "reject_duplicate", "terminate_if_running":
	default:
		issues = append(issues, validationError(prefix+".id_reuse_policy", fmt.Sprintf("%q is not supported", opts.IDReusePolicy), "use allow_duplicate, allow_duplicate_failed_only, reject_duplicate, or terminate_if_running, or omit for the server default"))
	}
	if opts.ExecutionTimeout > 0 && opts.RunTimeout > opts.ExecutionTimeout {
		issues = append(issues, validationError(prefix+".run_timeout", "must not exceed execution_timeout", "lower run_timeout or raise execution_timeout"))
	}
	for _, timeout := range []struct {
		name  string
		value time.Duration
	}{{"execution_timeout", opts.ExecutionTimeout}, {"run_timeout", opts.RunTimeout}, {"task_timeout", opts.TaskTimeout}} {
		if timeout.value < 0 {
			issues = append(issues, validationError(prefix+"."+timeout.name, "must not be negative", "omit for the Temporal default or use a positive duration"))
		}
	}
	if opts.TaskTimeout > 2*time.Minute {
		issues = append(issues, validationError(prefix+".task_timeout", "must not exceed 2m", "Temporal limits workflow task timeouts to two minutes"))
	}
	if strings.HasSuffix(target, ".fifo") {
		issues = append(issues, validationError(strings.TrimSuffix(prefix, ".temporal_options")+".target", "temporal task queue names must not end in .fifo", "rename the target; FIFO semantics do not apply to temporal task queues"))
	}
	return issues
}

func kafkaPartitionKeyValidationIssues(fieldPrefix string, pk KafkaPartitionKeyConfig) []ValidationIssue {
	// empty = round-robin, endpoint = key on endpoint name
	return sourceValueIssues(fieldPrefix, pk.Source, pk.Value, pk.Path, "partition key", true, "use static, endpoint, header, or body")
}

// sourceValueIssues validates the shared source/value/path shape used by FIFO
// group ids, Kafka partition keys, and Temporal workflow ids: static needs a
// value, header/body need a path, endpoint stands alone, and an empty source
// is allowed only when the option has a default (allowEmpty).
func sourceValueIssues(field, source, value, path, noun string, allowEmpty bool, sourceHint string) []ValidationIssue {
	var issues []ValidationIssue
	switch source {
	case "endpoint":
	case "":
		if !allowEmpty {
			issues = append(issues, validationError(field+".source", "source is required", sourceHint))
		}
	case "static":
		if value == "" {
			issues = append(issues, validationError(field+".value", "value is required when source is static", "set a literal "+noun))
		}
	case "header", "body":
		if path == "" {
			issues = append(issues, validationError(field+".path", "path is required when source is header or body", "set a header name or dotted body path"))
		}
	default:
		issues = append(issues, validationError(field+".source", fmt.Sprintf("%q is not supported", source), sourceHint))
	}
	return issues
}

// minReplayTokenLen matches the replay service's own minimum for its keys.
const minReplayTokenLen = 32

// unexpandedReference reports a ${VAR} left in place by validation that does
// not expand the environment; its length says nothing about the secret.
func unexpandedReference(s string) bool { return strings.Contains(s, "${") }

func replayIssues(c Config) []ValidationIssue {
	r := c.Replay
	if !r.Configured {
		return nil
	}
	var issues []ValidationIssue
	if u, err := url.Parse(r.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		issues = append(issues, validationError("replay.url", "must be the replay service's absolute http or https URL", "set replay.url, for example \"https://replay.internal\""))
	}
	if len(r.CaptureToken) < minReplayTokenLen && !unexpandedReference(r.CaptureToken) {
		issues = append(issues, validationError("replay.capture_token", fmt.Sprintf("is required and must be at least %d characters", minReplayTokenLen), "reference a generated secret, for example \"${REPLAY_CAPTURE_TOKEN}\""))
	}
	if r.MaxRecordBytes < 0 {
		issues = append(issues, validationError("replay.max_record_bytes", "must be positive", fmt.Sprintf("omit it for the default of %d (16 MiB)", DefaultReplayMaxRecordBytes)))
	}
	if (r.TLS.ClientCert == "") != (r.TLS.ClientKey == "") {
		issues = append(issues, validationError("replay.tls", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
	}
	ctl := r.Control
	switch {
	case ctl.Addr == "":
		issues = append(issues, validationError("replay.control.addr", "is required: the replay service redrives failures through this listener", "set a private listen address such as \":9092\""))
	case ctl.Addr == c.Server.Addr:
		issues = append(issues, validationError("replay.control.addr", "must differ from server.addr", "keep the control listener off the public webhook port"))
	case c.Metrics.Enabled && c.Metrics.Addr != "" && ctl.Addr == c.Metrics.Addr:
		issues = append(issues, validationError("replay.control.addr", "must differ from metrics.addr", "give the control listener its own port"))
	}
	switch {
	case len(ctl.Token) < minReplayTokenLen && !unexpandedReference(ctl.Token):
		issues = append(issues, validationError("replay.control.token", fmt.Sprintf("is required and must be at least %d characters", minReplayTokenLen), "reference a generated secret, for example \"${REPLAY_CONTROL_TOKEN}\""))
	case ctl.Token == r.CaptureToken:
		issues = append(issues, validationError("replay.control.token", "must differ from replay.capture_token", "use separate secrets: one lets core write to replay, the other lets replay drive core"))
	}
	if ctl.MaxConcurrent < 0 {
		issues = append(issues, validationError("replay.control.max_concurrent", "must be positive", fmt.Sprintf("omit it for the default of %d", DefaultReplayControlMaxConcurrent)))
	}
	return issues
}
