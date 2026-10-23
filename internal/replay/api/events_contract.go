package api

import (
	"github.com/jaysongiroux/hook_way/internal/apicontract"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/problem"
	"net/http"
	"strings"
)

func eventRoutes() []apicontract.Route {
	out := []apicontract.Route{}
	for _, spec := range eventops.Routes() {
		status := 200
		if spec.Operation == "retry" {
			status = 202
		}
		response := apicontract.JSONResponse(status, "Event operation result.", spec.Name, spec.Model)
		if spec.Operation == "payload" {
			response = apicontract.Response{Status: 200, Description: "Exact retained occurrence JSON; requires events_payload.", MediaType: "application/json", Schema: apicontract.Schema{"type": "object", "additionalProperties": true}}
		}
		r := apicontract.Route{Method: spec.Method, Path: eventops.Prefix + spec.Path, OperationID: "mcpEvent_" + spec.Operation, Description: "Namespace-scoped MCP event operation. Payloads and callback secrets are excluded from metadata.", Scope: spec.Scope, Responses: []apicontract.Response{response}}
		for _, code := range []int{400, 401, 403, 404, 409, 410, 413, 422, 429, 503} {
			r.Responses = append(r.Responses, apicontract.Response{Status: code, Description: http.StatusText(code), MediaType: problem.ContentType, Model: &apicontract.Model{Name: "Problem", Value: problem.Details{}, Adjust: describeProblem}})
		}
		if strings.Contains(spec.Path, "{id}") {
			r.Parameters = append(r.Parameters, apicontract.Parameter("id", "path", true, apicontract.Schema{"type": "string"}, "Opaque subscription ID or decimal delivery ID."))
		}
		if spec.Operation == "subscriptions" || spec.Operation == "deliveries" {
			for _, name := range []string{"endpoint", "event", "state", "owner", "subscription", "since", "until", "cursor"} {
				r.Parameters = append(r.Parameters, apicontract.Parameter(name, "query", false, apicontract.Schema{"type": "string"}, "Optional exact filter. Times use RFC3339; subscription times filter expiry, delivery times filter acceptance."))
			}
			r.Parameters = append(r.Parameters, apicontract.Parameter("limit", "query", false, apicontract.Schema{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}, "Page size."))
		}
		if spec.Method == "POST" {
			r.Parameters = append(r.Parameters, apicontract.Parameter("Idempotency-Key", "header", true, apicontract.Schema{"type": "string", "minLength": 1, "maxLength": 255}, "Unique per operator; reuse after an interrupted request."))
			r.Request = &apicontract.Request{Required: true, MediaType: "application/json", Model: &apicontract.Model{Name: "MCPEventActionInput", Value: eventops.ActionInput{}, Input: true, Required: []string{"version"}}}
		}
		out = append(out, r)
	}
	return out
}
