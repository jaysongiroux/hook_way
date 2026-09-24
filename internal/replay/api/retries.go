package api

import (
	"net/http"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

type retryBody struct {
	AcknowledgeDuplicateRisk bool `json:"acknowledge_duplicate_risk"`
}

type batchBody struct {
	FailureIDs               []string `json:"failure_ids"`
	AcknowledgeDuplicateRisk bool     `json:"acknowledge_duplicate_risk"`
}

type batchItem struct {
	FailureID string `json:"failure_id"`
	Status    int    `json:"status"`
	RunID     string `json:"run_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// idempotencyKey reads the required Idempotency-Key header: 1-255 printable
// ASCII characters without spaces.
func idempotencyKey(r *http.Request) (string, error) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return "", newError(http.StatusBadRequest, "idempotency_key_required", "the Idempotency-Key header is required")
	}
	if len(key) > maxKeyLen || strings.IndexFunc(key, func(c rune) bool { return c < 0x21 || c > 0x7e }) >= 0 {
		return "", newError(http.StatusBadRequest, "invalid_request", "Idempotency-Key must be 1-%d printable ASCII characters without spaces", maxKeyLen)
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
		FailureID: r.PathValue("id"), Actor: auth.Actor(r.Context()), IdempotencyKey: key,
		AcknowledgeDuplicateRisk: body.AcknowledgeDuplicateRisk,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if res.Created {
		a.metrics.RunsCreated(metrics.ModeSingle, 1)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": res.RunID})
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
	if n := len(body.FailureIDs); n == 0 || n > maxBatchSize {
		a.fail(w, r, newError(http.StatusBadRequest, "invalid_request", "failure_ids must list 1 to %d record ids", maxBatchSize))
		return
	}
	seen := map[string]bool{}
	for _, id := range body.FailureIDs {
		if seen[id] {
			a.fail(w, r, newError(http.StatusBadRequest, "invalid_request", "failure_ids lists %q more than once", id))
			return
		}
		seen[id] = true
	}
	items := make([]batchItem, 0, len(body.FailureIDs))
	created := 0
	defer func() { a.metrics.RunsCreated(metrics.ModeBatch, created) }()
	for _, id := range body.FailureIDs {
		res, err := a.store.RequestRetry(r.Context(), store.RetryRequest{
			FailureID: id, Actor: auth.Actor(r.Context()), IdempotencyKey: key + ":" + id,
			AcknowledgeDuplicateRisk: body.AcknowledgeDuplicateRisk,
		})
		if err != nil {
			ae := classify(err)
			if ae == nil {
				a.fail(w, r, err)
				return
			}
			items = append(items, batchItem{FailureID: id, Status: ae.status, Code: ae.code, Detail: ae.detail})
			continue
		}
		if res.Created {
			created++
		}
		items = append(items, batchItem{FailureID: id, Status: http.StatusAccepted, RunID: res.RunID})
	}
	writeJSON(w, http.StatusOK, map[string][]batchItem{"results": items})
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runOf(run))
}
