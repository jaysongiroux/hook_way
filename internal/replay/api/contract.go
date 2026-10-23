package api

import (
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/apicontract"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/problem"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replayresult"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

// Routes supplies both live routing and generated documentation. Metrics has a
// deployment-configured path/handler and is outside the fixed API contract.
func Routes() []apicontract.Route {
	jsonResponse := apicontract.JSONResponse
	pathID := apicontract.Parameter("id", "path", true, apicontract.Schema{"type": "string"}, "Captured record ID; malformed or absent records return 404.")
	pathRun := apicontract.Parameter("run_id", "path", true, apicontract.Schema{"type": "string"}, "Retry run ID.")
	key := apicontract.Parameter("Idempotency-Key", "header", true, apicontract.Schema{"type": "string", "minLength": 1, "maxLength": maxKeyLen, "pattern": "^[!-~]+$"}, "Printable ASCII without spaces. Required even when repeating a previous request.")
	route := func(method, path, id, description string, scope auth.Scope, response apicontract.Response, statuses ...int) apicontract.Route {
		r := apicontract.Route{Method: method, Path: path, OperationID: id, Description: description, Scope: string(scope), Responses: []apicontract.Response{response, {Status: http.StatusMethodNotAllowed, Description: "Unsupported method (ServeMux plain text response; includes Allow header).", MediaType: "text/plain", Schema: apicontract.Schema{"type": "string"}}}}
		if scope != "" {
			statuses = append(statuses, 401, 403, 429)
		}
		seen := map[int]bool{response.Status: true}
		for _, status := range statuses {
			if !seen[status] {
				seen[status] = true
				response := apicontract.Response{Status: status, Description: http.StatusText(status) + "; stable code in problem body.", MediaType: problem.ContentType, Model: &apicontract.Model{Name: "Problem", Value: problem.Details{}, Adjust: describeProblem}}
				if status == http.StatusTooManyRequests {
					response.Description = "Failed authentication attempts exceeded the peer or process allowance; valid credentials remain usable."
					response.Headers = map[string]any{"Retry-After": map[string]any{"schema": apicontract.Schema{"type": "string", "pattern": "^[0-9]+$"}, "description": "Seconds until the next failed-attempt allowance is available."}}
				}
				r.Responses = append(r.Responses, response)
			}
		}
		return r
	}
	capture := route("POST", "/v1/records", "captureRecord", "Captures one failure or inspection as multipart/form-data. Metadata is strict JSON, maximum 1 MiB, without duplicate or unknown parts. Snapshot and payload are exact binary bytes, required exactly when metadata declares them (both for retryable records). Each artifact is bounded by configured retention.max_record_bytes; the entire request by twice that plus 1 MiB metadata and 64 KiB framing. Digests and sizes must match. Repeated identical capture returns 200.", auth.ScopeCapture, jsonResponse(201, "Created captured record.", "CaptureResponse", captureResponse{}), 400, 409, 413, 507, 500)
	capture.Responses = append(capture.Responses, jsonResponse(200, "Duplicate capture acknowledged.", "CaptureResponse", captureResponse{}))
	capture.Request = &apicontract.Request{Required: true, Description: "Exactly one metadata part; artifact parts match declarations. Part order is arbitrary. Unknown metadata fields and trailing JSON are rejected.", MediaType: "multipart/form-data", Schema: apicontract.Schema{"type": "object", "required": []string{replaywire.PartMetadata}, "additionalProperties": false, "properties": apicontract.Schema{replaywire.PartMetadata: apicontract.Schema{"$ref": "#/components/schemas/RecordMetadata"}, replaywire.PartSnapshot: apicontract.Schema{"type": "string", "format": "binary"}, replaywire.PartPayload: apicontract.Schema{"type": "string", "format": "binary"}}}, Encoding: map[string]any{replaywire.PartMetadata: map[string]any{"contentType": "application/json"}, replaywire.PartSnapshot: map[string]any{"contentType": "application/octet-stream"}, replaywire.PartPayload: map[string]any{"contentType": "application/octet-stream"}}}
	list := route("GET", "/v1/records", "listRecords", "Cursor pagination, newest records first by failure or inspection time then id. Omitted kind lists both failures and inspections; filter kind to select one. Unknown or repeated query parameters are rejected. Empty filters are omitted. Text filters accept at most 1024 bytes printable UTF-8.", auth.ScopeRead, jsonResponse(200, "Record summaries and optional next cursor.", "RecordList", RecordList{}), 400, 500)
	for _, name := range []string{"flow", "node_id", "destination_id", "cursor"} {
		list.Parameters = append(list.Parameters, apicontract.Parameter(name, "query", false, apicontract.Schema{"type": "string", "x-max-bytes": maxFilterLen}, "Printable UTF-8; cursor is opaque; reuse with the same filters for consistent pagination."))
	}
	for _, p := range []struct {
		name        string
		schema      apicontract.Schema
		description string
	}{
		{"delivery_status", apicontract.Schema{"type": "string", "enum": store.DeliveryStatuses}, "Failure delivery state; absent for inspections."},
		{"replay_mode", apicontract.Schema{"type": "string", "enum": []string{replaywire.ReplayResume, replaywire.ReplayPublishOnly}}, "Failure replay execution mode."},
		{"dismissed", apicontract.Schema{"type": "boolean"}, "Filter by dismissal; omitted includes both states."},
		{"kind", apicontract.Schema{"type": "string", "enum": []string{replaywire.KindFailure, replaywire.KindInspection}}, "Record purpose. Omitted kind includes both purposes."},
		{"retryable", apicontract.Schema{"type": "boolean"}, "Literal true or false."},
		{"since", apicontract.Schema{"type": "string", "format": "date-time"}, "Inclusive failure or inspection time lower bound (RFC 3339)."},
		{"until", apicontract.Schema{"type": "string", "format": "date-time"}, "Exclusive failure or inspection time upper bound (RFC 3339)."},
		{"limit", apicontract.Schema{"type": "integer", "minimum": 1, "maximum": store.MaxListLimit, "default": store.DefaultListLimit}, "Maximum returned records."},
	} {
		list.Parameters = append(list.Parameters, apicontract.Parameter(p.name, "query", false, p.schema, p.description))
	}
	get := route("GET", "/v1/records/{id}", "getRecord", "Record metadata, current action eligibility, artifact descriptions, run history and child IDs. Eligibility is independent of scopes and rechecked at action time. Artifact bytes are not included.", auth.ScopeRead, jsonResponse(200, "Record detail.", "RecordDetail", RecordDetail{}), 404, 500)
	get.Parameters = []any{pathID}
	payload := route("GET", "/v1/records/{id}/payload", "downloadPayload", "Downloads exact captured bytes after integrity verification. Missing capture returns 404; expired or dismissed artifact returns 410.", auth.ScopePayload, apicontract.Response{Status: 200, Description: "Exact payload bytes. Content type is always octet-stream; original content type is in X-Hookway-Payload-Content-Type when available.", MediaType: "application/octet-stream", Schema: apicontract.Schema{"type": "string", "format": "binary"}, Headers: map[string]any{"Content-Disposition": map[string]any{"schema": apicontract.Schema{"type": "string"}, "description": "Attachment with sanitized encoded filename."}, "X-Hookway-Payload-Content-Type": map[string]any{"schema": apicontract.Schema{"type": "string"}, "description": "Optional originally captured content type."}, "Content-Length": map[string]any{"schema": apicontract.Schema{"type": "integer", "minimum": 0}}}}, 404, 410, 500)
	payload.Parameters = []any{pathID}
	retry := route("POST", "/v1/records/{id}/retries", "retryRecord", "Queues a retry for an unexpired record. Both a newly created run and an idempotent repeated request return 202. Unknown JSON fields or trailing data are rejected; an empty body is allowed. Unknown prior delivery outcomes require duplicate-risk acknowledgement.", auth.ScopeRetry, jsonResponse(202, "Retry run accepted.", "RetryResponse", retryResponse{}), 400, 404, 409, 410, 413, 422, 500)
	retry.Parameters = []any{pathID, key}
	retry.Request = &apicontract.Request{Required: false, Description: "Optional strict JSON object or null, maximum 64 KiB. Empty body or null uses the default false acknowledgement.", MediaType: "application/json", Schema: apicontract.Schema{"anyOf": []any{apicontract.Schema{"$ref": "#/components/schemas/RetryRequest"}, apicontract.Schema{"type": "null"}}}}
	retry.Extensions = map[string]any{"x-max-body-bytes": maxJSONBodyBytes}
	dismiss := route("POST", "/v1/records/{id}/dismiss", "dismissRecord", "Dismisses an eligible record and removes stored artifacts. Running or invalid states reject dismissal. Request body is ignored.", auth.ScopeRetry, jsonResponse(200, "Updated record summary.", "RecordSummary", RecordSummary{}), 404, 409, 422, 500)
	dismiss.Parameters = []any{pathID}
	batch := route("POST", "/v1/retries", "retryRecords", "Requests one run per unique failure ID, using <Idempotency-Key>:<record_id> for each item. HTTP 200 includes per-item 202 successes and classified errors; unexpected failures return 500 and earlier items may already have been accepted.", auth.ScopeRetry, jsonResponse(200, "Per-item retry outcomes, including mixed successes/errors.", "BatchRetryResponse", batchResponse{}), 400, 413, 500)
	batch.Parameters = []any{key}
	batch.Request = &apicontract.Request{Required: true, Description: "Strict JSON, maximum 64 KiB; record_ids contains 1-100 unique IDs. Missing acknowledgement defaults to false.", MediaType: "application/json", Model: &apicontract.Model{Name: "BatchRetryRequest", Value: batchBody{}}}
	batch.Extensions = map[string]any{"x-max-body-bytes": maxJSONBodyBytes, "x-max-batch-size": maxBatchSize}
	run := route("GET", "/v1/retries/{run_id}", "getRetry", "Run timestamps are nullable. Queued result is null. Newly written objects use RunResult; historical raw JSON and unknown members are preserved verbatim.", auth.ScopeRead, jsonResponse(200, "Retry run.", "Run", runView{}), 404, 500)
	run.Parameters = []any{pathRun}
	health := route("GET", "/healthz", "health", "Unauthenticated liveness.", "", apicontract.Response{Status: 200, Description: "ok followed by newline.", MediaType: "text/plain", Schema: apicontract.Schema{"type": "string"}})
	ready := route("GET", "/readyz", "ready", "Unauthenticated database reachability and schema readiness.", "", apicontract.Response{Status: 200, Description: "ready followed by newline.", MediaType: "text/plain", Schema: apicontract.Schema{"type": "string"}}, 503)
	me := route("GET", "/v1/me", "whoAmI", "Returns the authenticated key name, scopes, contract version and implemented replay API capabilities. Never returns credentials.", auth.ScopeRead, jsonResponse(200, "Authenticated operator capabilities.", "Identity", meResponse{}))
	resolve := route("POST", "/v1/records/{id}/resolve", "resolveRecord", "Records independent operator confirmation of an unknown outcome; it sends nothing. Requires an investigation note and no active retry. Preserves original runs, results, artifacts and retention expiry. An identical actor/key/body repeat returns the original resolution; changed body/key conflicts reject. Idempotency keys are scoped separately from retry keys, until record retention deletes the resolution.", auth.ScopeRetry, jsonResponse(200, "Resolved by operator confirmation; identical repeats return this same receipt.", "ResolutionResponse", resolveResponse{}), 400, 404, 409, 410, 413, 422, 500)
	resolve.Parameters = []any{pathID, key}
	resolve.Request = &apicontract.Request{Required: true, Description: "Strict evidence object, at most 64 KiB. Notes are required; reference is an optional opaque receipt or investigation identifier, never fetched or executed.", MediaType: "application/json", Model: &apicontract.Model{Name: "ResolutionRequest", Value: uicontract.ResolutionInput{}, Input: true, Required: []string{"note"}, Adjust: describeResolutionInput}}
	resolve.Extensions = map[string]any{"x-max-body-bytes": maxJSONBodyBytes}
	validate := route("POST", "/v1/actions/validate", "validateAction", "Pure retry/resolution input validation, also provided by WASM. Does not access records or perform an action. HTTP 200 with issues means inputs are invalid; scopes, eligibility and evidence authenticity remain authoritative at action time.", auth.ScopeRead, jsonResponse(200, "Input issues; empty issues means syntactically valid only.", "ActionValidationResult", uicontract.ReplayActionResult{}), 400, 413, 500)
	validate.Request = &apicontract.Request{Required: true, MediaType: "application/json", Model: &apicontract.Model{Name: "ActionValidationInput", Value: uicontract.ReplayActionInput{}, Input: true, Required: []string{"idempotency_key"}}}
	validate.Extensions = map[string]any{"x-max-body-bytes": maxJSONBodyBytes}
	return append([]apicontract.Route{me, resolve, validate, capture, list, get, payload, retry, dismiss, batch, run, health, ready}, eventRoutes()...)
}

func OpenAPI() ([]byte, error) {
	models := []apicontract.Model{
		{Name: "MCPEventEligibility", Value: eventops.Eligibility{}}, {Name: "MCPEventAttempt", Value: eventops.Attempt{}}, {Name: "MCPEventReceipt", Value: eventops.Receipt{}}, {Name: "MCPSubscription", Value: eventops.Subscription{}}, {Name: "MCPDelivery", Value: eventops.Delivery{}},
		{Name: "ActionValidationInput", Value: uicontract.ReplayActionInput{}, Input: true, Required: []string{"idempotency_key"}},
		{Name: "ActionValidationResult", Value: uicontract.ReplayActionResult{}},
		{Name: "ActionIssue", Value: uicontract.ActionIssue{}},
		{Name: "Identity", Value: meResponse{}}, {Name: "ResolutionResponse", Value: resolveResponse{}}, {Name: "Resolution", Value: store.Resolution{}}, {Name: "ResolutionRequest", Value: uicontract.ResolutionInput{}, Input: true, Required: []string{"note"}, Adjust: describeResolutionInput},
		{Name: "RecordSummary", Value: RecordSummary{}, Adjust: describeRecord}, {Name: "RecordDetail", Value: RecordDetail{}, Adjust: describeRecord}, {Name: "RecordList", Value: RecordList{}}, {Name: "Run", Value: runView{}, Adjust: describeRun},
		{Name: "RetryRequest", Value: retryBody{}, Input: true, Required: []string{}, Adjust: describeRetry}, {Name: "BatchRetryRequest", Value: batchBody{}, Input: true, Required: []string{"record_ids"}, Adjust: describeBatch},
		{Name: "RetryResponse", Value: retryResponse{}}, {Name: "BatchRetryResponse", Value: batchResponse{}}, {Name: "BatchRetryItem", Value: batchItem{}}, {Name: "CaptureResponse", Value: captureResponse{}},
		{Name: "Problem", Value: problem.Details{}, Adjust: describeProblem}, {Name: "RecordMetadata", Value: replaywire.RecordMetadata{}, Input: true, Required: []string{"id", "kind", "flow", "node_id", "node_type", "event_id", "received_at", "occurred_at", "inbound_status", "retryable"}, Adjust: describeMetadata}, {Name: "ArtifactInfo", Value: replaywire.ArtifactInfo{}, Input: true, Required: []string{"sha256", "size"}}, {Name: "RunResult", Value: replayresult.Result{}}, {Name: "InspectionPayload", Value: replaywire.InspectionPayload{}},
	}
	return apicontract.Document("Hookway replay API", "1", Routes(), models, apicontract.BearerSecurity("Named scoped replay bearer token. Scopes: "+scopeDescription()+". Required scope is declared on each operation."))
}
func scopeDescription() string {
	out := ""
	for i, s := range auth.Scopes() {
		if i > 0 {
			out += ", "
		}
		out += string(s)
	}
	return out
}

func properties(s apicontract.Schema) apicontract.Schema { return s["properties"].(apicontract.Schema) }
func describeRun(s apicontract.Schema) {
	p := properties(s)
	p["result"] = apicontract.Schema{"description": "Raw persisted JSON, including null for queued runs. Newly written result objects follow RunResult. Historical unknown keys and JSON are passed through unchanged.", "x-known-schema": map[string]any{"$ref": "#/components/schemas/RunResult"}}
	p["status"].(apicontract.Schema)["enum"] = []string{store.RunQueued, store.RunRunning, store.RunSucceeded, store.RunFailed, store.RunBlocked, store.RunUnknown}
}
func describeRetry(s apicontract.Schema) {
	properties(s)["acknowledge_duplicate_risk"] = apicontract.Schema{"type": []string{"boolean", "null"}, "default": false}
}
func describeBatch(s apicontract.Schema) {
	describeRetry(s)
	p := properties(s)
	p["record_ids"] = apicontract.Schema{"type": "array", "items": apicontract.Schema{"type": []string{"string", "null"}}, "minItems": 1, "maxItems": maxBatchSize, "uniqueItems": true}
}
func describeProblem(s apicontract.Schema) {
	codes := []string{"unauthorized", "forbidden", "auth_throttled", "origin_forbidden", "cors_preflight_forbidden", "internal", "not_ready", "invalid_request", "invalid_metadata", "artifact_mismatch", "request_too_large", "record_too_large", "invalid_parameter", "idempotency_key_required"}
	statuses := map[string]int{"unauthorized": 401, "forbidden": 403, "auth_throttled": 429, "origin_forbidden": 403, "cors_preflight_forbidden": 403, "internal": 500, "not_ready": 503, "invalid_request": 400, "invalid_metadata": 400, "artifact_mismatch": 400, "request_too_large": 413, "record_too_large": 413, "invalid_parameter": 400, "idempotency_key_required": 400}
	for _, e := range storeErrors {
		codes = append(codes, e.code)
		statuses[e.code] = e.status
	}
	properties(s)["code"].(apicontract.Schema)["enum"] = codes
	s["x-code-statuses"] = statuses
}
func describeMetadata(s apicontract.Schema) {
	p := properties(s)
	p["kind"].(apicontract.Schema)["enum"] = []string{replaywire.KindFailure, replaywire.KindInspection}
	p["id"].(apicontract.Schema)["pattern"] = "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
	p["inbound_status"].(apicontract.Schema)["minimum"] = 100
	p["inbound_status"].(apicontract.Schema)["maximum"] = 599
	p["attempts"].(apicontract.Schema)["minimum"] = 0
	p["attempts"].(apicontract.Schema)["maximum"] = 2147483647
	p["replay_mode"].(apicontract.Schema)["enum"] = []string{replaywire.ReplayResume, replaywire.ReplayPublishOnly}
	p["payload_unavailable_reason"].(apicontract.Schema)["enum"] = []string{replaywire.NotRetryableTooLarge, replaywire.NotRetryableUnsupported}
	var forbidden []any
	for _, field := range replaywire.InspectionForbiddenMetadataFields() {
		forbidden = append(forbidden, apicontract.Schema{"required": []string{field}})
	}
	s["allOf"] = []any{apicontract.Schema{
		"if":   apicontract.Schema{"properties": apicontract.Schema{"kind": apicontract.Schema{"const": replaywire.KindInspection}}, "required": []string{"kind"}},
		"then": apicontract.Schema{"required": []string{"not_retryable_reason"}, "properties": apicontract.Schema{"retryable": apicontract.Schema{"const": false}, "node_type": apicontract.Schema{"const": "inspect"}, "not_retryable_reason": apicontract.Schema{"const": "inspection_only"}}, "not": apicontract.Schema{"anyOf": forbidden}},
		"else": apicontract.Schema{"required": []string{"replay_mode", "error_class"}, "properties": apicontract.Schema{"inbound_status": apicontract.Schema{"minimum": 200, "maximum": 299}}},
	}}

	s["description"] = "Strict capture metadata. kind is failure or inspection; replay_mode is required only for failures. occurred_at is the capture occurrence time. Retryable failures require snapshot and payload; non-retryable failures retain metadata only and use too_large or unsupported. Inspections are view-only, have no failure/destination fields or snapshot, and always use inspection_only as their retry prohibition. Missing payloads require payload_unavailable_reason too_large or unsupported independently of retry policy. Inspection payload format remains version 1. Text bounds, SHA256 validation, and exact artifact sizes are enforced at ingest."

}

func describeRecord(s apicontract.Schema) {
	p := properties(s)
	p["resolution_source"].(apicontract.Schema)["enum"] = []string{store.ResolutionOperator, store.ResolutionRetry}
	p["kind"].(apicontract.Schema)["enum"] = []string{replaywire.KindFailure, replaywire.KindInspection}
	p["replay_mode"].(apicontract.Schema)["enum"] = []string{replaywire.ReplayResume, replaywire.ReplayPublishOnly}
	p["delivery_status"].(apicontract.Schema)["enum"] = store.DeliveryStatuses
	p["payload_unavailable_reason"].(apicontract.Schema)["enum"] = []string{replaywire.NotRetryableTooLarge, replaywire.NotRetryableUnsupported}
}
func describeResolutionInput(s apicontract.Schema) {
	p := properties(s)
	p["note"] = apicontract.Schema{"type": "string", "minLength": 1, "maxLength": uicontract.MaxResolutionNoteBytes, "x-max-utf8-bytes": uicontract.MaxResolutionNoteBytes, "description": "Required nonblank evidence, valid UTF-8, at most 4096 bytes. Line breaks and tabs are allowed; other control characters are rejected. Text is preserved exactly."}
	p["reference"] = apicontract.Schema{"type": []string{"string", "null"}, "maxLength": uicontract.MaxResolutionReferenceBytes, "x-max-utf8-bytes": uicontract.MaxResolutionReferenceBytes, "description": "Optional printable UTF-8 receipt/reference, at most 1024 bytes. Empty or null means omitted; whitespace-only and controls are rejected. This is opaque text, not a URL Hookway opens."}
}
