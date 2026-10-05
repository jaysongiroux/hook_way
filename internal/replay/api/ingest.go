package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

type captureResponse struct {
	ID string `json:"id"`
}

// ingest stores one captured failure (POST /v1/failures). 201 creates it,
// 200 acknowledges a re-send of the same record.
func (a *api) ingest(w http.ResponseWriter, r *http.Request) {
	f, err := a.readIngest(w, r)
	if err != nil {
		a.metrics.Capture(rejectionOutcome(err))
		a.fail(w, r, err)
		return
	}
	f.Actor = auth.Actor(r.Context())
	res, err := a.store.Ingest(r.Context(), f)
	switch {
	case errors.Is(err, store.ErrConflict):
		a.metrics.Capture(metrics.CaptureConflict)
	case errors.Is(err, store.ErrCapacity):
		a.metrics.Capture(metrics.CaptureCapacity)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if res == store.Duplicate {
		status = http.StatusOK
		a.metrics.Capture(metrics.CaptureDuplicate)
	} else {
		a.metrics.Capture(metrics.CaptureCreated)
		if f.Snapshot != nil {
			a.metrics.CaptureArtifactBytes(replaywire.PartSnapshot, len(f.Snapshot))
			a.metrics.CaptureArtifactBytes(replaywire.PartPayload, len(f.Payload))
		}
	}
	writeJSON(w, status, captureResponse{ID: f.Metadata.ID})
}

func rejectionOutcome(err error) string {
	if ae := classify(err); ae != nil && ae.status == http.StatusRequestEntityTooLarge {
		return metrics.CaptureTooLarge
	}
	return metrics.CaptureInvalid
}

// readIngest parses and verifies the multipart body. Artifacts are buffered
// because the metadata part that declares them may arrive after them; each is
// bounded by max_record_bytes and the whole body by twice that plus the
// metadata allowance.
func (a *api) readIngest(w http.ResponseWriter, r *http.Request) (store.NewFailure, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 2*a.maxRecordBytes+maxMetadataBytes+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		return store.NewFailure{}, newError(http.StatusBadRequest, "invalid_request", "the body must be multipart/form-data: %v", err)
	}
	var f store.NewFailure
	seen := map[string]bool{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return store.NewFailure{}, bodyError(err)
		}
		name := part.FormName()
		if seen[name] {
			return store.NewFailure{}, newError(http.StatusBadRequest, "invalid_request", "the %q part appears more than once", name)
		}
		seen[name] = true
		switch name {
		case replaywire.PartMetadata:
			data, over, err := readPart(part, maxMetadataBytes)
			if err != nil {
				return store.NewFailure{}, bodyError(err)
			}
			if over {
				return store.NewFailure{}, newError(http.StatusBadRequest, "invalid_metadata", "the metadata part exceeds %d bytes", maxMetadataBytes)
			}
			if err := decodeMetadata(data, &f.Metadata); err != nil {
				return store.NewFailure{}, err
			}
		case replaywire.PartSnapshot, replaywire.PartPayload:
			data, over, err := readPart(part, a.maxRecordBytes)
			if err != nil {
				return store.NewFailure{}, bodyError(err)
			}
			if over {
				return store.NewFailure{}, newError(http.StatusRequestEntityTooLarge, "record_too_large",
					"the %s part exceeds max_record_bytes (%d)", name, a.maxRecordBytes)
			}
			if name == replaywire.PartSnapshot {
				f.Snapshot = data
			} else {
				f.Payload = data
			}
		default:
			return store.NewFailure{}, newError(http.StatusBadRequest, "invalid_request", "unexpected part %q; send metadata, snapshot, and payload", name)
		}
	}
	if !seen[replaywire.PartMetadata] {
		return store.NewFailure{}, newError(http.StatusBadRequest, "invalid_metadata", "the metadata part is required")
	}
	if err := f.Metadata.Validate(); err != nil {
		return store.NewFailure{}, newError(http.StatusBadRequest, "invalid_metadata", "%v", err)
	}
	if err := replaywire.VerifyArtifact(replaywire.PartSnapshot, f.Metadata.Snapshot, seen[replaywire.PartSnapshot], f.Snapshot); err != nil {
		return store.NewFailure{}, newError(http.StatusBadRequest, "artifact_mismatch", "%v", err)
	}
	if err := replaywire.VerifyArtifact(replaywire.PartPayload, f.Metadata.Payload, seen[replaywire.PartPayload], f.Payload); err != nil {
		return store.NewFailure{}, newError(http.StatusBadRequest, "artifact_mismatch", "%v", err)
	}
	return f, nil
}

func readPart(r io.Reader, limit int64) (data []byte, over bool, err error) {
	data, err = io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return nil, true, nil
	}
	return data, false, nil
}

func bodyError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return newError(http.StatusRequestEntityTooLarge, "request_too_large", "the request body exceeds %d bytes", tooLarge.Limit)
	}
	return newError(http.StatusBadRequest, "invalid_request", "read multipart body: %v", err)
}

func decodeMetadata(data []byte, m *replaywire.FailureMetadata) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return newError(http.StatusBadRequest, "invalid_metadata", "the metadata part is not valid metadata JSON: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return newError(http.StatusBadRequest, "invalid_metadata", "the metadata part has trailing data")
	}
	return nil
}
