// Package deliverysnapshot is the versioned, lossless encoding of a failed
// delivery. Core captures a snapshot, the replay service stores it as opaque
// bytes, and core decodes it again to redrive. The package depends only on
// the standard library so both services can import it; converting to and from
// core's Go types lives in internal/capture.
//
// Every map, slice, and byte-slice field is encoded without omitempty, so a
// nil value and an empty value decode back to what they were.
package deliverysnapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"time"
)

// Version is the snapshot format this package writes.
const Version = 2

// SupportsVersion reports formats that this build can decode.
func SupportsVersion(version int) bool { return version == 1 || version == Version }

// Kind says how a snapshot is redriven.
type Kind string

const (
	// KindResume snapshots a flow node's input; a redrive resumes the flow at
	// that node.
	KindResume Kind = "resume"
	// KindPublishOnly snapshots a message already prepared for an async webhook;
	// a redrive delivers it to the same destination and target.
	KindPublishOnly Kind = "publish_only"
)

var (
	// ErrUnsupportedVersion reports a snapshot written in a format this build
	// does not read.
	ErrUnsupportedVersion = errors.New("deliverysnapshot: unsupported version")
	// ErrMalformed reports bytes that are not a valid snapshot.
	ErrMalformed = errors.New("deliverysnapshot: malformed snapshot")
)

// maxDecodedBytes bounds decompression so a corrupt or hostile artifact cannot
// exhaust memory. A variable only so tests can lower it.
var maxDecodedBytes int64 = 256 << 20

// Snapshot is one captured delivery. Exactly one of Envelope (KindResume) and
// Message (KindPublishOnly) is set.
type Snapshot struct {
	Version  int       `json:"version"`
	Kind     Kind      `json:"kind"`
	Envelope *Envelope `json:"envelope,omitempty"`
	Message  *Message  `json:"message,omitempty"`
}

// Encode writes s as gzip-compressed JSON, stamping the current Version.
func Encode(s Snapshot) ([]byte, error) {
	s.Version = Version
	if err := s.validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("deliverysnapshot: encode: %w", err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, fmt.Errorf("deliverysnapshot: compress: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("deliverysnapshot: compress: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode reads a snapshot written by Encode.
func Decode(data []byte) (Snapshot, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	defer func() { _ = zr.Close() }()
	raw, err := io.ReadAll(io.LimitReader(zr, maxDecodedBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if int64(len(raw)) > maxDecodedBytes {
		return Snapshot{}, fmt.Errorf("%w: decompressed size exceeds %d bytes", ErrMalformed, maxDecodedBytes)
	}
	// Read the version before the shape: a newer format may not fit these
	// types, and the caller needs to know the real cause.
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if !SupportsVersion(head.Version) {
		return Snapshot{}, fmt.Errorf("%w: %d (this build reads %d)", ErrUnsupportedVersion, head.Version, Version)
	}
	// An unknown field means a newer build forgot to bump the version;
	// silently dropping it would redrive a different input.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Snapshot{}, fmt.Errorf("%w: trailing data after the snapshot", ErrMalformed)
	}
	if s.Version == 1 && s.Envelope != nil && s.Envelope.Flags != nil {
		return Snapshot{}, fmt.Errorf("%w: v1 snapshots cannot contain flag metadata", ErrMalformed)
	}
	if err := s.validate(); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

func (s Snapshot) validate() error {
	switch s.Kind {
	case KindResume:
		if s.Envelope == nil || s.Message != nil {
			return fmt.Errorf("%w: a resume snapshot carries an envelope and no message", ErrMalformed)
		}
	case KindPublishOnly:
		if s.Message == nil || s.Envelope != nil {
			return fmt.Errorf("%w: a publish_only snapshot carries a message and no envelope", ErrMalformed)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrMalformed, s.Kind)
	}
	return nil
}

// Envelope mirrors graph.Envelope. Extra is represented only by
// CapturedEvent, the one Extra type the flow nodes produce.
type Envelope struct {
	Publish           *Publish                    `json:"publish,omitempty"`
	Verified          Verified                    `json:"verified"`
	Body              *Value                      `json:"body"`
	BodyFormat        string                      `json:"body_format"`
	Vars              map[string]string           `json:"vars"`
	Flags             *Value                      `json:"flags,omitempty"`
	Upstream          *Upstream                   `json:"upstream"`
	CapturedEvent     *CapturedEvent              `json:"captured_event"`
	Branch            string                      `json:"branch"`
	MessageAttributes map[string]MessageAttribute `json:"message_attributes"`
	PayloadMode       string                      `json:"payload_mode"`
}

// Verified mirrors graph.Verified.
type Verified struct {
	Raw      RawRequest `json:"raw"`
	Identity string     `json:"identity"`
}

// RawRequest mirrors graph.RawRequest.
type RawRequest struct {
	Head RequestHead `json:"head"`
	Body []byte      `json:"body"`
}

// RequestHead mirrors graph.RequestHead.
type RequestHead struct {
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	PathVars    map[string]string   `json:"path_vars"`
	Query       map[string][]string `json:"query"`
	Header      map[string][]string `json:"header"`
	SourceIP    string              `json:"source_ip"`
	Hops        int                 `json:"hops"`
	RequestID   string              `json:"request_id"`
	TraceID     string              `json:"trace_id"`
	Traceparent string              `json:"traceparent"`
	EventID     string              `json:"event_id"`
}

// Upstream mirrors graph.UpstreamResponse.
type Upstream struct {
	Values         map[string]string   `json:"values,omitempty"`
	SelectionError string              `json:"selection_error,omitempty"`
	Status         int                 `json:"status"`
	Header         map[string][]string `json:"header"`
	Body           []byte              `json:"body"`
}

// MessageAttribute mirrors graph.MessageAttribute and queue.MessageAttribute
// (whose StringValue maps to Value).
type MessageAttribute struct {
	DataType string `json:"data_type"`
	Value    string `json:"value"`
}

// CapturedEvent mirrors flowconfig.CapturedEvent.
type CapturedEvent struct {
	Event WebhookEnvelope `json:"event"`
}

// WebhookEnvelope mirrors webhook.Envelope, including its in-memory fields.
type WebhookEnvelope struct {
	SchemaVersion      string              `json:"schema_version"`
	ID                 string              `json:"id"`
	IdempotencyKey     string              `json:"idempotency_key"`
	RequestID          string              `json:"request_id"`
	TraceID            string              `json:"trace_id"`
	Traceparent        string              `json:"traceparent"`
	Endpoint           string              `json:"endpoint"`
	DestinationQueueID string              `json:"destination_queue_id"`
	Queue              string              `json:"queue"`
	ReceivedAt         time.Time           `json:"received_at"`
	Method             string              `json:"method"`
	Path               string              `json:"path"`
	Headers            map[string][]string `json:"headers"`
	Query              *Value              `json:"query"`
	Body               *Value              `json:"body"`
	RawBodyBase64      *string             `json:"raw_body_base64"`
	SourceIP           string              `json:"source_ip"`
	Auth               AuthInfo            `json:"auth"`
	Variables          map[string]string   `json:"variables"`
	RawBody            []byte              `json:"raw_body"`
	OrigHeaders        map[string][]string `json:"orig_headers"`
	Hops               int                 `json:"hops"`
}

// AuthInfo mirrors webhook.AuthInfo.
type AuthInfo struct {
	Type     string `json:"type"`
	Scheme   string `json:"scheme"`
	Verified bool   `json:"verified"`
}

// Message mirrors queue.Message, including its in-memory fields. Auth is nil
// when the message carried no auth information.
type Message struct {
	Publish            *Publish                    `json:"publish,omitempty"`
	SchemaVersion      string                      `json:"schema_version"`
	ID                 string                      `json:"id"`
	IdempotencyKey     string                      `json:"idempotency_key"`
	RequestID          string                      `json:"request_id"`
	TraceID            string                      `json:"trace_id"`
	Traceparent        string                      `json:"traceparent"`
	Endpoint           string                      `json:"endpoint"`
	DestinationQueueID string                      `json:"destination_queue_id"`
	Queue              string                      `json:"queue"`
	ReceivedAt         time.Time                   `json:"received_at"`
	Method             string                      `json:"method"`
	Path               string                      `json:"path"`
	Headers            map[string][]string         `json:"headers"`
	Query              *Value                      `json:"query"`
	Body               *Value                      `json:"body"`
	RawBodyBase64      *string                     `json:"raw_body_base64"`
	SourceIP           string                      `json:"source_ip"`
	Auth               *AuthInfo                   `json:"auth"`
	Variables          map[string]string           `json:"variables"`
	RawBody            []byte                      `json:"raw_body"`
	OrigHeaders        map[string][]string         `json:"orig_headers"`
	Hops               int                         `json:"hops"`
	DeadLetter         *DeadLetterMeta             `json:"dead_letter"`
	MessageAttributes  map[string]MessageAttribute `json:"message_attributes"`
	PayloadMode        string                      `json:"payload_mode"`
	ObjectBytes        []byte                      `json:"object_bytes"`
	ObjectContentType  string                      `json:"object_content_type"`
	ObjectFilename     string                      `json:"object_filename"`
}

// DeadLetterMeta mirrors queue.DeadLetterMeta.
type DeadLetterMeta struct {
	Destination string    `json:"destination"`
	Target      string    `json:"target"`
	Outcome     string    `json:"outcome"`
	Attempts    int       `json:"attempts"`
	FailedAt    time.Time `json:"failed_at"`
}

// ValueType names the Go type a body value had when it was captured.
type ValueType string

// Value types. Numbers carry their exact decimal text in Value.Text. The *_nil
// variants record a typed nil, so a nil map or slice decodes as nil and an
// empty one as empty. string_bytes is a string that is not valid UTF-8 (for
// example a latin-1 multipart filename); JSON cannot carry it as text, so its
// exact bytes travel in Value.Bytes.
const (
	TypeNull         ValueType = "null"
	TypeBool         ValueType = "bool"
	TypeString       ValueType = "string"
	TypeBinaryString ValueType = "string_bytes"
	TypeJSONNumber   ValueType = "json_number"
	TypeBytes        ValueType = "bytes"
	TypeNilBytes     ValueType = "bytes_nil"
	TypeInt          ValueType = "int"
	TypeInt8         ValueType = "int8"
	TypeInt16        ValueType = "int16"
	TypeInt32        ValueType = "int32"
	TypeInt64        ValueType = "int64"
	TypeUint         ValueType = "uint"
	TypeUint8        ValueType = "uint8"
	TypeUint16       ValueType = "uint16"
	TypeUint32       ValueType = "uint32"
	TypeUint64       ValueType = "uint64"
	TypeFloat32      ValueType = "float32"
	TypeFloat64      ValueType = "float64"
	TypeMap          ValueType = "map"
	TypeNilMap       ValueType = "map_nil"
	TypeList         ValueType = "list"
	TypeNilList      ValueType = "list_nil"
	TypeStringMap    ValueType = "string_map"
	TypeNilStringMap ValueType = "string_map_nil"
	TypeStrings      ValueType = "strings"
	TypeNilStrings   ValueType = "strings_nil"
	TypeXML          ValueType = "xml"
)

// Value is a lossless, type-tagged encoding of one body value. Type decides
// which payload field is meaningful; the others are empty.
type Value struct {
	Type    ValueType         `json:"t"`
	Bool    bool              `json:"b,omitempty"`
	Text    string            `json:"s,omitempty"`
	Bytes   []byte            `json:"y,omitempty"`
	Map     map[string]*Value `json:"m,omitempty"`
	List    []*Value          `json:"l,omitempty"`
	StrMap  map[string]string `json:"sm,omitempty"`
	Strings []string          `json:"ss,omitempty"`
	XML     *XMLDocument      `json:"x,omitempty"`
}

// XMLDocument mirrors transform.Document.
type XMLDocument struct {
	Declaration string       `json:"declaration"`
	Root        *XMLElement  `json:"root"`
	Before      []XMLContent `json:"before"`
	After       []XMLContent `json:"after"`
}

// XMLElement mirrors transform.Element.
type XMLElement struct {
	Name         xml.Name      `json:"name"`
	Attr         []xml.Attr    `json:"attr"`
	Children     []*XMLElement `json:"children"`
	Text         string        `json:"text"`
	Prefix       string        `json:"prefix"`
	QName        string        `json:"qname"`
	AttrPrefixes []string      `json:"attr_prefixes"`
	Content      []XMLContent  `json:"content"`
}

// XMLContent points into the containing element's Children, preserving identity
// without storing the same subtree twice. Top-level content has no child reference.
type XMLContent struct {
	Child  *int   `json:"child,omitempty"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Target string `json:"target"`
}
