# Configuration

Hookway reads one YAML config file. Set it with `HOOKWAY_CONFIG` or pass `--config`; the flag wins. YAML string values support `${ENV_NAME}` expansion.

Startup parses and validates the config before connecting to the message queue or serving traffic. Invalid YAML, unknown fields, missing required values, invalid durations, duplicate endpoint paths, and unsupported backend/auth types stop the app with field-level errors and suggestions.

Example validation output:

```txt
config validation failed
- server.addr: listen address is required Suggestion: set server.addr to a host:port such as ":8080"
- endpoints.paddle_billing.auth.secret: secret is required Suggestion: reference an environment variable such as ${PADDLE_WEBHOOK_SECRET}
```

## Server

- `server.addr`: required listen address, example `":8080"`.
- `server.read_timeout`: optional duration, default `5s`.
- `server.write_timeout`: optional duration, default `10s`.
- `server.idle_timeout`: optional duration, default `60s`.
- `server.shutdown_timeout`: optional duration, default `10s`.
- `server.max_body_bytes`: required positive integer.

## Security

- `security.trusted_proxy_headers`: optional boolean. Default false behavior uses `RemoteAddr`.
- `security.request_id_header`: optional request ID header name.
- `security.redacted_header_denylist`: optional list of exact HTTP header names that must be redacted in logs or diagnostics. This augments later code heuristics; it does not replace automatic redaction of obvious secrets.

```yaml
security:
  trusted_proxy_headers: false
  request_id_header: "X-Request-ID"
  redacted_header_denylist:
    - "Authorization"
    - "Cookie"
    - "Paddle-Signature"
```

## Rate Limit

- `rate_limit.enabled`: boolean.
- `rate_limit.requests_per_minute`: positive integer when enabled.
- `rate_limit.burst`: positive integer when enabled.
- `rate_limit.key_by`: must be `ip` for MVP.

## Metrics

- `metrics.enabled`: boolean.
- `metrics.path`: path beginning with `/`, usually `/metrics`.

When disabled, the metrics route is not registered.

Metrics include HTTP request counts, auth failures, rate limits, publish latency,
RabbitMQ reconnect/confirm/retry/DLQ counters, and config load failures. Startup
config failures still block serving traffic, so config failure metrics are
recorded in-process before exit and are most useful for tests or future reload
paths.

## Message Queue

`message_queue.type` selects the message queue backend. Only `rabbitmq` is supported in MVP, but the root is generic so future providers such as SQS or BullMQ can be added without changing the top-level shape.

RabbitMQ fields live under `message_queue.rabbitmq`:

- `message_queue.rabbitmq.url`: required AMQP URL, usually `${RABBITMQ_URL}`.
- `message_queue.rabbitmq.exchange`: required direct exchange name.
- `message_queue.rabbitmq.exchange_type`: required, use `direct` for MVP.
- `message_queue.rabbitmq.durable`: boolean for exchange/queue durability.
- `message_queue.rabbitmq.publish_timeout`: optional duration, default `5s`.
- `message_queue.rabbitmq.declare_queues`: boolean. If true, Hookway declares and binds allowed queues on startup.
- `message_queue.rabbitmq.dead_letter`: optional dead-letter exchange/queue declaration settings.

```yaml
message_queue:
  type: "rabbitmq"
  rabbitmq:
    url: "${RABBITMQ_URL}"
    exchange: "hookway.webhooks"
    exchange_type: "direct"
    durable: true
    publish_timeout: "5s"
    declare_queues: true
    dead_letter:
      enabled: true
      exchange: "hookway.webhooks.dlx"
      exchange_type: "direct"
      queue: "hookway.webhooks.dlq"
      routing_key: "webhooks.dead"
      durable: true
      create_queue: true
```

Dead-letter fields:

- `enabled`: when true, queue declaration code can attach endpoint queues to the dead-letter exchange.
- `exchange`: required safe exchange name when enabled.
- `exchange_type`: required when enabled, usually `direct`.
- `queue`: required only when `create_queue` is true.
- `routing_key`: required when enabled.
- `durable`: durability for dead-letter exchange/queue declaration.
- `create_queue`: when true, queue layer can declare the dead-letter queue and bind it to the dead-letter exchange.

## Endpoints

Endpoints define the public webhook paths and target queues. Endpoint names are internal stable identifiers; the HTTP path comes from `path`, not from the endpoint name.

```yaml
endpoints:
  paddle_billing:
    enabled: true
    path: "/webhooks/paddle/billing"
    queue: "billing-events"
    create_queue: true
    max_body_bytes: 2097152
    rate_limit:
      enabled: true
      requests_per_minute: 60
      burst: 10
      key_by: "ip"
    replay_protection:
      enabled: true
      ttl: "10m"
      max_entries: 50000
    ip_allowlist:
      - "203.0.113.10"
      - "198.51.100.0/24"
    auth:
      type: "hmac_sha256"
      secret: "${PADDLE_WEBHOOK_SECRET}"
      signature_header: "Paddle-Signature"
      timestamp_tolerance: "5m"

  github_events:
    enabled: true
    path: "/webhooks/github/events"
    queue: "github-events"
    create_queue: true
    rate_limit:
      enabled: false
    replay_protection:
      enabled: true
      ttl: "5m"
      max_entries: 25000
    ip_allowlist:
      - "192.30.252.0/22"
      - "185.199.108.0/22"
      - "140.82.112.0/20"
      - "143.55.64.0/20"
    auth:
      type: "hmac_sha256"
      secret: "${GITHUB_WEBHOOK_SECRET}"
      signature_header: "X-Hub-Signature-256"
      timestamp_tolerance: "5m"
```

Endpoint fields:

- `enabled`: only enabled endpoints are registered.
- `path`: public HTTP path; must start with `/` and be unique among enabled endpoints.
- `queue`: target RabbitMQ routing key/queue name; must match `^[a-zA-Z0-9._-]{1,128}$`.
- `create_queue`: when true, queue layer can declare and bind this endpoint queue during startup.
- `max_body_bytes`: optional positive endpoint override. Omit or set `0` to inherit `server.max_body_bytes`.
- `rate_limit`: optional endpoint override. Omit to inherit global `rate_limit`; set `enabled: false` to disable endpoint-specific limiting in later HTTP code.
- `replay_protection`: optional endpoint replay cache settings.
- `ip_allowlist`: optional list of IP addresses or CIDR ranges accepted for this endpoint.
- `auth`: endpoint authentication settings.

Endpoint rate-limit fields match global rate-limit fields. `key_by` must be `ip` for MVP.

Replay protection fields:

- `enabled`: enables endpoint replay protection in later HTTP code.
- `ttl`: positive duration for replay cache entries.
- `max_entries`: positive per-process entry cap.

Replay protection is in-process schema/config for MVP. In multi-node deployments, each node has its own replay cache unless later code uses shared storage, so identical replayed requests can pass if routed to a different node.

Auth type `hmac_sha256` verifies headers shaped like `ts=<unix>;h1=<hex_hmac>`.
