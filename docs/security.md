# Security

Hookway verifies generic HMAC SHA256 webhook signatures before publishing to RabbitMQ. Paddle is one supported example because its signature format uses timestamped HMAC SHA256, but `auth.type: hmac_sha256` is provider-neutral.

## HMAC SHA256 Signatures

Hookway reads the exact raw request body once. It reads the configured `signature_header`, extracts one `ts` value and one `h1` value, builds `ts + ":" + raw_body`, computes HMAC-SHA256 using the configured endpoint secret, hex-encodes it, and compares it to `h1` using `hmac.Equal`.

The body is not parsed, trimmed, normalized, or re-serialized before verification. The endpoint secret is used as a raw string and is not base64-decoded.

Requests outside `timestamp_tolerance` are rejected with `401 Unauthorized`. Empty secrets, non-positive timestamp tolerances, malformed signatures, duplicate signature keys, and non-SHA256-length HMAC values fail closed.

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

Rate limiting uses `golang.org/x/time/rate` token buckets keyed by source IP. Buckets are stored in process memory and idle keys are evicted to cap growth from one-off IPs. `X-Forwarded-For` and `X-Real-IP` are ignored unless `security.trusted_proxy_headers` is true.

For horizontally scaled deployments, use a shared edge or gateway limiter, or replace the process-local limiter with Redis or another shared backend. Process-local rate limits are useful as defense in depth but do not enforce a global quota across replicas.

## Replay Protection

Replay protection is process-local. It blocks repeated signatures seen by the same process, but a replay can pass if a load balancer sends it to a different replica. Production deployments that require strict replay rejection should use sticky routing by endpoint/signature or a shared replay store such as Redis.

## Header Redaction

Published envelopes redact:

- `authorization`
- `cookie`
- `set-cookie`
- `paddle-signature`
- any header containing `secret`, `token`, or `signature`

Redacted values are published as `"redacted"`.

## Logging

Logs include request method, path, endpoint, queue, status, and duration. Raw webhook payloads and secrets are not logged.

## Envelope Compatibility

`schema_version` is the consumer contract for published envelopes. Patch/minor changes may add fields or enum values without removing existing fields. Breaking payload shape, field meaning, or required-field changes require a new schema version and a migration window for consumers.

## Production Hardening

- Terminate TLS at a trusted load balancer or reverse proxy, and only enable `trusted_proxy_headers` when direct clients cannot spoof those headers.
- Keep RabbitMQ durable exchanges/queues enabled for production and configure dead-letter queues for failed consumers.
- Run at least two replicas, but treat rate limiting and replay protection as process-local until a shared backend is added.
- Keep `server.max_body_bytes` tight per endpoint and use endpoint-specific overrides for providers with known payload sizes.
- Forward structured logs and Prometheus metrics to centralized systems, and alert on auth failures, rate limits, publish failures, and readiness failures.
- Rotate webhook secrets per endpoint and keep old/new overlap short during provider-side rotations.
