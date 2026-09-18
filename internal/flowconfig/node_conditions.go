package flowconfig

import (
	"fmt"
	"net/http"

	"github.com/jasongiroux/hookway/internal/filter"
	"github.com/jasongiroux/hookway/internal/graph"
)

// ConditionSetOptions is the shared rule shape: a flat condition list plus the
// combinator joining it. Embedded by the node option structs that route on a
// condition so the OR handling is written once.
type ConditionSetOptions struct {
	Match      string                   `yaml:"match,omitempty" json:"match,omitempty" jsonschema:"enum=all,enum=any"`
	Conditions []FilterConditionOptions `yaml:"conditions,omitempty" json:"conditions,omitempty" jsonschema:"minItems=1"`
}

func conditionSpecs(conds []FilterConditionOptions) []filter.Spec {
	specs := make([]filter.Spec, len(conds))
	for i, c := range conds {
		specs[i] = filter.Spec{Source: c.Source, Path: c.Path, Operator: c.Operator, Value: c.Value}
	}
	return specs
}

// conditionMode maps the config word to a combinator. The empty string is unset
// and means all, so a rule written before match existed keeps AND semantics.
func conditionMode(match string) (filter.Mode, bool) {
	switch match {
	case "", "all":
		return filter.MatchAll, true
	case "any":
		return filter.MatchAny, true
	}
	return filter.MatchAll, false
}

// compileConditionSet fails closed: a set that does not compile matches nothing
// rather than routing events on a half-understood rule.
func compileConditionSet(o ConditionSetOptions) filter.Chain {
	mode, ok := conditionMode(o.Match)
	if !ok {
		return filter.MatchNothing()
	}
	chain, issues := filter.CompileMode(conditionSpecs(o.Conditions), mode)
	if len(issues) > 0 {
		return filter.MatchNothing()
	}
	return chain
}

// validateConditions reports the first problem in a rule. prefix qualifies the
// field names for callers that nest the rule, such as "cases[0].".
func validateConditions(prefix, match string, conds []FilterConditionOptions) error {
	if _, ok := conditionMode(match); !ok {
		return fmt.Errorf("%smatch: unknown value %q, use all or any", prefix, match)
	}
	if len(conds) == 0 {
		return fmt.Errorf("%sconditions: at least one condition is required", prefix)
	}
	for i, c := range conds {
		if c.Source == "" || c.Path == "" || c.Operator == "" {
			return fmt.Errorf("%sconditions[%d] requires source, path, and operator", prefix, i)
		}
	}
	return nil
}

// eventHeaders is the header set a condition reads. A replayed event carries
// the headers it originally arrived with, and has to route the way it would
// have routed the first time.
func eventHeaders(env graph.Envelope) http.Header {
	if captured, ok := env.Extra.(CapturedEvent); ok && captured.Event.OrigHeaders != nil {
		return captured.Event.OrigHeaders
	}
	return env.V.Raw.Head.Header
}
