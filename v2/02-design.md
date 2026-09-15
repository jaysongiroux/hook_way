# V2 Design — Typed-Port Node Graph

**Status:** draft, pending review. Decisions D1-D5 in `README.md` are settled; everything
below is proposal.

## 1. The core idea

A flow is a DAG of nodes. Each edge carries a **typed payload**. Each node type declares
the payload type it consumes and the one it produces. The graph is type-checked when the
config loads, and the type lattice is chosen so that V1's phase-ordering invariants
(I1-I9 in `00-v1-baseline.md`) are **type errors rather than lint warnings**.

The single most important property, stated precisely:

> Every node that can act on payload content consumes `Envelope`. The only producer of
> `Envelope` is the `envelope` node, which consumes `Verified`. The only producers of
> `Verified` are `auth` and `no_auth`. Therefore **no node can act on an unverified
> payload**, and auth-bypass is unrepresentable rather than linted.

## 2. Port types

| Type | Contents | Produced by |
|---|---|---|
| `RequestHead` | method, path, path vars, query, headers, source IP, hop count. **No body.** | `http_trigger`, and any gate node |
| `RawRequest` | `RequestHead` + raw body bytes. Unverified. | `read_body` only |
| `Verified` | `RawRequest` + auth identity. Signature checked against raw bytes. | `auth`, `no_auth` only |
| `Envelope` | `Verified` + parsed body + resolved vars + upstream response slot | `envelope` only |
| `Terminal` | nothing flows onward | destinations, `respond` |

**Assignability is strict equality.** `RawRequest` must NOT satisfy `RequestHead` even
though it contains one. If it did, a gate node could be wired downstream of `read_body`
and invariants I1-I4 would silently evaporate. This needs an explicit test asserting the
type checker rejects `read_body -> rate_limit`.

### Why body parsing is its own node

V1 folds JSON parsing into `webhook.BuildEnvelopeWithOptions`. V2 makes it the explicit
`Verified -> Envelope` step, because the universal-gateway proxies need `body_format: xml`
and `body_format: raw` — the DocMagic XML pipeline (gap A3) has no JSON representation at
all. Making the parse explicit means the format is a visible node property instead of an
assumption baked into the envelope builder.

## 3. What does NOT become a node

Not everything in `handleWebhook` is a step. Things with **scope or lifetime** semantics
stay executor concerns, because a node with an acquire/release span that brackets the rest
of the graph is not a node — it is a wrapper:

| V1 concern | V2 home | Why |
|---|---|---|
| Inflight semaphore (I2) | Executor, wrapping the whole flow | Acquire/release brackets everything. Acquiring before the graph runs satisfies I2 *more* strongly than a node could. |
| Panic recovery (I16) | Executor, per node and per flow | Cross-cutting |
| Metrics / structured logging | Executor, per node | Uniform per-node instrumentation is a V2 *win*: V1 only measures the whole request |
| Request/trace id propagation | Executor, in context | Cross-cutting |

Calling this out because "everything is a node" is the kind of purity that produces bad
designs. Point-in-time checks become nodes; scopes do not.

## 4. Node catalog

```
http_trigger  : None        -> RequestHead   path (with {seg} and {rest...}), methods
loop_guard    : RequestHead -> RequestHead   max_hops                         (I1)
ip_allowlist  : RequestHead -> RequestHead   cidrs                            (I3)
rate_limit    : RequestHead -> RequestHead   rpm, burst, key_by               (I4)
read_body     : RequestHead -> RawRequest    max_bytes                        (phase boundary)
auth          : RawRequest  -> Verified      scheme, secret, source           (I5, I6, I18)
no_auth       : RawRequest  -> Verified      — explicit and greppable         (I11)
replay_guard  : Verified    -> Verified      ttl, max_entries, key            (I7, I17)
envelope      : Verified    -> Envelope      body_format: json|xml|raw
filter        : Envelope    -> Envelope      conditions, on_no_match          (I8, I12)
lookup        : Envelope    -> Envelope      provider, table, key, cache, fallback
transform     : Envelope    -> Envelope      kind: changed_fields|field_map|xml_prune
code          : Envelope    -> Envelope      goja — see §6
switch        : Envelope    -> Envelope      labeled out-ports, first match wins
http_request  : Envelope    -> Envelope      upstream call; response into envelope
publish       : Envelope    -> Terminal      destination id, target, delivery options
respond       : Envelope    -> Terminal      status, headers, body, passthrough
```

`publish` references an entry in the shared top-level `destinations:` map. The existing
destination factory owns and reuses connections for all 13 destination types; graph nodes
do not embed or construct broker connections. This is decision D13.

`http_request -> Envelope` (not `Terminal`) is what makes the sync proxies work: the
upstream response lands in a slot on the envelope, and a downstream `respond` node
projects it back to the caller. That is gap A1, in two nodes.

## 5. Fan-out, branching, errors

Three distinct concerns that a naive "multiple out-edges" model conflates:

**Fan-out** — multiple edges from a node's *default* out-port means deliver to all of
them, in parallel. This is V1's `destinations` list.

**Branching** — the `switch` node has *labeled* out-ports with a condition each, plus an
optional `default`. Exactly one port fires. This replaces V1's route filters, which do
conditional routing awkwardly by attaching filters to destinations.

**Errors** — any node may declare an optional `on_error` out-port. If present, the error
payload routes there (n8n-style). If absent, the error propagates per the fan-out point's
`failure_policy`, which carries V1's two modes forward: `stop_on_failure` (default) and
`skip_failed`. Filtered-out branches are **not** failures (I14).

### Response rules

- **At most one `respond` may fire per request.** Load-time check: no two `respond` nodes
  reachable through *parallel* (fan-out) edges. Two `respond` nodes on different `switch`
  branches is legal, since only one branch fires.
- **A flow with no `respond` node** gets V1's default `202` + delivery summary. This makes
  `respond` purely additive and keeps every webhook-ingestion flow looking like V1.

### Compensation (preserving I17)

Some nodes reserve something that must be released if the flow later fails —
`replay_guard` and its idempotency key being the case I17 is about. The node cannot decide
this, because the outcome is downstream of it.

A node opts in by implementing `Compensator`. The executor records every one that ran
**successfully**, paired with the payload it consumed, and returns them on
`Terminal.Compensations` in reverse execution order. It does not invoke them — the server
does, because the condition depends on the delivery outcome:

> Release when **the flow errored, or any delivery failed.**

Both clauses are load-bearing. Under `skip_failed` a partial failure returns 202 while the
failed destinations still need the retry, so "release on error" is too narrow. A filter
silent drop returns success with zero deliveries and must **keep** the reservation because
the delivery was consumed, so "release on anything but full success" is too broad.

Two consequences worth stating:

- **Nodes recompute their reservation from the payload rather than storing it.** One node
  instance is shared across all requests and fan-out runs siblings in parallel, so a
  reservation field would be a data race. The replay key is a pure function of headers and
  raw body, both carried on `Verified`.
- **Compensation runs on a detached context.** A client disconnect is exactly when a
  publish fails; skipping the release there leaks the reservation until TTL, and the
  sender's retry gets a 409 instead of a redelivery.

This also means **`Flow.Run` returns a populated `Terminal` even when it returns an
error** — the server needs `Deliveries` both to decide on compensation and to build V1's
503 response body, which lists succeeded and unattempted destinations (`server.go:322`).

### Context and cancellation (preserving I10)

One rule, which reproduces V1 exactly and handles the new sync-proxy case correctly:

> **Destination nodes** run on a detached context (`context.WithoutCancel` + publish
> timeout). **Every other node** runs on the request context.

So a sender disconnect cannot cancel a committed delivery (I10), while `http_request ->
respond` correctly stays tied to the caller's request — which is what a synchronous proxy
must do.

## 6. The `code` node (goja)

### Trust model — state this first, it determines everything else

A `code` node is authored by **whoever writes the flow config**. That person can already
point a destination at an arbitrary URL, set arbitrary secrets, and disable auth. They are
inside the trust boundary. So the sandbox defends against **operator accidents** —
infinite loops, runaway allocation, accidental blocking — not against a hostile tenant.

This is why goja is defensible here. **If Hookway ever becomes multi-tenant or hosted,
where flow authors are not trusted operators, this conclusion does not hold** and the code
node would need to move to WASM with real resource isolation. Recording that now so the
decision is revisited deliberately rather than by drift.

### Sandbox requirements

| # | Control | Mechanism |
|---|---|---|
| C1 | Wall-clock timeout | `runtime.Interrupt()` from a watchdog goroutine; interrupt is checked between statements |
| C2 | Call stack bound | `runtime.SetMaxCallStackSize` |
| C3 | No host I/O | Fresh `goja.Runtime` per execution with **no** host objects registered: no `require`, no `fetch`, no timers, no filesystem |
| C4 | Bounded input | Refuse to hand the script a body over a configured size |
| C5 | Bounded output | Cap the returned value's serialized size |
| C6 | Fresh runtime per request | No state leaks between requests; no shared globals to poison |
| C7 | Panic recovery | A script that panics the runtime fails the node, never the process |

### The honest gap

**goja has no hard memory cap.** C1-C7 bound time, stack, I/O, and payload size, but a
script doing `let a=[]; while(1) a.push(1)` allocates until interrupted by C1 — which may
be after significant allocation, and under V1's single-process model that pressure is
shared with every other flow (weakness W2/B2). Mitigations, in order of cost: per-pod
memory limits plus liveness restart; deploy split by trust domain; or move the code node
out of process. **This must be written into the docs as a known limitation, not
discovered later.**

### What the code node is NOT for

The two transformations universal-gateway actually needs are specific algorithms, not user
scripting:

- **changed-field diff** (recursive diff with a `status.*` promotion rule)
- **5-rule XML prune pipeline** (ordered DOM rewrites over `@xmldom/xmldom`)

Both should be `transform` nodes in tested Go, not JS in a YAML string. goja has no DOM,
so the XML pipeline would need a host-provided XML API — which reintroduces exactly the
host-object surface C3 exists to avoid. The `code` node is the escape hatch for the long
tail; `transform` nodes handle the known cases.

## 7. Config shape

```yaml
schema_version: 2

flows:
  digifi_router:
    nodes:
      - {id: in,   type: http_trigger, path: /webhooks/universal/digifi, methods: [POST]}
      - {id: hops, type: loop_guard,   max_hops: 3}
      - {id: ip,   type: ip_allowlist, cidrs: ["10.0.0.0/8"]}
      - {id: rl,   type: rate_limit,   rpm: 600, burst: 60}
      - {id: body, type: read_body,    max_bytes: 1048576}
      - {id: auth, type: auth,         scheme: digifi, secret: "${DIGIFI_SECRET}"}
      - {id: dedupe, type: replay_guard, ttl: 5m}
      - {id: env,  type: envelope,     body_format: json}
      - {id: route, type: lookup,      provider: dynamodb, table: routing,
                                       key: "{body.data.object.id}", fallback: shared}
      - {id: diff, type: transform,    kind: changed_fields,
                                       from: body.data.previousObject, to: body.data.object}
      - {id: out,  type: sns,          topic: "{route}-applicationServiceQueue",
                                       message_attributes: {fields_changed: "{diff}"}}
    edges: [in>hops, hops>ip, ip>rl, rl>body, body>auth, auth>dedupe,
            dedupe>env, env>route, route>diff, diff>out]
```

And the Salesforce sync proxy — structurally impossible in V1 — is the same vocabulary
with a different tail:

```yaml
  salesforce_proxy:
    nodes:
      - {id: in,   type: http_trigger, path: /salesforce/proxy/{rest...}, methods: [ANY]}
      - {id: body, type: read_body,    max_bytes: 10485760}
      - {id: auth, type: auth,         scheme: header_exact, secret: "${SF_TOKEN}"}
      - {id: env,  type: envelope,     body_format: raw}
      - {id: up,   type: http_request, url: "${DIGIFI_BASE}/{rest}", forward_query: true,
                                       forward_method: true}
      - {id: resp, type: respond,      passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
```

Note this flow has no `rate_limit` node — legal, and exactly the "types enforce ordering,
not presence" gap from `01-topology-options.md`. A policy lint rule should warn.

## 8. Validation

Three layers, replacing most of `validation.go`'s 1,391 lines:

1. **Per-node config schema.** Reuse the existing `invopop/jsonschema` machinery already
   in `internal/config/schema.go`. Each node type contributes its schema to the registry.
2. **Edge type check.** One rule: `to.In == from.Out`, strict equality. Plus every leaf is
   `Terminal`, exactly one `http_trigger` root, no cycles.
3. **Policy lint.** The presence rules types cannot express: "public flow has no
   rate_limit", "code node has no timeout", "no_auth node present". Warn by default,
   `--strict` to error.

The registry doing double duty is the payoff for the UI: node types + their JSON schemas
means the graph editor can render property panels generically instead of hand-coding a
form per node type, which is what `web/src/graph/*Form.tsx` does today.

## 9. Settled implementation choices

Execution durability, readiness, UI scope and secret indirection are settled by decisions
D6–D11 in `README.md`. Destination vocabulary is settled by D13: one `publish` node
references the shared destination map. Remaining implementation questions and verification
gates are tracked in `IMPLEMENTATION.md` and `OPEN-QUESTIONS.md`.
