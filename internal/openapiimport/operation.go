package openapiimport

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

type operationBuild struct {
	input, output map[string]any
	request       flowconfig.MCPRequestMapping
	http          flowconfig.HTTPRequestOptions
	path          string
	empty         []int
	headerNames   []string
	aliases       map[string]string
	diagnostics   []Diagnostic
}

func buildOperation(root, item, op map[string]any, sel Selection) operationBuild {
	id := sel.Method + " " + sel.Path
	location := "#/paths/" + ptrPart(sel.Path) + "/" + strings.ToLower(sel.Method)
	b := operationBuild{path: sel.Path, aliases: map[string]string{}, diagnostics: []Diagnostic{}, request: flowconfig.MCPRequestMapping{Variables: map[string]string{}, Query: map[string]string{}, Headers: map[string]string{}, OptionalQuery: map[string]string{}, OptionalHeaders: map[string]string{}}, http: flowconfig.HTTPRequestOptions{HeaderMode: "replace", Headers: map[string]string{"Accept": "application/json"}, Body: "none", MaxResponseBytes: flowconfig.MCPMaxBytes}}
	fail := func(loc, msg string) {
		d := diagnostic(location+loc, msg)
		d.Operation = id
		b.diagnostics = append(b.diagnostics, d)
	}
	if !strings.HasPrefix(sel.Path, "/") || strings.ContainsAny(sel.Path, "?#$\\\r\n") {
		fail("", "unsupported operation path")
	}
	switch sel.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		fail("", "unsupported HTTP method")
	}
	if len(object(op["callbacks"])) > 0 {
		fail("/callbacks", "callbacks are not imported")
	}
	inputConv, outputConv := converter(root, "request"), converter(root, "response")
	properties := map[string]any{}
	required := []any{}
	b.input = map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	parameters := map[string]map[string]any{}
	for _, owner := range []map[string]any{item, op} {
		seen := map[string]bool{}
		list, _ := owner["parameters"].([]any)
		for _, raw := range list {
			p, err := resolve(root, raw)
			if err != nil {
				fail("/parameters", "invalid parameter reference")
				continue
			}
			key := text(p["in"]) + "\x00" + text(p["name"])
			if seen[key] {
				fail("/parameters", "duplicate parameter")
				continue
			}
			seen[key] = true
			parameters[key] = p
		}
	}
	groups := map[string]map[string]any{}
	groupRequired := map[string][]any{}
	pathParams := map[string]bool{}
	for _, key := range sortedParameterKeys(parameters) {
		p := parameters[key]
		name, in := text(p["name"]), text(p["in"])
		loc := "/parameters/" + ptrPart(in) + "/" + ptrPart(name)
		group := in
		if group == "header" {
			group = "headers"
		}
		if name == "" || (in != "path" && in != "query" && in != "header") {
			fail(loc, "only path, query and header parameters are supported")
			continue
		}
		if in == "header" && reservedHeader(name) {
			fail(loc, "authentication and reserved headers cannot be arguments")
			continue
		}
		style := text(p["style"])
		if style != "" && ((in == "query" && style != "form") || (in != "query" && style != "simple")) {
			fail(loc, "unsupported parameter serialization")
			continue
		}
		if len(object(p["content"])) > 0 || p["allowReserved"] == true || p["allowEmptyValue"] == true {
			fail(loc, "unsupported parameter encoding")
			continue
		}
		schema, err := resolve(root, p["schema"])
		if err != nil {
			fail(loc, "parameter requires a scalar schema")
			continue
		}
		typ := text(schema["type"])
		if typ != "string" && typ != "number" && typ != "integer" && typ != "boolean" {
			fail(loc, "only non-null scalar parameters are supported")
			continue
		}
		if schema["nullable"] == true {
			fail(loc, "nullable parameters have no supported wire representation")
			continue
		}
		complex := false
		for _, k := range []string{"allOf", "anyOf", "oneOf"} {
			if schema[k] != nil {
				complex = true
			}
		}
		if complex {
			fail(loc, "composed parameter schemas are not supported")
			continue
		}
		converted, err := inputConv.convert(p["schema"], 0)
		if err != nil {
			fail(loc, err.Error())
			continue
		}
		alias := normalize(name)
		if alias != name {
			b.aliases[group+"."+name] = group + "." + alias
		}
		if groups[group] == nil {
			groups[group] = map[string]any{}
		}
		if _, ok := groups[group][alias]; ok {
			fail(loc, "parameter aliases collide")
			continue
		}
		groups[group][alias] = converted
		isRequired, _ := p["required"].(bool)
		if in == "path" && !isRequired {
			fail(loc, "path parameters must be required")
		}
		if isRequired {
			groupRequired[group] = append(groupRequired[group], alias)
		}
		selector := group + "." + alias
		switch in {
		case "path":
			variable := fmt.Sprintf("path_%d", len(pathParams)+1)
			pathParams[name] = true
			if !strings.Contains(sel.Path, "{"+name+"}") {
				fail(loc, "path parameter is not present in the URL")
			}
			b.path = strings.ReplaceAll(b.path, "{"+name+"}", "{"+variable+"}")
			b.request.Variables[variable] = selector
		case "query":
			if isRequired {
				b.request.Query[name] = selector
			} else {
				b.request.OptionalQuery[name] = selector
			}
		case "header":
			for _, previous := range b.headerNames {
				if strings.EqualFold(name, previous) {
					fail(loc, "header parameters collide ignoring case")
				}
			}
			b.headerNames = append(b.headerNames, name)
			if isRequired {
				b.request.Headers[name] = selector
			} else {
				b.request.OptionalHeaders[name] = selector
			}
		}
	}
	placeholders := regexp.MustCompile(`\{([^{}]+)\}`)
	for _, match := range placeholders.FindAllStringSubmatch(sel.Path, -1) {
		if !pathParams[match[1]] {
			fail("/parameters", "URL placeholder has no required path parameter")
		}
	}
	if strings.ContainsAny(placeholders.ReplaceAllString(sel.Path, ""), "{}") {
		fail("", "malformed path placeholder")
	}
	for _, group := range []string{"path", "query", "headers"} {
		if len(groups[group]) > 0 {
			g := map[string]any{"type": "object", "properties": groups[group], "additionalProperties": false}
			if len(groupRequired[group]) > 0 {
				g["required"] = groupRequired[group]
				required = append(required, group)
			}
			properties[group] = g
		}
	}
	if raw, exists := op["requestBody"]; exists {
		body, err := resolve(root, raw)
		if err != nil {
			fail("/requestBody", "invalid request body")
		} else {
			schema, err := jsonContentSchema(body)
			if err != nil {
				fail("/requestBody", err.Error())
			} else {
				converted, e := inputConv.convert(schema, 0)
				if e != nil {
					fail("/requestBody", e.Error())
				} else {
					properties["body"] = converted
					b.request.BodySource = "body"
					b.request.BodyOptional = body["required"] != true
					b.http.Body = "raw"
					b.http.Headers["Content-Type"] = "application/json"
					b.http.BodyMethods = []string{sel.Method}
					if !b.request.BodyOptional {
						required = append(required, "body")
					}
				}
			}
		}
	}
	if len(required) > 0 {
		b.input["required"] = required
	}
	inputConv.finish(b.input)
	alternatives := []any{}
	responses := object(op["responses"])
	for _, status := range keys(responses) {
		code, err := strconv.Atoi(status)
		if err != nil {
			if strings.HasPrefix(strings.ToUpper(status), "2") {
				fail("/responses/"+status, "success status ranges are not supported")
			}
			continue
		}
		if code < 200 || code > 299 {
			continue
		}
		response, err := resolve(root, responses[status])
		if err != nil {
			fail("/responses/"+status, "invalid response reference")
			continue
		}
		if len(object(response["content"])) == 0 {
			b.empty = append(b.empty, code)
			alternatives = append(alternatives, map[string]any{"type": "null"})
			continue
		}
		schema, err := jsonContentSchema(response)
		if err != nil {
			fail("/responses/"+status, err.Error())
			continue
		}
		converted, err := outputConv.convert(schema, 0)
		if err != nil {
			fail("/responses/"+status, err.Error())
			continue
		}
		alternatives = append(alternatives, converted)
	}
	if len(alternatives) == 0 {
		fail("/responses", "a declared JSON or empty success response is required")
	}
	var data any = map[string]any{"anyOf": alternatives}
	if len(alternatives) == 1 {
		data = alternatives[0]
	}
	b.output = outputConv.finish(map[string]any{"type": "object", "required": []any{"data"}, "additionalProperties": false, "properties": map[string]any{"data": data}})
	for _, schema := range []map[string]any{b.input, b.output} {
		if _, err := flowconfig.CompileMCPSchema(schema); err != nil {
			fail("", "converted schema exceeds Hookway limits or is invalid")
		}
	}
	// Security schemes in query/cookies cannot be implemented by header credentials.
	if security, ok := op["security"]; ok {
		if !supportedSecurity(root, security) {
			fail("/security", "unsupported upstream security requirement")
		}
	} else if !supportedSecurity(root, root["security"]) {
		fail("/security", "unsupported upstream security requirement")
	}
	b.http.ForwardQuery = len(b.request.Query)+len(b.request.OptionalQuery) > 0
	b.http.ForwardHeaders = b.headerNames
	return b
}
func sortedParameterKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func jsonContentSchema(owner map[string]any) (any, error) {
	content := object(owner["content"])
	if len(content) != 1 || object(content["application/json"]) == nil {
		return nil, errors.New("exactly one application/json media type is supported")
	}
	schema, ok := object(content["application/json"])["schema"]
	if !ok {
		return nil, errors.New("JSON content requires a schema")
	}
	return schema, nil
}
func reservedHeader(name string) bool {
	n := strings.ToLower(name)
	return n == "authorization" || n == "cookie" || n == "set-cookie" || n == "host" || n == "content-length" || n == "content-type" || n == "accept" || n == "connection" || n == "transfer-encoding" || n == "upgrade" || n == "te" || n == "trailer" || n == "keep-alive" || n == "proxy-authorization" || n == "proxy-authenticate" || strings.HasPrefix(n, "mcp-") || strings.HasPrefix(n, "x-hookway-") || (strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "api-key") || strings.Contains(n, "apikey"))
}
func supportedSecurity(root map[string]any, security any) bool {
	if !requiresAuth(security) {
		return true
	}
	list, _ := security.([]any)
	for _, entry := range list {
		valid := true
		for name := range object(entry) {
			scheme, err := resolve(root, object(object(root["components"])["securitySchemes"])[name])
			if err != nil {
				valid = false
				break
			}
			switch text(scheme["type"]) {
			case "apiKey":
				valid = valid && text(scheme["in"]) == "header"
			case "http":
				valid = valid && (text(scheme["scheme"]) == "bearer" || text(scheme["scheme"]) == "basic")
			case "oauth2", "openIdConnect":
			default:
				valid = false
			}
		}
		if valid {
			return true
		}
	}
	return false
}
