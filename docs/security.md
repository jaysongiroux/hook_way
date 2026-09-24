# Security

Hookway verifies webhook authentication before publishing to its destinations (RabbitMQ, SQS, SNS, S3, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal, or outbound webhook).

An `auth` node is configured by **`scheme`**, and only by `scheme`. There is no "family" field: `AuthOptions` carries a scheme and nothing else, and the family names (`hmac_sha256`, `header_exact`, `jwt`) appear below only as a grouping for readers. Writing `scheme: hmac_sha256` or `scheme: jwt` is a configuration error, not a shorthand — those strings are not registered schemes, so `NewVerifier` returns `unknown signature scheme`, the node's verifier becomes `auth.RejectAll{}`, and the flow rejects **100% of traffic** with `401`. Of those three family names only `header_exact` is accepted, as an alias for the `exact` scheme. Always name a concrete scheme from the table below.

Unverified traffic uses a separate `no_auth` node — `auth.scheme: no_auth` is rejected. Scheme names describe the mechanism (digest, encoding, signed-timestamp), not any provider. Additional providers are added by registering a scheme in `internal/auth` rather than branching in the request path. The same list, with config detail, is in [Configuration](configuration.md#auth).

## Auth Schemes

Each `auth` node requires an explicit `scheme`. Inline secrets build a verifier at startup; `secret_ref` uses the cached current/overlap values. An unknown scheme fails closed — the flow rejects every request rather than accepting unverified traffic.

Eleven schemes are registered (`internal/auth/registry.go`). All of them:

| Scheme | Family | Verifies | Notes |
|---|---|---|---|
| `hmac_sha256_ts_colon` | `hmac_sha256` | timestamp + raw body | signed timestamp; see below |
| `hmac_sha256_ts_dot` | `hmac_sha256` | timestamp + raw body | signed timestamp; see below |
| `hmac_sha256_hex` | `hmac_sha256` | raw body | see below |
| `hmac_sha256_base64` | `hmac_sha256` | raw body | see below |
| **`hmac_sha1_hex`** | `hmac_sha256` | raw body | **SHA-1** — see the warning below |
| `exact` | `header_exact` | a static header value | body not verified |
| `basic` | `header_exact` | HTTP Basic credentials | body not verified |
| `jwt_hs256` | `jwt` | Bearer JWT (HMAC) | `body_hash_claim` binds the body |
| `jwt_rs256` | `jwt` | Bearer JWT (RSA) | `body_hash_claim` binds the body |
| `jwt_es256` | `jwt` | Bearer JWT (ECDSA) | `body_hash_claim` binds the body |
| `no_auth` | `no_auth` | nothing | only via the separate `no_auth` node |

### `hmac_sha256_ts_colon` (timestamped, `hmac_sha256`)

The default HMAC scheme (used by Paddle's `Paddle-Signature`). Hookway reads the exact raw request body once, reads the configured `signature_header`, extracts one `ts` value and one `h1` value, builds `ts + ":" + raw_body`, computes HMAC-SHA256 using the configured endpoint secret, hex-encodes it, and compares it to `h1` using `hmac.Equal`. Requests outside `timestamp_tolerance` are rejected with `401 Unauthorized`. Empty secrets, non-positive timestamp tolerances, malformed signatures, duplicate signature keys, and non-SHA256-length HMAC values fail closed.

### `hmac_sha256_hex` (`hmac_sha256`)

Prefixed hex HMAC-SHA256 over the exact raw body, no signed timestamp (used by GitHub's `X-Hub-Signature-256: sha256=<hex>`). Hookway computes HMAC-SHA256 over the exact raw body with the endpoint secret and compares using `hmac.Equal`. There is no signed timestamp, so `timestamp_tolerance` is not used. A missing/short/tampered signature or empty secret fails closed.

### `hmac_sha256_ts_dot` (timestamped, `hmac_sha256`)

Timestamped HMAC-SHA256 with a dot separator (used by Stripe's `Stripe-Signature`). Hookway parses a labeled, comma-delimited header — `t=<unix>,v1=<hex>` — using only `t` and the first `v1`, ignoring any other pairs; builds `t + "." + raw_body`; computes HMAC-SHA256 over those exact bytes with the endpoint secret; hex-encodes it; and compares to `v1` with `hmac.Equal`. Requests outside `timestamp_tolerance` are rejected `401`. A missing `t` or `v1`, an empty secret, or a non-positive tolerance fails closed.

### `hmac_sha256_base64` (`hmac_sha256`)

Raw base64 HMAC-SHA256 over the exact raw body, with no prefix and no signed timestamp (used by Shopify's `X-Shopify-Hmac-SHA256`). The header value is the base64 digest by itself — Hookway does not strip a `sha256=`-style prefix for this scheme. Because there is no signed timestamp, `timestamp_tolerance` is unused; pair it with a `replay_guard` keyed on the raw body or a provider delivery id. A missing, malformed, or tampered signature, or an empty secret, fails closed.

### `hmac_sha1_hex` (`hmac_sha256`) — SHA-1

> **This scheme uses SHA-1.** It exists for providers that still only offer a legacy SHA-1 signature — GitHub's pre-2020 `X-Hub-Signature: sha1=<hex>` is the canonical case. The implementation carries a `// #nosec G401` annotation at `internal/auth/hmac.go` precisely because a SAST scanner should, and does, flag it.

Prefixed hex HMAC-SHA1 over the exact raw body, no signed timestamp. Mechanically identical to `hmac_sha256_hex` apart from the digest and the `sha1=` prefix.

HMAC-SHA1 is **not** broken in the way bare SHA-1 is: the known SHA-1 attacks are collision attacks, and HMAC's security does not rest on collision resistance, so HMAC-SHA1 has no practical forgery attack today. It is nonetheless a deprecated construction with a 160-bit digest, it will fail a modern crypto review or a FIPS/PCI control questionnaire, and it is the weakest verifier Hookway ships.

Treat it as a migration path, not a destination: **use `hmac_sha256_hex` whenever the provider offers both** (GitHub sends `X-Hub-Signature-256` alongside the legacy header — verify that one). If you must run `hmac_sha1_hex`, keep it on the one flow that needs it rather than making it a default, and revisit whenever the provider ships SHA-256.

### `basic` (`header_exact`)

HTTP Basic. Hookway reads `signature_header` (normally `Authorization`), requires a case-insensitive `Basic` keyword followed by whitespace, base64-decodes the remainder, and compares the decoded `user:pass` bytes to the configured `secret` with `subtle.ConstantTimeCompare`. The `secret` is the plaintext `user:pass` pair, not base64. The body is not part of verification. An empty secret, a missing or malformed prefix, or undecodable base64 fails closed.

Basic credentials are a static bearer value like `exact`: they are identical on every request, so derive the `replay_guard` key from the raw body or a provider delivery id rather than the auth header. Only use Basic over TLS.

### `jwt_hs256` / `jwt_rs256` / `jwt_es256` (`jwt`)

Verify a Bearer JWT read from `signature_header` (typically `Authorization`). Each scheme pins exactly one signing algorithm, so a token whose `alg` does not match (including `alg: none`) is rejected. `secret` holds the HMAC secret (`jwt_hs256`) or a PEM public key (`jwt_rs256`/`jwt_es256`). `exp` is required and `timestamp_tolerance` is the clock-skew leeway; optional `expected_issuer`/`expected_audience` enforce `iss`/`aud`. `body_hash_claim` is required and binds the token to the raw body; There is no unbound-body opt-out.

### `no_auth` node

Accepts every request without verification. Use the `no_auth` node, not `auth.scheme: no_auth`. Hookway logs a startup warning for any flow using it. Reserve it for trusted internal traffic where authentication is enforced upstream.

### `exact` (`header_exact`)

For providers that authenticate with a static shared secret sent in a request header (e.g. RevenueCat `Authorization`), set `scheme` to `exact` on an `auth` node. Hookway reads `signature_header` and compares its value to `secret` using a constant-time equality check. The raw body is not part of verification.

Set `remove_bearer: true` when the provider sends a `Bearer` or `bearer` prefix but your configured `secret` stores only the token value. Hookway strips the prefix (case-insensitively) and any following whitespace before comparing.

Because the header value is identical on every request, use a `replay_guard` key derived from the raw body (or a provider delivery id) rather than the static auth header.

For HMAC schemes the body is not parsed, trimmed, normalized, or re-serialized before verification, and the endpoint secret is used as a raw string (not base64-decoded).

## Where the credential is read from (`auth.source`)

By default an `auth` node reads its credential from the header named by `signature_header`. `auth.source` overrides that with an explicit `{kind, key}` pair, and `kind` may be **`query`** as well as `header`:

```yaml
- {id: auth, type: auth, scheme: exact, secret: "${TOKEN}", source: {kind: query, key: authID}}
```

If both `source` and `signature_header` are set, `source` wins.

**`kind: query` puts a credential in the URL, and URLs leak.** A query-string secret ends up in places a header never does:

- web-server, proxy, load-balancer, and CDN access logs, usually unredacted and usually retained longer than application logs
- the `Referer` header on any outbound navigation from a page fetched at that URL
- browser history, bookmarks, and shared links
- crash and APM traces that record the full request URL

Some providers only offer this (a static token appended as `?authID=…` is a real pattern), which is why Hookway supports it. Prefer `kind: header` whenever the provider allows a choice.

Hookway compensates for the part it controls: when an `auth` node declares `source.kind: query`, that query key is added to the redaction set, so the value is published as `"redacted"` in the envelope's `query` object and never appears in a structured log. Header sources get the same treatment for the header name. This is automatic — you do not list the key in `security.redacted_header_denylist` yourself. It does **not** reach your ingress's own access logs, so configure redaction there too, and rotate a query-string credential more aggressively than a header one.

## Secrets

Put secrets in environment variables and reference them from YAML with `${ENV_NAME}`, or use `auth.secret_ref` (`env` / `file` / `aws_secrets_manager`) with a refresh interval and optional overlap window so rotation is not a flag day. See [Configuration § Secret references](configuration.md#secret-references). Hookway does not print expanded secret values.

Destination credentials belong in a named [connection](configuration.md#connections) when more than one destination shares them — one `aws` connection serves `s3`, `sqs`, `sns`, and `lambda` destinations, so the credential is declared and rotated in one place, and `connections:` enumerates every secret the process can reach. Two caveats:

- **Connections resolve at load time.** Unlike `auth.secret_ref`, there is no background refresh: rotating a destination credential requires a restart, because the backend clients capture their credentials when they are constructed.
- **A connection widens what one leaked block exposes.** A leaked `destinations.<id>` block exposes one backend; a leaked `connections.aws_prod` exposes everything that AWS account can reach. See [Configuration § Blast radius](configuration.md#blast-radius) and limitation L3 in [Invariants](invariants.md).

## Request Handling

- Only configured flow paths are accepted for ingress.
- Empty bodies are rejected.
- Body size is capped by each `read_body.max_bytes`; `server.max_body_bytes` is a lint reference, not an inherited cap. Keep `read_body.max_bytes` tight per flow.
- Inbound `X-Hookway-Hops` is enforced by a `loop_guard` node, not `security.loop_protection`. See [Configuration § Loop protection](configuration.md#loop-protection).
- Server timeouts are configured from YAML.
- Shutdown handles `SIGINT` and `SIGTERM`.
- `/healthz` reports process liveness for container healthchecks.
- `/ready` reports per-flow readiness derived from unique destination probes. The HTTP status is 200 while at least one flow is ready. Set `server.readiness_require_all: true` for 503 if any flow is unready. Readiness is maintained by a background prober (`server.readiness_interval` / `readiness_timeout`) and served from cache, so kubelet probes never trigger a live broker fan-out.
- `/readyz` reports the same cached readiness state as `/ready`.
- `/health` is a liveness alias for `/healthz` and does NOT report readiness, so infrastructure treating `/health` as a liveness probe will not restart pods on transient destination outages.

## Rate Limiting

Rate limiting uses `golang.org/x/time/rate` token buckets on a `rate_limit` node. `key_by` is `source_ip` (default) or `header.<name>` (falls back to source IP if that header is empty). Unknown `key_by` values are treated as source IP. Buckets are stored in process memory and idle keys are evicted to cap growth from one-off IPs. `X-Forwarded-For` and `X-Real-IP` are ignored unless `security.trusted_proxy_headers` is true. When `security.trusted_proxies` is configured, forwarding headers are honored only when the immediate TCP peer is within one of those CIDR ranges; otherwise the peer IP is used.

When `X-Forwarded-For` is honored, Hookway does **not** take the leftmost entry (that value is client-supplied and trivially spoofed). Instead it walks the comma-separated list right to left — the direction proxies append in — skipping any entry that itself falls within `trusted_proxies`, and uses the first (rightmost) entry that isn't a known proxy as the client IP. If every entry is a trusted proxy, the leftmost entry is used. A malformed entry stops the walk immediately and falls back to the last known-good hop rather than continuing toward the client-controlled end, so a crafted invalid entry can't be used to smuggle a different address into scope. This stops a client from spoofing `X-Forwarded-For` (e.g. prepending an allowlisted IP, or rotating fake addresses) to evade the IP allowlist or per-IP rate limit. Configure `trusted_proxies` to your load balancer/ingress ranges in production; leaving `trusted_proxy_headers` on with `trusted_proxies` empty trusts any peer and treats the rightmost `X-Forwarded-For` entry as the client IP with no proxy hops recognized to skip.

For horizontally scaled deployments, enforce global quotas at a shared edge or gateway limiter. Hookway has no Redis/shared limiter mode. Process-local rate limits are useful as defense in depth but do not enforce a global quota across replicas.

## Replay Protection

Replay protection is process-local. By default it keys on a hash of raw body bytes; an explicit key can use headers/path variables. A replay can pass on another replica (N pods permit N reservations). Hookway has no cluster-wide replay guard. Consumers must deduplicate.

## Header Redaction and Allowlisting

Published envelopes and request logs redact:

- `authorization`
- `cookie`
- `set-cookie`
- `paddle-signature`
- any header containing `secret`, `token`, `signature`, `key`, or `auth` (e.g. `X-Api-Key`, `X-Auth-Token`)
- any header named in `security.redacted_header_denylist`

Redacted values are published as `"redacted"`.

By default this is a denylist (fail-open): headers not matched above are **forwarded**. For a fail-safe forwarding posture, set `security.forwarded_header_allowlist`; only allowlisted headers are then forwarded to consumers, and everything else is dropped. The denylist/sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through.

Request logs are separate and fail-closed: set `logging.header_allowlist` to name headers that may appear in structured logs. An empty list (the default) logs no request headers.

## Idempotency

Each published envelope has a unique `id` (UUIDv7) so consumers can deduplicate at-least-once deliveries. Hookway does not populate `idempotency_key` on the envelope (there is no `idempotency_header` on `auth` / `no_auth`). Dedupe on `id`.

For fan-out flows, delivery is not atomic across destinations. Confirmed broker messages cannot be canceled or recalled. With the default `delivery_failure_policy: stop_on_failure`, Hookway publishes sequentially; if an earlier destination accepts a publish and a later destination fails, Hookway returns `503` so the provider retries. Earlier destinations can see the same event again and must dedupe by `id`. With `delivery_failure_policy: skip_failed`, Hookway attempts every publish and acknowledges partial success with `202` when at least one destination succeeds; that partial-success `202` also releases the replay reservation (see below), so an identical sender retry is accepted again rather than rejected, and can redeliver to the destinations that failed the first time. That retry re-delivers to the destinations that already succeeded too; consumers dedupe that duplicate on `id`, identical to the retry contract `stop_on_failure` already relies on.

## Availability Model

Hookway does not durably spool requests locally. With `stop_on_failure`, a webhook is acknowledged with `202 Accepted` only after every `publish` node in the flow confirms; if a broker is unavailable after retries, Hookway responds `503 Service Unavailable` and relies on the provider's own retry policy to redeliver. With `skip_failed`, Hookway acknowledges partial success with `202` and includes failed routes in `failed_destinations`; if every route fails, it returns `503`. Under load shedding (`server.max_inflight_requests`) it also responds `503` with `Retry-After`. Operate behind providers that retry on 5xx, and size each destination's `publisher_channels`, per-request `server.fanout_concurrency`, and global `max_inflight_requests` for expected burst volume. The replay reservation is released on a full-failure `503` and on a partial-failure `202` (`skip_failed` only), so a legitimate provider retry is never permanently rejected as a replay while destinations still need the event; it is kept only when every destination has confirmed the publish.

The webhook publish itself runs detached from the inbound HTTP request's context (`server.publish_timeout`, default `10s`, matching `write_timeout`/`shutdown_timeout`): once the request body is read and the signature verified, a sender disconnect or client-side timeout no longer aborts the in-flight fan-out, since that work is already committed to being delivered. The detached publish is still bounded by `server.publish_timeout` so a stalled destination cannot hang a delivery indefinitely; exceeding it fails the publish (surfaced as `503`) the same as any other publish error. Config validation requires `publish_timeout <= shutdown_timeout` and `publish_timeout <= write_timeout`: a sync-mode publish that outlives either would make a clean shutdown falsely report failure, or make the sender see a spurious timeout and retry after the response can no longer be written.

**Readiness:** `/ready` and `/readyz` report **per flow**. Each unique destination is probed once per interval (O(unique destinations), not O(flows × destinations)). A flow is ready when every destination its `publish` nodes reference is ready. The HTTP status is **200 while at least one flow is ready**, so one saturated destination cannot take an unrelated flow out of load-balancer rotation (D9). Set `server.readiness_require_all: true` for 503 if any flow is unready. An async webhook destination reports not-ready once its delivery queue reaches 90% of capacity. `/health`/`/healthz` stay liveness-only and never reflect destination readiness.

## Failure capture

With the `replay:` block configured, core uploads captured failures to the
replay service: each record holds the failed node's input — the request body
and, for proxy redrive, the original headers — plus the payload being sent.
The replay service stores them until retention expires, so treat its database
like the destinations it can redrive into: keep `replay.url` and the control
listener (`replay.control.addr`) on private networks, encrypt replay's
storage, and treat `capture_token` and `control.token` as secrets. Metadata
never carries credentials: error messages keep only the scheme and host of
any URL, and `node_config_hash` and `destination_hash` are computed with
secret-named fields, header maps, URL userinfo, queries, and fragments, and
`key=value` secrets removed. Snapshot and payload bytes are never logged.
See [replay-service.md § Security](replay-service.md#security).

The control listener lets whoever holds `control.token` make core run any
node of any flow with a snapshot of their choosing — skipping the
authentication, `replay_guard`, and rate limits upstream of that node — and so
deliver arbitrary payloads to any configured destination. Anyone who can
write to the replay service's database can do the same through a retry. Bind
the listener to a private interface or network, never expose it through the
ingress that serves webhooks, restrict database access like a destination
credential, and rotate the token like one. The token is compared in constant
time and never logged.

## Logging

Logs include request method, path, flow name (`endpoint` / `flow` depending on the logger), destination id (`destination`), target, status, and duration. Raw webhook payloads and secrets are not logged.

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

## Sync proxy (`http_request` / `respond`)

A proxy flow confines callers to whatever URL its `url` template builds — both
the path *and* the host. Three protections keep that boundary real.

- **Path variables that could re-shape the URL are rejected.** `net/http` hands
  handlers an already percent-decoded `URL.Path`, so Go's router redirects a
  literal `/proxy/../admin`, but `/proxy/..%2f..%2fadmin` only becomes `../..`
  after routing. Hookway rejects with `400`, before the value can be rendered
  into an upstream URL, any request whose captured path variable contains:

  | Rejected | Why |
  |---|---|
  | a `..` path segment | an upstream that normalizes `/safe/../../admin` to `/admin` escapes the configured prefix |
  | `#` | url.Parse reads it as a fragment delimiter and **silently drops the rest of the template**, including a mandatory suffix |
  | `?` | same, as a query delimiter — and it lets the caller append an arbitrary query string |
  | `\` | treated as `/` by several HTTP clients and by Windows path handling |
  | an ASCII control character | request smuggling / header injection |

  The `#` and `?` cases are the reason this list is longer than it used to be.
  Against a template like `https://up.example.com/tenants/{tenant}/readonly`,
  the same decode-after-routing step turns `%23` into `#` and `%3F` into `?`:

  ```
  tenant = "acme"       →  /tenants/acme/readonly       (intended)
  tenant = "acme#"      →  /tenants/acme                (suffix dropped)
  tenant = "acme?x=1&"  →  /tenants/acme?x=1&/readonly  (suffix dropped, query injected)
  ```

  The mandatory `/readonly` suffix — the thing the template existed to enforce
  — disappears. Earlier versions of Hookway rejected only `..`, so this was a
  real access-control bypass, and the claim that the proxy "confines callers to
  whatever path its `url` template builds" was false as written. It is true
  now. Rejecting at the trigger fails closed and does not depend on how any
  particular upstream normalizes paths. The outbound path is additionally
  normalized with `path.Clean` as defense in depth. Names that merely contain
  dots (`..hidden`, `a..b`) are unaffected.

- **The upstream host cannot be chosen by the caller.** When an `http_request`
  `url` has a `{variable}` in its *authority*, the destination is influenced by
  request data, which is an SSRF primitive. Hookway requires such a host to end
  in a literal dotted domain suffix (`{envName}-api.example.com`, never
  `{host}`), vets the rendered target against a private-network block list
  (loopback, link-local including `169.254.169.254`, RFC1918, CGNAT, ULA,
  multicast, cloud metadata addresses, plus `localhost` and the `.internal` /
  `.local` / `.localhost` / `.localdomain` / `.home.arpa` suffixes), refuses
  ambiguous numeric host spellings rather than trying to interpret them, and
  re-checks every resolved IP in the dialer so DNS rebinding cannot swap the
  address between check and connect. Redirects are not followed. A **literal**
  host is exempt: it is the operator's own explicit choice, and proxying to an
  internal service is a legitimate configuration. The per-node opt-out for a
  templated host is `allow_private_targets: true`; it defaults to `false` and
  is rejected at config time on a node whose host is literal. Full rules:
  [Configuration § `http_request` outbound egress policy](configuration.md#http_request-outbound-egress-policy).

- **Caller credentials are not forwarded upstream.** `Authorization` and
  `Cookie` authenticate the caller *to Hookway*, so forwarding them would hand
  a credential to a third party. Both are withheld unless explicitly named in
  `forward_headers`. Hop-by-hop and framing headers
  (`Connection`, `Keep-Alive`, `Transfer-Encoding`, `Upgrade`,
  `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`, `Host`,
  `Content-Length`), plus any field nominated by the inbound `Connection`
  header, are dropped in both directions. On the response side `Set-Cookie` is
  stripped so an upstream cannot set a cookie on Hookway's origin.

## Code node

A `code` node runs operator-authored JavaScript (goja) on the request envelope. **Flow authors are trusted operators.** They can already point destinations at arbitrary URLs and disable auth. The sandbox defends against accidents — infinite loops, unbounded recursion, accidental host I/O — not against a hostile tenant. **If Hookway is ever multi-tenant or hosted, this design does not hold; move the code node to WASM with real resource isolation rather than hardening goja further.**

Controls C1–C7 bound wall-clock time, call stack, host access, input/output size, per-request isolation, and panics. See [Configuration § Code node](configuration.md#code-node).

**Memory gap.** goja has no hard memory cap. Controls C1–C7 bound time, call stack, host access, and payload size, but a script doing `let a=[]; while(1) a.push(1)` allocates until the timeout interrupts it — which may be after significant allocation. Hookway is a single process, so that pressure is shared with every other flow. Mitigate with per-pod memory limits plus liveness restart, and deploy split by trust domain for untrusted flows.

**Interrupt gap.** A single long-running native operation (a pathological regex) may not be interruptible between statements. Prefer the `filter` node over complex regexes in `code`.

A `code` node with no explicit `timeout` is a config warning; the 1s default still applies. Set `timeout` deliberately.

## Production Hardening

- Terminate TLS at a trusted load balancer or reverse proxy. Enable `trusted_proxy_headers` only with `trusted_proxies` set to your proxy/ingress CIDRs so forwarded client IPs cannot be spoofed.
- Keep RabbitMQ durable exchanges/queues enabled for production and configure dead-letter queues for failed consumers.
- Use `amqps://` for broker connections. The container image ships the system CA bundle so publicly trusted brokers (e.g. CloudAMQP) work out of the box; for private/self-signed CAs or mutual TLS, set `destinations.<id>.rabbitmq.tls` (`ca_cert`, `client_cert`/`client_key`, `server_name`). Never enable `insecure_skip_verify` in production.
- Run at least two replicas, but treat rate limiting and replay protection as process-local until a shared backend is added.
- Keep `read_body.max_bytes` tight per flow and set `server.max_inflight_requests` to bound memory and shed load under flood.
- Tune `destinations.<id>.rabbitmq.publisher_channels` to the desired publish concurrency per destination (default 8).
- Tune `server.fanout_concurrency` to bound per-request goroutines and concurrent publishes across fan-out routes (default 8).
- Serve metrics on a private port via `metrics.addr` so `/metrics` is not exposed on the internet-facing webhook listener.
- Forward structured logs and Prometheus metrics to centralized systems, and alert on auth failures, rate limits, saturation (`hookway_saturated_total`), publish failures, and readiness failures.
- Set `security.forwarded_header_allowlist` so only intended headers reach consumers.
- Set `logging.header_allowlist` so only intended headers appear in request logs (empty logs none).
- Rotate webhook secrets per flow and keep old/new overlap short during provider-side rotations. `auth.secret_ref.overlap` is the in-process overlap window; `${ENV}` expansion still happens at load.
