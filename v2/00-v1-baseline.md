# V1 Baseline — What Exists, and What V2 Must Not Break

**Source:** `hook_way` @ `242c358` (main)

## 1. The pipeline as it exists

`internal/httpapi/server.go:364` — `handleWebhook` is a single 368-line function
implementing a fixed linear pipeline:

```
match endpoint (literal or {segment} pattern)     server.go:381
  -> method check (POST only)                     server.go:395
  -> loop-hop check (X-Hookway-Hops)              server.go:407
  -> inflight acquire (max_inflight_requests)     server.go:422
  -> source IP resolution + IP allowlist          server.go:436
  -> rate limit (global + per-endpoint)           server.go:445
  -> READ BODY (max_body_bytes)                   server.go:453
  -> auth verify (7 schemes, fail-closed)         server.go:472
  -> replay reservation (idempotency key)         server.go:487
  -> build envelope (parse JSON body)             server.go:503
  -> endpoint filter chain (gates all routes)     server.go:521
  -> route filter chains (selects routes)         server.go:559
  -> resolve variables + render targets (lookups) server.go:592
  -> replay capture INSERT                        server.go:657
  -> fan-out publish (stop_on_failure|skip_failed)server.go:673
  -> replay capture outcome UPDATE                server.go:716
```

This is already a graph. It is simply implicit, fixed-topology, and compiled into one
function. The node types already exist as packages — they are just not uniform:

| Concern | Package | Shape today |
|---|---|---|
| Auth | `internal/auth` | Registry of 7 schemes, `Verifier` interface. **Already node-like.** |
| Filtering | `internal/filter` | `Chain` of `Spec`, compiled at startup |
| Enrichment | `internal/vars` + `internal/lookup` | Variable specs + 3 lookup providers (dynamodb, http, postgres) |
| Destinations | `internal/queue` | 13 types behind a `Publisher` interface. **Already node-like.** |
| Transformation | — | **Does not exist.** |
| Response | — | **Does not exist.** Always a fixed `202` envelope. |

`internal/config/validation.go` is 1,391 lines of endpoint-shape rules. Most of it
exists to check that a fixed-shape config is internally consistent; a type checker over
graph edges subsumes a large fraction of it.

`web/src/graph/` already renders endpoint -> destination on a canvas via `fromConfig.ts`,
but it is a **view** of a fixed shape, not an editable graph.

## 2. Invariant inventory

These are the orderings and behaviours V2 must preserve. Each one is currently enforced
only by the literal statement order inside `handleWebhook`. Every one of them is a way a
free-topology graph could silently regress security or reliability.

### Phase-ordering invariants (what the type lattice must encode)

| ID | Invariant | Why | V1 site |
|---|---|---|---|
| I1 | Loop-hop check runs before body read | A terminal 4xx stops a webhook loop cheaply, without buffering | `server.go:407` |
| I2 | Inflight slot acquired before body read | "a flood cannot exhaust memory by buffering unbounded bodies" | `server.go:422` |
| I3 | IP allowlist runs before body read | Reject unauthorized sources without buffering | `server.go:436` |
| I4 | Rate limit runs before body read | Throttle before paying the buffering cost | `server.go:445` |
| I5 | Auth verify runs before any body-derived work | Nothing may act on an unverified payload | `server.go:472` |
| I6 | Auth verifies **raw bytes**, never a re-serialized body | `docs/security.md:33` — "does not parse, trim, normalize, or re-serialize" | `server.go:472` |
| I7 | Replay reservation happens after auth, before envelope build | An unauthenticated request must not consume an idempotency key | `server.go:487` |
| I8 | Filters run before variable resolution | "a filtered request is never a variable error" | `server.go:521` |
| I9 | Replay capture INSERT happens before the publish context is created | So publish always gets the full `publish_timeout` budget regardless of store latency | `server.go:657` |

### Execution-semantics invariants (not orderings — executor behaviour)

| ID | Invariant | Why | V1 site |
|---|---|---|---|
| I10 | Publish is detached from the inbound request context via `context.WithoutCancel` | A sender disconnect must not cancel work already committed to delivery | `server.go:665` |
| I11 | Unknown auth scheme fails **closed** (`auth.RejectAll`) | A misconfiguration rejects every request rather than accepting unverified ones | `server.go:126` |
| I12 | A filter chain that fails to compile fails **closed** (match nothing) | Same posture as I11 | `server.go:194` |
| I13 | A broken endpoint still registers its path, returning 500 not 404 | Prevents a config error from looking like an unconfigured endpoint | `server.go:156` |
| I14 | Filtered routes are never counted as delivery failures | Keeps `delivery_failure_policy` semantics intact | `server.go:559` |
| I15 | Replay capture is strictly best-effort; errors and panics never change the response | Observability must not affect delivery | `server.go:657` |
| I16 | Handler panics are recovered and become 500, not a dropped connection | `panic_recovery_test.go` | `server.go:311` |
| I17 | Replay reservation is released on any failure where a retry could still make progress | Otherwise a retry is rejected 409 and the event is lost | `server.go:497` |
| I18 | Timing-safe comparison everywhere in auth (`hmac.Equal` / `subtle.ConstantTimeCompare`) | Gaps doc C3 — the thing V1 does better than universal-gateway | `internal/auth/*` |

### Known V1 weaknesses V2 should NOT inherit

| ID | Weakness | Source |
|---|---|---|
| W1 | Readiness AND-aggregation: one saturated destination pulls the whole pod out of LB rotation, including endpoints that never route to it | gaps doc B2, `docs/security.md:90` |
| W2 | One process, one IAM role = union of all endpoint permissions | gaps doc B1 |
| W3 | Secrets only expanded from `${ENV}` at process start — no rotation without restart | gaps doc B3, production-readiness P0-5 |
| W4 | No in-process TLS | gaps doc B5, production-readiness P1-3 |
| W5 | Replay cache is process-local, so replay protection is per-pod not per-cluster | `server.go:110` |

W1 gets materially worse under a general executor and should be addressed in V2's design
(per-flow readiness rather than global AND). W2 is a deployment-topology answer, not a
code answer. W3/W4/W5 are pre-existing and orthogonal, but W3 is worth reconsidering
since a `secret` becomes a node property in V2.

## 3. What the universal-gateway migration additionally requires

From `docs/universal-gateway-migration-gaps.md`. The four hard blockers are exactly the
things a node graph makes expressible:

| Gap | Need | V2 node that answers it |
|---|---|---|
| A1 | Synchronous upstream response passthrough (4 of 7 handlers) | `http_request` -> `respond` terminal |
| A2 | Dynamic destination resolution from DynamoDB | `lookup` node (exists in V1 as endpoint `variables`) |
| A3 | Payload transformation: changed-field diff, 5-rule XML pipeline | `transform` / code node |
| A4 | SNS/SQS `MessageAttributes` from envelope fields | destination node config |
| A5 | Query-parameter auth | `auth` node with a query source |
| A6 | Arbitrary methods, `{proxy+}` greedy paths, query forwarding | `http_trigger` node config |
| A7 | DigiFi vendor HMAC over a re-serialized body | new auth scheme — **conflicts with I6, needs a spike** |

A7 is the one genuine unknown: DigiFi signs `JSON.stringify(body)`, not the bytes on the
wire. If the vendor canonicalization differs from raw bytes in key order, unicode
escaping, or whitespace, V2 will reject signatures the current gateway accepts. This is
independent of the V2 redesign and should be spiked separately against captured traffic.
