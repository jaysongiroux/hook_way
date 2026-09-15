# Chunk 1 handoff — graph core

Implementation date: 2026-09-14. This is the worker's verification report; the
orchestrator separately reviews and closes the gate in `v2/IMPLEMENTATION.md`.

The user excluded external gateway/provider migration work from this implementation.
No external source-parity claim or I6 exception is part of this implementation.

## Public contracts

```go
type Node interface {
    Run(context.Context, Payload) (Payload, error)
}
type NodeSpec struct {
    Type string
    In, Out PortType
    Detached bool
    New func(NodeConfig) (Node, error)
}
type NodeConfig struct {
    ID, Type string
    Options yaml.Node
}
type RunOptions struct {
    PublishTimeout time.Duration
    FanoutConcurrency int
    OnNode func(NodeEvent)
}
func Compile(string, FlowConfig, *Registry) (*Flow, error)
func (*Flow) Run(context.Context, Payload, RunOptions) (Terminal, error)
```

`Node` instances are shared between concurrent requests. They must not retain
request state. `Registry` is assembled before compilation; do not mutate it
concurrently. `Flow` is immutable after successful compilation. `Name`, `Root`,
`NodeIDs` provide read-only inspection; `NodeIDs` returns a copy.

Ports remain strict equality: None, RequestHead, RawRequest, Verified, Envelope,
Terminal. Enum names are prefixed `Port`. Concrete payloads travel **by value**;
pointer payloads, typed nils and unknown implementations fail rather than panic.
The root is invoked with the supplied seed; the seed must match its declared
output phase. No real node registry, HTTP adapter or global config loader ships
in this chunk.

`NodeConfig.Options` contains the node-specific mapping **without `id`/`type`**.
This intentionally differs from the sample implementation, which kept those keys.
Missing identity is diagnosed at Compile alongside other problems. YAML structural
errors still fail decoding. `ParseEdges` returns all valid edges plus joined,
indexed errors; Compile keeps checking independent nodes and valid edges.

## Execution semantics and API additions

- `stop_on_failure` walks successors sequentially in configured edge order. Later
  branches do not run after failure. `FanoutConcurrency` is reserved for Chunk 5's
  bounded parallel `skip_failed`; it currently changes nothing.
- Reconvergence (multiple incoming edges) is rejected. Without merge semantics it
  could publish the shared descendant twice. Chunk 4 can permit convergence proven
  exclusive through one switch, or explicitly define merge behavior.
- Labelled edges parse but **fail compilation** until their semantics exist.
  Duplicate edges, bad identifiers, unreachable nodes, nonterminal leaves, cycles,
  terminal successors, invalid specs, nil nodes and constructor panics fail closed.
- A successful `Terminal` from any node stops that branch. This supports filter
  drops and early responses. Normal outputs must match the registered phase.
- The returned terminal preserves explicit response and actual delivery metadata
  on both success and failure. `respond` is not a destination. Multiple responses
  cause a runtime error; configuration-level response exclusivity remains Chunk 2/4.
- Destination fallback records use `NodeSpec.Detached`, not merely Terminal output.
  **New:** optional `DeliveryTarget() string` on a destination instance supplies its
  configured target template. It is evaluated once at compile, with panic recovery.
  Returned delivery metadata takes precedence; empty node id/target fields are filled.
- **New:** `Delivery.Unattempted bool` distinguishes a destination skipped after a
  failure from successful delivery. Unattempted metadata is appended in config order.
  Destinations under an already completed early-stop branch are excluded. A detached
  node error fills missing `Delivery.Err`; a specific existing delivery error survives.
- Node panics become node errors and appear in `OnNode`. Observer panics are isolated
  so instrumentation cannot change the delivery outcome. Non-destination contexts
  remain caller-bound; destination contexts detach cancellation and receive a fresh
  positive `PublishTimeout`. Nodes must honor their context; no goroutine is forcibly
  killed on deadline.

## Compensation — D12 / I17

```go
type Compensator interface {
    Compensate(context.Context, Payload, error) error
}
type Compensation struct {
    NodeID string
    Node Compensator
    Input Payload
}
func NeedsCompensation(Terminal, error) bool
func RunCompensations(context.Context, Terminal, error, time.Duration) error
```

Only the executor records successful `Compensator` executions. Node-returned
`Terminal.Compensations` cannot inject callbacks and are ignored. The executor
snapshots the consumed input before Run, records successful reservations even if
the node then violates its output contract, and returns reverse execution order.
The server decides whether to release: any flow error or failed delivery requires
compensation, successful filtering does not. Run never releases automatically.
`RunCompensations` detaches cancellation, retains context values, applies one bounded
deadline (five seconds when unspecified), and joins errors/panics while continuing
with remaining releases.

## Payload ownership

The input seed, each fan-out branch and compensation snapshots receive independent
mutable payload storage. Linear successors own their predecessor's result. Nodes
must not return shared mutable instance fields as payloads. Copies include headers,
query slices, path vars, original raw bytes, vars, upstream response and JSON body
maps/slices. String/numeric/bool scalars are safe to share.

**New:** custom parsed body types implement `BodyCloner`:

```go
type BodyCloner interface { CloneBody() (any, error) }
```

The hook must produce independently mutable storage (future XML DOM support).
Unknown mutable types fail closed; clone-hook panics become errors. JSON cloning
has a depth limit of 256 to reject cycles/pathological nesting. No JSON
serialization round-trip alters number types or original signed bytes.

## Where later chunks attach

- Chunk 2: per-node schemas/config loading, policy lint, response exclusivity.
- Chunk 3: real nodes wrap `internal/auth` and `internal/webhook`; HTTP server owns
  the final response, inflight lifetime and calling compensation.
- Chunk 4: gates use `internal/ratelimit`; filters use `internal/filter`; lookup uses
  `internal/lookup` and its cache. Add branch selection **to each invocation's
  result**, never shared node state. `execution.node` successor traversal is the
  extension point. `on_error` attaches where a node error currently returns.
  I8 filter-before-lookup needs explicit structural validation, not port widening.
- Chunk 5: destinations wrap `internal/queue`; add bounded parallel `skip_failed`
  while preserving default sequential stop. Branch cloning is already required.
- Chunk 8: `OnNode` attaches metrics/logs/spans without importing metrics into graph.
  Replay capture and I9/I15 remain later executor/server integration work.

## Verification evidence

Tests were written before implementations. Initial `go test ./internal/graph/ -run
'TestAccepts|TestCompile'` failed to build on missing Payload/PortType/FlowConfig.
After the executor first compiled, its tests passed while the new compiler/parser
regressions failed on missing aggregation, duplicate unknown ids, unsupported labels,
identifier ambiguity and identity keys leaked into options. These failures were
resolved in production code. Additional failing-first regressions caught a typed-nil
payload panic, injected compensation callbacks and accepted panicking destination
metadata before their fixes.

- `go test ./internal/graph`: passed.
- `go test -race -coverprofile=/tmp/hookway-graph-coverage.out ./internal/graph`: passed;
  **91.3% statement coverage** after the final regression changes.
- `TestCompileRejectsGateAfterReadBody` passes and requires the error to name both
  RequestHead and RawRequest. `TestInvariantGateBeforeBodyCannotBeConfiguredAway`
  exercises illegal wiring decoded from YAML.
- Concurrent fan-out mutation regression runs 20 requests through one compiled flow
  and checks nested JSON, vars, headers, original bytes and upstream bytes remain isolated.
- Full repository `make test`, `make lint` and `git diff --check` passed after final edits.
  `make test` intentionally excludes external-service integration groups; this is
  not `make test-all` or a live HTTP verification.

No new third-party dependencies. No stage, commit, push or branch operation by the worker.
