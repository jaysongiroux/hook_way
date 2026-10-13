// Package featureflags defines native-independent evaluation contracts.
package featureflags

import (
	"encoding/json"
	"errors"

	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
)

type Context struct {
	Kind, Key  string
	Attributes map[string]any
}
type Result struct {
	Value             any
	UsedFallback      bool
	Reason, ErrorKind string
}
type Evaluator func(Context, string, string, any) Result

// Normalize gives YAML literals and SDK values the same bounded JSON representation.
func Normalize(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("value must be finite JSON")
	}
	if err := jsonbudget.Check(data, jsonbudget.Limits{}); err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("invalid JSON value")
	}
	return out, nil
}
func Matches(kind string, value any) bool {
	switch kind {
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "json":
		return true
	}
	return false
}
