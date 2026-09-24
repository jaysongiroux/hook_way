package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// encode builds the 200 multipart response: the result part, then each
// child's snapshot and payload. It enforces replay's limits so a real outcome
// is never lost to an oversized answer: at most MaxChildren children, child
// artifacts over replay's max_record_bytes or past the response budget sent
// metadata-only, and a result part within MaxResultBytes. It returns the
// resume node's outcome.
func (s *Server) encode(req replaywire.DeliverRequest, res Result) ([]byte, string, string, error) {
	kids := res.Children
	if len(kids) > replaywire.MaxChildren {
		s.opts.Logger.Warn("replay redrive had more downstream failures than one response carries; the rest are not reported",
			"run_id", req.RunID, "failures", len(kids), "reported", replaywire.MaxChildren)
		kids = kids[:replaywire.MaxChildren]
	}
	kept := make([]capture.Record, 0, len(kids))
	budget := s.budget
	for _, rec := range kids {
		if rec.Metadata.Snapshot != nil {
			size := int64(len(rec.Snapshot) + len(rec.Payload))
			overLimit := req.MaxRecordBytes > 0 && (int64(len(rec.Snapshot)) > req.MaxRecordBytes || int64(len(rec.Payload)) > req.MaxRecordBytes)
			if overLimit || size > budget {
				rec = rec.MetadataOnly(replaywire.NotRetryableTooLarge)
			} else {
				budget -= size
			}
		}
		kept = append(kept, rec)
	}
	for {
		result := replaywire.DeliverResult{
			Nodes: res.Nodes, Children: make([]replaywire.FailureMetadata, 0, len(kept)),
			NodeConfigHash: res.NodeConfigHash, DestinationHash: res.DestinationHash,
			CoreVersion: s.opts.CoreVersion, CoreInstance: s.opts.CoreInstance,
		}
		for _, rec := range kept {
			result.Children = append(result.Children, rec.Metadata)
		}
		if err := result.Validate(req.NodeID); err != nil {
			return nil, "", "", err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, "", "", err
		}
		if len(raw) <= replaywire.MaxResultBytes {
			resume, _ := result.ResumeOutcome(req.NodeID)
			body, contentType, err := writeParts(raw, kept)
			return body, contentType, resume.Outcome, err
		}
		if len(kept) == 0 {
			return nil, "", "", fmt.Errorf("the result is %d bytes, over the %d-byte limit", len(raw), replaywire.MaxResultBytes)
		}
		s.opts.Logger.Warn("replay redrive result is too large; dropping a downstream failure from it",
			"run_id", req.RunID, "failure_id", kept[len(kept)-1].Metadata.ID)
		kept = kept[:len(kept)-1]
	}
}

func writeParts(result []byte, kids []capture.Record) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	write := func(name string, data []byte) error {
		pw, err := w.CreateFormField(name)
		if err != nil {
			return err
		}
		_, err = pw.Write(data)
		return err
	}
	if err := write(replaywire.PartResult, result); err != nil {
		return nil, "", err
	}
	for i, rec := range kids {
		if rec.Metadata.Snapshot == nil {
			continue
		}
		if err := write(replaywire.ChildSnapshotPart(i), rec.Snapshot); err != nil {
			return nil, "", err
		}
		if err := write(replaywire.ChildPayloadPart(i), rec.Payload); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code"`
}

// writeProblem sends an RFC 9457 problem; replay's executor switches on code.
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	h := w.Header()
	h.Set("Content-Type", "application/problem+json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code})
}

// writeNotStarted is the only answer after which replay may resend: core
// provably did not start the delivery.
func writeNotStarted(w http.ResponseWriter, detail string) {
	w.Header().Set("Retry-After", "1")
	writeProblem(w, http.StatusServiceUnavailable, replaywire.NotStartedCode, detail)
}
