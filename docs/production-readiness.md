# Hookway — Enterprise Production Readiness Assessment

**Date:** 2026-08-26
**Reviewed at commit:** `612e372` (branch `main`, clean tree)
**Scope:** whole repository — Go services, config layer, CI/release pipeline, docs, web UI, deployment story

---

## 1. Executive summary

Hookway is a genuinely well-engineered codebase. It is materially better than most
internal services at this stage: 33.5k lines of Go with a high test-to-source ratio,
93% coverage in the HTTP request path, an unusually disciplined comment culture that
records *why* decisions were made, fail-closed defaults throughout auth and filtering,
panic recovery at every goroutine boundary, correct `X-Forwarded-For` handling,
constant-time secret comparison, bounded shutdown waits with carefully-ordered
`defer` LIFO semantics, and 45 Prometheus metrics with a tested Grafana dashboard.
`go vet` is clean and the full unit suite passes.

**It is not, however, production-ready for an enterprise environment yet.** The gaps
are not code-quality gaps — they are *platform* gaps. In rough order of severity:

1. **Legal/governance:** there is no `LICENSE` file. This alone blocks enterprise adoption.
2. **Durability:** Hookway can lose events. There is no local spool, and the one
   failure mode where replay would save you (total broker outage) is precisely the
   case the replay store cannot cover.
3. **Admin-plane security:** a single static API key guards metrics, config, *and* the
   ability to re-inject arbitrary captured production traffic. No RBAC, no rotation,
   no audit.
4. **Operability:** no config hot reload, no zero-downtime secret rotation, no
   readiness drain on `SIGTERM`, no deployment artifacts (Helm/k8s), no migrations.
5. **Supply chain:** `go vet` is the entire lint gate; no govulncheck, no SBOM, no
   image signing, no dependency scanning, single-arch images.

**Verdict:** ready for internal/low-stakes production behind a trusted network today.
Roughly one focused quarter of work away from enterprise-grade. The architecture is
sound enough that nearly every item below is additive — very little needs rework.

### Scorecard

| Dimension | Grade | Note |
|---|---|---|
| Code quality & design | **A** | Clean seams, fail-closed defaults, excellent inline rationale |
| Test discipline | **B+** | 93% on the hot path; thin on `queue`/`replaystore`; no fuzz, no gate |
| Documentation (user) | **A−** | Exceptional depth; missing runbook/upgrade/DR |
| Observability | **A−** | Great metrics + dashboard; no alert rules, no OTel export |
| Security (data plane) | **A−** | Auth, redaction, loop/replay/rate controls all solid |
| Security (admin plane) | **D** | One shared static key for read *and* replay-injection |
| Durability / DR | **D** | No spool; unreplayable total-outage path; async engine loses queue |
| Operability | **C−** | No reload, no drain, no migrations, no deploy artifacts |
| Supply chain / release | **C** | Trivy only; no SBOM/signing/govulncheck/multi-arch |
| Governance / legal | **F** | No LICENSE, SECURITY.md, CODEOWNERS, or CHANGELOG |

---

## 2. Verification baseline

Everything below was run against the working tree; results are reported as observed.

```
go vet ./...                                    → clean
go test ./... (unit, integration-skipped)       → all 18 packages PASS
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

### P0-1. No `LICENSE` file

The repository ships no license, so by default it is "all rights reserved." No
enterprise legal or OSS-review process will approve deployment of a dependency in
that state, regardless of technical merit. Also absent: `SECURITY.md` (vulnerability
disclosure contact and SLA), `CONTRIBUTING.md`, `CODEOWNERS`, `CHANGELOG.md`.

**Fix:** pick a license (Apache-2.0 is the safest default for enterprise ingestion —
the patent grant matters to legal reviewers, and it is more acceptable than MIT to
corporate OSS committees). Add the four governance files. This is a half-day of work
and it unblocks everything else.

### P0-2. No durable ingest buffer — events can be lost

`docs/security.md:86` states it plainly: *"Hookway does not durably spool requests
locally."* On a broker outage Hookway returns `503` and relies on the sender's retry
policy. That is a reasonable v1 stance, but it is not an enterprise availability
model, because sender retry behaviour is wildly inconsistent — some providers retry
for 72 hours, some retry three times over 15 minutes, some (and most internal
first-party senders) do not retry at all.

The sharper problem is that the replay store *looks* like the mitigation but is not:

- Capture happens (`internal/httpapi/server.go:560-566`), so the event is stored.
- On the `stop_on_failure` full-failure path, `publishRoutesStopOnFailure` bails on
  the first error and returns `nil, nil`, so no delivery outcomes are recorded and
  the event's status stays `pending` (`server.go:588-597`).
- `docs/security.md:127` confirms an event with no recorded delivery outcome
  **cannot be replayed** — `422` on single replay, skipped in bulk.

So the single scenario where you most need replay — every destination down — is
exactly the scenario where replay refuses to run. The DR story has a hole in the
middle of it.

**Fix, in increasing order of effort:**
1. *(cheap, high value)* Record the intended destination set at capture time so a
   `pending` event is replayable. This closes the hole with existing machinery.
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
  (`docs/security.md:108-116`) is the same credential that can replay production
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
- **Provider secret rotation has a hard cutover.** `docs/security.md:143` advises
  "keep old/new overlap short during provider-side rotations," but with a single
  secret field there is no overlap possible at all — during the switch, signatures
  from one side or the other fail with `401`.

**Fix:**
1. `auth.secret_file:` (and `secrets:` as a list) alongside `secret:`. Accept a list
   and try each — this alone makes rotation zero-downtime.
2. `SIGHUP` → re-load, re-validate, and atomically swap verifiers/filters/limiters.
   The `Server` already builds all of these once in `NewServerWithMetrics`; the
   refactor is to move them behind an `atomic.Pointer` swap rather than plain fields.
3. Reject a reload that fails validation and keep serving the old config, with a
   `hookway_config_reload_total{result}` metric.

---

## 4. P1 — High priority

### P1-1. No readiness drain on `SIGTERM`

`cmd/hookway/main.go:411-427`: on receiving a signal, the process logs and goes
straight into `httpapi.Shutdown`. Meanwhile `handleReady` (`internal/httpapi/server.go:309`)
keeps serving `ready` from the prober cache right up until the listener closes.

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
maps. `docs/security.md:57,61` documents this honestly, but the implications are worth
stating in enterprise terms:

- With N replicas, the effective rate limit is **N× the configured value**. A limit
  advertised to a partner as "1000 rpm" is actually 6000 rpm on a 6-pod deployment.
- **Replay protection is trivially bypassable.** It is a *security control*, and its
  guarantee is "per pod." An attacker replaying a captured signed request just needs
  the load balancer to pick a different pod — which it will, by design. "Use sticky
  routing by signature" (`docs/security.md:61`) is not a realistic operational ask.
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
anywhere (verified by grep for `ListenAndServeTLS`). `docs/security.md:132` correctly
advises terminating at a load balancer, and for many deployments that is sufficient —
but enterprise environments frequently mandate in-cluster encryption end to end (PCI
segmentation, zero-trust networking, FedRAMP), and mTLS is the natural authentication
mechanism for the admin/replay listeners.

Separately: the main webhook server and the metrics server set `ReadTimeout` but not
`ReadHeaderTimeout` — note that the replay and editor servers *do* set it
(`internal/replayapi/api.go:100`, `internal/editor/editor.go:49`). `ReadTimeout` does
bound the whole request so the Slowloris exposure is limited, but the inconsistency is
what a scanner flags (gosec G112) and what a reviewer will question.

**Fix:** add `server.tls: {cert_file, key_file, client_ca_file, min_version}` and the
same for the metrics/replay listeners; set `ReadHeaderTimeout` on all four servers.

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
body, headers, and source IP. Webhook payloads routinely carry PII, and in Hometap's
domain plausibly carry financial and identity data. Current state:

- No encryption at rest beyond whatever the database provides (which is usually
  full-disk — not field-level, and not protecting against a `SELECT`).
- No configurable payload redaction *before* capture. Header redaction exists and is
  good (`docs/security.md:63-76`), but the body is stored verbatim.
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

### P1-6. `make lint` is only `go vet`

```make
lint:
	$(GO) vet ./...
```

That is the entire static-analysis gate, and it is what CI runs
(`.github/workflows/release.yml:62-63`). Missing:

- **`govulncheck`** — notable because the Dockerfile comment (`Dockerfile:1-7`) shows
  govulncheck *is* being run manually during Go version bumps. It should be a CI gate,
  not a habit.
- **golangci-lint** (staticcheck, errcheck, ineffassign, unconvert, revive) —
  `errcheck` in particular matters here, since the codebase deliberately uses `_ =`
  for ignored errors in places and a linter would force those to stay intentional.
- **gosec** — SAST for the Go code.
- **Dependency scanning of `go.mod` itself.** Trivy scans the built image, but the
  image is `FROM scratch` containing a single static binary, so the scan sees only the
  Go stdlib version and module metadata stamped into the binary. There is no OS
  package surface. This is a much narrower check than it appears.
- **`web/` has no linting at all** — no ESLint config, no `npm audit` in CI, despite
  22 npm dependencies including a React + Vite tree.
- **No Dependabot or Renovate config.** `.github/` contains only `workflows/`.

**Fix:** add `golangci-lint` with a checked-in `.golangci.yml`, add `govulncheck` and
`npm audit --audit-level=high` as CI steps, add `.github/dependabot.yml` covering
`gomod`, `npm`, `docker`, and `github-actions`.

### P1-7. Supply-chain gaps in the release pipeline

Reviewing `.github/workflows/release.yml`:

| Gap | Detail | Impact |
|---|---|---|
| Floating action tags | `actions/checkout@v4`, `docker/login-action@v3`, etc. | A compromised action tag can exfiltrate `DOCKERHUB_TOKEN`. Enterprise policy usually mandates SHA pinning. |
| No SBOM | — | Cannot answer "are we affected by CVE-X" without rebuilding. Increasingly a procurement requirement. |
| No signing / provenance | No cosign, no SLSA, no GitHub attestations | Consumers cannot verify image authenticity. |
| Single architecture | No `platforms:` on `build-push-action` | Blocks Graviton/arm64 fleets — a real cost and availability constraint. |
| Base image by tag | `FROM golang:1.25.12-alpine` | Not reproducible; pin by digest. |
| No released binaries | Docker Hub image only | Blocks VM/bare-metal deployments; no checksums or signatures. |
| No CodeQL/SAST | — | No security scanning of first-party code. |
| One monolithic job | 11 sequential test steps in `release-check` | Slow feedback; one flaky container fails everything. Should be a matrix. |
| No coverage gate | — | Coverage can silently regress. |

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
`docs/security.md:135`. Every team will get some of this wrong.

**Fix:** ship a Helm chart with sane, secure defaults encoding all of the above, plus
a `NetworkPolicy` template, a `ServiceMonitor` for Prometheus Operator, and a
`PrometheusRule` (see P2-9). This is the single highest-leverage artifact for adoption.

---

## 5. P2 — Medium priority

### P2-1. Confirmed bug: unvalidated `X-Trace-ID` produces a malformed W3C traceparent

`internal/httpapi/server.go:939-971`. The inbound `Traceparent` trace-id segment is
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

The at-least-once consumer contract documented at `docs/security.md:80` — "consumers
dedupe by `idempotency_key`" — therefore silently does not apply to `no_auth`
endpoints, which is where sender-side retry behaviour is *least* predictable.

**Fix:** fall back to a raw-body SHA-256 for `no_auth` exactly as `header_exact` does,
or make config validation reject `no_auth` without an `idempotency_header`. The former
is more forgiving; the latter is more explicit.

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

`internal/config/validation.go:347-348` and `:539-540` reject anything but `"ip"` with
the message *"only ip is supported for MVP"*. Two issues: the word "MVP" surfacing in a
production validation error reads as unfinished, and the limitation itself blocks a
common enterprise requirement — per-partner or per-tenant quotas keyed on endpoint,
API key, or a body/header field. The field is already plumbed through config and the
filter package already knows how to extract values from headers and JSON bodies, so
the building blocks exist.

**Fix:** support `key_by: ip | endpoint | header:<name> | body:<jsonpath>`; reword the
error regardless.

### P2-6. Readiness AND-aggregation couples unrelated endpoints

`internal/httpapi/readiness.go:129-158` joins per-destination readiness with AND
semantics. `docs/security.md:90` documents the trade-off thoroughly and the reasoning
is defensible — but at enterprise scale, one saturated low-priority destination (an
audit sink at 90% queue depth) pulling the pod out of rotation for *every* endpoint,
including endpoints that never route to it, is an availability incident waiting to
happen. The blast radius is disproportionate to the trigger.

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
evidence that hand-written table tests missed a case here. There is also no coverage
threshold in CI, so the numbers in §2 can regress silently.

**Fix:** add `FuzzXxx` functions for each, run them briefly in CI and longer nightly;
add a coverage floor per package.

### P2-8. No load testing, no published capacity numbers

Nothing in the repository establishes throughput, p99 latency, memory per in-flight
request, or goroutine behaviour under sustained load. `docs/security.md:86` tells
operators to *"size each destination's `publisher_channels`, per-request
`server.fanout_concurrency`, and global `max_inflight_requests` for expected burst
volume"* without giving any basis for computing those numbers.

Enterprise capacity planning needs data, and a production SLO cannot be committed to
without it.

**Fix:** add a `bench/` harness (k6 or vegeta) with a documented scenario set; publish
a capacity table (RPS vs. p50/p99 vs. memory, per destination type); add a 24-hour soak
test proving no goroutine or memory leak; add Go benchmarks for the hot path
(envelope build, HMAC verify, filter evaluation).

### P2-9. No Prometheus alert rules shipped

The metric surface is genuinely excellent — 41 metrics, and a Grafana dashboard with a
test guarding it (`grafana/dashboards/hookway_overview_test.go`). But
`docs/security.md:141` lists the conditions operators should alert on in prose, and
nothing codifies them.

**Fix:** ship `deploy/alerts.yaml` (PrometheusRule) covering, at minimum: auth failure
rate spike, sustained `hookway_saturated_total`, publish failure ratio,
`hookway_destination_ready == 0`, `hookway_webhook_queue_depth` near capacity,
`hookway_webhook_dropped_total > 0`, `hookway_panics_total > 0`,
`hookway_replay_store_up == 0`, and `hookway_config_load_failures_total > 0`.

### P2-10. No OpenTelemetry export

`traceparent` is propagated end-to-end and correctly — the design work is done. But
OTel appears in `go.mod:106-110` only as an indirect dependency; there is no tracer, no
spans, no OTLP exporter. Hookway is a black box in the middle of a distributed trace it
is otherwise faithfully carrying.

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
- **`docs/configuration.md` is 990 lines / 78 KB.** Excellent content, hard to navigate.
  Split by section (server, security, destinations, endpoints, replay) with an index.
- **No `docs/observability.md`.** The Grafana dashboard exists but has no documented
  provisioning path.
- **No `hookway config diff`.** A dry-run/diff subcommand showing what a config change
  would alter (endpoints added/removed, secrets rotated, destinations changed) would
  pair well with the reload work in P0-5.

---

## 7. Prioritized roadmap

Sequenced so that each phase leaves the system in a shippable state.

### Phase 1 — Unblock adoption *(~1 week)*
1. `LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODEOWNERS`, `CHANGELOG.md` — P0-1
2. Fix the `X-Trace-ID` traceparent bug — P2-1
3. Fix the `no_auth` idempotency key — P2-2
4. `ReadHeaderTimeout` on all four servers — part of P1-3
5. Add `govulncheck` + `golangci-lint` + Dependabot to CI — P1-6

### Phase 2 — Make deploys safe *(~2 weeks)*
6. Readiness drain on `SIGTERM` — P1-1
7. Helm chart with correct probes, PDB, NetworkPolicy, ServiceMonitor — P1-8
8. Prometheus alert rules — P2-9
9. Supply chain: SHA-pin actions, SBOM, cosign, multi-arch, digest-pinned bases — P1-7

### Phase 3 — Close the durability hole *(~3 weeks)*
10. Record intended destinations at capture so `pending` events are replayable — P0-2.1
11. Automatic re-drive worker from the replay store — P0-2.2
12. Durable or explicitly-contracted async webhook queue — P0-3
13. Versioned migrations + `hookway migrate` subcommand — P1-4

### Phase 4 — Enterprise security & operability *(~4 weeks)*
14. Split admin API keys, key lists for rotation, replay audit log — P0-4
15. `secret_file:` + multi-secret + `SIGHUP` reload — P0-5
16. Shared (Redis) rate-limit and replay-protection stores — P1-2
17. Replay payload redaction, encryption, and filtered purge — P1-5
18. Optional in-process TLS/mTLS — P1-3

### Phase 5 — Scale & polish *(ongoing)*
19. OTel export — P2-10
20. Load/soak harness and published capacity numbers — P2-8
21. Fuzzing + coverage gates — P2-7
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
