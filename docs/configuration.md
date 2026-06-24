# Configuration

Hookway reads one YAML config file. Set it with `HOOKWAY_CONFIG` or pass `--config`; the flag wins. YAML string values support `${ENV_NAME}` expansion.

Startup parses and validates the config before connecting to the message queue or serving traffic. Invalid YAML, unknown fields, missing required values, invalid durations, duplicate or overlapping endpoint paths, and unsupported backend/auth types stop the app with field-level errors and suggestions.

Example validation output:

```txt
config validation failed
- server.addr: listen address is required Suggestion: set server.addr to a host:port such as ":8080"
- endpoints.paddle_billing.auth.secret: secret is required Suggestion: reference an environment variable such as ${WEBHOOK_SECRET}
```

## Server

- `server.addr`: required listen address, example `":8080"`.
- `server.read_timeout`: optional duration, default `5s`.
- `server.write_timeout`: optional duration, default `10s`.
- `server.idle_timeout`: optional duration, default `60s`.
- `server.shutdown_timeout`: optional duration, default `10s`.
- `server.max_body_bytes`: required positive integer.
- `server.max_inflight_requests`: optional non-negative integer. When `> 0`, caps concurrently processed webhook requests; once reached, further requests are shed with `503 Service Unavailable` and a `Retry-After` header before any body is buffered. `0` (default) means unlimited. Health, readiness, and metrics endpoints are never subject to this limit.
- `server.fanout_concurrency`: optional non-negative integer. Caps concurrent publishes per webhook request when an endpoint has multiple destination routes. `0` (default) uses `8`.

## Security

- `security.trusted_proxy_headers`: optional boolean. When false (default), the source IP is always the TCP peer (`RemoteAddr`) and `X-Forwarded-For`/`X-Real-IP` are ignored.
- `security.trusted_proxies`: optional list of IP addresses or CIDR ranges. When `trusted_proxy_headers` is true *and* this list is non-empty, forwarding headers are only honored when the immediate TCP peer falls inside one of these ranges; otherwise the peer IP is used. This prevents clients from spoofing `X-Forwarded-For` to bypass the IP allowlist and per-IP rate limiting. Leaving it empty with the flag on is the legacy permissive behavior and should only be used when clients cannot reach Hookway directly.
- `security.request_id_header`: optional request ID header name.
- `security.redacted_header_denylist`: optional list of exact HTTP header names that are redacted in logs and in the published envelope. This augments automatic redaction of obvious secrets; it does not replace it.
- `security.forwarded_header_allowlist`: optional list of HTTP header names. When non-empty, only these headers are logged and forwarded to the queue (fail-safe); every other header is dropped entirely. The denylist and built-in sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through. When empty, behavior is the legacy denylist filter (everything except denylisted/sensitive headers is forwarded).

```yaml
security:
  trusted_proxy_headers: true
  trusted_proxies:
    - "10.0.0.0/8"
    - "192.168.0.0/16"
  request_id_header: "X-Request-ID"
  redacted_header_denylist:
    - "Authorization"
    - "Cookie"
    - "Paddle-Signature"
    - "X-Hub-Signature-256"
  # Optional fail-safe: forward only these headers to consumers/logs.
  forwarded_header_allowlist:
    - "Content-Type"
    - "X-Request-ID"
```

## Rate Limit

- `rate_limit.enabled`: boolean.
- `rate_limit.requests_per_minute`: positive integer when enabled.
- `rate_limit.burst`: positive integer when enabled.
- `rate_limit.key_by`: must be `ip` for MVP.

## Metrics

- `metrics.enabled`: boolean.
- `metrics.path`: path beginning with `/`, usually `/metrics`.
- `metrics.addr`: optional listen address such as `":9090"`. When set, metrics are served on a dedicated listener instead of the public webhook port, so `/metrics` is not exposed on the internet-facing port. When empty, metrics are mounted on the main server at `metrics.path`.
- `metrics.runtime_metrics`: optional boolean, default `true`. When enabled, registers the standard Go runtime and process collectors (`go_goroutines`, `go_memstats_*`, `process_cpu_seconds_total`, `process_resident_memory_bytes`, open FDs). `process_*` metrics require Linux (`/proc`); Go runtime metrics are cross-platform. Set to `false` if your platform already collects container CPU/memory (e.g. cAdvisor) and you want to avoid duplication.

When disabled, the metrics route is not registered.

Metrics include HTTP request counts, auth failures, rate limits, load-shedding
saturation (`hookway_saturated_total`), publish latency,
RabbitMQ reconnect/confirm/retry/DLQ counters, config load failures, and —
when `runtime_metrics` is enabled — Go runtime and process CPU/memory metrics. Startup
config failures still block serving traffic, so config failure metrics are
recorded in-process before exit and are most useful for tests or future reload
paths.

## Message Queue

`message_queues` is a top-level map of queue destinations keyed by stable id. Endpoint `destinations` route to these ids and choose the queue for each destination. Only `rabbitmq` is supported in MVP, but each entry keeps a `type` field so future providers such as SQS or BullMQ can be added without changing endpoint shape.

RabbitMQ fields live under `message_queues.<id>.rabbitmq`:

- `message_queues.<id>.type`: required backend type; use `rabbitmq`.
- `message_queues.<id>.rabbitmq.url`: required AMQP URL, usually `${RABBITMQ_URL}`.
- `message_queues.<id>.rabbitmq.exchange`: required direct exchange name.
- `message_queues.<id>.rabbitmq.exchange_type`: required, use `direct` for MVP.
- `message_queues.<id>.rabbitmq.durable`: boolean for exchange/queue durability.
- `message_queues.<id>.rabbitmq.publish_timeout`: optional duration, default `5s`.
- `message_queues.<id>.rabbitmq.declare_queues`: boolean. If true, Hookway declares and binds allowed queues on startup.
- `message_queues.<id>.rabbitmq.publisher_channels`: optional non-negative integer. Bounds how many AMQP channels publish concurrently; each channel carries one publisher-confirm round-trip at a time, so this is the effective publish concurrency. Defaults to `8` when omitted or `0`.
- `message_queues.<id>.rabbitmq.queue_defaults`: optional queue arguments applied to each endpoint route queue Hookway declares for that destination. Destination route `queue_options` override these defaults.
  - `message_ttl`: optional positive duration mapped to RabbitMQ `x-message-ttl` in milliseconds. Stale messages are expired from the queue.
  - `expires`: optional positive duration mapped to `x-expires` in milliseconds. Useful for temporary/test queues; usually omit for production webhook queues.
  - `max_length`: optional positive integer mapped to `x-max-length` to cap backlog size.
  - `overflow`: optional overflow mode. Supported values are `reject-publish` and `reject-publish-dlx`; `reject-publish` is recommended so providers retry instead of silently losing older webhook messages.
  - `dead_letter_routing_key`: optional default RabbitMQ `x-dead-letter-routing-key` override.
- `message_queues.<id>.rabbitmq.tls`: optional TLS settings for `amqps://` URLs. All fields are optional — with no `tls` block, `amqps://` still works against brokers whose certificates chain to the system trust store (e.g. CloudAMQP). Configure these only for private CAs, mutual TLS, or a non-default server name.
  - `ca_cert`: path to a PEM bundle to trust instead of/in addition to system roots (for private/self-signed CAs).
  - `client_cert` / `client_key`: PEM paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: disables certificate verification. Dev-only; logs a warning at startup. Do not use in production.

  Bad or unreadable CA/cert paths fail fast at startup. Note: the published container image must contain a CA bundle for `amqps://` to work at all — the provided Dockerfile copies `ca-certificates` into the `scratch` image for this reason.
- `message_queues.<id>.rabbitmq.dead_letter`: optional dead-letter exchange/queue declaration settings.

```yaml
message_queues:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${BILLING_RABBITMQ_URL}"
      exchange: "hookway.billing"
      exchange_type: "direct"
      durable: true
      publish_timeout: "5s"
      declare_queues: true
      queue_defaults:
        message_ttl: "24h"
        max_length: 100000
        overflow: "reject-publish"
      dead_letter:
        enabled: true
        exchange: "hookway.billing.dlx"
        exchange_type: "direct"
        queue: "hookway.billing.dlq"
        routing_key: "webhooks.dead"
        durable: true
        create_queue: true
  audit:
    type: "rabbitmq"
    rabbitmq:
      url: "${AUDIT_RABBITMQ_URL}"
      exchange: "hookway.audit"
      exchange_type: "direct"
      durable: true
      publish_timeout: "5s"
      declare_queues: true
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
    destinations:
      - destination_queue_id: "billing"
        queue: "billing-events"
        create_queue: true
        queue_options:
          message_ttl: "72h"
      - destination_queue_id: "audit"
        queue: "billing-audit-events"
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
      scheme: "hmac_sha256_ts_colon"
      secret: "${WEBHOOK_SECRET}"
      signature_header: "Paddle-Signature"
      timestamp_tolerance: "5m"

  github_events:
    enabled: true
    path: "/webhooks/github/events"
    destinations:
      - destination_queue_id: "billing"
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
      scheme: "hmac_sha256_hex"
      secret: "${GITHUB_WEBHOOK_SECRET}"
      signature_header: "X-Hub-Signature-256"
      idempotency_header: "X-GitHub-Delivery"

  revenuecat_billing:
    enabled: true
    path: "/webhooks/revenuecat/billing"
    destinations:
      - destination_queue_id: "billing"
        queue: "revenuecat-events"
        create_queue: true
    replay_protection:
      enabled: true
      ttl: "10m"
      max_entries: 50000
    auth:
      type: "header_exact"
      scheme: "exact"
      secret: "${REVENUECAT_WEBHOOK_AUTH}"
      signature_header: "Authorization"
      # Strip a leading Bearer/bearer prefix before comparing to secret.
      remove_bearer: true
```

Set `REVENUECAT_WEBHOOK_AUTH` to the token value only when `remove_bearer` is true (e.g. `rc-secret`). Omit `remove_bearer` or set it to `false` when the secret must match the full header value exactly (e.g. `Bearer rc-secret`).

Endpoint fields:

- `enabled`: only enabled endpoints are registered.
- `path`: public HTTP path; must start with `/` and be unique among enabled endpoints. Parent/child overlaps such as `/webhooks/paddle` and `/webhooks/paddle/billing` are rejected to avoid ambiguous webhook routing.
- `destinations`: required list of destination route objects. Each route is published once. Multiple routes fan out concurrently up to `server.fanout_concurrency`.
- `max_body_bytes`: optional positive endpoint override. Omit or set `0` to inherit `server.max_body_bytes`.
- `rate_limit`: optional endpoint override. Omit to inherit global `rate_limit`; set `enabled: false` to disable endpoint-specific limiting in later HTTP code.
- `replay_protection`: optional endpoint replay cache settings.
- `ip_allowlist`: optional list of IP addresses or CIDR ranges accepted for this endpoint.
- `auth`: endpoint authentication settings.

Destination route fields:

- `destination_queue_id`: required target key under `message_queues`.
- `queue`: required RabbitMQ routing key/queue name for this destination; must match `^[a-zA-Z0-9._-]{1,128}$`.
- `create_queue`: when true, queue layer can declare and bind this route queue during startup.
- `queue_options`: optional RabbitMQ queue arguments. Same fields as `message_queues.<id>.rabbitmq.queue_defaults`; route values override defaults. Use this for source-specific or destination-specific retention/backlog policy. Routes sharing one queue within one destination must use identical queue options.

Endpoint rate-limit fields match global rate-limit fields. `key_by` must be `ip` for MVP.

Replay protection fields:

- `enabled`: enables endpoint replay protection in later HTTP code.
- `ttl`: positive duration for replay cache entries.
- `max_entries`: positive per-process entry cap.

Replay protection is in-process schema/config for MVP. In multi-node deployments, each node has its own replay cache unless later code uses shared storage, so identical replayed requests can pass if routed to a different node. The reservation is only committed after a successful publish, so a transient broker outage does not turn a provider's legitimate retry into a permanent `409 replay detected`.

Fan-out is at-least-once per destination, not atomic across destinations. If one destination succeeds and a later destination fails, Hookway returns `503` so the provider retries. Earlier destinations may receive the same event again, so consumers must dedupe with `idempotency_key`.

Auth fields:

- `type`: authentication family; `hmac_sha256` for HMAC body signatures (the digest is chosen per-scheme — most use SHA-256, `hmac_sha1_hex` uses SHA-1), `header_exact` for static header secrets, `jwt` for Bearer JSON Web Tokens, `no_auth` to disable verification entirely.
- `scheme`: the wire format *within* the auth type. Scheme names describe the mechanism (digest, encoding, and whether the header carries a signed timestamp), not any provider. Optional for `hmac_sha256`, defaults to `hmac_sha256_ts_colon`. Built-in schemes:
  - `hmac_sha256_ts_colon` (`hmac_sha256`): timestamped, labeled header `ts=<unix>;h1=<hex_hmac>` validated over `ts + ":" + raw_body`; requires `timestamp_tolerance`. *Example: Paddle's `Paddle-Signature`.*
  - `hmac_sha256_ts_dot` (`hmac_sha256`): timestamped, labeled header `t=<unix>,v1=<hex_hmac>` validated over `t + "." + raw_body`; requires `timestamp_tolerance`. Extra comma-separated pairs are ignored. *Example: Stripe's `Stripe-Signature` (its legacy `v0=` pair is ignored).*
  - `hmac_sha256_hex` (`hmac_sha256`): prefixed hex HMAC-SHA256 over the exact raw body, no signed timestamp (so `timestamp_tolerance` is not required); the header carries a `<prefix>=<hex>` value. *Example: GitHub's `X-Hub-Signature-256: sha256=<hex>`.*
  - `hmac_sha256_base64` (`hmac_sha256`): raw **base64** HMAC-SHA256 over the exact raw body (no prefix, no timestamp). *Example: Shopify's `X-Shopify-Hmac-SHA256`.*
  - `hmac_sha1_hex` (`hmac_sha256`): prefixed hex HMAC-**SHA1** over the exact raw body; for legacy integrations that have not migrated to SHA-256. *Example: GitHub's legacy `X-Hub-Signature: sha1=<hex>`.*
  - `exact` (`header_exact`): constant-time equality check between `signature_header` and `secret`; body is not verified. Use `remove_bearer: true` to strip a leading `Bearer`/`bearer` prefix before comparing. *Example: a static token such as RevenueCat's `Authorization` header.*
  - `basic` (`header_exact`): HTTP Basic — `Authorization: Basic <base64(user:pass)>`; constant-time compare against the configured `user:pass` `secret`; body not verified.
  - `no_auth` (`no_auth`): **accepts every request without verification.** Not recommended — Hookway logs a startup warning for any endpoint using it. Reserve it for trusted internal traffic where authentication is enforced upstream (e.g. a private network or gateway). `secret` and `signature_header` are not required for this scheme.
  - `jwt_hs256` / `jwt_rs256` / `jwt_es256` (`jwt`): verify a Bearer JWT read from `signature_header` (typically `Authorization`). Each scheme pins exactly one signing algorithm — a token whose `alg` does not match (including `alg: none`) is rejected, defeating algorithm-confusion attacks. `secret` holds the key: the shared HMAC secret for `jwt_hs256`, or a PEM-encoded public key for `jwt_rs256` (RSA) / `jwt_es256` (EC P-256). `exp` is required; `timestamp_tolerance` is the clock-skew leeway. Optional `expected_issuer`/`expected_audience` enforce `iss`/`aud`. Optional `body_hash_claim` names a claim carrying the hex SHA-256 of the raw body, binding the token to the payload (without it, the JWT authenticates the caller but not the body).
  - New providers are added by registering a scheme in `internal/auth`, without changing the request path or config shape.
- `secret`: required (except for `no_auth`); reference an environment variable such as `${WEBHOOK_SECRET}`. For `jwt_rs256` and `jwt_es256`, this must be a PEM-encoded public key (e.g. `${JWT_PUBLIC_KEY}`).
- `signature_header`: required HTTP header Hookway reads for verification (signature or static secret); not required for `no_auth`. For `jwt` schemes, set to `Authorization` (the JWT is read as `Bearer <token>`).
- `remove_bearer`: optional; when `true` on `header_exact` endpoints, strips a leading `Bearer`/`bearer` prefix (case-insensitive) and following whitespace from the header value before comparing to `secret`. Ignored for `hmac_sha256` endpoints (config validation rejects it).
- `timestamp_tolerance`: positive duration; required only for timestamped HMAC schemes (e.g. `hmac_sha256_ts_colon`). For `jwt` schemes, used as clock-skew leeway for `exp`/`nbf` validation.
- `idempotency_header`: optional header carrying a stable provider delivery/event id (e.g. `X-GitHub-Delivery`). Its value becomes the envelope `idempotency_key` and the AMQP `x-idempotency-key` header so consumers can deduplicate. When omitted on `hmac_sha256` endpoints, Hookway falls back to a hash of the verified signature. When omitted on `header_exact` endpoints, Hookway falls back to a hash of the raw body.
- `expected_issuer`: optional; for `jwt` schemes, enforces the `iss` claim must match this value.
- `expected_audience`: optional; for `jwt` schemes, enforces the `aud` claim must match this value.
- `body_hash_claim`: optional; for `jwt` schemes, names a JWT claim containing the hex-encoded SHA-256 hash of the raw request body. When set, Hookway rejects any request where the body does not match the hash in the token, binding the JWT to the exact payload and preventing body-swap attacks.
