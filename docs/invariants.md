# Request-Pipeline Invariants

Hookway's request pipeline is a typed node graph, so most of its ordering
guarantees are enforced by the port type system rather than by convention: a
gate node consumes `RequestHead` (which carries no body), so it *cannot* be
wired after `read_body`. See [Configuration § Flows](configuration.md#flows).

The guarantees that matter for security and reliability are numbered below and
each has a named test in `internal/conformance`. A test failure there names the
invariant it broke. Two rules for anyone changing that package:

- **Fix the implementation, not the test.** If a guarantee genuinely no longer
  applies, that is a design change: update this document with the reasoning
  first.
- **`PortType.Accepts` is strict equality.** Widening it to an assignability
  relation looks like a simplification and silently destroys I1–I4, because
  `RawRequest` contains a `RequestHead`.

## Phase ordering

These hold because of the port lattice; the conformance tests for I5 and I7
assert that the offending config *fails to load* rather than misbehaves at
runtime.

| ID | Invariant | Why |
|---|---|---|
| I1 | The loop-hop check runs before the body is read | A terminal 4xx stops a delivery loop without buffering the payload |
| I2 | The in-flight slot is acquired before the body is read | A flood must not exhaust memory by buffering unbounded bodies |
| I3 | The IP allowlist runs before the body is read | Reject an unauthorized source without paying the buffering cost |
| I4 | Rate limiting runs before the body is read | Throttle before paying the buffering cost |
| I5 | Auth verification runs before any body-derived work | Nothing may act on an unverified payload. The only producer of `Verified` is an `auth`/`no_auth` node, and every content-reading node consumes `Envelope`, which is reachable only through `Verified` — so auth bypass is unrepresentable, not merely linted |
| I6 | Auth verifies the **raw bytes**, never a re-serialized body | See [Security](security.md); Hookway does not parse, trim, normalize, or re-serialize the body before verifying |
| I7 | The replay reservation happens after auth, before envelope build | An unauthenticated request must not consume an idempotency key |
| I8 | Filters run before variable resolution | A filtered request is never reported as a variable-resolution error |
| I9 | The replay-capture INSERT happens before the publish context is created | Publish always gets its full `publish_timeout` budget regardless of store latency |

## Execution semantics

The type system cannot reach these; they are executor and node behaviour, and
tests are the only thing holding them.

| ID | Invariant | Why |
|---|---|---|
| I10 | Publish runs on a context detached from the inbound request (`context.WithoutCancel` + `publish_timeout`) | A sender disconnect must not cancel work already committed to delivery. `respond` is deliberately **not** detached: it writes to the caller's connection |
| I10a | A publish success edge runs only after that publish succeeds, on the same detached `publish_timeout` budget | Ordering (S3 then Lambda) is an edge, not YAML edge-list order. A cancelled request does not cancel the chain. `respond` cannot follow the continuation |
| I11 | An unknown auth scheme fails **closed** | A misconfiguration rejects every request rather than accepting unverified ones |
| I12 | A filter chain that fails to compile fails **closed** (matches nothing) | Same posture as I11 |
| I13 | A flow that fails to compile still registers its path and returns 500, not 404 | A config error must not look like an unconfigured endpoint |
| I14 | A filtered branch is never counted as a delivery failure | Keeps `delivery_failure_policy` semantics intact |
| I15 | Replay capture is strictly best-effort; its errors and panics never change the response | Observability must not affect delivery |
| I16 | Handler panics are recovered and become 500, not a dropped connection | |
| I17 | The replay reservation is released on any failure where a retry could still make progress | Otherwise a legitimate retry is rejected 409 and the event is lost. Includes a *partial* failure under `skip_failed`, which returns 202 while some destinations still need the retry |
| I18 | Auth uses timing-safe comparison throughout (`hmac.Equal` / `subtle.ConstantTimeCompare`) | Asserted structurally rather than by timing, which is flaky in CI |

## Known limitations

These are accepted trade-offs, not defects. They are documented so they are
revisited deliberately.

| ID | Limitation | Consequence |
|---|---|---|
| L1 | Rate limiting and replay protection are in-process (per pod) | N replicas mean N × the configured limit, and replay protection is per-pod rather than per-cluster. Consumers dedupe on the envelope `id` (UUIDv7), which is the documented contract |
| L2 | Flows are not resumable | A pod that dies mid-flow loses in-flight work; recovery is the sender's retry, which is why a publish failure returns 503 |
| L3 | One process, one set of destination credentials | A bug in any flow's handling can reach every destination the process can write to. Deploy split by trust domain when that blast radius matters |
| L4 | No in-process TLS | Terminate TLS at an ingress, ALB, or sidecar |
| L5 | goja has no hard memory cap | See [Security § Code node](security.md#code-node) |
