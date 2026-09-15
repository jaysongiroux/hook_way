# Chunk 7: The `code` Node (goja)

**Status:** implemented (2026-09-15); unsigned local commit pending
**Depends on:** Chunk 6
**Unlocks:** Chunk 9
**Verification gate:** **each sandbox control C1–C7 has a test proving the control actually
fires.** A test asserting "normal script works" proves nothing about a sandbox.

```bash
go test ./internal/codenode/ -v -timeout 60s   # every control, named C1..C7
go test -race ./internal/codenode/
make vuln-scan                                  # goja is a new dependency
```

**Goal:** Add a `code` node running operator-authored JavaScript on goja, with the sandbox
controls from `v2/02-design.md` §6 — and with the memory-cap limitation documented rather
than papered over.

## Read this first: the trust model this rests on

From `v2/02-design.md` §6, and it determines how much hardening is warranted:

> A `code` node is authored by **whoever writes the flow config**. That person can already
> point a destination at an arbitrary URL, set arbitrary secrets, and disable auth. They are
> inside the trust boundary. So the sandbox defends against **operator accidents** —
> infinite loops, runaway allocation, accidental blocking — not against a hostile tenant.

**If Hookway ever becomes multi-tenant or hosted, where flow authors are not trusted
operators, this conclusion does not hold** and the code node must move to WASM with real
resource isolation. If you are reading this in a world where that has happened, stop and
escalate rather than hardening goja further.

## Re-read before starting

- Confirm `graph.Envelope`'s shape — the script's input and output surface is derived from
  it, and anything Chunks 3–6 added to it (e.g. `Upstream`) needs a decision about whether
  scripts can see or modify it.
- Confirm whether Chunk 6's `transform` node already covers the real use cases. **If
  `changed_fields`, `field_map`, and `xml_prune` cover everything the seven handlers need,
  consider deferring this chunk.** It is an escape hatch for the long tail, not a
  requirement for the migration.

## What this node is explicitly NOT for

The two transformations universal-gateway needs are specific algorithms and shipped as Go
`transform` nodes in Chunk 6. Do **not** reimplement them in JS:

- goja has no DOM, so XML would need a host-provided XML API — which reintroduces exactly
  the host-object surface control C3 exists to remove
- a tested Go function beats a JS string in YAML for something that runs on every event

---

## Task 1: Add goja and build the sandboxed runtime

**Files:** `internal/codenode/runtime.go`, `runtime_test.go`; modify `go.mod`

```bash
go get github.com/dop251/goja@latest
```

goja is pure Go with no cgo, which keeps the `FROM scratch` image (`Dockerfile:23`) intact.
Confirm that: `make docker-build && make vuln-scan` must still pass. Record the pinned
version in `v2/README.md`'s decision table.

**Interfaces produced:**
```go
type Limits struct {
    Timeout        time.Duration // C1
    MaxCallStack   int           // C2
    MaxInputBytes  int           // C4
    MaxOutputBytes int           // C5
}

type Runtime struct { /* unexported */ }

func NewRuntime(script string, l Limits) (*Runtime, error) // compiles once at config load
func (rt *Runtime) Run(ctx context.Context, input any) (any, error)
```

Compile the script **once at config load**, not per request — `goja.Compile` returns a
`*goja.Program` that can be run on a fresh `*goja.Runtime`. A syntax error must fail
`hookway validate`, not the first request.

- [x] **Step 1: Write one failing test per control, named for it**

- [x] **Step 2: Confirm every test fails, then implement**

- [x] **Step 3: Confirm all pass, including under `-race`**

- [ ] **Step 4: Commit**

Each test must prove the control *fires*, not that normal operation works.

```go
// TestC1_TimeoutInterruptsInfiniteLoop proves control C1. An operator
// accidentally writing `while(true){}` must fail that one request, not hang a
// goroutine forever.
func TestC1_TimeoutInterruptsInfiniteLoop(t *testing.T) {
	rt, err := NewRuntime("while (true) {}", Limits{Timeout: 100 * time.Millisecond,
		MaxCallStack: 1000, MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	if err != nil { t.Fatal(err) }

	start := time.Now()
	_, err = rt.Run(context.Background(), map[string]any{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run() = nil for an infinite loop; C1 (timeout) did not fire")
	}
	if elapsed > time.Second {
		t.Errorf("took %v to interrupt a 100ms budget; the watchdog is not working", elapsed)
	}
}

// TestC2_DeepRecursionHitsStackLimit proves control C2.
func TestC2_DeepRecursionHitsStackLimit(t *testing.T) {
	rt, _ := NewRuntime("function f(n){return f(n+1)} f(0)", testLimits())
	if _, err := rt.Run(context.Background(), nil); err == nil {
		t.Fatal("unbounded recursion did not error; C2 (call stack bound) did not fire")
	}
}

// TestC3_NoHostObjectsAreReachable proves control C3 -- the most important
// one. Each of these must be undefined inside the sandbox.
func TestC3_NoHostObjectsAreReachable(t *testing.T) {
	forbidden := []string{
		"require", "process", "globalThis.process", "fetch", "XMLHttpRequest",
		"setTimeout", "setInterval", "Buffer", "__dirname", "module", "exports",
	}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			rt, err := NewRuntime("typeof "+name, testLimits())
			if err != nil { t.Fatal(err) }
			out, err := rt.Run(context.Background(), nil)
			if err != nil { t.Fatalf("Run() = %v", err) }
			if out != "undefined" {
				t.Errorf("typeof %s = %v, want undefined: the sandbox must expose no host objects", name, out)
			}
		})
	}
}

// TestC6_RuntimeIsFreshPerRequest proves control C6: no state may leak between
// requests, so one event cannot poison the next.
func TestC6_RuntimeIsFreshPerRequest(t *testing.T) {
	rt, _ := NewRuntime("globalThis.leaked = (globalThis.leaked || 0) + 1; globalThis.leaked", testLimits())
	first, err := rt.Run(context.Background(), nil)
	if err != nil { t.Fatal(err) }
	second, err := rt.Run(context.Background(), nil)
	if err != nil { t.Fatal(err) }
	if first != second {
		t.Errorf("global state leaked between runs: got %v then %v; C6 requires a fresh runtime", first, second)
	}
}

// TestC7_ScriptPanicDoesNotKillTheProcess proves control C7.
func TestC7_ScriptPanicDoesNotKillTheProcess(t *testing.T) {
	rt, _ := NewRuntime("null.foo.bar", testLimits())
	if _, err := rt.Run(context.Background(), nil); err == nil {
		t.Fatal("a TypeError did not surface as an error")
	}
	// Surviving to this line is the assertion.
}
```

Also write C4 (oversize input refused) and C5 (oversize output refused).

- [ ] **Step 2: Confirm every test fails, then implement**

Implementation notes that matter:

- **C1** — run a watchdog goroutine that calls `runtime.Interrupt(...)` after the timeout,
  and `runtime.ClearInterrupt()` before reuse. The interrupt is checked between statements,
  so a single long-running native call (a huge regex) may not be interruptible. Note that
  limitation in the docs alongside the memory one.
- **C1 + ctx** — also interrupt when `ctx` is done, so a caller disconnect stops the script.
- **C2** — `runtime.SetMaxCallStackSize(l.MaxCallStack)`.
- **C3** — a bare `goja.New()` already has no `require`, `fetch`, or timers. The risk is
  *adding* them later for convenience. Make C3's test the guard and say so in a comment.
- **C6** — fresh `goja.New()` per `Run`, reusing the compiled `*goja.Program`. Measure the
  allocation cost; if it is material, a `sync.Pool` of runtimes is **not** a safe
  substitute because pooled runtimes retain global state. Prefer slower and correct.
- **C4/C5** — measure serialized size, and reject rather than truncate. Truncation produces
  silently corrupt payloads.

- [ ] **Step 3: Confirm all pass, including under `-race`**

- [ ] **Step 4: Commit**

```bash
make test && make lint && make vuln-scan
git add go.mod go.sum internal/codenode/
git commit -m "feat(codenode): sandboxed goja runtime with controls C1-C7"
```

---

## Task 2: Wire the `code` node

**Files:** `internal/flowconfig/node_code.go`, `node_code_test.go`; modify `nodes.go`

`Envelope -> Envelope`. Options: `script`, `timeout` (default 1s),
`max_output_bytes` (default 1 MB), `max_call_stack` (default 1000).

The script contract — keep it small and document it in `docs/configuration.md`:

```js
// `input` is provided; return the new body, or an object {body, vars}.
// There is no I/O, no require, no timers, and no access to other flows.
return { body: input.body, vars: { computed: String(input.body.id).toUpperCase() } };
```

Decide and document what the script can see and mutate. Recommendation: give it
`{body, vars, headers}` as **deep copies**, and accept back only `{body, vars}`. Do not
expose `Verified`, the raw bytes, or the secret — a script has no business reading
credentials, and copies prevent a script mutating state other nodes already read.

- [ ] **Step 1: Write the failing tests**

Cover: a script transforming the body; a script setting a var that a downstream `publish`
target template consumes; a script returning a non-object errors clearly; a syntax error
fails at **config load** (so `hookway validate` catches it), not at first request; the
script cannot see `Verified.Identity` or the auth secret.

That last one deserves an explicit test — it is a real leak path if the input object is
built carelessly.

- [ ] **Step 2: Implement, then pass**

- [ ] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_code.go internal/flowconfig/node_code_test.go internal/flowconfig/nodes.go
git commit -m "feat(flowconfig): code node on the sandboxed goja runtime"
```

---

## Task 3: Document the limitations honestly

**Files:** modify `docs/configuration.md`, `docs/security.md`, `v2/README.md`

Per `v2/02-design.md` §6, this must be **written down, not discovered later**:

- [ ] **Step 1: Document the memory gap**

> goja has no hard memory cap. Controls C1–C7 bound time, call stack, host access, and
> payload size, but a script doing `let a=[]; while(1) a.push(1)` allocates until the
> timeout interrupts it — which may be after significant allocation. Hookway is a single
> process, so that pressure is shared with every other flow. Mitigate with per-pod memory
> limits plus liveness restart, and deploy split by trust domain for untrusted flows.

- [ ] **Step 2: Document the interrupt gap**

A single long-running native operation (a pathological regex) may not be interruptible
between statements. Recommend against complex regexes in code nodes and point at the
`filter` node instead.

- [ ] **Step 3: Document the trust model**

State plainly that the code node assumes flow authors are trusted operators, and that
multi-tenant use requires a different design. This is the sentence that stops someone
building a hosted product on it by accident.

- [ ] **Step 4: Add the lint rule cross-reference**

Chunk 2's lint warns on a `code` node with no explicit `timeout`. Make sure its message
points at these docs.

- [ ] **Step 5: Commit**

```bash
git add docs/ v2/README.md
git commit -m "docs: code node sandbox limitations and trust model"
```

---

## Done when

- [x] Every control C1–C7 has a named test that proves the control fires
- [x] `go test -race ./internal/codenode/` clean
- [x] A script syntax error fails `hookway validate`, not the first request
- [x] A script cannot read `Verified.Identity` or any auth secret — tested
- [x] Global state does not leak between requests — tested
- [x] Scratch image still builds (`docker build`, binary + CA bundle). `make vuln-scan` is not clean: 2 pre-existing HIGH (amqp091-go CVE-2026-79921, grpc CVE-2026-84445); goja added none
- [x] The memory gap, the interrupt gap, and the trust model are all documented
- [x] `make conformance` still green
