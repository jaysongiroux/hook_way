// Package replaywire holds the HTTP wire types core and the replay service
// exchange. It depends only on the standard library so both binaries can
// import it without pulling in each other's dependencies.
package replaywire

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Multipart part names of POST /v1/records.
const (
	PartMetadata = "metadata"
	PartSnapshot = "snapshot"
	PartPayload  = "payload"
)

// Record kinds (spec "Record kinds").
const (
	KindFailure       = "failure"
	KindInspection    = "inspection"
	ReplayResume      = "resume"
	ReplayPublishOnly = "publish_only"
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

// Reasons a record can never be retried. Inspections may retain display payloads.
const (
	NotRetryableInspectionOnly = "inspection_only"
	NotRetryableTooLarge       = "too_large"
	NotRetryableUnsupported    = "unsupported"
)

// MaxErrorMessageLen bounds the sanitized error message core sends.
const MaxErrorMessageLen = 4096

// maxFieldLen bounds every other text field, so one record cannot bloat the
// records table or a list response.
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

// RecordMetadata is the JSON metadata part of POST /v1/records: everything
// replay stores about a capture except the artifact bytes.
type RecordMetadata struct {
	ID                       string        `json:"id"`
	Kind                     string        `json:"kind"`
	ReplayMode               string        `json:"replay_mode,omitempty"`
	Flow                     string        `json:"flow"`
	NodeID                   string        `json:"node_id"`
	NodeType                 string        `json:"node_type"`
	DestinationID            string        `json:"destination_id,omitempty"`
	DestinationType          string        `json:"destination_type,omitempty"`
	Target                   string        `json:"target,omitempty"`
	RenderedTarget           string        `json:"rendered_target,omitempty"`
	EventID                  string        `json:"event_id"`
	RequestID                string        `json:"request_id,omitempty"`
	TraceID                  string        `json:"trace_id,omitempty"`
	IdempotencyKey           string        `json:"idempotency_key,omitempty"`
	ReceivedAt               time.Time     `json:"received_at"`
	OccurredAt               time.Time     `json:"occurred_at"`
	InboundStatus            int           `json:"inbound_status"`
	ErrorClass               string        `json:"error_class,omitempty"`
	ErrorMessage             string        `json:"error_message,omitempty"`
	Attempts                 int           `json:"attempts,omitempty"`
	NodeConfigHash           string        `json:"node_config_hash,omitempty"`
	DestinationHash          string        `json:"destination_hash,omitempty"`
	CoreVersion              string        `json:"core_version,omitempty"`
	CoreInstance             string        `json:"core_instance,omitempty"`
	Retryable                bool          `json:"retryable"`
	NotRetryableReason       string        `json:"not_retryable_reason,omitempty"`
	PayloadUnavailableReason string        `json:"payload_unavailable_reason,omitempty"`
	Snapshot                 *ArtifactInfo `json:"snapshot,omitempty"`
	Payload                  *ArtifactInfo `json:"payload,omitempty"`
}

// InspectionForbiddenMetadataFields is shared by wire decoding and OpenAPI.
// Presence is forbidden even when JSON null or an empty value decodes to zero.
func InspectionForbiddenMetadataFields() []string {
	return []string{"snapshot", "replay_mode", "error_class", "error_message", "attempts", "destination_id", "destination_type", "target", "rendered_target", "destination_hash"}
}

// DecodeRecordMetadata enforces the capture JSON contract before value validation.
func DecodeRecordMetadata(data []byte) (RecordMetadata, error) {
	var decoded RecordMetadata
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return RecordMetadata{}, err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RecordMetadata{}, fmt.Errorf("record metadata has trailing data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return RecordMetadata{}, err
	}
	if _, present := fields["retryable"]; !present {
		return RecordMetadata{}, fmt.Errorf("retryable is required")
	}
	for name, raw := range fields {
		if name != "snapshot" && name != "payload" && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return RecordMetadata{}, fmt.Errorf("%s must not be null", name)
		}
	}
	for _, name := range []string{"snapshot", "payload"} {
		raw, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var info map[string]json.RawMessage
		if err := json.Unmarshal(raw, &info); err != nil {
			return RecordMetadata{}, err
		}
		for field, value := range info {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return RecordMetadata{}, fmt.Errorf("%s.%s must not be null", name, field)
			}
		}
		size, present := info["size"]
		if !present || bytes.Equal(bytes.TrimSpace(size), []byte("null")) {
			return RecordMetadata{}, fmt.Errorf("%s.size is required and must not be null", name)
		}
	}
	if decoded.Kind == KindInspection {
		for _, field := range InspectionForbiddenMetadataFields() {
			if _, present := fields[field]; present {
				return RecordMetadata{}, fmt.Errorf("%s must be absent for an inspection", field)
			}
		}
	}
	return decoded, nil
}

// ValidationError lists every problem Validate found, in field order.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid record metadata: " + strings.Join(e.Problems, "; ")
}

var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate reports whether m is a record replay can store. It enforces the
// capture rules at the boundary. Failures require an acknowledged sender
// and both artifacts when retryable. Inspections accept any inbound status
// and may carry a display payload, but never an executable snapshot.
func (m RecordMetadata) Validate() error {
	var v validator
	if !uuidPattern.MatchString(m.ID) {
		v.addf("id must be a lowercase canonical UUID")
	}
	v.oneOf("kind", m.Kind, KindFailure, KindInspection)
	if m.Kind == KindFailure {
		v.oneOf("replay_mode", m.ReplayMode, ReplayResume, ReplayPublishOnly)
	} else if m.ReplayMode != "" {
		v.addf("replay_mode is only valid for failures")
	}
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
	if m.OccurredAt.IsZero() {
		v.addf("occurred_at is required")
	}
	if m.Kind != KindInspection && (m.InboundStatus < 200 || m.InboundStatus > 299) {
		v.addf("inbound_status must be 2xx: only failures the sender was acknowledged for are captured")
	}
	if m.Kind != KindInspection {
		v.oneOf("error_class", m.ErrorClass, ErrorClassTimeout, ErrorClass5xx, ErrorClass4xx, ErrorClass429,
			ErrorClassConnection, ErrorClassQueueFull, ErrorClassPanic, ErrorClassOther)
	}
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
	if m.ReplayMode == ReplayPublishOnly {
		for _, f := range []struct{ name, value string }{
			{"destination_id", m.DestinationID}, {"destination_type", m.DestinationType},
			{"target", m.Target}, {"rendered_target", m.RenderedTarget},
		} {
			if f.value == "" {
				v.addf("%s is required for a publish_only record", f.name)
			}
		}
	}
	if m.Kind == KindInspection {
		if m.InboundStatus < 100 || m.InboundStatus > 599 {
			v.addf("inbound_status must be an HTTP status")
		}
		if m.NodeType != "inspect" || m.Retryable || m.Snapshot != nil {
			v.addf("inspect must be view-only, with node_type inspect and no replay snapshot")
		}
		if m.ErrorClass != "" || m.ErrorMessage != "" || m.Attempts != 0 {
			v.addf("inspect carries no failure fields")
		}
		if m.DestinationID != "" || m.DestinationType != "" || m.Target != "" || m.RenderedTarget != "" || m.DestinationHash != "" {
			v.addf("inspect carries no destination")
		}
		v.oneOf("not_retryable_reason", m.NotRetryableReason, NotRetryableInspectionOnly)
	} else if m.Retryable {
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
	if m.Payload == nil {
		v.oneOf("payload_unavailable_reason", m.PayloadUnavailableReason, NotRetryableTooLarge, NotRetryableUnsupported)
		if m.Kind == KindFailure && m.PayloadUnavailableReason != m.NotRetryableReason {
			v.addf("payload_unavailable_reason must match the failure capture reason")
		}
	} else if m.PayloadUnavailableReason != "" {
		v.addf("payload_unavailable_reason must be absent when a payload is captured")
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

// RecordState groups lifecycle counts without conflating dismissal and delivery.
// Inspections have an empty DeliveryStatus.
type RecordState struct {
	Kind           string
	DeliveryStatus string
	Dismissed      bool
}
