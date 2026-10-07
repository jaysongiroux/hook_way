package api

import (
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
	"net/http"
)

type meResponse struct {
	KeyName         string       `json:"key_name"`
	Scopes          []auth.Scope `json:"scopes" openapi:"nonnullable"`
	ContractVersion int          `json:"contract_version"`
	Capabilities    []string     `json:"capabilities" openapi:"nonnullable"`
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	identity := auth.Principal(r.Context())
	writeJSON(w, http.StatusOK, meResponse{KeyName: identity.Name, Scopes: identity.Scopes, ContractVersion: uicontract.ContractVersion, Capabilities: []string{"record_api", "inspection_records", "failure_inspection", "payload_download", "destination_retry", "batch_retry", "unknown_resolution", "action_input_validation", "action_eligibility"}})
}

type resolveResponse struct {
	DeliveryStatus   string           `json:"delivery_status"`
	ResolutionSource string           `json:"resolution_source"`
	Resolution       store.Resolution `json:"resolution"`
}

func (a *api) resolve(w http.ResponseWriter, r *http.Request) {
	key, err := idempotencyKey(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var body uicontract.ResolutionInput
	if err := decodeJSON(w, r, &body, false); err != nil {
		a.fail(w, r, err)
		return
	}
	if issues := uicontract.ValidateResolutionInput(body).Issues; len(issues) > 0 {
		a.fail(w, r, newError(http.StatusBadRequest, "invalid_resolution", "%s", issues[0].Message))
		return
	}
	result, err := a.store.ResolveUnknown(r.Context(), store.ResolveRequest{RecordID: r.PathValue("id"), Actor: auth.Actor(r.Context()), IdempotencyKey: key, ResolutionInput: body})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resolveResponse{DeliveryStatus: store.StatusResolved, ResolutionSource: store.ResolutionOperator, Resolution: result.Resolution})
}

// validateAction is a read-scoped, pure validation fallback when WASM cannot
// run. It cannot authenticate evidence or decide whether an action is eligible.
func (a *api) validateAction(w http.ResponseWriter, r *http.Request) {
	var input uicontract.ReplayActionInput
	if err := decodeJSON(w, r, &input, false); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, uicontract.ValidateReplayAction(input))
}
