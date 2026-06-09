# Security

Hookway verifies HMAC SHA256 webhook signatures before publishing to RabbitMQ. The signature *layout* is selected per endpoint by `auth.scheme`, so the proxy is provider-neutral: Paddle is the default scheme today, and additional providers are added by registering a scheme in `internal/auth` rather than branching in the request path.

## Signature Schemes

Each endpoint builds its verifier once at startup from `auth.scheme` (default `paddle`). An unknown scheme fails closed — the endpoint rejects every request rather than accepting unverified traffic.

### `paddle` (timestamped)

Hookway reads the exact raw request body once, reads the configured `signature_header`, extracts one `ts` value and one `h1` value, builds `ts + ":" + raw_body`, computes HMAC-SHA256 using the configured endpoint secret, hex-encodes it, and compares it to `h1` using `hmac.Equal`. Requests outside `timestamp_tolerance` are rejected with `401 Unauthorized`. Empty secrets, non-positive timestamp tolerances, malformed signatures, duplicate signature keys, and non-SHA256-length HMAC values fail closed.

### `github`

Hookway reads the `X-Hub-Signature-256` header shaped like `sha256=<hex>`, computes HMAC-SHA256 over the exact raw body with the endpoint secret, and compares using `hmac.Equal`. GitHub does not sign a timestamp, so `timestamp_tolerance` is not used. A missing/short/tampered signature or empty secret fails closed.

In all schemes the body is not parsed, trimmed, normalized, or re-serialized before verification, and the endpoint secret is used as a raw string (not base64-decoded).

## Secrets

Put secrets in environment variables and reference them from YAML with `${ENV_NAME}`. Hookway validates required secrets but does not print expanded secret values.

## Request Handling

- Only configured endpoint paths are accepted for ingress.
- Empty bodies are rejected.
- Body size is capped by `server.max_body_bytes`.
- Server timeouts are configured from YAML.
- Shutdown handles `SIGINT` and `SIGTERM`.
- `/healthz` reports process liveness for container healthchecks.
- `/ready` reports message-queue readiness.
- `/readyz` reports message-queue readiness.
- `/health` reports the same readiness state as `/readyz`.

## Rate Limiting

Rate limiting uses `golang.org/x/time/rate` token buckets keyed by source IP. Buckets are stored in process memory and idle keys are evicted to cap growth from one-off IPs. `X-Forwarded-For` and `X-Real-IP` are ignored unless `security.trusted_proxy_headers` is true. When `security.trusted_proxies` is configured, forwarding headers are honored only when the immediate TCP peer is within one of those CIDR ranges; otherwise the peer IP is used. This stops a client from spoofing `X-Forwarded-For` to evade the IP allowlist or per-IP rate limit. Configure `trusted_proxies` to your load balancer/ingress ranges in production.

For horizontally scaled deployments, use a shared edge or gateway limiter, or replace the process-local limiter with Redis or another shared backend. Process-local rate limits are useful as defense in depth but do not enforce a global quota across replicas.

## Replay Protection

Replay protection is process-local. It blocks repeated signatures seen by the same process, but a replay can pass if a load balancer sends it to a different replica. Production deployments that require strict replay rejection should use sticky routing by endpoint/signature or a shared replay store such as Redis.

## Header Redaction and Allowlisting

Published envelopes and request logs redact:

- `authorization`
- `cookie`
- `set-cookie`
- `paddle-signature`
- any header containing `secret`, `token`, or `signature`
- any header named in `security.redacted_header_denylist`

Redacted values are published as `"redacted"`.

By default this is a denylist (fail-open): headers not matched above are forwarded. For a fail-safe posture, set `security.forwarded_header_allowlist`; only allowlisted headers are then logged or forwarded to consumers, and everything else is dropped. The denylist/sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through.

## Idempotency

Each published envelope carries an `idempotency_key` (also set as the AMQP `x-idempotency-key` header) so consumers can deduplicate at-least-once deliveries. The key is taken from the per-endpoint `auth.idempotency_header` (e.g. GitHub's `X-GitHub-Delivery`) when configured, and otherwise derived from a hash of the verified signature, which is stable across provider retries of the same event. Because delivery is at-least-once and replay protection is process-local, consumers should treat the idempotency key as authoritative for deduplication.

## Availability Model

Hookway does not durably spool requests locally. A webhook is acknowledged with `202 Accepted` only after RabbitMQ confirms the publish; if the broker is unavailable after retries, Hookway responds `503 Service Unavailable` and relies on the provider's own retry policy to redeliver. Under load shedding (`server.max_inflight_requests`) it also responds `503` with `Retry-After`. Operate behind providers that retry on 5xx, and size `publisher_channels`/`max_inflight_requests` for expected burst volume. The replay reservation is released on publish failure, so legitimate provider retries after a transient outage are not rejected as replays.

## Logging

Logs include request method, path, endpoint, queue, status, and duration. Raw webhook payloads and secrets are not logged.

## Envelope Compatibility

`schema_version` is the consumer contract for published envelopes. Patch/minor changes may add fields or enum values without removing existing fields. Breaking payload shape, field meaning, or required-field changes require a new schema version and a migration window for consumers.

## Production Hardening

- Terminate TLS at a trusted load balancer or reverse proxy. Enable `trusted_proxy_headers` only with `trusted_proxies` set to your proxy/ingress CIDRs so forwarded client IPs cannot be spoofed.
- Keep RabbitMQ durable exchanges/queues enabled for production and configure dead-letter queues for failed consumers.
- Run at least two replicas, but treat rate limiting and replay protection as process-local until a shared backend is added.
- Keep `server.max_body_bytes` tight per endpoint and set `server.max_inflight_requests` to bound memory and shed load under flood.
- Tune `message_queue.rabbitmq.publisher_channels` to the desired publish concurrency (default 8).
- Serve metrics on a private port via `metrics.addr` so `/metrics` is not exposed on the internet-facing webhook listener.
- Forward structured logs and Prometheus metrics to centralized systems, and alert on auth failures, rate limits, saturation (`hookway_saturated_total`), publish failures, and readiness failures.
- Set `security.forwarded_header_allowlist` so only intended headers reach consumers and logs.
- Rotate webhook secrets per endpoint and keep old/new overlap short during provider-side rotations.
