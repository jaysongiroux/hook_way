# Chunk 4: Gates, Processing Nodes, and the Invariant Conformance Suite

**Status:** implemented
**Depends on:** Chunk 3 (`a468a52`)
**Unlocks:** Chunk 5
**Verification gate:** the **I1–I18 conformance suite** passes. Every gate node
demonstrably rejects what it should, at the phase it should.

```bash
go test ./internal/conformance/ -v          # every invariant, named
go test -race ./internal/flowconfig/ ./internal/httpapi/
make test
```

**Goal:** Implement the remaining pre-body gates (`loop_guard`, `ip_allowlist`,
`rate_limit`), the post-auth processing nodes (`replay_guard`, `filter`, `lookup`,
`switch`), and — most importantly — **build the conformance suite that proves invariants
I1–I18 still hold.**

## Why the conformance suite lives here

Decision D4 was a clean break with no config converter, which orphaned V1's ~80 test files.
Those tests were the only thing holding invariants I1–I18 in place. Chunk 4 is where the
invariant-bearing nodes arrive, so it is where the replacement net must be built.

**This is the single most skippable and most regretted item in the whole plan.** If time
pressure appears, cut a node type, not the suite.

## Re-read before starting

- Confirm `graph.NodeError` exists from Chunk 3 Task 6 with the `Status` field; the gates
  all return it.
- Confirm `flowconfig.WithBodyReader` is still how the body reaches `read_body` — the
  conformance tests for I1–I4 assert the body is *never read* when a gate rejects, and they
  do that by giving the context a reader that fails the test if read.
- Confirm Chunk 1 shipped `graph.Compensator`, `graph.Compensation`,
  `graph.NeedsCompensation`, and `graph.RunCompensations`, and that `Flow.Run` returns a
  **populated** `Terminal` even on error. Task 2 depends on all of it.

---

## Task 1: Pre-body gates — `loop_guard`, `ip_allowlist`, `rate_limit`

**Files:** `internal/flowconfig/node_gates.go`, `node_gates_test.go`

All three are `RequestHead -> RequestHead`. Wrap existing code:

| Node | Wraps | V1 site |
|---|---|---|
| `loop_guard` | `parseHopCount` + the hop comparison | `server.go:407` |
| `ip_allowlist` | `Server.ipAllowed` | `server.go:1165` |
| `rate_limit` | `ratelimit.New(...).Allow(key, now)` | `internal/ratelimit/limiter.go` |

Per decision **D11**, `rate_limit` is **pod-scoped only**. Do not add a Redis or
`scope: cluster` option. The approximation (N pods means N× the limit) is deliberate and
documented; adding shared state would put a network round-trip on the hot path and break
scalability rule R1.

- [ ] **Step 1: Write the failing tests**

Per node: passes a compliant request through unchanged; rejects a violating one with the
right status (`loop_guard` 400, `ip_allowlist` 403, `rate_limit` 429); the returned error
is a `graph.NodeError` carrying that status.

`rate_limit` also needs: `key_by` defaults to source IP; the burst allowance behaves; two
different source IPs get independent buckets.

- [ ] **Step 2: Confirm failure, implement, confirm pass**

One `ratelimit.Limiter` per node instance, constructed once in `New` and shared across
requests — `graph.Node` implementations must be concurrency-safe (see the doc comment on
`graph.Node`).

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_gates.go internal/flowconfig/node_gates_test.go
git commit -m "feat(flowconfig): loop_guard, ip_allowlist and rate_limit gate nodes"
```

---

## Task 2: `replay_guard`

**Files:** `internal/flowconfig/node_replay_guard.go`, `node_replay_guard_test.go`

`Verified -> Verified`. Wraps `replay.NewCache(ttl, maxEntries)` (`internal/replay`).

**Interfaces produced:**
```go
type replayGuardNode struct {
    id    string
    opts  ReplayGuardOptions
    cache *replay.Cache   // one per node, shared across requests
}

func (n *replayGuardNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error)
func (n *replayGuardNode) Compensate(ctx context.Context, in graph.Payload, reason error) error
```

### The mechanism is decided: implement `graph.Compensator`

Chunk 1 provides `graph.Compensator`, `graph.Compensation`, `graph.NeedsCompensation`, and
`graph.RunCompensations`. Read the "Compensation — how invariant I17 is preserved" section
in `01-graph-core.md` Task 7 before writing any code here. **Do not invent a second
mechanism** — no context accumulator, no callback slice on the payload.

Three rules that make this correct, each of which is easy to get wrong:

**1. Recompute the key from `in`. Never store it on the node.**
One node instance is shared across every request and fan-out runs siblings in parallel, so
a `reservedKey` field is a data race. The key is a pure function of the headers and raw
body (V1's `replayReservationKey`, `server.go:1021`), and `graph.Verified` carries both via
`in.(graph.Verified).Raw`. So:

```go
func (n *replayGuardNode) Compensate(_ context.Context, in graph.Payload, _ error) error {
	v, ok := in.(graph.Verified)
	if !ok {
		// Defensive: the port signature guarantees Verified, so this is a
		// programming error rather than a runtime condition.
		return fmt.Errorf("replay_guard: compensate got %T, want graph.Verified", in)
	}
	n.cache.Release(n.key(v))
	return nil
}
```

Factor `n.key(v graph.Verified) string` out and have **both** `Run` and `Compensate` call
it. If they can compute different keys, compensation releases the wrong reservation — write
a test asserting they agree.

**2. The caller decides, not the node.** `flowserver.go` calls
`graph.NeedsCompensation(term, err)` and then `graph.RunCompensations(...)`. The node never
decides; it cannot see the outcome.

**3. `Compensate` runs on a detached context.** `graph.RunCompensations` already handles
this. Do not "fix" it to use the request context: a client disconnect is *exactly* when a
publish fails, and skipping the release there leaks the reservation until TTL, so the
sender's retry gets a 409 instead of a redelivery.

### Wiring in `flowserver.go`

Add after `flow.Run` returns, before the response is written:

```go
term, runErr := flow.Run(ctx, head, runOpts)

if graph.NeedsCompensation(term, runErr) {
    if cErr := graph.RunCompensations(r.Context(), term, runErr, s.cfg.Server.PublishTimeout); cErr != nil {
        // Best-effort: the response has already been decided. A failed
        // release means the reservation stays until TTL, so it must be
        // loudly visible.
        s.logger.Error("replay reservation release failed; duplicate suppression "+
            "may reject a legitimate retry until the TTL expires",
            "error", cErr, "flow", flowName,
            "request_id", requestIDFromContext(r.Context()))
        s.metrics.CompensationFailed(flowName)
    }
}
```

Add the `CompensationFailed` counter to `internal/metrics`. A silent release failure is
indistinguishable from correct operation until a sender complains about 409s.

### Why not `err != nil`

`graph.NeedsCompensation` is `err != nil || any delivery failed`, and the second clause is
load-bearing. Under `skip_failed` a partial failure returns **202** to the sender while some
destinations still need that retry — V1 releases there (`server.go:344`). Meanwhile a
filter silent drop returns success with zero deliveries and must **keep** the reservation,
because the delivery was consumed (`server.go:181`). A naive "compensate on error" gets the
first case wrong; "compensate on anything but full success" gets the second wrong.

- [ ] **Step 1: Write the failing tests**

```go
// TestReplayGuardKeyIsStableAcrossRunAndCompensate is the test that stops the
// worst failure mode: if Run and Compensate derive different keys,
// compensation releases a reservation that was never taken and the real one
// leaks until TTL.
func TestReplayGuardKeyIsStableAcrossRunAndCompensate(t *testing.T) { /* ... */ }

// TestReplayGuardRejectsDuplicateWithinTTL
// TestReplayGuardAcceptsAfterTTL
// TestReplayGuardCompensateReleasesReservation:
//   reserve -> compensate -> an identical request is accepted, not 409'd.
// TestReplayGuardConcurrentRequestsDoNotRace: run under -race with fan-out.
```

Then the two end-to-end cases in `internal/httpapi/flowserver_test.go`, which are the ones
that actually prove I17:

```go
// TestPublishFailureReleasesReservation: a request whose publish fails must
// be accepted again immediately. V1 does this at server.go:498. Without it,
// the sender's retry -- which is the entire recovery mechanism for a publish
// failure -- is rejected 409 and the event is lost.
func TestPublishFailureReleasesReservation(t *testing.T) {
	srv := newTestFlowServer(t, withReplayGuard(5*time.Minute), withPublishError(errBrokerDown))
	if rec := srv.post(t, "/webhooks/github", validBody, validSig); rec.Code != 503 {
		t.Fatalf("first attempt = %d, want 503", rec.Code)
	}
	srv.clearPublishError()
	rec := srv.post(t, "/webhooks/github", validBody, validSig)
	if rec.Code == http.StatusConflict {
		t.Fatal("retry after a publish failure was rejected 409: the reservation was not " +
			"released, so the only recovery path for a failed publish is closed (I17)")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry = %d, want 202", rec.Code)
	}
}

// TestFilterSilentDropKeepsReservation is the other half, and the one a
// naive implementation breaks. A silently-dropped event WAS consumed, so an
// identical resend must be treated as a replay (server.go:181).
func TestFilterSilentDropKeepsReservation(t *testing.T) {
	srv := newTestFlowServer(t, withReplayGuard(5*time.Minute), withFilterThatDropsEverything())
	if rec := srv.post(t, "/webhooks/github", validBody, validSig); rec.Code != 202 {
		t.Fatalf("first attempt = %d, want 202 (silent drop looks like success)", rec.Code)
	}
	if rec := srv.post(t, "/webhooks/github", validBody, validSig); rec.Code != http.StatusConflict {
		t.Fatalf("resend = %d, want 409: a consumed delivery must keep its reservation", rec.Code)
	}
}
```

- [ ] **Step 2: Confirm every test fails for the right reason, then implement, then pass**

Run under `-race`: `go test -race ./internal/flowconfig/ ./internal/httpapi/`

- [ ] **Step 3: Note the scope limitation in the docs**

The cache is process-local, so replay protection is **per pod**, not per cluster —
weakness W5, carried forward deliberately by decision D11 (no shared state on the hot path).
N pods means N allowed replays. Document it in `docs/configuration.md` next to
`replay_guard`, and say plainly that consumers should dedupe on `idempotency_key`, which is
already the documented contract V1 relies on for retry semantics.

- [ ] **Step 4: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_replay_guard.go internal/flowconfig/node_replay_guard_test.go \
        internal/httpapi/flowserver.go internal/httpapi/flowserver_test.go internal/metrics/
git commit -m "feat(flowconfig): replay_guard node with compensation-based reservation release"
```

---

## Task 3: `filter`

**Files:** `internal/flowconfig/node_filter.go`, `node_filter_test.go`

`Envelope -> Envelope`. Wraps `filter.Chain` / `filter.Spec` (`internal/filter`).

V1 behaviours to preserve:
- **I8** — filters run before variable resolution. Guaranteed by placing `filter` before
  `lookup` in the graph; assert in the conformance suite that a filtered request never
  produces a variable error.
- **I12** — a chain that fails to compile fails **closed** (matches nothing).
- Filters evaluate against **unfiltered** headers (`env.OrigHeaders` in V1,
  `server.go:521`) so behaviour does not change when redaction lists change.
- The `body_unparsed` attribution case (`server.go:186`): a body-sourced condition against
  a body that never parsed as JSON is labelled `body_unparsed`, not `endpoint`/`route`.
- `on_no_match: drop` returns the normal accepted response so senders cannot probe the
  filter config; `on_no_match: reject` returns an error. V1 also differs on the replay
  reservation between these two (`server.go:181` vs `:189`) — match it.

- [ ] **Step 1: Write the failing tests, then implement, then pass**

- [ ] **Step 2: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_filter.go internal/flowconfig/node_filter_test.go
git commit -m "feat(flowconfig): filter node over the existing filter chain"
```

---

## Task 4: `lookup`

**Files:** `internal/flowconfig/node_lookup.go`, `node_lookup_test.go`

`Envelope -> Envelope`. Wraps `lookup.New` and the cached provider in
`internal/lookup/cache.go`.

**Scalability rule R4 is the point of this node.** The existing cache already has TTL,
bounded entries (10k default), `singleflight`, and optional negative caching. Use it
**always** — never construct a raw provider. Steady-state read load must be
`unique_keys / TTL`, not `events/sec`.

The lint rule from Chunk 2 warns when a `lookup` node has no `cache` block; make sure the
default is still a sane cache rather than no cache, so the warning is about tuning and not
about a footgun.

Behaviours: resolved value lands in `Envelope.Vars` under the node's configured name; a
miss routes to `fallback` if configured, otherwise errors with 400 (V1 treats an
unresolved variable as a bad request or a fallback redirect, never a delivery failure —
`server.go:263`).

- [ ] **Step 1: Write the failing tests**

Include a **cache-behaviour test**: two identical lookups within the TTL must call the
provider **once**. Use a counting fake provider. Also test `singleflight`: N concurrent
first lookups of the same key make one provider call.

- [ ] **Step 2: Confirm failure, implement, confirm pass**

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_lookup.go internal/flowconfig/node_lookup_test.go
git commit -m "feat(flowconfig): lookup node with mandatory caching and singleflight"
```

---

## Task 5: `switch` and labelled output ports

**Files:** `internal/flowconfig/node_switch.go`, `node_switch_test.go`;
modify `internal/graph/executor.go`

This is the first node using the labelled output ports that Chunk 1's `ParseEdge` already
parses (`from:port>to`). The executor currently ignores `Edge.FromPort` — it must now
honour it.

Semantics from `v2/02-design.md` §5:
- A `switch` has labelled out-ports, one condition each, plus optional `default`.
- **Exactly one port fires** — first match wins.
- Fan-out on the *default* port still means all successors, in parallel. These are
  different mechanisms and must not be conflated.

**Executor change:** in `execution.node`, when a node reports which port it selected, only
follow edges whose `FromPort` matches. Add a method to the `Node` interface or a separate
optional interface:

```go
// Brancher is implemented by nodes that select one labelled output port.
// The executor follows only edges whose FromPort equals the returned label.
// A node that does not implement Brancher fans out to all successors.
type Brancher interface {
    SelectedPort() string
}
```

Prefer the optional-interface approach — it leaves `Node` unchanged, so none of the 16
existing nodes need edits.

- [ ] **Step 1: Write the failing tests**

Executor tests: a `Brancher` returning `"matched"` runs only the `matched` branch; a
`Brancher` returning `""` with a `default` edge runs the default branch; a `Brancher`
whose label matches no edge is an error, not a silent drop.

Compile-time test: a `switch` whose labelled edges reference an undeclared case is a
compile error. That means `graph.Compile` needs to ask the node which port labels it
declares — add an optional interface `PortDeclarer { OutPorts() []string }` and validate
edges against it.

Node tests: first-match-wins ordering; `default` fires when nothing matches; no `default`
and no match is an error.

- [ ] **Step 2: Confirm failure, implement, confirm pass**

Also add the two-`respond`-in-parallel-fan-out check here if Chunk 2's lint rule deferred
it: two `respond` nodes on different `switch` branches is **legal** (one fires); two
reachable through the same parallel fan-out is an error.

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_switch.go internal/flowconfig/node_switch_test.go \
        internal/graph/executor.go internal/graph/executor_test.go internal/graph/compile.go
git commit -m "feat(graph): labelled output ports and the switch node"
```

---

## Task 6: The I1–I18 conformance suite

**Files:** Create `internal/conformance/` — `conformance_test.go`, `invariants_test.go`,
`helpers_test.go`

A separate package so the suite reads as a specification rather than as unit tests, and so
it can exercise the full stack (config → compile → serve) the way a real request does.

**One test function per invariant, named for it.** A failure must immediately say which
invariant broke, not which assertion tripped:

```go
// TestI2_InflightAcquiredBeforeBodyRead asserts invariant I2 from
// v2/00-v1-baseline.md §2, established at V1's server.go:422:
// "a flood cannot exhaust memory by buffering unbounded bodies".
//
// The test proves it structurally: the body reader placed in the context
// fails the test if it is read at all, and the inflight limit is set to zero
// so the request must be rejected before any node runs.
func TestI2_InflightAcquiredBeforeBodyRead(t *testing.T) {
	// ...
}
```

Coverage plan — every invariant gets a test, and the ones the type system already handles
get a **compile-rejection** test rather than a runtime one:

| Invariant | How to test it |
|---|---|
| I1 loop-hop before body | Body reader that fails the test if read; send hops at the limit; assert 400 and that the body was never read |
| I2 inflight before body | Same pattern, `max_inflight_requests: 0` |
| I3 IP allowlist before body | Same pattern, disallowed source IP |
| I4 rate limit before body | Same pattern, exhausted bucket |
| I5 nothing acts on unverified payload | **Compile-rejection test**: a config wiring `envelope` directly to `read_body` must fail to load |
| I6 auth verifies raw bytes | Sign a body with whitespace and unsorted keys; assert it verifies |
| I7 replay reservation after auth | **Compile-rejection test**: `replay_guard` takes `Verified`, so wiring it before `auth` cannot load |
| I8 filters before variable resolution | A filtered request must return the filter outcome, never a variable-resolution error |
| I9 capture INSERT before publish context | Deferred to Chunk 8, which wires capture. **Leave a skipped test with a pointer**, do not silently omit it |
| I10 publish detached from request context | Cancel the client context mid-request; assert the publish still completed |
| I11 unknown auth scheme fails closed | Assert every request rejected, none accepted |
| I12 broken filter chain fails closed | Assert matches nothing |
| I13 broken flow registers its path, 500 not 404 | A flow that fails to build must still answer its path with 500 |
| I14 filtered routes are not delivery failures | Assert the response is 202 and the filtered branch is absent from `failed` |
| I15 replay capture is best-effort | Deferred to Chunk 8. Skipped test with a pointer |
| I16 handler panics become 500 | A panicking node; assert 500 and the process survives |
| I17 reservation released on retryable failure | Publish failure, then an identical retry must be accepted, not 409 |
| I18 timing-safe comparison in auth | Assert by inspection: `grep` that no verifier uses `==` on secret material, and that `hmac.Equal`/`subtle.ConstantTimeCompare` is used. A timing test would be flaky; a structural assertion is honest |

The `grep`-style test for I18:

```go
// TestI18_AuthUsesConstantTimeComparison is a structural assertion rather
// than a timing measurement: timing tests are flaky in CI, and what we
// actually want to prevent is a future edit introducing `==` on secret
// material. Gaps doc C3 records that universal-gateway's clearedge-utils.js
// does exactly that.
func TestI18_AuthUsesConstantTimeComparison(t *testing.T) {
	// walk internal/auth/*.go, skip _test.go, assert no comparison of a
	// variable named like a secret/signature/mac with == or !=, and assert
	// hmac.Equal or subtle.ConstantTimeCompare appears in every verifier
	// that compares digests.
}
```

- [ ] **Step 1: Write the whole suite, expecting failures**

Write all 18 first. Several will fail — that is the point; it tells you which invariants the
V2 implementation has not actually preserved.

- [ ] **Step 2: Fix the implementation until the suite is green**

**Fix the code, never the test.** If an invariant genuinely no longer applies, that is a
design change: stop, update `v2/00-v1-baseline.md` with the reasoning, and confirm with the
user. Do not quietly relax an assertion.

- [ ] **Step 3: Wire the suite into CI**

Add a `make conformance` target running `go test ./internal/conformance/ -v`, and add it to
the release workflow alongside `make test`. An invariant suite that is not in CI is
decoration.

- [ ] **Step 4: Commit**

```bash
make test && make lint && make conformance
git add internal/conformance/ Makefile .github/
git commit -m "test(conformance): invariant suite covering I1-I18"
```

---

## Done when

- [ ] `make conformance` passes with a named test per invariant I1–I18
- [ ] I9 and I15 exist as **skipped** tests pointing at Chunk 8, not omitted
- [ ] `make conformance` runs in CI
- [ ] All seven nodes from Tasks 1–5 have their own tests and pass
- [ ] `lookup` provably calls its provider once per key per TTL window
- [ ] `switch` fires exactly one branch; labelled edges are validated at compile time
- [ ] `go test -race` clean across `internal/graph`, `internal/flowconfig`, `internal/httpapi`
- [ ] **V1 still serves and validates**
