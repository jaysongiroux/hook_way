# Production overview and remaining gaps

Reviewed **2026-10-02** against the local V2 working tree. This document describes
implemented behavior and deployment responsibilities. It replaces the earlier
2026-08-26 assessment, which described the removed endpoint configuration and
embedded replay store. Historical coverage figures, grades, line references and
closed findings are not evidence for the current build.

Hookway can provide a configurable ingress layer ahead of existing destinations.
Production acceptance depends on the configured response contract, sender retry
policy, destination capacity, traffic mix and deployment platform. Local tests
and synthetic throughput measurements cannot certify those external properties.

## What runs

- **Core (`hookway`)** validates a YAML graph, serves ingress, verifies requests,
  applies configured gates/transforms, and publishes or relays to destinations.
- **Builder** is optional. It edits and validates YAML; it does not manage a
  deployed process. Core's production image does not require the web bundle.
- **Replay (`hookway-replay`)** is an optional service with PostgreSQL storage for
  failed deliveries that core already acknowledged. Operators initiate retries;
  workers execute the queued runs. It is not a general durable ingress buffer.
- **Queue consumers** are external. Hookway does not poll SQS or run downstream
  processing workers.

Configuration uses named `flows`, typed nodes and `edges`. Removed `endpoints`
configuration is rejected. The legacy metric label `endpoint` carries the flow
name. See [Configuration](configuration.md), [Invariants](invariants.md),
[Ingress adapters](ingress-adapters.md) and [CLI](cli.md).

## Request and delivery contracts

| Area | Current behavior | Deployment consequence |
|---|---|---|
| Authentication | Registered verification schemes fail closed; trusted unverified flows use the explicit `no_auth` node. | Authenticate public traffic and reserve `no_auth` for a trusted boundary. |
| Admission | Default finite limit is 256 executing requests per instance. Excess work receives 503 with `Retry-After` before body buffering. Bounded waiting is opt-in. | Size execution and waiting limits for the request mix; waiting handlers still consume memory/connections. |
| Publication | Normal publish work is detached from caller cancellation and bounded by its publication timeout. Batch children use their parent batch deadline. | A disconnected caller does not imply a confirmed message was recalled. |
| Fan-out | Default `stop_on_failure` is sequential; `skip_failed` attempts other deliveries. Destinations are not a transaction. | Deduplicate using stable sender identities when retries can repeat a previously successful delivery. |
| Responses | Explicit response policies can acknowledge filtering or tolerated downstream failure; synchronous proxies can relay upstream responses. | HTTP 2xx alone does not establish publication success. Observe flow outcomes and publication counters. |
| Broker support | Standard/FIFO SQS and SNS publication, FIFO identifiers, typed attributes and wire-body selection are supported. | Actual queue/topic settings, quotas, IAM and downstream semantics remain external acceptance checks. |
| Routing | Typed lookups and registrations use configured stores and explicit environment admission. Registration can remain pending after an upstream create succeeds. | Observe registration outcomes and recover pending registration without repeating an already successful upstream creation. |
| Documents | Configurable wire/decoded limits and raw, XML, JSON and multipart relays are available. Bodies/responses are buffered within configured limits. | Bound file concurrency separately from small webhooks. A JSON wire cap does not validate a decoded base64 field. |
| Idempotency | Per-request envelope IDs survive internal retry/redrive. Sender retries receive new IDs. Replay guards are process-local; broker deduplication IDs are explicit. | There is no fleet-wide exactly-once guarantee. |

There is **no durable ingress spool**. A rejected request relies on sender retry.
The optional replay capture path is best effort and covers eligible acknowledged
failures; it cannot turn every rejection, crash or outage into durable acceptance.
Flows may disable failure capture explicitly. Batch graphs currently require
`capture_failures: false` when replay is configured; batch failure capture is
unsupported.

Asynchronous webhook delivery uses an in-memory queue and can lose queued or
in-flight work on crash, forced shutdown or drain timeout. Use broker-confirmed
publication or a synchronous relay when that loss model is unsuitable.

Routing, admission and registration execute inside configured core flows. Table
backfills, exports, cleanup and conditional recovery use external datastore
tooling. Core registration is last-write-wins.

A successful create followed by failed route registration must retain the
successful upstream response. Registration recovery and destination replay are
separate from repeating the entire ingress workflow. Read the configured
response policy before interpreting success metrics.

## Security boundaries

### Ingress and outbound requests

Use [request authentication](security.md#auth-schemes), finite body/admission
limits and explicit IP/proxy trust. `security.trusted_proxy_headers` does not
make arbitrary forwarding headers trustworthy: configure `trusted_proxies` to
match the actual ingress peers. The ingress proxy should overwrite untrusted
forwarding headers and should not retry a non-idempotent request against a
second replica automatically.

Managed outbound credentials support static headers and token acquisition with
bounded refresh/cache behavior. Use named credential profiles and configured
origin admission. Outbound HTTP includes authority-template validation and
SSRF protections when the private-address guard is enabled. Guard policy must
match the intended public or private upstream; DNS/origin admission and network
policy are separate controls.

Published envelope headers use sensitive-header redaction. A forwarding
allowlist provides a stricter posture than the default denylist. Request logs
have a separate header allowlist and do not log request bodies. Synchronous
proxy forwarding and replay snapshots have their own contracts; do not assume
envelope redaction automatically sanitizes stored request artifacts.

### Metrics, editor and replay

Core metrics/config APIs use the configured `security.api_key`; an empty key
leaves those surfaces unauthenticated. Prefer a separate private metrics listener
and restrict its access to scrapers. Core has no metrics-only key scope. The
editor is optional, stores no server-side configuration, and is not a deployment control plane. Its API supports wildcard
CORS; it does not provide a production identity/RBAC system or a complete browser
security-header policy. Keep it private or host the static builder separately.

Replay uses **named scoped keys**: `capture`, `read`, `payload`, `retry` and
`metrics`. Core should receive a capture-only key; Prometheus a metrics-only key.
Payload access, retries and history are separate from scraping. Replay records
caller key names in its history. Core's control listener has a separate token
and can perform delivery work, so it also belongs on a private network.

Replay artifacts can contain request bodies and original headers. Apply database
storage encryption, access restrictions, retention and backup policy. Dismissal
and garbage collection delete eligible artifacts; no generalized subject-based
redaction/erasure API or application-level payload encryption is provided.

Inbound listeners serve HTTP; Hookway does not terminate inbound TLS/mTLS.
Terminate TLS at the deployment edge or use a service mesh for encrypted internal
links. Outbound TLS settings exist separately for destinations, replay uploads,
replay control and the PostgreSQL connection. Read-header timeouts are present;
request, publication, shutdown and proxy deadlines still need coherent settings.

## Scaling, secrets and lifecycle

Replicas can serve ingress concurrently; no in-process leader coordinates normal
request execution. Size fleets using measured CPU, memory, admission occupancy,
waiting, downstream latency and publication errors. Autoscaling lag means an
existing fleet needs headroom for short bursts. Per-instance rate limits and
replay guards do not enforce global quotas or deduplication across replicas.

The rate limiter evicts idle keys but has **no hard key-count cap**. A stream of
unique keys can grow its map between sweeps. Enforce trusted identity and global
limits at the edge; a bounded limiter store remains a code hardening gap if this
surface is exposed to high-cardinality untrusted traffic.

`auth.secret_ref` supports environment, file and AWS Secrets Manager providers
with refresh and overlap behavior. Startup environment expansion is different
from a refreshing secret reference. Managed credentials also refresh according
to their profiles. There is no configuration hot reload/SIGHUP handler: change
flows, admin tokens and deployment bindings through a rolling restart.

SIGTERM starts bounded graceful HTTP shutdown, waits for in-flight requests and
control redrives, then closes flow/publisher/capture resources in order. This is
**graceful request drain**, not an implemented readiness-withdrawal delay. Core
has no draining readiness state or configurable pre-shutdown propagation wait.
The load balancer/orchestrator must remove a replica from traffic before stopping
it. `/ready` and `/readyz` normally remain healthy while any configured flow is
ready; `server.readiness_require_all: true` requires every flow to be ready. Set its termination allowance to cover withdrawal plus application shutdown
and any final publisher/capture drain. Forced termination can still lose work.

Replay replicas coordinate through PostgreSQL. Run `hookway-replay migrate`
before serving a new schema; `serve` refuses an older database schema. Plan
schema-compatible upgrades, database backups/restore and retry reconciliation.

Deployment templates live in [`deploy/kubernetes/`](../deploy/kubernetes/).
They are templates to bind to your environment, not proof of deployment readiness.
Compose demonstrations and their runtime configuration files live together under
[`examples/`](../examples/); their header comments describe what they demonstrate.
Traffic splitting, release orchestration and autoscaling belong to the platform.

## Metrics and dashboards

Enable metrics explicitly and, when needed, Go/process collectors. Import
[`hookway-overview.json`](../grafana/dashboards/hookway-overview.json) into Grafana
and select your Prometheus data source, job, instance and flow. Import the separate
[`hookway-replay.json`](../grafana/dashboards/hookway-replay.json) for replay.
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
No OTLP exporter or tracer-provider setup ships. Node spans use the global
OpenTelemetry provider and are no-ops unless a provider is configured by an
embedding application.

## Performance evidence and deployment acceptance

[Performance results and methodology](performance.md) records three 5,000/s,
20-second signed bursts and a continuous 500/s, 30-minute mixed workload on two
2-vCPU/4-GiB replicas, plus selected fallback, batch and concurrent document checks.
The measured build used synthetic destinations on one local Docker host. Results
are dated and tied to the recorded source/image; later code changes, actual AWS,
TLS, autoscaler behavior and the final workload mix require their own validation.

Before production traffic, verify:

1. Authoritative neutral request/response and broker-message fixtures for each
   required route: authentication bytes, transformations, routing, attributes,
   FIFO identities and failure responses. Company source code is not required.
2. Destination permissions, queue/topic settings, quotas, deduplication/filtering
   behavior and response latency using the actual backend.
3. The final webhook/batch/file mix, burst headroom, latency budget and timeout
   policy. Count skips and over-budget replies as failures, not throughput.
4. Replica removal, in-flight drain, rolling rollback and autoscaler response
   under load. Verify sender retry behavior during a destination outage.
5. Replay retention, backup/restore and operator permissions if replay is enabled.

## Engineering and release checks

The repository defines Go unit/race/conformance tests, container-backed destination
and replay tests, Compose end-to-end tests, example validation/smoke tests, builder
unit/Playwright tests, schema drift checks, a coverage floor, dead-code checks,
`go vet`, golangci-lint/gosec, govulncheck, dashboard/Prometheus-rule checks, and
Docker image scanning. These are available checks; their presence does not mean
the current local diff or a particular release has passed them.

The release workflow reruns CI for a version tag, publishes core and replay images
and creates a GitHub Release. Current release gaps are floating action/base-image
tags, no explicit multi-architecture build matrix, no explicit SBOM/signing workflow
or release-level provenance policy, and no downloadable standalone binary assets.
Do not infer a provenance or authenticity guarantee from Docker build defaults.

There is no web ESLint configuration, dependency-update bot configuration or
required npm audit job. A JSON canonicalization fuzz target exists; the older
claim that there is no fuzzing anywhere is false. Broader fuzz coverage for
untrusted auth/header/path parsers and a scheduled fuzz job remain opportunities.
MIT licensing and contribution instructions ship. A dedicated vulnerability
reporting policy, CODEOWNERS and changelog file are absent.

## Remaining work by responsibility

| Responsibility | Remaining gap or decision |
|---|---|
| Application hardening | Bound rate-limiter key cardinality; add readiness withdrawal if it is required inside core; broaden parser fuzzing. |
| Administration | Core/editor have no scoped multi-caller authorization or token overlap. Replay has scoped keys but no built-in OIDC/mTLS identity service. |
| Durability/privacy | No ingress WAL, durable async webhook queue, application-level replay encryption or generic subject-based erasure. Choose only the guarantees your deployment requires. |
| Release/tooling | Harden artifact identity/signing and dependency maintenance according to release policy; bind an arm64 build if that is the target platform. |
| Deployment acceptance | Real backend parity, quotas, TLS, lifecycle and autoscaler behavior; representative workload and longer observation where needed. |

Avoid treating optional enterprise features as universal blockers. A durable
spool, Helm chart, shared limiter, hosted editor or extra alert system is a
separate product/deployment choice. The acceptance contract should state what
Hookway confirms before responding and which system owns recovery afterward.
