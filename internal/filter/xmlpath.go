package filter

import (
	"strings"

	"github.com/jaysongiroux/hook_way/internal/transform"
)

// walkXMLPath resolves a dotted path against an XML document, mirroring
// walkBodyPath's contract for JSON: it returns the leaf value and whether it
// was found, and a value that is absent is simply not found rather than an
// error.
//
// Segments name child elements by local name, starting below the root: the
// root element is the document's wrapper, the way a JSON body's outermost
// object is, so "ORDER.Amount" reads <root><ORDER><Amount>. A segment prefixed
// with "@" reads an attribute of the element addressed so far. Namespace
// prefixes are ignored on both elements and attributes, because a sender can
// change a prefix without changing what the document means and a filter
// should not break when they do.
//
// Repeated siblings resolve to the first in document order. walkBodyPath has
// the same limitation for JSON arrays; a filter is not the right tool for
// "any of these elements matches".
func walkXMLPath(doc transform.Document, path string) (any, bool) {
	if path == "" || doc.Root == nil {
		return nil, false
	}
	current := doc.Root
	for segment := range strings.SplitSeq(path, ".") {
		if segment == "" {
			return nil, false
		}
		if name, isAttr := strings.CutPrefix(segment, "@"); isAttr {
			// An attribute is terminal: nothing can be addressed below it.
			return attrValue(current, name)
		}
		child := firstChild(current, segment)
		if child == nil {
			return nil, false
		}
		current = child
	}
	// An element that exists resolves to its direct character data. Text is a
	// string so the existing comparisons keep working: toFloat coerces
	// strings, so gt/lt work on <Amount>500</Amount>, and the regex kind
	// requires a string.
	return current.Text, true
}

// firstChild returns the first child element whose local name matches, or nil.
func firstChild(e *transform.Element, name string) *transform.Element {
	for _, child := range e.Children {
		if child != nil && child.Name.Local == name {
			return child
		}
	}
	return nil
}

// attrValue reads an attribute by local name.
func attrValue(e *transform.Element, name string) (any, bool) {
	for _, attr := range e.Attr {
		if attr.Name.Local == name {
			return attr.Value, true
		}
	}
	return nil, false
}
