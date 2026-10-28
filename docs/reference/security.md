# Security

Choose a sender authentication scheme, protect credentials, and restrict the
interfaces your deployment exposes. Authentication happens before event delivery;
flows using the explicit `no_auth` node accept unauthenticated requests.

## Deployment trust boundaries

Flow authors are trusted operators; webhook bodies, path variables, forwarded
headers and browser requests are not. Binding a listener to a private interface
does not replace its configured authentication. CORS is not authentication.

| Surface | Intended exposure | Authentication and secret owner |
| --- | --- | --- |
| Configured webhook routes | Public ingress or an explicitly trusted internal network | Per-flow provider authentication; core owns verifier secrets. `no_auth` remains an explicit operator choice. |
| Core health/readiness | Deployment probes | Public status; no credential. Core readiness can expose flow and destination status, so restrict probe access if that topology is sensitive. |
| Core metrics | Private scrape listener, preferably separate from ingress | `security.api_key` when configured; core/scraper own the key. Without it metrics are unauthenticated. |
| Editor API | Local authoring or a protected authoring origin | Optional `security.api_key`; every protected API route requires one unambiguous Bearer header. It validates submitted documents, not deployed files, and does not load deployment secrets. |
| Builder/browser assets | Static public assets or an operator-only origin | No deployment credentials; editor wildcard CORS supports stateless authoring and is not a replay API policy. |
| Replay capture | Core-to-replay private network | Dedicated `capture` key; core and replay own it. |
| Replay operator API | Operator-only network/origin | Separate named `read`, `payload`, and `retry` scopes; exact configured browser origins. |
| Replay metrics | Private scrape endpoint | Dedicated `metrics` key; replay/scraper own it. |
| Replay health/readiness | Deployment probes | Minimal public status, no credential. |
| Core control | Private replay-to-core listener; never public ingress | Separate `control.token`; replay worker and core own it. This authorizes redrive, not ordinary ingress. |
| Replay database | Private database network | Runtime DML role; separate owner/migration credential confined to provisioning and migration jobs. Database writers can influence recovery. |

The [local production recipe](../development/local-development.md#persistent-local-production-setup)
keeps host ports loopback-only and separates database roles without requiring a
cloud account. Production operators still own firewall/proxy configuration,
TLS termination, storage/backup encryption and access-log redaction.

## Choose an authentication format

Set `auth.scheme` to a concrete name from the table below. There is no `family`
field. `hmac_sha256` and `jwt` are grouping names, not valid schemes;
`header_exact` is accepted as an alias for `exact`.

Use the [provider recipes](../guides/provider-compatibility.md) for GitHub, Stripe,
Paddle, and Slack. See [Configuration](configuration.md#auth) for all auth fields.

## Auth Schemes

Each `auth` node requires an explicit `scheme`. Inline secrets build a verifier at startup; `secret_ref` uses the cached current/overlap values. An unknown scheme fails closed: validation and standalone startup reject it; verification errors never accept unverified traffic.

The builder exposes registered authentication formats. The table
also shows the separate `no_auth` option; it is not an authentication format.

| Scheme | Family | Verifies | Notes |
|---|---|---|---|
| `hmac_sha256_ts_colon` | `hmac_sha256` | timestamp + raw body | signed timestamp; see below |
| `hmac_sha256_ts_dot` | `hmac_sha256` | timestamp + raw body | signed timestamp; see below |
| `hmac_sha256_headers_dot` | `hmac_sha256` | separate timestamp header + body | explicit raw or original JavaScript JSON representation |
| `hmac_sha256_ts_dot_multi` | `hmac_sha256` | timestamp + body, multiple signatures | accepts any matching configured signature label |
| `hmac_sha256_polar` | `hmac_sha256` | message ID + timestamp + raw body | current and legacy Polar keys; Base64 signatures |
| `ecdsa_p256_sha256_headers` | `ecdsa` | separate timestamp header concatenated with raw body | P-256/SHA-256, base64 ASN.1 DER signature |
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

Used by Paddle's `Paddle-Signature`. Select it explicitly; `auth.scheme` has no
default. Hookway reads the exact raw request body once, reads the configured `signature_header`, extracts one `ts` value and up to 16 `h1` signatures, builds `ts + ":" + raw_body`, computes HMAC-SHA256 using the configured endpoint secret, hex-encodes it, and compares it to `h1` using `hmac.Equal`. Requests outside `timestamp_tolerance` are rejected with `401 Unauthorized`. Empty secrets, non-positive timestamp tolerances, malformed signatures, duplicate timestamps, oversized headers, too many signatures, and non-SHA256-length HMAC values fail closed.

### `hmac_sha256_hex` (`hmac_sha256`)

Prefixed hex HMAC-SHA256 over the exact raw body, no signed timestamp (used by GitHub's `X-Hub-Signature-256: sha256=<hex>`). Hookway computes HMAC-SHA256 over the exact raw body with the endpoint secret and compares using `hmac.Equal`. There is no signed timestamp, so `timestamp_tolerance` is not used. A missing/short/tampered signature or empty secret fails closed.

### `hmac_sha256_ts_dot` (timestamped, `hmac_sha256`)

Timestamped HMAC-SHA256 with a dot separator (used by Stripe's `Stripe-Signature`). Hookway parses a labeled, comma-delimited header — `t=<unix>,v1=<hex>` — using exactly one `t` and up to 16 `v1` signatures (any matching candidate succeeds during secret rotation); builds `t + "." + raw_body`; computes HMAC-SHA256 over those exact bytes with the endpoint secret; hex-encodes it; and compares to `v1` with `hmac.Equal`. Requests outside `timestamp_tolerance` are rejected `401`. A missing or repeated `t`, missing `v1`, an oversized header, too many candidates, an empty secret, or a non-positive tolerance fails closed.

### `hmac_sha256_slack_v0` (separate timestamp)

Verifies Slack's `X-Slack-Signature: v0=<hex>` over `v0:<timestamp>:<raw_body>`. Configure `timestamp_header: X-Slack-Request-Timestamp`, `timestamp_unit: seconds`, and `timestamp_tolerance: 5m`. The timestamp is checked symmetrically by default; raw bytes are authenticated before JSON decoding. See the signed Events API challenge recipe and tested scope in [Provider compatibility](../guides/provider-compatibility.md).

### `hmac_sha256_polar` (signed message ID and timestamp)

Verifies Polar's `webhook-signature: v1,<base64>` over
`webhook-id + "." + webhook-timestamp + "." + raw_body`. Set
`signature_header: webhook-signature`, `timestamp_header: webhook-timestamp`,
`message_id_header: webhook-id`, and `timestamp_unit: seconds`. All three header
names must differ, case-insensitively; missing or duplicate headers fail closed.
The message ID must be nonempty, bounded to 8 KiB and contain no dots or whitespace.
The signature header is bounded to 8 KiB and at most 16 space-separated candidates;
unknown versions and malformed candidates cannot authenticate a request, but do
not prevent a later valid `v1` candidate from matching. Digests use constant-time
comparison.

Use the dashboard secret unchanged. Hookway automatically tries both Polar's
current Standard Webhooks key (Base64-decoded after removing an optional `whsec_`
prefix) and its legacy key (the full secret's UTF-8 bytes). If Base64 decoding
fails, legacy verification remains available. Empty or oversized secrets are
rejected. Refresh and rotation overlap use the existing secret-reference mechanism.

The timestamp window is symmetric and defaults to five minutes. Milliseconds,
`age`, `unchecked`, JSON reserialization, custom signature labels, Bearer stripping
and query credentials are rejected. Invalid authentication defaults to 401; key
resolution failures retain 503, and existing flow response overrides apply.
Preserve the verified webhook ID for consumer deduplication: freshness checking
does not prevent repeats within the allowed window. See the [Polar provider
recipe](../guides/provider-compatibility.md#polar) and [Polar's validation
documentation](https://polar.sh/docs/integrate/webhooks/delivery#custom-validation).

### `hmac_sha256_base64` (`hmac_sha256`)

Raw base64 HMAC-SHA256 over the exact raw body, with no prefix and no signed timestamp (used by Shopify's `X-Shopify-Hmac-SHA256`). The header value is the base64 digest by itself — Hookway does not strip a `sha256=`-style prefix for this scheme. Because there is no signed timestamp, `timestamp_tolerance` is unused; pair it with a `replay_guard` keyed on the raw body or a provider delivery id. A missing, malformed, or tampered signature, or an empty secret, fails closed.

### `hmac_sha1_hex` (`hmac_sha256`) — SHA-1

Legacy HMAC-SHA1 over the raw body, with a `sha1=` prefix and no signed
timestamp. Use it only for a provider that requires this format. Prefer
`hmac_sha256_hex` when the provider offers SHA-256; for GitHub, verify
`X-Hub-Signature-256`.

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

HMAC schemes use exact raw bytes by default. The explicit `body_representation: json_js` option on request-signature schemes uses JavaScript JSON parsing/stringification of the original body before any transforms. See [Ingress adapters](../guides/ingress-adapters.md#request-signatures). Otherwise the body is not parsed, trimmed, normalized, or re-serialized before verification, and the endpoint secret is used as a raw string (not base64-decoded).

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
- **A connection widens what one leaked block exposes.** A leaked `destinations.<id>` block exposes one backend; a leaked `connections.aws_prod` exposes everything that AWS account can reach. See [Configuration § Blast radius](configuration.md#blast-radius) and limitation L3 in [Invariants](../development/invariants.md).

## Request Handling

- Only configured flow paths are accepted for ingress.
- Empty bodies are rejected unless `read_body.allow_empty: true`; downstream authentication and parsing must support empty input too.
- Body size is capped by each `read_body.max_bytes`; `server.max_body_bytes` is a lint reference, not an inherited cap. Keep `read_body.max_bytes` tight per flow.
- Inbound `X-Hookway-Hops` is enforced by a `loop_guard` node, not `security.loop_protection`. See [Configuration § Loop protection](configuration.md#loop-protection).
- Server timeouts are configured from YAML.
- Shutdown handles `SIGINT` and `SIGTERM`.
- `/healthz` reports process liveness for container healthchecks.
- `/ready` reports per-flow readiness derived from unique destination probes. The HTTP status is 200 while at least one flow is ready. Set `server.readiness_require_all: true` for 503 if any flow is unready. Readiness is maintained by a background prober (`server.readiness_interval` / `readiness_timeout`) and served from cache, so platform health checks never trigger a live broker fan-out.
- `/readyz` reports the same cached readiness state as `/ready`.
- `/health` is a liveness alias for `/healthz` and does NOT report readiness, so infrastructure treating `/health` as a liveness probe will not restart containers on transient destination outages.

## Rate Limiting

`rate_limit` supports token buckets and approximate sliding-window counters in
explicit memory, Redis or PostgreSQL stores. Memory is process-local; shared
stores coordinate replicas. Missing header identity returns 400, invalid identity
configuration fails validation, exhausted quota returns 429, and infrastructure
or live-identity capacity failures return 503. Unknown keys never silently fall
back to IP. Prefer authenticated credential/JWT identities for tenant quotas;
otherwise authenticate header identities at a trusted upstream boundary.
`X-Forwarded-For` and `X-Real-IP` are ignored unless proxy trust is configured.
See [Protection stores](../guides/protection-stores.md) for trust requirements and state
lifecycle.

When `X-Forwarded-For` is honored, Hookway does **not** take the leftmost entry (that value is client-supplied and trivially spoofed). Instead it walks the comma-separated list right to left — the direction proxies append in — skipping any entry that itself falls within `trusted_proxies`, and uses the first (rightmost) entry that isn't a known proxy as the client IP. If every entry is a trusted proxy, the leftmost entry is used. A malformed entry stops the walk immediately and falls back to the last known-good hop rather than continuing toward the client-controlled end, so a crafted invalid entry can't be used to smuggle a different address into scope. This stops a client from spoofing `X-Forwarded-For` (e.g. prepending an allowlisted IP, or rotating fake addresses) to evade the IP allowlist or per-IP rate limit. Configure `trusted_proxies` to your load balancer/ingress ranges in production; leaving `trusted_proxy_headers` on with `trusted_proxies` empty trusts any peer and treats the rightmost `X-Forwarded-For` entry as the client IP with no proxy hops recognized to skip.

Repeated `X-Forwarded-For` fields are combined in wire order before that walk.
If a present list has no usable hop, identity falls back to the socket peer,
not `X-Real-IP`. When XFF is absent, `X-Real-IP` is accepted only as one field
containing one valid IP address. Malformed or repeated values fall back to the
socket peer. Empty trusted ranges remain supported for compatibility but produce
a configuration warning; strict validation rejects that warning.

## Replay Protection

A `replay_guard` requires an explicit store. Redis and PostgreSQL coordinate
reservations across replicas; memory protection resets on restart. Reservations
expire logically at admission time plus TTL, independently of cleanup. Owner
checks prevent stale requests from releasing newer reservations. Partial or
uncertain outbound effects retain protection, even if replay capture is absent
or fails. Consumers still need idempotency. Shared protection does not provide
exactly-once execution or guarantee persistence through backend data loss.

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

Use a stable sender identity for business deduplication. The
[delivery contract](../operations/delivery-guarantees.md#event-identity-and-duplicate-handling)
owns event identity, fan-out and recovery semantics; the
[published envelope reference](configuration.md#published-envelope-reference)
defines `id`, `idempotency_key` and compatibility.

## Availability Model

Choose the acknowledgement and recovery policy from
[Delivery guarantees](../operations/delivery-guarantees.md). Configure
[admission and publication budgets](configuration.md#server), and use the
[readiness contract](configuration.md#http-endpoints-and-readiness) for probes.

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
See [replay-service.md § Security](../operations/replay-service.md#security).

The control listener lets whoever holds `control.token` make core run any
node of any flow with a snapshot of their choosing — skipping the
authentication, `replay_guard`, and rate limits upstream of that node — and so
deliver arbitrary payloads to any configured destination. Anyone who can
write to the replay service's database can do the same through a retry. Bind
the listener to a private interface or network, never expose it through the
ingress that serves webhooks, restrict database access like a destination
credential, and rotate the token like one. The token is compared in constant
time and never logged.

Core control, core metrics and protected editor APIs reject repeated
`Authorization` fields before invoking the protected operation, even when one
field contains the correct credential. Send exactly one Bearer header.

Capture uploads and webhook publishers do not follow HTTP redirects, including
same-origin redirects: configure the canonical receiving URL. Redirect responses
are delivery failures, not permission to resend a credential or retained payload
to another endpoint. Direct authenticated local/private HTTP receivers remain
supported. Capture-upload, dead-letter failure, node and readiness logs, node
span exception/status text, and stored capture/control transport diagnostics
remove URL userinfo, paths, queries, fragments
and credential assignments while retaining destination hosts and failure classes;
this does not redact legitimate retained snapshot or payload bytes.

Webhook destination authorities containing request variables use the same
private-address classification and DNS-vetted, pinned dialing as guarded
`http_request` nodes. Literal operator-selected hosts remain trusted.
`destinations.<id>.webhook.allow_private_targets: true` permits an intentional
private templated destination, but does not permit arbitrary authority syntax,
remove the required literal domain suffix, or enable redirects. Use separate
network permissions for trusted operator-authored destinations where needed.

## Logging

Logs include request method, path, flow name (`endpoint` / `flow` depending on the logger), destination id (`destination`), target, status, and duration. The request logger does not include raw webhook payloads or resolved authentication secrets.

Do not encode credentials in routing paths, target names, flow/destination names,
request IDs or other diagnostic labels. These values can be logged; diagnostic
URL/credential sanitization is not a general secret detector for arbitrary
operator-authored strings. Ingress access logs and observability exporters need
their own redaction policy. Retained raw replay artifacts are sensitive data,
not application log messages.

## Envelope Compatibility

The [published envelope reference](configuration.md#published-envelope-reference)
owns payload fields and `schema_version` compatibility.

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

Managed outbound credential profiles use exact origin allowlists, secret references and bounded native token acquisition. They cannot override transport headers or `X-Hookway-Hops`; resolved credentials never enter JavaScript or delivery snapshots. Creation and caller-key flows produce metadata-only failure records to avoid an unsafe automatic replay. See [Ingress adapters](../guides/ingress-adapters.md#managed-outbound-credentials).

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

  These characters can escape the intended path or drop a required suffix.
  Rejection happens before forwarding; the outbound path is also normalized.
  Names containing dots, such as `a..b`, remain valid.

- **The upstream host cannot be chosen by the caller.** When an `http_request`
  `url` has a `{variable}` in its *authority*, the destination is influenced by
  request data, which is an SSRF primitive. Hookway requires such a host to end
  in a literal dotted domain suffix (`{envName}-api.example.com`, never
  `{host}`), restricts authority variable values to single DNS labels even
  when private targets are allowed, and vets the rendered target against a private-network block list
  (loopback, link-local including `169.254.169.254`, RFC1918, CGNAT, ULA,
  multicast, cloud metadata addresses, plus `localhost` and the `.internal` /
  `.local` / `.localhost` / `.localdomain` / `.home.arpa` suffixes), refuses
  ambiguous numeric host spellings rather than trying to interpret them, and
  re-checks every resolved IP in the dialer so DNS rebinding cannot swap the
  address between check and connect. Guarded requests use a separate connection
  pool from unguarded requests. Redirects are not followed by default; explicit redirect policies are bounded and constrained. Managed credential requests do not follow redirects. A **literal**
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

A `code` node runs operator-authored JavaScript on the request envelope.
Flow authors must be trusted: this is not a sandbox for hostile users or tenants.

Timeout, call-stack and input/output limits catch common mistakes, but do not
cap VM heap. A large allocation can kill core, including acknowledged volatile
jobs. A long native operation such as a pathological regex can outlast the
configured timeout. Prefer `filter` for regex matching and built-in transforms
where possible.

Set an explicit timeout and process memory limit. Split scripting trust domains
into separate core processes when needed; this still does not make hostile
scripts safe. Run stress tests only in disposable hard-limited processes with an
external kill deadline. See [code settings](configuration.md#code-node) and
[resource sizing](../operations/resource-limits.md).

## Production Hardening

- Terminate TLS at a trusted load balancer or reverse proxy. Enable `trusted_proxy_headers` only with `trusted_proxies` set to your proxy/ingress CIDRs so forwarded client IPs cannot be spoofed.
- Keep RabbitMQ durable exchanges/queues enabled for production and configure dead-letter queues for failed consumers.
- Use `amqps://` for broker connections. The container image ships the system CA bundle so publicly trusted brokers (e.g. CloudAMQP) work out of the box; for private/self-signed CAs or mutual TLS, set `destinations.<id>.rabbitmq.tls` (`ca_cert`, `client_cert`/`client_key`, `server_name`). Never enable `insecure_skip_verify` in production.
- Use a shared Redis or PostgreSQL protection store for quotas and duplicate checks across replicas; memory remains process-local.
- Keep `read_body.max_bytes` tight per flow and set `server.max_inflight_requests` to bound memory and shed load under flood.
- Tune `destinations.<id>.rabbitmq.publisher_channels` to the desired publish concurrency per destination (default 8).
- Tune `server.fanout_concurrency` to bound per-request goroutines and concurrent publishes across fan-out routes (default 8).
- Serve metrics on a private port via `metrics.addr` so `/metrics` is not exposed on the internet-facing webhook listener.
- Forward structured logs and Prometheus metrics to centralized systems, and alert on auth failures, rate limits, saturation (`hookway_saturated_total`), publish failures, and readiness failures.
- Set `security.forwarded_header_allowlist` so only intended headers reach consumers.
- Set `logging.header_allowlist` so only intended headers appear in request logs (empty logs none).
- Rotate webhook secrets per flow and keep old/new overlap short during provider-side rotations. `auth.secret_ref.overlap` is the in-process overlap window; `${ENV}` expansion still happens at load.

## Replay operator confirmation

`GET /v1/me` returns the authenticated key name and scopes without exposing the
credential. Resolution requires `retry` scope; inspection requires `read` and
payload downloads require `payload`. Use individual named operator keys for
useful attribution; shared tokens identify a credential, not an individual.

Operator resolution records independent confirmation of an unknown outcome.
Hookway validates the note/reference shape but does not verify their truth or
fetch supplied references. The original unknown result and artifacts remain
until retention GC. Evidence text is sensitive stored data; keep credentials
and unnecessary personal details out of notes. Responses are JSON and `no-store`;
clients must display supplied text without interpreting HTML or executing URLs.
Atomic state/audit writes and per-actor resolution idempotency prevent competing
retry/dismiss/resolve transitions from all succeeding. See the
[investigation runbook](../operations/replay-investigation.md).

## Replay console

The standalone console image serves `/` and the public builder `/builder`.
Use HTTPS on an operator-only console origin, set its browser-reachable
`REPLAY_API_URL`, and allow that exact origin in replay `server.cors.allowed_origins`.
The frontend never proxies `/v1/` or receives deployment secrets. Use named least-privilege
credentials for attribution: read, retry and payload are independent scopes.
Tokens and failure state remain in browser memory and are cleared on leaving,
reloading or signing out. Console responses use no-store, frame denial, nosniff,
no-referrer and a CSP limiting scripts to local assets and API connections to
the configured replay API origin. Enabled metrics require a metrics-scoped key;
health/readiness endpoints expose only minimal public status.

Payload access is explicit, scoped and audited. Previews are bounded escaped
text, preserving JSON numbers; full downloads are octet-stream bytes and are
not opened automatically. API state checks prevent a browser's stale eligibility
from authorizing recovery. Unknown retries require duplicate-risk acknowledgement,
and operator confirmation retains its original uncertain evidence. See
[console setup, security boundaries and uncertain submissions](../operations/replay-console.md).

## Replay API keys and abuse limits

Generate each credential from at least 32 random bytes, for example
`openssl rand -hex 32`. Replay accepts tokens of 32–256 bytes. Use separate named
credentials for core capture, each operator and metrics scraping; keep the core
control token separate too. Grant only the needed scopes and load secrets through
environment references or your deployment's secret manager. The frontend's runtime
API URL is public configuration and must never contain credentials.

Rotate by deploying a new named key alongside the old key, migrating its clients,
then removing the old key and rolling every replay replica. Keys resolve at
startup, so configuration edits alone do not revoke a running process's key.
Browser sessions receiving 401 clear their credential. Use production HTTPS,
restrict operator/API access at the network boundary and redact Authorization in
upstream access logs. CORS is an exact-origin browser policy, not authentication.

Authentication hashes the bounded submitted token and compares its SHA-256 digest
against every configured key with constant-time digest comparisons. It does not
return early at a matching key. Errors are generic, duplicate Authorization headers
are rejected, tokens are never logged and the server caps request headers at 16 KiB.
Actual operator, capture and enabled metrics requests require their respective
scopes. Only minimal health/readiness probes and successful operator CORS preflights
are public.

Missing, malformed and invalid credentials consume a per-replica failure budget:
each socket peer has a burst of 10 with 10 attempts per minute refilling, alongside
a global burst of 200 with 100 attempts per second refilling. Storage holds at most
4096 peers, expires idle peers after 15 minutes and shares a bounded overflow
bucket when full. Exhaustion returns an immediate generic 429 with `Retry-After`;
handlers do not sleep or lock out keys. Valid credentials are checked before this
limiter and remain usable behind a shared load balancer, subject to normal scopes.
The peer comes from the socket address; forwarded headers cannot create allowances.

This budget limits failed responses and bounds limiter memory. It still performs
bounded authentication work on every request and resets on process replacement;
it is not a distributed brute-force or volumetric denial-of-service boundary.
Deploy ingress/WAF limits across replicas, private metrics/control listeners,
connection/time limits and resource limits for stronger traffic protection.
See [replay metrics scraping](../operations/replay-service.md#operations) for a metrics-only key.
These deployment controls follow the HTTPS, endpoint authorization, input-boundary
and CORS guidance in [OWASP REST Security](https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html).
