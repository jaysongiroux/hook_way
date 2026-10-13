package transform

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"gopkg.in/yaml.v3"
)

// JSONSchema keeps numeric schema constraints exact when reading authored YAML.
// Its wire representation is an ordinary JSON object.
type JSONSchema map[string]any

func (s *JSONSchema) UnmarshalYAML(node *yaml.Node) error {
	count := 0
	value, err := schemaYAMLValue(node, 0, &count)
	if err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("JSON schema must be an object")
	}
	*s = object
	return nil
}
func schemaYAMLValue(node *yaml.Node, depth int, count *int) (any, error) {
	*count++
	if depth > 64 || *count > 10000 {
		return nil, errors.New("JSON schema structure limit exceeded")
	}
	switch node.Kind {
	case yaml.AliasNode:
		return schemaYAMLValue(node.Alias, depth+1, count)
	case yaml.MappingNode:
		out := map[string]any{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, errors.New("JSON schema object keys must be strings")
			}
			if _, exists := out[key.Value]; exists {
				return nil, errors.New("JSON schema contains a duplicate key")
			}
			value, err := schemaYAMLValue(node.Content[i+1], depth+1, count)
			if err != nil {
				return nil, err
			}
			out[key.Value] = value
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, len(node.Content))
		for i, child := range node.Content {
			value, err := schemaYAMLValue(child, depth+1, count)
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return out, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			// YAML's resolver labels overflowing decimal literals as strings.
			// Quoting explicitly selects a string; plain decimals stay numeric.
			if node.Style == 0 && decimalPattern.MatchString(strings.ReplaceAll(node.Value, "_", "")) {
				text, ok := exactDecimal(strings.ReplaceAll(node.Value, "_", ""))
				if !ok {
					return nil, errors.New("JSON schema numbers must be finite bounded decimals")
				}
				return json.Number(text), nil
			}
			return node.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			var value bool
			err := node.Decode(&value)
			return value, err
		case "!!int":
			text := strings.ReplaceAll(node.Value, "_", "")
			if len(text) > maxNumericDigits {
				return nil, errors.New("JSON schema numeric limit exceeded")
			}
			integer, ok := new(big.Int).SetString(text, 0)
			if !ok {
				integer, ok = new(big.Int).SetString(text, 10)
			}
			if !ok {
				return nil, errors.New("invalid JSON schema integer")
			}
			return json.Number(integer.String()), nil
		case "!!float":
			text, ok := exactDecimal(strings.ReplaceAll(node.Value, "_", ""))
			if !ok {
				return nil, errors.New("JSON schema numbers must be finite bounded decimals")
			}
			return json.Number(text), nil
		}
	}
	return nil, errors.New("JSON schema must contain JSON-compatible YAML values")
}

func (s JSONSchema) MarshalYAML() (any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	return node.Content[0], nil
}
