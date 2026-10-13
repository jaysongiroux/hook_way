// Package apicontract is a small, dependency-free OpenAPI 3.1 renderer for
// Hookway's explicit HTTP route metadata and Go JSON DTOs. It is not a router.
package apicontract

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Schema = map[string]any

type Model struct {
	Name     string
	Value    any
	Input    bool
	Required []string
	Adjust   func(Schema)
}
type Response struct {
	Status                 int
	Description, MediaType string
	Model                  *Model
	Schema                 Schema
	Headers                map[string]any
}
type Request struct {
	Required               bool
	Description, MediaType string
	Model                  *Model
	Schema                 Schema
	Encoding               map[string]any
}
type Route struct {
	Method, Path, OperationID, Description, Scope string
	OptionalAuth                                  bool
	Request                                       *Request
	Responses                                     []Response
	Parameters                                    []any
	Extensions                                    map[string]any
}

func (r Route) Pattern() string { return r.Method + " " + r.Path }

// Document derives every DTO property and optional member from its JSON tags.
// Explicit schemas describe transport bytes, constraints and raw historical JSON.
func Document(title, version string, routes []Route, models []Model, security Schema) ([]byte, error) {
	b := &builder{schemas: Schema{}, names: map[reflect.Type]string{}, inputs: map[reflect.Type]Model{}}
	for _, m := range models {
		b.names[reflect.TypeOf(m.Value)] = m.Name
		if m.Input {
			b.inputs[reflect.TypeOf(m.Value)] = m
		}
	}
	for _, m := range models {
		b.schema(reflect.TypeOf(m.Value))
	}
	paths := Schema{}
	for _, r := range routes {
		op := Schema{"operationId": r.OperationID, "description": r.Description, "responses": Schema{}}
		if r.Scope != "" || r.OptionalAuth {
			op["security"] = []any{Schema{"bearerAuth": []string{}}}
			if r.OptionalAuth {
				op["security"] = []any{Schema{}, Schema{"bearerAuth": []string{}}}
			}
			if r.Scope != "" {
				op["x-required-scope"] = r.Scope
			}
		}
		if len(r.Parameters) > 0 {
			op["parameters"] = r.Parameters
		}
		if q := r.Request; q != nil {
			media := Schema{"schema": b.content(q.Model, q.Schema)}
			if q.Encoding != nil {
				media["encoding"] = q.Encoding
			}
			op["requestBody"] = Schema{"required": q.Required, "description": q.Description, "content": Schema{q.MediaType: media}}
		}
		for _, response := range r.Responses {
			out := Schema{"description": response.Description}
			if response.MediaType != "" {
				out["content"] = Schema{response.MediaType: Schema{"schema": b.content(response.Model, response.Schema)}}
			}
			if len(response.Headers) > 0 {
				out["headers"] = response.Headers
			}
			op["responses"].(Schema)[statusString(response.Status)] = out
		}
		for k, v := range r.Extensions {
			op[k] = v
		}
		item, _ := paths[r.Path].(Schema)
		if item == nil {
			item = Schema{}
			paths[r.Path] = item
		}
		item[strings.ToLower(r.Method)] = op
	}
	for _, m := range models {
		if m.Adjust != nil {
			m.Adjust(b.schemas[m.Name].(Schema))
		}
	}
	doc := Schema{"openapi": "3.1.0", "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema", "info": Schema{"title": title, "version": version}, "paths": paths, "components": Schema{"schemas": b.schemas, "securitySchemes": security}}
	data, err := json.MarshalIndent(doc, "", "  ")
	return append(data, '\n'), err
}
func statusString(n int) string { return strconv.Itoa(n) }

type builder struct {
	schemas Schema
	names   map[reflect.Type]string
	inputs  map[reflect.Type]Model
}

func (b *builder) content(m *Model, s Schema) Schema {
	if m != nil {
		return b.schema(reflect.TypeOf(m.Value))
	}
	if s != nil {
		return s
	}
	return Schema{}
}

var rawType = reflect.TypeOf(json.RawMessage{})
var timeType = reflect.TypeOf(time.Time{})

func (b *builder) schema(t reflect.Type) Schema {
	if t == rawType {
		return Schema{}
	} // arbitrary JSON, including null
	if t == timeType {
		return Schema{"type": "string", "format": "date-time"}
	}
	if t.Kind() == reflect.Pointer {
		return Schema{"anyOf": []any{b.schema(t.Elem()), Schema{"type": "null"}}}
	}
	switch t.Kind() {
	case reflect.Bool:
		return Schema{"type": "boolean"}
	case reflect.String:
		return Schema{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return Schema{"type": "number"}
	case reflect.Interface:
		return Schema{}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return Schema{"type": "string", "contentEncoding": "base64"}
		}
		return Schema{"type": []string{"array", "null"}, "items": b.schema(t.Elem())}
	case reflect.Map:
		return Schema{"type": []string{"object", "null"}, "additionalProperties": b.schema(t.Elem())}
	case reflect.Struct:
		name := b.names[t]
		if name == "" {
			name = t.Name()
			if name != "" {
				name = strings.ToUpper(name[:1]) + name[1:]
			}
		}
		if name == "" {
			return b.object(t)
		}
		if _, ok := b.schemas[name]; !ok {
			b.schemas[name] = Schema{}
			b.schemas[name] = b.object(t)
		}
		return Schema{"$ref": "#/components/schemas/" + name}
	default:
		panic("unsupported API DTO type: " + t.String())
	}
}
func (b *builder) object(t reflect.Type) Schema {
	properties := Schema{}
	required := []string{}
	var fields func(reflect.Type)
	fields = func(st reflect.Type) {
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			if f.Anonymous && tag == "" {
				fields(f.Type)
				continue
			}
			if f.PkgPath != "" {
				continue
			}
			parts := strings.Split(tag, ",")
			name := parts[0]
			if name == "" {
				name = f.Name
			}
			properties[name] = b.schema(f.Type)
			if f.Tag.Get("openapi") == "nonnullable" {
				properties[name].(Schema)["type"] = "array"
			}
			if !strings.Contains(","+tag+",", ",omitempty,") {
				required = append(required, name)
			}
		}
	}
	fields(t)
	if m, ok := b.inputs[t]; ok {
		return Schema{"type": "object", "properties": properties, "required": m.Required, "additionalProperties": false}
	}
	return Schema{"type": "object", "properties": properties, "required": required, "additionalProperties": true}
}
func BearerSecurity(description string) Schema {
	return Schema{"bearerAuth": Schema{"type": "http", "scheme": "bearer", "description": description}}
}
func Parameter(name, location string, required bool, schema Schema, description string) Schema {
	return Schema{"name": name, "in": location, "required": required, "schema": schema, "description": description}
}
func JSONResponse(status int, description string, name string, dto any) Response {
	return Response{Status: status, Description: description, MediaType: "application/json", Model: &Model{Name: name, Value: dto}}
}
