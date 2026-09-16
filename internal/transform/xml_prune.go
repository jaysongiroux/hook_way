package transform

import (
	"fmt"
	"strings"
	"time"
)

// DefaultXMLPruneRules is the load-bearing DocMagic order. Later rules assume
// earlier ones have already run. Universal-gateway pipeline.js is not in this
// repo; predicates below are the documented contract, tested against
// synthetic MISMO-shaped fixtures.
var DefaultXMLPruneRules = []string{
	RuleRemoveEmptySelfClosingElements,
	RuleRemoveEmptyParents,
	RuleRemoveHardcodedOnlyParties,
	RuleRemoveBlankGeneralPartners,
	RulePruneOrphanedRelationships,
}

const (
	RuleRemoveEmptySelfClosingElements = "remove_empty_self_closing_elements"
	RuleRemoveEmptyParents             = "remove_empty_parents"
	RuleRemoveHardcodedOnlyParties     = "remove_hardcoded_only_parties"
	RuleRemoveBlankGeneralPartners     = "remove_blank_general_partners"
	RulePruneOrphanedRelationships     = "prune_orphaned_relationships"
)

// PruneLogFunc is called after each rule with element counts. Rule names come
// from config (bounded enum), never from payload bytes.
type PruneLogFunc func(rule string, index, elementsBefore, elementsAfter int, d time.Duration)

// KnownXMLPruneRule reports whether name is one of the five pipeline rules.
func KnownXMLPruneRule(name string) bool {
	switch name {
	case RuleRemoveEmptySelfClosingElements,
		RuleRemoveEmptyParents,
		RuleRemoveHardcodedOnlyParties,
		RuleRemoveBlankGeneralPartners,
		RulePruneOrphanedRelationships:
		return true
	default:
		return false
	}
}

// Prune clones doc, applies rules in order, and returns the rewrite. The input
// document is not mutated. A failing rule aborts without returning a partial
// tree to the caller.
func Prune(doc Document, rules []string, log PruneLogFunc) (Document, error) {
	if doc.Root == nil {
		return Document{}, fmt.Errorf("transform: xml_prune: empty document")
	}
	if len(rules) == 0 {
		rules = DefaultXMLPruneRules
	}
	for _, rule := range rules {
		if !KnownXMLPruneRule(rule) {
			return Document{}, fmt.Errorf("transform: xml_prune: unknown rule %q", rule)
		}
	}
	out := doc.Clone()
	for i, rule := range rules {
		before := CountElements(out)
		start := time.Now()
		if err := applyXMLPruneRule(out.Root, rule); err != nil {
			return Document{}, err
		}
		after := CountElements(out)
		if log != nil {
			log(rule, i, before, after, time.Since(start))
		}
	}
	return out, nil
}

func applyXMLPruneRule(root *Element, rule string) error {
	switch rule {
	case RuleRemoveEmptySelfClosingElements:
		removeEmptySelfClosing(root)
	case RuleRemoveEmptyParents:
		removeEmptyParents(root)
	case RuleRemoveHardcodedOnlyParties:
		removeMatching(root, isHardcodedOnlyParty)
	case RuleRemoveBlankGeneralPartners:
		removeMatching(root, isBlankGeneralPartner)
	case RulePruneOrphanedRelationships:
		labels := map[string]struct{}{}
		collectLabels(root, labels)
		removeMatching(root, func(el *Element) bool { return isOrphanRelationship(el, labels) })
	default:
		return fmt.Errorf("transform: xml_prune: unknown rule %q", rule)
	}
	return nil
}

// structuralNames are rewritten by later dedicated rules. Rules 1 and 2 must
// not steal PARTY / GENERAL_PARTNER / RELATIONSHIP nodes or those later rules
// become no-ops after parse (self-closing and empty-element forms are identical).
func isStructuralName(el *Element) bool {
	return localNameEq(el, "PARTY") || localNameEq(el, "GENERAL_PARTNER") || localNameEq(el, "RELATIONSHIP")
}

func isEmptyLeaf(el *Element) bool {
	return el != nil && len(el.Children) == 0 && strings.TrimSpace(el.Text) == "" && len(el.Attr) == 0
}

func removeEmptySelfClosing(el *Element) {
	if el == nil {
		return
	}
	kept := el.Children[:0]
	for _, c := range el.Children {
		if isEmptyLeaf(c) && !isStructuralName(c) {
			continue
		}
		removeEmptySelfClosing(c)
		kept = append(kept, c)
	}
	el.Children = kept
}

func removeEmptyParents(el *Element) {
	if el == nil {
		return
	}
	kept := el.Children[:0]
	for _, c := range el.Children {
		removeEmptyParents(c)
		if isEmptyLeaf(c) && !isStructuralName(c) {
			continue
		}
		kept = append(kept, c)
	}
	el.Children = kept
}

func removeMatching(el *Element, drop func(*Element) bool) {
	if el == nil {
		return
	}
	kept := el.Children[:0]
	for _, c := range el.Children {
		removeMatching(c, drop)
		if drop(c) {
			continue
		}
		kept = append(kept, c)
	}
	el.Children = kept
}

// isHardcodedOnlyParty is the contract for rule 3:
// a PARTY with Hardcoded="true"/"1"/"yes", a HARDCODED child, or neither
// INDIVIDUAL nor LEGAL_ENTITY descendants (role-only stub).
func isHardcodedOnlyParty(el *Element) bool {
	if !localNameEq(el, "PARTY") {
		return false
	}
	if boolAttr(el, "Hardcoded") || hasChildLocal(el, "HARDCODED") {
		return true
	}
	return !hasDescendantLocal(el, "INDIVIDUAL") && !hasDescendantLocal(el, "LEGAL_ENTITY")
}

func isBlankGeneralPartner(el *Element) bool {
	return localNameEq(el, "GENERAL_PARTNER") && !hasNonEmptyText(el)
}

func collectLabels(el *Element, labels map[string]struct{}) {
	if el == nil {
		return
	}
	for _, a := range el.Attr {
		if strings.EqualFold(a.Name.Local, "label") && a.Value != "" {
			labels[a.Value] = struct{}{}
		}
	}
	for _, c := range el.Children {
		collectLabels(c, labels)
	}
}

func isOrphanRelationship(el *Element, labels map[string]struct{}) bool {
	if !localNameEq(el, "RELATIONSHIP") {
		return false
	}
	from, to := attrLocal(el, "from"), attrLocal(el, "to")
	if from == "" && to == "" {
		return true
	}
	if from != "" {
		if _, ok := labels[from]; !ok {
			return true
		}
	}
	if to != "" {
		if _, ok := labels[to]; !ok {
			return true
		}
	}
	return false
}
