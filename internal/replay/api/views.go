package api

import (
	"encoding/json"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

type actionEligibility struct {
	Allowed                          bool   `json:"allowed"`
	Reason                           string `json:"reason,omitempty"`
	RequiresDuplicateAcknowledgement bool   `json:"requires_duplicate_acknowledgement"`
}

type recordActions struct {
	Retry   actionEligibility `json:"retry"`
	Dismiss actionEligibility `json:"dismiss"`
	Resolve actionEligibility `json:"resolve"`
}

type RecordSummary struct {
	Actions                  recordActions `json:"actions"`
	ID                       string        `json:"id"`
	Kind                     string        `json:"kind"`
	ResolutionSource         string        `json:"resolution_source,omitempty"`
	ReplayMode               string        `json:"replay_mode,omitempty"`
	DeliveryStatus           string        `json:"delivery_status,omitempty"`
	DismissedAt              *time.Time    `json:"dismissed_at,omitempty"`
	Flow                     string        `json:"flow"`
	NodeID                   string        `json:"node_id"`
	NodeType                 string        `json:"node_type"`
	DestinationID            string        `json:"destination_id,omitempty"`
	DestinationType          string        `json:"destination_type,omitempty"`
	Target                   string        `json:"target,omitempty"`
	RenderedTarget           string        `json:"rendered_target,omitempty"`
	EventID                  string        `json:"event_id"`
	OccurredAt               time.Time     `json:"occurred_at"`
	ErrorClass               string        `json:"error_class,omitempty"`
	Retryable                bool          `json:"retryable"`
	NotRetryableReason       string        `json:"not_retryable_reason,omitempty"`
	PayloadUnavailableReason string        `json:"payload_unavailable_reason,omitempty"`
	ParentID                 string        `json:"parent_id,omitempty"`
	RootID                   string        `json:"root_id"`
	CapturedAt               time.Time     `json:"captured_at"`
	ExpiresAt                time.Time     `json:"expires_at"`
}

type RecordDetail struct {
	RecordSummary
	RequestID       string            `json:"request_id,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	ReceivedAt      time.Time         `json:"received_at"`
	InboundStatus   int               `json:"inbound_status"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	Attempts        *int              `json:"attempts,omitempty"`
	NodeConfigHash  string            `json:"node_config_hash,omitempty"`
	DestinationHash string            `json:"destination_hash,omitempty"`
	CoreVersion     string            `json:"core_version,omitempty"`
	CoreInstance    string            `json:"core_instance,omitempty"`
	Artifacts       artifactsView     `json:"artifacts"`
	Runs            []runView         `json:"runs" openapi:"nonnullable"`
	History         []eventView       `json:"history" openapi:"nonnullable"`
	Resolution      *store.Resolution `json:"resolution,omitempty"`
	Children        []string          `json:"children" openapi:"nonnullable"`
}

// artifactsView describes the artifacts as captured — sizes and digests,
// never bytes. Stored is false for a metadata-only record and after dismiss.
type artifactsView struct {
	Stored   bool          `json:"stored"`
	Snapshot *snapshotView `json:"snapshot"`
	Payload  *payloadView  `json:"payload"`
}

type snapshotView struct {
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Version int    `json:"version"`
}

type payloadView struct {
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type,omitempty"`
	Filename    string `json:"filename,omitempty"`
}

type runView struct {
	RunID                    string          `json:"run_id"`
	RecordID                 string          `json:"record_id"`
	Status                   string          `json:"status"`
	RequestedBy              string          `json:"requested_by"`
	IdempotencyKey           string          `json:"idempotency_key"`
	AcknowledgeDuplicateRisk bool            `json:"acknowledge_duplicate_risk"`
	CreatedAt                time.Time       `json:"created_at"`
	ClaimedAt                *time.Time      `json:"claimed_at"`
	LeaseExpiresAt           *time.Time      `json:"lease_expires_at"`
	WorkerID                 string          `json:"worker_id,omitempty"`
	FinishedAt               *time.Time      `json:"finished_at"`
	Result                   json.RawMessage `json:"result"`
}

type eventView struct {
	Event  string          `json:"event"`
	Actor  string          `json:"actor"`
	At     time.Time       `json:"at"`
	Detail json.RawMessage `json:"detail"`
}

type RecordList struct {
	Items      []RecordSummary `json:"items" openapi:"nonnullable"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func summaryOf(f store.Record) RecordSummary {
	return RecordSummary{
		Actions: actionsOf(f, time.Now()),
		ID:      f.ID, Kind: f.Kind, ReplayMode: f.ReplayMode, DeliveryStatus: f.DeliveryStatus, DismissedAt: f.DismissedAt, ResolutionSource: f.ResolutionSource, Flow: f.Flow, NodeID: f.NodeID, NodeType: f.NodeType,
		DestinationID: f.DestinationID, DestinationType: f.DestinationType, Target: f.Target, RenderedTarget: f.RenderedTarget,
		EventID: f.EventID, OccurredAt: f.OccurredAt, ErrorClass: f.ErrorClass, Retryable: f.Retryable,
		NotRetryableReason: f.NotRetryableReason, PayloadUnavailableReason: f.PayloadUnavailableReason, ParentID: f.ParentID, RootID: f.RootID,
		CapturedAt: f.CapturedAt, ExpiresAt: f.ExpiresAt,
	}
}

func detailOf(d store.RecordDetail) RecordDetail {
	out := RecordDetail{
		RecordSummary: summaryOf(d.Record),
		Resolution:    d.Resolution,
		RequestID:     d.RequestID, TraceID: d.TraceID, IdempotencyKey: d.IdempotencyKey, ReceivedAt: d.ReceivedAt,
		InboundStatus: d.InboundStatus, ErrorMessage: d.ErrorMessage,
		NodeConfigHash: d.NodeConfigHash, DestinationHash: d.DestinationHash, CoreVersion: d.CoreVersion, CoreInstance: d.CoreInstance,
		Artifacts: artifactsView{Stored: d.HasArtifacts},
		Runs:      make([]runView, 0, len(d.Runs)),
		History:   make([]eventView, 0, len(d.History)),
		Children:  append([]string{}, d.Children...),
	}
	if d.Kind == "failure" {
		out.Attempts = &d.Attempts
	}
	if s := d.Snapshot; s != nil {
		out.Artifacts.Snapshot = &snapshotView{Size: s.Size, SHA256: s.SHA256, Version: s.Version}
	}
	if p := d.Payload; p != nil {
		out.Artifacts.Payload = &payloadView{Size: p.Size, SHA256: p.SHA256, ContentType: p.ContentType, Filename: p.Filename}
	}
	for _, r := range d.Runs {
		out.Runs = append(out.Runs, runOf(r))
	}
	for _, e := range d.History {
		out.History = append(out.History, eventView{Event: e.Event, Actor: e.Actor, At: e.At, Detail: e.Detail})
	}
	return out
}

func runOf(r store.Run) runView {
	return runView{
		RunID: r.RunID, RecordID: r.RecordID, Status: r.Status, RequestedBy: r.RequestedBy, IdempotencyKey: r.IdempotencyKey,
		AcknowledgeDuplicateRisk: r.AcknowledgeDuplicateRisk, CreatedAt: r.CreatedAt, ClaimedAt: r.ClaimedAt,
		LeaseExpiresAt: r.LeaseExpiresAt, WorkerID: r.WorkerID, FinishedAt: r.FinishedAt, Result: r.Result,
	}
}

// Eligibility describes current record state, independently of the caller's scopes.
// Each mutation rechecks state inside the store transaction.
func actionsOf(f store.Record, now time.Time) recordActions {
	open := f.DismissedAt == nil && (f.DeliveryStatus == store.StatusOpen || f.DeliveryStatus == store.StatusUnknown)
	retry := actionEligibility{Allowed: open && f.Retryable, RequiresDuplicateAcknowledgement: open && f.DeliveryStatus == store.StatusUnknown}
	if !retry.Allowed {
		retry.Reason = "This record has no eligible destination retry in its current state."
	}
	if !f.Retryable && f.NotRetryableReason != "" {
		switch f.NotRetryableReason {
		case "too_large":
			retry.Reason = "Captured artifacts exceeded the size limit. Metadata is available, but this delivery cannot be retried."
		case "unsupported":
			retry.Reason = "The original input could not be captured for a safe retry. Inspect metadata and investigate the destination."
		default:
			retry.Reason = f.NotRetryableReason
		}
	}
	if f.Kind == "inspection" {
		retry = actionEligibility{Reason: "Inspections are view only and cannot be replayed."}
	}
	resolve := actionEligibility{Allowed: f.DeliveryStatus == store.StatusUnknown && f.Kind == "failure" && f.DismissedAt == nil}
	if !resolve.Allowed {
		resolve.Reason = "Only uncertain deliveries can be confirmed by an operator."
	}
	if !f.ExpiresAt.After(now) {
		retry.Allowed = false
		retry.Reason = "Retention has expired. New retries are unavailable."
		resolve.Allowed = false
		resolve.Reason = "Retention has expired. New confirmations are unavailable."
	}
	dismiss := actionEligibility{Allowed: f.DismissedAt == nil && (open || f.Kind == "inspection")}
	if !dismiss.Allowed {
		dismiss.Reason = "Only open or uncertain records can be dismissed; wait for active work to finish."
	}
	return recordActions{Retry: retry, Dismiss: dismiss, Resolve: resolve}
}
