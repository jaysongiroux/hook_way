# Security

Hookway verifies generic HMAC SHA256 webhook signatures before publishing to RabbitMQ. Paddle is one supported example because its signature format uses timestamped HMAC SHA256, but `auth.type: hmac_sha256` is provider-neutral.

## HMAC SHA256 Signatures

Hookway reads the exact raw request body once. It reads the configured `signature_header`, extracts `ts` and `h1`, builds `ts + ":" + raw_body`, computes HMAC-SHA256 using the configured endpoint secret, hex-encodes it, and compares it to `h1` using `hmac.Equal`.

The body is not parsed, trimmed, normalized, or re-serialized before verification. The endpoint secret is used as a raw string and is not base64-decoded.

Requests outside `timestamp_tolerance` are rejected with `401 Unauthorized`.

## Secrets

Put secrets in environment variables and reference them from YAML with `${ENV_NAME}`. Hookway validates required secrets but does not print expanded secret values.

## Request Handling

- Only configured endpoint paths are accepted for ingress.
- Empty bodies are rejected.
- Body size is capped by `server.max_body_bytes`.
- Server timeouts are configured from YAML.
- Shutdown handles `SIGINT` and `SIGTERM`.
- `/healthz` reports process liveness for container healthchecks.
- `/readyz` reports message-queue readiness.
- `/health` reports the same readiness state as `/readyz`.

## Rate Limiting

The MVP rate limiter is in-memory and keyed by source IP. `X-Forwarded-For` and `X-Real-IP` are ignored unless `security.trusted_proxy_headers` is true.

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
