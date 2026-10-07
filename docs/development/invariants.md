# Request-pipeline invariants

These rules protect the request and delivery boundaries. They are checked by
configuration validation, the graph compiler, and runtime tests. Port types
prevent incompatible phases from being connected; they do not require a flow to
include every optional protection node.

The numbered rules correspond to `TestI*` tests in `internal/conformance`.
Run `make conformance` after changing request ordering, delivery, or capture.
Change a guarantee deliberately: update the implementation, explanation, and
relevant tests together.

## Port model

| Port | Carries | Produced by |
| --- | --- | --- |
| `PortNone` | No input; starts a top-level flow | — |
| `PortRequestHead` | Method, path, query, headers, source IP, and request metadata; no buffered body | `http_trigger` and request-head gates |
| `PortRawRequest` | Request head and original, unverified body bytes | `read_body` |
| `PortVerified` | Original request after the configured auth decision | `auth`, explicit `no_auth`, verified quotas, and `replay_guard` |
| `PortEnvelope` | Original request, parsed body, and variables accumulated by later nodes | `envelope` and envelope-processing nodes |
| `PortTerminal` | Response or delivery result with no outgoing payload | `respond` or a terminal delivery |

`no_auth` deliberately permits unverified traffic. Its `Verified` port means the
flow made an explicit authentication decision, not that a signature was checked.
It supplies no trusted identity for verified quotas.

A top-level flow has exactly one trigger and no cycles. Batch child flows instead
start with an envelope and have no trigger. Leaves must be terminal or a publish
node with no success continuation. A publish continuation carries an envelope.
See [Configuration](../reference/configuration.md#flows) for node and edge syntax.

`PortType.Accepts` in `internal/graph/port.go` uses strict equality. Keep that
boundary: allowing `RawRequest` to stand in for `RequestHead` would let gates run
after the body was buffered. Error edges carry the failing node's **input**;
their target must accept that input phase.

## Request ordering

| ID | Rule | Enforcement and scope |
| --- | --- | --- |
| I1 | Loop-hop checks precede body buffering. | `loop_guard` takes a request head and stops a detected loop before `read_body`. |
| I2 | In-flight admission precedes body buffering. | The server acquires capacity before executing the flow. |
| I3 | IP allowlists precede body buffering. | An `ip_allowlist` node only accepts a request head. |
| I4 | Request-head rate limits precede body buffering. | The default stage uses a request head. Explicit `stage: verified` quotas instead follow authentication and use trusted identity. |
| I5 | Body-derived business work follows the auth decision. | Parsing an envelope requires `Verified`; use `no_auth` only when unauthenticated access is intentional. |
| I6 | Body signatures use the original request, before business transforms. | Raw bytes are the default. The explicit `hmac_sha256_headers_dot` / `body_representation: json_js` protocol signs a JavaScript JSON representation of that original body. Static tokens and Basic auth do not sign the body. |
| I7 | Duplicate reservations follow the auth decision and precede envelope parsing. | `replay_guard` consumes and returns `Verified`. |
| I8 | A branch filtered out before a lookup performs no lookup. | Graph execution stops that branch. When a flow contains filters and lookups, configuration validation requires a filter before each lookup path. Types alone do not enforce this ordering. |

Signature formats and their coverage are explained in
[Security](../reference/security.md). Rate limits and duplicate reservations require
an explicit [protection store](../guides/protection-stores.md).

## Execution and recovery

These rules depend on node and executor behavior, not just port types.

| ID | Rule |
| --- | --- |
| I10 | Publish detaches from caller cancellation with `context.WithoutCancel`, bounded by `publish_timeout`. `respond` still uses the caller's connection. |
| I10a | A publish success edge runs only after success, using the same detached timeout budget. Edge-list order is not a delivery dependency; `respond` cannot follow a publish continuation. |
| I11 | Unknown authentication schemes fail validation and standalone startup. Verification errors never grant access. |
| I12 | A filter whose conditions cannot compile matches nothing; it cannot accidentally deliver an event. Validate configurations before serving to catch the error. |
| I13 | A per-flow graph compilation error keeps that configured path registered with a 500 response. Startup-wide errors, such as invalid auth or store configuration, can refuse the whole server. |
| I14 | A filtered branch is not counted as a failed delivery. |
| I15 | Best-effort failure capture does not change the caller's response or readiness. Bounded background workers build/upload records; capture can be dropped. Protection-store readiness is a separate rule. |
| I16 | A handler panic is recovered and returns 500 instead of dropping the connection. |
| I17 | A duplicate reservation is released only by its owner when no delivery side effect occurred. Successful, partial, or uncertain effects retain it. |
| I18 | Secret/digest comparisons use `hmac.Equal` or `subtle.ConstantTimeCompare`. The conformance check is structural, not a timing benchmark. |
| I19 | Nodes leave their input unchanged and return a changed copy. Failure capture depends on retaining the input that the failed node actually received. |
| I20 | Failure capture retains eligible failures only after a final 2xx response and when no successful error path or dead-letter delivery handled them. Async webhook capture waits for the final inbound status. Explicit Inspect observations freeze current body/context, survive later failures/non-2xx responses and never contribute delivery side effects. |
| I21 | A redrive executes at most once per run and does not collect inspections, capture, dead-letter, or compensate its own failure. Core reads the full control request before execution. Replay resubmits only when core explicitly reports `not_started`, or the request was incomplete before execution could begin. Destination retry settings can still make several delivery attempts. |

See [Delivery guarantees](../operations/delivery-guarantees.md) for acknowledgement
policies and [Replay service](../operations/replay-service.md) for captured retries.

## Limits to plan around

| ID | Limit | What it means |
| --- | --- | --- |
| L1 | Protection depends on its store. | Memory is per process. Redis/PostgreSQL coordinate replicas, but expiry, restart, or backend data loss can admit repeats. Consumers still need stable sender event IDs and idempotency. |
| L2 | In-flight work is not durably resumable. | A crash loses unfinished work and async memory-queue jobs. Sender redelivery or a retained replay record may recover some failures; neither covers every acknowledged event. |
| L3 | One process has access to its configured destination credentials. | Named connections organize credentials but do not isolate flows. Split deployments by trust domain when needed. |
| L4 | Inbound servers use plain HTTP. | Terminate TLS at your ingress, load balancer, or sidecar. |
| L5 | The JavaScript runtime has no hard memory cap. | Use bounded code and deployment memory limits; see the [code-node security guidance](../reference/security.md#code-node). |
