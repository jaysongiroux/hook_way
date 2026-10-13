# Configuration

Use YAML to define the server, destinations, and flows. Start with
[examples/config.yaml](../../examples/config.yaml) or choose a flow from the
[example catalog](../../examples/README.md).

Set the file with `HOOKWAY_CONFIG` or `--config`; the flag wins. String values
support `${ENV_NAME}` expansion. Check the file before serving it:

```sh
hookway validate --config core.yaml
```

This reference explains fields and defaults. For workflows, see
[ingress adapters](../guides/ingress-adapters.md), [JSON validation](../guides/json-validation.md),
[lookups](../guides/lookups.md), and [protection stores](../guides/protection-stores.md).

### Optional flow files

Keep all flows inline, or store every flow in its own YAML file. An entry file
continues to hold all central settings and shared resources:

```yaml
# core.yaml
server:
  addr: ":8080"
flows:
  orders:
    file: flows/orders.yaml
  billing:
    file: flows/billing.yaml
```

Each referenced file contains a flow body directly: `nodes`, `edges`, and optional
`responses`, `delivery_failure_policy`, or `capture_failures`. The flow name comes
from the entry file. Flow files cannot contain a `flows` wrapper, process settings,
or further file references. Inline flows and file references cannot be mixed.

References are literal `.yaml` or `.yml` paths relative to the entry file's
directory. Absolute paths, paths escaping that directory, escaping symlinks,
URLs, glob patterns, environment variables in paths, and multiple references to
the same file are rejected. YAML anchors are local to each file. All files are
assembled before graph compilation, shared-reference checks and cross-flow
validation; missing or invalid files prevent startup.

Use the same commands for either layout:

```sh
hookway validate --config core.yaml
hookway validate --config core.yaml --json --no-env
hookway serve --config core.yaml
```

For a complete example, see [multi-YAML configuration](../../examples/multi-yaml/README.md).
Mount or deploy the entire configuration directory, retaining the relative paths;
mounting only the entry YAML will leave its flow files unavailable. Unpack builder
ZIP downloads before starting Hookway. Replay's separate configuration continues
to be passed to `hookway-replay` independently.

In the builder, **Config → Export YAML → YAML organization** selects **Single
YAML** or **One YAML per flow**. Filename controls and per-file previews use the
same graphs and forms; changing organization does not remove flows. Downloads
preserve the selected layout and become a ZIP when there is more than one flow
or more than one document. A configured replay document is included in that ZIP.

**Config → Import YAML → Files / ZIP** reopens a ZIP, selected YAML files, or a
configuration directory. Select the entry YAML when more than one candidate is
present. Flat file selection can lose directory paths; use the attachment
controls to map a selected file to a missing reference. Complete file imports
replace the workspace only after syntax and layout checks succeed. Semantic
validation issues remain editable. Comments and formatting are regenerated,
while configuration values and assigned filenames are retained. Imports allow
up to 1,024 files, 5 MiB per YAML document and 64 MiB total decompressed YAML.

There is no configuration hot reload or SIGHUP handler. Change flows, admin tokens
and deployment bindings by restarting with the new configuration; refreshing secret references
are a separate mechanism described below.

Startup parses and validates the config before connecting to its destinations or serving traffic. The file defines `flows:`. Invalid YAML, unknown fields, missing required values, invalid durations, duplicate or overlapping flow paths, and unsupported backend/auth types stop the app with field-level errors and suggestions.

Use `hookway validate` to check without connecting to dependencies. Supported
risk choices produce actionable warnings, including unlimited request admission,
universal proxy trust, known sample credentials, and weaker acknowledgement
policies. Ordinary validation accepts these choices; `--strict` fails on warnings
as an optional policy gate. `--no-env` checks templates without reading secret
references or expanding environment values. See [CLI validation](cli.md#hookway-validate)
for the complete policy and JSON missing-environment behavior. Local HTTP,
private destinations, explicit `no_auth`, and proxy-terminated TLS remain valid
operator choices.

Example validation output:

```txt
config validation failed
- server.addr: listen address is required Suggestion: set server.addr to a host:port such as ":8080"
- flows.github.nodes[2].secret: secret is required Suggestion: set secret or secret_ref
```

## Server

- `server.addr`: required listen address, example `":8080"`.
- `server.read_timeout`: optional duration, default `5s`. If set explicitly, must be positive; omit the field (rather than setting `0s` or a negative value) to use the default.
- `server.write_timeout`: optional duration, default `10s`. Same non-positive restriction as `read_timeout`.
- `server.idle_timeout`: optional duration, default `60s`. Same non-positive restriction as `read_timeout`.
- `server.shutdown_delay`: optional duration, default `0s`. On termination, readiness immediately returns 503 while liveness stays healthy. Continue serving for this interval so load balancers can remove the replica, then stop admitting requests and drain. Negative values are rejected.
- `server.shutdown_timeout`: optional duration, default `10s`. HTTP request drain budget after withdrawal. Same non-positive restriction as `read_timeout`.
- `server.shutdown_total_timeout`: optional duration. Omitted uses the greater of `60s` or `shutdown_delay + shutdown_timeout + 30s`, preserving longer existing drain settings. Total budget from termination through withdrawal, request/control drain and resource cleanup. Must exceed `shutdown_delay + shutdown_timeout`; size it for replay execution deadlines and sequential publisher drains too. Exhausting it exits unsuccessfully and may lose volatile work. Set the platform termination allowance longer than this budget.
- `server.publish_timeout`: optional duration, default `10s` (matches `write_timeout`/`shutdown_timeout`). Same non-positive restriction as `read_timeout`. Bounds the fan-out publish for a single webhook request; the publish runs detached from the inbound request's cancellation (a sender disconnect or timeout does not abort in-flight publishes) but is still bounded by this deadline so a stalled destination cannot hang a delivery indefinitely. Config validation rejects `publish_timeout` greater than `shutdown_timeout` (a slow synchronous publish could otherwise make a clean SIGTERM shutdown time out and report failure) or greater than `write_timeout` (a sync-mode response can't be written after `write_timeout` elapses, so the sender would see a spurious timeout and retry). Lower `publish_timeout` or raise the other value to resolve either error.
- `server.max_body_bytes`: required positive integer used for configuration linting. Each `read_body.max_bytes` sets the enforced limit; it does not inherit this value.
- `server.max_inflight_requests`: optional integer. Omitted or explicit `0` defaults to `256`, capping concurrently processed webhook requests; by default, once reached, further requests are shed with `503 Service Unavailable` and a `Retry-After` header before any body is buffered. Explicit `-1` disables the limit (unlimited in-flight requests). Any other negative value is rejected. Health, readiness, and metrics endpoints are never subject to this limit.
- `server.max_pending_requests` and `server.admission_wait_timeout`: optional bounded waiting before an execution slot becomes available. Set both to positive values, for example `256` and `1s`; omitting both retains immediate overload rejection. Requires a finite `max_inflight_requests`. Waiting uses the existing HTTP handler goroutine and reads no body. Queue overflow or timeout returns `503` with `Retry-After`; cancellation or server close ends waiting without running the flow. The wait timeout must be shorter than read, write and shutdown timeouts. It uses part of the response budget, so leave room for subsequent processing and publishing. Observe `hookway_admission_waiting_requests`, `hookway_admission_wait_total{flow,result}` and `hookway_admission_wait_duration_seconds{flow,result}`; results are bounded to admitted, full, timeout, canceled and closing. This queue is volatile and sends success only after the configured flow completes.

Admission rejection closes an HTTP/1 connection with an unread body so a slow sender cannot delay the `503` while the HTTP server tries to drain it for reuse. Healthy keep-alive connections are unchanged. Admission limits executions, not all accepted TCP connections; size proxy/OS connection limits independently. See [resource limits](../operations/resource-limits.md) for combined time and memory budgets.

- `server.fanout_concurrency`: optional non-negative integer. Caps concurrent publishes per webhook request for flows using `delivery_failure_policy: skip_failed`. `0` (default) uses `8`.
- `server.readiness_require_all`: optional boolean, default `false`. When false, `/ready` returns 200 with a per-flow body while at least one flow is ready. When true, `/ready` returns 503 if any flow is unready.

### HTTP endpoints and readiness

| Endpoint | Purpose |
| --- | --- |
| Configured flow paths | Webhook ingress; methods come from the trigger |
| `GET /healthz`, `GET /health` | Process liveness; not destination readiness |
| `GET /ready`, `GET /readyz` | Cached per-flow readiness |
| `GET /metrics` | Prometheus when enabled, on the configured listener/path |

An unready protection store makes readiness return 503 globally. Otherwise,
readiness is reported per flow: a flow is ready when all destinations referenced
by its publish nodes are ready. Each unique destination is probed once per interval.

By default the HTTP status is 200 while **at least one flow** is ready. Set
`server.readiness_require_all: true` to require every flow to be ready. An async
webhook destination becomes unready at 90% queue capacity. Liveness stays healthy
even when destinations are unavailable.

Readiness responses use the probe cache rather than contacting destinations on
every HTTP request. See [Upgrades](../operations/upgrades.md#core-shutdown-contract) for readiness
during termination.

Successful webhook response:

```json
{
  "status": "accepted",
  "endpoint": "github_events",
  "destinations": [
    {"destination_queue_id": "primary", "queue": "github-webhooks"}
  ],
  "event_id": "018f...",
  "request_id": "req_..."
}
```

`endpoint` is the **flow name** (compatibility field). `destination_queue_id` is the `destinations:` map key; `queue` is the publish `target`. A `respond` node can replace this envelope with an explicit HTTP status and body (sync proxy).


## Secret references

`auth.secret_ref` is an alternative to inline `auth.secret`. Exactly one of `secret` or `secret_ref` is required. The resolver is cached and refreshed on a timer — never fetched per request.

```yaml
- id: auth
  type: auth
  scheme: hmac_sha256_hex
  secret_ref: {provider: env, name: WEBHOOK_SECRET, refresh: 15m, overlap: 5m}
  signature_header: X-Hub-Signature-256
```

- `provider`: `env` (read `name` as an environment variable), `file` (read `name` as a path), or `aws_secrets_manager`. For AWS, `selection: exact` reads only the configured secret; `selection: newest_matching` requires a matching name and chooses the newest `LastChangedDate`. Omitted selection preserves legacy behavior: list matching names, use the newest, and fall back to the exact name if listing fails or finds nothing.
- `name`: env var, file path, or Secrets Manager name/prefix.
- `refresh`: optional duration, default `15m`.
- `overlap`: optional duration, default `0`. During this window after a value change, **both** the previous and current secrets verify, allowing a planned overlap during rotation.

A refresh failure keeps serving the last known-good value, emits `hookway_secret_refresh_failures_total`, and logs at error. AWS credentials use the default SDK chain; `AWS_ENDPOINT_URL` targets LocalStack.

## Security

- `security.trusted_proxy_headers`: optional boolean. When false (default), the source IP is always the TCP peer (`RemoteAddr`) and `X-Forwarded-For`/`X-Real-IP` are ignored.
- `security.trusted_proxies`: optional list of IP addresses or CIDR ranges. When `trusted_proxy_headers` is true *and* this list is non-empty, forwarding headers are only honored when the immediate TCP peer falls inside one of these ranges; otherwise the peer IP is used. This prevents clients from spoofing `X-Forwarded-For` to bypass the IP allowlist and per-IP rate limiting. Leaving it empty with the flag on is the legacy permissive behavior and should only be used when clients cannot reach Hookway directly.
- `security.request_id_header`: optional request ID header name.
- `security.redacted_header_denylist`: optional list of exact HTTP header names that are redacted in logs and in the published envelope. This augments automatic redaction of obvious secrets; it does not replace it.
- `security.forwarded_header_allowlist`: optional list of HTTP header names. When non-empty, only these headers are **forwarded** to destinations (fail-safe); every other header is dropped entirely. The denylist and built-in sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through. When empty, behavior is the legacy denylist filter (everything except denylisted/sensitive headers is forwarded). Request **logs** use `logging.header_allowlist` instead; this list does not control what is logged.
- `security.loop_protection`: leftover YAML. It is still parsed and validated, but **inbound hop rejection is not applied from this block**. Put a `loop_guard` node on each flow that should reject `X-Hookway-Hops`. Outbound webhook deliveries still stamp `X-Hookway-Hops` regardless. See [Loop protection](#loop-protection).

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
  # Optional fail-safe: forward only these headers to consumers.
  forwarded_header_allowlist:
    - "Content-Type"
    - "X-Request-ID"
  # Inbound hop rejection is a loop_guard node, not this block.
  # loop_protection:
  #   enabled: true
  #   max_hops: 3
```

### `security.api_key`

Optional shared API key protecting the metrics endpoint and the config API
(`hookway editor`). When set, callers must send `Authorization: Bearer <key>`.
When empty or omitted, those endpoints are unauthenticated. The value may be an
`${ENV}` reference (expanded on config load), e.g.:

```yaml
security:
  api_key: "${HOOKWAY_API_KEY}"
```

### Loop protection

A webhook destination can point back at a Hookway path (directly, or through a chain of Hookways), creating a delivery loop that would amplify traffic without bound. Hookway detects this with a hop counter carried in the `X-Hookway-Hops` header:

- Every outbound **webhook** delivery is stamped with `X-Hookway-Hops: <inbound count + 1>`, in both envelope and proxy payload modes. A request that never passed through Hookway has no header and counts as `0`. `http_request` nodes also increment the header on the outbound call.
- On the inbound side, a `loop_guard` node rejects a request whose hop count is greater than or equal to `max_hops` (default `3`) with `400`, before `read_body`. The rejection is a terminal `400` so a well-behaved sender — including an upstream Hookway, whose delivery engine treats `4xx` as non-retryable — stops rather than retrying the loop. A flow with no `loop_guard` does not reject hop headers.

`security.loop_protection` is accepted YAML (and `max_hops` is still validated) but is **not** wired to the serve path. Do not rely on it for inbound enforcement.

Because the hop count increments per Hookway hop (not per loop iteration), a tight self-loop is broken after at most `max_hops` iterations, and an intentional finite chain of up to `max_hops` Hookways still works. A malformed or negative header value counts as `0`, so it can only reset the counter, never bypass the limit. The header is always stamped on outbound webhook deliveries even when a given flow has no `loop_guard`, so a downstream Hookway can still detect the loop.

This mechanism relies on the header surviving each hop; a loop that transits an intermediary which strips unknown headers will not be caught. Rejections are counted by `hookway_loop_rejected_total` (the flow name is the `endpoint` label) and logged at Warn.

## Rate Limit

There is no process-global `rate_limit:` block. Put a `rate_limit` node in each flow (typically before `read_body`). See [Flows § Trigger, gates, and body](#trigger-gates-and-body). A flow without one is a lint warning.

## Metrics

- `metrics.enabled`: boolean.
- `metrics.path`: path beginning with `/`, usually `/metrics`.
- `metrics.addr`: optional listen address such as `":9090"`. When set, metrics are served on a dedicated listener instead of the public webhook port, so `/metrics` is not exposed on the internet-facing port. When empty, metrics are mounted on the main server at `metrics.path`.
- `metrics.runtime_metrics`: optional boolean, default `true`. When enabled, registers the standard Go runtime and process collectors (`go_goroutines`, `go_memstats_*`, `process_cpu_seconds_total`, `process_resident_memory_bytes`, open FDs). `process_*` metrics require Linux (`/proc`); Go runtime metrics are cross-platform. Set to `false` if your platform already collects container CPU/memory (e.g. cAdvisor) and you want to avoid duplication.

When disabled, the metrics route is not registered.

Metrics include HTTP request counts, auth failures, rate limits, load-shedding
saturation (`hookway_saturated_total`), filter drops (`hookway_filtered_total`), loop
rejections (`hookway_loop_rejected_total`), publish latency,
RabbitMQ reconnect/confirm/retry/DLQ/unroutable (`hookway_rabbitmq_unroutable_total`) counters, config load failures, and —
when `runtime_metrics` is enabled — Go runtime and process CPU/memory metrics. Startup
config failures still block serving traffic, so config failure metrics are
recorded in-process before exit and are most useful for tests or future reload
paths.

Some of the less obvious series, which are easy to miss when building a
dashboard or an alert:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `hookway_build_info` | gauge | `version`, `commit` | Always `1`. The build the process came from, as injected by `-ldflags` (`dev`/`none` for a plain `go build`). Join on it to attribute a regression to a release. |
| `hookway_request_body_bytes` | histogram | `endpoint`, `destination_queue_id`, `queue` | Inbound webhook body size. Buckets run 128 B → 1 MiB. Use it to size `read_body.max_bytes` from observed traffic rather than guessing. |
| `hookway_variable_lookup_total` | counter | `endpoint`, `variable`, `provider`, `result` | Lookup-source variable resolutions. `result` is `hit`, `miss`, `cached`, or `error`. |
| `hookway_variable_lookup_duration_seconds` | histogram | `endpoint`, `variable`, `provider` | Latency of those resolutions, cache hits included. |
| `hookway_variable_unresolved_total` | counter | `endpoint`, `variable`, `action` | Requests whose routing variables could not be resolved, by what happened next: `rejected` or `fallback`. A rising `rejected` rate means traffic is being dropped on a config gap. |
| `hookway_compensation_failed_total` | counter | `flow` | Replay-reservation *release* failures. Non-zero is worth an alert: a silent failure leaves the reservation in place until its TTL, so a legitimate sender retry gets a `409` and the event is lost. |
| `hookway_rabbitmq_confirms_total` | counter | — | Publisher confirms received from RabbitMQ. |
| `hookway_rabbitmq_retries_total` | counter | — | RabbitMQ publish retries. Both are process-wide, with no destination label. |

### Dashboards and interpretation

Enable metrics explicitly and, when needed, Go/process collectors. Import
[`hookway-overview.json`](../../metrics/grafana/hookway-overview.json) into Grafana
and select your Prometheus data source, job, instance and flow. Import the separate
[`hookway-replay.json`](../../metrics/grafana/hookway-replay.json) for replay.
Scrape each replica's private metrics listener with the appropriate credential.

The core dashboard includes HTTP/publication latency and counts, bounded/unlimited
admission mode, executing capacity/utilization, waiting depth/outcomes/duration,
flow outcomes, routing/registration results, credential operations and batch child
outcomes. Node execution, lookup, secret refresh, capture/redrive and runtime panels
remain available. Flow-level panels do not apply destination/queue filters; those
labels are not present on all request or node metrics. Routing/credential/batch
panels use their configured node/profile labels and have no flow label.

These metrics do not poll SQS or report broker DLQ activity. Existing optional
Prometheus alert templates can be used with your monitoring stack; additional
alerting or DLQ instrumentation is not a requirement for ingress deployment.
See [OpenTelemetry spans](#opentelemetry-spans) for the separate tracing boundary.

### OpenTelemetry spans

`internal/httpapi/flowserver.go` holds a package-level
`otel.Tracer("hookway")` and starts one span per executed graph node, named
`hookway.node.<node_type>` (`hookway.node.auth`, `hookway.node.publish`, …).
Each span is timestamped to the node's real start and end, records the node's
error and sets an error status when one occurred, and carries five attributes:

| Attribute | Value |
|---|---|
| `hookway.flow` | flow name |
| `hookway.node_id` | node id from the config |
| `hookway.node_type` | node type |
| `hookway.request_id` | request id |
| `hookway.event_id` | envelope event id |

Two facts matter before you plan around this:

- The spans are emitted on the **global** OTel tracer provider. Hookway never
  sets one. With no provider registered the global default is a no-op, so the
  spans go nowhere and cost almost nothing.
- **No exporter ships.** There is no OTLP endpoint config, no
  `otel.SetTracerProvider` call, no collector wiring. Nothing in Hookway sends
  a span anywhere.

To get traces out you must register your own tracer provider and exporter in
your own build. Hookway does not claim OpenTelemetry support; it propagates
`traceparent` end to end (see the envelope reference below) and emits spans
into whatever global provider happens to be installed.

## Replay

The optional `replay:` block connects core to the replay service
(`hookway-replay`), which stores failed deliveries for operator-initiated retry
and view-only webhook inspections. Without the block, failure capture and the control
listener are off. The service itself is documented in
[replay-service.md](../operations/replay-service.md).

```yaml
replay:
  url: "https://replay.internal"           # the replay service's API (plain HTTP unless a proxy or mesh terminates TLS)
  capture_token: "${REPLAY_CAPTURE_TOKEN}" # a replay key with the capture scope
  max_record_bytes: 16777216               # per artifact; larger → metadata-only record
  tls: {ca_cert: "", client_cert: "", client_key: "", server_name: ""}
  control:
    addr: ":9092"                          # private; must differ from server.addr and metrics.addr
    token: "${REPLAY_CONTROL_TOKEN}"
    max_concurrent: 8
```

Core records a failure only when it acknowledged the request (final status
2xx) and no `on_error` or `dead_letter` path handled it — in practice a
publish (or a success continuation after it) that failed under
`delivery_failure_policy: skip_failed`, or an async webhook delivery that
failed for good — retries exhausted or a terminal 4xx — with no dead letter,
or whose dead-letter dispatch failed. An async
failure waits for its request's final status before it is kept. A flow opts
out with `capture_failures: false`.

Each failure record carries metadata (flow, node, destination, sanitized error,
attempts, config hashes), a snapshot of the failed node's input, and the
payload the node was sending. A snapshot or payload over `max_record_bytes`,
or one that cannot be reproduced exactly, makes the record metadata-only and
not retryable.

Uploads are best effort and never touch a request: four workers, at most 128
records and 64 MiB buffered, at most three attempts within five seconds. A
record that cannot be kept is dropped, logged at Error with its `event_id`,
`flow`, and `node_id`, and counted. Replay being slow or down never changes a
response or readiness. Both tokens must be at least 32 characters and
must differ; `${VAR}` references are accepted by `validate` without
expansion. `tls` sets the client TLS for uploads (`client_cert` and
`client_key` together for mutual TLS).

The control listener (`replay.control.addr`) serves one route, `POST
/internal/replay/v1/deliver`, for the replay service's workers; it is never
part of readiness. Every request needs `Authorization: Bearer
<control.token>`. Core reads the whole request before it starts, and answers
`503` with code `not_started` — the only answer after which replay resends —
when it is shutting down or already running `max_concurrent` redrives. A
record the current configuration can no longer run is refused with `422` and a
block reason (`unsupported_snapshot_version`, `hash_mismatch`,
`flow_missing`, `flow_broken`, `node_missing`, `node_type_changed`,
`destination_changed`, `target_not_configured`), and nothing is sent. Otherwise
core resumes the flow at the failed node (or re-sends a `publish_only`
message) with inline delivery, under the deadline replay sends, and reports
each node's outcome; downstream failures come back as child records. A
redrive never captures, dead-letters, or runs compensations. At
shutdown readiness first withdraws for `server.shutdown_delay`, then every
listener stops accepting. The control listener answers `503 not_started` to
requests still arriving and waits for in-flight redrives before publishers close.
The redrive execution deadline and `server.shutdown_total_timeout` bound this
wait. Budget the total for withdrawal, the longer of request/control drain,
sequential publisher drains, and capture cleanup. Set the platform termination
allowance longer than the total budget; see [Upgrades and rollback](../operations/upgrades.md).

| Metric | Labels | Description |
|--------|--------|-------------|
| `hookway_capture_total` | `flow`, `outcome` | Captured failures by outcome: `uploaded` or `dropped`. |
| `hookway_capture_dropped_total` | `reason` | Records that never reached replay: `buffer_full`, `upload_failed` (three attempts or five seconds spent), `capacity` (replay answered 507), `rejected` (replay refused the record, or core could not build it), `shutdown`. |
| `hookway_inspection_total` | `flow`, `outcome` | View-only inspections by upload outcome: `uploaded` or `dropped`. |
| `hookway_inspection_dropped_total` | `reason` | Inspection uploads dropped by the same bounded-uploader reasons as failure capture. Captured content is never logged. |
| `hookway_capture_buffer_bytes` | — | Shared failure/inspection bytes reserved during requests or waiting for upload, including in-flight uploads (gauge). |
| `hookway_control_runs_total` | `flow`, `outcome` | Deliver requests answered: `succeeded`, `failed` (the resumed node failed), `blocked` (422), `not_started` (503), `rejected` (401/4xx), `error` (500). `flow` is a configured flow or `unknown`. |
| `hookway_control_inflight` | — | Redrives running now (gauge). |

## Connections

`connections` is an optional top-level map of named credentials keyed by stable id. A connection holds everything needed to *reach and authenticate* a backend; a destination that references it keeps only its own sink — the bucket, table, topic, or key prefix it writes to. One AWS account can then serve an `s3`, an `sns`, an `sqs`, and a `lambda` destination from a single declaration, and rotating the credential is one edit in one place.

```yaml
connections:
  aws_prod:
    type: aws
    region: us-east-1
    access_key_id: "${AWS_ACCESS_KEY_ID}"
    secret_access_key: "${AWS_SECRET_ACCESS_KEY}"

destinations:
  events_bucket:
    type: s3
    connection: aws_prod
    s3: {bucket: hookway-events, sse: AES256}
  notify:
    type: sns
    connection: aws_prod
```

A connection is authored **flat**: `type` sits alongside its own fields rather than above a per-type block, unlike a destination. Ids are 1–128 characters of letters, digits, underscore, or hyphen — stricter than a destination id, which also permits `.`, because validation issue paths are resolved segment by segment and a dot makes them ambiguous.

Connections are entirely optional and purely additive. Every destination can still be configured inline, which is what `examples/destinations/*.yaml` do.

### Connection types

| `type` | Owns | Serves destination types | Destination keeps |
|---|---|---|---|
| `aws` | `region`, `endpoint`, `access_key_id`, `secret_access_key` | `s3`, `sqs`, `sns`, `lambda` | s3: `bucket`, `create`, `sse`, `sse_kms_key_id` · lambda: `invocation_type` · sqs/sns: nothing |
| `redis` | `mode`, `url`, `addrs`, `master_name`, `username`, `password`, `db`, `tls` | `redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv` | `key_prefix` |
| `postgres` | `url`, `max_conns`, `tls` | `postgres` | `schema`, `table` |
| `kafka` | `brokers`, `sasl`, `tls` | `kafka` | `acks`, `compression`, `publish_timeout`, `retry` |
| `mongodb` | `uri`, `max_pool_size`, `tls` | `mongodb` | `database` |
| `rabbitmq` | `url`, `publisher_channels`, `tls` | `rabbitmq` | `exchange`, `exchange_type`, `durable`, `publish_timeout`, `declare_queues`, `dead_letter`, `queue_defaults`, `retry` |
| `temporal` | `host_port`, `namespace`, `api_key`, `connect_timeout`, `tls` | `temporal` | `start_timeout` |

`key_prefix` stays on the destination on purpose, so two redis destinations can share one server with different prefixes.

There is **no `webhook` connection type**. It would carry an auth header and TLS material but no host, so unlike every other type the credential would not travel with the address of the thing it authenticates to — one profile could be attached to destinations pointing at different vendors. Shared outbound webhook auth is an auth profile rather than a connection, and `destinations.<id>.webhook.auth` remains per-destination.

`tls.enabled` exists only on `redis`, `kafka`, and `temporal`. For `postgres`, `mongodb`, and `rabbitmq`, transport security is implied by the connection string (`sslmode`, `mongodb+srv`, `amqps://`) and the `tls` block carries only custom CA and mutual-TLS material.

### The split is strict

A field belongs either to the connection or to the destination, never both. Setting a connection-owned field on a destination that also names a connection is a validation error:

```
destinations.events.s3.region: region is provided by connection "aws_prod" and
must not be set on the destination
```

This is deliberate: an override-merge would leave "which of these two places did this value come from" as a question you have to answer by reading both. Validation of these bindings runs against the YAML document rather than the decoded config, so a field written as an explicit zero — `redis.db: 0`, `tls.insecure_skip_verify: false`, `postgres.max_conns: 0` — is caught rather than mistaken for an omitted one.

An error in a value the connection supplied is reported at the path you wrote, with the destination that pulled it in named for context:

```
connections.aws_prod.region: region is required for s3 destinations
(via destinations.events.connection)
```

A connection defined but referenced by nothing is reported at severity `warning`, so it does not block `hookway validate` or startup.

### Resolution and rotation

Connections are resolved once, at load time, exactly like inline config. The merged result is an ordinary destination, so nothing about publishing, readiness, or retry behaviour changes.

That means **rotating a destination credential still requires a restart.** `connections` changes *where* a secret lives, not *when* it is read. Inbound auth and [managed HTTP credentials](../guides/ingress-adapters.md#managed-outbound-credentials) support live rotation, via [`auth.secret_ref`](#secret-references) and its background refresh and overlap window; there is no equivalent for destinations, because the backend clients (pgxpool, Kafka writers, Redis clients, AMQP connections) capture their credentials at construction.

Secrets in a connection are written the same way as anywhere else: `${ENV_VAR}` references, expanded at load. Note that expansion runs over the whole file as text *before* it is parsed, so a `${VAR}` written inside a comment still counts as a reference.

### Blast radius

Consolidating credentials narrows the number of places to look, and widens what a single leaked block exposes. Before, a leaked `destinations.archive` block exposed one bucket; afterwards a leaked `connections.aws_prod` exposes every bucket, queue, topic, and function that account can reach. That is the trade the feature makes deliberately. It does not change limitation **L3** in [Invariants](../development/invariants.md) — one process still holds one set of destination credentials — but it does make the full set enumerable in one block, which is what you want when deciding whether to split a deployment by trust domain.

## Destinations

`destinations` is a top-level map of backends keyed by stable id. Each entry may either configure its backend inline (every example under `examples/destinations/` does) or set `connection: <name>` to take its credentials from a [connection](#connections) and keep only its sink fields. A flow `publish` node names one of these ids and a `target` (queue, topic, routing key, stream, collection, object prefix, Temporal task queue, or Lambda function name/ARN). Per-publish options include `create`, `message_attributes`, `payload`, and SQS/SNS-specific wire formats, grouping, FIFO identities, retries and resource attributes. See [Ingress adapters](../guides/ingress-adapters.md#sqs-and-sns-publication) for those contracts. Unknown fields such as `sqs_options`, `sns_options`, `queue_options` or `kafka_options` still fail validation. Supported types: `rabbitmq`, `sqs`, `sns`, `s3`, `lambda`, `redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv`, `postgres`, `mongodb`, `kafka`, `webhook`, and `temporal`. See `examples/destinations/` for a complete reference config per type.

### RabbitMQ

RabbitMQ fields live under `destinations.<id>.rabbitmq`:

- `destinations.<id>.type`: required backend type; use `rabbitmq`.
- `destinations.<id>.rabbitmq.url`: required AMQP URL, usually `${RABBITMQ_URL}`.
- `destinations.<id>.rabbitmq.exchange`: required direct exchange name.
- `destinations.<id>.rabbitmq.exchange_type`: required, use `direct`.
- `destinations.<id>.rabbitmq.durable`: boolean for exchange/queue durability.
- `destinations.<id>.rabbitmq.publish_timeout`: optional duration, default `5s`.
- `destinations.<id>.rabbitmq.declare_queues`: boolean. If true, Hookway declares and binds allowed queues on startup.
- `destinations.<id>.rabbitmq.publisher_channels`: optional non-negative integer. Bounds how many AMQP channels publish concurrently; each channel carries one publisher-confirm round-trip at a time, so this is the effective publish concurrency. Defaults to `8` when omitted or `0`.
- `destinations.<id>.rabbitmq.queue_defaults`: optional queue arguments applied to every queue this destination declares (`publish` `create: true`). There is no per-publish `queue_options` override.
  - `message_ttl`: optional positive duration mapped to RabbitMQ `x-message-ttl` in milliseconds. Stale messages are expired from the queue.
  - `expires`: optional positive duration mapped to `x-expires` in milliseconds. Useful for temporary/test queues; usually omit for production webhook queues.
  - `max_length`: optional positive integer mapped to `x-max-length` to cap backlog size.
  - `overflow`: optional overflow mode. Supported values are `reject-publish` and `reject-publish-dlx`; `reject-publish` is recommended so providers retry instead of silently losing older webhook messages.
  - `dead_letter_routing_key`: optional default RabbitMQ `x-dead-letter-routing-key` override.
- `destinations.<id>.rabbitmq.tls`: optional TLS settings for `amqps://` URLs. All fields are optional — with no `tls` block, `amqps://` still works against brokers whose certificates chain to the system trust store (e.g. CloudAMQP). Configure these only for private CAs, mutual TLS, or a non-default server name.
  - `ca_cert`: path to a PEM bundle to trust instead of/in addition to system roots (for private/self-signed CAs).
  - `client_cert` / `client_key`: PEM paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: disables certificate verification. Dev-only; logs a warning at startup. Do not use in production.

  Bad or unreadable CA/cert paths fail fast at startup. Note: the published container image must contain a CA bundle for `amqps://` to work at all — the provided Dockerfile copies `ca-certificates` into the `scratch` image for this reason.
- `destinations.<id>.rabbitmq.dead_letter`: optional dead-letter exchange/queue declaration settings.
- `destinations.<id>.rabbitmq.retry`: optional publish retry/backoff, covering both a nacked confirm and a transient publish/connect failure. This does NOT retry an unroutable message (see below) — that is a config error, not a transient failure.
  - `max_attempts`: default `3`.
  - `initial_backoff`: default `100ms`.
  - `max_backoff`: default `2s`.

Every publish is sent `mandatory`: if the exchange has no queue bound for the routing key, the broker returns the message instead of silently dropping it. Hookway surfaces this as a `Publish` error (so the inbound webhook request gets a non-2xx response rather than an incorrect `202`) and increments `hookway_rabbitmq_unroutable_total{destination_queue_id, queue}`. This is treated as a terminal configuration problem — a missing queue binding — so it is not retried.

```yaml
      retry:
        max_attempts: 3
        initial_backoff: "100ms"
        max_backoff: "2s"
```

```yaml
destinations:
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

- `enabled`: when true, queue declaration attaches declared queues to the dead-letter exchange.
- `exchange`: required safe exchange name when enabled.
- `exchange_type`: required when enabled, usually `direct`.
- `queue`: required only when `create_queue` is true.
- `routing_key`: required when enabled.
- `durable`: durability for dead-letter exchange/queue declaration.
- `create_queue`: when true, queue layer can declare the dead-letter queue and bind it to the dead-letter exchange.

### AWS SQS

SQS fields live under `destinations.<id>.sqs`. Credentials are optional: when both `access_key_id` and `secret_access_key` are set they are used as static credentials; otherwise the default AWS credential chain (env, IAM role, shared profile) applies.

- `destinations.<id>.type`: set to `sqs`.
- `destinations.<id>.sqs.region`: required AWS region, e.g. `us-east-1`.
- `destinations.<id>.sqs.endpoint`: optional endpoint override; set to `http://localhost:4566` to target LocalStack. Usually `${AWS_ENDPOINT}` (left empty in production).
- `destinations.<id>.sqs.access_key_id` / `secret_access_key`: optional static credentials, usually `${AWS_ACCESS_KEY_ID}` / `${AWS_SECRET_ACCESS_KEY}`. Omit both to use the default credential chain.

Standard and FIFO queues are supported. A `.fifo` target requires `publish.fifo` identities; standard queues may use `publish.group_id` for fair grouping and `publish.sqs.delay_seconds`. Use `publish.resource_attributes` for supported queue attributes. See [broker options](../guides/ingress-adapters.md#sqs-and-sns-publication) and [the FIFO example](../../examples/destinations/fifo-sqs.yaml).

```yaml
destinations:
  jobs:
    type: "sqs"
    on_existing_conflict: "reconcile"
    sqs:
      region: "us-east-1"
      # endpoint: "${AWS_ENDPOINT}"          # http://localhost:4566 for LocalStack
      # access_key_id: "${AWS_ACCESS_KEY_ID}"
      # secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
```

`endpoint`, `access_key_id`, and `secret_access_key` are optional: to use the default AWS credential chain, omit them entirely rather than referencing an env var and leaving it unset — `${ENV_NAME}` references require the variable to be set (empty is fine; unset is a config-load error, see [Secrets](security.md#secrets)).

### AWS SNS

SNS fields live under `destinations.<id>.sns` and mirror SQS (region, endpoint override, optional static credentials):

- `destinations.<id>.type`: set to `sns`.
- `destinations.<id>.sns.region`: required AWS region.
- `destinations.<id>.sns.endpoint`: optional endpoint override (LocalStack).
- `destinations.<id>.sns.access_key_id` / `secret_access_key`: optional static credentials; omit both for the default chain.

Standard and FIFO topics are supported. A `.fifo` target requires `publish.fifo`; `publish.sns` supplies an existing topic ARN, subject or protocol JSON structure as applicable. See [broker options](../guides/ingress-adapters.md#sqs-and-sns-publication).

### AWS Lambda

Lambda fields live under `destinations.<id>.lambda`. Hookway invokes each publish target with the JSON Message envelope as the event payload. Credentials mirror SQS/SNS.

- `destinations.<id>.type`: set to `lambda`.
- `destinations.<id>.lambda.region`: required AWS region, e.g. `us-east-1`.
- `destinations.<id>.lambda.endpoint`: optional endpoint override; set to `http://localhost:4566` for LocalStack.
- `destinations.<id>.lambda.access_key_id` / `secret_access_key`: optional static credentials; omit both for the default chain.
- `destinations.<id>.lambda.invocation_type`: `Event` (default) or `RequestResponse`.
  - **Event**: `Publish` succeeds when AWS accepts the invoke. Function failures are retried by AWS asynchronously; Hookway does not see them. Payload limit: 256 KiB.
  - **RequestResponse**: `Publish` waits for the function to finish. A `FunctionError` fails the publish (like webhook `delivery_mode: sync`). Adds function runtime latency to the inbound request — raise `server.publish_timeout` (and the coupled `write_timeout` / `shutdown_timeout`) when functions may run longer than the 10s default. Payload limit: 6 MiB.

The publish `target` is the AWS `FunctionName`: a function name, `name:alias` / `name:version` / the LATEST qualifier, or a function ARN. One destination (region + credentials) can invoke many functions. `create: true` is **rejected** — Hookway does not create functions. `payload: object` is not supported (envelope JSON only). `message_attributes` are ignored. `on_existing_conflict` is not valid for lambda.

IAM: `lambda:InvokeFunction` on each target; `lambda:GetFunction` for `/ready` probes of static (non-template) targets. Dynamic `{variable}` targets skip the startup probe.

Function return values are not wired into later graph nodes — a publish success edge forwards the **input** envelope, not the Lambda response. See `examples/destinations/lambda.yaml`. For multipart uploads, chain S3 then Lambda with a publish success edge (`examples/multipart-s3-lambda.yaml`): store bytes with `publish.payload: object` on S3, then Event-invoke Lambda with metadata after PutObject returns.

```yaml
destinations:
  workers:
    type: "lambda"
    lambda:
      region: "us-east-1"
      # endpoint: "${AWS_ENDPOINT}"
      # access_key_id: "${AWS_ACCESS_KEY_ID}"
      # secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
      invocation_type: "Event"   # or RequestResponse
```

### AWS S3

S3 fields live under `destinations.<id>.s3`. By default Hookway writes each webhook event as a single JSON Message (`Content-Type: application/json`). Set `publish.payload: object` to store decoded file bytes instead (see [Publish payload modes](#publish-payload-modes)). Credentials are optional: when both `access_key_id` and `secret_access_key` are set they are used as static credentials; otherwise the default AWS credential chain (env, IAM role, shared profile) applies.

- `destinations.<id>.type`: set to `s3`.
- `destinations.<id>.s3.region`: required AWS region, e.g. `us-east-1`.
- `destinations.<id>.s3.endpoint`: optional endpoint override; set to `http://localhost:4566` to target LocalStack, or reference `${AWS_ENDPOINT}` if you export it. Omit the field for production (default AWS endpoint).
- `destinations.<id>.s3.access_key_id` / `secret_access_key`: optional static credentials, e.g. `${AWS_ACCESS_KEY_ID}` / `${AWS_SECRET_ACCESS_KEY}`. Omit both fields entirely to use the default credential chain.
- `destinations.<id>.s3.bucket`: required S3 bucket name.
- `destinations.<id>.s3.create`: when `true`, auto-create the bucket at startup if it does not exist. `us-east-1` uses no `LocationConstraint`; all other regions set it automatically. The bucket is verified via `HeadBucket` at startup regardless of this setting.
- `destinations.<id>.s3.sse`: server-side encryption mode. Leave empty (default) to rely on the bucket's or account's default encryption. Use `"AES256"` for SSE-S3 managed keys or `"aws:kms"` for SSE-KMS.
- `destinations.<id>.s3.sse_kms_key_id`: KMS key ARN or alias. Only used when `sse` is `"aws:kms"`. Omit to use the default `aws/s3` KMS key.

**Object key layout (envelope, default):** the publish `target` is the base folder (key prefix). Each event is stored at:

```
<target>/yyyy/mm/dd/<timestamp>-<id>.json
```

**Object key layout (`payload: object`):** same date partition, but the leaf name uses the sanitized vendor filename (when present) and no `.json` suffix:

```
<target>/yyyy/mm/dd/<timestamp>-<id>[-<filename>]
```

S3 user metadata includes `hookway-event-id` and, when available, `hookway-filename`. `Content-Type` is taken from the file object's `content_type` (or `application/octet-stream`).

The date partition and timestamp are derived from the event's `ReceivedAt` in UTC. The timestamp is colon-free so the key is usable in all contexts. Example with `target: "orders"`:

```
orders/2026/06/29/20260629T143000.000000000Z-evt_1.json
```

**`on_existing_conflict`:** accepted for config parity but is a NO-OP for S3. S3 has no mutable per-target attributes to reconcile; the bucket is verified at startup via `HeadBucket` but is never mutated by this policy.

```yaml
destinations:
  events:
    type: "s3"
    on_existing_conflict: "reconcile"     # accepted but no-op for S3
    s3:
      region: "us-east-1"
      # endpoint: "${AWS_ENDPOINT}"       # http://localhost:4566 for LocalStack
      # access_key_id: "${AWS_ACCESS_KEY_ID}"
      # secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
      bucket: "my-webhook-events"
      create: true
      sse: "AES256"                       # or "aws:kms"; omit for default encryption
      # sse_kms_key_id: "${S3_KMS_KEY_ID}"
```

### Postgres

Postgres fields live under `destinations.<id>.postgres`. Hookway inserts each webhook event as one row into a single shared table using [jackc/pgx/v5](https://github.com/jackc/pgx) (pgxpool). The table is created unconditionally at startup with `CREATE TABLE IF NOT EXISTS`; no per-publish `create` flag is used.

- `destinations.<id>.type`: set to `postgres`.
- `destinations.<id>.postgres.url`: required libpq DSN. `sslmode` is honored inline (e.g. `postgres://user:pass@host:5432/db?sslmode=require`). Usually `${POSTGRES_URL}`.
- `destinations.<id>.postgres.schema`: optional Postgres schema name, default `"public"`.
- `destinations.<id>.postgres.table`: required table name. Only letters, digits, and underscores; must start with a letter or underscore. The table is quoted via `pgx.Identifier.Sanitize()` before use.
- `destinations.<id>.postgres.max_conns`: optional pool size cap. `0` (default) uses the pgx pool default.
- `destinations.<id>.postgres.tls`: optional TLS block. Only needed for a custom/private CA or mutual TLS (client certificate); `sslmode` in the `url` controls whether TLS is used. Fields mirror the RabbitMQ/Redis TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

**`on_existing_conflict` is not valid for Postgres destinations** — the field is rejected at config validation time. Do not set it on `postgres` entries.

**Table schema:** at startup Hookway runs `CREATE TABLE IF NOT EXISTS` with fixed columns for the Message envelope:

| Column | Type | Notes |
|--------|------|-------|
| `id` | `text` | Event id; PRIMARY KEY |
| `target` | `text` | Per-route `target` value (not a table name) |
| `idempotency_key` | `text` | Nullable; ingest leaves this empty. Sender retries require a stable sender event ID for deduplication |
| `source_ip` | `text` | Client IP (nullable) |
| `method` | `text` | HTTP method |
| `path` | `text` | Request path |
| `received_at` | `timestamptz` | UTC receive time |
| `headers` | `jsonb` | HTTP headers (nullable) |
| `query` | `jsonb` | Query parameters (nullable) |
| `body` | `jsonb` | Request body (nullable) |
| `auth` | `jsonb` | Auth metadata (nullable) |

Two indexes are also created `IF NOT EXISTS`: one on `target` and one on `received_at`.

**`target` as a column:** the publish `target` is stored in the `target` column rather than selecting a different table. Use it to distinguish event sources when multiple flows share the same destination.

**Deduplication:** inserts use `INSERT ... ON CONFLICT (id) DO NOTHING`. Repeated delivery of the same Hookway envelope is ignored. Each new inbound request receives a new Hookway ID, so sender retries can insert another row. Deduplicate sender retries using a stable sender event ID in your application.

**Pre-existing table caveat:** `CREATE TABLE IF NOT EXISTS` is a no-op against an existing table. If the table already exists with an incompatible schema, inserts will fail at runtime. Hookway does not migrate columns — ensure the table matches the expected schema or drop and recreate it before starting.

```yaml
destinations:
  warehouse:
    type: "postgres"
    postgres:
      url: "${POSTGRES_URL}"           # libpq DSN; sslmode honored inline
      schema: "public"                 # optional, default "public"
      table: "webhook_events"          # required
      max_conns: 0                     # optional; 0 = pgx default
      # tls:
      #   ca_cert: "/etc/ssl/certs/pg-ca.pem"
      #   client_cert: "/etc/ssl/certs/pg-client.pem"
      #   client_key: "/etc/ssl/private/pg-client.key"
      #   server_name: "pg.internal"
      #   insecure_skip_verify: false
```

### MongoDB

MongoDB fields live under `destinations.<id>.mongodb`. Hookway inserts each webhook event as one BSON document into a collection within the configured database using [go.mongodb.org/mongo-driver/v2](https://www.mongodb.com/docs/drivers/go/current/).

- `destinations.<id>.type`: set to `mongodb`.
- `destinations.<id>.mongodb.uri`: required MongoDB connection URI (e.g. `mongodb://user:pass@host:27017/?replicaSet=rs0`). Usually `${MONGODB_URI}`.
- `destinations.<id>.mongodb.database`: required database name to write into. The collection is determined by the publish `target`.
- `destinations.<id>.mongodb.max_pool_size`: optional connection pool cap. `0` (default) uses the driver default.
- `destinations.<id>.mongodb.tls`: optional TLS block. Only needed for a custom/private CA or mutual TLS (client certificate). Fields mirror the RabbitMQ/Redis TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

**`on_existing_conflict` is not valid for MongoDB destinations** — the field is rejected at config validation time. Do not set it on `mongodb` entries.

**Collection-per-target:** the publish `target` names the MongoDB collection within `database`. Use different targets to send events from different flows into separate collections.

**Document shape:** each event is stored as one BSON document. The stored document carries the same envelope fields (`_id`, `endpoint`, `received_at`, `headers`, `query`, `body`, `auth`) that other destinations emit, stored as native BSON — top-level scalars map to BSON string/datetime types and nested objects (`headers`, `query`, `body`, `auth`) become nested BSON documents.

**No DDL:** Hookway does not create collections or indexes at startup. Collections auto-create on first insert; `_id` is automatically indexed by MongoDB, so deduplication works with no setup required.

**Deduplication:** inserts use `InsertOne`. A duplicate `_id` (MongoDB E11000 duplicate key error) is treated as a no-op success. This deduplicates internal retries of the same Hookway envelope, not fresh sender requests, which get new IDs. Use a stable sender event ID for application-level deduplication.

```yaml
destinations:
  archive:
    type: "mongodb"
    mongodb:
      uri: "${MONGODB_URI}"         # mongodb:// connection string
      database: "webhooks"          # required; collection comes from route target
      max_pool_size: 0              # optional; 0 = driver default
      # tls:
      #   ca_cert: "/etc/ssl/certs/mongo-ca.pem"
      #   client_cert: "/etc/ssl/certs/mongo-client.pem"
      #   client_key: "/etc/ssl/private/mongo-client.key"
      #   server_name: "mongo.internal"
      #   insecure_skip_verify: false
```

### Kafka

Kafka fields live under `destinations.<id>.kafka`. Hookway publishes each webhook event as one message to a topic using [segmentio/kafka-go](https://github.com/segmentio/kafka-go).

- `destinations.<id>.type`: set to `kafka`.
- `destinations.<id>.kafka.brokers`: required list of one or more bootstrap broker addresses (e.g. `["broker1:9092", "broker2:9092"]`).
- `destinations.<id>.kafka.acks`: producer acknowledgement level — `all` (default, durable — waits for all in-sync replicas), `one`, or `none`.
- `destinations.<id>.kafka.compression`: `none` (default), `gzip`, `snappy`, `lz4`, or `zstd`.
- `destinations.<id>.kafka.publish_timeout`: optional duration, default `5s`.
- `destinations.<id>.kafka.sasl`: optional SASL auth. Omit the whole block for no SASL.
  - `mechanism`: `plain`, `scram-sha-256`, or `scram-sha-512`.
  - `username` / `password`: SASL credentials, usually `${KAFKA_USERNAME}` / `${KAFKA_PASSWORD}`.
- `destinations.<id>.kafka.tls`: optional TLS settings. Omit or set `enabled: false` for plaintext. Fields mirror the RabbitMQ/Redis TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.
- `destinations.<id>.kafka.retry`: optional producer retry/backoff, mapped onto the kafka-go writer.
  - `max_attempts`: default `3`.
  - `initial_backoff`: default `100ms`.
  - `max_backoff`: default `2s`.

**Security note:** omitting both `tls.enabled` and `sasl` connects to the brokers in plaintext with no authentication. Hookway logs a startup warning in this case; use it only for local development.

**`target` is the topic.** The publish `target` is the Kafka topic name. Setting `create: true` on that `publish` node makes Hookway pre-create the topic via an admin `CreateTopics` call at startup (idempotent — an already-existing topic is left as-is). This replaces relying on broker-side auto-creation, which raced the first publish. Omit `create: true` when topics are pre-provisioned; `create: false` (or omitted) targets must already exist on the broker.

**Partition key / topic creation options:** `kafka_options` (`partition_key`, `partitions`, `replication_factor`) are not fields on the `publish` node. Messages are produced without a key (round-robin partitions) and topic create uses the broker defaults. Put a dedicated destination per topic when you need different destination-level Kafka settings.

See `examples/destinations/kafka.yaml` for a complete reference config.

```yaml
destinations:
  events:
    type: "kafka"
    kafka:
      brokers:
        - "${KAFKA_BROKER_1}"
        - "${KAFKA_BROKER_2}"
      acks: "all"
      compression: "snappy"
      publish_timeout: "5s"
      sasl:
        mechanism: "scram-sha-256"
        username: "${KAFKA_USERNAME}"
        password: "${KAFKA_PASSWORD}"
      tls:
        enabled: true
      retry:
        max_attempts: 3
        initial_backoff: "100ms"
        max_backoff: "2s"
```

### Temporal

Temporal fields live under `destinations.<id>.temporal`. Hookway starts one workflow execution per webhook event using the official [Temporal Go SDK](https://github.com/temporalio/sdk-go). It only starts workflows (no signals, no worker of its own); your existing workers on the task queue pick the execution up.

- `destinations.<id>.type`: set to `temporal`.
- `destinations.<id>.temporal.host_port`: required frontend gRPC address, e.g. `localhost:7233` (self-hosted) or `<namespace>.<account>.tmprl.cloud:7233` (Temporal Cloud).
- `destinations.<id>.temporal.namespace`: required Temporal namespace.
- `destinations.<id>.temporal.api_key`: optional Temporal Cloud API key, sent as a bearer credential. Requires `tls.enabled: true`.
- `destinations.<id>.temporal.connect_timeout`: optional duration, default `10s`. Bounds the startup Dial; the destination fails fast if the frontend is unreachable (same behaviour as the Postgres and MongoDB destinations).
- `destinations.<id>.temporal.start_timeout`: optional duration, default `5s`. Bounds each start-workflow RPC. Keep it below `server.publish_timeout`.
- `destinations.<id>.temporal.tls`: optional TLS settings. Omit or set `enabled: false` for plaintext. Fields mirror the Kafka TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

**Security note:** omitting both `tls.enabled` and `api_key` connects to the frontend in plaintext with no authentication. Hookway logs a startup warning in this case. The published image is built `FROM scratch`, so certificate paths must be mounted into the container.

**`target` is the task queue.** The publish `target` is the Temporal task queue name. `create: true` is rejected; workers create task queues by polling. Task queue names must not end in `.fifo`.

**Workflow settings:** set `publish.temporal.workflow_type` to your registered worker workflow type. The standalone server and builder support the same fields:

- `input`: `envelope` (default) passes the whole Hookway event; `body` passes decoded JSON. For a non-JSON body, `body` falls back to the envelope containing base64 request bytes.
- `workflow_id`: `source` is `static` (requires `value`), `endpoint`, `header` or `body` (requires `path`). Optional `prefix` can contain routing variables. Omission or an unresolved value uses the Hookway event ID. A fresh sender request normally receives a new event ID; use a stable provider delivery ID to deduplicate sender retries, and allowlist that header on the envelope node.
- `id_reuse_policy`: `reject_duplicate` prevents restarting a retained completed workflow with the same ID; `allow_duplicate` permits another execution after completion; `allow_duplicate_failed_only` permits another execution after failure. Omission uses the Temporal server default. **`terminate_if_running` terminates a running workflow before starting its replacement**; use only when that replacement is intended.
- `execution_timeout`, `run_timeout`, `task_timeout`: optional positive Go durations, such as `24h`, `1h`, `10s`. Omission uses Temporal defaults. Run timeout cannot exceed execution timeout. Task timeout cannot exceed `2m` ([Temporal timeout documentation](https://docs.temporal.io/encyclopedia/detecting-workflow-failures)). These govern workflow execution, separately from the destination's start-RPC timeout.

Validation rejects missing workflow types, unsupported inputs/policies, invalid timeouts, use on another destination type, and conflicting settings on a shared destination/task queue.

**Duplicates count as delivered.** When the server reports that an execution with the derived workflow id already exists, Hookway logs it at debug level and returns success, so sender retries with a deterministic workflow id are idempotent (first-start-wins).

**Failure handling.** There is no dead-letter path for this destination. Any other start error (frontend unreachable, `start_timeout` exceeded, unknown namespace, rejected start) fails the publish: under the default `stop_on_failure` policy the sender receives a 503 and is expected to retry; under `skip_failed` the route is marked failed and the other routes still deliver. The SDK retries transient gRPC errors internally within `start_timeout`. A stopped worker is not a failure: the start succeeds and the execution waits on the task queue.

**Readiness.** The destination's readiness probe calls the frontend gRPC health service; a proxy that blocks it will report the destination unready even though publishes succeed.

**Payload encoding.** Inputs are encoded with the SDK's default JSON data converter. Clusters that require a custom payload codec (for example, encryption at rest) are not supported yet.

A temporal destination cannot be used as a webhook `dead_letter` target because a dead-letter route has no `workflow_type`.

See `examples/destinations/temporal.yaml` for a complete reference config.

```yaml
destinations:
  orchestrator:
    type: "temporal"
    temporal:
      host_port: "${TEMPORAL_ADDRESS}"
      namespace: "${TEMPORAL_NAMESPACE}"
      connect_timeout: "10s"
      start_timeout: "5s"
      tls:
        enabled: false

flows:
  order_events:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "${WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope, body_format: json}
      - id: out
        type: publish
        destination: orchestrator
        target: order-events
        temporal:
          workflow_type: OrderWorkflow
          input: body
          workflow_id: {source: body, path: id, prefix: order-}
          id_reuse_policy: reject_duplicate
    edges: [in>body, body>auth, auth>env, env>out]
```

### Redis / Valkey

Hookway supports four Redis destination types. All share a common `redis:` connection block on the destination. `redis_options` (`max_len`, `consumer_group`, `ttl`, `key_template`) are not fields on the `publish` node.

**Available types:**

| Type | Primitive | Notes |
|------|-----------|-------|
| `redis_stream` | XADD to a Stream — durable; consumers may XREADGROUP | Recommended for at-least-once delivery. YAML has no `max_len` / `consumer_group`, so Hookway does not trim or create groups |
| `redis_list` | LPUSH to a List — FIFO work queue | Simple competing-consumer queue. YAML has no `max_len`, so lists are unbounded unless consumers trim |
| `redis_pubsub` | PUBLISH to a channel — live broadcast; lossy, not durable | Fire-and-forget; subscribers offline at delivery time miss the message |
| `redis_kv` | SET a key per webhook — storage, not a queue | Latest-event overwrite. Default key `{prefix}:{endpoint}:{id}`. YAML has no `ttl`, so keys do not expire. `publish.payload: object` stores decoded file bytes instead of the JSON Message |

`redis_stream` is the recommended type for at-least-once webhook handoff. `redis_pubsub` is intentionally lossy — if no subscriber is connected at publish time, the message is silently dropped; use it only for live broadcast scenarios where occasional loss is acceptable.

[Valkey](https://valkey.io/) is fully supported: it speaks the RESP protocol and behaves identically to Redis.

**Shared `redis:` connection block** — configure once on the destination, reused by every `publish` that targets it:

- `mode`: deployment topology. `single` (default) for one node or a proxied endpoint; `cluster` for Redis Cluster; `sentinel` for Redis Sentinel.
- `url`: redis:// or rediss:// URL. Required for `single` mode. Usually `${REDIS_URL}`.
- `addrs`: list of node addresses. Required for `cluster` and `sentinel` modes (e.g. `["redis-1:6379", "redis-2:6379"]`).
- `master_name`: sentinel primary name. Required for `sentinel` mode (e.g. `"mymaster"`).
- `username` / `password`: optional auth credentials.
- `db`: optional database index. Applies to `single` and `sentinel`; Redis Cluster ignores it.
- `key_prefix`: optional string automatically prepended to stream/list/channel targets for `redis_stream`, `redis_list`, and `redis_pubsub` (formatted as `<prefix>:<target>`). For `redis_kv`, the prefix is substituted into the default key template `{prefix}:{endpoint}:{id}` (`endpoint` is the flow name) but is not automatically prepended.
- `tls`: optional TLS settings — mirror of the RabbitMQ TLS block.
  - `enabled`: boolean.
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

```yaml
destinations:
  events:
    type: redis_stream
    redis:
      mode: single
      url: "${REDIS_URL}"   # redis:// or rediss://
      # Cluster example:
      # mode: cluster
      # addrs: ["redis-1:6379", "redis-2:6379", "redis-3:6379"]
      # Sentinel example:
      # mode: sentinel
      # addrs: ["sentinel-1:26379", "sentinel-2:26379"]
      # master_name: "mymaster"
      key_prefix: "hookway"   # optional
```

See `examples/destinations/redis_stream.yaml` for a full annotated reference with TLS and cluster/sentinel comments.

**Best practices for `redis_stream`:**

- `redis_options.max_len` / `consumer_group` are not on the `publish` node, so streams are unbounded unless you trim from the consumer side, and Hookway does not create a consumer group at startup.
- Delivery is at-least-once: a message appended via XADD is acknowledged to the webhook provider before the consumer confirms processing. Consumers must tolerate duplicates: the envelope `id` identifies internal delivery retries and replay, while sender retries require a stable sender event ID.
- Redis connectivity for a `redis_stream` destination is established lazily — verified at the `/ready` (`/readyz`) probe and on the first publish.

### Webhook

Webhook fields live under `destinations.<id>.webhook`. Hookway delivers each inbound webhook event to a configured HTTP/HTTPS endpoint as an outbound HTTP POST.

Webhook deliveries do not follow HTTP redirects, including same-origin redirects.
Configure the receiver's canonical URL so credentials, signatures and original
payload bytes stay at the selected endpoint. A `3xx` is a terminal delivery
failure, not acceptance by the receiver. This applies to sync, async and proxy
payload modes; intentional literal localhost/private HTTP endpoints remain valid.

If `url` contains a `{variable}` in its authority, its receiver is partly chosen
by request data. Such deliveries reject loopback, private, link-local, metadata,
and other blocked addresses by default. DNS is checked at every new connection
and the dial is pinned to a vetted IP; changing a DNS answer cannot redirect a
new connection to a forbidden address. Guarded transports do not use environment
forward proxies and do not share their connection pool with unguarded ones.
For an intentional internal templated receiver, set
`destinations.<id>.webhook.allow_private_targets: true`. This exception applies
only to the named destination and never relaxes DNS-label substitution, safe
path values, outbound auth, or redirect refusal. Literal operator-selected hosts,
including environment-expanded literal hosts and URLs with only path variables,
retain their existing local/private networking support.

**Delivery mode (`delivery_mode`):** `async` (default) or `sync`.

- **`async` (default):** `Publish()` enqueues the delivery job and returns immediately with `nil` (success). With the default response policy, the inbound sender receives Hookway's own `202 Accepted`: the event was accepted into memory for delivery, not confirmed by the receiver. Actual delivery outcome is observable via logs, the `hookway_webhook_*` Prometheus metrics, and (optionally) a `dead_letter` destination. This is the default so upgrades preserve existing behavior.
- **`sync`:** `Publish()` performs delivery inline (including retries/backoff) and returns the outcome. With default failure handling, a terminal `4xx` or exhausted retries returns an error and the inbound sender sees a non-2xx response. The shared `server.publish_timeout` bounds publication, including retries. Normal publication is detached from caller cancellation: a disconnect does not cancel a delivery already in progress. Use sync when the sender should observe downstream delivery failure directly, and keep the total latency within its acknowledgement deadline. `dead_letter` does not apply to sync mode. Explicit failure response policies or `on_error` paths can still acknowledge a failed delivery; see [Delivery guarantees](../operations/delivery-guarantees.md).

**Per-destination delivery engine and worker pool (async):** each webhook destination has its **own** delivery engine (goroutine pool + queue), so a slow or failing target cannot head-of-line-block deliveries to other destinations. The number of concurrent delivery workers per engine is controlled by `server.fanout_concurrency`. Two independent limits apply: `max_queue` bounds queued jobs (**per-destination**, default 1000), and `max_queue_bytes` bounds conservative accounted bytes for **queued and active** jobs (default 64 MiB). Either limit can reject publication before async acceptance; with default response policies, the sender receives a non-2xx response and can retry. Readiness (`/readyz`) reports a webhook destination as not-ready at ≥90% of either limit. Sync/inline delivery does not use the async queue limits.

The byte budget includes retained raw/object/encoded payloads, headers and parsed values with JSON/base64 headroom; it is not exact wire size or a hard heap cap. Jobs remain charged through retries, dead-letter publication and terminal callbacks. Aliases are conservatively charged again; cycles and excessive structure fail boundedly. A job too large for the remaining budget uses the existing queue-full error, without truncation or partial acceptance. Raise a finite limit deliberately for legitimate large files, and reserve additional container memory for parser, allocator, transport and capture overhead. See [resource limits and sizing](../operations/resource-limits.md).

**Graceful drain on shutdown (async):** on SIGTERM / shutdown, Hookway attempts to drain in-flight and queued deliveries within `drain_timeout` (defaults to `server.shutdown_timeout`). A completed drain avoids abandoning queued work; an expired deadline, forced termination, or hard crash (kill -9, OOM, host failure) can lose acknowledged queued or in-flight deliveries. Neither `dead_letter` nor replay makes this memory queue durable: both need the live process to observe and forward a failure. Use broker-confirmed publication when accepted work must survive a core crash. See [Delivery guarantees and recovery](../operations/delivery-guarantees.md).

**NON-DURABLE by default:** the delivery queue is in-memory only. There is no write-ahead log or persistent queue. A `dead_letter` destination can retain observed terminal failures after its own publish succeeds. To survive a core crash after acknowledgement, publish directly to a durable broker configured with the required persistence and replication. Broker acceptance does not guarantee downstream processing or eliminate duplicate delivery.

**Dead-letter (`dead_letter`, async only):** when an async delivery fails terminally — a terminal `4xx`, or `5xx`/timeout/error after retries are exhausted — Hookway can redirect the message to another destination instead of dropping it (log-and-drop is the default when no `dead_letter` is set). The redirected message is the original envelope enriched with a `dead_letter` block (`destination`, `target`, `outcome`, `attempts`, `failed_at`). The referenced destination MUST exist and MUST NOT itself be a `webhook` destination — this makes delivery loops structurally impossible (no cycle-detection needed). The dead-letter target queue/topic is auto-collected into the backend destination (and declared for RabbitMQ when `declare_queues: true`) even if no `publish` node targets it directly.

**Not a transparent proxy (async webhook destination):** Hookway does not return the downstream HTTP response to the inbound sender. With default response policies, async mode returns Hookway's own `202 Accepted` (queue acceptance) or an error. Sync mode also returns Hookway's status, not the downstream body. Use `http_request` and a passthrough `respond` node to relay the upstream response.

For a synchronous reverse proxy of the inbound request (upstream status, headers, and body), use `http_request` plus `respond` `passthrough: true` — see [Flows](#flows) and `examples/showcase/proxy.yaml` — not a webhook destination.

#### Fields

- `destinations.<id>.type`: set to `webhook`.
- `destinations.<id>.webhook.url`: required. The target endpoint URL. Must be `http://` or `https://`.
- `destinations.<id>.webhook.allow_private_targets`: optional boolean, default `false`. Permits private/loopback receivers for a request-templated authority; requires a `{variable}` in the URL authority. Omit for literal hosts, which already support intentional local/private destinations.
- `destinations.<id>.webhook.delivery_mode`: optional. `async` (default) or `sync`. See [Delivery mode](#webhook) above.
- `destinations.<id>.webhook.timeout`: optional per-request HTTP timeout. Default `10s`.
- `destinations.<id>.webhook.max_queue`: optional bound on the in-memory delivery queue (async only, per-destination). Default `1000`. When the queue fills, inbound `Publish` calls fail (backpressure).
- `destinations.<id>.webhook.max_queue_bytes`: optional positive integer, conservative queued-plus-active byte budget (async only, per-destination). Omitted or `0` uses `67108864` (64 MiB); negative values are invalid. There is no unlimited value. A single oversized job or exhausted budget fails publication before queue acceptance. Raise this alongside the container memory allowance when supporting large files.
- `destinations.<id>.webhook.drain_timeout`: optional graceful-drain deadline on shutdown (async only). Defaults to `server.shutdown_timeout`.
- `destinations.<id>.webhook.dead_letter`: optional (async only). Redirect terminally-failed deliveries to another destination.
  - `destination`: id of a configured **non-webhook** destination. Required with `target`.
  - `target`: the queue/topic/target name on that destination. Required with `destination`.
- `destinations.<id>.webhook.retry`: optional retry/backoff settings.
  - `max_attempts`: total attempts (including the first). Default `3`.
  - `initial_backoff`: first retry wait. Default `500ms`.
  - `max_backoff`: maximum retry wait. Default `30s`.
- `destinations.<id>.webhook.auth`: outbound authentication. See [Auth types](#outbound-auth-types) below.
- `destinations.<id>.webhook.tls`: optional TLS block for a custom CA or mutual TLS. Fields mirror the RabbitMQ TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification. Never use in production.

**`on_existing_conflict` is not valid for webhook destinations** — the field is rejected at config validation time. Do not set it on `webhook` entries.

#### Retry classification

| HTTP status / error | Behavior |
|---------------------|----------|
| `2xx` | Success — delivery complete |
| `3xx` | Terminal — redirect is not followed; use the canonical receiver URL |
| `429` | Retry with backoff |
| `5xx` | Retry with backoff |
| Network error / timeout | Retry with backoff |
| Other `4xx` (400, 401, 403, 404, …) | Terminal — no retry; dead-lettered if configured, else dropped |

Retries use exponential backoff (doubling, +/-20% jitter, capped at `retry.max_backoff`) — jitter keeps many destinations that all start failing at once (e.g. a shared downstream outage) from retrying in lockstep. A `429` response with a valid `Retry-After` header (integer seconds or an HTTP-date, per RFC 9110) overrides the computed backoff for that wait, still capped at `retry.max_backoff`; an absent or unparseable header falls back to the computed backoff. After `retry.max_attempts` the delivery is abandoned; the final outcome is observable via `hookway_webhook_delivery_total{outcome=...}` and an Error log entry.

#### Payload modes

**Envelope mode (default):** the outbound POST body is the Hookway JSON `Message` envelope (`Content-Type: application/json`) containing the normalized event fields (`id`, `endpoint`, `received_at`, `headers`, `query`, `body`, `auth`). Used for all auth types except `proxy`.

**Proxy mode (`auth.type: proxy`):** Hookway forwards the _original raw inbound body_ verbatim, preserving the original `Content-Type`, and copies the original inbound headers (minus hop-by-hop headers: `Connection`, `Transfer-Encoding`, `Upgrade`, etc.) to the outbound request. No hook_way auth headers are added — the vendor's original auth headers (e.g. `X-Stripe-Signature`) pass through as-is. Use proxy mode when the downstream endpoint expects to verify the vendor's original signature.

#### Outbound auth types

**`none`**: no authentication headers are added. Suitable for internal/trusted networks only.

**`api_key`**: sets a static header on every outbound request. Configure `auth.header` (the header name) and `auth.value` (the value, usually `${WEBHOOK_API_KEY}`).

**`hmac`** (default): signs each outbound request with HMAC-SHA256. Configure `auth.secret` (the signing secret). Three signing schemes are supported via `auth.scheme`:

| Scheme | Signature header format | Signing string |
|--------|------------------------|----------------|
| `hmac_sha256_ts_dot` (default) | `t=<unix_ts>,v1=<hex>` | `<unix_ts>.<raw_body>` |
| `hmac_sha256_ts_colon` | `ts=<unix_ts>;h1=<hex>` | `<unix_ts>:<raw_body>` |
| `hmac_sha256_hex` | `sha256=<hex>` | `<raw_body>` (no timestamp) |

The signature header name defaults to `X-Hookway-Signature` (override with `auth.header`). For `hmac_sha256_ts_dot` and `hmac_sha256_ts_colon`, Hookway also sends an event-id header (default `X-Hookway-Event-Id`, override with `auth.event_id_header`) to enable replay detection.

**`proxy`**: forwards the original inbound request's raw body and headers (minus hop-by-hop) to the downstream endpoint unchanged. No hook_way auth is added. See [Payload modes](#payload-modes) above.

#### Consumer-side auth verification

Your downstream endpoint (the receiver) should verify incoming requests from Hookway. The verification recipe differs by auth type.

**api_key**: read the configured header from the incoming request and compare it to the expected value using a constant-time comparison function to prevent timing attacks.

```
expected = config.auth.value
received = request.headers[config.auth.header]
assert constant_time_equal(received, expected)
```

**hmac (hmac_sha256_ts_dot / hmac_sha256_ts_colon)**: parse the signature header, recompute the HMAC, compare, and reject stale or replayed requests.

```
# Step 1: parse the signature header
# hmac_sha256_ts_dot:   "t=<ts>,v1=<hex>"
# hmac_sha256_ts_colon: "ts=<ts>;h1=<hex>"
ts, received_hex = parse_signature_header(request.headers["X-Hookway-Signature"])

# Step 2: reject stale timestamps (replay window, e.g. 5 minutes)
now = current_unix_timestamp()
assert abs(now - ts) <= 300   # seconds; tune to your tolerance

# Step 3: recompute HMAC over the RAW received bytes
# IMPORTANT: use the raw request body bytes, not a re-serialised struct
# signing_string is a BYTE sequence: ASCII-encode the timestamp integer,
# then the separator byte, then the raw body bytes — concatenated as bytes.
if scheme == "hmac_sha256_ts_dot":
    signing_string = ascii_bytes(ts) + b"." + raw_body_bytes
else:  # hmac_sha256_ts_colon
    signing_string = ascii_bytes(ts) + b":" + raw_body_bytes
expected_hex = hex(HMAC_SHA256(secret, signing_string))

# Step 4: constant-time compare — never use == on HMAC hex strings
assert constant_time_equal(received_hex, expected_hex)

# Step 5: deduplicate replayed requests (optional but recommended)
event_id = request.headers["X-Hookway-Event-Id"]
assert not seen_event_ids.contains(event_id)
seen_event_ids.add(event_id, ttl=300)
```

Key points:
- Always compare over the **raw** received bytes, not a parsed/re-serialised body — any whitespace change invalidates the signature.
- Use a constant-time comparison (`hmac.Equal` in Go, `hmac.compare_digest` in Python, `crypto.timingSafeEqual` in Node.js) to prevent timing attacks.
- Reject any request whose timestamp is outside your replay window (e.g. ±5 minutes).
- Optionally track `X-Hookway-Event-Id` in a short-lived store (TTL = replay window) to detect duplicate deliveries from retries.

**hmac_sha256_hex**: no timestamp is included in this scheme. Verify as above but the signing string is the raw body alone and there is no `ts` to check. For Hookway outbound deliveries, deduplicate `X-Hookway-Event-Id` in the downstream consumer. For inbound GitHub deliveries, use the provider's `X-GitHub-Delivery` identity; it is distinct from Hookway's generated event ID. Persist deduplication across replicas and restarts. See [provider compatibility](../guides/provider-compatibility.md).

```
received_hex = parse "sha256=<hex>" from request.headers["X-Hookway-Signature"]
expected_hex = hex(HMAC_SHA256(secret, raw_body_bytes))
assert constant_time_equal(received_hex, expected_hex)
# No timestamp — rely on X-Hookway-Event-Id dedup for replay protection
```

**proxy**: Hookway does not add any hook_way auth headers in proxy mode. The original vendor auth headers are forwarded verbatim. Verify using the vendor's own webhook verification procedure (e.g. validate `X-Stripe-Signature` using Stripe's SDK).

#### Metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `hookway_webhook_delivery_total` | `destination_queue_id`, `queue`, `outcome` | Delivery attempts by outcome. `outcome` values: `2xx`, `4xx`, `5xx`, `429`, `timeout`, `error`. |
| `hookway_webhook_retries_total` | `destination_queue_id`, `queue` | Incremented per retry attempt. |
| `hookway_webhook_dropped_total` | `destination_queue_id`, `queue`, `reason` | Async delivery dropped before it was ever attempted. `reason` is `queue_full` (buffer full at enqueue — backpressure) or `engine_closing` (enqueue during shutdown). NOT incremented on retry exhaustion or terminal 4xx. |
| `hookway_webhook_dead_letter_total` | `destination_queue_id`, `queue`, `outcome` | Terminally-failed deliveries successfully redirected to the `dead_letter` destination. `outcome` is the last delivery outcome (`4xx`, `5xx`, `429`, `timeout`, `error`). |
| `hookway_webhook_dead_letter_failures_total` | `destination_queue_id`, `queue` | Dead-letter redirects that themselves failed (or fired with no resolver). The event is then logged and dropped. |
| `hookway_webhook_queue_depth` | `destination_queue_id` | Current async delivery queue depth (gauge). |
| `hookway_webhook_queue_capacity` | `destination_queue_id` | Configured async delivery queue capacity (gauge; set once at startup). |
| `hookway_webhook_queue_accounted_bytes` | `destination_queue_id` | Conservative bytes retained by queued and active async jobs, including retries and terminal callbacks (gauge; not live heap). |
| `hookway_webhook_queue_accounted_capacity_bytes` | `destination_queue_id` | Configured async accounted-byte capacity (gauge). |

> **Metric change:** `hookway_webhook_dropped_total` gained a `reason` label in this release. Dashboards/alerts that aggregated the metric without a `reason` selector continue to work; those that pinned an exact label set must add `reason`.

Retry-exhausted or terminal-4xx deliveries are observable via `hookway_webhook_delivery_total{outcome="5xx"|"4xx"|"timeout"|"error"}` and an Error log entry — they do NOT increment `hookway_webhook_dropped_total`. When a `dead_letter` destination is configured, such failures also increment `hookway_webhook_dead_letter_total` (on successful redirect) or `hookway_webhook_dead_letter_failures_total` (on redirect failure).

The generic `Publish` metric (`hookway_publish_total{status}`) reflects queue-accept (`accepted`) or queue-full drop (`dropped`) at the point where the inbound event is enqueued, before any delivery attempt.

```yaml
destinations:
  forward:
    type: "webhook"
    webhook:
      url: "${WEBHOOK_URL}"
      delivery_mode: "async"    # sync | async (default async)
      timeout: "10s"
      max_queue: 1000           # async only; per-destination
      max_queue_bytes: 67108864 # async only; queued + active accounted bytes
      drain_timeout: "30s"      # async only
      dead_letter:              # async only; must reference a non-webhook destination
        destination: "dlq-rabbit"
        target: "webhook-dead-letters"
      retry:
        max_attempts: 5
        initial_backoff: "500ms"
        max_backoff: "30s"
      auth:
        type: "hmac"
        secret: "${WEBHOOK_SECRET}"
        # scheme: "hmac_sha256_ts_dot"    # default
        # header: "X-Hookway-Signature"   # default
        # event_id_header: "X-Hookway-Event-Id"  # default
      # tls:
      #   ca_cert: "/etc/ssl/certs/ca-bundle.pem"
      #   client_cert: "/etc/ssl/certs/client.pem"
      #   client_key: "/etc/ssl/private/client.key"
      #   server_name: "internal.example.com"
      #   insecure_skip_verify: false

  # The dead_letter destination referenced above — a durable, non-webhook backend.
  dlq-rabbit:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.dlq"
      exchange_type: "direct"
      declare_queues: true   # declares the "webhook-dead-letters" queue on startup
```

See `examples/destinations/webhook.yaml` for the full annotated reference config with all four auth types shown.

### Resource provisioning and drift (`on_existing_conflict`)

For `sqs` and `sns` destinations, `destinations.<id>.on_existing_conflict` controls what happens when a target queue/topic already exists with attributes that differ from the config. Allowed values are `error`, `warn`, and `reconcile`; the default is `reconcile`.

- `reconcile` (default): edit the existing resource's mutable attributes (SQS `VisibilityTimeout`, `MessageRetentionPeriod`, `ContentBasedDeduplication`; SNS `ContentBasedDeduplication`) to match config, logging each change. Requires `sqs:SetQueueAttributes` / `sns:SetTopicAttributes` IAM permissions.
- `warn`: log each differing attribute and use the existing resource unchanged.
- `error`: fail startup on any difference.

Immutable conflicts — the FIFO-vs-standard nature of a queue/topic — always fail startup regardless of the policy, since they cannot be edited and would otherwise cause failed publishes. The field is only valid on `sqs`/`sns` destinations; setting it on a `rabbitmq` destination is a config error.

When `create: true` and the target does not exist, it is created with the configured attributes (no reconcile needed).

## Flows

Typed routing, registration, managed outbound credentials, bounded batches, ordered XML and flow response policies are documented in [Ingress adapters](../guides/ingress-adapters.md).

A flow is the request pipeline. Trigger paths must not overlap another flow or the reserved health/readiness/metrics paths. Flow names are internal identifiers; the public HTTP path comes from the `http_trigger` node, not from the flow name.

```yaml
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: webhook
    webhook: {url: "https://example.test/hooks"}
flows:
  github_events:
    nodes:
      - {id: in, type: http_trigger, description: "GitHub posts deliveries here.", path: /webhooks/github, methods: [POST]}
      - {id: rl, type: rate_limit, store: local, description: "Caps traffic before any body is read.", rpm: 60}
      - {id: body, type: read_body, description: "Reads the raw body up to 1 MiB.", max_bytes: 1048576}
      - {id: auth, type: auth, description: "Verifies the HMAC over the raw body.", scheme: hmac_sha256_hex, secret: "${GITHUB_WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope, description: "Builds the JSON event envelope.", body_format: json}
      - {id: out, type: publish, description: "Delivers to the billing queue.", destination: billing, target: github-events, create: true}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
```

Nodes are wired by `edges` (`from>to`, or `from:port>to` for a labelled port). Illegal port wiring fails `hookway validate`. Typical order: `http_trigger` → pre-body gates (`loop_guard`, `ip_allowlist`, `rate_limit`) → `read_body` → `auth` or `no_auth` → `envelope` → `filter` / `sample` / `transform` / `lookup` / `code` / `http_request` → `publish` and/or `respond`.

`on_error` is a reserved output port on nodes that can fail with a recoverable input. It is not available on `http_trigger` (no input), `read_body` (the body reader is consumed by a failed read), or `no_auth` (no semantic failure). If the node returns an error and an `on_error` edge exists, the flow continues with that node's **input** (not its output) and the request succeeds unless the error path itself fails. The error-path target must consume the failing node's input port — `rate_limit:on_error` carries `RequestHead` by default or `Verified` for the verified stage, `auth:on_error` still carries `RawRequest`, `envelope:on_error` still carries `Verified`, `http_request:on_error` still carries `Envelope`. Protection infrastructure errors (503) never enter `on_error`. Quota and duplicate denial recovery paths may return a custom response, but cannot execute outbound effects or child flows. A node that has only `on_error` successors is rejected; it still needs a success path. Without `on_error`, the failure becomes the HTTP status from the node (`401`, `413`, `429`, `502`, …) or `500`. Filter `on_no_match: drop` is a successful stop, not an error, so it does not take `on_error`.

```yaml
- {id: ack, type: http_request, url: "https://example.test/hooks", error_on_status: [5xx]}
- {id: out, type: publish, destination: billing, target: github-events}
- {id: dlq, type: publish, destination: billing, target: github-failed}
# …
edges: [env>ack, ack>out, ack:on_error>dlq]
```

See `examples/on_error.yaml` for `rate_limit`, `auth`, `envelope`, and `http_request`. `examples/showcase/production.yaml` uses `ack:on_error>dlq`.

#### `http_request` retry

`http_request` makes one attempt by default. Add a `retry:` block to re-attempt
transient failures:

```yaml
- id: notify
  type: http_request
  url: "${SLACK_WEBHOOK_URL}"
  body: transformed
  timeout: 5s
  retry:
    max_attempts: 4        # total attempts including the first; 1 or absent = no retry, max 10
    initial_backoff: 500ms # doubled with +/-20% jitter each attempt (default 500ms)
    max_backoff: 10s       # ceiling for any single wait (default 30s)
```

- **What retries:** transport errors, timeouts, `429`, and `5xx`. Every other
  status — including all other `4xx` — is terminal, because a payload the
  upstream rejected will not become acceptable on a second try.
- **`Retry-After`:** honored on a `429` (integer seconds or HTTP-date), clamped
  by `max_backoff` so a mistaken or hostile header cannot park the flow.
- **`timeout` is per attempt,** not a total deadline. `timeout: 5s` with
  `max_attempts: 4` can take ~20s plus backoff. The loop still stops early when
  the caller's context is cancelled.
- **Sync flows:** a flow ending in `respond` holds the original caller for the
  whole sequence. Keep `max_attempts` small (2-3) there, or move the call to an
  async `publish` destination.
- **`retry` and `error_on_status` are independent.** `retry` decides whether to
  re-attempt; `error_on_status` is applied once, to the final attempt's status.
  Combining them — retry the transient failures, then take `on_error` when the
  attempts run out — is the intended production shape; see
  `examples/on_error.yaml`.
- **Retry is not rate limiting.** It absorbs a burst against a rate-limited API
  (Slack incoming webhooks allow roughly one message per second; a Discord
  webhook allows about five requests per five seconds). It does not help when a
  flow's steady-state rate exceeds what the upstream permits — for that, publish
  to a queue destination and pace the consumer.

#### `http_request` outbound egress policy

An `http_request` whose destination host can be influenced by request data is
an SSRF primitive unless it is bounded. Hookway bounds it in three places, and
the whole policy turns on one distinction:

> **A literal host is the operator's choice; a templated host is not.**
> `url: "http://10.0.0.5:8080/ingest"` is a deliberate configuration decision —
> proxying to an internal service is a legitimate thing to do, and Hookway
> does not second-guess it. `url: "https://{tenant}-api.example.com/ingest"`
> has a host that depends on `env.Vars`, which is seeded from the trigger's
> path variables and extended by `lookup` and `transform` nodes. **The egress
> policy applies only to the second case.**
>
> "Templated" means the parsed *authority* has a `{placeholder}`. A `${VAR}`
> environment reference does not count: it is expanded to literal text at
> config load, so `url: "${UPSTREAM_BASE}/{rest}"` is a literal host with a
> templated path, and is not subject to the policy.

**1. Config time: a templated host needs a literal domain suffix.** A
`{variable}` in the URL authority must be followed by a literal dotted domain
suffix, so a request can choose at most one label under a fixed domain and
never the whole host. `hookway validate` rejects anything else with:

```
a {variable} in the url host must be followed by a literal domain suffix:
write the host as "{envName}-api.example.com" so requests cannot choose an
arbitrary host
```

So `https://{envName}-api.example.com/v1` is accepted and
`https://{host}/v1` is not.

**2. Render time: the rendered host is vetted before any connection.** Each
authority variable must be a single DNS label: 1–63 ASCII letters, digits, or
hyphens, with no leading or trailing hyphen. Dots, slashes, URL delimiters, and
encoded delimiters are rejected with `400`, including when
`allow_private_targets: true`. Path variables may still contain slashes. With
the policy on, the target is classified after variable substitution and
refused if it addresses the gateway's own host or a private network. Denied by
address: loopback (`127.0.0.0/8`, `::1`), unspecified (`0.0.0.0/8`, `::`),
link-local (`169.254.0.0/16` — which is where `169.254.169.254` instance
metadata lives — and `fe80::/10`), RFC1918 private (`10/8`, `172.16/12`,
`192.168/16`), CGNAT `100.64.0.0/10`, IPv6 ULA `fc00::/7`, multicast
(`224.0.0.0/4`, `ff00::/8`), reserved/benchmarking ranges, IETF protocol
assignments `192.0.0.0/24`, and the Alibaba metadata address
`100.100.100.200`. IPv4-mapped IPv6 spellings collapse to the IPv4 rules, so
`::ffff:169.254.169.254` is caught too. Denied by name: `localhost`, and the
`.localhost`, `.local`, `.internal` (which covers
`metadata.google.internal`), `.localdomain`, and `.home.arpa` suffixes.

**Ambiguous host spellings are rejected rather than interpreted.** Deciding
whether `2130706433`, `127.1`, `0177.0.0.1`, or `0x7f000001` is "really"
`127.0.0.1` is a classic parser-differential SSRF bug — Hookway's answer and
the resolver's answer only have to disagree once. So a host with an all-digit
final label, a `0x`-prefixed label, an empty label, or any character not valid
in a DNS name is refused outright. A real TLD is never all digits, so nothing
legitimate is lost.

**3. Connect time: DNS rebinding is closed.** A name that passes the check
above can still resolve to `169.254.169.254`, and a second lookup between the
check and the connect can return a different answer than the first. When the
guard is on, the dialer resolves the host itself, classifies **every**
candidate address, and dials a specific vetted IP — so the address that was
checked is the address that is connected to. Guarded and unguarded nodes use
separate connection pools, so a guarded request cannot reuse a connection
opened without this check. Redirects are not followed by default (`http_request`
returns the response as-is). An explicit `redirects.max_hops` policy follows only
bounded redirects on the original scheme and host/port, rechecks the target and
normalizes its path. Cross-origin changes and userinfo are refused; managed
credential requests never follow redirects. Capture uploads and replay control
calls also refuse redirects and require the canonical receiver URL.

**`allow_private_targets`**

```yaml
- id: ack
  type: http_request
  url: "https://{envName}-api.internal.example.com/ingest"
  allow_private_targets: true
```

| | |
|---|---|
| Type | boolean |
| Default | `false` — fail closed |
| Effect | Opts this node out of steps 2 and 3 above. Step 1 (the literal-domain-suffix requirement) still applies. |
| Scope | per node, not global |

Use it when a templated host is *supposed* to reach an internal upstream — a
per-environment internal API, say. It is the only opt-out, it is explicit, and
it is scoped to the one node that needs it.

`hookway validate` **rejects** `allow_private_targets: true` on a node whose
URL host is literal:

```
allow_private_targets has no effect unless the url host contains a
{variable}; a literal host is already the operator's own choice
```

That is deliberate. A literal host was never subject to the policy, so setting
the flag there does nothing — and a no-op security flag in a config file is
worse than no flag, because it reads like a decision was made. Failing
validation forces the config to say what is actually true.

An `initial_backoff` or `max_backoff` without `max_attempts: 2` or more is
rejected by `hookway validate` rather than silently ignored.

Node fields (every type):

- `id`: required. Unique within the flow; this is the name `edges` refer to.
- `type`: required. One of the catalog types below.
- `description`: optional. A note explaining what the node does, shown on the
  node in the config UI. Nothing at runtime reads it: it is not published in
  the envelope and never appears in logs. Type-specific options are listed per
  node type below.

Flow fields:

- `capture_failures`: optional, default `true`. Set `false` to never capture this flow's failures for the replay service (see [Replay](#replay)). It has no effect without the `replay:` block. Inspect collection is independent of this flag.
- `delivery_failure_policy`: optional. Defaults to `stop_on_failure`. Fan-out is the set of `publish` successors of a node. A `filter` that drops a branch is not a delivery failure. Partial or uncertain delivery retains the `replay_guard` reservation. Recover missed destinations through replay when a retained record exists; consumers must still deduplicate using a stable sender event ID.
- `nodes` / `edges`: required.

### Protection stores

Declare `state_stores` at the top level with a stable deployment namespace and exactly one of `memory`, `redis` or `postgres`. Nodes must explicitly reference a store. Startup fails when any configured store cannot be verified. Apply PostgreSQL migrations with `hookway state-store migrate` before serving. Runtime outages withdraw readiness and reject protected traffic. Configuration, expiry, policy-update and deployment examples are in [Protection stores](../guides/protection-stores.md).

### Trigger, gates, and body

- `http_trigger`: `path` (must start with `/`, unique among flows; `{name}` captures a path segment into envelope vars) and `methods` (default `[POST]`; `ANY` skips 405).
- `loop_guard`: rejects inbound `X-Hookway-Hops` at `max_hops` with 400.
- `ip_allowlist`: `cidrs` list of IPs or CIDR ranges; mismatch is 403. Source IP honors `security.trusted_proxy_headers` / `trusted_proxies`.
- `rate_limit`: requires a named `store`. Choose `strategy: token_bucket` (`rpm`, optional `burst`) or `sliding_window_counter` (`limit`, `window`). Default `stage: request_head` supports source IP or trusted headers; `stage: verified` supports a configured credential ID, JWT subject or JWT tenant after authentication. `max_keys` defaults to 10,000. Quota exhaustion returns 429; new-identity capacity exhaustion and store failures return 503. See [Protection stores](../guides/protection-stores.md) for identities, shared policies, expiry and operator commands.
- `read_body`: required positive `max_bytes` (the per-node cap; values above `server.max_body_bytes` produce a lint warning); oversize is 413.
- `replay_guard`: requires a named `store`; `ttl` defaults to 5m, `max_entries` to 1,024, and an omitted `key` hashes the raw body. Memory is per process; Redis/PostgreSQL coordinate a policy across replicas. The reservation remains after partial or uncertain delivery. It is released only when no delivery effect occurred. See [Protection stores](../guides/protection-stores.md).

### Auth

The additional separate-header HMAC, multi-signature HMAC and P-256 ECDSA protocols are documented in [Request signatures](../guides/ingress-adapters.md#request-signatures).

Each flow builds its verifier once at startup from the `auth` (or `no_auth`) node. An unknown scheme fails closed.

- `scheme`: wire format. Built-in schemes:
  - `hmac_sha256_ts_colon` (`hmac_sha256`): timestamped `ts=<unix>;h1=<hex>` over `ts + ":" + raw_body`. *Example: Paddle `Paddle-Signature`.*
  - `hmac_sha256_ts_dot` (`hmac_sha256`): `t=<unix>,v1=<hex>` over `t + "." + raw_body`. *Example: Stripe `Stripe-Signature`.*
  - `hmac_sha256_hex` (`hmac_sha256`): prefixed hex HMAC-SHA256 over the raw body. *Example: GitHub `X-Hub-Signature-256`.*
  - `hmac_sha256_base64` (`hmac_sha256`): raw base64 HMAC-SHA256. *Example: Shopify `X-Shopify-Hmac-SHA256`.*
  - `hmac_sha1_hex` (`hmac_sha256`): prefixed hex HMAC-SHA1. *Example: GitHub legacy `X-Hub-Signature`.*
  - `exact` (`header_exact`): constant-time compare of `signature_header` to `secret`. Use `remove_bearer: true` to strip a leading `Bearer` prefix. *Example: RevenueCat `Authorization`.*
  - `basic` (`header_exact`): HTTP Basic against the configured `user:pass` secret.
  - Use the separate `no_auth` node for intentional unverified traffic; `auth.scheme: no_auth` is rejected.
  - `jwt_hs256` / `jwt_rs256` / `jwt_es256` (`jwt`): Bearer JWT from `signature_header`. `body_hash_claim` is required and binds the token to the raw body. There is no opt-out.
- `secret` or `secret_ref`: required except for `no_auth`. See [Secret references](#secret-references).
- `signature_header`: header Hookway reads. Alternative: `source: {kind: header|query, key: ...}`. `kind: query` reads a query parameter (e.g. `?authID=`). Query keys used for auth are redacted. If both `source` and `signature_header` are set, `source` wins.
- `timestamp_tolerance`: defaults to `5m`; timestamped HMAC uses it as the freshness window, JWT as clock leeway.

HMAC schemes verify the **raw** body bytes. New providers are added by registering a scheme in `internal/auth`.

### Envelope and publish

- `envelope`: builds the JSON envelope (`body_format: json|xml|raw|multipart`). `json` decodes to an object; `xml` decodes to an element tree that `filter`, `code`, `transform` `xml_prune`, and `http_request` `body: transformed` all understand; `multipart` decodes a `multipart/form-data` upload to `{fields, files}` (see [Multipart bodies](#multipart-bodies)); `raw` leaves the body empty and publishes the bytes as `raw_body_base64`. Headers are redacted/allowlisted using `security.redacted_header_denylist` and `security.forwarded_header_allowlist`.

For `body_format: json`, `envelope.json.max_depth` defaults to `128` (maximum `1024`), and `envelope.json.max_nodes` defaults to `1000000` (maximum `1000000`). Positive overrides are optional. A syntax/structure preflight checks valid JSON before allocating the parsed tree, including values supplied under duplicate object keys. Exceeding either structural budget returns `413`; malformed JSON retains the existing raw-base64 fallback. These limits do not replace `read_body.max_bytes`. A downstream `json_validate` has its own fixed depth/node limits, so raising envelope depth does not raise validation limits.

- `publish`: substitutions in dynamic targets use safe names (`[a-zA-Z0-9._-]`, 1–128 characters) for destination matching. Lambda targets additionally allow function ARNs and `name:qualifier` forms (1–256 characters). `destination` (id under top-level `destinations`), `target` (queue/topic/routing key/function name, or `{var}` template), optional `create`, optional `message_attributes` (SNS/SQS MessageAttributes; at most 10), optional `payload` (`envelope` default, or `object` for s3 / redis_kv — see [Publish payload modes](#publish-payload-modes)). A successful publish may take **at most one** unlabeled success edge; that edge runs only after `Publish` returns nil, carrying the **input** envelope (not the destination response) on the **same** `server.publish_timeout` budget. Zero success edges remains a valid leaf. `respond` is rejected anywhere reachable along that continuation (including through `code`). Fan-out of sibling publishes still belongs on the node before the first publish. A failed publish does not run its success edge; `on_error` is unchanged.
- `respond`: sync HTTP response (`status`, `headers`, `body`, or `passthrough` of an upstream `http_request`).
- `http_request`: outbound request (`url` may use `{rest}` / vars; `forward_method`, `forward_query`, `forward_headers`, `error_on_status`, `retry`, `allow_private_targets`). `Authorization` and `Cookie` are never forwarded unless explicitly listed in `forward_headers`. A captured path variable containing a `..` segment, a `#`, a `?`, a backslash, or an ASCII control character is rejected with `400`. A `{variable}` in the `url` **host** must be followed by a literal dotted domain suffix, and a templated host is additionally checked against the private-network egress policy unless `allow_private_targets: true` — see [`http_request` outbound egress policy](#http_request-outbound-egress-policy). See also [Security](security.md) and `examples/showcase/proxy.yaml` / `examples/on_error.yaml`.
- `json_validate`: checks a JSON envelope against a required JSON Schema. Validation is strict unless individual scalar conversions are configured. Invalid bodies return 400 or follow `on_error`. See [Validate JSON](../guides/json-validation.md) for drafts, references, precision, sample testing, and limits.
- `transform`: `changed_fields`, `field_map`, `json_schema`, `strip_fields`, `changed_fields_js` (JSON bodies), or `xml_prune` / `xml_ordered_prune` (XML bodies). The legacy `json_schema` kind uses the same validation engine as `json_validate`. `hookway validate` rejects a kind that does not match the `body_format` on the path reaching it.
- `lookup`: resolves an incoming key through a named HTTP, PostgreSQL, or DynamoDB provider configured under `lookups`. Stores the result in a variable for routing; see [named lookup providers](../guides/lookups.md) for configuration, cache limits, credentials, and replay behavior.
- `code`: sandboxed JavaScript; see [Code node](#code-node).
- `switch`: first matching case (or default) selects exactly one labelled successor. Each declared port needs one edge. Selection is consumed before running the selected path. A case states its rule either as a single `when:` condition or as a `conditions:` list joined by `match: all` (default) or `match: any` — one spelling per case, not both.
- `if_else`: a two-port `switch`. Conditions are the same `conditions:` list with the same `match: all|any`, and the event leaves by the `true` port when the rule holds and the `false` port when it does not. Both ports need an edge; to ignore one side, wire it at a terminal node rather than leaving it dangling. There is no failure to catch, so `if_else` takes no `on_error` edge.

#### Shared destinations

A `publish` node can receive multiple incoming edges. Each active path sends its
own payload and variables independently. Processing and response nodes still
allow only one incoming edge; duplicate edges and cycles are invalid.

For two active branches, both transformed inputs reach the same destination:

```yaml
edges:
  - env>left
  - env>right
  - left>send
  - right>send
```

Here `left` and `right` are processing nodes and `send` is a publish node.
See [the complete shared-destination example](../../examples/shared-destination.yaml).
To share a destination across exclusive conditional outputs, connect both labels:

```yaml
edges:
  - choose:true>send
  - choose:false>send
```

Here `choose` is an `if_else` node. Only its selected output runs, so one input
reaches `send`. The same pattern works with declared `switch` outputs and with
compatible success/error paths. Dropped or unselected paths do not publish.

Each publish invocation runs its own applicable success or error continuation
with that input. Existing delivery failure policies and timeout/concurrency rules
still apply: an earlier failure can prevent a later arrival from being attempted.
Delivery outcomes and eligible failure records are separate even when they share
a node ID. Replaying a failure starts from that record's saved input and follows
existing continuation behavior; it does not rerun sibling arrivals.

Batch child flows may likewise connect several paths to their one publish node.
Response counts remain per child event, and any failed or unattempted delivery
makes that child fail. Existing message identities and broker deduplication
settings still apply; multiple publish calls do not guarantee distinct broker
messages if the destination deduplicates their configured identities.

#### Builder templates

The config UI's palette has a **Templates** group above the node types. A
template drops several pre-wired nodes in at once:

- **Stripe webhook** / **Paddle webhook** — ingest prefix: trigger,
  `loop_guard`, `rate_limit`, `read_body`, the provider's timestamped HMAC
  via `secret_ref` (env, 15m refresh, 5m overlap), `replay_guard`, and
  `envelope`. Drop onto a new flow, then wire envelope to `publish` or a
  filter. Do not replace the auth node with `no_auth`.
- **XML webhook** — the whole XML pipeline: the ingest prefix above with
  `hmac_sha256_hex` over the raw bytes and a `body_format: xml` envelope, then
  a `filter` on an element path, a `code` node reading the element tree, an
  `xml_prune` `transform`, and an `http_request` with `body: transformed` that
  forwards `application/xml`. This one inserts a whole pipeline rather than a
  half, because `xml_prune` only validates behind an `xml` envelope — see
  [XML bodies](#xml-bodies).
- **Slack notification** / **Discord notification** — format an event and POST
  it to an incoming-webhook URL. The inserted pair does not forward inbound
  `Authorization` / `Cookie`; keep `url` as `${SLACK_WEBHOOK_URL}` /
  `${DISCORD_WEBHOOK_URL}` (the URL *is* the credential). The standalone
  wrapper around that pair still authenticates the inbound caller.

Each template is backed by a complete config in `examples/templates/`, so
`make validate-examples` validates it like any other example and a renamed node
option cannot break the palette silently. The builder inserts only the node ids
listed in the manifest (`web/src/graph/templates.ts`).

Inserted nodes are renamed on collision (`fmt`, then `fmt_2`), so dropping the
same template in twice is safe.

After inserting a notification template, edit two things: the `code` node's
script (what the message says) and the `http_request` node's `url` (which
should stay an environment reference such as `${SLACK_WEBHOOK_URL}` — the
webhook URL *is* the credential). Never paste a live webhook URL into the
node. Both notification templates treat a `4xx` as terminal, because a payload
the platform rejected will not become acceptable on a retry.

`stop_on_failure` (default) publishes `publish` successors sequentially and returns `503` on the first failure. `skip_failed` attempts every direct publish successor with bounded concurrency and returns `202` when at least one succeeds, with `failed_destinations` listing failures. Fan-out through processing nodes remains sequential and stops on error. Filter drops are successful branch termination.

### Filtering

A `filter` node is a list of AND-combined conditions. Place it after `envelope` and before every `publish` to drop the whole event, or on only some outgoing edges to skip a branch. `on_no_match: drop` (default) is a silent `202` with no destinations; `on_no_match: reject` returns 422. A dropped branch is never a delivery failure.

```yaml
- id: filt
  type: filter
  on_no_match: drop
  conditions:
    - source: header        # header | body
      path: X-GitHub-Event  # header name, or dotted body path such as repository.private
      operator: eq
      value: "push"
```

- `source`: `header` reads the unredacted request header named by `path`. `body` walks a dotted path into the decoded body: JSON object keys (`repository.private`), or — after an `envelope` with `body_format: xml` — element local names below the root, with a leading `@` for an attribute (`ORDER.Amount`, `ORDER.@id`). No array indexing, and repeated XML siblings resolve to the first in document order. A body condition after `body_format: raw` is rejected by `hookway validate`, because a raw envelope leaves the body empty.
- `operator`: `eq`/`ne` (type-aware); `gt`/`gte`/`lt`/`lte` (numeric); `contains`/`prefix`/`suffix`; `regex` (RE2, compiled at load); `exists`/`not_exists`.
- `value`: comparison value. Omit for `exists`/`not_exists`.

Header values are strings, so numeric/bool operators parse them (`"42"` matches `eq: 42`). A missing or incomparable value fails the condition, including `ne`. A body that is neither a JSON object nor an XML document (for example after `body_format: raw`) fails every `body` condition except `not_exists`; `hookway validate` rejects that combination rather than letting it drop every event silently. Silent drops are counted by `hookway_filtered_total` with `level="flow"`. Per-node metrics record execution; condition values are not logged.

Config files pass through `os.ExpandEnv` before parsing, so `$name` inside a `regex` value is expanded. Escape or avoid literal `$`-prefixed tokens.

See `examples/filters.yaml`.

### Sampling

Place a `sample` node after `envelope` to keep a random fraction of events:

```yaml
- id: sample_events
  type: sample
  rate: 0.2
```

`rate` is required and must be a finite number from 0 to 1 inclusive. It is the
fraction **kept**: `0` drops all events, `1` keeps all, and `0.2` keeps roughly
20%. Smaller fractions work too: `0.001` keeps roughly 0.1% and `0.00001` keeps
roughly 0.001%. Each node execution makes an independent random decision, including retries
and replays; this is an approximate proportion, not an exact quota or a stable
selection by event ID. The builder starts new sample nodes at `rate: 1`; YAML
without a rate is invalid.

Selected events continue unchanged. Sampled-out events stop that branch
successfully, receive the default HTTP 202 acknowledgment (or the flow's
`responses.dropped` policy), produce no failure capture, and retain any preceding
`replay_guard` reservation. Other fan-out branches continue normally. Existing
deliveries and responses follow the usual flow response rules.

Sampling supports every envelope body format. Before a `batch`, it selects whole
requests; inside a batch child flow, it selects individual items. Sampled-out
children use the existing successful filtered-child skip counts, so they do not
cause a dedicated batch to return 502. Node metrics use `outcome="dropped"`, and
`hookway_filtered_total` records `level="sampled_out"`.

See [the runnable sampling configuration](../../examples/sample.yaml). Release
the matching core backend, builder, and WASM validator together before using this
node; older versions do not recognize `type: sample`.

### Inspecting webhooks

An `inspect` node captures the **current** rendered body and request context for
viewing in the replay console's **Inspections** tab. It takes no options and
requires the global [`replay:` connection](#replay), even with
`capture_failures: false`. Connect it inline (`env>inspect>publish`) to observe
every event that reaches it, or leave it as a successful observation leaf.
It passes its input onward unchanged; downstream changes cannot alter a capture.

For spot-checking without reducing delivery traffic, put Sample and Inspect on
an independent branch **before** the Publish edge:

```yaml
nodes:
  # ... http_trigger → read_body → auth → envelope (id: env)
  - {id: spot_check, type: sample, rate: 0.05}
  - {id: inspect_sample, type: inspect}
  - {id: send_all, type: publish, destination: primary, target: events}
edges: [env>spot_check, spot_check>inspect_sample, env>send_all]
```

The executor visits the observation branch first in edge order. Approximately
5% of requests are inspected while Publish receives every request. Putting
Sample inline before Publish instead would reduce deliveries. An inspection-only
flow uses the `inspected` response outcome (default 202); publication, explicit
responses and processing errors retain their normal precedence. A later failure
or non-2xx sender response does not discard an earlier inspection.

View-only records contain inspection time, flow/node and correlation identifiers,
and the final inbound HTTP status. The versioned JSON payload contains the
rendered body (UTF-8, or base64 for binary), content type, filtered/redacted
headers, variables, method and path. JSON number precision is preserved. Headers
use existing logging policies and authentication credential redactions. Body and
variable contents are operator-authored data; there is no field-level redaction.

Uploads are best effort, reuse the bounded replay uploader, and never fail the
webhook request. Inspections and failures share retention, record-size limits,
upload-buffer memory and storage caps. Oversized/unsupported captures retain
metadata with an unavailable-payload reason. View-only records have no executable
snapshot, retry, or delivery-resolution action; dismissal deletes their payload.
V1 supports inbound flows, excludes batch-child placement, and collects no new
inspections during replay redrives. Deploy the replay migration/API first, then
matching console/builder/WASM assets, before core configs using Inspect.

See [the runnable spot-check example](../../examples/inspect.yaml) and
[console inspection controls](../operations/replay-console.md#view-only-inspections).

### XML bodies

An `envelope` with `body_format: xml` parses the verified bytes into an element
tree. Parsing follows the flow's authentication decision; explicit `no_auth`
flows still accept unsigned input.

What understands that tree:

| Node | Behavior |
| --- | --- |
| `filter` | `source: body` paths address element local names below the root, `@name` for an attribute. A leaf compares as a string, so `gt`/`lt` work on numeric text. |
| `code` | The script sees `{declaration, root, before, after}`; elements include names/namespaces, legacy attribute/text views, ordered `attributes` and `content`, and `children`. Preserve that tagged tree; see [ordered XML editing](../guides/ingress-adapters.md#proxy-contracts-and-document-handling). |
| `transform` | `kind: xml_prune` only. `field_map` and `changed_fields` are JSON-only and are rejected at validate time. |
| `http_request` | `body: transformed` re-serializes the tree and sends `application/xml`. `body: raw` sends the original bytes. |
| `publish` | Serializes the tree and base64-encodes it into the envelope's `raw_body_base64`, with `body: null`. |

Limits worth knowing before you design a flow around it:

- **Namespace prefixes are ignored** for filter paths and in the `code` shape's
  `name`/`attr` keys — a vendor can change a prefix without changing what the
  document means, and a filter should not break when they do. The namespace URI
  is still available to a script as an element's `ns`, and per attribute in
  `attrns`, so a script that returns the body it was given keeps them.
- **Repeated siblings resolve to the first** in document order. A filter cannot
  express "any `ORDER` has an amount over 500"; a `code` node can, by walking
  `children`.
- **`xml_prune` is five fixed rules**, not a rule language:
  `remove_empty_self_closing_elements`, `remove_empty_parents`,
  `remove_hardcoded_only_parties`, `remove_blank_general_partners`,
  `prune_orphaned_relationships`.

`examples/xml.yaml` is a complete flow using all of it.

### Multipart bodies

`body_format: multipart` parses a `multipart/form-data` request into a shape
the rest of the flow can address:

```yaml
- id: env
  type: envelope
  body_format: multipart
  multipart:
    max_parts: 4            # parts allowed before the request is rejected (default 16, max 256)
    max_part_bytes: 2097152 # per-part cap (default 1 MiB)
    max_total_bytes: 4194304 # optional aggregate decoded bytes (maximum 64 MiB)
    include_content: true   # carry the bytes as base64 (default true)
```

The body becomes:

```json
{
  "fields": {"account": "acct_1"},
  "files": {
    "document": {
      "filename": "report.pdf",
      "content_type": "application/pdf",
      "size": 20481,
      "content": "<base64>"
    }
  }
}
```

Files are keyed by form field name rather than collected into a list, so the
ordinary dotted-path filter reaches them: `files.document.content_type`. A
repeated field name gets a `#2`, `#3` suffix in document order
(`files.document#2.filename`). `code` sees the same object as `input.body`, and
`publish` sends it as the envelope's `body`.

For authenticated flows, signature verification uses the raw bytes before
multipart parsing. Explicit `no_auth` flows accept unsigned uploads.

**Memory.** The whole request is buffered by `read_body`; parsing temporarily
buffers each decoded part, and carried file content retains a base64 copy of
`4 * ceil(decoded_bytes / 3)` bytes. Raw bytes, parsed metadata, transient part
buffers and branch copies can overlap, so this expansion formula is not a peak
heap guarantee. Use `read_body.max_bytes`, `max_parts`, `max_part_bytes` and an
optional `max_total_bytes` together. Set `include_content: false` when you only
route on metadata: each file then carries `size` and `content_omitted: true`
instead of a retained base64 copy. The parser still reads a bounded temporary
part buffer to determine its size. Genuinely large uploads want a streaming
intake, which Hookway does not have.

**Limits worth knowing before you design around it:**

| Limit | Detail |
| --- | --- |
| Form-data only | `multipart/mixed` and `multipart/related` are rejected with `400`. |
| Closing boundary required | A body with no closing `--boundary--` is rejected with `400`. `mime/multipart` reports a truncated body as a clean end of input, so without this check a cut-off upload would parse to zero parts instead of failing. |
| No re-serialization | `http_request` `body: transformed` JSON-encodes the parsed shape, which is not a multipart body. To forward an upload unchanged, use `body: raw`, which sends the original bytes. |
| Claimed types | `content_type` is what the vendor's part headers said, verbatim. There is no magic-byte sniffing, so a filter matches the claim, not the content. |
| Repeated fields | Non-file fields keep the last value; files get `#2`/`#3` keys. |
| Transform kinds | `field_map`, `changed_fields`, and `xml_prune` all reject a multipart body at validate time — read the parts in a `code` node instead. |
| Aggregate cap | Optional `max_total_bytes` limits all decoded parts together, including files omitted from the output. Omitted means no additional aggregate cap; wire, part-count and per-part caps still apply. The reader stops at the smaller of the part cap and remaining aggregate cap plus one byte, without draining an over-limit part. |
| Statuses | Over `max_parts`, `max_part_bytes` or `max_total_bytes` is `413`; a malformed body or a non-form-data content type is `400`. |

`examples/multipart.yaml` is a complete flow.

To send the file to one destination and the form fields to another, fan out
after `envelope` and reshape each branch with a `code` node (see
`examples/multipart-split.yaml`). Use `publish.payload: object` on the S3
branch so PutObject writes decoded file bytes (with the vendor Content-Type
and filename on the key) instead of a JSON envelope.

### Publish payload modes

`publish.payload` selects how object-capable destinations store the event:

| Value | Destinations | What is stored |
| --- | --- | --- |
| `envelope` (default) | all | JSON Message (`application/json`) |
| `object` | `s3`, `redis_kv` only | Decoded file bytes; S3 also uses supplied filename / Content-Type metadata |

For `payload: object`, the envelope body must be a file object:

```json
{"content": "<base64>", "filename": "report.pdf", "content_type": "application/pdf"}
```

That is the shape multipart parsing already uses for each entry under `files.*`.
A `code` node can lift `files.document` onto `body` before publish. When the
envelope used `body_format: raw` (empty parsed body), object mode falls back to
the raw request bytes and the original inbound `Content-Type` header. That
header is used only as object metadata; it does not bypass
`security.forwarded_header_allowlist` in the stored envelope.

`hookway validate` rejects `payload: object` on any other destination type.
`examples/multipart-split.yaml` is the reference flow (S3 + RabbitMQ).
`examples/multipart-s3-lambda.yaml` is the same pattern with an Event Lambda
notify instead of a queue.

### Variables

Path placeholders on `http_trigger` (`path: /webhooks/{envName}/events`) are copied onto `envelope.Vars`. A `transform` `field_map` can copy a JSON body field to `vars.tenant`; a `code` node can derive variables from its parsed `body`, existing `vars`, or filtered `headers` input. `{name}` placeholders in `publish.target` and in a webhook destination `url` are filled per request. This can select a pre-provisioned queue per environment, such as `{envName}-events`. Unresolved templates and unsafe values fail closed at publish. A `lookup` node can resolve a key through a named provider under top-level `lookups` and store the result as a variable; see [named lookup providers](../guides/lookups.md).

A placeholder in a webhook `url` **host** must be followed by a literal domain suffix (`{envName}-api.example.com`) so a request cannot choose an arbitrary host. `${ENV_VAR}` references expand at load and are not request variables.

See `examples/variables.yaml`.

## Published envelope reference

Every `publish` node with the default `payload: envelope` writes one JSON
document, the same shape on every destination type. This is the consumer
contract; `schema_version` governs its compatibility. The Go source of truth
is `queue.Message` in `internal/queue/publisher.go`.

| Field | JSON type | Always present | Description |
|---|---|---|---|
| `schema_version` | string | yes | Envelope contract version, currently `"1.0"`. Additive changes keep the major; a removal or a change of meaning requires a new version. Gate your parser on this. |
| `id` | string | yes | UUIDv7 ID assigned per inbound request and retained by internal delivery retries and replay. A sender retry creates a new ID; deduplicate those with a stable sender event ID. |
| `idempotency_key` | string | **no — omitted on ingest** | Present in the struct, but ingest never populates it, so it is omitted from every normally-published envelope. Do not build dedup on it. See [Idempotency](security.md#idempotency). |
| `request_id` | string | when non-empty | One nonempty non-whitespace printable ASCII inbound `X-Request-ID` (or `security.request_id_header`) of at most 128 bytes; missing, invalid or repeated values use a generated UUID. Matches the `X-Request-ID` response header and `request_id` log field. |
| `trace_id` | string | when non-empty | 32-hex W3C trace id, taken from the inbound `traceparent` or `X-Trace-ID` when either is a valid trace id, else generated. |
| `traceparent` | string | when non-empty | The outbound W3C `traceparent` value: the inbound header verbatim when syntactically valid, otherwise one synthesized from `trace_id`. The **webhook** destination sets it as a real header on the outbound request (envelope and proxy modes); every other destination carries it inside the JSON and does nothing else with it. |
| `endpoint` | string | yes | The **request path** the event arrived on (e.g. `/webhooks/github`). Note this differs from the `endpoint` in Hookway's *HTTP response* body and in metric/log labels, which is the flow name. The name is kept for compatibility with pre-graph consumers. |
| `destination_queue_id` | string | yes | The `destinations:` map key this envelope was published through. |
| `queue` | string | yes | The publish `target` — queue, topic, routing key, stream, collection, object prefix, or Temporal task queue. Rendered, if the target was a `{var}` template. |
| `received_at` | string (RFC 3339) | yes | When Hookway received the request. |
| `method` | string | when non-empty | Inbound HTTP method. |
| `path` | string | when non-empty | Inbound request path. |
| `headers` | object | when non-empty | Inbound headers after redaction and allowlisting (`security.redacted_header_denylist`, `security.forwarded_header_allowlist`). Redacted values are the literal string `"redacted"`. |
| `query` | object | when non-empty | Inbound query parameters. Query keys used for auth are redacted. |
| `body` | any | yes (may be `null`) | The decoded body. An object for `body_format: json`; the element tree for `xml`; `{fields, files}` for `multipart`; `null` for `raw` and for XML published as bytes. |
| `raw_body_base64` | string | only when set | Base64 of the original bytes. Set by `body_format: raw`, and by XML publishes (which serialize the tree here and leave `body` null). |
| `source_ip` | string | when non-empty | Client IP, resolved through `security.trusted_proxies` / `trusted_proxy_headers`. |
| `auth` | object | yes | `{"type", "scheme", "verified"}` describing how the request was authenticated — the auth node's identity and scheme, with `verified: true`. A `no_auth` flow still emits the object. |
| `variables` | object (string→string) | when non-empty | The routing variables resolved for this event — path placeholders, `transform` `field_map` outputs, `code`-derived values. Publishers render templated destination settings (e.g. a webhook `url`) from these. |
| `dead_letter` | object | only on dead-letters | Present **only** when an async webhook delivery terminally failed and was redirected to a configured `dead_letter` destination. It records why. Additive, so `schema_version` stays `1.0`. Its absence is the normal case. |

Fields on `queue.Message` tagged `json:"-"` are in-memory only and never
serialized: `RawBody` and `OrigHeaders` (proxy-mode webhook reconstruction),
`Hops` (loop protection — the webhook destination stamps `Hops+1` as a
header), `MessageAttributes` (SNS/SQS attributes ride the AWS API, not the
JSON body), and the `PayloadMode` / `ObjectBytes` / `ObjectContentType` /
`ObjectFilename` group used by `payload: object` on `s3` and `redis_kv`.

With `payload: object` there is no envelope at all — the decoded file bytes
are stored directly. See [Publish payload modes](#publish-payload-modes).

## Response headers

Hookway sets a fixed set of headers on **every** response from the webhook
listener, including error responses, before any flow runs:

| Header | Value | When |
|---|---|---|
| `X-Content-Type-Options` | `nosniff` | always |
| `Cache-Control` | `no-store` | always |
| `X-Request-ID` | the request id | always — a single nonempty non-whitespace printable ASCII inbound value of at most 128 bytes is echoed; missing, invalid or repeated values use a generated UUID. This applies to `security.request_id_header` too. It matches the envelope's `request_id` and log field; give the returned value to a sender investigating a delivery. |
| `Retry-After` | `1` | on the `503` returned when `server.max_inflight_requests` is exhausted (`{"error":"server busy"}`). Not set on other 503s. |
| `Allow` | the flow's configured methods | on a `405`, when the request method is not in the trigger's `methods` list. A trigger with `methods: [ANY]` never 405s. |

A `respond` node controls status and body and may add its own headers, but the
three always-on headers above are already written by the time it runs.

## Code node

`code` runs JavaScript on a goja VM compiled at config load. A syntax error fails `hookway validate`, not the first request. Known transforms (`changed_fields`, `field_map`, `xml_prune`) belong on `transform`, not in JS.

```js
// `input` is provided; return an object {body, vars}.
// There is no I/O, no require, no timers, and no access to other flows.
return { body: input.body, vars: { computed: String(input.body.id).toUpperCase() } };
```

Input is an isolated view of `{body, vars, headers, request}`. `request` contains method, path and redacted query values. Sensitive headers are redacted. Optional `upstream` contains only status, explicitly selected scalar values and a selection error; raw upstream bodies, raw request bytes, `Verified` and resolved auth secrets are unavailable. Return `{body, vars}`; omitted keys keep the previous envelope values.

An XML body (from `body_format: xml`) arrives as a tagged node tree — `{declaration, root}`, where each element is `{name, ns, attr, attrns, text, children}` — and the script must return the same shape, which keeps the body an XML document for a downstream `xml_prune` or a `body: transformed` forward. The shape is deliberately explicit rather than a convenience mapping of element names to values: a convenience mapping cannot represent attributes, repeated siblings, or mixed content, so a script that read and returned such a body would silently drop parts of the document. `attr` is keyed by local name so a script need not know the vendor's namespace prefix; `attrns` carries the namespace URI of any attribute that has one, keyed by the same local name, so a read-and-return script round-trips namespaces without having to think about them.

Options:

- `script`: required JavaScript. Top-level `return` is allowed (the runtime wraps the script in a function).
- `timeout`: optional duration, default `1s`. A missing timeout is a lint warning because goja has no memory cap. See [Security § Code node](security.md#code-node).
- `max_input_bytes`: optional, default `1048576`. Raise it deliberately when a script must inspect a larger parsed body; base64 file content is larger than its decoded bytes.
- `max_output_bytes`: optional, default `1048576`. Oversize output is refused, not truncated.
- `max_call_stack`: optional, default `1000`.

The interrupt is checked between JavaScript statements. A pathological regex may run until it finishes; use `filter` instead. Each request gets a separate VM.

## Logging & observability

Hookway emits structured `slog` JSON logs. Each log record carries a consistent set of field keys so log queries and alerts are stable across destinations.

- `logging.level`: optional, one of `debug`, `info`, `warn`, `error`; default `info`. Controls the minimum severity written to stdout — e.g. set `debug` to see the startup verify/exists checks and retry-attempt lines called out below, which are otherwise suppressed at the default `info` level. `HOOKWAY_LOG_LEVEL` overrides this at process start (env > `logging.level` > info). An invalid env or config value fails startup.
- Flow admission refusals use bounded error summaries instead of a request log for every 503. Each configured flow permits at most one summary write in flight and one summary per second; other refused handlers do not wait for that writer. `rejected_requests` counts refusals accumulated since the preceding summary. The final unsummarized interval remains visible in HTTP/admission metrics, which count every request.
- Native core/replay info/warn/error logging additionally samples thirty fixed hot-path message categories at ten records per second per category, with suppression summaries and unlabeled `hookway_logs_suppressed_total` / `hookway_replay_logs_suppressed_total` counters. Logger clones share the budget. Outcome counters and persisted audit are not sampled. Startup/configuration/shutdown and operator audit diagnostics bypass this budget; explicit `debug` logging bypasses sampling for bounded troubleshooting. This is not an asynchronous sink or a universal log-byte/memory bound. See [telemetry limits](../operations/observability.md#telemetry).
- `logging.header_allowlist`: optional list of HTTP header names that may appear in structured request logs. **Empty (the default) logs no request headers** (fail-closed). Allowed names are still redacted by the built-in sensitive-header heuristics and `security.redacted_header_denylist`. Proxy forwarding uses `security.forwarded_header_allowlist` and is independent of this list.

`hookway_config_load_failures_total` still increments on a failed load, but **fatal pre-listener failures are primarily visible via structured logs and a supervisor restart**. The process usually exits before Prometheus can scrape that counter.

### Canonical log fields

| Key | Present on | Description |
|-----|-----------|-------------|
| `destination` | publish path, startup | Stable destination id from the config map (e.g. `billing`). |
| `target` | publish path, startup | Queue/topic/routing-key/stream name (e.g. `stripe-events`). |
| `event_id` | publish path | Envelope event id (`id` on the published message). |
| `endpoint` | publish path | Flow name (e.g. `stripe_billing`); the metric label remains `endpoint` for compatibility. |
| `request_id` | publish path | Valid bounded value of the configured `request_id_header`, or generated UUID; matches the response/envelope. |
| `trace_id` | publish path | Trace id propagated from the inbound request, when present. |
| `error` | error/warn records | Diagnostic error text with URL/credential redaction on protected backend paths; not a place for payloads or custom-code secrets. |
| `subscribers` | `redis_pubsub` | Integer subscriber count at publish time; `0` triggers a drop warning. |
| `attempt` | `rabbitmq` retry | Publish attempt number. `attempt=1` is the initial try (not logged); the first logged retry is `attempt=2`. |
| `group` | `redis_stream` | Consumer group name used or created at startup. |
| `attr` / `want` / `got` / `policy` | `sqs`, `sns` reconcile | Attribute name, expected value, actual value, and policy action during queue/topic attribute reconciliation at startup. |

> **Breaking change (log keys only):** publish logs previously used `destination_queue_id` and `queue` as field keys. They are now `destination` and `target`. Prometheus metric label names and the JSON response/envelope body are unchanged — only the structured log field names changed.

### Log levels by event

| Level | Event |
|-------|-------|
| **Error** | Request/flow publication failures and terminal async failures; repeated hot-path diagnostics are storm-sampled in normal mode. Counters count every outcome. |
| **Info** | Startup resource creation: "created queue", "created topic", "created bucket", "ensured table", "created consumer group". |
| **Debug** | Startup verify/exists checks; `redis_list` and `redis_kv` per-target registration; RabbitMQ publish retry (with `attempt`); MongoDB duplicate-key skip ("skipped duplicate event"). |
| **Warn** | `redis_pubsub` no-subscriber drop ("published to channel with no subscribers; message dropped") — also increments `hookway_redis_pubsub_dropped_no_subscriber_total`. |
| **Warn** | RabbitMQ connection lost; initial-connect failure; dead-letter delivery. |
| **Info** | RabbitMQ reconnected. |

### Replay operations console

Frontend URL configuration, API CORS and scoped credentials belong to the
[console deployment guide](../operations/replay-console.md#build-and-deploy).
