# Ingress adapters

Hookway can verify, route, transform and publish incoming requests to SQS or
SNS, or synchronously relay requests to a configured HTTP backend. Queue
consumption belongs to a separate application. Hookway does not poll SQS.

This guide describes the native configuration contracts. Examples use
synthetic resources and fields; match your integration's expected request,
response and broker bytes before changing its ingress.

## Request signatures

Hookway offers 14 authentication formats. The separate `no_auth` node
explicitly permits unauthenticated traffic. The existing schemes are listed in [Security](../reference/security.md#auth-schemes).
Three request protocols add explicit timestamp and body controls:

| Scheme | Credential layout | Signed bytes |
|---|---|---|
| `hmac_sha256_headers_dot` | Hex SHA-256 signature and a separate timestamp header | timestamp + `.` + body representation |
| `hmac_sha256_ts_dot_multi` | `t=<timestamp>,s=<hex>,s=<hex>`; `signature_label` defaults to `s` | timestamp + `.` + original body bytes |
| `ecdsa_p256_sha256_headers` | Base64 ASN.1 DER signature and separate timestamp header | timestamp concatenated directly with original body bytes |

ECDSA requires a base64 PKIX DER P-256 public key. This is a different key
format from the PEM key used by JWT ES256. Separate-header schemes require
`timestamp_header`; all three declare `timestamp_unit: seconds` or
`milliseconds`. `timestamp_policy` defaults to `symmetric` with a `5m`
tolerance. `age` rejects old timestamps but permits future timestamps;
`unchecked` disables the freshness check. Choose either explicitly only when
matching a known contract. A signature still has to verify.

`body_representation` defaults to `raw`. Separate-header HMAC can opt into
`json_js`: JavaScript `JSON.stringify(JSON.parse(original_body))` semantics,
including key ordering, numbers and duplicate keys. This representation uses
the original request before transforms. A native implementation handles
supported values and falls back to an isolated JavaScript runtime when needed.
Operator-authored `code` nodes still run JavaScript.

Set exactly one of `secret` or `secret_ref`. References support `env`, `file`
and `aws_secrets_manager`, optional `json_field`, `refresh` and `overlap`.
For AWS, set `selection: exact` to read only the named secret, or
`selection: newest_matching` to require a matching name and choose the newest
secret. Omitted selection keeps legacy name matching with an exact-name fallback
if listing fails or finds nothing. Resolution is cached and refreshed; last known good values
survive a failed refresh. Never put resolved values in deployment exports.
Unknown schemes or invalid authentication configuration prevent standalone
startup. See [request-signatures.yaml](../../examples/auth/request-signatures.yaml).

## SQS and SNS publication

Both standard and FIFO resources are supported. A publish node references a
named `destination` and a `target` resource name. `create: true` provisions
supported resources; omit it for infrastructure managed elsewhere. Credentials
use the configured AWS connection or the SDK default chain. Access policies,
subscriptions, IAM and KMS bindings remain deployment responsibilities.

`wire_body` is an explicit choice on SQS/SNS publish nodes:

| Value | Broker message body |
|---|---|
| `envelope` (default) | Hookway's JSON event envelope |
| `raw` | Exact original request bytes |
| `json` | Current decoded body, including transforms, encoded as JSON |
| `json_js` | JavaScript JSON representation of the **original** request |

These choices do not bypass broker limits. SQS publication is bounded to
1 MiB including attribute content; SNS is bounded to 256 KiB. Bodies must be
UTF-8 and contain broker-supported characters. A 12,000,000-byte document
cannot be sent directly to either broker: publish an object reference or
project its metadata. Message attributes are limited to ten, with explicit
String, Number or Binary types. Templates resolve before publication.

A `.fifo` target requires `fifo.group_id` and exactly one deduplication mode:

```yaml
wire_body: json
fifo:
  group_id: {source: body, path: record_id}
  deduplication_id: {source: body, path: event_id}
retry: {max_attempts: 3, initial_backoff: 100ms, max_backoff: 1s}
```

Use `content_based_deduplication: true` instead of `deduplication_id` when the
resource is configured for it. Identity sources are `static` (`value`),
`body`, `header` or `variable` (`path`), `raw_sha256` and `json_js_sha256`.
Optional `fallback`, `fallback_hash` and `hash_length` are explicit.
Hash sources always use the original request. Hash truncation increases
collision risk; prefer a full digest or a stable sender event ID. FIFO ordering
is per group and broker deduplication has a finite window, not an exactly-once
delivery guarantee. IDs and bytes are frozen for internal retries and redrive.
A fresh sender retry has a new Hookway envelope ID.

Standard queues/topics can use `group_id` without FIFO deduplication. Additional
per-message bindings are:

- `sqs.queue_owner_account_id`: owner used to resolve an existing queue.
- `sqs.delay_seconds`: 0–900 seconds on standard queues; forbidden on FIFO.
- `sns.topic_arn`: an existing topic binding, checked against target and region.
- `sns.subject`: a bounded subject on standard topics.
- `sns.message_structure: json`: protocol-specific JSON with a `default`
  string and string values; cannot be combined with message attributes.
- `resource_attributes`: supported resource creation settings such as
  retention, visibility, encryption and FIFO throughput settings. Unknown
  attributes fail validation; immutable conflicts fail resource resolution.
  Static targets are checked at startup; dynamic targets are resolved on
  their first publication. Existing resources follow
  `on_existing_conflict: reconcile|warn|error`.

An explicit publish retry policy accepts 1–10 attempts and bounded backoff,
disables overlapping SDK retries, and stays within the publish timeout.
An ambiguous transport failure may already have delivered the message;
standard resources and retries remain at least once. See
[standard SQS](../../examples/destinations/sqs.yaml),
[FIFO SQS](../../examples/destinations/fifo-sqs.yaml) and
[SNS projection](../../examples/adapters/changed-fields-sns.yaml).

## Typed environment routing

Top-level `routing` contains `profiles`, `environments` and optional `stores`.
A profile names a 12-digit `account_id` and `credential_mode`. Each environment
admits specific profiles and accounts and declares `destination_prefix`,
optional aliases and optional `backend_host`. These are operator allowlists;
request data cannot invent a destination account, prefix or backend.

A `route` node declares its `profile` and either a `fixed` environment or an
ordered list of `candidates`. Candidates select body, header or variable
paths. Without a store they identify an admitted environment; with a named
store they include `id_type` and select a typed identifier. Missing candidates
can fall through; errors and a found but denied environment fail closed.
The output variable prefix (`name`, default `route`) exposes the admitted
binding to subsequent target templates. See
[admission.yaml](../../examples/routing/admission.yaml) and
[admitted proxy](../../examples/proxy/admitted.yaml).

The native DynamoDB store requires an existing table with string partition
and sort keys. Defaults are `id`, `id_type`, `env_name`, `created_at`, with a
2s operation timeout; field names are configurable. Reads are consistent and
there is no process-local routing-row cache. DynamoDB TTL is not a routing
prerequisite or an admission mechanism. Deploy admission removals to every
replica before deleting associated records.

`register_route` supports `direct` registration and `after_create`. The latter
uses an explicitly selected upstream ID after HTTP 200 or 201. A registration
write failure preserves the successful upstream response and records recovery
metadata, rather than prompting the caller to create the object again.
It uses bounded attempts, timeout and backoff. The preceding HTTP node's
`response_bindings.fields` supplies named scalar values. See
[registration.yaml](../../examples/routing/registration.yaml).

Routing table backfills, exports and cleanup use external datastore tooling.
For failed registration, retain the returned resource ID and use an authenticated
registration-only flow or external datastore tooling; do not repeat creation.
Core registration is last-write-wins. Conditional recovery that protects a
reassigned mapping belongs to the external recovery procedure.

## Managed outbound credentials

Top-level `credentials` contains named profiles. HTTP nodes select one via
`credential.profile`, or map admitted environments through
`credential.environment_profiles` plus a configured routing profile.
Each profile declares 1–64 exact `allowed_origins`; no userinfo, path, query
or fragment is allowed. HTTPS is required unless `allow_private_targets`
explicitly permits private deployment targets and HTTP.

- `static`: managed headers from `secret_ref`, with an optional nonsecret
  prefix. At least one header is required.
- `oauth_client_credentials`: client ID/secret references, token URL,
  Basic or form client authentication, optional scope, selected token and
  expiry fields. Cached acquisition uses a single shared refresh; an optional
  returned instance origin must also be allowlisted. `invalid_session` can
  trigger one refresh and one resend for a matching response marker.
- `token_exchange`: form or JSON request with nonsecret `parameters` and
  referenced `secret_parameters`, plus an explicit token response path.
  This exchange occurs per request and is not a reusable OAuth session cache.

Each header/parameter map has at most 32 entries. Managed headers cannot
override framing, hop-by-hop headers or `X-Hookway-Hops`; token profiles own
Authorization. Prefixes are bounded and reject CR, LF and NUL. Acquisition
timeouts default to 10s and cannot exceed 30s. Token responses and selected
values are bounded. Resolved credentials stay inside the native HTTP boundary,
outside graph payloads, script input, captures and builder exports.

Managed requests disallow redirects and additional HTTP retry policies. An
OAuth refresh resend can still repeat a side effect after an ambiguous
response; match the upstream session-expiry contract deliberately. Examples:
[static](../../examples/credentials/static.yaml),
[OAuth](../../examples/credentials/oauth.yaml),
[token exchange](../../examples/credentials/token-exchange.yaml).

## Proxy contracts and document handling

`http_trigger.methods: [ANY]` accepts all methods. `read_body.allow_empty`
supports GET/HEAD bodies when downstream nodes also permit them.
`read_body.max_bytes` is the enforced ingress cap; `server.max_body_bytes`
is a lint reference, not an inherited cap. Set effective limits on every flow.

HTTP nodes can use a literal/template `url`, or `upstream` derived from an
admitted routing binding. `forward_method`, `forward_query` and
`query_mode: all|first|last` control method and duplicate query handling.
`remove_query`, `header_mode: forward|replace`, `headers`, `forward_headers`
and `remove_headers` define explicit projections. Framing and hop-by-hop
headers are always filtered. `body_methods`, `remove_body_fields`,
`require_headers` and `require_body_fields` constrain supported requests.
`max_response_bytes` bounds responses (default 10 MiB, maximum 64 MiB).
The reader consumes at most the cap plus one byte; overflow returns `502`.
Automatic decompression is disabled, so compressed replies retain their wire
bytes. JSON response bindings have independent byte and structural limits;
selection failure is metadata and does not replace an otherwise successful reply.
`response_bindings` exposes only named bounded scalar values and status to
later nodes. `respond.passthrough` relays the filtered upstream response;
HEAD never sends a response body. See [request-contract.yaml](../../examples/proxy/request-contract.yaml).

JSON schema transforms validate offline schemas, with no remote `$ref`
fetches. `coercions` explicitly declare a dotted path and one scalar type;
union IDs are left unchanged unless explicitly coerced. `strip_fields`
removes exact dotted fields after validation. See
[json-projection.yaml](../../examples/adapters/json-projection.yaml).

JSON envelopes check depth and node count before decoding (default 128 levels
and 1,000,000 nodes). Set `envelope.json.max_depth` up to 1024 for a legitimate
deep document, or lower `max_nodes` for a smaller budget. Keep the wire cap on
`read_body`; structural and byte budgets protect different allocations. See
[combined resource limits and sizing](../operations/resource-limits.md).

XML parsing preserves ordered mixed content, namespace prefixes, comments
and processing instructions. It rejects DTD/directive input and bounds
bytes, depth and nodes. Defaults are 1 MiB, depth 256 and 100,000 nodes.
For a decimal 12 MB XML document, set both `read_body.max_bytes` and
`envelope.xml.max_bytes` to at least **12,000,000**; other limits still apply.
Opaque PDF/DOCX relays should use raw bodies, or bounded multipart parsing
when that is the actual wire format. Account for multipart/base64 overhead.
Broker payloads require projection or object storage because their limits
are much smaller. [Ordered XML example](../../examples/adapters/xml-ordered.yaml).

Scripts receive an explicit XML tree with legacy `text`/`children` views
and ordered `content`. `text_view` marks the original aggregate text: changing
`text` rewrites direct text content, while changing `content` with unchanged
`text` preserves its new ordering. Preserve the tagged tree to forward XML.
Script-generated names, comments and processing instructions are validated.

## Batches and HTTP responses

`batch` authenticates the parent once and runs an isolated child flow per
event. `shared` accepts an object or array and processes children sequentially,
returning best-effort HTTP 200 accounting. `dedicated` requires an array,
selects one receiving environment, skips other admitted environments and
uses bounded concurrency (default 8). Eligible failures return HTTP 502 after
the attempted children complete. Unknown environments fail closed.

Child flows have 1–64 nodes, exactly one publish node, and IDs prefixed with
the parent batch ID plus a dot. Supported nodes are transform, code, filter,
if/else, switch and publish; no trigger, HTTP request, response or nested batch.
`max_children` defaults to 1,000 (maximum 10,000), `max_inflight` defaults to
64 per batch node, with a process-wide ceiling of 1,024 active children.
Timeouts and cancellation bound admitted child work. Child event/group
variables can supply stable broker identities. Batch snapshot redrive is
unsupported; with replay configured, set `capture_failures: false`.
Examples: [shared](../../examples/adapters/batch-shared.yaml),
[dedicated](../../examples/adapters/batch-dedicated.yaml).

Flow `responses` maps finite execution outcomes to static status, headers and
body: `responded`, `published`, `dropped`, `publish_error`, `partial_failure`,
`auth_error`, `body_error`, `lookup_error`, `payload_too_large`, `error`.
Unspecified outcomes retain defaults. A successful explicit response or batch
response takes precedence; execution errors take their error policy.
Delivery metrics still report the execution outcome, regardless of the HTTP
status. A policy that acknowledges failed publication changes sender retry
behavior; configure it only to match the expected contract. Set Retry-After
explicitly when a custom error response needs it.

## Deployment and verification

Admission is bounded per instance (`max_inflight_requests`, default 256), with
optional bounded waiting before body buffering. Rate and replay guards select
explicit memory, Redis or PostgreSQL stores. Memory applies per process; shared
backends coordinate replicas. See [Protection stores](protection-stores.md).
Scale replicas and CPU/memory using measured request mix and downstream latency.
There is no durable ingress spool. Sender success depends on the configured
flow result and broker confirmation, subject to explicit response policies.

Prometheus exposes admission capacity/occupancy/waiting, secret refresh,
credential acquisition, routing/registration and batch-child outcomes. Labels
use configured identities and finite outcomes, rather than sender event IDs
or credentials. Metrics can feed existing monitoring; alerting and DLQ
monitoring are optional deployment concerns.

AWS protocol checks live in `make aws-integration-test`; LocalStack does not
establish real AWS IAM, quotas or durability. The
[performance report](../evidence/performance.md) records local ingress measurements,
methodology and workload limits. Traffic splitting and rollout orchestration
belong to the deployment platform or load balancer.
Validate request/response and broker-message fixtures with your existing test
tools. For load measurements, count planned, skipped and completed work and
latency failures; skipped work cannot count as successful throughput.

For provider-specific signatures and retry behavior, see
[Provider compatibility](provider-compatibility.md). Configure duplicate and
error responses to match your sender's retry policy.
