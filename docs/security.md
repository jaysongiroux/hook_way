# Security

Hookway verifies webhook authentication before publishing to its destinations (RabbitMQ, SQS, or SNS). Auth **type** selects the verifier family (`hmac_sha256`, `header_exact`, `jwt`, or `no_auth`); **scheme** selects the header layout within that family. Scheme names describe the mechanism (digest, encoding, signed-timestamp), not any provider. Additional providers are added by registering a scheme in `internal/auth` rather than branching in the request path. The full scheme list lives in [Configuration](configuration.md#endpoints).

## Auth Schemes

Each endpoint builds its verifier once at startup from `auth.type` and `auth.scheme` (HMAC schemes default to `hmac_sha256_ts_colon` when omitted). An unknown or mismatched type/scheme pair fails closed — the endpoint rejects every request rather than accepting unverified traffic.

### `hmac_sha256_ts_colon` (timestamped, `hmac_sha256`)

The default HMAC scheme (used by Paddle's `Paddle-Signature`). Hookway reads the exact raw request body once, reads the configured `signature_header`, extracts one `ts` value and one `h1` value, builds `ts + ":" + raw_body`, computes HMAC-SHA256 using the configured endpoint secret, hex-encodes it, and compares it to `h1` using `hmac.Equal`. Requests outside `timestamp_tolerance` are rejected with `401 Unauthorized`. Empty secrets, non-positive timestamp tolerances, malformed signatures, duplicate signature keys, and non-SHA256-length HMAC values fail closed.

### `hmac_sha256_hex` (`hmac_sha256`)

Prefixed hex HMAC-SHA256 over the exact raw body, no signed timestamp (used by GitHub's `X-Hub-Signature-256: sha256=<hex>`). Hookway computes HMAC-SHA256 over the exact raw body with the endpoint secret and compares using `hmac.Equal`. There is no signed timestamp, so `timestamp_tolerance` is not used. A missing/short/tampered signature or empty secret fails closed.

### `jwt_hs256` / `jwt_rs256` / `jwt_es256` (`jwt`)

Verify a Bearer JWT read from `signature_header` (typically `Authorization`). Each scheme pins exactly one signing algorithm, so a token whose `alg` does not match (including `alg: none`) is rejected. `secret` holds the HMAC secret (`jwt_hs256`) or a PEM public key (`jwt_rs256`/`jwt_es256`). `exp` is required and `timestamp_tolerance` is the clock-skew leeway; optional `expected_issuer`/`expected_audience`/`body_hash_claim` enforce `iss`/`aud`/body binding.

### `no_auth` (`no_auth`)

Accepts every request without verification. Hookway logs a startup warning for any endpoint using it. Reserve it for trusted internal traffic where authentication is enforced upstream.

### `exact` (`header_exact`)

For providers that authenticate with a static shared secret sent in a request header (e.g. RevenueCat `Authorization`), set `auth.type` to `header_exact` and `auth.scheme` to `exact`. Hookway reads `signature_header` and compares its value to `secret` using a constant-time equality check. The raw body is not part of verification.

Set `auth.remove_bearer: true` when the provider sends a `Bearer` or `bearer` prefix but your configured `secret` stores only the token value. Hookway strips the prefix (case-insensitively) and any following whitespace before comparing.

Because the header value is identical on every request, replay protection and idempotency fall back to a SHA-256 hash of the raw body when `idempotency_header` is not set. Configure `idempotency_header` when the provider sends a per-delivery header.

For HMAC schemes the body is not parsed, trimmed, normalized, or re-serialized before verification, and the endpoint secret is used as a raw string (not base64-decoded).

## Secrets

Put secrets in environment variables and reference them from YAML with `${ENV_NAME}`. Hookway validates required secrets but does not print expanded secret values.

## Request Handling

- Only configured endpoint paths are accepted for ingress.
- Empty bodies are rejected.
- Body size is capped by `server.max_body_bytes`.
- Server timeouts are configured from YAML.
- Shutdown handles `SIGINT` and `SIGTERM`.
- `/healthz` reports process liveness for container healthchecks.
- `/ready` reports readiness for every message queue used by enabled endpoints, including every fan-out destination. Readiness is maintained by a background prober (server.readiness_interval / readiness_timeout) and served from cache, so kubelet probes never trigger a live broker fan-out and one flaky destination cannot exceed the probe deadline.
- `/readyz` reports the same cached readiness state as `/ready`.
- `/health` is a liveness alias for `/healthz` and does NOT report readiness, so infrastructure treating `/health` as a liveness probe will not restart pods on transient destination outages.

## Rate Limiting

Rate limiting uses `golang.org/x/time/rate` token buckets keyed by source IP. Buckets are stored in process memory and idle keys are evicted to cap growth from one-off IPs. `X-Forwarded-For` and `X-Real-IP` are ignored unless `security.trusted_proxy_headers` is true. When `security.trusted_proxies` is configured, forwarding headers are honored only when the immediate TCP peer is within one of those CIDR ranges; otherwise the peer IP is used.

When `X-Forwarded-For` is honored, Hookway does **not** take the leftmost entry (that value is client-supplied and trivially spoofed). Instead it walks the comma-separated list right to left — the direction proxies append in — skipping any entry that itself falls within `trusted_proxies`, and uses the first (rightmost) entry that isn't a known proxy as the client IP. If every entry is a trusted proxy, the leftmost entry is used. A malformed entry stops the walk immediately and falls back to the last known-good hop rather than continuing toward the client-controlled end, so a crafted invalid entry can't be used to smuggle a different address into scope. This stops a client from spoofing `X-Forwarded-For` (e.g. prepending an allowlisted IP, or rotating fake addresses) to evade the IP allowlist or per-IP rate limit. Configure `trusted_proxies` to your load balancer/ingress ranges in production; leaving `trusted_proxy_headers` on with `trusted_proxies` empty trusts any peer and treats the rightmost `X-Forwarded-For` entry as the client IP with no proxy hops recognized to skip.

For horizontally scaled deployments, use a shared edge or gateway limiter, or replace the process-local limiter with Redis or another shared backend. Process-local rate limits are useful as defense in depth but do not enforce a global quota across replicas.

## Replay Protection

Replay protection is process-local. It blocks repeated signatures seen by the same process, but a replay can pass if a load balancer sends it to a different replica. Production deployments that require strict replay rejection should use sticky routing by endpoint/signature or a shared replay store such as Redis.

## Header Redaction and Allowlisting

Published envelopes and request logs redact:

- `authorization`
- `cookie`
- `set-cookie`
- `paddle-signature`
- any header containing `secret`, `token`, `signature`, `key`, or `auth` (e.g. `X-Api-Key`, `X-Auth-Token`)
- any header named in `security.redacted_header_denylist`

Redacted values are published as `"redacted"`.

By default this is a denylist (fail-open): headers not matched above are forwarded. For a fail-safe posture, set `security.forwarded_header_allowlist`; only allowlisted headers are then logged or forwarded to consumers, and everything else is dropped. The denylist/sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through.

## Idempotency

Each published envelope carries an `idempotency_key` (also set as the AMQP `x-idempotency-key` header) so consumers can deduplicate at-least-once deliveries. The key is taken from the per-endpoint `auth.idempotency_header` (e.g. GitHub's `X-GitHub-Delivery`) when configured. For `hmac_sha256` endpoints without that header, Hookway hashes the verified signature header value, which is stable across provider retries of the same event. For `header_exact` endpoints without `idempotency_header`, Hookway hashes the raw body instead, because the auth header value is static across requests.

For fan-out endpoints, delivery is not atomic across destinations. Confirmed broker messages cannot be canceled or recalled. With the default `delivery_failure_policy: stop_on_failure`, Hookway publishes routes sequentially; if an earlier destination accepts a publish and a later destination fails, Hookway returns `503` so the provider retries. Earlier destinations can see the same event again and must dedupe by `idempotency_key`. With `delivery_failure_policy: skip_failed`, Hookway attempts every route and acknowledges partial success with `202` when at least one destination succeeds; that partial-success `202` also releases the replay reservation (see below), so an identical sender retry is accepted again rather than rejected, and can redeliver to the destinations that failed the first time. That retry re-delivers to the destinations that already succeeded too; consumers dedupe that duplicate by `idempotency_key`, identical to the retry contract `stop_on_failure` already relies on.

## Availability Model

Hookway does not durably spool requests locally. With `stop_on_failure`, a webhook is acknowledged with `202 Accepted` only after every endpoint destination route confirms the publish; if a broker is unavailable after retries, Hookway responds `503 Service Unavailable` and relies on the provider's own retry policy to redeliver. With `skip_failed`, Hookway acknowledges partial success with `202` and includes failed routes in `failed_destinations`; if every route fails, it returns `503`. Under load shedding (`server.max_inflight_requests`) it also responds `503` with `Retry-After`. Operate behind providers that retry on 5xx, and size each destination's `publisher_channels`, per-request `server.fanout_concurrency`, and global `max_inflight_requests` for expected burst volume. The replay reservation is released on a full-failure `503` and on a partial-failure `202` (`skip_failed` only), so a legitimate provider retry is never permanently rejected as a replay while destinations still need the event; it is kept only when every destination has confirmed the publish.

The webhook publish itself runs detached from the inbound HTTP request's context (`server.publish_timeout`, default `10s`, matching `write_timeout`/`shutdown_timeout`): once the request body is read and the signature verified, a sender disconnect or client-side timeout no longer aborts the in-flight fan-out, since that work is already committed to being delivered. The detached publish is still bounded by `server.publish_timeout` so a stalled destination cannot hang a delivery indefinitely; exceeding it fails the publish (surfaced as `503`) the same as any other publish error. Config validation requires `publish_timeout <= shutdown_timeout` and `publish_timeout <= write_timeout`: a sync-mode publish that outlives either would make a clean shutdown falsely report failure, or make the sender see a spurious timeout and retry after the response can no longer be written.

**Readiness-aggregation trade-off:** the background readiness prober (`/ready`, `/readyz`) joins per-destination readiness with AND semantics — the pod is only reported ready when *every* configured destination is ready. An async webhook destination reports not-ready once its delivery queue reaches 90% of capacity (backpressure signal for a saturated/backed-up destination). Because readiness is joined across all destinations, one saturated or backed-up async webhook destination pulls the *whole* pod out of Service/load-balancer rotation — including traffic for endpoints that don't route to that destination at all — until its queue drains back below 90%. This is deliberate: it is safer to shed the pod's share of traffic than to keep accepting requests a known-saturated destination can't keep up with. `/health`/`/healthz` stay liveness-only and never reflect destination readiness, so this trade-off never triggers a restart loop — it only affects load-balancer routing, and self-heals as the queue drains. Operators running multiple webhook destinations per pod should size each destination's queue (`webhook.max_queue`) and consumer capacity so that a slow destination doesn't route pods with otherwise-healthy destinations out of rotation.

## Logging

Logs include request method, path, endpoint, destination queue id, queue, status, and duration. Raw webhook payloads and secrets are not logged.

## Envelope Compatibility

`schema_version` is the consumer contract for published envelopes. Patch/minor changes may add fields or enum values without removing existing fields. Breaking payload shape, field meaning, or required-field changes require a new schema version and a migration window for consumers.

## Metrics & config-API authentication

Set `security.api_key` to require `Authorization: Bearer <key>` on the metrics
endpoint and on the `hookway editor` config API. Health endpoints
(`/health`, `/healthz`, `/ready`, `/readyz`) are never authenticated.

Prometheus scrape config when a key is set:

```yaml
scrape_configs:
  - job_name: hookway
    authorization:
      type: Bearer
      credentials: "<your-api-key>"
    static_configs:
      - targets: ["hookway:9090"]
```

## Replay

The optional replay store and its API (see [Configuration § Replay](configuration.md#replay)) re-deliver past webhook events to their original destinations, so it carries its own authentication and isolation requirements beyond the read-only metrics/config-API key.

- **Separate listener.** The replay API is served on `replay.addr`, a dedicated listener distinct from the public webhook port (`server.addr`). Keep `replay.addr` off the internet-facing network path and behind the same network controls (firewalls, private subnets, VPN) you'd use for any admin-plane traffic — it is not designed to be reachable from the same network as webhook senders.
- **Mandatory authentication.** `replay.enabled: true` requires a non-empty `security.api_key`; config validation rejects the config otherwise. This is enforced (not merely recommended) because the replay API is not read-only — `POST /replay/replay` and `POST /replay/events/{id}/replay` re-inject stored payloads into live destinations, so an unauthenticated instance of this API would let anyone replay arbitrary captured traffic. Callers authenticate the same way as the metrics endpoint: `Authorization: Bearer <security.api_key>`.
- **Capture scope.** Only events that pass the endpoint's own authentication are ever captured — a request that fails auth verification is rejected before capture, so the replay store cannot become a side channel for unauthenticated payloads. Set `replay.skip_unauthenticated: true` to additionally exclude `no_auth` endpoints from capture, since those endpoints have no caller identity to attribute a captured event to.
- **Best-effort, non-gating capture.** Writing a captured event (or its delivery outcome) to the replay store is strictly best-effort: a store error or panic is caught, logged, and counted in `hookway_replay_capture_failures_total{op}` — it never blocks the webhook response or changes its status code. The store's reachability is exposed only as the gauge `hookway_replay_store_up` (set by the background cleanup sweep); it is observability-only and does **not** feed into `/ready`/`/readyz`, so a replay-store outage never pulls the pod out of load-balancer rotation.
- **Replay identity and idempotency.** A replayed event is published as a new event: a fresh event id, `replay_of` set to the original event's id, and hop count reset to `0`. The `reprocess` flag on both replay endpoints controls dedup behavior — `false` (default) preserves the original `idempotency_key`, so a consumer that already processed the original delivery treats the replay as a no-op; `true` generates a fresh idempotency key so consumers process it as a new event. Choose `reprocess: true` deliberately — it is what makes an operator-triggered replay actually reach a consumer that already deduplicated the first delivery.
- **v1 limitation:** an event with no recorded per-destination delivery outcome cannot be replayed (`422` on single replay; skipped in bulk replay). This includes the `stop_on_failure` full-failure path, where the first failed destination stops the fan-out before any outcome is known — the event is still captured (status `pending`) but has nothing to replay against.
- **v1 limitation:** replaying to a `proxy`-mode webhook destination is not supported (the original raw request is not persisted); such destinations are skipped on replay with a reason in the response.

## Production Hardening

- Terminate TLS at a trusted load balancer or reverse proxy. Enable `trusted_proxy_headers` only with `trusted_proxies` set to your proxy/ingress CIDRs so forwarded client IPs cannot be spoofed.
- Keep RabbitMQ durable exchanges/queues enabled for production and configure dead-letter queues for failed consumers.
- Use `amqps://` for broker connections. The container image ships the system CA bundle so publicly trusted brokers (e.g. CloudAMQP) work out of the box; for private/self-signed CAs or mutual TLS, set `destinations.<id>.rabbitmq.tls` (`ca_cert`, `client_cert`/`client_key`, `server_name`). Never enable `insecure_skip_verify` in production.
- Run at least two replicas, but treat rate limiting and replay protection as process-local until a shared backend is added.
- Keep `server.max_body_bytes` tight per endpoint and set `server.max_inflight_requests` to bound memory and shed load under flood.
- Tune `destinations.<id>.rabbitmq.publisher_channels` to the desired publish concurrency per destination (default 8).
- Tune `server.fanout_concurrency` to bound per-request goroutines and concurrent publishes across fan-out routes (default 8).
- Serve metrics on a private port via `metrics.addr` so `/metrics` is not exposed on the internet-facing webhook listener.
- If `replay` is enabled, keep `replay.addr` off the internet-facing network path (private port/network, same as `metrics.addr`) and always set `security.api_key` — config validation already requires it, but treat the key with the same care as any credential capable of re-injecting webhook traffic.
- Forward structured logs and Prometheus metrics to centralized systems, and alert on auth failures, rate limits, saturation (`hookway_saturated_total`), publish failures, and readiness failures.
- Set `security.forwarded_header_allowlist` so only intended headers reach consumers and logs.
- Rotate webhook secrets per endpoint and keep old/new overlap short during provider-side rotations.
