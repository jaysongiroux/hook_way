package flowconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

// AWS SNS/SQS message-attribute limits. Oversized values fail the publish
// instead of being silently dropped.
const (
	maxMessageAttributes            = 10
	maxMessageAttributeNameBytes    = 256
	maxMessageAttributeValueBytes   = 1048576
	messageAttributeTypeString      = "String"
	messageAttributeTypeNumber      = "Number"
	messageAttributeTypeStringArray = "String.Array"
)

func renderMessageAttributes(raw map[string]any, values map[string]string) (map[string]graph.MessageAttribute, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > maxMessageAttributes {
		return nil, fmt.Errorf("message_attributes: at most %d attributes allowed (AWS limit), got %d", maxMessageAttributes, len(raw))
	}
	out := make(map[string]graph.MessageAttribute, len(raw))
	for name, value := range raw {
		if err := validateAttributeName(name); err != nil {
			return nil, err
		}
		attr, err := renderOneAttribute(name, value, values)
		if err != nil {
			return nil, err
		}
		size := len(attr.Value)
		base, _, _ := strings.Cut(attr.DataType, ".")
		if base == "Binary" {
			decoded, _ := base64.StdEncoding.Strict().DecodeString(attr.Value)
			size = len(decoded)
		}
		if size > maxMessageAttributeValueBytes {
			return nil, fmt.Errorf("message_attributes.%s: value is %d bytes, AWS limit is %d", name, len(attr.Value), maxMessageAttributeValueBytes)
		}
		if options, ok := value.(map[string]any); ok && options["omit_if_empty"] == true {
			var items []any
			_ = json.Unmarshal([]byte(attr.Value), &items)
			if len(items) == 0 {
				continue
			}
		}
		out[name] = attr
	}
	return out, nil
}

func validateAttributeName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("message_attributes: attribute name %q is empty or has surrounding space", name)
	}
	if len(name) > maxMessageAttributeNameBytes {
		return fmt.Errorf("message_attributes.%s: name is %d bytes, AWS limit is %d", name, len(name), maxMessageAttributeNameBytes)
	}
	return nil
}

func renderOneAttribute(name string, raw any, values map[string]string) (graph.MessageAttribute, error) {
	switch v := raw.(type) {
	case nil:
		return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: value is empty", name)
	case string:
		s, err := renderAttrString(v, values)
		if err != nil {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: %w", name, err)
		}
		return inferAttributeType(s), nil
	case bool:
		return graph.MessageAttribute{DataType: messageAttributeTypeString, Value: strconv.FormatBool(v)}, nil
	case int:
		return graph.MessageAttribute{DataType: messageAttributeTypeNumber, Value: strconv.Itoa(v)}, nil
	case int64:
		return graph.MessageAttribute{DataType: messageAttributeTypeNumber, Value: strconv.FormatInt(v, 10)}, nil
	case uint64:
		return graph.MessageAttribute{DataType: messageAttributeTypeNumber, Value: strconv.FormatUint(v, 10)}, nil
	case float64:
		if err := rejectNonFinite(v); err != nil {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: %w", name, err)
		}
		return graph.MessageAttribute{DataType: messageAttributeTypeNumber, Value: strconv.FormatFloat(v, 'f', -1, 64)}, nil
	case json.Number:
		if _, err := v.Float64(); err != nil {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: invalid number %q", name, v)
		}
		return graph.MessageAttribute{DataType: messageAttributeTypeNumber, Value: v.String()}, nil
	case []any:
		encoded, err := marshalAttrArray(v, values)
		if err != nil {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: %w", name, err)
		}
		return graph.MessageAttribute{DataType: messageAttributeTypeStringArray, Value: encoded}, nil
	case map[string]any:
		return renderTypedAttribute(name, v, values)
	default:
		return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: unsupported value type %T (use a string, number, array, or {data_type, value})", name, raw)
	}
}

func renderTypedAttribute(name string, m map[string]any, values map[string]string) (graph.MessageAttribute, error) {
	for key := range m {
		switch key {
		case "data_type", "DataType", "value", "Value", "omit_if_empty":
		default:
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: unknown typed attribute option", name)
		}
	}
	dt := stringField(m, "data_type", "DataType")
	if flag, set := m["omit_if_empty"]; set {
		if _, ok := flag.(bool); !ok || dt != messageAttributeTypeStringArray {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: omit_if_empty requires String.Array and a boolean", name)
		}
	}
	raw, ok := mapField(m, "value", "Value")
	if dt == "" || !ok {
		return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: typed attributes need data_type and value", name)
	}
	if len(dt) > 256 || strings.HasSuffix(dt, ".") {
		return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: invalid data type", name)
	}
	base, _, _ := strings.Cut(dt, ".")
	switch base {
	case "String", "Number", "Binary":
	default:
		return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: unsupported data_type %q (use String, Number, Binary, or String.Array)", name, dt)
	}
	s, err := coerceAttrString(raw, values)
	if err != nil {
		return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: %w", name, err)
	}
	switch {
	case base == "Binary":
		decoded, err := base64.StdEncoding.Strict().DecodeString(s)
		if err != nil || len(decoded) == 0 {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: Binary value must be nonempty base64", name)
		}
	case base == messageAttributeTypeNumber:
		if n, err := strconv.ParseFloat(s, 64); err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: Number value %q is not numeric", name, s)
		}
	case dt == messageAttributeTypeStringArray:
		var arr []any
		if err := json.Unmarshal([]byte(s), &arr); err != nil || arr == nil {
			return graph.MessageAttribute{}, fmt.Errorf("message_attributes.%s: String.Array value must be a JSON array, got %q", name, s)
		}
	}
	return graph.MessageAttribute{DataType: dt, Value: s}, nil
}

func inferAttributeType(s string) graph.MessageAttribute {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[") {
		var arr []any
		if json.Unmarshal([]byte(trimmed), &arr) == nil {
			return graph.MessageAttribute{DataType: messageAttributeTypeStringArray, Value: trimmed}
		}
	}
	return graph.MessageAttribute{DataType: messageAttributeTypeString, Value: s}
}

func renderAttrString(s string, values map[string]string) (string, error) {
	if !strings.Contains(s, "{") {
		return s, nil
	}
	tpl, err := vars.Parse(s)
	if err != nil {
		return "", err
	}
	return tpl.Render(values)
}

func coerceAttrString(raw any, values map[string]string) (string, error) {
	switch v := raw.(type) {
	case string:
		return renderAttrString(v, values)
	case []any:
		return marshalAttrArray(v, values)
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		if err := rejectNonFinite(v); err != nil {
			return "", err
		}
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return "", fmt.Errorf("cannot coerce %T to an attribute value", raw)
	}
}

func marshalAttrArray(arr []any, values map[string]string) (string, error) {
	out := make([]any, len(arr))
	for i, el := range arr {
		if s, ok := el.(string); ok {
			rendered, err := renderAttrString(s, values)
			if err != nil {
				return "", err
			}
			out[i] = rendered
			continue
		}
		out[i] = el
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}

func mapField(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v, true
		}
	}
	return nil, false
}

func rejectNonFinite(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("non-finite number")
	}
	return nil
}
