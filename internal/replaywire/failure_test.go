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

func validResume() FailureMetadata {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	return FailureMetadata{
		ID: "01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8e", Kind: KindResume,
		Flow: "orders", NodeID: "out_s3", NodeType: "publish",
		DestinationID: "archive", DestinationType: "s3", Target: "uploads", RenderedTarget: "uploads",
		EventID: "evt-1", RequestID: "req-1", ReceivedAt: at, FailedAt: at.Add(time.Second),
		InboundStatus: 202, ErrorClass: ErrorClass5xx, ErrorMessage: "s3: 503", Attempts: 1,
		Retryable: true,
		Snapshot:  &ArtifactInfo{Size: 10, SHA256: digest, Version: 1},
		Payload:   &ArtifactInfo{Size: 4, SHA256: digest, ContentType: "application/json"},
	}
}

func validPublishOnly() FailureMetadata {
	m := validResume()
	m.Kind, m.NodeType, m.DestinationType = KindPublishOnly, "publish", "webhook"
	return m
}

func validMetadataOnly() FailureMetadata {
	m := validResume()
	m.Retryable, m.NotRetryableReason, m.Snapshot, m.Payload = false, NotRetryableTooLarge, nil, nil
	return m
}

func TestValidateAcceptsValidRecords(t *testing.T) {
	for name, m := range map[string]FailureMetadata{
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
		mutate func(*FailureMetadata)
		want   string
	}{
		"id not a uuid":         {func(m *FailureMetadata) { m.ID = "not-a-uuid" }, "id"},
		"id uppercase":          {func(m *FailureMetadata) { m.ID = strings.ToUpper(m.ID) }, "id"},
		"unknown kind":          {func(m *FailureMetadata) { m.Kind = "other" }, "kind"},
		"missing flow":          {func(m *FailureMetadata) { m.Flow = "" }, "flow"},
		"missing node_id":       {func(m *FailureMetadata) { m.NodeID = "" }, "node_id"},
		"missing node_type":     {func(m *FailureMetadata) { m.NodeType = "" }, "node_type"},
		"missing event_id":      {func(m *FailureMetadata) { m.EventID = "" }, "event_id"},
		"long flow":             {func(m *FailureMetadata) { m.Flow = strings.Repeat("f", 1025) }, "flow"},
		"control char":          {func(m *FailureMetadata) { m.Target = "a\nb" }, "target"},
		"zero received_at":      {func(m *FailureMetadata) { m.ReceivedAt = time.Time{} }, "received_at"},
		"zero failed_at":        {func(m *FailureMetadata) { m.FailedAt = time.Time{} }, "failed_at"},
		"non-2xx inbound":       {func(m *FailureMetadata) { m.InboundStatus = 503 }, "inbound_status"},
		"unknown error class":   {func(m *FailureMetadata) { m.ErrorClass = "weird" }, "error_class"},
		"long error message":    {func(m *FailureMetadata) { m.ErrorMessage = strings.Repeat("e", MaxErrorMessageLen+1) }, "error_message"},
		"nul in error message":  {func(m *FailureMetadata) { m.ErrorMessage = "boom\x00" }, "error_message"},
		"bad utf-8 in message":  {func(m *FailureMetadata) { m.ErrorMessage = "caf\xe9" }, "error_message"},
		"bad utf-8 in target":   {func(m *FailureMetadata) { m.Target = "caf\xe9" }, "target"},
		"attempts overflow":     {func(m *FailureMetadata) { m.Attempts = math.MaxInt32 + 1 }, "attempts"},
		"version overflow":      {func(m *FailureMetadata) { m.Snapshot.Version = math.MaxInt32 + 1 }, "snapshot.version"},
		"negative attempts":     {func(m *FailureMetadata) { m.Attempts = -1 }, "attempts"},
		"publish_only no dest":  {func(m *FailureMetadata) { m.Kind = KindPublishOnly; m.DestinationID = "" }, "destination_id"},
		"publish_only no rt":    {func(m *FailureMetadata) { m.Kind = KindPublishOnly; m.RenderedTarget = "" }, "rendered_target"},
		"retryable no snapshot": {func(m *FailureMetadata) { m.Snapshot = nil }, "snapshot"},
		"retryable no payload":  {func(m *FailureMetadata) { m.Payload = nil }, "payload"},
		"retryable reason":      {func(m *FailureMetadata) { m.NotRetryableReason = NotRetryableTooLarge }, "not_retryable_reason"},
		"not retryable no reason": {func(m *FailureMetadata) {
			m.Retryable, m.Snapshot, m.Payload = false, nil, nil
		}, "not_retryable_reason"},
		"not retryable artifacts": {func(m *FailureMetadata) {
			m.Retryable, m.NotRetryableReason = false, NotRetryableUnsupported
		}, "artifacts"},
		"empty snapshot":          {func(m *FailureMetadata) { m.Snapshot.Size = 0 }, "snapshot.size"},
		"bad snapshot digest":     {func(m *FailureMetadata) { m.Snapshot.SHA256 = "abc" }, "snapshot.sha256"},
		"snapshot no version":     {func(m *FailureMetadata) { m.Snapshot.Version = 0 }, "snapshot.version"},
		"snapshot content type":   {func(m *FailureMetadata) { m.Snapshot.ContentType = "x/y" }, "snapshot.content_type"},
		"negative payload size":   {func(m *FailureMetadata) { m.Payload.Size = -1 }, "payload.size"},
		"payload version":         {func(m *FailureMetadata) { m.Payload.Version = 1 }, "payload.version"},
		"payload bad filename":    {func(m *FailureMetadata) { m.Payload.Filename = "../etc/passwd" }, "payload.filename"},
		"payload bad contenttype": {func(m *FailureMetadata) { m.Payload.ContentType = "text/plain\r\nX: y" }, "payload.content_type"},
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
