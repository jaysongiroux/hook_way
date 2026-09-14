# Chunk 3: Minimal Serving Path — First Traffic

**Status:** not started
**Depends on:** Chunk 2 (config format, registry, stub nodes)
**Unlocks:** Chunks 4, 5, 6
**Verification gate:** a running V2 binary accepts a real signed webhook by `curl`,
delivers it to a destination, and returns `202`. This is the first chunk that serves
traffic.

```bash
# terminal 1
HOOKWAY_CONFIG=examples/v2/github.yaml GITHUB_WEBHOOK_SECRET=testsecret \
  go run ./cmd/hookway

# terminal 2 -- signature computed the way GitHub does it
BODY='{"action":"opened","number":1}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac testsecret -hex | awk '{print $2}')"
curl -sS -i -X POST localhost:8080/webhooks/github \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: $SIG" \
  --data "$BODY"
# want: HTTP 202 and {"status":"accepted",...}

# and the negative case, which matters more
curl -sS -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/webhooks/github \
  -H 'X-Hub-Signature-256: sha256=deadbeef' --data "$BODY"
# want: 401
```

**Goal:** Implement five real nodes — `http_trigger`, `read_body`, `auth`, `envelope`,
`publish` — and wire the graph executor into the HTTP server so a V2 config serves live
traffic on one narrow path.

## Scope discipline

**Only those five nodes.** Gates, filters, lookups, replay, `switch`, `respond`, and
transforms are later chunks. The value of this chunk is proving one complete path end to
end so that every later addition is attributable: if traffic breaks in Chunk 4, it was
Chunk 4.

## Re-read before starting

- Confirm Chunk 2's `decodeOptions[T]` helper and the option struct names
  (`AuthOptions`, `PublishOptions`, …) match what this chunk's code blocks assume.
- Confirm Chunk 1 shipped `graph.NeedsCompensation` / `graph.RunCompensations` and that
  `Flow.Run` returns a populated `Terminal` on the error path — step 9 of Task 6 reads
  `Terminal.Deliveries` to build the 503 body, and step 11 invokes compensation.
- Confirm Q11 resolved to a single `publish` node. If it resolved to one node type per
  destination, Task 5 below changes shape — you will register 13 specs sharing one
  implementation rather than one spec.
- Check Chunk 0's S1 findings. **If DigiFi needs an I6 exception, the `auth` node needs a
  `canonicalize` option and Task 4 must add it.**

## Existing machinery to wrap, not reimplement

Every one of these five nodes is a thin adapter over code that already works and is already
tested. Writing new implementations instead of wrapping is the main way this chunk goes
wrong.

| Node | Wraps | Notes |
|---|---|---|
| `http_trigger` | `internal/vars`' path-pattern matching (`compileEndpointVariables` uses it), `Server.sourceIP` (`server.go:906`), `parseHopCount` (`server.go:880`) | Trusted-proxy handling is subtle and already correct — reuse `sourceIP`, do not rewrite it |
| `read_body` | `readBody` (`server.go:893`) | Already enforces the limit and writes the right error |
| `auth` | `auth.NewVerifier(auth.Spec{...})` (`internal/auth/registry.go`) | All 7 schemes, constant-time comparison (I18), fail-closed on unknown scheme (I11) |
| `envelope` | `webhook.BuildEnvelopeWithOptions` (`internal/webhook/envelope.go`) | Add `body_format` handling around it |
| `publish` | `queue.Publisher` (`internal/queue`) | The existing publisher, built from the shared `destinations:` block |

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/flowconfig/node_trigger.go` | `http_trigger` implementation |
| `internal/flowconfig/node_body.go` | `read_body` |
| `internal/flowconfig/node_auth.go` | `auth`, `no_auth` |
| `internal/flowconfig/node_envelope.go` | `envelope` |
| `internal/flowconfig/node_publish.go` | `publish` |
| `internal/flowconfig/node_*_test.go` | One test file per node |
| `internal/httpapi/flowserver.go` | V2 server: path routing to flows, executor invocation, response writing |
| `internal/httpapi/flowserver_test.go` | `httptest` end-to-end tests |
| `cmd/hookway/main.go` | Modified: `runServe` routes on `schema_version` |

Node implementations move out of `nodes.go` (which keeps only the specs and option structs)
into one file per node, so each stays small and focused. Update `nodes.go`'s spec functions
to construct the real nodes.

---

## Task 1: `http_trigger`

**Files:** `internal/flowconfig/node_trigger.go`, `node_trigger_test.go`

The trigger does **not** read the body. It produces a `graph.RequestHead` from an
`*http.Request`, which is what makes gates provably pre-body (I1–I4).

**Interfaces produced:**
```go
// HeadFromRequest builds the seed payload for a flow. Exported because
// flowserver.go calls it before invoking the executor.
func HeadFromRequest(r *http.Request, pathVars map[string]string, sourceIP string) graph.RequestHead
```

- [ ] **Step 1: Write the failing tests**

Cover: method and path copied through; `{seg}` path variables extracted; `{rest...}`
greedy remainder captured (needed by Chunk 6's proxies, so get the format right now);
query values present (Qualia's query-param auth, gap A5); `Hops` parsed from
`config.LoopHopsHeader`; `RequestID` and `TraceID` populated.

```go
func TestHeadFromRequestCapturesPreBodyFields(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/webhooks/github?authID=abc", strings.NewReader("ignored"))
	r.Header.Set("X-Hub-Signature-256", "sha256=x")
	r.Header.Set(config.LoopHopsHeader, "2")

	head := HeadFromRequest(r, map[string]string{"provider": "github"}, "203.0.113.7")

	if head.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", head.Method)
	}
	if head.Query.Get("authID") != "abc" {
		t.Errorf("Query[authID] = %q, want abc (gap A5 needs query-param auth)", head.Query.Get("authID"))
	}
	if head.Hops != 2 {
		t.Errorf("Hops = %d, want 2", head.Hops)
	}
	if head.SourceIP != "203.0.113.7" {
		t.Errorf("SourceIP = %q, want 203.0.113.7", head.SourceIP)
	}
	if head.PathVars["provider"] != "github" {
		t.Errorf("PathVars[provider] = %q, want github", head.PathVars["provider"])
	}
}

// TestHeadFromRequestDoesNotReadBody is an invariant test: if the trigger
// consumed the body, gate nodes downstream of it would no longer be provably
// pre-buffering, and I1-I4 would be decoration rather than enforcement.
func TestHeadFromRequestDoesNotReadBody(t *testing.T) {
	body := "sentinel-body-content"
	r := httptest.NewRequest(http.MethodPost, "/w", strings.NewReader(body))
	_ = HeadFromRequest(r, nil, "1.2.3.4")

	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("ReadAll() = %v", err)
	}
	if string(got) != body {
		t.Errorf("body was consumed by the trigger: read %q, want the full %q", got, body)
	}
}
```

- [ ] **Step 2: Confirm failure, implement, confirm pass**

For path matching, reuse whatever `internal/vars` already provides — read
`compileEndpointVariables` in `internal/httpapi/server.go:155` and the `path.IsLiteral()`
usage to find the existing pattern type. Add `{rest...}` greedy support if it is missing;
that is new and needs its own test.

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_trigger.go internal/flowconfig/node_trigger_test.go
git commit -m "feat(flowconfig): http_trigger node producing RequestHead"
```

---

## Task 2: `read_body`

**Files:** `internal/flowconfig/node_body.go`, `node_body_test.go`

This node is the phase boundary: `RequestHead -> RawRequest`. It needs the
`*http.Request` body, which is not in `RequestHead` — so the body reader is injected into
the context by `flowserver.go` before the executor runs.

**Interfaces produced:**
```go
type bodyReaderKey struct{}

// WithBodyReader attaches the request body to ctx for the read_body node.
// flowserver.go calls this; nothing else should.
func WithBodyReader(ctx context.Context, r io.Reader) context.Context
```

Passing the reader through context rather than through the payload is deliberate: keeping
it out of `RequestHead` is what guarantees no gate node can reach the body.

- [ ] **Step 1: Write the failing tests**

Cover: a body under the limit is read whole; a body **over** the limit returns an error
naming the limit; an empty body is an error (V1 rejects empty at `server.go:454`); a
missing reader in context is an error, not a panic.

```go
func TestReadBodyRejectsOversizeBody(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil { t.Fatal(err) }
	spec, _ := reg.Lookup("read_body")
	node, err := spec.New(nodeConfigFromYAML(t, "id: body\ntype: read_body\nmax_bytes: 10\n"))
	if err != nil { t.Fatal(err) }

	ctx := WithBodyReader(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	_, err = node.Run(ctx, graph.RequestHead{})
	if err == nil {
		t.Fatal("Run() = nil for a 100-byte body with max_bytes: 10")
	}
	if !strings.Contains(err.Error(), "10") {
		t.Errorf("error %q should name the limit so the operator can raise it", err)
	}
}
```

- [ ] **Step 2: Confirm failure, implement, confirm pass**

Use `io.LimitReader` with `max_bytes + 1` and error if you read more than `max_bytes` —
that distinguishes "exactly at the limit" (allowed) from "over" (rejected). Reuse
`readBody`'s logic from `server.go:893`.

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_body.go internal/flowconfig/node_body_test.go
git commit -m "feat(flowconfig): read_body node as the RequestHead->RawRequest boundary"
```

---

## Task 3: `auth` and `no_auth`

**Files:** `internal/flowconfig/node_auth.go`, `node_auth_test.go`

A thin adapter over `auth.NewVerifier`. **Do not reimplement any scheme.**

- [ ] **Step 1: Write the failing tests**

Cover, at minimum:
- a valid `hmac_sha256_hex` signature passes and yields a `graph.Verified`
- an invalid signature fails, and the error does not leak the expected value
- **an unknown scheme fails closed** — every request rejected, never accepted (I11).
  Build the node with `scheme: not_a_real_scheme` and assert `Run` errors; mirror V1's
  `auth.RejectAll` fallback at `server.go:126`
- **verification is against raw bytes** (I6): sign a body with significant whitespace, then
  verify the identical bytes. If the node ever re-serialized, this fails
- query-param source works (gap A5): `source: {kind: query, key: authID}`
- `no_auth` produces `Verified` and its construction logs a warning

```go
// TestAuthVerifiesRawBytesNotReserialized pins invariant I6. The body has
// whitespace and key ordering that JSON re-serialization would normalize
// away; if the node canonicalizes, the signature will not match.
func TestAuthVerifiesRawBytesNotReserialized(t *testing.T) {
	const secret = "shh"
	raw := []byte("{\n  \"z\": 1,\n  \"a\": 2\n}")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))

	node := mustAuthNode(t, "scheme: hmac_sha256_hex\nsecret: shh\nsignature_header: X-Sig\n")
	in := graph.RawRequest{
		Head: graph.RequestHead{Header: http.Header{"X-Sig": []string{sig}}},
		Body: raw,
	}
	out, err := node.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run() = %v; I6 requires verification against the raw bytes", err)
	}
	if out.PortType() != graph.PortVerified {
		t.Errorf("PortType() = %s, want Verified", out.PortType())
	}
}

// TestAuthUnknownSchemeFailsClosed mirrors V1's RejectAll fallback: a
// misconfigured scheme must reject every request, never accept unverified ones.
func TestAuthUnknownSchemeFailsClosed(t *testing.T) {
	reg, _ := NewRegistry()
	spec, _ := reg.Lookup("auth")
	node, err := spec.New(nodeConfigFromYAML(t,
		"id: a\ntype: auth\nscheme: not_a_real_scheme\nsecret: s\nsignature_header: X-S\n"))
	// Either construction fails, or it succeeds and Run rejects everything.
	// Both are fail-closed; accepting the request is not.
	if err != nil {
		return
	}
	if _, err := node.Run(context.Background(), graph.RawRequest{}); err == nil {
		t.Fatal("unknown scheme accepted a request; must fail closed (I11)")
	}
}
```

- [ ] **Step 2: Confirm failure, implement, confirm pass**

Copy `AuthOptions` fields straight onto `auth.Spec` — Chunk 2 mirrored the field names for
exactly this reason. Apply `defaultScheme` (`server.go:1144`) so an empty scheme keeps V1's
default. The `source` option is new: header (default) or query, reading from
`RawRequest.Head.Header` or `.Query`.

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_auth.go internal/flowconfig/node_auth_test.go
git commit -m "feat(flowconfig): auth and no_auth nodes over the existing verifier registry"
```

---

## Task 4: `envelope`

**Files:** `internal/flowconfig/node_envelope.go`, `node_envelope_test.go`

`Verified -> Envelope`. Wraps `webhook.BuildEnvelopeWithOptions` and adds `body_format`.

- [ ] **Step 1: Write the failing tests**

Cover: `body_format: json` parses into `Envelope.Body`; `body_format: raw` leaves `Body`
nil and preserves the bytes (needed by Chunk 6's proxies); `body_format: json` with a
non-JSON body does **not** error — V1 tolerates it and filters handle it via the
`body_unparsed` path (`server.go:186`), so match that; header redaction still applies via
the existing envelope options.

- [ ] **Step 2: Confirm failure, implement, confirm pass**

`body_format: xml` may return `errNotImplemented` in this chunk — XML lands in Chunk 6.
Add a test asserting it errors clearly rather than silently doing nothing.

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_envelope.go internal/flowconfig/node_envelope_test.go
git commit -m "feat(flowconfig): envelope node with body_format"
```

---

## Task 5: `publish`

**Files:** `internal/flowconfig/node_publish.go`, `node_publish_test.go`

`Envelope -> Terminal`, and the only node with `Detached: true` (I10).

**Interfaces produced:**
```go
// PublisherResolver hands a publish node its destination at construction.
// flowserver.go supplies the real one; tests supply a fake.
type PublisherResolver interface {
    Resolve(destinationID string) (queue.Publisher, error)
}
```

The resolver is injected rather than looked up globally because it keeps the node unit
testable with a fake publisher, and because it is the seam that guarantees one connection
per `destinations:` entry rather than one per node.

- [ ] **Step 1: Write the failing tests**

Cover: a successful publish returns `Terminal` with one `Delivery`; a publish error is
returned and recorded; `target` templating with `{var}` renders from `Envelope.Vars`
(Chunk 4's `lookup` depends on this); **the `Delivery.Target` label is the configured
template, not the rendered value** (scalability rule R3).

```go
// TestPublishLabelsWithTemplateNotRenderedTarget guards R3. If Target carried
// the rendered value, a DynamoDB routing table with 5,000 environments would
// produce 5,000 Prometheus label values.
func TestPublishLabelsWithTemplateNotRenderedTarget(t *testing.T) {
	fake := &fakePublisher{}
	node := mustPublishNode(t, "destination: primary\ntarget: \"{route}-queue\"\n",
		staticResolver{pub: fake})

	env := graph.Envelope{Vars: map[string]string{"route": "env-4711"}}
	out, err := node.Run(context.Background(), env)
	if err != nil { t.Fatalf("Run() = %v", err) }

	if fake.lastTarget != "env-4711-queue" {
		t.Errorf("published to %q, want the RENDERED target env-4711-queue", fake.lastTarget)
	}
	term := out.(graph.Terminal)
	if term.Deliveries[0].Target != "{route}-queue" {
		t.Errorf("Delivery.Target = %q, want the TEMPLATE {route}-queue: "+
			"metric labels must be bounded by config, not data (R3)",
			term.Deliveries[0].Target)
	}
}
```

- [ ] **Step 2: Confirm failure, implement, confirm pass**

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_publish.go internal/flowconfig/node_publish_test.go
git commit -m "feat(flowconfig): publish node with injected publisher resolver"
```

---

## Task 6: Wire the executor into the HTTP server

**Files:** `internal/httpapi/flowserver.go`, `flowserver_test.go`; modify `cmd/hookway/main.go`

**Interfaces produced:**
```go
type FlowServer struct { /* unexported */ }

func NewFlowServer(cfg flowconfig.Config, pub queue.Publisher, logger *slog.Logger,
                   m *metrics.Metrics) (*FlowServer, error)

func (s *FlowServer) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (s *FlowServer) HTTPServer() *http.Server
func (s *FlowServer) Close() error
```

This is a **new file, not a modification of `server.go`.** V1's `Server` must keep working
until Chunk 9. Share the small helpers (`writeJSON`, `sourceIP`, `requestID`,
`traceIDFromRequest`, `recoverHandlerPanic`) by calling them — they are already in the same
package.

### What `ServeHTTP` must do, in order

This ordering is not arbitrary; it reproduces the parts of `handleWebhook` that the graph
does **not** own, in the same sequence. Reread `v2/02-design.md` §3 — these are the
"scope, not step" concerns that deliberately stayed outside the graph.

1. **Panic recovery** wrapping everything (I16) — reuse `recoveringResponseWriter`.
2. **Match a flow by path.** Literal paths first, then patterns, exactly as
   `matchEndpoint` (`server.go:1204`) does. No match is 404.
3. **Method check** against the trigger's `methods`. Mismatch is 405 with an `Allow` header.
4. **Acquire the inflight semaphore** (I2). Per `v2/02-design.md` §3 this is an executor-
   level scope, not a node, and acquiring it here — before the body is read — satisfies I2
   more strongly than a node could. Release on completion.
5. **Resolve source IP** via the existing `sourceIP` helper, so trusted-proxy handling stays
   identical.
6. **Build the seed `RequestHead`** with `flowconfig.HeadFromRequest`.
7. **Attach the body reader** with `flowconfig.WithBodyReader`.
8. **Run the flow.** `flow.Run(ctx, head, graph.RunOptions{...})` with `PublishTimeout`
   from `cfg.Server.PublishTimeout`, `FanoutConcurrency` from
   `cfg.Server.FanoutConcurrencyLimit()`, and `OnNode` wired to metrics and logs.
9. **Write the response.** If `Terminal.Response` is non-nil, write it. Otherwise write
   V1's default `202` with the delivery summary — this is what keeps `respond` purely
   additive (`v2/02-design.md` §5).
10. **Map errors to status codes.** An auth node error is 401, a `read_body` limit error is
    413, a publish failure is 503 so the sender retries (gaps doc C1 — this is the single
    most important behaviour in the migration), anything else is 500.
11. **Run compensations** if `graph.NeedsCompensation(term, err)`. No node implements
    `graph.Compensator` yet — `replay_guard` arrives in Chunk 4 — but wire the call site
    **now**, while there is one narrow path to reason about. Retrofitting it later means
    auditing every error return in a much larger handler. See `01-graph-core.md` Task 7's
    compensation section and decision D12.

Error mapping needs a typed error, not string matching. Define in `internal/graph`:

```go
// NodeError wraps a node failure with the HTTP status the server should
// return. Nodes return these so the server does not string-match error text.
type NodeError struct {
    NodeID string
    Status int
    Err    error
}
```
and have each node return one. String-matching error messages to pick a status code will
rot the first time someone rewords an error.

- [ ] **Step 1: Write the failing end-to-end tests**

Use `httptest.NewServer` with a real compiled flow and a fake publisher. Cover:
valid signed request → 202 and the fake publisher saw the body; bad signature → 401;
oversize body → 413; publish failure → **503** (assert explicitly — this is C1); unknown
path → 404; wrong method → 405 with `Allow`; panicking node → 500, not a dropped
connection.

```go
// TestPublishFailureReturns503 is the migration's headline behaviour. The
// system Hookway replaces returns 200 on publish failure and loses the event
// permanently (gaps doc C1). Returning 503 is what makes the sender retry.
func TestPublishFailureReturns503(t *testing.T) {
	srv := newTestFlowServer(t, withPublishError(errors.New("broker down")))
	rec := srv.post(t, "/webhooks/github", validBody, validSig)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: a publish failure must make the sender retry, "+
			"never be swallowed as a success (gaps doc C1)", rec.Code)
	}
}
```

- [ ] **Step 2: Confirm failure, implement, confirm pass**

- [ ] **Step 3: Route `runServe` on `schema_version`**

In `cmd/hookway/main.go:237`, detect the version. Version 1 keeps the existing
`httpapi.NewServer` path untouched; version 2 builds a `FlowServer`. Keep the readiness
prober, metrics server, and graceful shutdown wiring identical for both — those are not
part of this chunk's change.

- [ ] **Step 4: Run the verification gate**

Run the `curl` sequence from the top of this file against a real running binary. Both the
202 and the 401 must be observed by hand, not only in tests — the point of this chunk is
proving the wiring works outside `httptest`.

- [ ] **Step 5: Commit**

```bash
make test && make lint
git add internal/httpapi/flowserver.go internal/httpapi/flowserver_test.go cmd/hookway/main.go
git commit -m "feat(httpapi): serve schema_version 2 flows through the graph executor"
```

---

## Done when

- [ ] The compensation call site exists in `flowserver.go` even though no node uses it yet
- [ ] The `curl` gate at the top of this file produces 202 for a valid signature
- [ ] The same gate produces 401 for an invalid signature
- [ ] A publish failure produces **503**, verified by test
- [ ] Oversize body produces 413; unknown path 404; wrong method 405 with `Allow`
- [ ] A panicking node produces 500 and the process survives
- [ ] `Delivery.Target` carries the configured template, never the rendered value
- [ ] **V1 still serves**: `HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway` works
- [ ] `make test && make lint` clean
