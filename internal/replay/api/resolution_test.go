package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

func TestMeReturnsIdentityScopesAndImplementedCapabilities(t *testing.T) {
	h := newHarness(t, notFoundStore())
	for _, tc := range []struct {
		token, name string
		scopes      int
	}{{opsToken, "ops", 3}, {readToken, "reader", 1}} {
		rec := h.do(t, "GET", "/v1/me", tc.token, nil, nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code)
		}
		var body meResponse
		decode(t, rec, &body)
		if body.KeyName != tc.name || len(body.Scopes) != tc.scopes || body.ContractVersion != uicontract.ContractVersion || !strings.Contains(rec.Body.String(), "unknown_resolution") || strings.Contains(rec.Body.String(), tc.token) {
			t.Fatal(rec.Body.String())
		}
		assertContractBody(t, contractDocument(t), "Identity", rec.Body.Bytes())
	}
}

func TestResolveValidatesEvidenceAndRecordsAuthenticatedActor(t *testing.T) {
	fs := notFoundStore()
	calls := 0
	fs.resolve = func(req store.ResolveRequest) (store.ResolveResult, error) {
		calls++
		if req.Actor != "ops" || req.RecordID != testID || req.IdempotencyKey != "resolve-1" || req.Note != "Confirmed in downstream logs" || req.Reference != "INC-42" {
			t.Fatal(req)
		}
		return store.ResolveResult{Created: true, Resolution: store.Resolution{RecordID: testID, Actor: req.Actor, Note: req.Note, Reference: req.Reference, IdempotencyKey: req.IdempotencyKey, ResolvedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)}}, nil
	}
	h := newHarness(t, fs)
	rec := h.do(t, "POST", "/v1/records/"+testID+"/resolve", opsToken, strings.NewReader(`{"note":"Confirmed in downstream logs","reference":"INC-42"}`), http.Header{"Idempotency-Key": []string{"resolve-1"}})
	if rec.Code != 200 || calls != 1 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var body resolveResponse
	decode(t, rec, &body)
	if body.DeliveryStatus != "resolved" || body.ResolutionSource != "operator_confirmed" {
		t.Fatal(body)
	}
	assertContractBody(t, contractDocument(t), "ResolutionResponse", rec.Body.Bytes())
	for _, input := range []string{`null`, `{}`, `{"note":"  "}`, `{"note":"x","reference":"\n"}`, `{"note":"x","actor":"forged"}`, `{"note":"x"} {}`, `{"note":"\u0000"}`, `{"note":"` + strings.Repeat("a", 4097) + `"}`} {
		rec = h.do(t, "POST", "/v1/records/"+testID+"/resolve", opsToken, strings.NewReader(input), http.Header{"Idempotency-Key": []string{"resolve-1"}})
		if rec.Code != 400 || calls != 1 {
			t.Fatalf("%s: %d %s", input, rec.Code, rec.Body.String())
		}
		assertContractBody(t, contractDocument(t), "Problem", rec.Body.Bytes())
	}
	for _, header := range []http.Header{nil, {"Idempotency-Key": []string{"a", "b"}}, {"Idempotency-Key": []string{"bad key"}}} {
		rec = h.do(t, "POST", "/v1/records/"+testID+"/resolve", opsToken, strings.NewReader(`{"note":"Confirmed"}`), header)
		if rec.Code != 400 || calls != 1 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	rec = h.do(t, "POST", "/v1/records/"+testID+"/resolve", opsToken, strings.NewReader(`{"note":"`+strings.Repeat("a", maxJSONBodyBytes)+`"}`), http.Header{"Idempotency-Key": []string{"large"}})
	if rec.Code != 413 || calls != 1 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestResolveMapsStateAndIdempotencyErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrNotFound, 404, "not_found"},
		{store.ErrNotResolvable, 422, "not_resolvable"},
		{store.ErrResolutionConflict, 409, "resolution_conflict"},
		{store.ErrRunActive, 409, "run_active"},
		{store.ErrExpired, 410, "record_expired"},
		{store.ErrIdempotencyKeyMismatch, 422, "idempotency_key_mismatch"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			fs := notFoundStore()
			fs.resolve = func(store.ResolveRequest) (store.ResolveResult, error) { return store.ResolveResult{}, tc.err }
			rec := newHarness(t, fs).do(t, "POST", "/v1/records/"+testID+"/resolve", opsToken, strings.NewReader(`{"note":"Investigated receipt"}`), http.Header{"Idempotency-Key": []string{"k"}})
			if rec.Code != tc.status || problemCode(t, rec) != tc.code {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
}

func TestActionValidationFallbackMatchesWASMInputs(t *testing.T) {
	h := newHarness(t, notFoundStore())
	for _, body := range []string{`{"action":"resolve","idempotency_key":"k","note":"Confirmed receipt"}`, `{"action":"resolve","idempotency_key":"k","note":"\u0000"}`, `{"idempotency_key":"k","record_ids":["x","x"]}`} {
		rec := h.do(t, "POST", "/v1/actions/validate", readToken, strings.NewReader(body), nil)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var input uicontract.ReplayActionInput
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(uicontract.ValidateReplayAction(input))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(rec.Body.String()) != string(want) {
			t.Fatalf("%s != %s", rec.Body.String(), want)
		}
		assertContractBody(t, contractDocument(t), "ActionValidationResult", rec.Body.Bytes())
	}
}
