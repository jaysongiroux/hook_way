package capture

import (
	"errors"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestErrorMessageIsExported(t *testing.T) {
	if got := ErrorMessage(errors.New("post \"https://u:secret@h/x?k=1\": \x00down")); got != "post \"https://h\": down" {
		t.Fatalf("ErrorMessage = %q", got)
	}
}

func TestAttemptsAndCleanTextAreExported(t *testing.T) {
	if Attempts(errors.New("x")) != 1 {
		t.Fatal("a plain error made one attempt")
	}
	if got := CleanText("a\x00b\x1bc"); got != "abc" {
		t.Fatalf("CleanText = %q", got)
	}
}

func TestLimitedLowersButNeverRaisesTheArtifactLimit(t *testing.T) {
	b := testBuilder(t, 1<<20)
	if rec, _ := b.Limited(16).FromFailure("orders", testHead, 202, graphEvent()); rec.Metadata.NotRetryableReason != replaywire.NotRetryableTooLarge {
		t.Fatalf("Limited(16) record = %+v", rec.Metadata)
	}
	small := testBuilder(t, 16)
	if rec, _ := small.Limited(1<<30).FromFailure("orders", testHead, 202, graphEvent()); rec.Metadata.Retryable {
		t.Fatal("Limited raised the builder's own limit")
	}
	if rec, _ := b.Limited(0).FromFailure("orders", testHead, 202, graphEvent()); !rec.Metadata.Retryable {
		t.Fatal("Limited(0) must keep the builder's limit")
	}
}

func TestMetadataOnlyDropsTheArtifacts(t *testing.T) {
	rec, _ := testBuilder(t, 1<<20).FromFailure("orders", testHead, 202, graphEvent())
	m := rec.MetadataOnly(replaywire.NotRetryableTooLarge)
	if m.Snapshot != nil || m.Payload != nil || m.Metadata.Retryable || m.Metadata.Snapshot != nil || m.Metadata.Payload != nil ||
		m.Metadata.NotRetryableReason != replaywire.NotRetryableTooLarge {
		t.Fatalf("MetadataOnly = %+v", m.Metadata)
	}
	if err := m.Metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	if !rec.Metadata.Retryable {
		t.Fatal("MetadataOnly changed the original record")
	}
}

func TestCapturerExposesItsBuilder(t *testing.T) {
	var none *Capturer
	if none.Builder() != nil {
		t.Fatal("a nil capturer has a builder")
	}
	c := newTestCapturer(t, "http://127.0.0.1:1", "", newFakeMetrics())
	if c.Builder() == nil {
		t.Fatal("no builder")
	}
}
