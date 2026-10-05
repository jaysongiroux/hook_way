package editor

import (
	"net/http"
	"strconv"

	"github.com/jaysongiroux/hook_way/internal/apicontract"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

// Routes is the API route metadata used by both registration and OpenAPI.
// Static SPA routes are intentionally separate from the HTTP API contract.
func Routes() []apicontract.Route {
	schema := apicontract.Response{Status: 200, Description: "JSON Schema for YAML configuration.", MediaType: "application/schema+json", Schema: apicontract.Schema{"type": "object", "additionalProperties": true}}
	nodes := apicontract.Response{Status: 200, Description: "Map of node type to JSON option schema.", MediaType: "application/json", Schema: apicontract.Schema{"type": "object", "additionalProperties": apicontract.Schema{"type": "object", "additionalProperties": true}}}
	plain := func(status int, description string) apicontract.Response {
		return apicontract.Response{Status: status, Description: description, MediaType: "text/plain", Schema: apicontract.Schema{"type": "string"}}
	}
	validate := func(path, id string) apicontract.Route {
		return apicontract.Route{Method: "POST", Path: path, OperationID: id, Description: "Validates raw YAML without expanding environment variables. Validation issues return 200; body read failures return plain text 400. Maximum body is 5 MiB.", OptionalAuth: true, Request: &apicontract.Request{Required: false, Description: "Raw YAML bytes. Content-Type is not enforced.", MediaType: "application/yaml", Schema: apicontract.Schema{"type": "string"}}, Responses: []apicontract.Response{apicontract.JSONResponse(200, "Validation issues (empty array when valid).", "ValidateResponse", configapi.ValidateResponse{}), plain(400, "Request body too large or unreadable."), plain(401, "Missing or incorrect configured editor bearer key.")}, Extensions: map[string]any{"x-max-body-bytes": maxValidateBytes}}
	}
	get := func(path, id string, response apicontract.Response) apicontract.Route {
		return apicontract.Route{Method: "GET", Path: path, OperationID: id, Description: "Requires the editor bearer key when one is configured.", OptionalAuth: true, Responses: []apicontract.Response{response, plain(401, "Missing or incorrect configured editor bearer key."), plain(500, "Schema generation failed.")}}
	}
	routes := []apicontract.Route{
		get("/schema", "coreSchema", schema), get("/node-schema", "nodeSchemas", nodes), validate("/validate", "validateCore"), get("/replay-schema", "replaySchema", schema), validate("/validate-replay", "validateReplay"),
		{Method: "GET", Path: "/contract", OperationID: "contractInfo", Description: "Version and capabilities shared with the WASM bridge. Requires the editor key when configured.", OptionalAuth: true, Responses: []apicontract.Response{apicontract.JSONResponse(http.StatusOK, "UI contract metadata.", "ContractInfo", uicontract.InfoDTO{}), plain(401, "Missing or incorrect configured editor bearer key.")}},
		{Method: "POST", Path: "/json-sample-validation", OperationID: "jsonSampleValidation", Description: "Validates and explicitly coerces one JSON sample with the runtime's bounded, offline JSON Schema engine (Draft 7 by default). Schema and sample issues return 200 with valid=false. The sample is at most 1 MiB and schema at most 256 KiB; the escaped request envelope is at most 8 MiB. No external references or environment expansion. Output is compact JSON text of at most 2 MiB to preserve exact numbers.", OptionalAuth: true, Request: &apicontract.Request{Required: true, Description: "Strict JSON object containing schema, optional scalar coercions and raw sample JSON text. Content-Type is not enforced.", MediaType: "application/json", Model: &apicontract.Model{Name: "JSONSampleValidationInput", Value: uicontract.JSONSampleValidationInput{}}}, Responses: []apicontract.Response{apicontract.JSONResponse(200, "Structured validation result. Output is present only when valid.", "JSONSampleValidationResult", uicontract.JSONSampleValidationResult{}), plain(400, "Unreadable body or malformed JSON request (including unknown fields, missing schema/sample or multiple values)."), plain(413, "Escaped request body exceeds 8 MiB."), plain(401, "Missing or incorrect configured editor bearer key.")}, Extensions: map[string]any{"x-max-body-bytes": uicontract.MaxJSONSampleRequestBytes, "x-max-sample-bytes": uicontract.MaxJSONSampleBytes, "x-max-schema-bytes": 256 << 10, "x-max-output-bytes": uicontract.MaxJSONSampleOutputBytes}},
	}
	for i := range routes {
		routes[i].Responses = append(routes[i].Responses, plain(http.StatusMethodNotAllowed, "Unsupported method (plain text; includes Allow header)."))
	}
	return routes
}

func OpenAPI() ([]byte, error) {
	return apicontract.Document("Hookway editor API", strconv.Itoa(uicontract.ContractVersion), Routes(), []apicontract.Model{{Name: "ValidateResponse", Value: configapi.ValidateResponse{}, Adjust: func(s apicontract.Schema) {
		s["properties"].(apicontract.Schema)["issues"].(apicontract.Schema)["type"] = "array"
	}}, {Name: "Issue", Value: configapi.Issue{}}, {Name: "ContractInfo", Value: uicontract.InfoDTO{}, Adjust: func(s apicontract.Schema) {
		s["properties"].(apicontract.Schema)["capabilities"].(apicontract.Schema)["type"] = "array"
	}}, {Name: "JSONSampleValidationInput", Value: uicontract.JSONSampleValidationInput{}, Input: true, Required: []string{"schema", "sample"}, Adjust: func(s apicontract.Schema) {
		s["properties"].(apicontract.Schema)["schema"].(apicontract.Schema)["type"] = "object"
	}}, {Name: "ScalarCoercion", Value: transform.ScalarCoercion{}, Input: true, Required: []string{"path", "type"}, Adjust: func(s apicontract.Schema) {
		s["properties"].(apicontract.Schema)["type"].(apicontract.Schema)["enum"] = []string{"string", "number", "integer", "boolean", "null"}
	}}, {Name: "JSONSampleValidationResult", Value: uicontract.JSONSampleValidationResult{}}, {Name: "JSONSampleIssue", Value: uicontract.JSONSampleIssue{}}}, apicontract.BearerSecurity("Optional configured editor API key; authentication is disabled when no key is configured."))
}
func handleContract(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, uicontract.Info())
}
