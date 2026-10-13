package replaywire

import (
	"errors"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func validResume() RecordMetadata {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	return RecordMetadata{
		ID: "01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8e", Kind: KindFailure, ReplayMode: ReplayResume,
		Flow: "orders", NodeID: "out_s3", NodeType: "publish",
		DestinationID: "archive", DestinationType: "s3", Target: "uploads", RenderedTarget: "uploads",
		EventID: "evt-1", RequestID: "req-1", ReceivedAt: at, OccurredAt: at.Add(time.Second),
		InboundStatus: 202, ErrorClass: ErrorClass5xx, ErrorMessage: "s3: 503", Attempts: 1,
		Retryable: true,
		Snapshot:  &ArtifactInfo{Size: 10, SHA256: digest, Version: 1},
		Payload:   &ArtifactInfo{Size: 4, SHA256: digest, ContentType: "application/json"},
	}
}

func validPublishOnly() RecordMetadata {
	m := validResume()
	m.ReplayMode, m.NodeType, m.DestinationType = ReplayPublishOnly, "publish", "webhook"
	return m
}

func validMetadataOnly() RecordMetadata {
	m := validResume()
	m.Retryable, m.NotRetryableReason, m.Snapshot, m.Payload = false, NotRetryableTooLarge, nil, nil
	m.PayloadUnavailableReason = NotRetryableTooLarge
	return m
}

func TestValidateAcceptsValidRecords(t *testing.T) {
	for name, m := range map[string]RecordMetadata{
		"resume": validResume(), "publish_only": validPublishOnly(), "metadata only": validMetadataOnly(),
	} {
		if err := m.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateAcceptsAnEmptyPayload(t *testing.T) {
	m := validResume()
	m.Payload.Size = 0
	if err := m.Validate(); err != nil {
		t.Fatalf("an empty payload (for example an empty raw body) is valid: %v", err)
	}
}

func TestValidateReportsEachProblem(t *testing.T) {
	cases := map[string]struct {
		mutate func(*RecordMetadata)
		want   string
	}{
		"id not a uuid":         {func(m *RecordMetadata) { m.ID = "not-a-uuid" }, "id"},
		"id uppercase":          {func(m *RecordMetadata) { m.ID = strings.ToUpper(m.ID) }, "id"},
		"unknown kind":          {func(m *RecordMetadata) { m.Kind = "other" }, "kind"},
		"missing flow":          {func(m *RecordMetadata) { m.Flow = "" }, "flow"},
		"missing node_id":       {func(m *RecordMetadata) { m.NodeID = "" }, "node_id"},
		"missing node_type":     {func(m *RecordMetadata) { m.NodeType = "" }, "node_type"},
		"missing event_id":      {func(m *RecordMetadata) { m.EventID = "" }, "event_id"},
		"long flow":             {func(m *RecordMetadata) { m.Flow = strings.Repeat("f", 1025) }, "flow"},
		"control char":          {func(m *RecordMetadata) { m.Target = "a\nb" }, "target"},
		"zero received_at":      {func(m *RecordMetadata) { m.ReceivedAt = time.Time{} }, "received_at"},
		"zero occurred_at":      {func(m *RecordMetadata) { m.OccurredAt = time.Time{} }, "occurred_at"},
		"non-2xx inbound":       {func(m *RecordMetadata) { m.InboundStatus = 503 }, "inbound_status"},
		"unknown error class":   {func(m *RecordMetadata) { m.ErrorClass = "weird" }, "error_class"},
		"long error message":    {func(m *RecordMetadata) { m.ErrorMessage = strings.Repeat("e", MaxErrorMessageLen+1) }, "error_message"},
		"nul in error message":  {func(m *RecordMetadata) { m.ErrorMessage = "boom\x00" }, "error_message"},
		"bad utf-8 in message":  {func(m *RecordMetadata) { m.ErrorMessage = "caf\xe9" }, "error_message"},
		"bad utf-8 in target":   {func(m *RecordMetadata) { m.Target = "caf\xe9" }, "target"},
		"attempts overflow":     {func(m *RecordMetadata) { m.Attempts = math.MaxInt32 + 1 }, "attempts"},
		"version overflow":      {func(m *RecordMetadata) { m.Snapshot.Version = math.MaxInt32 + 1 }, "snapshot.version"},
		"negative attempts":     {func(m *RecordMetadata) { m.Attempts = -1 }, "attempts"},
		"publish_only no dest":  {func(m *RecordMetadata) { m.ReplayMode = ReplayPublishOnly; m.DestinationID = "" }, "destination_id"},
		"publish_only no rt":    {func(m *RecordMetadata) { m.ReplayMode = ReplayPublishOnly; m.RenderedTarget = "" }, "rendered_target"},
		"retryable no snapshot": {func(m *RecordMetadata) { m.Snapshot = nil }, "snapshot"},
		"retryable no payload":  {func(m *RecordMetadata) { m.Payload = nil }, "payload"},
		"retryable reason":      {func(m *RecordMetadata) { m.NotRetryableReason = NotRetryableTooLarge }, "not_retryable_reason"},
		"not retryable no reason": {func(m *RecordMetadata) {
			m.Retryable, m.Snapshot, m.Payload = false, nil, nil
		}, "not_retryable_reason"},
		"not retryable artifacts": {func(m *RecordMetadata) {
			m.Retryable, m.NotRetryableReason = false, NotRetryableUnsupported
		}, "artifacts"},
		"empty snapshot":          {func(m *RecordMetadata) { m.Snapshot.Size = 0 }, "snapshot.size"},
		"bad snapshot digest":     {func(m *RecordMetadata) { m.Snapshot.SHA256 = "abc" }, "snapshot.sha256"},
		"snapshot no version":     {func(m *RecordMetadata) { m.Snapshot.Version = 0 }, "snapshot.version"},
		"snapshot content type":   {func(m *RecordMetadata) { m.Snapshot.ContentType = "x/y" }, "snapshot.content_type"},
		"negative payload size":   {func(m *RecordMetadata) { m.Payload.Size = -1 }, "payload.size"},
		"payload version":         {func(m *RecordMetadata) { m.Payload.Version = 1 }, "payload.version"},
		"payload bad filename":    {func(m *RecordMetadata) { m.Payload.Filename = "../etc/passwd" }, "payload.filename"},
		"payload bad contenttype": {func(m *RecordMetadata) { m.Payload.ContentType = "text/plain\r\nX: y" }, "payload.content_type"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := validResume()
			c.mutate(&m)
			err := m.Validate()
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("Validate = %v, want a *ValidationError", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate = %q, want a problem naming %q", err, c.want)
			}
		})
	}
}

// Both services import this package, so it must stay free of either
// service's dependencies.
func TestPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if first := strings.SplitN(path, "/", 2)[0]; strings.Contains(first, ".") {
				t.Errorf("%s imports %s; only the standard library is allowed", name, path)
			}
		}
	}
}

func TestVerifyArtifact(t *testing.T) {
	data := []byte("test")
	info := &ArtifactInfo{Size: 4, SHA256: digest}
	if err := VerifyArtifact("payload", info, true, data); err != nil {
		t.Fatalf("matching part: %v", err)
	}
	if err := VerifyArtifact("payload", nil, false, nil); err != nil {
		t.Fatalf("absent and undeclared: %v", err)
	}
	for name, c := range map[string]struct {
		info    *ArtifactInfo
		present bool
		data    []byte
	}{
		"undeclared but present": {nil, true, data},
		"declared but missing":   {info, false, nil},
		"size differs":           {&ArtifactInfo{Size: 5, SHA256: digest}, true, data},
		"digest differs":         {&ArtifactInfo{Size: 4, SHA256: strings.Repeat("0", 64)}, true, data},
	} {
		if err := VerifyArtifact("payload", c.info, c.present, c.data); err == nil || !strings.Contains(err.Error(), "payload") {
			t.Errorf("%s: VerifyArtifact = %v, want an error naming the part", name, err)
		}
	}
}

func TestInspectionRecordPurposeAndPayloadPolicy(t *testing.T) {
	m := RecordMetadata{ID: validResume().ID, Kind: KindInspection, Flow: "orders", NodeID: "look", NodeType: "inspect", EventID: "event", ReceivedAt: time.Now(), OccurredAt: time.Now(), InboundStatus: 503, NotRetryableReason: NotRetryableInspectionOnly, PayloadUnavailableReason: NotRetryableTooLarge}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RecordMetadata){
		"inspection mode":                 func(m *RecordMetadata) { m.ReplayMode = ReplayResume },
		"missing time":                    func(m *RecordMetadata) { m.OccurredAt = time.Time{} },
		"wrong retry policy":              func(m *RecordMetadata) { m.NotRetryableReason = NotRetryableTooLarge },
		"missing unavailable reason":      func(m *RecordMetadata) { m.PayloadUnavailableReason = "" },
		"payload with unavailable reason": func(m *RecordMetadata) { m.Payload = validResume().Payload },
	} {
		t.Run(name, func(t *testing.T) {
			copy := m
			mutate(&copy)
			if err := copy.Validate(); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func TestInspectionJSONRejectsFailureFieldsEvenWhenEmpty(t *testing.T) {
	for _, field := range InspectionForbiddenMetadataFields() {
		for _, value := range []string{"null", `""`, "0"} {
			data := []byte(`{"kind":"inspection","retryable":false,"` + field + `":` + value + `}`)
			if _, err := DecodeRecordMetadata(data); err == nil {
				t.Fatalf("inspection accepted %s=%s", field, value)
			}
		}
	}
	for _, data := range []string{`{"kind":"inspection","retryable":false}`, `{"kind":"failure","retryable":false,"attempts":0}`} {
		if _, err := DecodeRecordMetadata([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	for _, data := range []string{`{"kind":"inspection","unexpected":0}`, `{"kind":"inspection"} {}`} {
		if _, err := DecodeRecordMetadata([]byte(data)); err == nil {
			t.Fatalf("accepted malformed metadata: %s", data)
		}
	}
}

func TestCaptureJSONRequiresExplicitBooleanAndZeroSize(t *testing.T) {
	for _, input := range []string{
		`{"kind":"inspection"}`,
		`{"kind":"inspection","retryable":null}`,
		`{"kind":"failure","retryable":false,"error_message":null}`,
		`{"kind":"inspection","retryable":false,"payload":{"sha256":"abc"}}`,
		`{"kind":"inspection","retryable":false,"payload":{"size":null,"sha256":"abc"}}`,
		`{"kind":"inspection","retryable":false,"payload":{"size":0,"sha256":"abc","content_type":null}}`,
		`{"kind":"inspection","retryable":false,"payload":{"size":0,"sha256":"abc","filename":null}}`,
		`{"kind":"inspection","retryable":false,"payload":{"size":0,"sha256":"abc","version":null}}`,
	} {
		if _, err := DecodeRecordMetadata([]byte(input)); err == nil {
			t.Fatalf("accepted invalid capture JSON: %s", input)
		}
	}
	if _, err := DecodeRecordMetadata([]byte(`{"kind":"inspection","retryable":false,"payload":{"size":0,"sha256":"abc"}}`)); err != nil {
		t.Fatalf("explicit zero-byte size was rejected: %v", err)
	}
}
