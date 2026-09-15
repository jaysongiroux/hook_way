package graph

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// BodyCloner supplies branch ownership for custom parsed bodies such as XML DOMs.
// CloneBody must return independent mutable storage. JSON bodies are built in.
// Unknown mutable types fail closed instead of silently aliasing across branches.
type BodyCloner interface{ CloneBody() (any, error) }

func cloneHead(h RequestHead) RequestHead {
	h.PathVars = maps.Clone(h.PathVars)
	h.Query = maps.Clone(h.Query)
	for k, v := range h.Query {
		h.Query[k] = slices.Clone(v)
	}
	h.Header = h.Header.Clone()
	return h
}
func cloneRaw(r RawRequest) RawRequest {
	r.Head = cloneHead(r.Head)
	r.Body = slices.Clone(r.Body)
	return r
}
func clonePayload(p Payload) (out Payload, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = fmt.Errorf("graph: payload clone panicked: %v", r)
		}
	}()
	switch v := p.(type) {
	case RequestHead:
		return cloneHead(v), nil
	case RawRequest:
		return cloneRaw(v), nil
	case Verified:
		v.Raw = cloneRaw(v.Raw)
		return v, nil
	case Envelope:
		v.V.Raw = cloneRaw(v.V.Raw)
		v.Vars = maps.Clone(v.Vars)
		b, err := cloneBody(v.Body, 0)
		if err != nil {
			return nil, err
		}
		v.Body = b
		if v.Upstream != nil {
			u := *v.Upstream
			u.Header = u.Header.Clone()
			u.Body = slices.Clone(u.Body)
			v.Upstream = &u
		}
		return v, nil
	default:
		return nil, fmt.Errorf("graph: unsupported payload %T; payloads must be concrete values", p)
	}
}
func cloneBody(v any, depth int) (any, error) {
	// Cycles cannot occur in parsed JSON. Bound unexpected operator-produced nesting.
	if depth > 256 {
		return nil, fmt.Errorf("graph: body exceeds clone depth limit")
	}
	switch b := v.(type) {
	case nil:
		return nil, nil
	case BodyCloner:
		return b.CloneBody()
	case []byte:
		return slices.Clone(b), nil
	case map[string]string:
		return maps.Clone(b), nil
	case []string:
		return slices.Clone(b), nil
	case map[string]any:
		if b == nil {
			return b, nil
		}
		out := make(map[string]any, len(b))
		for k, value := range b {
			copy, err := cloneBody(value, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = copy
		}
		return out, nil
	case []any:
		if b == nil {
			return b, nil
		}
		out := make([]any, len(b))
		for i, value := range b {
			copy, err := cloneBody(value, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = copy
		}
		return out, nil
	default:
		switch reflect.TypeOf(v).Kind() {
		case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			return v, nil
		}
		return nil, fmt.Errorf("graph: body type %T needs BodyCloner", v)
	}
}
