# Topology Models Considered

Decision D2. The question: **which concerns become nodes, and what stops you from wiring
them in a dangerous order?** The invariants in `00-v1-baseline.md` §2 are currently
enforced by nothing but statement order inside one function; whatever V2 does has to
re-establish them.

## Option 1 — Free DAG (n8n-faithful) — REJECTED

Every concern is a node, anything wires to anything, one grab-bag `Payload` type where
every field is optional. Ordering invariants become hand-written lint rules: roughly ten
separate graph traversals, each with its own tests, each bypassable by ignoring a warning.

Rejected because the security boundary would be enforced by warnings, and because every
node has to defensively re-check at runtime whether the fields it needs are populated yet.

Also a **dead end for migration**: free-DAG configs legally contain orderings that typed
ports reject, so converting later is a breaking rewrite of every config.

## Option 2 — Constrained spine — REJECTED (but a safe fallback)

Pre-auth concerns stop being nodes. One fixed root `trigger` owns path, methods, IP
allowlist, rate limit, max body, loop protection, auth, and replay protection as config
fields; its single output port emits an already-verified `Envelope`. Everything downstream
is a free DAG.

Invariants I1-I7 become *unrepresentable* rather than checked — there is no port meaning
"raw unverified request", so nothing can attach there. Cheapest credible option:
most of `handleWebhook` survives as the trigger's implementation.

Rejected because of two ceilings:

- **Auth can never branch or be conditional.** One scheme per flow, permanently. Fine for
  all seven universal-gateway handlers today, but a hard ceiling.
- **The graph can say nothing about the pre-body phase.** "IP allowlist sourced from a
  DynamoDB lookup" is unexpressible, because lookups are nodes and nodes only exist after
  the trigger.

Kept on file as the fallback if typed ports overrun: it delivers ~60% of the value for
~40% of the work, and spine -> typed ports is a *forward* migration (split the trigger
into its constituent nodes; configs change, executor does not).

## Option 3 — Typed ports — CHOSEN

Every concern is a node, as in Option 1. But each edge carries a **typed** payload and the
graph is type-checked at load. The type is the ordering constraint.

The port types are the phases of a request:

| Type | Contents |
|---|---|
| `RequestHead` | method, path, query, headers, source IP, hop count — **no body** |
| `RawRequest` | `RequestHead` + raw bytes, unverified |
| `Verified` | `RawRequest` + auth identity, signature checked |
| `Envelope` | `Verified` + parsed body + resolved variables — what publishes |
| `Terminal` | a response was written or a delivery completed |

Each node type declares its signature in a registry:

```
http_trigger : None        -> RequestHead
ip_allowlist : RequestHead -> RequestHead
rate_limit   : RequestHead -> RequestHead
read_body    : RequestHead -> RawRequest     <- the phase boundary
auth         : RawRequest  -> Verified
replay_guard : Verified    -> Verified
filter       : Envelope    -> Envelope
lookup       : Envelope    -> Envelope
transform    : Envelope    -> Envelope
sns          : Envelope    -> Terminal
respond      : Envelope    -> Terminal       <- unlocks the sync proxies
```

The whole validator is then one rule applied per edge — `to.In` must equal `from.Out` —
plus "every leaf must be `Terminal`". What that buys:

- **I1-I4 (gates before body read):** `rate_limit` needs `RequestHead`; `read_body` emits
  `RawRequest`. Wiring a gate downstream of the body read is a type error. Strict
  equality, not assignability — `RawRequest` must **not** satisfy `RequestHead`, or the
  invariant evaporates.
- **I5 (nothing acts on an unverified payload):** every useful node consumes `Envelope`;
  the only route to `Envelope` runs through `Verified`; the only producer of `Verified` is
  `auth`. **Auth is mandatory because it is the only path through the type graph.**
- **Dangling nodes that silently drop events:** a non-`Terminal` leaf is a type error.

`no_auth` becomes an explicit node that also emits `Verified` — strictly better than
V1's config field, because it is loud on the canvas and greppable in YAML.

### Honest weaknesses of typed ports

1. **Types enforce ordering, not presence.** Nothing requires a `rate_limit` node to exist
   at all — `http_trigger` emits `RequestHead` and `read_body` consumes it directly. So
   "every public flow must be rate limited" stays a policy lint rule. Types give ordering
   for free, and presence only where a type has exactly one producer (which is why the
   auth guarantee is real and the rate-limit one is not).
2. **Most new code** of the three options: a new `internal/graph` package for port types,
   registry, type checker, and executor. Offset by deleting most of `handleWebhook` and a
   large fraction of `validation.go`'s 1,391 lines.
3. **The lattice needs design.** Fan-out with multiple out-edges, error edges, and nodes
   that want both `RequestHead` and `Envelope` are all unresolved. Any of these could
   force the lattice to be more complicated than the clean table above.
4. **Execution-semantics invariants get no help.** I10-I18 are executor behaviour, not
   wiring, so the type checker does nothing for them. They need tests, same as V1.

### Validation spike (recommended before building)

Define the five port types and hand-write **all seven** universal-gateway handlers as node
graphs. If any cannot be expressed, the lattice is wrong — and that is an hour to find
out rather than three weeks. The four sync proxies are the interesting cases.
