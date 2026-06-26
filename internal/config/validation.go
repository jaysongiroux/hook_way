package config

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/jasongiroux/hookway/internal/auth"
)

type ValidationIssue struct {
	Field      string
	Message    string
	Suggestion string
}

type ConfigValidationError struct {
	Issues []ValidationIssue
}

func (e ConfigValidationError) Error() string {
	var b strings.Builder
	b.WriteString("config validation failed")
	for _, issue := range e.Issues {
		b.WriteString("\n- ")
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

func validationErrors(issues ...ValidationIssue) []ValidationIssue {
	var out []ValidationIssue
	for _, issue := range issues {
		if issue.Field != "" {
			out = append(out, issue)
		}
	}
	return out
}

func configValidationError(issues []ValidationIssue) error {
	if len(issues) == 0 {
		return nil
	}
	return ConfigValidationError{Issues: issues}
}

func (c Config) Validate() error {
	return configValidationError(c.ValidationIssues())
}

func (c Config) ValidationIssues() []ValidationIssue {
	var issues []ValidationIssue
	if c.Server.Addr == "" {
		issues = append(issues, validationError("server.addr", "listen address is required", "set server.addr to a host:port such as \":8080\""))
	}
	if c.Server.MaxBodyBytes <= 0 {
		issues = append(issues, validationError("server.max_body_bytes", "must be a positive integer", "set a safe byte limit, for example 1048576"))
	}
	if c.Server.MaxInflightRequests < 0 {
		issues = append(issues, validationError("server.max_inflight_requests", "must be zero (unlimited) or a positive integer", "set a bound such as 256 to apply backpressure under load"))
	}
	if c.Server.FanoutConcurrency < 0 {
		issues = append(issues, validationError("server.fanout_concurrency", "must be zero (default) or a positive integer", "set a bound such as 8"))
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
	for i, entry := range c.Security.TrustedProxies {
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				issues = append(issues, validationError(fmt.Sprintf("security.trusted_proxies[%d]", i), "must be an IP address or CIDR range", "use values such as \"10.0.0.0/8\" or \"203.0.113.10\""))
			}
		}
	}
	if len(c.Destinations) == 0 {
		issues = append(issues, validationError("destinations", "at least one destination is required", "add destinations.billing with type \"rabbitmq\""))
	}
	for id, dest := range c.Destinations {
		fieldPrefix := "destinations." + id
		if !safeName.MatchString(id) {
			issues = append(issues, validationError(fieldPrefix, "destination id contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if dest.Type == "" {
			issues = append(issues, validationError(fieldPrefix+".type", "destination backend type is required", "set type to \"rabbitmq\", \"sqs\", \"sns\", \"redis_stream\", \"redis_list\", \"redis_pubsub\", or \"redis_kv\""))
		} else if dest.Type != "rabbitmq" && dest.Type != "sqs" && dest.Type != "sns" &&
			dest.Type != "redis_stream" && dest.Type != "redis_list" && dest.Type != "redis_pubsub" && dest.Type != "redis_kv" {
			issues = append(issues, validationError(fieldPrefix+".type", fmt.Sprintf("%q is not supported", dest.Type), "supported values are \"rabbitmq\", \"sqs\", \"sns\", \"redis_stream\", \"redis_list\", \"redis_pubsub\", and \"redis_kv\""))
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
	if len(c.Endpoints) == 0 {
		issues = append(issues, validationError("endpoints", "at least one endpoint is required", "add an endpoint such as endpoints.billing"))
	}
	if c.Metrics.Enabled && (c.Metrics.Path == "" || c.Metrics.Path[0] != '/') {
		issues = append(issues, validationError("metrics.path", "must start with / when metrics are enabled", "set metrics.path to \"/metrics\""))
	}
	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerMinute <= 0 {
			issues = append(issues, validationError("rate_limit.requests_per_minute", "must be positive when rate limiting is enabled", "set a value such as 120"))
		}
		if c.RateLimit.Burst <= 0 {
			issues = append(issues, validationError("rate_limit.burst", "must be positive when rate limiting is enabled", "set a value such as 20"))
		}
		if c.RateLimit.KeyBy != "ip" {
			issues = append(issues, validationError("rate_limit.key_by", "only ip is supported for MVP", "set rate_limit.key_by to \"ip\""))
		}
	}
	paths := map[string]string{}
	queueOptions := map[string]RabbitMQQueueOptions{}
	sqsTargetSettings := map[string]sqsRouteSettings{}
	snsTargetSettings := map[string]snsRouteSettings{}
	for name, endpoint := range c.Endpoints {
		fieldPrefix := "endpoints." + name
		if !safeName.MatchString(name) {
			issues = append(issues, validationError(fieldPrefix, "endpoint name contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if !endpoint.Enabled {
			continue
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
		for i, destination := range endpoint.Destinations {
			field := fmt.Sprintf("%s.destinations[%d]", fieldPrefix, i)
			if destination.Destination == "" {
				issues = append(issues, validationError(field+".destination", "destination is required", "reference an existing destinations entry"))
				continue
			}
			if _, ok := c.Destinations[destination.Destination]; !ok {
				issues = append(issues, validationError(field+".destination", "destination is not configured", "reference an existing destinations entry"))
			}
			if !safeName.MatchString(destination.Target) {
				issues = append(issues, validationError(field+".target", "target name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
			}
			routeKey := destination.Destination + "\x00" + destination.Target
			if _, duplicate := seenRoutes[routeKey]; duplicate {
				issues = append(issues, validationError(field, "destination route is duplicated", "list each destination and target pair once"))
				continue
			}
			seenRoutes[routeKey] = struct{}{}
			queueOptionKey := routeKey
			if previous, ok := queueOptions[queueOptionKey]; ok && previous != destination.QueueOptions {
				issues = append(issues, validationError(field+".queue_options", "cannot differ between endpoints that share a RabbitMQ queue", "use one queue per endpoint when queue arguments need to differ"))
			} else {
				queueOptions[queueOptionKey] = destination.QueueOptions
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
			}
			// SQS cross-endpoint consistency
			if destOK && dest.Type == "sqs" {
				cur := sqsRouteSettings{opts: destination.SQSOptions, fifo: destination.FIFO}
				if prev, ok := sqsTargetSettings[routeKey]; ok && !reflect.DeepEqual(prev, cur) {
					issues = append(issues, validationError(field+".sqs_options", "cannot differ between endpoints that share an sqs target", "use one queue per endpoint when sqs options need to differ"))
				} else {
					sqsTargetSettings[routeKey] = cur
				}
			}
			// SNS cross-endpoint consistency
			if destOK && dest.Type == "sns" {
				cur := snsRouteSettings{opts: destination.SNSOptions, fifo: destination.FIFO}
				if prev, ok := snsTargetSettings[routeKey]; ok && !reflect.DeepEqual(prev, cur) {
					issues = append(issues, validationError(field+".sns_options", "cannot differ between endpoints that share an sns target", "use one queue per endpoint when sns options need to differ"))
				} else {
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
				switch destination.FIFO.GroupID.Source {
				case "endpoint":
				case "static":
					if destination.FIFO.GroupID.Value == "" {
						issues = append(issues, validationError(field+".fifo.group_id.value", "value is required when source is static", "set a literal group id"))
					}
				case "header", "body":
					if destination.FIFO.GroupID.Path == "" {
						issues = append(issues, validationError(field+".fifo.group_id.path", "path is required when source is header or body", "set a header name or dotted body path"))
					}
				case "":
					issues = append(issues, validationError(field+".fifo.group_id.source", "source is required", "use static, endpoint, header, or body"))
				default:
					issues = append(issues, validationError(field+".fifo.group_id.source", fmt.Sprintf("%q is not supported", destination.FIFO.GroupID.Source), "use static, endpoint, header, or body"))
				}
				if !destination.FIFO.Dedup.ContentBasedFallback {
					issues = append(issues, validationError(
						field+".fifo.dedup.content_based_fallback",
						"must be true for FIFO targets so messages without an idempotency key are still deduplicated",
						"set fifo.dedup.content_based_fallback: true",
					))
				}
			}
		}
		if endpoint.Path == "" {
			issues = append(issues, validationError(fieldPrefix+".path", "path is required for enabled endpoints", "set a path such as \"/webhooks/billing/events\""))
		} else if endpoint.Path[0] != '/' {
			issues = append(issues, validationError(fieldPrefix+".path", "path must start with /", "set a path such as \"/webhooks/billing/events\""))
		} else if other, ok := paths[endpoint.Path]; ok {
			issues = append(issues, validationError(fieldPrefix+".path", "path duplicates endpoint "+other, "each enabled endpoint must use a unique path"))
		} else {
			for otherPath, otherName := range paths {
				if endpointPathsOverlap(endpoint.Path, otherPath) {
					issues = append(issues, validationError(fieldPrefix+".path", "path overlaps endpoint "+otherName, "avoid parent/child paths such as \"/webhooks/billing\" and \"/webhooks/billing/events\""))
					break
				}
			}
			paths[endpoint.Path] = name
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
	}
	return issues
}

type sqsRouteSettings struct {
	opts SQSEndpointOptions
	fifo *FIFOConfig
}

type snsRouteSettings struct {
	opts SNSEndpointOptions
	fifo *FIFOConfig
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

func endpointPathsOverlap(a, b string) bool {
	shorter, longer := a, b
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	return strings.HasPrefix(longer, shorter+"/")
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
