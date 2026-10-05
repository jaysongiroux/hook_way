package graph

import (
	"net/http"
	"net/url"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
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
	Method      string
	Path        string
	PathVars    map[string]string
	Query       url.Values
	Header      http.Header
	SourceIP    string
	Hops        int
	RequestID   string
	TraceID     string
	Traceparent string
	EventID     string
}

func (RequestHead) PortType() PortType { return PortRequestHead }

// RawRequest adds unverified body bytes. Head is a named field rather than an
// embedded type to make phase boundaries explicit. Go struct embedding does
// not itself make the containing type assignable to the embedded type.
type RawRequest struct {
	Head RequestHead
	Body []byte
}

func (RawRequest) PortType() PortType { return PortRawRequest }

// Verified is a RawRequest whose signature has been checked against the raw
// bytes by default (I6). Explicit protocols may verify a declared representation
// of the original bounded body before business transforms. Identity names the
// credential that passed, for logging and replay attribution.
type Verified struct {
	// OriginalJSON is request-local immutable serialization metadata, attached
	// only after successful request authentication. It is not persisted and
	// must match Raw.Body before reuse. Sharing it across clones is safe.
	OriginalJSON *jsoncanon.Representation `json:"-"`
	Raw          RawRequest
	Identity     string
	// CredentialID is operator-configured and stable across credential rotation.
	// JWT selectors are populated only by the verifier after signature, expiry,
	// issuer and audience checks; no_auth leaves them empty.
	CredentialID string `json:"-"`
	Subject      string `json:"-"`
	Tenant       string `json:"-"`
	Issuer       string `json:"-"`
	TrustScope   string `json:"-"`
	// Reservations is request-local compensation metadata; it is neither
	// persisted nor exposed to user scripts. Only the owner can release a guard.
	Reservations map[string]GuardReservation `json:"-"`
}
type GuardReservation struct{ Key, Owner string }

func (Verified) PortType() PortType { return PortVerified }

// Envelope is the publishable phase: a verified request plus its parsed body
// and resolved variables. Upstream is populated by an http_request node and
// consumed by a respond node, which is how synchronous proxying works.
type Envelope struct {
	Publish *deliverymeta.Publish
	V       Verified
	Body    any
	// BodyFormat is the envelope node's normalized parser mode. It lets
	// adapters distinguish an intentionally raw body from parsed JSON null.
	BodyFormat string
	Vars       map[string]string
	Upstream   *UpstreamResponse
	// Extra holds node-private data such as the captured webhook event.
	// Clone requires BodyCloner (or a JSON-cloneable body type).
	Extra any
	// Branch is the labelled output port selected by a switch node. Empty
	// means the executor follows every successor (ordinary fan-out).
	Branch string
	// MessageAttributes are SNS/SQS attributes rendered by a publish node.
	// They travel on the envelope so the serving adapter can attach them
	// without flowconfig importing internal/queue.
	MessageAttributes map[string]MessageAttribute
	// PayloadMode is set by a publish node (envelope|object) so the serving
	// adapter can populate Message object fields for s3 / redis_kv.
	PayloadMode string
}

// MessageAttribute is one SNS/SQS message attribute. DataType is String,
// Number, Binary, or String.Array (custom suffixes supported). Value is StringValue (JSON for arrays), or base64 for Binary.
type MessageAttribute struct {
	DataType string
	Value    string
}

func (Envelope) PortType() PortType { return PortEnvelope }

// UpstreamResponse is an upstream HTTP reply captured by an http_request node.
type UpstreamResponse struct {
	// Values contains only explicitly selected, bounded scalar response fields.
	Values         map[string]string
	SelectionError string
	Status         int
	Header         http.Header
	Body           []byte
}

// Terminal is a leaf result. Response is nil unless a respond node ran, in
// which case the server writes it; otherwise the caller gets the default
// 202 plus the Deliveries summary.
//
// Terminal is ALWAYS populated by Run, including when Run returns an error.
// Run returns the already-succeeded and still-unattempted destination lists
// even on a 503 so replay has a complete picture, and the
// caller needs Deliveries to decide whether to compensate (see
// NeedsCompensation).
type Terminal struct {
	// SideEffects is monotonic request-local evidence: an outbound operation
	// succeeded or may have reached its destination. Such requests retain guards.
	SideEffects bool
	// DropReason is finite, configuration-supplied filter metadata.
	DropReason string
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
	// NoEffect means dispatch was known not to have started. A delivery error
	// without this evidence may represent an uncertain remote acceptance.
	NoEffect bool
	NodeID   string
	// RenderedTarget is delivery metadata for responses/replay, never a metric label.
	RenderedTarget string
	Target         string
	Err            error
	// Unattempted distinguishes a destination skipped after failure from a
	// successful publish. Err can be nil in either case.
	Unattempted bool
}
