# Chunk 1: Graph Core

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Commit policy for this branch:** the user's standing instruction is **do not commit
> without asking**. Each task below ends with a commit step because that is the correct
> TDD rhythm — confirm with the user before running the first one, then proceed normally.

**Status:** not started
**Depends on:** Chunk 0 (spikes) — gate only; no code dependency
**Unlocks:** every other chunk
**Verification gate:** `go test -race ./internal/graph/` passes, coverage > 85%, and a config
wiring a gate node after `read_body` fails to compile with a type error naming both ports.

**Goal:** Build `internal/graph` — a typed-port DAG engine that compiles a
`schema_version: 2` flow config into an executable graph and rejects illegally-wired
graphs at load time.

**Architecture:** Each node declares an input and an output `PortType`. Edges are
type-checked at compile time with **strict equality**, which makes V1's phase-ordering
invariants (I1–I9) type errors rather than lint warnings. Execution is in-memory per
request with no persistence, so writes per event stay independent of node count.

**Tech Stack:** Go 1.25.4, `gopkg.in/yaml.v3`, `golang.org/x/sync/errgroup`. No new
third-party dependencies in this plan.

**Spec:** `v2/02-design.md` (design), `v2/03-scalability.md` (write budget),
`v2/00-v1-baseline.md` (invariant inventory I1–I18, weaknesses W1–W5).

## Global Constraints

- Go module is `github.com/jasongiroux/hookway`; Go directive `1.25.4`.
- **No new third-party dependencies in Chunk 1.** goja arrives in Chunk 7.
- **Port assignability is strict equality.** `RawRequest` must never satisfy
  `RequestHead`. This single rule is what enforces I1–I4; a test asserts it directly.
- **No per-node persistence and no per-node database writes** (D8). Execution state is
  in-memory for the life of the request.
- **Metric and log labels must be bounded by config, never by data** (R3). Use node ids
  and configured templates, never rendered targets.
- Fail closed on every ambiguity, matching V1's posture (I11, I12): an unknown node type,
  a duplicate id, or an unresolvable edge is a hard compile error, never a warning.
- Tests use the standard library `testing` package and table-driven subtests, matching the
  existing style in `internal/auth` and `internal/config`.
- Run `make test` and `make lint` before every commit.

---

## Scope

See `v2/plans/00-INDEX.md` for the full chunk sequence and dependency graph.

**This chunk explicitly excludes:** `switch` nodes, `on_error` ports, the `skip_failed`
fan-out policy, and every real node implementation. The executor here supports a
sequential walk plus parallel fan-out on the default port under `stop_on_failure`. The rest
lands in Chunks 4 and 5 once real nodes exist to exercise it.

## File Structure

| File | Responsibility |
|---|---|
| `internal/graph/port.go` | `PortType` enum and the strict-equality `Accepts` rule |
| `internal/graph/port_test.go` | Asserts strict equality, incl. the `RawRequest`/`RequestHead` case |
| `internal/graph/payload.go` | The five payload types and the `Payload` interface |
| `internal/graph/payload_test.go` | Each payload reports the right `PortType` |
| `internal/graph/registry.go` | `Node`, `NodeSpec`, `Registry` |
| `internal/graph/registry_test.go` | Registration, duplicate rejection, lookup |
| `internal/graph/model.go` | Config structs and edge-string parsing |
| `internal/graph/model_test.go` | Edge parse table, including malformed input |
| `internal/graph/compile.go` | `Compile` — the eight structural checks and `CompileError` |
| `internal/graph/compile_test.go` | One subtest per check, plus the I1–I4 regression case |
| `internal/graph/executor.go` | `Flow.Run` — walk, fan-out, detached context for destinations |
| `internal/graph/executor_test.go` | Ordering, fan-out, error propagation, context detachment |
| `internal/graph/testnodes_test.go` | Shared in-package test nodes (`_test.go`, never shipped) |

Splitting by responsibility rather than layer: `compile.go` and `executor.go` change for
different reasons and are independently reviewable. All files stay well under the
1,229-line weight of `internal/httpapi/server.go`, which is the size this design exists to
break up.

---

## Spikes moved

The S1 (DigiFi signature) and S2 (lattice validation) spikes previously lived here. They
are now **Chunk 0** — see `00-spikes.md`. Run them before Task 1: S2 can amend the port
lattice this chunk implements verbatim, and S1 can change the `auth` node contract.

---

## Task 1: Port types and the strict-equality rule

**Files:**
- Create: `internal/graph/port.go`
- Test: `internal/graph/port_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `PortType` (a `uint8` enum) with constants `PortNone`, `PortRequestHead`,
  `PortRawRequest`, `PortVerified`, `PortEnvelope`, `PortTerminal`; methods
  `func (PortType) String() string` and `func (PortType) Accepts(out PortType) bool`.

- [ ] **Step 1: Write the failing test**

```go
package graph

import "testing"

// TestAcceptsIsStrictEquality is the load-bearing test of the whole V2 design.
// RawRequest carries a RequestHead, so the intuitive choice is to make it
// assignable to RequestHead. It must not be: if a gate node (rate_limit,
// ip_allowlist, loop_guard) can be wired downstream of read_body, then
// invariants I1-I4 -- gates run before the body is buffered -- silently
// evaporate, and typed ports lose the property they were chosen for.
func TestAcceptsIsStrictEquality(t *testing.T) {
	all := []PortType{PortNone, PortRequestHead, PortRawRequest, PortVerified, PortEnvelope, PortTerminal}
	for _, in := range all {
		for _, out := range all {
			want := in == out
			if got := in.Accepts(out); got != want {
				t.Errorf("PortType(%s).Accepts(%s) = %v, want %v", in, out, got, want)
			}
		}
	}
}

// TestRawRequestIsNotARequestHead names the specific regression that
// TestAcceptsIsStrictEquality exists to prevent, so a failure reads as the
// invariant it breaks rather than as an enum mismatch.
func TestRawRequestIsNotARequestHead(t *testing.T) {
	if PortRequestHead.Accepts(PortRawRequest) {
		t.Fatal("PortRequestHead accepts PortRawRequest: a gate node could now be wired " +
			"after read_body, breaking invariants I1-I4 (gates before body buffering)")
	}
}

func TestPortTypeString(t *testing.T) {
	cases := map[PortType]string{
		PortNone:        "None",
		PortRequestHead: "RequestHead",
		PortRawRequest:  "RawRequest",
		PortVerified:    "Verified",
		PortEnvelope:    "Envelope",
		PortTerminal:    "Terminal",
		PortType(99):    "PortType(99)",
	}
	for pt, want := range cases {
		if got := pt.String(); got != want {
			t.Errorf("PortType(%d).String() = %q, want %q", pt, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/graph/ -run TestAccepts -v`
Expected: FAIL to build — `undefined: PortType`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package graph implements Hookway's typed-port node graph: the compiler that
// turns a flow config into an executable DAG, and the executor that runs it.
//
// Each node declares the payload type it consumes and the one it produces.
// Edges are type-checked when the config loads, and the type lattice is chosen
// so that V1's phase-ordering invariants are compile errors rather than lint
// warnings. See v2/02-design.md.
package graph

import "fmt"

// PortType identifies the payload type carried on a graph edge. The values
// are the phases of a request, in order.
type PortType uint8

const (
	// PortNone is the input port of a trigger: it consumes nothing.
	PortNone PortType = iota
	// PortRequestHead carries method, path, query, headers, source IP and hop
	// count -- and deliberately NO body, so that gate nodes operating on it
	// provably run before the body is buffered.
	PortRequestHead
	// PortRawRequest carries a RequestHead plus raw, unverified body bytes.
	PortRawRequest
	// PortVerified carries a RawRequest whose signature has been checked
	// against the raw bytes.
	PortVerified
	// PortEnvelope carries a Verified payload plus the parsed body and
	// resolved variables. This is what publishes.
	PortEnvelope
	// PortTerminal marks a leaf: a response was written or a delivery
	// completed, and nothing flows onward.
	PortTerminal
)

func (p PortType) String() string {
	switch p {
	case PortNone:
		return "None"
	case PortRequestHead:
		return "RequestHead"
	case PortRawRequest:
		return "RawRequest"
	case PortVerified:
		return "Verified"
	case PortEnvelope:
		return "Envelope"
	case PortTerminal:
		return "Terminal"
	default:
		return fmt.Sprintf("PortType(%d)", uint8(p))
	}
}

// Accepts reports whether an input port of type p may take an edge from an
// output port of type out.
//
// This is STRICT EQUALITY, and deliberately so. PortRawRequest contains a
// RequestHead, so widening this to an assignability relation is the obvious
// refactor -- and it would let a gate node be wired downstream of read_body,
// which destroys invariants I1-I4. Do not "simplify" this. See
// TestRawRequestIsNotARequestHead.
func (p PortType) Accepts(out PortType) bool { return p == out }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/graph/ -v`
Expected: PASS — three tests.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/graph/port.go internal/graph/port_test.go
git commit -m "feat(graph): port types with strict-equality assignability"
```

---

## Task 2: Payload types

**Files:**
- Create: `internal/graph/payload.go`
- Test: `internal/graph/payload_test.go`

**Interfaces:**
- Consumes: `PortType` and its constants from Task 1.
- Produces: `Payload` interface with method `PortType() PortType`; structs `RequestHead`,
  `RawRequest`, `Verified`, `Envelope`, `Terminal`; supporting structs `Response`,
  `Delivery`, `UpstreamResponse`. Every payload is passed and returned **by value**.

- [ ] **Step 1: Write the failing test**

```go
package graph

import (
	"net/http"
	"net/url"
	"testing"
)

func TestPayloadsReportTheirPortType(t *testing.T) {
	cases := []struct {
		name string
		p    Payload
		want PortType
	}{
		{"RequestHead", RequestHead{}, PortRequestHead},
		{"RawRequest", RawRequest{}, PortRawRequest},
		{"Verified", Verified{}, PortVerified},
		{"Envelope", Envelope{}, PortEnvelope},
		{"Terminal", Terminal{}, PortTerminal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.PortType(); got != tc.want {
				t.Errorf("PortType() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestRawRequestNestsHeadByField pins the structural choice that backs the
// strict-equality rule: RawRequest holds its RequestHead in a named field
// rather than embedding it, so Go's own assignability rules cannot be used to
// pass a RawRequest where a RequestHead is expected either.
func TestRawRequestNestsHeadByField(t *testing.T) {
	raw := RawRequest{
		Head: RequestHead{Method: http.MethodPost, Path: "/webhooks/github"},
		Body: []byte(`{"a":1}`),
	}
	if raw.Head.Method != http.MethodPost {
		t.Errorf("Head.Method = %q, want POST", raw.Head.Method)
	}
	var _ Payload = raw
}

func TestRequestHeadCarriesQueryForQueryParamAuth(t *testing.T) {
	// Qualia authenticates from the query string (gaps doc A5), so the
	// pre-body phase must carry query values.
	head := RequestHead{Query: url.Values{"authID": []string{"secret"}}}
	if got := head.Query.Get("authID"); got != "secret" {
		t.Errorf("Query.Get(authID) = %q, want %q", got, "secret")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/graph/ -run TestPayload -v`
Expected: FAIL to build — `undefined: RequestHead`.

- [ ] **Step 3: Write minimal implementation**

```go
package graph

import (
	"net/http"
	"net/url"
)

// Payload is the value carried on a graph edge. Implementations are the five
// request phases; each reports the PortType it corresponds to so the executor
// can assert at runtime what the compiler already proved statically.
type Payload interface {
	PortType() PortType
}

// RequestHead is the pre-body phase: everything known about a request before
// its body has been buffered. Gate nodes consume and produce this, which is
// what makes "gates run before the body is read" (I1-I4) a type property.
type RequestHead struct {
	Method   string
	Path     string
	PathVars map[string]string
	Query    url.Values
	Header   http.Header
	SourceIP string
	Hops     int
	RequestID string
	TraceID   string
}

func (RequestHead) PortType() PortType { return PortRequestHead }

// RawRequest adds unverified body bytes. Head is a named field rather than an
// embedded type on purpose: embedding would make RawRequest assignable where a
// RequestHead is expected in plain Go, undermining the port rule at the
// language level. See TestRawRequestNestsHeadByField.
type RawRequest struct {
	Head RequestHead
	Body []byte
}

func (RawRequest) PortType() PortType { return PortRawRequest }

// Verified is a RawRequest whose signature has been checked against the raw
// bytes (I6 -- never against a re-serialized body). Identity names the
// credential that passed, for logging and replay attribution.
type Verified struct {
	Raw      RawRequest
	Identity string
}

func (Verified) PortType() PortType { return PortVerified }

// Envelope is the publishable phase: a verified request plus its parsed body
// and resolved variables. Upstream is populated by an http_request node and
// consumed by a respond node, which is how synchronous proxying works.
type Envelope struct {
	V        Verified
	Body     any
	Vars     map[string]string
	Upstream *UpstreamResponse
}

func (Envelope) PortType() PortType { return PortEnvelope }

// UpstreamResponse is an upstream HTTP reply captured by an http_request node.
type UpstreamResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// Terminal is a leaf result. Response is nil unless a respond node ran, in
// which case the executor writes it; otherwise the caller gets the default
// 202 plus the Deliveries summary, matching V1.
//
// Terminal is ALWAYS populated by Run, including when Run returns an error.
// V1 returns the already-succeeded and still-unattempted destination lists
// even on a 503 (server.go:322) so replay has a complete picture, and the
// caller needs Deliveries to decide whether to compensate (see
// NeedsCompensation).
type Terminal struct {
	Response   *Response
	Deliveries []Delivery
	// Compensations lists nodes that reserved something and ran
	// successfully, in reverse execution order. Run does not invoke them:
	// the decision depends on the delivery outcome, not just on whether Run
	// errored. See NeedsCompensation and RunCompensations.
	Compensations []Compensation
}

func (Terminal) PortType() PortType { return PortTerminal }

// Response is an explicit reply produced by a respond node.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Delivery records one destination outcome. NodeID and Target are used as
// metric labels, so Target must be the CONFIGURED template (for example
// "{route}-applicationServiceQueue"), never the rendered value -- see
// scalability rule R3. The rendered value belongs in logs and span attributes.
type Delivery struct {
	NodeID string
	Target string
	Err    error
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/graph/ -v`
Expected: PASS — all tests from Tasks 1 and 2.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/graph/payload.go internal/graph/payload_test.go
git commit -m "feat(graph): request-phase payload types"
```

---

## Task 3: Node interface and registry

**Files:**
- Create: `internal/graph/registry.go`
- Test: `internal/graph/registry_test.go`

**Interfaces:**
- Consumes: `PortType` (Task 1), `Payload` (Task 2), `NodeConfig` (defined here, reused in
  Task 4).
- Produces:
  - `type Node interface { Run(ctx context.Context, in Payload) (Payload, error) }`
  - `type NodeSpec struct { Type string; In, Out PortType; Detached bool; New func(NodeConfig) (Node, error) }`
  - `type Registry struct{...}` with `NewRegistry() *Registry`,
    `(*Registry) Register(NodeSpec) error`, `(*Registry) Lookup(string) (NodeSpec, bool)`.
  - `type NodeConfig struct { ID, Type string; Options yaml.Node }`

`Detached` marks destination nodes, which the executor runs on a context detached from the
inbound request (I10). It lives on the spec rather than being inferred from
`Out == PortTerminal`, because `respond` is also Terminal and must **not** be detached — it
writes to the caller's connection.

- [ ] **Step 1: Write the failing test**

```go
package graph

import (
	"errors"
	"testing"
)

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	spec := NodeSpec{
		Type: "read_body",
		In:   PortRequestHead,
		Out:  PortRawRequest,
		New:  func(NodeConfig) (Node, error) { return nil, nil },
	}
	if err := r.Register(spec); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	got, ok := r.Lookup("read_body")
	if !ok {
		t.Fatal("Lookup(read_body) not found after Register")
	}
	if got.In != PortRequestHead || got.Out != PortRawRequest {
		t.Errorf("spec ports = %s->%s, want RequestHead->RawRequest", got.In, got.Out)
	}
}

func TestRegistryRejectsDuplicateType(t *testing.T) {
	r := NewRegistry()
	spec := NodeSpec{Type: "auth", In: PortRawRequest, Out: PortVerified,
		New: func(NodeConfig) (Node, error) { return nil, nil }}
	if err := r.Register(spec); err != nil {
		t.Fatalf("first Register() = %v, want nil", err)
	}
	err := r.Register(spec)
	if !errors.Is(err, ErrDuplicateNodeType) {
		t.Errorf("second Register() = %v, want ErrDuplicateNodeType", err)
	}
}

func TestRegistryRejectsIncompleteSpec(t *testing.T) {
	cases := []struct {
		name string
		spec NodeSpec
	}{
		{"no type", NodeSpec{In: PortEnvelope, Out: PortTerminal,
			New: func(NodeConfig) (Node, error) { return nil, nil }}},
		{"no constructor", NodeSpec{Type: "sns", In: PortEnvelope, Out: PortTerminal}},
		{"none as output", NodeSpec{Type: "bad", In: PortEnvelope, Out: PortNone,
			New: func(NodeConfig) (Node, error) { return nil, nil }}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := NewRegistry().Register(tc.spec); err == nil {
				t.Error("Register() = nil, want error")
			}
		})
	}
}

func TestRegistryLookupMissing(t *testing.T) {
	if _, ok := NewRegistry().Lookup("nope"); ok {
		t.Error("Lookup(nope) found a spec in an empty registry")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/graph/ -run TestRegistry -v`
Expected: FAIL to build — `undefined: NewRegistry`.

- [ ] **Step 3: Write minimal implementation**

```go
package graph

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ErrDuplicateNodeType is returned when two NodeSpecs claim the same Type.
var ErrDuplicateNodeType = errors.New("graph: duplicate node type")

// Node is one step in a flow. Run transforms the inbound payload; returning an
// error aborts this branch and is reported per the fan-out failure policy.
//
// Implementations must be safe for concurrent use: one Node instance is built
// at config load and shared across every request, and fan-out runs siblings in
// parallel. Keep per-request state in the Payload, never on the Node.
type Node interface {
	Run(ctx context.Context, in Payload) (Payload, error)
}

// NodeConfig is one entry from a flow's `nodes:` list. Options holds the
// type-specific fields, decoded by the NodeSpec's New function.
type NodeConfig struct {
	ID      string
	Type    string
	Options yaml.Node
}

// NodeSpec declares a node type's port signature and how to build one.
type NodeSpec struct {
	// Type is the string used in config, e.g. "read_body".
	Type string
	// In is the payload this node consumes; PortNone marks a trigger.
	In PortType
	// Out is the payload it produces.
	Out PortType
	// Detached marks a destination: the executor runs it on a context
	// detached from the inbound request, bounded by publish_timeout, so a
	// sender disconnect cannot cancel work already committed to delivery
	// (I10).
	//
	// This is NOT inferrable from Out == PortTerminal: `respond` is Terminal
	// too and must stay on the request context, because it writes to the
	// caller's connection.
	Detached bool
	// New builds a node from its config. Returning an error fails the load.
	New func(NodeConfig) (Node, error)
}

// Registry maps node type names to their specs.
type Registry struct {
	specs map[string]NodeSpec
}

func NewRegistry() *Registry {
	return &Registry{specs: map[string]NodeSpec{}}
}

// Register adds a node type. It rejects incomplete specs and duplicates rather
// than overwriting, so a typo in a node type name fails the build instead of
// silently shadowing a real node (fail-closed, matching I11/I12).
func (r *Registry) Register(s NodeSpec) error {
	switch {
	case s.Type == "":
		return errors.New("graph: NodeSpec.Type is empty")
	case s.New == nil:
		return fmt.Errorf("graph: NodeSpec %q has no New function", s.Type)
	case s.Out == PortNone:
		return fmt.Errorf("graph: NodeSpec %q declares Out=None; only In may be None", s.Type)
	}
	if _, exists := r.specs[s.Type]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateNodeType, s.Type)
	}
	r.specs[s.Type] = s
	return nil
}

// Lookup returns the spec for a node type.
func (r *Registry) Lookup(nodeType string) (NodeSpec, bool) {
	s, ok := r.specs[nodeType]
	return s, ok
}

// Types returns every registered type name, for error messages and for the
// config-UI schema endpoint.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.specs))
	for t := range r.specs {
		out = append(out, t)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/graph/ -v`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/graph/registry.go internal/graph/registry_test.go
git commit -m "feat(graph): node interface and type registry"
```

---

## Task 4: Config model and edge parsing

**Files:**
- Create: `internal/graph/model.go`
- Test: `internal/graph/model_test.go`

**Interfaces:**
- Consumes: `NodeConfig` (Task 3).
- Produces: `type FlowConfig struct { Nodes []NodeConfig; Edges []string }`;
  `type Edge struct { From, FromPort, To string }`;
  `func ParseEdge(s string) (Edge, error)`; `func ParseEdges(ss []string) ([]Edge, error)`;
  `func (fc FlowConfig) UnmarshalYAML(*yaml.Node) error` on `NodeConfig`.

Edge syntax is `from>to`, or `from:port>to` for a labelled output port. Labelled ports are
parsed now and unused until `switch` lands in Chunk 4 — parsing them here means the config
format does not change when it arrives.

- [ ] **Step 1: Write the failing test**

```go
package graph

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseEdge(t *testing.T) {
	cases := []struct {
		in      string
		want    Edge
		wantErr bool
	}{
		{in: "in>body", want: Edge{From: "in", To: "body"}},
		{in: " in > body ", want: Edge{From: "in", To: "body"}},
		{in: "route:matched>sns", want: Edge{From: "route", FromPort: "matched", To: "sns"}},
		{in: "route:default>fallback", want: Edge{From: "route", FromPort: "default", To: "fallback"}},
		{in: "", wantErr: true},
		{in: "in", wantErr: true},
		{in: "in>", wantErr: true},
		{in: ">body", wantErr: true},
		{in: "a>b>c", wantErr: true},
		{in: "a:>b", wantErr: true},
		{in: "a:x:y>b", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseEdge(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseEdge(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEdge(%q) = %v, want nil", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseEdge(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseEdgesReportsIndexOnFailure(t *testing.T) {
	_, err := ParseEdges([]string{"a>b", "broken"})
	if err == nil {
		t.Fatal("ParseEdges() = nil, want error")
	}
	// The index must appear so an operator can find the offending entry in a
	// long edges list.
	if !contains(err.Error(), "edges[1]") {
		t.Errorf("error %q does not mention edges[1]", err.Error())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestNodeConfigUnmarshalKeepsOptions(t *testing.T) {
	const src = `
id: rl
type: rate_limit
rpm: 600
burst: 60
`
	var nc NodeConfig
	if err := yaml.Unmarshal([]byte(src), &nc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if nc.ID != "rl" || nc.Type != "rate_limit" {
		t.Fatalf("got id=%q type=%q, want rl/rate_limit", nc.ID, nc.Type)
	}
	var opts struct {
		RPM   int `yaml:"rpm"`
		Burst int `yaml:"burst"`
	}
	if err := nc.Options.Decode(&opts); err != nil {
		t.Fatalf("Options.Decode() = %v", err)
	}
	if opts.RPM != 600 || opts.Burst != 60 {
		t.Errorf("opts = %+v, want rpm=600 burst=60", opts)
	}
}

func TestFlowConfigUnmarshal(t *testing.T) {
	const src = `
nodes:
  - {id: in, type: http_trigger, path: /webhooks/github}
  - {id: body, type: read_body, max_bytes: 1048576}
edges: [in>body]
`
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if len(fc.Nodes) != 2 || len(fc.Edges) != 1 {
		t.Fatalf("got %d nodes / %d edges, want 2/1", len(fc.Nodes), len(fc.Edges))
	}
	if fc.Nodes[1].ID != "body" {
		t.Errorf("Nodes[1].ID = %q, want body", fc.Nodes[1].ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/graph/ -run 'TestParseEdge|TestNodeConfig|TestFlowConfig' -v`
Expected: FAIL to build — `undefined: ParseEdge`.

- [ ] **Step 3: Write minimal implementation**

```go
package graph

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FlowConfig is one entry under the top-level `flows:` map.
type FlowConfig struct {
	Nodes []NodeConfig `yaml:"nodes"`
	Edges []string     `yaml:"edges"`
}

// Edge is a parsed connection. FromPort is empty for a node's default output
// port and set for a labelled port, as a switch node uses.
type Edge struct {
	From     string
	FromPort string
	To       string
}

func (e Edge) String() string {
	if e.FromPort == "" {
		return e.From + ">" + e.To
	}
	return e.From + ":" + e.FromPort + ">" + e.To
}

// ParseEdge parses "from>to" or "from:port>to". Whitespace around any
// component is ignored so that operators can align long edge lists.
func ParseEdge(s string) (Edge, error) {
	from, to, found := strings.Cut(strings.TrimSpace(s), ">")
	if !found {
		return Edge{}, fmt.Errorf("graph: edge %q is missing %q", s, ">")
	}
	if strings.Contains(to, ">") {
		return Edge{}, fmt.Errorf("graph: edge %q has more than one %q", s, ">")
	}
	e := Edge{To: strings.TrimSpace(to)}
	src := strings.TrimSpace(from)
	if name, port, hasPort := strings.Cut(src, ":"); hasPort {
		if strings.Contains(port, ":") {
			return Edge{}, fmt.Errorf("graph: edge %q has more than one %q", s, ":")
		}
		e.From, e.FromPort = strings.TrimSpace(name), strings.TrimSpace(port)
		if e.FromPort == "" {
			return Edge{}, fmt.Errorf("graph: edge %q has an empty output port", s)
		}
	} else {
		e.From = src
	}
	if e.From == "" || e.To == "" {
		return Edge{}, fmt.Errorf("graph: edge %q has an empty endpoint", s)
	}
	return e, nil
}

// ParseEdges parses every edge, reporting the list index of the first failure
// so an operator can locate it in a long list.
func ParseEdges(ss []string) ([]Edge, error) {
	out := make([]Edge, 0, len(ss))
	for i, s := range ss {
		e, err := ParseEdge(s)
		if err != nil {
			return nil, fmt.Errorf("edges[%d]: %w", i, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// UnmarshalYAML pulls id and type out of a node entry and retains the whole
// mapping in Options, so each NodeSpec's New function decodes only the fields
// it understands. Keeping the raw node (rather than a map[string]any) means
// type-specific decoding gets yaml.v3's normal strictness and error positions.
func (nc *NodeConfig) UnmarshalYAML(value *yaml.Node) error {
	var head struct {
		ID   string `yaml:"id"`
		Type string `yaml:"type"`
	}
	if err := value.Decode(&head); err != nil {
		return err
	}
	if head.ID == "" {
		return fmt.Errorf("graph: node at line %d has no id", value.Line)
	}
	if head.Type == "" {
		return fmt.Errorf("graph: node %q has no type", head.ID)
	}
	nc.ID, nc.Type, nc.Options = head.ID, head.Type, *value
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/graph/ -v`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/graph/model.go internal/graph/model_test.go
git commit -m "feat(graph): flow config model and edge parsing"
```

---

## Task 5: Shared test nodes

**Files:**
- Create: `internal/graph/testnodes_test.go`

**Interfaces:**
- Consumes: `Node`, `NodeSpec`, `Registry` (Task 3), payloads (Task 2).
- Produces, for use by Tasks 6 and 7 only:
  - `func testRegistry(t *testing.T) *Registry` — a registry with one spec per port
    transition in the lattice.
  - `type recordingNode struct { id string; log *[]string; err error }` implementing `Node`.
  - `func passthroughSpec(name string, in, out PortType) NodeSpec`

This is a `_test.go` file, so none of it ships in the binary. It exists because Tasks 6
and 7 both need the same fixtures, and duplicating them would let the two drift.

- [ ] **Step 1: Write the file**

```go
package graph

import (
	"context"
	"sync"
	"testing"
)

// recordingNode appends its id to a shared log when it runs, so tests can
// assert execution order and concurrency. The mutex matters: fan-out runs
// siblings in parallel and the race detector is on in CI (make test-race).
type recordingNode struct {
	id  string
	mu  *sync.Mutex
	log *[]string
	err error
	out Payload
}

func (n *recordingNode) Run(_ context.Context, in Payload) (Payload, error) {
	n.mu.Lock()
	*n.log = append(*n.log, n.id)
	n.mu.Unlock()
	if n.err != nil {
		return nil, n.err
	}
	if n.out != nil {
		return n.out, nil
	}
	return in, nil
}

// recorder builds recordingNodes that share one log.
type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) node(id string) *recordingNode {
	return &recordingNode{id: id, mu: &r.mu, log: &r.log}
}

func (r *recorder) entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

// passthroughSpec builds a NodeSpec whose node returns the payload the port
// signature promises, so compile-time and run-time types agree.
func passthroughSpec(name string, in, out PortType) NodeSpec {
	return NodeSpec{
		Type: name, In: in, Out: out,
		New: func(nc NodeConfig) (Node, error) {
			return &staticNode{id: nc.ID, out: zeroPayload(out)}, nil
		},
	}
}

type staticNode struct {
	id  string
	out Payload
}

func (n *staticNode) Run(_ context.Context, _ Payload) (Payload, error) { return n.out, nil }

// zeroPayload returns an empty payload of the given port type.
func zeroPayload(p PortType) Payload {
	switch p {
	case PortRequestHead:
		return RequestHead{}
	case PortRawRequest:
		return RawRequest{}
	case PortVerified:
		return Verified{}
	case PortEnvelope:
		return Envelope{}
	default:
		return Terminal{}
	}
}

// testRegistry provides one node type per transition in the real lattice, so
// compile tests exercise the same shapes production configs will.
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	specs := []NodeSpec{
		passthroughSpec("trigger", PortNone, PortRequestHead),
		passthroughSpec("gate", PortRequestHead, PortRequestHead),
		passthroughSpec("read_body", PortRequestHead, PortRawRequest),
		passthroughSpec("auth", PortRawRequest, PortVerified),
		passthroughSpec("envelope", PortVerified, PortEnvelope),
		passthroughSpec("process", PortEnvelope, PortEnvelope),
		passthroughSpec("respond", PortEnvelope, PortTerminal),
	}
	dest := passthroughSpec("dest", PortEnvelope, PortTerminal)
	dest.Detached = true
	specs = append(specs, dest)

	for _, s := range specs {
		if err := r.Register(s); err != nil {
			t.Fatalf("Register(%s) = %v", s.Type, err)
		}
	}
	return r
}
```

- [ ] **Step 2: Verify it compiles and the existing suite still passes**

Run: `go test ./internal/graph/ -v`
Expected: PASS — no new tests, but the file must compile and `go vet` must be clean.

- [ ] **Step 3: Lint and commit**

```bash
make lint
git add internal/graph/testnodes_test.go
git commit -m "test(graph): shared test nodes and lattice-shaped registry"
```

---

## Task 6: The compiler

**Files:**
- Create: `internal/graph/compile.go`
- Test: `internal/graph/compile_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces:
  - `type Flow struct{...}` with unexported fields and accessors `Name() string`,
    `Root() string`, `NodeIDs() []string`.
  - `func Compile(name string, fc FlowConfig, reg *Registry) (*Flow, error)`
  - `type CompileError struct { Flow string; Problems []string }` with
    `Error() string` and `Unwrap() []error` omitted (problems are strings, aggregated).

`Compile` performs eight checks and **aggregates** every failure rather than returning the
first, so one `hookway validate` run reports all problems in a flow.

- [ ] **Step 1: Write the failing test**

```go
package graph

import (
	"strings"
	"testing"
)

func flowConfig(nodes [][2]string, edges ...string) FlowConfig {
	fc := FlowConfig{Edges: edges}
	for _, n := range nodes {
		fc.Nodes = append(fc.Nodes, NodeConfig{ID: n[0], Type: n[1]})
	}
	return fc
}

func TestCompileAcceptsAValidFlow(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"rl", "gate"}, {"body", "read_body"},
		{"auth", "auth"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>rl", "rl>body", "body>auth", "auth>env", "env>out")

	f, err := Compile("github", fc, testRegistry(t))
	if err != nil {
		t.Fatalf("Compile() = %v, want nil", err)
	}
	if f.Name() != "github" {
		t.Errorf("Name() = %q, want github", f.Name())
	}
	if f.Root() != "in" {
		t.Errorf("Root() = %q, want in", f.Root())
	}
	if len(f.NodeIDs()) != 6 {
		t.Errorf("NodeIDs() has %d entries, want 6", len(f.NodeIDs()))
	}
}

// TestCompileRejectsGateAfterReadBody is the invariant regression test: this
// is the exact wiring that would break I1-I4, and it must not compile.
func TestCompileRejectsGateAfterReadBody(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"rl", "gate"},
		{"auth", "auth"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>rl", "rl>auth", "auth>env", "env>out")

	_, err := Compile("bad", fc, testRegistry(t))
	if err == nil {
		t.Fatal("Compile() = nil; a gate wired after read_body must not compile (I1-I4)")
	}
	if !strings.Contains(err.Error(), "RequestHead") || !strings.Contains(err.Error(), "RawRequest") {
		t.Errorf("error %q should name both port types so the operator sees the mismatch", err)
	}
}

// TestCompileRejectsAuthBypass covers the property the whole design rests on:
// there is no path to Envelope that skips auth.
func TestCompileRejectsAuthBypass(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>env", "env>out")

	if _, err := Compile("bypass", fc, testRegistry(t)); err == nil {
		t.Fatal("Compile() = nil; envelope wired straight to read_body bypasses auth")
	}
}

func TestCompileStructuralChecks(t *testing.T) {
	cases := []struct {
		name    string
		fc      FlowConfig
		wantMsg string
	}{
		{
			name: "unknown node type",
			fc: flowConfig([][2]string{{"in", "trigger"}, {"x", "nonexistent"}},
				"in>x"),
			wantMsg: "unknown node type",
		},
		{
			name: "duplicate node id",
			fc: flowConfig([][2]string{{"in", "trigger"}, {"in", "gate"}},
				"in>in"),
			wantMsg: "duplicate node id",
		},
		{
			name: "no root",
			fc: flowConfig([][2]string{{"rl", "gate"}, {"body", "read_body"}},
				"rl>body"),
			wantMsg: "no trigger",
		},
		{
			name: "two roots",
			fc: flowConfig([][2]string{{"a", "trigger"}, {"b", "trigger"}, {"body", "read_body"}},
				"a>body", "b>body"),
			wantMsg: "more than one trigger",
		},
		{
			name: "edge to missing node",
			fc: flowConfig([][2]string{{"in", "trigger"}},
				"in>ghost"),
			wantMsg: "unknown node",
		},
		{
			name: "unreachable node",
			fc: flowConfig([][2]string{
				{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
				{"env", "envelope"}, {"out", "dest"}, {"orphan", "process"},
			}, "in>body", "body>auth", "auth>env", "env>out"),
			wantMsg: "unreachable",
		},
		{
			name: "non-terminal leaf",
			fc: flowConfig([][2]string{
				{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
			}, "in>body", "body>auth", "auth>env"),
			wantMsg: "not Terminal",
		},
		{
			name: "cycle",
			fc: flowConfig([][2]string{
				{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
				{"env", "envelope"}, {"p1", "process"}, {"p2", "process"}, {"out", "dest"},
			}, "in>body", "body>auth", "auth>env", "env>p1", "p1>p2", "p2>p1", "p1>out"),
			wantMsg: "cycle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile("f", tc.fc, testRegistry(t))
			if err == nil {
				t.Fatalf("Compile() = nil, want error containing %q", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("Compile() error = %q, want it to contain %q", err, tc.wantMsg)
			}
		})
	}
}

// TestCompileAggregatesProblems keeps `hookway validate` useful: one run
// should report every problem in a flow, not just the first.
func TestCompileAggregatesProblems(t *testing.T) {
	fc := flowConfig([][2]string{{"in", "trigger"}, {"x", "nonexistent"}, {"y", "alsomissing"}},
		"in>x", "x>y")
	_, err := Compile("f", fc, testRegistry(t))
	if err == nil {
		t.Fatal("Compile() = nil, want error")
	}
	var ce *CompileError
	if !asCompileError(err, &ce) {
		t.Fatalf("error is %T, want *CompileError", err)
	}
	if len(ce.Problems) < 2 {
		t.Errorf("Problems has %d entries, want >= 2: %v", len(ce.Problems), ce.Problems)
	}
}

func asCompileError(err error, target **CompileError) bool {
	ce, ok := err.(*CompileError)
	if ok {
		*target = ce
	}
	return ok
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/graph/ -run TestCompile -v`
Expected: FAIL to build — `undefined: Compile`.

- [ ] **Step 3: Write minimal implementation**

```go
package graph

import (
	"fmt"
	"sort"
	"strings"
)

// CompileError aggregates every problem found in one flow, so a single
// `hookway validate` run reports all of them rather than the first.
type CompileError struct {
	Flow     string
	Problems []string
}

func (e *CompileError) Error() string {
	return fmt.Sprintf("flow %q: %d problem(s):\n  - %s",
		e.Flow, len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// bound is a node config paired with its spec and constructed instance.
type bound struct {
	id   string
	spec NodeSpec
	node Node
}

// Flow is a compiled, executable graph. It is immutable after Compile and safe
// for concurrent use across requests.
type Flow struct {
	name  string
	root  string
	nodes map[string]*bound
	out   map[string][]Edge
	order []string // deterministic node order, for stable error and metric output
}

func (f *Flow) Name() string { return f.name }
func (f *Flow) Root() string { return f.root }

func (f *Flow) NodeIDs() []string { return append([]string(nil), f.order...) }

// Compile turns a flow config into an executable graph, or reports every
// structural problem it finds. Checks, in order:
//
//  1. every node type is registered
//  2. node ids are unique
//  3. exactly one trigger (In == PortNone) exists
//  4. every edge endpoint names a real node
//  5. every edge is type-compatible: to.In.Accepts(from.Out)
//  6. every non-root node is reachable from the root
//  7. every leaf produces PortTerminal
//  8. the graph is acyclic
//
// Checks 5 and 7 are what carry the invariants: 5 enforces phase ordering
// (I1-I9) and the no-auth-bypass property, 7 stops a dangling node from
// silently dropping events.
func Compile(name string, fc FlowConfig, reg *Registry) (*Flow, error) {
	ce := &CompileError{Flow: name}
	f := &Flow{name: name, nodes: map[string]*bound{}, out: map[string][]Edge{}}

	// (1) and (2): resolve types and reject duplicate ids.
	for _, nc := range fc.Nodes {
		if _, dup := f.nodes[nc.ID]; dup {
			ce.Problems = append(ce.Problems, fmt.Sprintf("duplicate node id %q", nc.ID))
			continue
		}
		spec, ok := reg.Lookup(nc.Type)
		if !ok {
			known := reg.Types()
			sort.Strings(known)
			ce.Problems = append(ce.Problems,
				fmt.Sprintf("node %q: unknown node type %q (known types: %s)",
					nc.ID, nc.Type, strings.Join(known, ", ")))
			continue
		}
		node, err := spec.New(nc)
		if err != nil {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node %q (%s): %v", nc.ID, nc.Type, err))
			continue
		}
		f.nodes[nc.ID] = &bound{id: nc.ID, spec: spec, node: node}
		f.order = append(f.order, nc.ID)
	}

	// (3): exactly one trigger.
	var roots []string
	for _, id := range f.order {
		if f.nodes[id].spec.In == PortNone {
			roots = append(roots, id)
		}
	}
	switch {
	case len(roots) == 0:
		ce.Problems = append(ce.Problems, "no trigger node (none has an input port of None)")
	case len(roots) > 1:
		sort.Strings(roots)
		ce.Problems = append(ce.Problems,
			fmt.Sprintf("more than one trigger node: %s", strings.Join(roots, ", ")))
	default:
		f.root = roots[0]
	}

	// (4) and (5): endpoints exist and are type-compatible.
	edges, err := ParseEdges(fc.Edges)
	if err != nil {
		ce.Problems = append(ce.Problems, err.Error())
		return nil, ce
	}
	inbound := map[string]int{}
	for _, e := range edges {
		from, fromOK := f.nodes[e.From]
		to, toOK := f.nodes[e.To]
		if !fromOK {
			ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: unknown node %q", e, e.From))
			continue
		}
		if !toOK {
			ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: unknown node %q", e, e.To))
			continue
		}
		if !to.spec.In.Accepts(from.spec.Out) {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"edge %s: type mismatch -- %q (%s) produces %s but %q (%s) consumes %s",
				e, e.From, from.spec.Type, from.spec.Out, e.To, to.spec.Type, to.spec.In))
			continue
		}
		f.out[e.From] = append(f.out[e.From], e)
		inbound[e.To]++
	}

	// (6): reachability from the root.
	if f.root != "" {
		seen := map[string]bool{}
		f.walk(f.root, seen)
		for _, id := range f.order {
			if id != f.root && !seen[id] {
				ce.Problems = append(ce.Problems,
					fmt.Sprintf("node %q is unreachable from trigger %q", id, f.root))
			}
		}
	}

	// (7): leaves must be Terminal.
	for _, id := range f.order {
		if len(f.out[id]) == 0 && f.nodes[id].spec.Out != PortTerminal {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s) has no outgoing edge but produces %s, not Terminal: "+
					"events reaching it would be silently dropped",
				id, f.nodes[id].spec.Type, f.nodes[id].spec.Out))
		}
	}

	// (8): acyclicity.
	if cycle := f.findCycle(); cycle != nil {
		ce.Problems = append(ce.Problems, fmt.Sprintf("cycle: %s", strings.Join(cycle, " -> ")))
	}

	if len(ce.Problems) > 0 {
		return nil, ce
	}
	return f, nil
}

func (f *Flow) walk(id string, seen map[string]bool) {
	for _, e := range f.out[id] {
		if seen[e.To] {
			continue
		}
		seen[e.To] = true
		f.walk(e.To, seen)
	}
}

// findCycle returns one cycle as a node path, or nil. Iterative-free DFS with
// three colours: unvisited, on the current stack, done.
func (f *Flow) findCycle() []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := map[string]int{}
	var stack []string
	var cycle []string

	var visit func(string) bool
	visit = func(id string) bool {
		colour[id] = grey
		stack = append(stack, id)
		for _, e := range f.out[id] {
			switch colour[e.To] {
			case grey:
				for i, s := range stack {
					if s == e.To {
						cycle = append(append([]string(nil), stack[i:]...), e.To)
						return true
					}
				}
				cycle = []string{e.To, e.To}
				return true
			case white:
				if visit(e.To) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		colour[id] = black
		return false
	}

	for _, id := range f.order {
		if colour[id] == white && visit(id) {
			return cycle
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/graph/ -v`
Expected: PASS — including `TestCompileRejectsGateAfterReadBody` and
`TestCompileRejectsAuthBypass`.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/graph/compile.go internal/graph/compile_test.go
git commit -m "feat(graph): flow compiler with type-checked edges"
```

---

## Task 7: The executor

**Files:**
- Create: `internal/graph/executor.go`
- Test: `internal/graph/executor_test.go`

**Interfaces:**
- Consumes: `Flow` (Task 6), payloads (Task 2), `NodeSpec.Detached` (Task 3).
- Produces:
  - `type RunOptions struct { PublishTimeout time.Duration; FanoutConcurrency int; OnNode func(NodeEvent) }`
  - `type NodeEvent struct { NodeID, NodeType string; Duration time.Duration; Err error }`
  - `func (f *Flow) Run(ctx context.Context, in Payload, opts RunOptions) (Terminal, error)`
    — **`Terminal` is always populated, even when the error is non-nil.**
  - `type Compensator interface { Compensate(ctx context.Context, in Payload, reason error) error }`
  - `type Compensation struct { NodeID string; Node Compensator; Input Payload }`
  - `func NeedsCompensation(term Terminal, err error) bool`
  - `func RunCompensations(ctx context.Context, term Terminal, reason error, timeout time.Duration) error`

`OnNode` is the per-node observability hook required by scalability rule R2: metrics, logs,
and spans attach here. It is a callback rather than a metrics dependency so `internal/graph`
stays free of a `internal/metrics` import, and so tests can assert per-node instrumentation
without a registry.

`Detached` nodes run on `context.WithoutCancel(ctx)` bounded by `PublishTimeout`,
preserving I10.

### Compensation — how invariant I17 is preserved

Some nodes reserve something that must be **released** if the flow later fails: `replay_guard`
reserves an idempotency key, and I17 requires releasing it whenever a sender retry could
still make progress. The node cannot decide this itself — the outcome is downstream of it.

A node opts in by implementing `Compensator`. The executor records every `Compensator` that
ran **successfully**, paired with the payload it consumed, and returns them on
`Terminal.Compensations` in reverse execution order. It does **not** invoke them.

The node recomputes whatever it needs from `in` rather than storing per-request state: one
`Node` instance is shared across every request and fan-out runs siblings in parallel, so a
reservation field on the node would be a data race. `replayReservationKey` is a pure
function of the headers and raw body, both of which `Verified` carries, so recomputation is
exact.

The caller decides whether to compensate, because the condition is **not** simply "Run
errored". From V1:

| Outcome | Reservation | Why |
|---|---|---|
| Full success | keep | delivered |
| Filter silent drop | keep | the delivery was consumed (`server.go:181`) |
| Filter configured rejection | release | a retry should be filtered again, not 409'd (`server.go:189`) |
| Full publish failure | release | the retry is the recovery path |
| **Partial failure under `skip_failed`** | release | **returns 202, yet the failed destinations still need the retry** (`server.go:344`) |

That last row is why `err != nil` is insufficient. `NeedsCompensation` encodes the real
rule: **the flow errored, or any delivery failed.**

- [ ] **Step 1: Write the failing test**

```go
package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func mustCompile(t *testing.T, fc FlowConfig, reg *Registry) *Flow {
	t.Helper()
	f, err := Compile("test", fc, reg)
	if err != nil {
		t.Fatalf("Compile() = %v", err)
	}
	return f
}

// registryWithRecorder replaces the passthrough constructors with recording
// nodes so tests can assert execution order.
func registryWithRecorder(t *testing.T, rec *recorder) *Registry {
	t.Helper()
	r := NewRegistry()
	add := func(name string, in, out PortType, detached bool) {
		spec := NodeSpec{
			Type: name, In: in, Out: out, Detached: detached,
			New: func(nc NodeConfig) (Node, error) {
				n := rec.node(nc.ID)
				n.out = zeroPayload(out)
				return n, nil
			},
		}
		if err := r.Register(spec); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false)
	add("gate", PortRequestHead, PortRequestHead, false)
	add("read_body", PortRequestHead, PortRawRequest, false)
	add("auth", PortRawRequest, PortVerified, false)
	add("envelope", PortVerified, PortEnvelope, false)
	add("process", PortEnvelope, PortEnvelope, false)
	add("dest", PortEnvelope, PortTerminal, true)
	return r
}

func linearFlow() FlowConfig {
	return flowConfig([][2]string{
		{"in", "trigger"}, {"rl", "gate"}, {"body", "read_body"},
		{"auth", "auth"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>rl", "rl>body", "body>auth", "auth>env", "env>out")
}

func TestRunExecutesInTopologicalOrder(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, linearFlow(), registryWithRecorder(t, rec))

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	got := strings.Join(rec.entries(), ",")
	if want := "in,rl,body,auth,env,out"; got != want {
		t.Errorf("execution order = %q, want %q", got, want)
	}
	if len(term.Deliveries) != 1 || term.Deliveries[0].NodeID != "out" {
		t.Errorf("Deliveries = %+v, want one entry for node out", term.Deliveries)
	}
}

func TestRunFansOutToEveryDestination(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	f := mustCompile(t, fc, registryWithRecorder(t, rec))

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if len(term.Deliveries) != 2 {
		t.Errorf("Deliveries has %d entries, want 2: %+v", len(term.Deliveries), term.Deliveries)
	}
}

func TestRunReportsNodeErrors(t *testing.T) {
	rec := &recorder{}
	sentinel := errors.New("auth failed")
	reg := NewRegistry()
	specs := []struct {
		name     string
		in, out  PortType
		detached bool
		err      error
	}{
		{"trigger", PortNone, PortRequestHead, false, nil},
		{"read_body", PortRequestHead, PortRawRequest, false, nil},
		{"auth", PortRawRequest, PortVerified, false, sentinel},
		{"envelope", PortVerified, PortEnvelope, false, nil},
		{"dest", PortEnvelope, PortTerminal, true, nil},
	}
	for _, s := range specs {
		s := s
		if err := reg.Register(NodeSpec{
			Type: s.name, In: s.in, Out: s.out, Detached: s.detached,
			New: func(nc NodeConfig) (Node, error) {
				n := rec.node(nc.ID)
				n.err, n.out = s.err, zeroPayload(s.out)
				return n, nil
			},
		}); err != nil {
			t.Fatalf("Register(%s) = %v", s.name, err)
		}
	}
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
		{"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>out")
	f := mustCompile(t, fc, reg)

	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() = %v, want it to wrap %v", err, sentinel)
	}
	// Nothing downstream of the failure may run.
	for _, id := range rec.entries() {
		if id == "env" || id == "out" {
			t.Errorf("node %q ran after auth failed", id)
		}
	}
}

// TestRunDetachesDestinationContext pins I10: a cancelled inbound request must
// not cancel a destination node, because that work is already committed.
func TestRunDetachesDestinationContext(t *testing.T) {
	var destSawCancelled bool
	reg := NewRegistry()
	add := func(name string, in, out PortType, detached bool, run func(context.Context) ) {
		if err := reg.Register(NodeSpec{
			Type: name, In: in, Out: out, Detached: detached,
			New: func(NodeConfig) (Node, error) {
				return &fnNode{out: zeroPayload(out), fn: run}, nil
			},
		}); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false, nil)
	add("read_body", PortRequestHead, PortRawRequest, false, nil)
	add("auth", PortRawRequest, PortVerified, false, nil)
	add("envelope", PortVerified, PortEnvelope, false, nil)
	add("dest", PortEnvelope, PortTerminal, true, func(ctx context.Context) {
		destSawCancelled = ctx.Err() != nil
	})

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
		{"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>out")
	f := mustCompile(t, fc, reg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller has already disconnected

	if _, err := f.Run(ctx, RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
		t.Fatalf("Run() = %v, want nil: a detached destination must still run", err)
	}
	if destSawCancelled {
		t.Error("destination node saw a cancelled context; I10 requires it be detached")
	}
}

// --- compensation (invariant I17) ---

// recordingCompensator reserves nothing real; it records that Compensate was
// called and with which reason, so tests can assert the executor collected it
// and the caller invoked it.
type recordingCompensator struct {
	out         Payload
	fail        bool
	mu          sync.Mutex
	compensated int
	lastInput   Payload
}

func (n *recordingCompensator) Run(_ context.Context, in Payload) (Payload, error) {
	if n.fail {
		return nil, errors.New("node failed")
	}
	return n.out, nil
}

func (n *recordingCompensator) Compensate(_ context.Context, in Payload, _ error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.compensated++
	n.lastInput = in
	return nil
}

func TestRunCollectsCompensationsForNodesThatRan(t *testing.T) {
	comp := &recordingCompensator{out: Verified{}}
	f := compileWithCompensator(t, comp, false)

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(term.Compensations) != 1 {
		t.Fatalf("Compensations has %d entries, want 1", len(term.Compensations))
	}
	if term.Compensations[0].NodeID != "guard" {
		t.Errorf("NodeID = %q, want guard", term.Compensations[0].NodeID)
	}
	// Run must NOT have invoked it: a successful flow keeps the reservation.
	if comp.compensated != 0 {
		t.Errorf("Compensate called %d times during Run; Run must only collect, never invoke", comp.compensated)
	}
}

// TestRunPopulatesTerminalOnError pins the contract the caller depends on:
// V1 returns the succeeded and unattempted destination lists even on a 503
// (server.go:322), and compensation needs Deliveries to make its decision.
func TestRunPopulatesTerminalOnError(t *testing.T) {
	rec := &recorder{}
	sentinel := errors.New("publish failed")
	f := compileWithFailingDestination(t, rec, sentinel)

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() err = %v, want it to wrap %v", err, sentinel)
	}
	if len(term.Deliveries) == 0 {
		t.Fatal("Terminal.Deliveries is empty on the error path; the caller needs it for the " +
			"503 response body and for replay capture")
	}
}

// TestRunDoesNotCollectCompensationForNodesThatFailed: a node that errored
// reserved nothing, so compensating it would release a key it never took.
func TestRunDoesNotCollectCompensationForNodesThatFailed(t *testing.T) {
	comp := &recordingCompensator{out: Verified{}, fail: true}
	f := compileWithCompensator(t, comp, false)

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("Run() = nil, want the node error")
	}
	if len(term.Compensations) != 0 {
		t.Errorf("Compensations has %d entries for a node that failed, want 0", len(term.Compensations))
	}
}

func TestNeedsCompensation(t *testing.T) {
	failed := Terminal{Deliveries: []Delivery{{NodeID: "a"}, {NodeID: "b", Err: errors.New("x")}}}
	allOK := Terminal{Deliveries: []Delivery{{NodeID: "a"}, {NodeID: "b"}}}
	none := Terminal{}

	cases := []struct {
		name string
		term Terminal
		err  error
		want bool
	}{
		{"flow errored", none, errors.New("boom"), true},
		{"full success", allOK, nil, false},
		// The skip_failed partial-failure case: 202 to the sender, but the
		// failed destinations still need the retry (server.go:344).
		{"partial delivery failure, no error", failed, nil, true},
		// A filter silent drop: no error, no deliveries attempted. The
		// delivery was consumed, so the reservation is kept (server.go:181).
		{"filtered, nothing attempted", none, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsCompensation(tc.term, tc.err); got != tc.want {
				t.Errorf("NeedsCompensation() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRunCompensationsUsesDetachedContext is the subtle one. A client
// disconnect is exactly when a publish fails, and the reservation MUST still
// be released -- otherwise it leaks until TTL and the sender's retry gets a
// 409 instead of being redelivered.
func TestRunCompensationsUsesDetachedContext(t *testing.T) {
	comp := &recordingCompensator{out: Verified{}}
	term := Terminal{Compensations: []Compensation{{NodeID: "guard", Node: comp, Input: RawRequest{}}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller already gone

	if err := RunCompensations(ctx, term, errors.New("publish failed"), time.Second); err != nil {
		t.Fatalf("RunCompensations() = %v, want nil", err)
	}
	if comp.compensated != 1 {
		t.Fatal("compensation was skipped because the caller's context was cancelled; " +
			"it must run on a detached context or the reservation leaks (I17)")
	}
}

func TestRunCompensationsRunsInReverseOrder(t *testing.T) {
	var order []string
	mk := func(id string) Compensator { return &orderCompensator{id: id, log: &order} }
	term := Terminal{Compensations: []Compensation{
		{NodeID: "second", Node: mk("second")},
		{NodeID: "first", Node: mk("first")},
	}}
	if err := RunCompensations(context.Background(), term, nil, time.Second); err != nil {
		t.Fatalf("RunCompensations() = %v", err)
	}
	// Terminal.Compensations is already reverse execution order, so it runs
	// front to back. LIFO, like defer.
	if strings.Join(order, ",") != "second,first" {
		t.Errorf("order = %v, want second,first", order)
	}
}

type orderCompensator struct {
	id  string
	log *[]string
}

func (o *orderCompensator) Run(_ context.Context, in Payload) (Payload, error) { return in, nil }
func (o *orderCompensator) Compensate(_ context.Context, _ Payload, _ error) error {
	*o.log = append(*o.log, o.id)
	return nil
}

// TestRunCompensationsSurvivesPanic: a failing release must be visible but
// must never escalate. The response has already failed; a panic here would
// turn a handled 503 into a crash.
func TestRunCompensationsSurvivesPanic(t *testing.T) {
	term := Terminal{Compensations: []Compensation{
		{NodeID: "boom", Node: panicCompensator{}},
		{NodeID: "ok", Node: &orderCompensator{id: "ok", log: new([]string)}},
	}}
	err := RunCompensations(context.Background(), term, nil, time.Second)
	if err == nil {
		t.Error("RunCompensations() = nil; a panicking compensation must be reported")
	}
	// Reaching here without the process dying is the rest of the assertion.
}

type panicCompensator struct{}

func (panicCompensator) Run(_ context.Context, in Payload) (Payload, error) { return in, nil }
func (panicCompensator) Compensate(_ context.Context, _ Payload, _ error) error {
	panic("release exploded")
}

type fnNode struct {
	out Payload
	fn  func(context.Context)
}

func (n *fnNode) Run(ctx context.Context, _ Payload) (Payload, error) {
	if n.fn != nil {
		n.fn(ctx)
	}
	return n.out, nil
}

// TestRunEmitsOneEventPerNode covers scalability rule R2: per-node
// observability comes from this callback, never from a persisted row.
func TestRunEmitsOneEventPerNode(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, linearFlow(), registryWithRecorder(t, rec))

	var events []NodeEvent
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{
		PublishTimeout: time.Second,
		OnNode:         func(e NodeEvent) { events = append(events, e) },
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(events) != 6 {
		t.Fatalf("got %d node events, want 6: %+v", len(events), events)
	}
	for _, e := range events {
		if e.NodeID == "" || e.NodeType == "" {
			t.Errorf("event %+v is missing NodeID or NodeType (needed as metric labels)", e)
		}
	}
}

func TestRunRejectsWrongRootPayload(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, linearFlow(), registryWithRecorder(t, rec))
	_, err := f.Run(context.Background(), Envelope{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("Run() = nil; an Envelope cannot seed a flow whose trigger emits RequestHead")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/graph/ -run TestRun -v`
Expected: FAIL to build — `undefined: RunOptions`.

Write these two helpers in the test file; both build a linear flow with one extra node:
`compileWithCompensator(t *testing.T, node Node, failDest bool) *Flow` registers `node`
under type `guard` at `Verified -> Verified` with id `guard`, and
`compileWithFailingDestination(t *testing.T, rec *recorder, err error) *Flow` registers a
`dest` spec whose node returns `err`.

- [ ] **Step 3: Write minimal implementation**

```go
package graph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// NodeEvent reports one node's execution. NodeID and NodeType are safe as
// metric labels because both are bounded by config (scalability rule R3);
// never add a rendered target or any data-derived value here.
type NodeEvent struct {
	NodeID   string
	NodeType string
	Duration time.Duration
	Err      error
}

// RunOptions configures one execution.
type RunOptions struct {
	// PublishTimeout bounds detached destination nodes. Required: a zero
	// value would let a stuck destination hold a goroutine forever.
	PublishTimeout time.Duration
	// FanoutConcurrency caps parallel sibling execution; <= 0 means 8,
	// matching V1's ServerConfig.FanoutConcurrencyLimit default.
	FanoutConcurrency int
	// OnNode, when set, is called once per node after it runs. This is the
	// per-node observability seam (R2): metrics, structured logs, and spans
	// attach here rather than being persisted per event.
	OnNode func(NodeEvent)
}

func (o RunOptions) fanout() int {
	if o.FanoutConcurrency <= 0 {
		return 8
	}
	return o.FanoutConcurrency
}

// Run executes the flow from its trigger. Execution state is in-memory for the
// life of the call: there is no checkpointing and no per-node persistence, so
// writes per event stay independent of node count (decision D8).
//
// Node errors abort their branch and fail the run (stop_on_failure, V1's
// default). The skip_failed policy arrives in Chunk 5, switch/on_error in Chunk 4.
func (f *Flow) Run(ctx context.Context, in Payload, opts RunOptions) (Terminal, error) {
	if opts.PublishTimeout <= 0 {
		return Terminal{}, fmt.Errorf("graph: RunOptions.PublishTimeout must be positive")
	}
	root := f.nodes[f.root]
	if want := root.spec.Out; in != nil && in.PortType() != root.spec.In && root.spec.In != PortNone {
		return Terminal{}, fmt.Errorf("graph: trigger %q consumes %s but was seeded with %s",
			f.root, root.spec.In, in.PortType())
	} else if in != nil && root.spec.In == PortNone && in.PortType() != want {
		// A trigger consumes nothing, so the seed payload stands in for its
		// output and must match it.
		return Terminal{}, fmt.Errorf("graph: trigger %q produces %s but was seeded with %s",
			f.root, want, in.PortType())
	}

	run := &execution{flow: f, opts: opts}
	err := run.node(ctx, f.root, in)
	// Terminal is populated on BOTH paths. V1 returns the succeeded and
	// unattempted destination lists even on a 503 (server.go:322) so replay
	// has a complete picture, and NeedsCompensation reads Deliveries to
	// decide whether reservations must be released.
	return Terminal{
		Deliveries:    run.deliveries(),
		Compensations: run.compensations(),
	}, err
}

// execution holds the mutable state of one Run.
type execution struct {
	flow *Flow
	opts RunOptions

	mu    sync.Mutex
	dels  []Delivery
	comps []Compensation
}

// recordCompensation notes a Compensator that ran successfully. Collected in
// execution order; Run reverses the slice before returning it, so the caller
// gets LIFO order.
func (e *execution) recordCompensation(c Compensation) {
	e.mu.Lock()
	e.comps = append(e.comps, c)
	e.mu.Unlock()
}

func (e *execution) compensations() []Compensation {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Compensation, len(e.comps))
	for i, c := range e.comps {
		out[len(e.comps)-1-i] = c
	}
	return out
}

func (e *execution) record(d Delivery) {
	e.mu.Lock()
	e.dels = append(e.dels, d)
	e.mu.Unlock()
}

func (e *execution) deliveries() []Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Delivery(nil), e.dels...)
}

// node runs one node and then its successors. A node with several successors
// fans out in parallel, bounded by RunOptions.FanoutConcurrency.
func (e *execution) node(ctx context.Context, id string, in Payload) error {
	b := e.flow.nodes[id]

	nodeCtx, cancel := e.contextFor(ctx, b)
	start := time.Now()
	out, err := b.node.Run(nodeCtx, in)
	elapsed := time.Since(start)
	cancel()

	if e.opts.OnNode != nil {
		e.opts.OnNode(NodeEvent{NodeID: id, NodeType: b.spec.Type, Duration: elapsed, Err: err})
	}
	if b.spec.Out == PortTerminal {
		e.record(Delivery{NodeID: id, Err: err})
	}
	if err != nil {
		// No compensation is recorded for a node that failed: it reserved
		// nothing, and releasing a key it never took would let a genuine
		// replay through.
		return fmt.Errorf("node %q (%s): %w", id, b.spec.Type, err)
	}
	// Record compensation only after the node succeeded, and pass the input
	// it consumed so it can recompute its reservation without holding
	// per-request state.
	if c, ok := b.node.(Compensator); ok {
		e.recordCompensation(Compensation{NodeID: id, Node: c, Input: in})
	}

	successors := e.flow.out[id]
	switch len(successors) {
	case 0:
		return nil
	case 1:
		return e.node(ctx, successors[0].To, out)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(e.opts.fanout())
	for _, edge := range successors {
		to := edge.To
		g.Go(func() error { return e.node(gctx, to, out) })
	}
	return g.Wait()
}

// contextFor implements invariant I10: a destination node runs on a context
// detached from the inbound request and bounded by PublishTimeout, so a sender
// disconnect cannot cancel work already committed to delivery. Every other
// node stays on the request context -- including respond, which writes to the
// caller's connection and must observe their cancellation.
func (e *execution) contextFor(ctx context.Context, b *bound) (context.Context, context.CancelFunc) {
	if !b.spec.Detached {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), e.opts.PublishTimeout)
}

// Compensator is implemented by a node that reserves something which must be
// released if the flow later fails -- replay_guard and its idempotency key
// being the case invariant I17 is about.
//
// The node MUST recompute whatever it needs from `in` rather than storing
// per-request state: one Node instance is shared across every request and
// fan-out runs siblings in parallel, so a reservation field on the node is a
// data race. replayReservationKey is a pure function of the headers and raw
// body, both of which Verified carries.
type Compensator interface {
	Compensate(ctx context.Context, in Payload, reason error) error
}

// Compensation pairs a Compensator that ran successfully with the payload it
// consumed. Fields are exported so callers and tests can build and inspect
// these without reaching into the package.
type Compensation struct {
	NodeID string
	Node   Compensator
	Input  Payload
}

// NeedsCompensation reports whether a flow's outcome means a sender retry
// could still make progress, and therefore that reservations must be released.
//
// It is deliberately NOT just `err != nil`. Under the skip_failed policy a
// partial failure returns 202 to the sender while some destinations still
// need that retry (V1 releases here, server.go:344). Conversely a filter
// silent drop returns success with zero deliveries attempted and MUST keep
// the reservation, because the delivery was consumed (server.go:181).
func NeedsCompensation(term Terminal, err error) bool {
	if err != nil {
		return true
	}
	for _, d := range term.Deliveries {
		if d.Err != nil {
			return true
		}
	}
	return false
}

// RunCompensations invokes every collected compensation, front to back --
// Terminal.Compensations is already in reverse execution order, so this is
// LIFO like defer.
//
// It runs on a context DETACHED from the caller's. That is the whole point: a
// client disconnect is exactly when a publish fails, and the reservation must
// still be released or it leaks until TTL and the sender's retry gets a 409
// instead of a redelivery (I17).
//
// Errors and panics are collected and returned joined, for logging. They never
// escalate: the response has already failed, and a panic here would turn a
// handled 503 into a crash.
func RunCompensations(ctx context.Context, term Terminal, reason error, timeout time.Duration) error {
	if len(term.Compensations) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	var errs []error
	for _, c := range term.Compensations {
		errs = append(errs, runOneCompensation(cctx, c, reason))
	}
	return errors.Join(errs...)
}

func runOneCompensation(ctx context.Context, c Compensation, reason error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("compensation for node %q panicked: %v", c.NodeID, r)
		}
	}()
	if err := c.Node.Compensate(ctx, c.Input, reason); err != nil {
		return fmt.Errorf("compensation for node %q: %w", c.NodeID, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/graph/ -v && go test -race ./internal/graph/`
Expected: PASS, and clean under the race detector — fan-out runs siblings in parallel and
compensations are collected from those goroutines, so the race detector is load-bearing
here, not optional.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add internal/graph/executor.go internal/graph/executor_test.go
git commit -m "feat(graph): DAG executor with fan-out and detached destinations"
```

---

## Task 8: End-to-end compile-and-run integration test

**Files:**
- Create: `internal/graph/graph_test.go`

**Interfaces:**
- Consumes: everything. Produces nothing — this is the chunk's acceptance test.

- [ ] **Step 1: Write the test**

```go
package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestCompileAndRunFromYAML is the acceptance test for Chunk 1: real YAML in,
// type-checked graph out, executed in order. It uses test nodes because real
// node implementations land in Chunk 3.
func TestCompileAndRunFromYAML(t *testing.T) {
	const src = `
nodes:
  - {id: in,   type: trigger}
  - {id: hops, type: gate}
  - {id: rl,   type: gate}
  - {id: body, type: read_body}
  - {id: auth, type: auth}
  - {id: env,  type: envelope}
  - {id: a,    type: dest}
  - {id: b,    type: dest}
edges: [in>hops, hops>rl, rl>body, body>auth, auth>env, env>a, env>b]
`
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}

	rec := &recorder{}
	f, err := Compile("digifi", fc, registryWithRecorder(t, rec))
	if err != nil {
		t.Fatalf("Compile() = %v", err)
	}

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(term.Deliveries) != 2 {
		t.Errorf("Deliveries has %d entries, want 2", len(term.Deliveries))
	}
	// The gate chain must precede read_body in the actual execution trace,
	// not merely in the config, which is the runtime half of I1-I4.
	order := strings.Join(rec.entries(), ",")
	bodyAt, rlAt := strings.Index(order, "body"), strings.Index(order, "rl")
	if rlAt == -1 || bodyAt == -1 || rlAt > bodyAt {
		t.Errorf("execution order %q: gate must run before read_body", order)
	}
}

// TestInvariantGateBeforeBodyCannotBeConfiguredAway restates the design's
// central claim as an executable assertion: the wiring that would break
// I1-I4 is not merely discouraged, it does not compile.
func TestInvariantGateBeforeBodyCannotBeConfiguredAway(t *testing.T) {
	const src = `
nodes:
  - {id: in,   type: trigger}
  - {id: body, type: read_body}
  - {id: rl,   type: gate}
  - {id: auth, type: auth}
  - {id: env,  type: envelope}
  - {id: out,  type: dest}
edges: [in>body, body>rl, rl>auth, auth>env, env>out]
`
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if _, err := Compile("bad", fc, testRegistry(t)); err == nil {
		t.Fatal("Compile() = nil: a gate after read_body must be a type error (I1-I4)")
	}
}
```

- [ ] **Step 2: Run the full suite**

Run: `go test ./internal/graph/ -v`
Expected: PASS — every test from Tasks 1–8.

- [ ] **Step 3: Confirm coverage and the whole repo still builds**

```bash
go test -cover ./internal/graph/
make test
make lint
```

Expected: `internal/graph` coverage above 85%; `make test` and `make lint` clean. If
coverage is below 85%, the gap is almost certainly an uncovered `CompileError` branch — add
the missing subtest to `TestCompileStructuralChecks` rather than lowering the bar.

- [ ] **Step 4: Commit**

```bash
git add internal/graph/graph_test.go
git commit -m "test(graph): end-to-end compile-and-run acceptance test"
```

---

## Task 9: Record what this chunk settled

**Files:**
- Modify: `v2/README.md` (decision table)
- Modify: `v2/02-design.md` §9 (open design questions)
- Create: `v2/plans/HANDOFF.md`

- [ ] **Step 1: Update the decision table**

Add rows for anything Tasks 0a/0b forced. At minimum, record the S1 outcome (does the
DigiFi scheme need an exception to I6?) and any lattice amendment from S2.

- [ ] **Step 2: Capture handoff notes for Chunk 2**

`v2/plans/HANDOFF.md` records, for whoever implements Chunk 2:
- the exact `Node`, `NodeSpec`, and `RunOptions` signatures Chunk 2's node specs must satisfy
- that `switch`/`on_error` (Chunk 4) and `skip_failed` (Chunk 5) are deliberately absent, and where in
  `executor.go` each hooks in (`execution.node`'s successor switch for `switch`; the
  `err != nil` branch for `on_error`; `errgroup` handling for `skip_failed`)
- which V1 packages each real node wraps: `internal/auth` for `auth`, `internal/webhook`
  for `envelope`, `internal/queue` for destinations, `internal/ratelimit` for `rate_limit`,
  `internal/filter` for `filter`, `internal/lookup` for `lookup`
- that `OnNode` is where `internal/metrics` attaches, keeping `internal/graph` free of a
  metrics dependency

- [ ] **Step 3: Commit**

```bash
git add v2/
git commit -m "docs(v2): record Chunk 1 outcomes and Chunk 2 handoff notes"
```

---

## Self-Review

**Spec coverage.** Against `v2/02-design.md`: §2 port types → Task 1; §2 payloads → Task 2;
§4 node catalog → the registry in Task 3 (implementations are Chunk 3, by design); §5
fan-out → Task 7; §5 context rule → Task 7's `contextFor` and
`TestRunDetachesDestinationContext`; §8 validation layer 2 (edge type check) → Task 6.
Against `v2/03-scalability.md`: D8 no per-node persistence → Task 7's in-memory
`execution`; R2 per-node observability → `OnNode` and `TestRunEmitsOneEventPerNode`; R3
label cardinality → documented on `Delivery` and `NodeEvent`.

**Deliberately out of scope**, each assigned a later chunk: §6 goja (Chunk 7), §7 config
shape beyond one flow (Chunk 2), §8 layers 1 and 3 — per-node JSON schema and policy lint
(Chunk 2), `secret_ref` (Chunk 8), readiness (Chunk 8), UI (Chunk 10), I7–I18 conformance
(Chunk 4).

**Known gap, called out rather than hidden.** Compile check (5) enforces phase *ordering*,
which covers I1–I6 and the auth-bypass property. Invariants I7–I18 are executor and node
behaviour that the type system cannot reach, and Chunk 1 tests only I10. The remaining
eleven need the conformance suite in Chunk 4 — which is precisely the regression net the
clean break (D4) removed, so it should not slip.

**Type consistency.** `Payload.PortType()` is exported and used identically in Tasks 2, 7,
and 8. `NodeConfig` is defined once in Task 3 and consumed in Task 4 (`UnmarshalYAML`) and
Task 6 (`spec.New`). `zeroPayload` and `recorder` are defined once in Task 5 and reused in
Tasks 7 and 8. `flowConfig` is defined in Task 6's test file and reused in Tasks 7 and 8,
which share the package.

**One rough edge to fix during Task 7.** The seed-payload validation in `Run` has an
awkward double condition because a trigger's `In` is `PortNone` while the seed payload must
match its `Out`. Simplify it to a single check against `root.spec.Out` when
`root.spec.In == PortNone`; `TestRunRejectsWrongRootPayload` pins the behaviour either way.
