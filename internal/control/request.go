package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

const (
	maxRequestPartBytes = 64 << 10
	multipartSlack      = 64 << 10
	maxDeadlineMS       = 5 * 60 * 1000
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// requestError is a request core refuses before reading its meaning.
type requestError struct {
	status       int
	code, detail string
}

func (e *requestError) Error() string { return e.code + ": " + e.detail }

func badRequest(format string, args ...any) error {
	return &requestError{status: http.StatusBadRequest, code: "bad_request", detail: fmt.Sprintf(format, args...)}
}

func tooLarge(format string, args ...any) error {
	return &requestError{status: http.StatusRequestEntityTooLarge, code: "too_large", detail: fmt.Sprintf(format, args...)}
}

func readError(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return tooLarge("the request exceeds the control listener's %d-byte limit", mbe.Limit)
	}
	return badRequest("read the request: %v", err)
}

// readRequest reads the whole multipart body — exactly one request part and
// one snapshot part — and checks the request's fields.
func (s *Server) readRequest(w http.ResponseWriter, r *http.Request) (replaywire.DeliverRequest, []byte, error) {
	var req replaywire.DeliverRequest
	r.Body = http.MaxBytesReader(w, r.Body, s.opts.MaxRecordBytes+maxRequestPartBytes+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		return req, nil, badRequest("the request must be multipart/form-data: %v", err)
	}
	limits := map[string]int64{replaywire.PartRequest: maxRequestPartBytes, replaywire.PartSnapshot: s.opts.MaxRecordBytes}
	parts := map[string][]byte{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return req, nil, readError(err)
		}
		name := p.FormName()
		limit, known := limits[name]
		if !known {
			return req, nil, badRequest("unexpected part %q", name)
		}
		if _, dup := parts[name]; dup {
			return req, nil, badRequest("part %q appears twice", name)
		}
		data, err := io.ReadAll(io.LimitReader(p, limit+1))
		if err != nil {
			return req, nil, readError(err)
		}
		if int64(len(data)) > limit {
			return req, nil, tooLarge("part %q exceeds %d bytes", name, limit)
		}
		parts[name] = data
	}
	for _, name := range []string{replaywire.PartRequest, replaywire.PartSnapshot} {
		if _, ok := parts[name]; !ok {
			return req, nil, badRequest("the %q part is missing", name)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(parts[replaywire.PartRequest]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return replaywire.DeliverRequest{}, nil, badRequest("the request part: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return replaywire.DeliverRequest{}, nil, badRequest("the request part has trailing data")
	}
	if err := validateRequest(req); err != nil {
		return req, nil, err
	}
	return req, parts[replaywire.PartSnapshot], nil
}

func validateRequest(r replaywire.DeliverRequest) error {
	switch {
	case r.RunID == "" || r.RecordID == "":
		return badRequest("run_id and record_id are required")
	case r.Kind != replaywire.KindFailure:
		return badRequest("kind must be failure: inspections are view only")
	case r.ReplayMode != replaywire.ReplayResume && r.ReplayMode != replaywire.ReplayPublishOnly:
		return badRequest("replay_mode must be %s or %s", replaywire.ReplayResume, replaywire.ReplayPublishOnly)
	case r.Flow == "" || r.NodeID == "" || r.NodeType == "":
		return badRequest("flow, node_id, and node_type are required")
	case r.ReplayMode == replaywire.ReplayPublishOnly && (r.DestinationID == "" || r.RenderedTarget == ""):
		return badRequest("a publish_only request needs destination_id and rendered_target")
	case !sha256Hex.MatchString(r.SnapshotSHA256):
		return badRequest("snapshot_sha256 must be 64 lowercase hex characters")
	case r.DeadlineMS <= 0 || r.DeadlineMS > maxDeadlineMS:
		return badRequest("deadline_ms must be between 1 and %d", maxDeadlineMS)
	case r.InboundStatus < 200 || r.InboundStatus > 299:
		return badRequest("inbound_status must be 2xx: only acknowledged failures are captured")
	case r.MaxRecordBytes < 0:
		return badRequest("max_record_bytes must not be negative")
	}
	return nil
}

// checkSnapshot applies the snapshot block reasons, in the spec's order, and
// decodes it.
func checkSnapshot(req replaywire.DeliverRequest, raw []byte) (deliverysnapshot.Snapshot, error) {
	if req.SnapshotVersion != deliverysnapshot.Version {
		return deliverysnapshot.Snapshot{}, Blockf(replaywire.BlockUnsupportedSnapshotVersion,
			"snapshot version %d; this core reads version %d", req.SnapshotVersion, deliverysnapshot.Version)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != req.SnapshotSHA256 {
		return deliverysnapshot.Snapshot{}, Blockf(replaywire.BlockHashMismatch, "the snapshot does not match its sha256")
	}
	snap, err := deliverysnapshot.Decode(raw)
	switch {
	case errors.Is(err, deliverysnapshot.ErrUnsupportedVersion):
		return snap, Blockf(replaywire.BlockUnsupportedSnapshotVersion, "%v", err)
	case err != nil:
		return snap, fmt.Errorf("%w: %w", ErrMalformedSnapshot, err)
	case snap.Kind != deliverysnapshot.Kind(req.ReplayMode):
		return snap, fmt.Errorf("%w: a %s snapshot for a %s request", ErrMalformedSnapshot, snap.Kind, req.ReplayMode)
	case snap.Kind == deliverysnapshot.KindResume && snap.Envelope == nil,
		snap.Kind == deliverysnapshot.KindPublishOnly && snap.Message == nil:
		return snap, fmt.Errorf("%w: the %s snapshot is empty", ErrMalformedSnapshot, snap.Kind)
	}
	return snap, nil
}
