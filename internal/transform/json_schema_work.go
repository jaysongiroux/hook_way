package transform

import (
	"encoding/json"
	"errors"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const maxSchemaExpansion = 10000
const maxValidationWork = 1000000
const maxValidationTextWork = 32 << 20

var ErrJSONValidationWork = errors.New("JSON validation exceeds supported work limit")

// The validator library has no interruptible work budget. Bound the expanded
// compiled graph before it can allocate an amplified error tree. Count every
// branch conservatively, including dynamic/recursive reference candidates.
// Recursive instance schemas may descend at most the supported body depth.
func schemaWork(root *jsonschema.Schema, declared declaredSchemaAnchors) (int, bool, error) {
	links := map[*jsonschema.Schema][]*jsonschema.Schema{}
	var gather func(*jsonschema.Schema)
	gather = func(s *jsonschema.Schema) {
		if s == nil {
			return
		}
		if _, found := links[s]; found {
			return
		}
		links[s] = schemaChildren(s)
		for _, child := range links[s] {
			gather(child)
		}
	}
	gather(root)
	for _, anchor := range declared.roots {
		gather(anchor)
	}
	unique := false
	anchors := map[string][]*jsonschema.Schema{}
	var recursiveAnchors []*jsonschema.Schema
	recursiveSeen := map[*jsonschema.Schema]bool{}
	addRecursive := func(candidate *jsonschema.Schema) {
		if candidate != nil && !recursiveSeen[candidate] {
			recursiveSeen[candidate] = true
			recursiveAnchors = append(recursiveAnchors, candidate)
		}
	}
	for s := range links {
		unique = unique || s.UniqueItems
		if s.DynamicAnchor != "" {
			anchors[s.DynamicAnchor] = append(anchors[s.DynamicAnchor], s)
		}
		if s.RecursiveAnchor {
			addRecursive(s)
		}

		// Entering an anchored resource through a reference can skip its root.
		// The recursive resolver selects that entry, not the resource root.
		for _, entry := range []*jsonschema.Schema{s.Ref, s.RecursiveRef} {
			if declared.recursiveEntry(entry) {
				addRecursive(entry)
			}
		}
		if s.DynamicRef != nil && declared.recursiveEntry(s.DynamicRef.Ref) {
			addRecursive(s.DynamicRef.Ref)
		}
		if s.DynamicAnchor != "" && declared.recursiveEntry(s) {
			addRecursive(s)
		}
	}
	for s := range links {
		appendCandidate := func(candidate *jsonschema.Schema) {
			for _, existing := range links[s] {
				if existing == candidate {
					return
				}
			}
			links[s] = append(links[s], candidate)
		}
		if s.DynamicRef != nil && s.DynamicRef.Anchor != "" {
			for _, candidate := range anchors[s.DynamicRef.Anchor] {
				appendCandidate(candidate)
			}
		}
		if s.RecursiveRef != nil {
			for _, candidate := range recursiveAnchors {
				appendCandidate(candidate)
			}
		}
	}
	// Memoize acyclic subgraphs, retaining multiplicity when references repeat.
	// Cyclic costs depend on the active path and must not use that cache.
	memo := map[*jsonschema.Schema]int{}
	steps := 0
	var cost func(*jsonschema.Schema, int, map[*jsonschema.Schema]bool) (int, bool)
	cost = func(s *jsonschema.Schema, remaining int, active map[*jsonschema.Schema]bool) (int, bool) {
		if s == nil {
			return 0, false
		}
		steps++
		if steps > maxSchemaExpansion*10 {
			return maxSchemaExpansion + 1, true
		}
		if active[s] {
			if remaining == 0 {
				return 1, true
			}
			n, _ := cost(s, remaining-1, map[*jsonschema.Schema]bool{})
			return n, true
		}
		if cached, found := memo[s]; found {
			return cached, false
		}
		active[s] = true
		defer delete(active, s)
		n := 1 + len(s.Required)
		for _, required := range s.DependentRequired {
			n += len(required)
		}
		for _, dependency := range s.Dependencies {
			if required, ok := dependency.([]string); ok {
				n += len(required)
			}
		}
		if s.Pattern != nil {
			n += len(s.Pattern.String())
		}
		for pattern := range s.PatternProperties {
			n += len(pattern.String())
		}
		if s.Enum != nil {
			n += len(s.Enum.Values)
		}
		cyclic := false
		for _, child := range links[s] {
			childCost, childCyclic := cost(child, remaining, active)
			cyclic = cyclic || childCyclic
			n += childCost
			if n > maxSchemaExpansion {
				return maxSchemaExpansion + 1, cyclic
			}
		}
		if !cyclic {
			memo[s] = n
		}
		return n, cyclic
	}
	n, _ := cost(root, 128, map[*jsonschema.Schema]bool{})
	if n > maxSchemaExpansion {
		return 0, false, errors.New("JSON schema expansion exceeds supported work limit")
	}
	return n, unique, nil
}

func schemaChildren(s *jsonschema.Schema) []*jsonschema.Schema {
	children := []*jsonschema.Schema{s.Ref, s.RecursiveRef, s.Not, s.If, s.Then, s.Else,
		s.PropertyNames, s.UnevaluatedProperties, s.Contains, s.Items2020, s.UnevaluatedItems, s.ContentSchema}
	if s.DynamicRef != nil {
		children = append(children, s.DynamicRef.Ref)
	}
	for _, list := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf, s.PrefixItems} {
		children = append(children, list...)
	}
	for _, dict := range []map[string]*jsonschema.Schema{s.Properties, s.DependentSchemas} {
		for _, child := range dict {
			children = append(children, child)
		}
	}
	for _, child := range s.PatternProperties {
		children = append(children, child)
	}
	for _, item := range []any{s.Items, s.AdditionalItems, s.AdditionalProperties} {
		switch value := item.(type) {
		case *jsonschema.Schema:
			children = append(children, value)
		case []*jsonschema.Schema:
			children = append(children, value...)
		}
	}
	for _, item := range s.Dependencies {
		if child, ok := item.(*jsonschema.Schema); ok {
			children = append(children, child)
		}
	}
	return children
}

func maxArrayWidth(value any) int {
	maxWidth := 1
	switch v := value.(type) {
	case []any:
		maxWidth = max(maxWidth, len(v))
		for _, child := range v {
			maxWidth = max(maxWidth, maxArrayWidth(child))
		}
	case map[string]any:
		for _, child := range v {
			maxWidth = max(maxWidth, maxArrayWidth(child))
		}
	}
	return maxWidth
}

// Node count alone misses repeated scanning of a large string. Weight names,
// strings and numeric digits/exponents as well, saturating before overflow.
func validationTextWork(value any) int {
	work := 1
	add := func(n int) { work = min(maxValidationTextWork+1, work+n) }
	switch v := value.(type) {
	case string:
		add(len(v))
	case json.Number:
		add(len(v))
		if digits, point, _, ok := decimalParts(string(v)); ok {
			add(len(digits) + max(point, -point))
		}
	case []any:
		for _, child := range v {
			add(validationTextWork(child))
			if work > maxValidationTextWork {
				break
			}
		}
	case map[string]any:
		for key, child := range v {
			add(len(key))
			add(validationTextWork(child))
			if work > maxValidationTextWork {
				break
			}
		}
	}
	return work
}
