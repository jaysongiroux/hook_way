// Package replaywire holds the HTTP wire types core and the replay service
// exchange. It depends only on the standard library so both binaries can
// import it without pulling in each other's dependencies.
package replaywire

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Multipart part names of POST /v1/failures.
const (
	PartMetadata = "metadata"
	PartSnapshot = "snapshot"
	PartPayload  = "payload"
)

// Record kinds (spec "Record kinds").
const (
	KindResume      = "resume"
	KindPublishOnly = "publish_only"
)

// Error classes (spec "Record metadata").
const (
	ErrorClassTimeout    = "timeout"
	ErrorClass5xx        = "5xx"
	ErrorClass4xx        = "4xx"
	ErrorClass429        = "429"
	ErrorClassConnection = "connection"
	ErrorClassQueueFull  = "queue_full"
	ErrorClassPanic      = "panic"
	ErrorClassOther      = "other"
)

// Reasons a record can never be retried. Such records carry no artifacts.
const (
	NotRetryableTooLarge    = "too_large"
	NotRetryableUnsupported = "unsupported"
)

// MaxErrorMessageLen bounds the sanitized error message core sends.
const MaxErrorMessageLen = 4096

// maxFieldLen bounds every other text field, so one record cannot bloat the
// failures table or a list response.
const maxFieldLen = 1024

// ArtifactInfo describes one binary part of an ingest request. SHA256 is the
// lowercase hex digest of the part's bytes. Version applies to snapshots;
// ContentType and Filename apply to payloads.
type ArtifactInfo struct {
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Version     int    `json:"version,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Filename    string `json:"filename,omitempty"`
}

// FailureMetadata is the JSON metadata part of POST /v1/failures: everything
// replay stores about a failure except the artifact bytes.
type FailureMetadata struct {
	ID                 string        `json:"id"`
	Kind               string        `json:"kind"`
	Flow               string        `json:"flow"`
	NodeID             string        `json:"node_id"`
	NodeType           string        `json:"node_type"`
	DestinationID      string        `json:"destination_id,omitempty"`
	DestinationType    string        `json:"destination_type,omitempty"`
	Target             string        `json:"target,omitempty"`
	RenderedTarget     string        `json:"rendered_target,omitempty"`
	EventID            string        `json:"event_id"`
	RequestID          string        `json:"request_id,omitempty"`
	TraceID            string        `json:"trace_id,omitempty"`
	IdempotencyKey     string        `json:"idempotency_key,omitempty"`
	ReceivedAt         time.Time     `json:"received_at"`
	FailedAt           time.Time     `json:"failed_at"`
	InboundStatus      int           `json:"inbound_status"`
	ErrorClass         string        `json:"error_class"`
	ErrorMessage       string        `json:"error_message,omitempty"`
	Attempts           int           `json:"attempts"`
	NodeConfigHash     string        `json:"node_config_hash,omitempty"`
	DestinationHash    string        `json:"destination_hash,omitempty"`
	CoreVersion        string        `json:"core_version,omitempty"`
	CoreInstance       string        `json:"core_instance,omitempty"`
	Retryable          bool          `json:"retryable"`
	NotRetryableReason string        `json:"not_retryable_reason,omitempty"`
	Snapshot           *ArtifactInfo `json:"snapshot,omitempty"`
	Payload            *ArtifactInfo `json:"payload,omitempty"`
}

// ValidationError lists every problem Validate found, in field order.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid failure metadata: " + strings.Join(e.Problems, "; ")
}

var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate reports whether m is a record replay can store. It enforces the
// capture rules that are checkable at the boundary: the sender was
// acknowledged (2xx), and a record carries both artifacts exactly when it is
// retryable.
func (m FailureMetadata) Validate() error {
	var v validator
	if !uuidPattern.MatchString(m.ID) {
		v.addf("id must be a lowercase canonical UUID")
	}
	v.oneOf("kind", m.Kind, KindResume, KindPublishOnly)
	for _, f := range []struct{ name, value string }{
		{"flow", m.Flow}, {"node_id", m.NodeID}, {"node_type", m.NodeType}, {"event_id", m.EventID},
	} {
		if f.value == "" {
			v.addf("%s is required", f.name)
		}
	}
	for _, f := range []struct{ name, value string }{
		{"flow", m.Flow}, {"node_id", m.NodeID}, {"node_type", m.NodeType},
		{"destination_id", m.DestinationID}, {"destination_type", m.DestinationType},
		{"target", m.Target}, {"rendered_target", m.RenderedTarget}, {"event_id", m.EventID},
		{"request_id", m.RequestID}, {"trace_id", m.TraceID}, {"idempotency_key", m.IdempotencyKey},
		{"node_config_hash", m.NodeConfigHash}, {"destination_hash", m.DestinationHash},
		{"core_version", m.CoreVersion}, {"core_instance", m.CoreInstance},
	} {
		v.text(f.name, f.value, maxFieldLen)
	}
	if m.ReceivedAt.IsZero() {
		v.addf("received_at is required")
	}
	if m.FailedAt.IsZero() {
		v.addf("failed_at is required")
	}
	if m.InboundStatus < 200 || m.InboundStatus > 299 {
		v.addf("inbound_status must be 2xx: only failures the sender was acknowledged for are captured")
	}
	v.oneOf("error_class", m.ErrorClass, ErrorClassTimeout, ErrorClass5xx, ErrorClass4xx, ErrorClass429,
		ErrorClassConnection, ErrorClassQueueFull, ErrorClassPanic, ErrorClassOther)
	if len(m.ErrorMessage) > MaxErrorMessageLen {
		v.addf("error_message exceeds %d bytes", MaxErrorMessageLen)
	}
	// Newlines are fine in an error message; NUL and invalid UTF-8 are not
	// storable text.
	if strings.ContainsRune(m.ErrorMessage, 0) || !utf8.ValidString(m.ErrorMessage) {
		v.addf("error_message must be valid UTF-8 without NUL bytes")
	}
	if m.Attempts < 0 || m.Attempts > math.MaxInt32 {
		v.addf("attempts must be between 0 and %d", math.MaxInt32)
	}
	if m.Kind == KindPublishOnly {
		for _, f := range []struct{ name, value string }{
			{"destination_id", m.DestinationID}, {"destination_type", m.DestinationType},
			{"target", m.Target}, {"rendered_target", m.RenderedTarget},
		} {
			if f.value == "" {
				v.addf("%s is required for a publish_only record", f.name)
			}
		}
	}
	if m.Retryable {
		if m.NotRetryableReason != "" {
			v.addf("not_retryable_reason must be empty for a retryable record")
		}
		if m.Snapshot == nil {
			v.addf("snapshot is required for a retryable record")
		}
		if m.Payload == nil {
			v.addf("payload is required for a retryable record")
		}
	} else {
		v.oneOf("not_retryable_reason", m.NotRetryableReason, NotRetryableTooLarge, NotRetryableUnsupported)
		if m.Snapshot != nil || m.Payload != nil {
			v.addf("a record that is not retryable carries no artifacts")
		}
	}
	if s := m.Snapshot; s != nil {
		if s.Size < 1 {
			v.addf("snapshot.size must be >= 1")
		}
		v.digest("snapshot.sha256", s.SHA256)
		if s.Version < 1 || s.Version > math.MaxInt32 {
			v.addf("snapshot.version must be between 1 and %d", math.MaxInt32)
		}
		if s.ContentType != "" || s.Filename != "" {
			v.addf("snapshot.content_type and snapshot.filename must be empty")
		}
	}
	if p := m.Payload; p != nil {
		if p.Size < 0 {
			v.addf("payload.size must be >= 0")
		}
		v.digest("payload.sha256", p.SHA256)
		if p.Version != 0 {
			v.addf("payload.version must be empty")
		}
		v.text("payload.content_type", p.ContentType, 255)
		v.text("payload.filename", p.Filename, 255)
		if strings.ContainsAny(p.Filename, `/\`) || p.Filename == "." || p.Filename == ".." {
			v.addf("payload.filename must be a bare file name")
		}
	}
	return v.err()
}

type validator struct{ problems []string }

func (v *validator) addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *validator) oneOf(name, value string, allowed ...string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	v.addf("%s must be one of %s", name, strings.Join(allowed, ", "))
}

// text bounds a field's length and rejects control characters and invalid
// UTF-8, so stored values are valid PostgreSQL text and safe to echo in
// headers, logs, and JSON.
func (v *validator) text(name, value string, limit int) {
	if len(value) > limit {
		v.addf("%s exceeds %d bytes", name, limit)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		v.addf("%s contains control characters", name)
	}
	if !utf8.ValidString(value) {
		v.addf("%s is not valid UTF-8", name)
	}
}

func (v *validator) digest(name, value string) {
	if !sha256Pattern.MatchString(value) {
		v.addf("%s must be 64 lowercase hex characters", name)
	}
}

func (v *validator) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

// VerifyArtifact checks a multipart part against its declaration: present
// exactly when declared, with the declared size and sha256. name labels the
// part in the error.
func VerifyArtifact(name string, info *ArtifactInfo, present bool, data []byte) error {
	switch {
	case info == nil && present:
		return fmt.Errorf("the %s part is present but the metadata declares none", name)
	case info == nil:
		return nil
	case !present:
		return fmt.Errorf("the metadata declares a %s but the part is missing", name)
	case int64(len(data)) != info.Size:
		return fmt.Errorf("the %s part is %d bytes; the metadata declares %d", name, len(data), info.Size)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != info.SHA256 {
		return fmt.Errorf("the %s part's sha256 does not match the metadata", name)
	}
	return nil
}
