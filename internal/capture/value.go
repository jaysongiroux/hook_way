package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

// ErrUnsupported reports a payload value capture cannot encode losslessly.
// Capture falls back to a metadata-only record rather than store a lossy
// snapshot.
var ErrUnsupported = errors.New("capture: value cannot be snapshotted")

// maxValueDepth matches the executor's clone depth limit.
const maxValueDepth = 256

type (
	dsValue = deliverysnapshot.Value
	dsType  = deliverysnapshot.ValueType
)

var intTypes = map[reflect.Kind]dsType{
	reflect.Int: deliverysnapshot.TypeInt, reflect.Int8: deliverysnapshot.TypeInt8, reflect.Int16: deliverysnapshot.TypeInt16,
	reflect.Int32: deliverysnapshot.TypeInt32, reflect.Int64: deliverysnapshot.TypeInt64,
}

var uintTypes = map[reflect.Kind]dsType{
	reflect.Uint: deliverysnapshot.TypeUint, reflect.Uint8: deliverysnapshot.TypeUint8, reflect.Uint16: deliverysnapshot.TypeUint16,
	reflect.Uint32: deliverysnapshot.TypeUint32, reflect.Uint64: deliverysnapshot.TypeUint64,
}

// valueFromAny encodes every body value type the executor accepts (see
// graph's cloneBody): nil, XML documents, []byte, map[string]string, []string,
// map[string]any, []any, and scalars of a bool, string, integer, or float
// kind. A named scalar is recorded by its kind.
func valueFromAny(v any, depth int) (*dsValue, error) {
	if depth > maxValueDepth {
		return nil, fmt.Errorf("%w: nesting exceeds %d levels", ErrUnsupported, maxValueDepth)
	}
	switch x := v.(type) {
	case nil:
		return &dsValue{Type: deliverysnapshot.TypeNull}, nil
	case json.Number:
		return &dsValue{Type: deliverysnapshot.TypeJSONNumber, Text: string(x)}, nil
	case transform.Document:
		return &dsValue{Type: deliverysnapshot.TypeXML, XML: xmlFromDocument(x)}, nil
	case *transform.Document:
		if x == nil {
			return nil, fmt.Errorf("%w: nil *transform.Document", ErrUnsupported)
		}
		// The executor's clone already turns *Document into Document.
		return &dsValue{Type: deliverysnapshot.TypeXML, XML: xmlFromDocument(*x)}, nil
	case []byte:
		if x == nil {
			return &dsValue{Type: deliverysnapshot.TypeNilBytes}, nil
		}
		return &dsValue{Type: deliverysnapshot.TypeBytes, Bytes: slices.Clone(x)}, nil
	case map[string]string:
		if x == nil {
			return &dsValue{Type: deliverysnapshot.TypeNilStringMap}, nil
		}
		return &dsValue{Type: deliverysnapshot.TypeStringMap, StrMap: maps.Clone(x)}, nil
	case []string:
		if x == nil {
			return &dsValue{Type: deliverysnapshot.TypeNilStrings}, nil
		}
		return &dsValue{Type: deliverysnapshot.TypeStrings, Strings: slices.Clone(x)}, nil
	case map[string]any:
		if x == nil {
			return &dsValue{Type: deliverysnapshot.TypeNilMap}, nil
		}
		out := make(map[string]*dsValue, len(x))
		for k, item := range x {
			ev, err := valueFromAny(item, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = ev
		}
		return &dsValue{Type: deliverysnapshot.TypeMap, Map: out}, nil
	case []any:
		if x == nil {
			return &dsValue{Type: deliverysnapshot.TypeNilList}, nil
		}
		out := make([]*dsValue, len(x))
		for i, item := range x {
			ev, err := valueFromAny(item, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return &dsValue{Type: deliverysnapshot.TypeList, List: out}, nil
	}
	rv := reflect.ValueOf(v)
	kind := rv.Kind()
	switch {
	case kind == reflect.Bool:
		return &dsValue{Type: deliverysnapshot.TypeBool, Bool: rv.Bool()}, nil
	case kind == reflect.String:
		if text := rv.String(); !utf8.ValidString(text) {
			return &dsValue{Type: deliverysnapshot.TypeBinaryString, Bytes: []byte(text)}, nil
		}
		return &dsValue{Type: deliverysnapshot.TypeString, Text: rv.String()}, nil
	case intTypes[kind] != "":
		return &dsValue{Type: intTypes[kind], Text: strconv.FormatInt(rv.Int(), 10)}, nil
	case uintTypes[kind] != "":
		return &dsValue{Type: uintTypes[kind], Text: strconv.FormatUint(rv.Uint(), 10)}, nil
	case kind == reflect.Float32:
		return &dsValue{Type: deliverysnapshot.TypeFloat32, Text: strconv.FormatFloat(rv.Float(), 'g', -1, 32)}, nil
	case kind == reflect.Float64:
		return &dsValue{Type: deliverysnapshot.TypeFloat64, Text: strconv.FormatFloat(rv.Float(), 'g', -1, 64)}, nil
	}
	return nil, fmt.Errorf("%w: type %T", ErrUnsupported, v)
}

// anyFromValue reverses valueFromAny.
func anyFromValue(v *dsValue, depth int) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: missing value", ErrUnsupported)
	}
	if depth > maxValueDepth {
		return nil, fmt.Errorf("%w: nesting exceeds %d levels", ErrUnsupported, maxValueDepth)
	}
	switch v.Type {
	case deliverysnapshot.TypeNull:
		return nil, nil
	case deliverysnapshot.TypeBool:
		return v.Bool, nil
	case deliverysnapshot.TypeString:
		return v.Text, nil
	case deliverysnapshot.TypeBinaryString:
		return string(v.Bytes), nil
	case deliverysnapshot.TypeJSONNumber:
		return json.Number(v.Text), nil
	case deliverysnapshot.TypeBytes:
		if v.Bytes == nil {
			return []byte{}, nil
		}
		return slices.Clone(v.Bytes), nil
	case deliverysnapshot.TypeNilBytes:
		return []byte(nil), nil
	case deliverysnapshot.TypeStringMap:
		if v.StrMap == nil {
			return map[string]string{}, nil
		}
		return maps.Clone(v.StrMap), nil
	case deliverysnapshot.TypeNilStringMap:
		return map[string]string(nil), nil
	case deliverysnapshot.TypeStrings:
		if v.Strings == nil {
			return []string{}, nil
		}
		return slices.Clone(v.Strings), nil
	case deliverysnapshot.TypeNilStrings:
		return []string(nil), nil
	case deliverysnapshot.TypeMap:
		out := make(map[string]any, len(v.Map))
		for k, item := range v.Map {
			decoded, err := anyFromValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = decoded
		}
		return out, nil
	case deliverysnapshot.TypeNilMap:
		return map[string]any(nil), nil
	case deliverysnapshot.TypeList:
		out := make([]any, len(v.List))
		for i, item := range v.List {
			decoded, err := anyFromValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = decoded
		}
		return out, nil
	case deliverysnapshot.TypeNilList:
		return []any(nil), nil
	case deliverysnapshot.TypeXML:
		if v.XML == nil {
			return nil, fmt.Errorf("%w: xml value without a document", ErrUnsupported)
		}
		return documentFromXML(*v.XML), nil
	case deliverysnapshot.TypeFloat32:
		f, err := strconv.ParseFloat(v.Text, 32)
		return float32(f), numberErr(v, err)
	case deliverysnapshot.TypeFloat64:
		f, err := strconv.ParseFloat(v.Text, 64)
		return f, numberErr(v, err)
	}
	return parseInteger(v)
}

func parseInteger(v *dsValue) (any, error) {
	switch v.Type {
	case deliverysnapshot.TypeInt:
		n, err := strconv.ParseInt(v.Text, 10, strconv.IntSize)
		return int(n), numberErr(v, err)
	case deliverysnapshot.TypeInt8:
		n, err := strconv.ParseInt(v.Text, 10, 8)
		return int8(n), numberErr(v, err)
	case deliverysnapshot.TypeInt16:
		n, err := strconv.ParseInt(v.Text, 10, 16)
		return int16(n), numberErr(v, err)
	case deliverysnapshot.TypeInt32:
		n, err := strconv.ParseInt(v.Text, 10, 32)
		return int32(n), numberErr(v, err)
	case deliverysnapshot.TypeInt64:
		n, err := strconv.ParseInt(v.Text, 10, 64)
		return n, numberErr(v, err)
	case deliverysnapshot.TypeUint:
		n, err := strconv.ParseUint(v.Text, 10, strconv.IntSize)
		return uint(n), numberErr(v, err)
	case deliverysnapshot.TypeUint8:
		n, err := strconv.ParseUint(v.Text, 10, 8)
		return uint8(n), numberErr(v, err)
	case deliverysnapshot.TypeUint16:
		n, err := strconv.ParseUint(v.Text, 10, 16)
		return uint16(n), numberErr(v, err)
	case deliverysnapshot.TypeUint32:
		n, err := strconv.ParseUint(v.Text, 10, 32)
		return uint32(n), numberErr(v, err)
	case deliverysnapshot.TypeUint64:
		n, err := strconv.ParseUint(v.Text, 10, 64)
		return n, numberErr(v, err)
	}
	return nil, fmt.Errorf("%w: unknown value type %q", ErrUnsupported, v.Type)
}

func numberErr(v *dsValue, err error) error {
	if err != nil {
		return fmt.Errorf("%w: %s value %q: %w", ErrUnsupported, v.Type, v.Text, err)
	}
	return nil
}

func xmlFromDocument(d transform.Document) *deliverysnapshot.XMLDocument {
	return &deliverysnapshot.XMLDocument{Declaration: d.Declaration, Root: xmlFromElement(d.Root)}
}

func xmlFromElement(e *transform.Element) *deliverysnapshot.XMLElement {
	if e == nil {
		return nil
	}
	out := &deliverysnapshot.XMLElement{Name: e.Name, Attr: slices.Clone(e.Attr), Text: e.Text}
	if e.Children != nil {
		out.Children = make([]*deliverysnapshot.XMLElement, len(e.Children))
		for i, c := range e.Children {
			out.Children[i] = xmlFromElement(c)
		}
	}
	return out
}

func documentFromXML(d deliverysnapshot.XMLDocument) transform.Document {
	return transform.Document{Declaration: d.Declaration, Root: elementFromXML(d.Root)}
}

func elementFromXML(e *deliverysnapshot.XMLElement) *transform.Element {
	if e == nil {
		return nil
	}
	out := &transform.Element{Name: e.Name, Attr: slices.Clone(e.Attr), Text: e.Text}
	if e.Children != nil {
		out.Children = make([]*transform.Element, len(e.Children))
		for i, c := range e.Children {
			out.Children[i] = elementFromXML(c)
		}
	}
	return out
}
