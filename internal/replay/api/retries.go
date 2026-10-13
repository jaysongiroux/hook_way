package api

import (
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

type retryBody struct {
	AcknowledgeDuplicateRisk bool `json:"acknowledge_duplicate_risk"`
}

type batchBody struct {
	RecordIDs                []string `json:"record_ids"`
	AcknowledgeDuplicateRisk bool     `json:"acknowledge_duplicate_risk"`
}

type batchItem struct {
	RecordID string `json:"record_id"`
	Status   int    `json:"status"`
	RunID    string `json:"run_id,omitempty"`
	Code     string `json:"code,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type retryResponse struct {
	RunID string `json:"run_id"`
}
type batchResponse struct {
	Results []batchItem `json:"results" openapi:"nonnullable"`
}

// idempotencyKey reads the required Idempotency-Key header: 1-255 printable
// ASCII characters without spaces.
func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", newError(http.StatusBadRequest, "invalid_request", "Idempotency-Key must appear once")
	}
	key := r.Header.Get("Idempotency-Key")
	issues := uicontract.ValidateReplayAction(uicontract.ReplayActionInput{IdempotencyKey: key}).Issues
	if len(issues) > 0 {
		code := "invalid_request"
		if key == "" {
			code = "idempotency_key_required"
		}
		return "", newError(http.StatusBadRequest, code, "%s", issues[0].Message)
	}
	return key, nil
}

func (a *api) retry(w http.ResponseWriter, r *http.Request) {
	key, err := idempotencyKey(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var body retryBody
	if err := decodeJSON(w, r, &body, true); err != nil {
		a.fail(w, r, err)
		return
	}
	res, err := a.store.RequestRetry(r.Context(), store.RetryRequest{
		RecordID: r.PathValue("id"), Actor: auth.Actor(r.Context()), IdempotencyKey: key,
		AcknowledgeDuplicateRisk: body.AcknowledgeDuplicateRisk,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if res.Created {
		a.metrics.RunsCreated(metrics.ModeSingle, 1)
	}
	writeJSON(w, http.StatusAccepted, retryResponse{RunID: res.RunID})
}

// retryBatch requests a run for each listed record. Each item gets its own
// idempotency key, "<Idempotency-Key>:<record id>", so repeating the batch
// is safe and one item's outcome never affects another's.
func (a *api) retryBatch(w http.ResponseWriter, r *http.Request) {
	key, err := idempotencyKey(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var body batchBody
	if err := decodeJSON(w, r, &body, false); err != nil {
		a.fail(w, r, err)
		return
	}
	ids := body.RecordIDs
	if ids == nil {
		ids = []string{}
	} // nil means a single action to the shared input validator
	issues := uicontract.ValidateReplayAction(uicontract.ReplayActionInput{IdempotencyKey: key, RecordIDs: ids}).Issues
	if len(issues) > 0 {
		a.fail(w, r, newError(http.StatusBadRequest, "invalid_request", "%s", issues[0].Message))
		return
	}
	items := make([]batchItem, 0, len(body.RecordIDs))
	created := 0
	defer func() { a.metrics.RunsCreated(metrics.ModeBatch, created) }()
	for _, id := range body.RecordIDs {
		res, err := a.store.RequestRetry(r.Context(), store.RetryRequest{
			RecordID: id, Actor: auth.Actor(r.Context()), IdempotencyKey: key + ":" + id,
			AcknowledgeDuplicateRisk: body.AcknowledgeDuplicateRisk,
		})
		if err != nil {
			ae := classify(err)
			if ae == nil {
				a.fail(w, r, err)
				return
			}
			items = append(items, batchItem{RecordID: id, Status: ae.status, Code: ae.code, Detail: ae.detail})
			continue
		}
		if res.Created {
			created++
		}
		items = append(items, batchItem{RecordID: id, Status: http.StatusAccepted, RunID: res.RunID})
	}
	writeJSON(w, http.StatusOK, batchResponse{Results: items})
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runOf(run))
}
