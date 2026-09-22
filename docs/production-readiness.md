# Hookway — Enterprise Production Readiness Assessment

> **Historical snapshot.** Reviewed 2026-08-26 at commit `612e372` on `main`, **before** the typed-port graph. Keep this file as a **backlog**, not current operator truth. Live behavior is in [configuration.md](configuration.md) and [security.md](security.md).
>
> **Treat every line-number citation in this file as unreliable.** Citations to `handleWebhook` / `internal/httpapi/server.go` in the three- and four-digit range point past the end of that file — it is 278 lines today, and most of the code described moved to `internal/httpapi/flowserver.go`, `internal/flowconfig/`, or `internal/graph/`. Symbol names are a better guide than line numbers. The status table below and the **CLOSED** / **Corrected** callouts through the body are the current-truth layer; the prose around them is the 2026-08-26 text.

**Date:** 2026-08-26 (endpoint-based config). Status table added 2026-09-16.

**Reviewed at commit:** `612e372` (branch `main`, clean tree)
**Scope:** whole repository — Go services, config layer, CI/release pipeline, docs, web UI, deployment story

### Status vs current Hookway

| Item | Now |
|---|---|
| **P0-2** pending events unreplayable | **Closed.** Failed and unattempted `publish` routes are recorded so `stop_on_failure` 503s are replayable. |
| **P0-5** no secret file / overlap | **Partial.** `auth.secret_ref` (`env` / `file` / `aws_secrets_manager`) with refresh + overlap. Still no SIGHUP / hot reload. |
| **P2-5** `key_by` only `"ip"` / “MVP” | **Partial.** `rate_limit.key_by` is `source_ip` or `header.<name>`. The old validator guards config keys that are now rejected outright, so its "MVP" message is unreachable. The §5 body text is stale; this row is correct. |
| **P2-6** readiness AND-aggregation | **Closed** (D9). `/ready` is 200 while at least one flow is ready; `server.readiness_require_all: true` restores 503-if-any. |
| **P2-10** OpenTelemetry | **Spans only.** `OnNode` starts `hookway.node.*` spans on the global tracer; there is no OTLP exporter. Do not claim OpenTelemetry. See [configuration.md § OpenTelemetry spans](configuration.md#opentelemetry-spans). |
| **P2-2** empty `idempotency_key` | **Still true, broader.** envelopes omit `idempotency_key`; consumers should dedupe on `id`. |
| **P2-1** unvalidated `X-Trace-ID` | **Closed.** `traceIDFromRequest` now gates the `X-Trace-ID` fallback through `isValidTraceID` (length 32, hex, not all-zeros) — stricter than this document's own proposed fix. The §5 repro no longer reproduces. |
| **P2-9** no Prometheus alert rules | **Closed.** `deploy/alerts.yaml` ships 7 alerts with `deploy/alerts_test.go`, promtool-validated in the required **Observability** CI job. |
| **P2-7b** no coverage gate | **Closed.** A statement-coverage floor is enforced by `make coverage` in the required **Coverage** job. (The fuzzing half of P2-7 is still open.) |
| **P0-1** no LICENSE | **Partially closed.** A repo-root `LICENSE` (MIT) now ships. `SECURITY.md`, `CONTRIBUTING.md`, `CODEOWNERS`, and `CHANGELOG.md` are still absent. Note the original recommendation was Apache-2.0 for its patent grant; MIT has none. |
| **P0-3** async webhook in-memory queue | **Still open** (documented in configuration.md). |
| **P0-4** one static API key | **Still open.** |
| **P1-1** no SIGTERM drain | **Still open.** The symbol moved (`cmd/hookway/shutdown.go`), the finding did not. |
| **P1-2** process-local rate limit / replay guard | **Still open** (D11). |
| **P1-3a** `ReadHeaderTimeout` on the main listener | **Closed.** All four servers (webhook, metrics, replay, editor) set it from the shared `readHeaderTimeout` const in `internal/httpapi/server.go`. |
| **P1-3b** no in-process TLS / mTLS | **Still open.** There is no `server.tls.*` block and no `ListenAndServeTLS`; terminate at an ingress. |
| **P1-4** replay store DDL on startup | **Still open.** |
| **P1-6** `make lint` is `go vet` | **Mostly closed.** `make lint` is still `go vet`, but it is no longer the gate: `.golangci.yml` enables 12 linters (incl. `gosec`, `staticcheck`, `errcheck`) in a required job, and `govulncheck` runs in two workflows. ESLint and `npm audit` are genuinely still absent. See the rewritten §4 entry. |
| **P1-8** no Helm chart | **Still open.** |

---

> ### ⚠️ Config examples below use the removed v1 schema
>
> This document predates the typed-port node graph. Its body talks about
> **`endpoints:`**, and `endpoints` is now a **hard validation error** — the
> strict decoder reports it as an unknown top-level key, so a config shaped
> like the ones described below fails at startup, immediately, before serving
> a single request.
>
> Wherever the text below says "endpoint", read "**flow**". Configuration is
> named `flows` of typed nodes wired by `edges`. Do not copy a config shape
> out of this file. The current schema is in
> [configuration.md § Flows](configuration.md#flows); the port lattice that
> replaced the fixed pipeline is in [invariants.md](invariants.md).
>
> The one place `endpoint` survives as a live term is as a *label*: the
> `endpoint` metric label and the `endpoint` field in the HTTP response body
> both still carry the flow name, kept for consumer compatibility.

---

## 1. Executive summary

Hookway is a genuinely well-engineered codebase. It is materially better than most
internal services at this stage: ~58.7k lines of Go with a high test-to-source ratio,
93% coverage in the HTTP request path, an unusually disciplined comment culture that
records *why* decisions were made, fail-closed defaults throughout auth and filtering,
panic recovery at every goroutine boundary, correct `X-Forwarded-For` handling,
constant-time secret comparison, bounded shutdown waits with carefully-ordered
`defer` LIFO semantics, and 45 Prometheus metrics with a tested Grafana dashboard.
`go vet` is clean and the full unit suite passes.

**It is not, however, production-ready for an enterprise environment yet.** The gaps
are not code-quality gaps — they are *platform* gaps. In rough order of severity:

1. **Legal/governance:** a repo-root `LICENSE` (MIT) now ships, clearing the hard blocker. `SECURITY.md`, `CONTRIBUTING.md`, `CODEOWNERS`, and `CHANGELOG.md` are still missing.
2. **Durability:** Hookway can lose events. There is no local spool, and the one
   failure mode where replay would save you (total broker outage) is precisely the
   case the replay store cannot cover.
3. **Admin-plane security:** a single static API key guards metrics, config, *and* the
   ability to re-inject arbitrary captured production traffic. No RBAC, no rotation,
   no audit.
4. **Operability:** no config hot reload, no zero-downtime secret rotation, no
   readiness drain on `SIGTERM`, no deployment artifacts (Helm/k8s), no migrations.
5. **Supply chain:** no SBOM, no image signing, no dependency scanning of `go.mod`,
   single-arch images. *(The original finding here — "`go vet` is the entire lint
   gate; no govulncheck" — is **closed**: golangci-lint with 12 linters and
   `govulncheck` are both required CI jobs. See the rewritten P1-6.)*

**Verdict:** ready for internal/low-stakes production behind a trusted network today.
Roughly one focused quarter of work away from enterprise-grade. The architecture is
sound enough that nearly every item below is additive — very little needs rework.

### Scorecard

| Dimension | Grade | Note |
|---|---|---|
| Code quality & design | **A** | Clean seams, fail-closed defaults, excellent inline rationale |
| Test discipline | **B+** | 93% on the hot path; thin on `queue`/`replaystore`; no fuzz (a coverage floor is now gated in CI) |
| Documentation (user) | **A−** | Exceptional depth; missing runbook/upgrade/DR |
| Observability | **A−** | Great metrics + dashboard; alert rules now ship (P2-9 closed); still no OTel export |
| Security (data plane) | **A−** | Auth, redaction, loop/replay/rate controls all solid |
| Security (admin plane) | **D** | One shared static key for read *and* replay-injection |
| Durability / DR | **D** | No spool; unreplayable total-outage path; async engine loses queue |
| Operability | **C−** | No reload, no drain, no migrations, no deploy artifacts |
| Supply chain / release | **C** | Trivy + govulncheck + golangci-lint now gate; still no SBOM/signing/multi-arch |
| Governance / legal | **D** | LICENSE (MIT) now ships; still no SECURITY.md, CODEOWNERS, or CHANGELOG |

---

## 2. Verification baseline

Everything below was run against the working tree; results are reported as observed.

```
go vet ./...                                    → clean
go test ./... (unit, integration-skipped)       → all 18 packages PASS   [now 33 packages]
go test ... -cover                              → see table below
```

| Package | Coverage | | Package | Coverage |
|---|---|---|---|---|
| `internal/apikey` | 100.0% | | `internal/logging` | 89.7% |
| `internal/auth` | 96.0% | | `cmd/.../bridge` | 77.8% |
| `internal/ratelimit` | 96.0% | | `internal/metrics` | 75.0% |
| `internal/webhook` | 95.0% | | `internal/config` | 73.7% |
| `internal/replay` | 93.9% | | `internal/configapi` | 73.6% |
| `internal/httpapi` | **93.0%** | | `internal/replayapi` | 70.0% |
| `internal/editor` | 90.4% | | `cmd/hookway` | 61.8% |
| `internal/filter` | 89.4% | | `internal/replaystore` | 50.8% |
| | | | `internal/queue` | **43.3%** |

`queue` and `replaystore` are understated — much of their behaviour is covered by
Docker-gated integration suites that the unit run skips. They are still the two
packages carrying the most production risk relative to their measured coverage, and
there is no combined unit+integration coverage report to prove otherwise.

---

## 3. P0 — Blockers

### P0-1. No `LICENSE` file — **partially CLOSED**

> **A repo-root `LICENSE` now ships (MIT).** The hard blocker described below —
> "all rights reserved" by default — is cleared. Note the fix below recommended
> Apache-2.0 specifically for its patent grant, which MIT does not provide; if a
> reviewer's checklist asks for one, that is a deliberate open decision now, not
> an omission. Still absent: `SECURITY.md` (vulnerability disclosure contact and
> SLA), `CONTRIBUTING.md`, `CODEOWNERS`, `CHANGELOG.md`.

The repository shipped no license, so by default it was "all rights reserved." No
enterprise legal or OSS-review process will approve deployment of a dependency in
that state, regardless of technical merit.

**Fix:** pick a license (Apache-2.0 is the safest default for enterprise ingestion —
the patent grant matters to legal reviewers, and it is more acceptable than MIT to
corporate OSS committees). Add the four governance files. This is a half-day of work
and it unblocks everything else.

### P0-2. No durable ingest buffer — events can be lost

[security.md § Availability Model](security.md#availability-model) states it plainly: *"Hookway does not durably spool requests
locally."* On a broker outage Hookway returns `503` and relies on the sender's retry
policy. That is a reasonable v1 stance, but it is not an enterprise availability
model, because sender retry behaviour is wildly inconsistent — some providers retry
for 72 hours, some retry three times over 15 minutes, some (and most internal
first-party senders) do not retry at all.

> **This sub-finding is CLOSED** — the status table says so and the code agrees.
> `internal/httpapi/flowserver.go` now records routes that were never attempted
> as `failed` delivery rows alongside the one that actually failed, so a
> `stop_on_failure` 503 leaves a replayable event. [security.md § Replay](security.md#replay)
> states the current behaviour: *"Failed and unattempted routes are replayable."*
> The description below is the **original 2026-08-26 finding**, kept for the
> reasoning and the remaining fixes (2 and 3), not as current behaviour.

The sharper problem *was* that the replay store *looked* like the mitigation but
was not:

- Capture happens, so the event is stored.
- On the `stop_on_failure` full-failure path, the publisher bailed on the first
  error and recorded no delivery outcomes, so the event's status stayed
  `pending`.
- An event with no recorded delivery outcome **cannot be replayed** — `422` on
  single replay, skipped in bulk. That rule still holds; what changed is that
  a failed `stop_on_failure` run no longer produces such an event.

So the single scenario where you most needed replay — every destination down —
was exactly the scenario where replay refused to run. That hole is now filled.

**Fix, in increasing order of effort:**
1. ~~*(cheap, high value)* Record the intended destination set at capture time so a
   `pending` event is replayable.~~ **Done.**
2. Add an automatic re-drive worker that sweeps `pending`/`failed` events from the
   replay store on a backoff schedule, instead of requiring an operator to notice
   and hit the API. This turns the replay store from a forensic tool into an actual
   durability layer.
3. *(optional)* A local WAL spool (bbolt/SQLite) for deployments with no Postgres, so
   the durability guarantee doesn't depend on an optional subsystem.

Also: document the availability model as an explicit, numbered SLO rather than prose.

### P0-3. Async webhook delivery loses in-flight events on every restart

`internal/queue/webhook_engine.go:59-80` — the async delivery engine is an in-memory
buffered channel (`jobs`, default capacity 1000) drained by a worker pool. In async
mode Hookway returns `202` to the sender *before* delivery is attempted, so the
sender will never retry. Any of the following silently drops every queued and
in-retry job:

- `SIGKILL` (grace period exceeded), OOM kill, node eviction, spot reclaim
- exceeding `drain_timeout` during graceful shutdown
- a crash between enqueue and delivery

In Kubernetes this is not an edge case — it happens on **every rolling deploy** for
whatever is in the queue when the drain timer expires. `hookway_webhook_dropped_total`
covers `queue_full` and `engine_closing`, but a hard kill records nothing at all.

**Fix:** persist the async queue (reuse the replay store, or a small embedded
durable queue), or make the durability contract explicit — refuse `delivery_mode:
async` unless a dead-letter destination *and* the replay store are both configured,
and document that async mode is at-most-once without them.

### P0-4. One static API key guards the entire admin plane

`internal/apikey/apikey.go` implements a single constant-time comparison against one
configured `security.api_key`. That same key guards:

| Surface | Capability |
|---|---|
| `/metrics` | Read observability data |
| `hookway editor` config API | Read schema, validate config |
| Replay API (`POST /replay/replay`) | **Re-inject arbitrary captured production traffic into live destinations** |

Consequences:

- The credential you paste into your Prometheus scrape config
  ([security.md § Metrics & config-API authentication](security.md#metrics--config-api-authentication)) is the same credential that can replay production
  payments traffic. Prometheus configs are widely readable.
- No per-caller identity → no audit trail of *who* replayed *what*, which is a
  compliance requirement for anything touching financial or health data.
- No key rotation without a restart, and no support for two valid keys during a
  rotation window.
- No RBAC — you cannot grant read-only replay-list access without also granting
  replay-execute.
- No expiry, no scoping.

**Fix (staged):**
1. Split into separate keys per surface (`security.metrics_api_key`,
   `security.replay_api_key`, `security.config_api_key`), each accepting a *list* of
   valid keys so rotation is zero-downtime.
2. Emit a structured audit event for every replay: caller key id (hashed), event ids,
   destinations, `reprocess` flag, timestamp, result.
3. Add OIDC/JWT or mTLS as an option for the replay listener specifically.

### P0-5. No config hot reload; no zero-downtime secret rotation

`AuthConfig.Secret` (`internal/config/endpoints.go:153-164`) is a single string,
populated by `${ENV}` expansion at load time. There is no `SIGHUP` handler, no
file-watch reload, and no `secret_file:` indirection anywhere in the codebase
(verified by grep).

This produces three separate operational problems:

- **Adding or changing any endpoint requires a full restart** — and a restart is not
  free, because of P0-3 (drops the async queue) and P1-1 (drops in-flight requests).
- **Kubernetes Secret rotation does not work.** Mounted-secret updates and Vault
  Agent / Secrets Store CSI injection both work by updating a file on disk. Hookway
  only reads env vars, which are frozen at process start.
- **Provider secret rotation has a hard cutover.** [security.md § Production Hardening](security.md#production-hardening) advises
  "keep old/new overlap short during provider-side rotations," but with a single
  secret field there is no overlap possible at all — during the switch, signatures
  from one side or the other fail with `401`.

**Fix:**
1. `auth.secret_file:` (and `secrets:` as a list) alongside `secret:`. Accept a list
   and try each — this alone makes rotation zero-downtime.
2. `SIGHUP` → re-load, re-validate, and atomically swap verifiers/filters/limiters.
   The `FlowServer` already builds verifiers, filters, and limiters once at
   construction; the
   refactor is to move them behind an `atomic.Pointer` swap rather than plain fields.
3. Reject a reload that fails validation and keep serving the old config, with a
   `hookway_config_reload_total{result}` metric.

---

## 4. P1 — High priority

### P1-1. No readiness drain on `SIGTERM`

**Still valid; the symbol moved.** The `httpapi.Shutdown` wrapper this entry
originally cited has been deleted; the drain path is now
`servingResources.drain` in `cmd/hookway/shutdown.go`. The finding is
unchanged: on receiving a signal the process logs and goes straight into
`drain`, which calls `srv.Shutdown` on every listener immediately. Meanwhile
`handleReady` (`internal/httpapi/flowserver.go`) keeps serving `ready` from
the prober cache right up until the listener closes. There is still no
draining state and no `server.drain_delay`.

Kubernetes endpoint removal is asynchronous — kube-proxy, the ingress controller, and
any service mesh each learn about the terminating pod on their own schedule, typically
1–5 seconds after the `SIGTERM`. During that window the pod is still receiving new
connections while it is actively refusing them, which senders see as connection resets
or `502`s from the ingress. On a large rolling deploy this is a visible error spike.

**Fix:** on signal, flip a `draining` flag that makes `/ready` and `/readyz` return
`503` immediately, wait a configurable `server.drain_delay` (default ~5s), *then* call
`Shutdown`. Document `terminationGracePeriodSeconds >= drain_delay + shutdown_timeout`.

### P1-2. Rate limiting and replay protection are process-local

`internal/ratelimit/limiter.go` and `internal/replay/cache.go:8-13` are both in-process
maps. [security.md § Rate Limiting](security.md#rate-limiting) and [§ Replay Protection](security.md#replay-protection) document this honestly, but the implications are worth
stating in enterprise terms:

- With N replicas, the effective rate limit is **N× the configured value**. A limit
  advertised to a partner as "1000 rpm" is actually 6000 rpm on a 6-pod deployment.
- **Replay protection is trivially bypassable.** It is a *security control*, and its
  guarantee is "per pod." An attacker replaying a captured signed request just needs
  the load balancer to pick a different pod — which it will, by design.
  (An earlier revision of this entry attributed a "use sticky routing by
  signature" recommendation to `docs/security.md`. No such text exists there,
  or anywhere in the repo — the quote was fabricated. What security.md § Replay
  Protection actually says is that replay protection is process-local, that a
  replay can pass on another replica, and that **consumers must deduplicate**.)
- All state is lost on restart, so a deploy resets the replay window entirely.

The Redis client already exists (`internal/queue/redis_client.go`) and go-redis is
already a direct dependency, so the backend is available.

**Additionally**, `Limiter.Allow` (`limiter.go:52-54`) only sweeps when
`now.Sub(lastSweep) > idleTTL`, with `idleTTL` hardcoded at 10 minutes
(`limiter.go:10`). Between sweeps the `buckets` map grows without bound. With
`trusted_proxy_headers` enabled and an attacker-influenced `X-Forwarded-For`, a spray
of unique addresses grows the map for a full 10-minute window before any eviction —
a slow memory-growth vector. Cap the map size and make `idleTTL` configurable.

**Fix:** extract `ratelimit.Store` and `replay.Store` interfaces with `memory` and
`redis` implementations; make the backend configurable per deployment. Fail closed
(reject) rather than open if the shared store is unreachable, configurably.

### P1-3. No in-process TLS, no mTLS, no `ReadHeaderTimeout` on the main listener

`Server.HTTPServer()` (`server.go:289-297`) and `MetricsServer()` (`server.go:214-227`)
only support plaintext `ListenAndServe`. There is no `server.tls.*` config block
anywhere (verified by grep for `ListenAndServeTLS`). [security.md § Production Hardening](security.md#production-hardening) correctly
advises terminating at a load balancer, and for many deployments that is sufficient —
but enterprise environments frequently mandate in-cluster encryption end to end (PCI
segmentation, zero-trust networking, FedRAMP), and mTLS is the natural authentication
mechanism for the admin/replay listeners.

**The `ReadHeaderTimeout` half is CLOSED (P1-3a).** All four servers — webhook,
metrics, replay, and editor — now set `ReadHeaderTimeout` from the shared
`readHeaderTimeout` constant in `internal/httpapi/server.go`. The gosec G112
inconsistency this entry described is gone.

**The TLS/mTLS half is genuinely still open (P1-3b).** There is no
`server.tls.*` config block and no `ListenAndServeTLS` anywhere. For many
deployments, terminating at a load balancer is sufficient — but enterprise
environments frequently mandate in-cluster encryption end to end, and mTLS
remains the natural authentication mechanism for the admin/replay listeners.

**Fix (remaining):** add `server.tls: {cert_file, key_file, client_ca_file, min_version}`
and the same for the metrics/replay listeners.

### P1-4. Replay store has no versioned migrations

`internal/replaystore/store.go:84,148-186` runs `CREATE TABLE / CREATE INDEX IF NOT
EXISTS` on every process start. Three consequences:

- **The application requires permanent DDL privileges on the production database.**
  Most enterprise DBA teams will refuse to grant that to a service account, full stop.
- **There is no schema version.** During a rolling deploy two binary versions talk to
  one database with no compatibility check, and there is no way to gate startup on a
  schema the binary understands.
- **`IF NOT EXISTS` cannot retrofit constraints** — the code says so itself at
  `store.go:173-177` and `store.go:277`. Any deployment created before the `outcome`
  CHECK was introduced is still running without it, silently, forever. Today a Go-level
  guard covers it; the next constraint might not get one.

**Fix:** adopt a migration tool (goose, golang-migrate, or Atlas), add a
`schema_migrations` table, and split migration into an explicit `hookway migrate`
subcommand (runnable as a k8s Job/init container with elevated credentials) so the
serving process can run with `SELECT/INSERT/UPDATE/DELETE` only. Fail startup with a
clear error if the schema version doesn't match the binary.

### P1-5. Webhook payloads are stored in plaintext, with no redaction or erasure path

The `replay_events.envelope jsonb` column (`store.go:159-168`) holds the full webhook
body, headers, and source IP. Webhook payloads routinely carry PII, and in
regulated domains plausibly carry financial and identity data. Current state:

- No encryption at rest beyond whatever the database provides (which is usually
  full-disk — not field-level, and not protecting against a `SELECT`).
- No configurable payload redaction *before* capture. Header redaction exists and is
  good ([security.md § Header Redaction and Allowlisting](security.md#header-redaction-and-allowlisting)), but the body is stored verbatim.
- No per-endpoint capture opt-out — only the global `replay.skip_unauthenticated`.
- `GET /replay/events/{id}` returns the full envelope to any holder of the single API
  key (see P0-4), with no field masking.
- No GDPR/CCPA erasure path other than waiting for TTL expiry. There is no
  "delete all events matching subject X" operation.

**Fix:** add `replay.capture.body: full | redacted | metadata_only` with a JSON-path
redaction list reusing the existing filter path machinery; add per-endpoint
`replay.capture: false`; add envelope encryption with a configurable KMS/age key; add
a `DELETE /replay/events` filtered-purge endpoint for erasure requests; document the
data classification and retention rationale.

### P1-6. `make lint` is only `go vet` — **mostly CLOSED**

`make lint` is still literally `go vet ./...`, but the claim that followed —
"that is the entire static-analysis gate, and it is what CI runs
(`.github/workflows/release.yml:62-63`)" — is **wrong now**. That release.yml
step no longer exists, and the gate lives in `.github/workflows/reusable-ci.yml`.

Closed since this was written:

- **golangci-lint.** `.golangci.yml` is checked in and enables 12 linters:
  `govet`, `staticcheck`, `errcheck`, `errorlint`, `nilerr`, `bodyclose`,
  `noctx`, `ineffassign`, `unused`, and `gosec`. It runs as its own required CI
  job (`golangci-lint`) and is available locally as `make golangci-lint`.
  `errcheck` and `gosec` — both called out as missing here — are on.
- **`govulncheck`** runs in two places: a required `govulncheck` job in
  reusable-ci, and again in the weekly/on-change `security.yml`. `make govulncheck`
  runs it locally. It is a gate, not a habit.
- **Additional gates that did not exist at all:** `make deadcode` (fails on any
  unreachable function), `go mod tidy -diff`, a **Coverage** floor job, a
  **Formatting** job (`gofmt -l` + `git diff --check`), an **Observability** job
  (Grafana dashboard tests + promtool on the shipped alert rules), and `knip`
  dead-code analysis for `web/`.

Genuinely still missing:

- **`web/` has no ESLint config and no `npm audit` in CI.** It does have vitest,
  Playwright e2e, knip, and a node-schema drift check — but no linter and no
  dependency audit, across 23 npm dependencies including a React + Vite tree.
- **Dependency scanning of `go.mod` itself.** Trivy scans the built image, but the
  image is `FROM scratch` containing a single static binary, so the scan sees only the
  Go stdlib version and module metadata stamped into the binary. There is no OS
  package surface. This is a much narrower check than it appears. (`govulncheck`
  partly covers this now, reachability-filtered.)
- **No Dependabot or Renovate config.** `.github/` contains only `workflows/` —
  no `dependabot.yml`.

**Fix (remaining):** add an ESLint config and `npm audit --audit-level=high` to the
Web job, and add `.github/dependabot.yml` covering `gomod`, `npm`, `docker`, and
`github-actions`.

### P1-7. Supply-chain gaps in the release pipeline

Reviewing `.github/workflows/release.yml`:

| Gap | Detail | Impact |
|---|---|---|
| Floating action tags | `actions/checkout@v4`, `docker/login-action@v3`, etc. | A compromised action tag can exfiltrate `DOCKERHUB_TOKEN`. Enterprise policy usually mandates SHA pinning. |
| No SBOM | — | Cannot answer "are we affected by CVE-X" without rebuilding. Increasingly a procurement requirement. |
| No signing / provenance | No cosign, no SLSA, no GitHub attestations | Consumers cannot verify image authenticity. |
| Single architecture | No `platforms:` on `build-push-action` | Blocks Graviton/arm64 fleets — a real cost and availability constraint. |
| Base image by tag | `FROM golang:1.25.13-alpine` | Not reproducible; pin by digest. The criticism stands — the pin just tracks a newer patch now. |
| No released binaries | Docker Hub image only | Blocks VM/bare-metal deployments; no checksums or signatures. |
| ~~No CodeQL/SAST~~ | `gosec` runs via golangci-lint | **Partly closed.** First-party Go is scanned; there is still no CodeQL. |
| ~~One monolithic job~~ | Was "11 sequential test steps in `release-check`" | **Closed.** CI is 13 parallel jobs in `reusable-ci.yml`, including a 9-way integration matrix, behind one required `All checks passed` status. |
| ~~No coverage gate~~ | — | **Closed.** A statement-coverage floor is enforced by `make coverage` in the required **Coverage** job. |

Note the workflow's `permissions: contents: read` default is correct and good practice —
that part is already right.

**Fix:** SHA-pin all actions; add syft SBOM generation + upload; add cosign keyless
signing and `actions/attest-build-provenance`; build `linux/amd64,linux/arm64`; pin
base images by digest; add a `goreleaser` job for binaries + checksums; split the test
matrix.

### P1-8. No deployment artifacts

There is no Helm chart, Kustomize base, or plain Kubernetes manifest anywhere in the
repository (verified by find). The only orchestration file,
`examples/docker-compose.yaml`, is explicitly dev-only and ships
`PADDLE_WEBHOOK_SECRET: change-me` with `guest`/`guest` RabbitMQ.

Every enterprise team adopting Hookway therefore has to independently rediscover:
liveness → `/healthz` (never `/ready`, and the docs are good about explaining why),
readiness → `/ready`, `terminationGracePeriodSeconds` aligned to `shutdown_timeout`,
the preStop drain hook, resource requests/limits, `metrics.addr` on a separate port
with a `NetworkPolicy` keeping it off the ingress path, `replay.addr` similarly
isolated, and a PodDisruptionBudget honouring the "at least two replicas" guidance in
[security.md § Production Hardening](security.md#production-hardening). Every team will get some of this wrong.

**Fix:** ship a Helm chart with sane, secure defaults encoding all of the above, plus
a `NetworkPolicy` template, a `ServiceMonitor` for Prometheus Operator, and a
`PrometheusRule` (see P2-9). This is the single highest-leverage artifact for adoption.

---

## 5. P2 — Medium priority

### P2-1. Confirmed bug: unvalidated `X-Trace-ID` produces a malformed W3C traceparent — **CLOSED**

> **Fixed.** `traceIDFromRequest` (`internal/httpapi/server.go`) now routes the
> `X-Trace-ID` fallback through `isValidTraceID`, which requires `len == 32`,
> all-hex, and rejects an all-zeros id — *stricter* than the one-line fix this
> entry proposed, which did not exclude all-zeros. The **Verified:** repro below
> no longer reproduces: `X-Trace-ID: not-hex-at-all` is now discarded and a
> generated id is used, so the synthesized traceparent is always well-formed.
> The same `isValidTraceID` guard is applied to the inbound `Traceparent`
> segment, so the two paths can no longer diverge. The original finding is
> retained below for the record.

The inbound `Traceparent` trace-id segment is
carefully hex-validated at `server.go:949`, and the comment above it explains exactly
why a length-only check would be insufficient — *"a crafted non-hex segment (e.g. 32
'g' characters) flow[ing] into that synthesized header"*. But the `X-Trace-ID` fallback
three lines later (`server.go:953`) returns the header value with no validation at all,
and `traceparentFromRequest` then embeds it into `"00-" + traceID + "-" + span + "-01"`.

The exact hazard the first check was written to prevent is reachable through the second
path.

**Verified:**

```
X-Trace-ID: not-hex-at-all
  → traceID     = "not-hex-at-all"
  → traceparent = "00-not-hex-at-all-f7ed4519afde4d20-01"
  → isValidTraceparent(traceparent) == false
```

That value is stamped onto outbound webhook deliveries
(`internal/queue/webhook_engine.go:404-408`) and persisted in the envelope. A sender can
therefore inject arbitrary strings into downstream trace collectors, and a value with a
mismatched hyphen count or unexpected characters can break a strict W3C parser on the
receiving side. A value containing CRLF is rejected by Go's outbound header writer,
which turns it into a delivery failure instead — still sender-controlled breakage.

**Fix:** apply the same `len == 32 && isHex(...)` check to the `X-Trace-ID` fallback;
fall through to the generated id when it fails. One line, plus a test.

### P2-2. `no_auth` endpoints publish an empty `idempotency_key`

`Server.idempotencyKey` (`server.go:890-906`) resolves in this order: configured
`idempotency_header` → for `header_exact`, a SHA-256 of the raw body → otherwise, a
hash of the signature header. For a `no_auth` endpoint with no `idempotency_header`
configured, there is no signature header, so it returns `""`.

> **Corrected.** This entry quoted `docs/security.md` as documenting a
> consumer contract of *"consumers dedupe by `idempotency_key`"*. security.md
> has never said that, and today says the **opposite**, explicitly: *"Hookway
> does not populate `idempotency_key` on the envelope (there is no
> `idempotency_header` on `auth` / `no_auth`). Dedupe on `id`."* See
> [security.md § Idempotency](security.md#idempotency). The proposed fix below
> also named an `idempotency_header` config field on `no_auth` — **no such
> field exists** on any node.
>
> The underlying observation generalized rather than resolved: `idempotency_key`
> is empty on **every** v2 ingest envelope, not just `no_auth` ones (see the
> P2-2 status row). The documented and implemented dedup key is the envelope
> `id` (UUIDv7), which is always populated and always unique per delivery.

**Fix:** none needed at the envelope level — dedupe on `id`. If a future change
wants a real idempotency key, it must introduce the config surface first; do not
document a field that does not exist.

### P2-3. Wildcard CORS and missing security headers on the config editor

`internal/editor/editor.go:79-89` sets `Access-Control-Allow-Origin: *` and permits the
`Authorization` request header. Because credentials are not allowed, browsers will not
attach cookies, so the practical exposure is limited — but this lets any page on the
internet drive a reachable `hookway editor` instance, and it is an automatic pen-test
finding on an administrative interface.

The comment justifying it (*"These endpoints expose no secrets"*) is true today, but it
is a property that a future `editor` feature (loading a config from disk, for example)
would quietly invalidate.

Separately, the editor serves the SPA with no `Content-Security-Policy`,
`X-Frame-Options`, or `X-Content-Type-Options` — note the webhook server *does* set
`X-Content-Type-Options` and `Cache-Control` (`server.go:230-231`), so the editor is
the inconsistent one.

**Fix:** replace `*` with a configurable `editor.allowed_origins` list defaulting to
same-origin only; add CSP/frame-options/nosniff to the editor's responses.

### P2-4. Config drafts persisted unencrypted in browser IndexedDB

`web/package.json` depends on `dexie`, so the config UI persists drafts client-side.
The intended workflow uses `${ENV}` references rather than literal secrets, but in
practice operators paste real values while iterating. Unencrypted browser-local
persistence of an administrative config, on a shared or unmanaged workstation, with no
explicit clear action, is a finding.

**Fix:** add a visible "clear local drafts" control, warn in the UI when a draft
contains a literal-looking secret rather than a `${VAR}` reference, and document the
storage behaviour.

### P2-5. `rate_limit.key_by` supports only `"ip"`, and says "MVP" in the error

> **Largely CLOSED; the text below is stale.** The live `rate_limit` node
> supports `key_by: source_ip` (default) and `key_by: header.<name>`, with an
> unknown value falling back to source IP. The *"only ip is supported for
> MVP"* validator quoted here guards v1 config keys that the strict decoder
> now rejects outright, so that message is unreachable — the word "MVP" cannot
> surface in a production validation error any more. The status table gets
> this right; this paragraph did not. See
> [configuration.md § Trigger, gates, and body](configuration.md#trigger-gates-and-body).

What remains open is the *breadth* of the key: per-partner or per-tenant quotas
keyed on a body field (JSON path) are still not expressible, only headers and
source IP. The filter package already knows how to extract values from JSON
bodies, so the building blocks exist — but note that `rate_limit` consumes
`RequestHead`, which by design carries **no body**, so body-keyed limiting
would require rethinking where the limiter sits in the graph rather than just
widening an enum.

**Fix (remaining):** decide whether body-keyed quotas are wanted at all given the
port ordering; if so, they belong in a post-`read_body` node, not in `rate_limit`.

### P2-6. Readiness AND-aggregation couples unrelated flows (mitigated)

`/ready` reports per flow and returns 200 while at least one flow is ready
(D9). `server.readiness_require_all: true` restores 503-if-any-unready. A
saturated low-priority destination still marks *its* flows unready; it no
longer takes unrelated flows out of rotation by default.

**Fix:** add `critical: true|false` per destination (default `true` to preserve current
behaviour) and exclude non-critical destinations from the readiness join. Optionally
add `server.readiness_mode: all | critical | any`.

### P2-7. No fuzzing on untrusted-input parsers; no coverage gate

The following functions parse attacker-controlled input and have no fuzz coverage:

- `parseHopCount` (`server.go:767`)
- `rightmostUntrustedForwardedIP` (`server.go:832`) — the most security-sensitive
  parser in the codebase
- `isValidTraceparent` / `traceIDFromRequest` (`server.go:939-994`) — see P2-1
- the filter JSON-path evaluator (`internal/filter/filter.go`)
- the HMAC/JWT signature-header parsers (`internal/auth/hmac.go`, `jwt.go`)

These are exactly the surfaces Go's native fuzzing is designed for, and P2-1 is
evidence that hand-written table tests missed a case here.

> **The coverage half is CLOSED.** The original claim — "there is also no coverage
> threshold in CI, so the numbers in §2 can regress silently" — is no longer true.
> `make coverage` runs the short suite with `-covermode=atomic`, prints the
> per-function report, and fails when total statement coverage falls below a
> configured floor. It runs as the required **Coverage** job in
> `reusable-ci.yml`, which also uploads `coverage.out` / `coverage.txt` as
> artifacts. The floor is a single repo-wide number, not per-package.

**Fix (remaining):** add `FuzzXxx` functions for each parser above, run them briefly
in CI and longer nightly. Optionally tighten the coverage floor from repo-wide to
per-package.

### P2-8. No load testing, no published capacity numbers

Nothing in the repository establishes throughput, p99 latency, memory per in-flight
request, or goroutine behaviour under sustained load. [security.md § Availability Model](security.md#availability-model) tells
operators to *"size each destination's `publisher_channels`, per-request
`server.fanout_concurrency`, and global `max_inflight_requests` for expected burst
volume"* without giving any basis for computing those numbers.

Enterprise capacity planning needs data, and a production SLO cannot be committed to
without it.

**Fix:** add a `bench/` harness (k6 or vegeta) with a documented scenario set; publish
a capacity table (RPS vs. p50/p99 vs. memory, per destination type); add a 24-hour soak
test proving no goroutine or memory leak; add Go benchmarks for the hot path
(envelope build, HMAC verify, filter evaluation).

### P2-9. No Prometheus alert rules shipped — **CLOSED**

> **Shipped.** `deploy/alerts.yaml` is a PrometheusRule with **7 alerts**:
> `HookwayPanics`, `HookwayCompensationFailures`,
> `HookwaySecretRefreshFailures`, `HookwayWebhookDrops`,
> `HookwayWebhookDeadLetterFailures`, `HookwayDestinationNotReady`, and
> `HookwayPublishErrorRatio`. `deploy/alerts_test.go` asserts their shape, and
> the required **Observability** CI job renders them through
> `scripts/promrule2groups` and validates them with `promtool check rules`.
> So the rules cannot silently rot into invalid PromQL.

The metric surface is genuinely excellent — **45** metrics, and a Grafana dashboard
with a test guarding it (`grafana/dashboards/`).
[security.md § Production Hardening](security.md#production-hardening) lists in prose
the conditions operators should alert on; `deploy/alerts.yaml` now codifies most
of them.

**Fix (remaining):** the shipped set does not yet cover an auth-failure rate spike,
sustained `hookway_saturated_total`, `hookway_webhook_queue_depth` near capacity,
`hookway_replay_store_up == 0`, or `hookway_config_load_failures_total > 0`. Note
the last one is of limited value: fatal pre-listener config failures are primarily
visible via structured logs and a supervisor restart, because the process usually
exits before Prometheus can scrape that counter.

### P2-10. No OpenTelemetry export

`traceparent` is propagated end-to-end and correctly — the design work is done.

> **Partially addressed: spans now exist, export still does not.**
> `internal/httpapi/flowserver.go` holds an `otel.Tracer("hookway")` and starts
> a `hookway.node.<type>` span per executed graph node, with
> `hookway.flow` / `hookway.node_id` / `hookway.node_type` /
> `hookway.request_id` / `hookway.event_id` attributes, real start/end
> timestamps, and recorded errors. But they are emitted on the **global**
> tracer provider, which Hookway never sets — so with no provider registered
> they are no-ops. **No exporter ships**: no OTLP config, no
> `otel.SetTracerProvider`, no collector wiring. Register your own provider to
> get anything out. Per `PRODUCT.md`, Hookway must not *claim* OpenTelemetry
> support, and this does not change that. Documented factually in
> [configuration.md § OpenTelemetry spans](configuration.md#opentelemetry-spans).

Hookway is therefore still, by default, a black box in the middle of a
distributed trace it is otherwise faithfully carrying.

**Fix:** add optional OTLP trace export with spans for receive → verify → filter →
capture → publish-per-route, and optional OTLP metrics export alongside the Prometheus
endpoint.

---

## 6. P3 — Lower priority / polish

- **No audit log stream.** Request logs are good, but there is no separate,
  tamper-evident audit channel recording: config loaded (with a hash of the effective
  config), replay invoked (by whom, what, where), and auth-failure patterns. Enterprise
  compliance wants this separable from application logs.
- **No operations runbook.** `docs/` is excellent on *configuration* and thin on
  *operating*. Missing: common failure modes → response, upgrade/rollback procedure,
  version-skew policy, replay-store DR, and what to do when `/ready` flaps.
- **Envelope schema is not published as an artifact.** `schema_version` is a documented
  contract, but consumers get prose. Publish a versioned JSON Schema for the envelope
  so consumers can validate and codegen against it.
- **No multi-tenancy.** One config equals one deployment. Fine for a dedicated service;
  a constraint if Hookway becomes a shared internal platform serving many teams.
- **No FIPS build option.** Some regulated environments require a boringcrypto build.
- **`docs/configuration.md` is over 1,400 lines / ~105 KB.** Excellent content, hard to navigate.
  Split by section (server, security, destinations, endpoints, replay) with an index.
- **No `docs/observability.md`.** The Grafana dashboard exists but has no documented
  provisioning path.
- **No `hookway config diff`.** A dry-run/diff subcommand showing what a config change
  would alter (endpoints added/removed, secrets rotated, destinations changed) would
  pair well with the reload work in P0-5.

---

## 7. Prioritized roadmap

Sequenced so that each phase leaves the system in a shippable state.

Items struck through are **done**; see the status table and the per-item CLOSED
markers above.

### Phase 1 — Unblock adoption *(~1 week)*
1. ~~`LICENSE`~~ **done (MIT)**; `SECURITY.md`, `CONTRIBUTING.md`, `CODEOWNERS`, `CHANGELOG.md` — P0-1
2. ~~Fix the `X-Trace-ID` traceparent bug — P2-1~~ **Done.**
3. ~~Fix the `no_auth` idempotency key — P2-2~~ **Withdrawn**: the contract is
   dedup on `id`; no `idempotency_header` field exists to configure.
4. ~~`ReadHeaderTimeout` on all four servers — part of P1-3~~ **Done** (P1-3a).
5. ~~Add `govulncheck` + `golangci-lint`~~ **Done**; Dependabot still outstanding — P1-6

### Phase 2 — Make deploys safe *(~2 weeks)*
6. Readiness drain on `SIGTERM` — P1-1
7. Helm chart with correct probes, PDB, NetworkPolicy, ServiceMonitor — P1-8
8. ~~Prometheus alert rules — P2-9~~ **Done** (7 alerts in `deploy/alerts.yaml`).
9. Supply chain: SHA-pin actions, SBOM, cosign, multi-arch, digest-pinned bases — P1-7

### Phase 3 — Close the durability hole *(~3 weeks)*
10. ~~Record intended destinations at capture so `pending` events are replayable — P0-2.1~~ **Done.**
11. Automatic re-drive worker from the replay store — P0-2.2
12. Durable or explicitly-contracted async webhook queue — P0-3
13. Versioned migrations + `hookway migrate` subcommand — P1-4

### Phase 4 — Enterprise security & operability *(~4 weeks)*
14. Split admin API keys, key lists for rotation, replay audit log — P0-4
15. `secret_file:` + multi-secret + `SIGHUP` reload — P0-5
16. Shared (Redis) rate-limit and replay-protection stores — P1-2
17. Replay payload redaction, encryption, and filtered purge — P1-5
18. Optional in-process TLS/mTLS — P1-3b

### Phase 5 — Scale & polish *(ongoing)*
19. OTel export — P2-10
20. Load/soak harness and published capacity numbers — P2-8
21. Fuzzing — P2-7 (the ~~coverage gate~~ half is **done**)
22. `key_by` expansion, per-destination readiness criticality, editor CORS/CSP —
    P2-5, P2-6, P2-3
23. Runbook, published envelope JSON Schema, docs split — P3

---

## 8. What is already right

Worth recording explicitly, because these are the things that make the roadmap above
achievable rather than a rewrite:

- **Fail-closed everywhere.** Unknown auth scheme → `RejectAll` (`server.go:105-108`).
  Uncompilable filter chain → `MatchNothing` (`server.go:155-170`). Malformed hop
  header → treated as `0`, which can only *reset* the count, never bypass the limit
  (`server.go:767-776`). These are the right instincts.
- **`X-Forwarded-For` handling is correct**, including the right-to-left walk and the
  fail-toward-the-trusted-side behaviour on a malformed entry (`server.go:832-846`).
  Most implementations get this wrong.
- **Panic recovery at every goroutine boundary** — handler, fan-out worker, readiness
  prober, webhook worker — each with its own metric label.
- **Shutdown ordering is deliberate and documented.** The `defer` LIFO reasoning in
  `main.go:286-407` is the kind of thing that is usually discovered in an incident.
- **Publish is detached from the request context** (`server.go:576`) with an
  independent timeout, and validation enforces `publish_timeout <= shutdown_timeout`
  and `<= write_timeout`. That is a subtle correctness property, enforced.
- **Replay capture is strictly best-effort** and provably cannot affect the webhook
  response.
- **The docs explain trade-offs, not just settings** — the readiness-aggregation
  section, the v1 limitations list, and the IAM note on `on_existing_conflict` are all
  better than most commercial products manage.
- **Destination coverage is broad and real:** RabbitMQ, SQS, SNS, S3, Redis (stream,
  list, pubsub, kv), Postgres, MongoDB, Kafka, Temporal, and outbound webhook — each with an
  integration suite.

The foundation is strong. The work ahead is making it deployable and governable at
enterprise scale, not making it correct.
