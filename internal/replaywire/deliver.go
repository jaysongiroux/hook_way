package replaywire

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Multipart part names of POST /internal/replay/v1/deliver. The request
// carries PartRequest and PartSnapshot; a 200 response carries PartResult
// and, per child record i, ChildSnapshotPart(i) and ChildPayloadPart(i).
const (
	PartRequest = "request"
	PartResult  = "result"
)

// DeliverPath is the control API route on core's control listener.
const DeliverPath = "/internal/replay/v1/deliver"

// MaxResultBytes bounds the result JSON part of a deliver response.
const MaxResultBytes = 1 << 20

// Connection reuse between replay's executor and core's control listener.
// The client drops idle connections well before the server does, so a
// request is never written onto a connection the server is closing.
const (
	ControlServerIdleTimeout = 2 * time.Minute
	ControlClientIdleTimeout = 30 * time.Second
)

func ChildSnapshotPart(i int) string { return "child-" + strconv.Itoa(i) + "-snapshot" }
func ChildPayloadPart(i int) string  { return "child-" + strconv.Itoa(i) + "-payload" }

// Limits core must respect in a deliver response. A child whose artifacts do
// not fit is sent metadata-only (retryable=false, too_large).
const (
	MaxChildren             = 64
	MaxDeliverResponseBytes = 64 << 20
)

// NotStartedCode is the 503 problem code core returns when it provably did
// not start the delivery (busy or draining). Only then may replay resend.
const NotStartedCode = "not_started"

// Node outcomes in a deliver result.
const (
	NodeDelivered = "delivered"
	NodeFailed    = "failed"
	NodeHandled   = "handled" // failed, and an on_error path handled it
	NodeNotRun    = "not_run"
)

// Block reasons: the problem code of a 422 from the control API. Nothing was
// sent to any destination.
const (
	BlockUnsupportedSnapshotVersion = "unsupported_snapshot_version"
	BlockHashMismatch               = "hash_mismatch"
	BlockFlowMissing                = "flow_missing"
	BlockFlowBroken                 = "flow_broken"
	BlockNodeMissing                = "node_missing"
	BlockNodeTypeChanged            = "node_type_changed"
	BlockDestinationChanged         = "destination_changed"
	BlockTargetNotConfigured        = "target_not_configured"
)

// DeliverRequest is the JSON request part: which record to redrive, the
// configuration identity it was captured under, and core's budget.
type DeliverRequest struct {
	RunID           string `json:"run_id"`
	RecordID        string `json:"record_id"`
	Kind            string `json:"kind"`
	ReplayMode      string `json:"replay_mode"`
	Flow            string `json:"flow"`
	NodeID          string `json:"node_id"`
	NodeType        string `json:"node_type"`
	DestinationID   string `json:"destination_id,omitempty"`
	DestinationType string `json:"destination_type,omitempty"`
	RenderedTarget  string `json:"rendered_target,omitempty"`
	NodeConfigHash  string `json:"node_config_hash,omitempty"`
	DestinationHash string `json:"destination_hash,omitempty"`
	SnapshotSHA256  string `json:"snapshot_sha256"`
	SnapshotVersion int    `json:"snapshot_version"`
	// InboundStatus is the record's original inbound status; core stamps it
	// on child records, which replay accepts only with a 2xx.
	InboundStatus int `json:"inbound_status"`
	// MaxRecordBytes is replay's per-artifact limit; a child artifact over it
	// is sent metadata-only.
	MaxRecordBytes int64 `json:"max_record_bytes"`
	DeadlineMS     int64 `json:"deadline_ms"`
}

// NodeOutcome is one node's result in a redrive.
type NodeOutcome struct {
	NodeID         string `json:"node_id"`
	Outcome        string `json:"outcome"`
	ErrorClass     string `json:"error_class,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
	RenderedTarget string `json:"rendered_target,omitempty"`
	Attempts       int    `json:"attempts"`
}

// DeliverResult is the JSON result part of a 200 response. Children are the
// downstream failures of a run whose resume node succeeded; each becomes a
// child record, its artifacts in parts ChildSnapshotPart(i) and
// ChildPayloadPart(i). The hashes are the current configuration's.
type DeliverResult struct {
	Nodes           []NodeOutcome    `json:"nodes"`
	Children        []RecordMetadata `json:"children"`
	NodeConfigHash  string           `json:"node_config_hash,omitempty"`
	DestinationHash string           `json:"destination_hash,omitempty"`
	CoreVersion     string           `json:"core_version,omitempty"`
	CoreInstance    string           `json:"core_instance,omitempty"`
}

// ResumeOutcome returns nodeID's outcome.
func (r DeliverResult) ResumeOutcome(nodeID string) (NodeOutcome, bool) {
	for _, n := range r.Nodes {
		if n.NodeID == nodeID {
			return n, true
		}
	}
	return NodeOutcome{}, false
}

// Validate checks a result against the run it answers: the resume node ran,
// every outcome is known, and children are valid, bounded, downstream, and
// present only when the resume node succeeded.
func (r DeliverResult) Validate(resumeNodeID string) error {
	var v validator
	seen := map[string]bool{}
	for i, n := range r.Nodes {
		if n.NodeID == "" {
			v.addf("nodes[%d].node_id is required", i)
		}
		if seen[n.NodeID] {
			v.addf("node %q is reported twice", n.NodeID)
		}
		seen[n.NodeID] = true
		v.oneOf(fmt.Sprintf("nodes[%d].outcome", i), n.Outcome, NodeDelivered, NodeFailed, NodeHandled, NodeNotRun)
		v.text(fmt.Sprintf("nodes[%d].node_id", i), n.NodeID, maxFieldLen)
		v.text(fmt.Sprintf("nodes[%d].rendered_target", i), n.RenderedTarget, maxFieldLen)
		if len(n.ErrorMessage) > MaxErrorMessageLen {
			v.addf("nodes[%d].error_message exceeds %d bytes", i, MaxErrorMessageLen)
		}
		if strings.ContainsRune(n.ErrorMessage, 0) || !utf8.ValidString(n.ErrorMessage) {
			v.addf("nodes[%d].error_message must be valid UTF-8 without NUL bytes", i)
		}
		if n.ErrorClass != "" {
			v.oneOf(fmt.Sprintf("nodes[%d].error_class", i), n.ErrorClass, ErrorClassTimeout, ErrorClass5xx, ErrorClass4xx,
				ErrorClass429, ErrorClassConnection, ErrorClassQueueFull, ErrorClassPanic, ErrorClassOther)
		}
	}
	resume, ok := r.ResumeOutcome(resumeNodeID)
	switch {
	case !ok:
		v.addf("the resume node %q is not in the result", resumeNodeID)
	case resume.Outcome == NodeNotRun:
		v.addf("the resume node %q did not run", resumeNodeID)
	}
	succeeded := ok && (resume.Outcome == NodeDelivered || resume.Outcome == NodeHandled)
	if len(r.Children) > 0 && !succeeded {
		v.addf("children are reported only when the resume node succeeded")
	}
	if len(r.Children) > MaxChildren {
		v.addf("%d children exceed the limit of %d", len(r.Children), MaxChildren)
	}
	ids := map[string]bool{}
	for i, c := range r.Children {
		if err := c.Validate(); err != nil {
			v.addf("children[%d]: %v", i, err)
		}
		if c.Kind != KindFailure || c.ReplayMode != ReplayResume {
			v.addf("children[%d] must be a resume record", i)
		}
		if c.NodeID == resumeNodeID {
			v.addf("children[%d] must fail downstream of the resume node", i)
		}
		if ids[c.ID] {
			v.addf("child %s is reported twice", c.ID)
		}
		ids[c.ID] = true
	}
	for _, f := range []struct{ name, value string }{
		{"node_config_hash", r.NodeConfigHash}, {"destination_hash", r.DestinationHash},
		{"core_version", r.CoreVersion}, {"core_instance", r.CoreInstance},
	} {
		v.text(f.name, f.value, maxFieldLen)
	}
	return v.err()
}
