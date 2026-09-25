package api

import (
	"encoding/json"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

type failureSummary struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"`
	Status             string    `json:"status"`
	Flow               string    `json:"flow"`
	NodeID             string    `json:"node_id"`
	NodeType           string    `json:"node_type"`
	DestinationID      string    `json:"destination_id,omitempty"`
	DestinationType    string    `json:"destination_type,omitempty"`
	Target             string    `json:"target,omitempty"`
	RenderedTarget     string    `json:"rendered_target,omitempty"`
	EventID            string    `json:"event_id"`
	FailedAt           time.Time `json:"failed_at"`
	ErrorClass         string    `json:"error_class"`
	Retryable          bool      `json:"retryable"`
	NotRetryableReason string    `json:"not_retryable_reason,omitempty"`
	ParentID           string    `json:"parent_id,omitempty"`
	RootID             string    `json:"root_id"`
	CapturedAt         time.Time `json:"captured_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

type failureDetail struct {
	failureSummary
	RequestID       string        `json:"request_id,omitempty"`
	TraceID         string        `json:"trace_id,omitempty"`
	IdempotencyKey  string        `json:"idempotency_key,omitempty"`
	ReceivedAt      time.Time     `json:"received_at"`
	InboundStatus   int           `json:"inbound_status"`
	ErrorMessage    string        `json:"error_message,omitempty"`
	Attempts        int           `json:"attempts"`
	NodeConfigHash  string        `json:"node_config_hash,omitempty"`
	DestinationHash string        `json:"destination_hash,omitempty"`
	CoreVersion     string        `json:"core_version,omitempty"`
	CoreInstance    string        `json:"core_instance,omitempty"`
	Artifacts       artifactsView `json:"artifacts"`
	Runs            []runView     `json:"runs"`
	History         []eventView   `json:"history"`
	Children        []string      `json:"children"`
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
	FailureID                string          `json:"failure_id"`
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

type listResponse struct {
	Items      []failureSummary `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func summaryOf(f store.Failure) failureSummary {
	return failureSummary{
		ID: f.ID, Kind: f.Kind, Status: f.Status, Flow: f.Flow, NodeID: f.NodeID, NodeType: f.NodeType,
		DestinationID: f.DestinationID, DestinationType: f.DestinationType, Target: f.Target, RenderedTarget: f.RenderedTarget,
		EventID: f.EventID, FailedAt: f.FailedAt, ErrorClass: f.ErrorClass, Retryable: f.Retryable,
		NotRetryableReason: f.NotRetryableReason, ParentID: f.ParentID, RootID: f.RootID,
		CapturedAt: f.CapturedAt, ExpiresAt: f.ExpiresAt,
	}
}

func detailOf(d store.Detail) failureDetail {
	out := failureDetail{
		failureSummary: summaryOf(d.Failure),
		RequestID:      d.RequestID, TraceID: d.TraceID, IdempotencyKey: d.IdempotencyKey, ReceivedAt: d.ReceivedAt,
		InboundStatus: d.InboundStatus, ErrorMessage: d.ErrorMessage, Attempts: d.Attempts,
		NodeConfigHash: d.NodeConfigHash, DestinationHash: d.DestinationHash, CoreVersion: d.CoreVersion, CoreInstance: d.CoreInstance,
		Artifacts: artifactsView{Stored: d.HasArtifacts},
		Runs:      make([]runView, 0, len(d.Runs)),
		History:   make([]eventView, 0, len(d.History)),
		Children:  append([]string{}, d.Children...),
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
		RunID: r.RunID, FailureID: r.FailureID, Status: r.Status, RequestedBy: r.RequestedBy, IdempotencyKey: r.IdempotencyKey,
		AcknowledgeDuplicateRisk: r.AcknowledgeDuplicateRisk, CreatedAt: r.CreatedAt, ClaimedAt: r.ClaimedAt,
		LeaseExpiresAt: r.LeaseExpiresAt, WorkerID: r.WorkerID, FinishedAt: r.FinishedAt, Result: r.Result,
	}
}
