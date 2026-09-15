# Chunk 6: Synchronous Proxy and Transform Nodes

**Status:** implemented (2026-09-15); UG JS differential tests skipped (source not in repo)
**Depends on:** Chunk 5
**Unlocks:** Chunks 7, 8
**Verification gate:** a `curl` through a proxy flow returns the **upstream's** status,
headers, and body — not a Hookway envelope. This is the capability V1 structurally cannot
provide, and it is what makes replacing universal-gateway possible at all.

```bash
# terminal 1: a stub upstream that echoes a distinctive status and body
python3 -m http.server 9999 &   # or any upstream you can assert against

# terminal 2
HOOKWAY_CONFIG=examples/v2/proxy.yaml go run ./cmd/hookway

# terminal 3
curl -sS -i -X PUT 'localhost:8080/salesforce/proxy/api/v1/thing?a=1' \
  -H 'X-Auth: testtoken' --data '{"k":"v"}'
# want: the UPSTREAM's status code and body, the forwarded path /api/v1/thing,
#       the query ?a=1 preserved, and the method PUT preserved
```

**Goal:** Implement `http_request` and `respond` (closing gaps A1 and A6), plus the
`transform` node with the three kinds universal-gateway needs (closing A3).

## Why the proxy ships before the code node

It is the higher-value capability — four of seven handlers need it — and it has no security
surface. goja (Chunk 7) has a security surface and is an escape hatch rather than a
requirement.

## Re-read before starting

- Confirm Chunk 3's `http_trigger` supports `{rest...}` greedy paths and `methods: [ANY]`.
  If it only handles `{seg}`, that work moves here and Task 1 grows.
- Confirm Chunk 3's `envelope` node has `body_format: raw` working, and that
  `body_format: xml` currently returns `errNotImplemented` — Task 4 implements it.
- Confirm Chunk 0's S2 spike decided the DocMagic XML shape (one node with five rules vs
  five chained nodes) and the upstream-OAuth shape. **Task 3 and Task 4 assume S2's
  answers; if the spike was skipped, decide these first.**

---

## Task 1: `http_request`

**Files:** `internal/flowconfig/node_http_request.go`, `node_http_request_test.go`

`Envelope -> Envelope`. Calls an upstream and puts the reply in `Envelope.Upstream`. It is
**not** Terminal and **not** `Detached` — a synchronous proxy must stay on the caller's
request context so their cancellation propagates (`v2/02-design.md` §5).

Options: `url` (templated, supports `{rest}` from the trigger), `forward_method`,
`forward_query`, `forward_headers` (allowlist), `timeout`, `headers` (static additions),
and — per S2 — upstream auth.

Behaviours to implement and test:
- The upstream's status, headers, and body land in `Envelope.Upstream`
- A non-2xx upstream is **not** an error by default — the proxy's job is to pass it through.
  Add `error_on_status: [5xx]` if a flow wants otherwise, but default to passthrough
- `forward_query: true` preserves the inbound query string
- `forward_method: true` preserves the method, including PUT/DELETE/PATCH
- `{rest}` from a `{rest...}` trigger path appends correctly, with no double slash
- `timeout` is honoured and produces a 504, not a 500
- **Hop-count header** is incremented on the outbound request (`config.LoopHopsHeader`), so
  loop protection works through a proxy chain the same way it does for webhook destinations
- A header allowlist governs what is forwarded — reuse
  `cfg.Security.ForwardedHeaderAllowlist`, which V1 already has, rather than inventing a
  second mechanism. **Never forward the inbound `Authorization` header by default**; that
  is a credential-leak path to a third party

- [x] **Step 1: Write the failing tests using `httptest.NewServer` as the upstream**

- [x] **Step 2: Implement, then pass**

Use a package-level `http.Client` with a bounded transport (connection pooling matters — a
proxy under load must not open a socket per request). Look at how
`internal/queue/webhook.go` builds its client and match it.

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_http_request.go internal/flowconfig/node_http_request_test.go
git commit -m "feat(flowconfig): http_request node for upstream calls"
```

---

## Task 2: `respond`

**Files:** `internal/flowconfig/node_respond.go`, `node_respond_test.go`;
modify `internal/httpapi/flowserver.go`

`Envelope -> Terminal`, `Detached: false`. Produces `Terminal.Response`, which
`flowserver.go` (Chunk 3 Task 6, step 9) already knows how to write.

Options: `status`, `headers`, `body` (templated), `passthrough`.

`passthrough: true` is the proxy mode: copy `Envelope.Upstream`'s status, headers, and body
to the caller. Header handling needs care:
- **Strip hop-by-hop headers** — `Connection`, `Keep-Alive`, `Transfer-Encoding`,
  `Upgrade`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`. Copying these
  through produces subtly broken responses
- **Do not copy `Content-Length`** if you are re-writing the body; let Go set it
- Apply a response-header allowlist or denylist so an upstream cannot inject
  `Set-Cookie` into your domain

Response rules already specified in `v2/02-design.md` §5 and enforced by Chunk 2's lint: at
most one `respond` may fire per request; a flow with no `respond` gets the default 202.

- [ ] **Step 1: Write the failing tests**

Cover: explicit status/body; `passthrough` copies upstream status and body; hop-by-hop
headers are stripped; a flow with no `respond` still returns the default 202 with the
delivery summary; two `respond` nodes on different `switch` branches is legal and exactly
one fires.

- [ ] **Step 2: Implement, then pass**

- [ ] **Step 3: Write `examples/v2/proxy.yaml`**

The Salesforce proxy shape from `v2/02-design.md` §7. This is the file the verification gate
runs, so it must be real and working.

- [ ] **Step 4: Run the verification gate by hand**

Run the `curl` sequence at the top of this file. Assert by eye that the upstream's status
and body come back, the method and query survive, and the forwarded path is right.

- [ ] **Step 5: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_respond.go internal/flowconfig/node_respond_test.go \
        internal/httpapi/flowserver.go examples/v2/proxy.yaml
git commit -m "feat(flowconfig): respond node with upstream passthrough"
```

---

## Task 3: `transform` — `changed_fields` and `field_map`

**Files:** `internal/flowconfig/node_transform.go`,
`internal/transform/changed_fields.go`, `internal/transform/field_map.go`, tests

Put the algorithms in a new `internal/transform` package, not in `flowconfig` — they are
pure functions with real logic and deserve their own tests and their own file.

### `changed_fields`

Ports `getChangedFieldsArray` from
`../universal-gateway/digifi_router_webhook_handler/app.js`. Recursively diffs two objects
and emits dot-notation paths:

```
['status', 'variables.borrower_first_name']
```

**The special rule matters and is easy to miss:** any nested `status.*` change also
promotes `status` itself into the array. Read the JS before writing the Go; the promotion
rule is the part a reimplementation gets wrong.

Test with **golden files** derived from real captured payloads (reuse or re-capture from
Chunk 0's S1 fixtures, scrubbed). Table-driven unit tests on synthetic objects will not
catch the cases that matter — nested arrays, nulls, type changes, added and removed keys.

### `field_map`

A declarative path-to-path mapping: read from a source path in the envelope, write to a
destination path. Needed for the general case and much cheaper than a code node.

- [ ] **Step 1: Write the failing tests, golden files first**

- [ ] **Step 2: Implement, then pass**

- [ ] **Step 3: Cross-check against the JS**

Run the original Node implementation and the Go one over the same 20+ captured payloads and
assert identical output. A hand-written test suite will not prove behavioural equivalence;
a differential test will. Record the result.

- [ ] **Step 4: Commit**

```bash
make test && make lint
git add internal/transform/ internal/flowconfig/node_transform.go
git commit -m "feat(transform): changed_fields diff and field_map"
```

---

## Task 4: XML — `body_format: xml` and `transform` kind `xml_prune`

**Files:** `internal/transform/xml_prune.go`, tests; modify
`internal/flowconfig/node_envelope.go`

Ports the five-rule pipeline from
`../universal-gateway/docmagic_xml_proxy_handler/pipeline.js`. The five rules, **in order**
(order is load-bearing — a later rule depends on an earlier one having run):

1. remove empty self-closing elements
2. remove empty parents
3. remove hardcoded-only parties
4. remove blank general partners
5. prune orphaned relationships

Requirements:
- Use `encoding/xml` if it is sufficient; if the rules need real DOM traversal with parent
  pointers, add a pure-Go DOM library and record the dependency choice in
  `v2/README.md`'s decision table. **Do not** shell out to anything
- **Log before and after element counts per rule** — the JS does, and it is the only
  practical way to debug a prune that removed too much
- Whichever shape S2 chose (one node with five rules, or five chained nodes), per-rule
  counts must be visible

- [x] **Step 1: Write tests with synthetic MISMO-shaped XML covering all five rules**

Universal-gateway `pipeline.js` and real DocMagic documents are not in this repo
(user excluded UG). The fixture in `internal/transform/xml_prune_test.go` exercises
empty leaves, empty parents, hardcoded-only parties (attribute and role-only),
blank general partners, surviving P1→P4 relationships, and orphaned P2/P3/GONE
arcs. Predicates are documented in `xml_prune.go` and D14.

- [x] **Step 2: Implement, then pass**

- [x] **Step 3: Differential test against the JS pipeline** — **skipped**. No UG
  JS or golden payloads are available. Do not invent a silent substitute for the
  JS predicates; the V2 contract is the documented rules plus the synthetic fixture.

- [x] **Step 4: Commit**

```bash
make test && make lint
git add internal/transform/xml_prune.go internal/flowconfig/node_envelope.go
git commit -m "feat(transform): xml_prune pipeline and body_format xml"
```

---

## Done when

- [x] The `curl` gate returns the upstream's status, headers, and body (`TestProxyFlowReturnsUpstreamResponse`)
- [x] Method, query, and `{rest...}` path forwarding all verified by test
- [x] Hop-by-hop headers are stripped from passthrough responses
- [x] The inbound `Authorization` header is not forwarded upstream by default
- [x] Outbound requests carry an incremented hop count, so loop protection spans proxies
- [x] `changed_fields` implements gaps A3 (`status.*` promotes `status`). JS differential skipped (UG out of repo)
- [x] `xml_prune` five-rule pipeline on synthetic MISMO-shaped XML. JS/DocMagic differential skipped (UG out of repo)
- [x] Per-rule element counts are logged for `xml_prune`
- [x] `make test-all` green; `make conformance` still green
