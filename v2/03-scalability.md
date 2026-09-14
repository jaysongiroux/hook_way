# V2 Scalability — Avoiding the n8n / Temporal Write Amplification Trap

**Constraint (from D1, sharpened):** Hookway must scale horizontally on compute without
forcing the operator to scale a database early. This is the single most common complaint
about n8n and Temporal, and it is an architectural property, not a tuning exercise.

## 1. The trap, quantified

For a flow of `K` nodes, per logical event:

| System | Writes per event | Why |
|---|---|---|
| n8n | **O(K)** | One execution row plus a row per node per run, plus node input/output data blobs |
| Temporal | **O(K)** | Several history events per activity (scheduled / started / completed), plus visibility records |
| **Hookway V1 today** | **2, or 0** | 2 round-trips when the replay store is on; **zero** when it is off. Independent of node count. |
| **Hookway V2 target** | **≤2, or 0** | Must stay independent of `K`. Non-negotiable. |

V1 is already in the right place. The V2 risk is not failing to optimize — it is
**regressing** by adding the per-node execution records that make a node-based system feel
observable. So Q5 is answered:

> **Q5 — DECIDED: no per-node checkpointing, no per-node persistence.** Execution state
> lives in memory for the life of the request. The graph is a DAG evaluated in-process,
> not a durable workflow.

The consequence is honest and must be documented: **V2 flows are not resumable.** A pod
that dies mid-flow loses in-flight work, exactly as V1 does, and recovery is the sender's
retry (which is why returning 503 on failure — gaps doc C1 — matters so much). If durable
resumption is ever needed, that is what the existing `temporal` destination node is for:
hand the event to a workflow engine built for it rather than becoming one.

## 2. Design rules

### R1 — Zero-I/O default

A flow with no `lookup`, no replay store, and no cluster-scoped guard performs **zero**
database or control-plane I/O per event, beyond the destination publish itself.

*Enforce with a test*, not a comment: build a representative flow, run it with store and
lookup fakes that `panic()` on any call, assert it serves traffic.

### R2 — Per-node observability goes to metrics, logs, and spans. Never to rows.

This is the direct substitute for n8n's execution records, and it is strictly better under
load: cost is O(1) memory per node *type* and is scraped, not stored per event.

- Prometheus counter + duration histogram per node type and outcome
- one structured log line per node at debug, correlated by request/trace/event id
- one OTel span per node (V1 already propagates W3C traceparent end to end)

You get "which node failed and how long did it take" without a single write.

### R3 — Metric cardinality is bounded by config, never by data

V1 gets this right and it is easy to break. `EndpointConfig.TargetLabel()`
(`internal/config/endpoints.go:738`) returns the **configured** target string — for a
templated target that is `"{route}-applicationServiceQueue"`, the template, not the
rendered value.

**V2 must keep labelling with the template.** Label with the rendered target and a
DynamoDB-driven routing table with 5,000 environments produces 5,000 label values per
metric. The whole point of the `lookup` node is that the target is data-dependent, so this
is a live risk, not a theoretical one.

Cardinality budget: `flow` x `node_id` x `outcome`. All three are config-bounded.
The rendered target belongs in logs and span attributes, where high cardinality is free.

### R4 — Reads are cached, single-flighted, and negatively cached

Already built: `internal/lookup/cache.go` has TTL, bounded entries (10k default),
`singleflight` so concurrent first lookups of one key make one provider call, and optional
negative caching. Reuse it verbatim.

Extend the same wrapper to Q8's `secret_ref` (§4) — a Secrets Manager read per event would
get throttled by AWS long before Hookway broke a sweat.

Steady-state read load is therefore `unique_keys / TTL`, **not** `events/sec`. Worth
stating in the docs as the actual capacity formula.

### R5 — No shared state on the hot path

`replay_guard` and `rate_limit` both need cluster-wide state to be strictly correct, and
both are in-process today (W5 — V1's replay protection is per-pod, so N pods means N
allowed replays).

**Decision (D11): pod scope only.** No `cluster` option ships in V2.

| | Writes/event | Guarantee | In V2? |
|---|---|---|---|
| `pod` (in-process) | 0 | Approximate. N pods = N x the limit. | **yes** |
| `cluster` (Redis) | 1 op | Exact. | no — deferred |

Rationale: it keeps R1's zero-I/O property absolute and keeps Redis off the hot path.
Rate limiting is abuse mitigation, where approximate is genuinely fine. Replay protection
is a correctness guarantee, so this one is a real accepted gap rather than a free
simplification — W5 carries forward from V1 and must be documented, not implied.

If exactness is needed later, the `scope` field is the shape to add, and consumers already
dedupe on `idempotency_key` as the documented contract (V1 relies on this for retry
semantics under both delivery failure policies), so a duplicate delivery is a handled case
rather than a corruption.

### R6 — Replay capture: port V1's scheme unchanged. Do not optimize it yet.

V1 does INSERT-before-publish then UPDATE-after (`store.go:294`, `store.go:355`) — 2
round-trips per event, and **only when the replay store is enabled**, which it is not by
default.

**Decision: port `replaystore` to V2 as-is.** No batching, no WAL, no disk spool.

This was scoped in detail and then deliberately rejected, so the reasoning is recorded
rather than the design:

- **It is not an architectural property.** Batching lives entirely behind `replaystore`'s
  existing interface. It is a constant-factor change (O(2) -> O(0.01)), not a
  complexity-class change. The complexity-class problem is per-node persistence, and D8
  already settles that. Conflating the two is what made this look urgent.
- **The traffic is not there.** Per the gaps doc §5, sandbox traffic is bursty and
  low-volume, staging and production deploys are commented out, and there is no production
  baseline. 2 writes/event at a few hundred events/sec is a few hundred trivial
  parameterized INSERTs — unremarkable for a small Postgres. Batching starts paying above
  roughly 1k events/sec sustained.
- **The replay store is not the durability mechanism.** Returning 503 so the sender
  retries is (gaps doc C1, the strongest single argument for the whole migration). The
  store is an operator convenience layered on top of that, not the safety net. Optimizing
  it at the cost of new failure modes inverts those priorities.
- **A WAL would make Hookway stateful.** `Dockerfile:23` is `FROM scratch` with
  `USER 65532:65532` and no writable path. A durable local log needs a PVC or
  StatefulSet — ordinal pod identity, slower rollouts, a volume per replica — which is in
  direct tension with D1. An `emptyDir` instead protects against the rare failure (panic)
  and not the common one (drain, deploy, eviction). And the crash-loss problem a WAL
  solves only exists if an in-memory buffer is introduced in the first place.

#### Deferred: the batching design, and its trigger

Revisit **when sustained throughput exceeds ~1k events/sec, or when the replay store shows
up as a measured bottleneck** — not before. The pre-thought design:

> One write after the outcome is known. Successes buffer in memory and flush as a
> multi-row INSERT; failures write through synchronously. Safe because capture is
> best-effort (I15) and the events worth replaying are the failures, so a crash loses
> observability of already-succeeded deliveries, never an event needing redelivery.
> Requires: byte-bounded buffer with a drop counter, flush on shutdown, and upsert on
> event id when a failure arrives for an event still buffered.

#### The answer for durability-critical setups

Do not build audit durability into the replay store. **Publish to a durable destination
instead** — S3 or Kafka, via a normal destination node. Those are purpose-built for
durable append, already implemented among the 13 destination types, and already covered by
`delivery_failure_policy` and the readiness prober.

> The replay store serves the replay UI. If you need a durable audit log, publish to one.

### R7 — Readiness probes are per unique destination, not per flow

V1's prober plus its global AND-aggregation is weakness W1: one saturated destination
pulls the whole pod out of LB rotation, including flows that never touch it. Under a
general executor with many flows this gets worse, and naive per-flow probing makes probe
load `O(flows x destinations)`.

> **Q6 — DECIDED: probe each unique destination once per interval; derive per-flow
> readiness from that.** Probe load becomes `O(unique destinations)`, and `/ready` reports
> per-flow so a slow DocMagic no longer takes DigiFi webhooks down with it.

## 3. The resulting per-event budget

Default flow (no lookup, no store, pod-scoped guards):

```
reads:  0
writes: 0
publish: 1 per destination
```

Fully-featured flow (lookup + replay store enabled):

```
reads:  1 lookup per unique key per TTL window  (amortized ~0 at steady state)
writes: 2 Postgres round-trips (capture INSERT + outcome UPDATE)
publish: 1 per destination
```

Note there is no Redis line: D11 keeps `rate_limit` and `replay_guard` pod-scoped and
in-process, so they contribute zero I/O.

Both are **independent of node count**, which is the property that matters.

### `hookway explain --cost`

Because the write budget is computable from the graph at load time, the CLI can print it.
This makes the cost of adding a node visible when the config is written rather than when
the database saturates, and gives the policy linter (§8 of `02-design.md`) something
concrete to warn on.

## 4. Q8 — `secret_ref`

> **Q8 — DECIDED: in scope.** A secret becomes a node property in V2, so the indirection
> belongs here rather than bolted on later.

```yaml
- {id: auth, type: auth, scheme: hmac_sha256, secret_ref: {provider: aws_secrets_manager,
                                                           name: prod-lambdas, refresh: 15m}}
```

Providers: `env` (V1 behaviour, the default), `file`, `aws_secrets_manager`.

This also closes W3 / production-readiness P0-5. Requirements:

- **Cached with refresh on a timer, never fetched per request** (R4). A per-event
  Secrets Manager call is an outage waiting to happen.
- **Rotation overlap:** accept *both* the current and previous secret during a
  configurable window, so rotation is not a flag day. V1 supports one secret per endpoint,
  so there is no overlap window today — this is a real capability gain, and it is what
  universal-gateway's `getLatestSecret` approximates by sorting on `LastChangedDate`.
- **Fail closed on refresh failure:** keep serving the last known-good value and emit a
  metric plus a loud log. Do not fall back to rejecting everything (that turns a Secrets
  Manager blip into an outage), and do not fall back to accepting everything (I11).

## 5. What this costs us

Stating the tradeoffs plainly, since every rule above buys throughput with a guarantee:

1. **No flow resumption.** A pod restart loses in-flight events; recovery is sender retry.
2. **Approximate rate limiting and replay protection, with no exact option.** D11 ships
   pod scope only, so N pods means N x the limit and replay protection is per-pod (W5
   persists from V1). Acceptable for abuse mitigation; documented, not silent.
3. **No per-event execution history to click through.** Debugging is metrics, logs, and
   traces rather than an n8n-style run inspector. This is the deliberate trade for not
   making the operator scale Postgres.
4. **The replay store still costs 2 writes/event when enabled** (R6). Known, measured,
   and cheap to improve later behind an unchanged interface.
