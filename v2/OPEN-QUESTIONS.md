# Open Questions

Promoted into `README.md`'s decision table once answered.

## Answered

- [x] **Q1 — Code node execution engine.** -> **Sandboxed JS (goja).** See D3, and
      `02-design.md` §6 for the trust model this rests on and the memory-cap gap it leaves.
- [x] **Q2 — V1 config compatibility.** -> **Clean break**, no converter. See D4.
- [x] **Q3 — Code layout.** -> **In-place on the `v2` branch.** See D5.
- [x] **Q4 — Fan-out and branch semantics.** -> Three separate mechanisms rather than one
      overloaded edge-count rule: default-port fan-out (parallel, all), `switch` node with
      labeled out-ports (exactly one fires), optional `on_error` port per node. V1's
      `delivery_failure_policy` carries forward as a property of each fan-out point.
      See `02-design.md` §5.
- [x] **Q5 — Execution durability.** -> **No per-node checkpointing.** In-memory per
      request. Writes per event must stay independent of node count. Cost: flows are not
      resumable. See D8 and `03-scalability.md` §1.
- [x] **Q6 — Readiness model.** -> **Probe each unique destination once per interval,
      derive per-flow readiness.** Fixes W1. See D9 and `03-scalability.md` §R7.
- [x] **Q7 — UI scope.** -> **Config generator only**, not an n8n-style live editor. See D6.
- [x] **Q8 — Secrets as node properties.** -> **In scope.** `secret_ref` with caching,
      timed refresh, and rotation overlap. See D7 and `03-scalability.md` §4.
- [x] **Q9 — Replay capture write scheme.** -> **Option A: port `replaystore` unchanged.**
      Batching was scoped and then deliberately deferred: it is a constant-factor
      optimization inside one package (O(2) -> O(0.01)), not an architectural property,
      and current traffic is orders of magnitude below where it pays. No WAL, no incident
      spool. See D10 and `03-scalability.md` §R6.
- [x] **Q10 — Cluster scope for `replay_guard` / `rate_limit`.** -> **Pod scope only.**
      Keeps the zero-I/O default absolute and no Redis on the hot path. See D11.

## Blocking implementation, not the design
(none)

## Independent spikes (not blocking V2 design)

- [ ] **S1 — DigiFi signature compatibility.** Vendor SDK signs `JSON.stringify(body)`,
      V1 verifies raw bytes (I6). Verify against captured sandbox traffic. Half a day, and
      it changes the cost of every migration option. Pre-existing from the gaps doc.
- [ ] **S2 — Lattice validation.** Hand-write all seven universal-gateway handlers as
      typed-port node graphs on paper before building. An hour; catches a wrong lattice
      early. The four sync proxies are the interesting cases.
