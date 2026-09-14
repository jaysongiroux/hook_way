# Chunk 8: Secrets, Observability, Readiness, and Replay

**Status:** not started
**Depends on:** Chunk 6 (Chunk 7 optional)
**Unlocks:** Chunk 9
**Verification gate:** a secret rotates with **no process restart** and no rejected
requests; `/ready` reports per flow; per-node metrics are present with bounded cardinality;
the I9 and I15 conformance tests un-skip and pass.

```bash
go test ./internal/secretref/ ./internal/httpapi/ -v
go test ./internal/conformance/ -v          # I9 and I15 no longer skipped
curl -s localhost:8080/ready | jq           # per-flow, not one global boolean
curl -s localhost:9090/metrics | grep hookway_node_   # per-node metrics exist
make aws-integration-test                    # Secrets Manager via LocalStack
```

**Goal:** Four related pieces of production plumbing — `secret_ref` (D7, closes W3/P0-5),
per-node observability (scalability rule R2), per-flow readiness (D9, fixes W1), and wiring
the replay store into the V2 executor (D10).

## Re-read before starting

- Confirm `graph.RunOptions.OnNode` and `graph.NodeEvent` exist as Chunk 1 defined them.
  Task 2 is entirely built on that callback.
- Confirm `AuthOptions` has `secret_ref` as an alternative to `secret` (Chunk 2 Task 2's
  option table says it should). If it does not, add it here.
- Re-read `v2/03-scalability.md` §R6 before Task 4. The decision is to port `replaystore`
  **unchanged** — 2 writes/event, INSERT then UPDATE. **Do not implement batching or a
  WAL.** That was scoped and deliberately deferred; the reasoning is in §R6.

---

## Task 1: `secret_ref` (D7)

**Files:** `internal/secretref/` — `secretref.go`, `env.go`, `file.go`, `awssm.go`, tests;
modify `internal/flowconfig/node_auth.go`

V1 expands `${ENV}` once at process start (weakness W3, production-readiness P0-5): no
Secrets Manager, no rotation without a restart, and one secret per endpoint so there is no
old/new overlap window.

**Interfaces produced:**
```go
// Resolver returns the currently-valid secrets for a reference. It returns a
// SLICE because rotation needs an overlap window: during rotation both the
// new and previous secret must verify, or rotation is a flag day.
type Resolver interface {
    Secrets(ctx context.Context) ([][]byte, error)
}

type Ref struct {
    Provider string        `yaml:"provider"` // env | file | aws_secrets_manager
    Name     string        `yaml:"name"`
    Refresh  time.Duration `yaml:"refresh"`  // default 15m
    Overlap  time.Duration `yaml:"overlap"`  // how long the previous secret stays valid
}

func New(r Ref, logger *slog.Logger) (Resolver, error)
```

Returning a slice is the whole design. `auth` tries each secret and passes if **any**
verifies, using constant-time comparison for each (I18).

Requirements, from `v2/03-scalability.md` §4:
- **Cached with a background refresh timer. Never fetched per request.** A per-event
  Secrets Manager call will get throttled by AWS and is an outage waiting to happen. Reuse
  the caching pattern from `internal/lookup/cache.go` rather than writing a new one.
- **Fail closed on refresh failure, in the specific sense of:** keep serving the last
  known-good value, emit a metric and a loud log. Do **not** start rejecting everything
  (that turns a Secrets Manager blip into an outage) and do **not** start accepting
  everything (that violates I11).
- `aws_secrets_manager` should mirror universal-gateway's `getLatestSecret` behaviour where
  it makes sense: it lists secrets matching a name pattern, paginates, sorts by
  `LastChangedDate`, and takes the newest. Read
  `../universal-gateway/*/layer_external_services/` for the exact semantics.

- [ ] **Step 1: Write the failing tests**

Cover per provider: resolves a secret; `env` matches V1's `${VAR}` behaviour exactly;
refresh picks up a changed value **without a restart**; during the overlap window **both**
old and new verify; after the overlap only the new one does; a refresh failure keeps
serving the last good value and increments a metric; the resolver is safe under concurrent
reads (`-race`).

The rotation test is the important one and needs to assert *no rejected requests*:

```go
// TestRotationHasNoFlagDay proves the overlap window works: a sender still
// signing with the previous secret must be accepted throughout the overlap,
// so rotation never drops traffic. V1 cannot do this -- one secret per
// endpoint means rotation is a flag day (W3).
func TestRotationHasNoFlagDay(t *testing.T) { /* ... */ }
```

- [ ] **Step 2: Implement, then pass**

- [ ] **Step 3: Add a LocalStack integration test**

Follow the existing pattern in `internal/queue/aws_integration_test.go`; wire it into
`make aws-integration-test`.

- [ ] **Step 4: Commit**

```bash
make test && make aws-integration-test && make lint
git add internal/secretref/ internal/flowconfig/node_auth.go
git commit -m "feat(secretref): env/file/aws_secrets_manager with refresh and rotation overlap"
```

---

## Task 2: Per-node observability (scalability rule R2)

**Files:** modify `internal/metrics/metrics.go`, `internal/httpapi/flowserver.go`;
`internal/metrics/node_test.go`

This is the substitute for n8n's per-node execution records, and it is strictly better
under load: O(1) memory per node *type*, scraped rather than stored. Decision D8 forbids
per-node persistence; this is what replaces it.

Everything hangs off `RunOptions.OnNode`, which Chunk 1 already provides.

Add:
- `hookway_node_total{flow, node_id, node_type, outcome}` — counter
- `hookway_node_duration_seconds{flow, node_id, node_type}` — histogram
- one structured log line per node at debug, correlated by request/trace/event id
- one OTel span per node (V1 already propagates W3C traceparent — see
  `traceparentFromRequest`, `server.go:1079`)

**Cardinality is the thing to get right.** Per scalability rule R3, labels must be bounded
by config, never by data:

- [ ] **Step 1: Write a cardinality guard test**

```go
// TestNodeMetricCardinalityIsBoundedByConfig guards scalability rule R3.
// The lookup node exists to make targets data-dependent, so a rendered
// target as a label would give a 5,000-environment routing table 5,000
// label values per metric. Only config-derived values may be labels.
func TestNodeMetricCardinalityIsBoundedByConfig(t *testing.T) {
	// Run the same flow 100 times with 100 DISTINCT lookup results, then
	// assert the number of distinct label sets did not grow with the
	// distinct values. Rendered targets belong in logs and span attributes.
}
```

Write that test **first**. It is the one that catches the mistake.

- [ ] **Step 2: Implement, then pass**

- [ ] **Step 3: Extend the Grafana dashboard**

`grafana/` has a dashboard with a test guarding it — find the test and extend both. A
per-node latency panel is the payoff for this whole task.

- [ ] **Step 4: Commit**

```bash
make test && make lint
git add internal/metrics/ internal/httpapi/flowserver.go grafana/
git commit -m "feat(metrics): per-node counters, histograms and spans with bounded cardinality"
```

---

## Task 3: Per-flow readiness (D9, fixes W1)

**Files:** modify `internal/httpapi/readiness.go`, `flowserver.go`; tests

V1's weakness W1: readiness AND-aggregates every destination, so **one saturated
destination pulls the whole pod out of load-balancer rotation — including flows that never
route to it.** The gaps doc B2 spells out the consequence: a slow DocMagic would take DigiFi
webhooks down with it. Under a general executor with many flows this gets worse.

Per decision D9:
- **Probe each unique destination once per interval**, not once per flow. Probe load becomes
  `O(unique destinations)`, not `O(flows × destinations)`.
- **Derive per-flow readiness** from the probe results by mapping flow → the destinations
  its `publish` nodes reference.
- `/ready` reports **per flow**.

A decision you need to make and document: what does `/ready` return as its **status code**
when some flows are ready and others are not? Options:
- 200 with a per-flow body, so the pod stays in rotation and healthy flows keep serving.
  **Recommended** — it is the whole point of fixing W1.
- 503 if any flow is unready, preserving V1's behaviour but not fixing anything.

Recommend 200-with-detail, and add a `readiness.require_all: true` option for operators who
want V1's stricter behaviour. Document the choice in `docs/security.md`, which currently
documents the AND-aggregation at line 90.

- [ ] **Step 1: Write the failing tests**

Cover: two flows with disjoint destinations, one destination down → the healthy flow is
reported ready and the response is 200; probe count equals the number of **unique**
destinations, not flows × destinations (assert with a counting fake); `/healthz` remains a
pure liveness check and never reflects destination state (V1 is explicit about this in the
README, and infrastructure relies on it).

- [ ] **Step 2: Implement, then pass**

- [ ] **Step 3: Update the docs**

`README.md`'s HTTP Endpoints section and `docs/security.md:90` both describe the old
aggregation. Fix both.

- [ ] **Step 4: Commit**

```bash
make test && make lint
git add internal/httpapi/readiness.go internal/httpapi/flowserver.go README.md docs/security.md
git commit -m "feat(httpapi): per-flow readiness with per-destination probing"
```

---

## Task 4: Wire the replay store into the V2 executor (D10)

**Files:** modify `internal/httpapi/flowserver.go`; modify `internal/conformance/`

**Port `replaystore` unchanged.** Per decision D10 and `v2/03-scalability.md` §R6: keep
V1's INSERT-before-publish plus outcome-UPDATE-after. 2 round-trips per event, only when
replay is enabled, which it is not by default. **No batching, no WAL, no spool** — that was
scoped and deliberately deferred, and the reasoning is written down in §R6. If you think it
should be optimized, read §R6 first; the answer is "not at this traffic volume."

Two invariants become testable here, and their conformance tests are currently **skipped**
with a pointer to this chunk:

- **I9** — the capture INSERT happens **before** the publish context is created, so publish
  always gets its full `publish_timeout` budget regardless of how long the store write took.
  In V1 this ordering is explicit at `server.go:657` with a long comment.
- **I15** — capture is **strictly best-effort**: its errors and panics never change the
  response.

In V2 the publish context is created per destination node by the executor
(`execution.contextFor`), not once in the handler. So I9 needs re-expressing: the capture
write must complete before the executor is invoked, on its own `replayCaptureTimeout`
budget. Do the capture in `flowserver.go` after the flow has determined its active
destinations but before `flow.Run`. **That may require a two-phase run** — resolve the graph
far enough to know the destinations, capture, then execute. If it does, say so and design it
explicitly rather than quietly dropping I9.

Also carry forward: `replay.skip_unauthenticated` (V1 skips capturing `no_auth` events when
configured — `server.go:395`), and the capture-outcome UPDATE using a **fresh** context
budget rather than the possibly-exhausted publish context (`server.go:716`).

- [ ] **Step 1: Un-skip the I9 and I15 conformance tests and watch them fail**

- [ ] **Step 2: Implement until they pass**

- [ ] **Step 3: Verify the replay API works against V2 flows**

`internal/replayapi` serves list/inspect/redeliver on a separate listener. Redelivery
re-runs a captured event — under V2 that means re-running a flow. Check whether
`replayapi` needs changes to target a flow rather than an endpoint, and test a real
redelivery end to end.

- [ ] **Step 4: Commit**

```bash
make test && make integration-test && make lint && make conformance
git add internal/httpapi/flowserver.go internal/conformance/ internal/replayapi/
git commit -m "feat(httpapi): wire replay capture into V2 flows, preserving I9 and I15"
```

---

## Done when

- [ ] A secret rotates with no restart and **no rejected requests** during the overlap
- [ ] A Secrets Manager refresh failure keeps serving the last good value, with a metric
- [ ] Per-node metrics exist and cardinality does **not** grow with lookup result count
- [ ] `/ready` reports per flow; one bad destination does not sideline unrelated flows
- [ ] Probe count is `O(unique destinations)`, asserted with a counting fake
- [ ] `/healthz` still never reflects destination state
- [ ] I9 and I15 conformance tests are un-skipped and passing
- [ ] Replay redelivery works against a V2 flow, tested end to end
- [ ] `replaystore` is **unchanged** — no batching, no WAL
- [ ] `make test-all && make conformance` green
