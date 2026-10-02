package flowconfig

import (
	"encoding/xml"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/transform"
)

// The code node hands scripts an XML body as an explicitly tagged node tree
// rather than a convenience mapping of element names to values. A convenience
// mapping cannot represent an attribute, two siblings with the same name, or
// text sitting beside child elements, so a script that read such a body and
// returned it would silently drop parts of the document. The shape is:
//
//	{
//	  declaration: "<?xml version=\"1.0\"?>",
//	  root: {
//	    name:     "MESSAGE",
//	    ns:       "",
//	    attr:     {id: "O1"},
//	    attrns:   {},
//	    text:     "",
//	    children: [ /* same shape, recursively */ ]
//	  }
//	}
//
// Every value is a string, map[string]any, or []any, so the tree is
// JSON-cloneable and goja can marshal it without seeing a Go pointer type.
//
// attr is keyed by local name, matching the filter's path semantics, so a
// script reads ORDER.attr.kind whatever prefix the sender used. That alone is
// lossy — it cannot tell ns:kind from kind, and an xmlns:ns declaration is
// itself an attribute — so attrns carries the namespace URI of any attribute
// that has one, keyed by the same local name. A script that only reads attr,
// or that assigns a plain attribute, never has to know attrns exists; a script
// that reads a body and returns it round-trips namespaces because attrns came
// along for the ride.
func xmlToScript(doc transform.Document) map[string]any {
	return map[string]any{
		"declaration": doc.Declaration,
		"root":        elementToScript(doc.Root),
		"before":      contentToScript(doc.Before, nil),
		"after":       contentToScript(doc.After, nil),
	}
}

func elementToScript(e *transform.Element) any {
	if e == nil {
		return nil
	}
	attr := make(map[string]any, len(e.Attr))
	attrns := map[string]any{}
	for _, a := range e.Attr {
		// Local names only, matching the filter's path semantics: a vendor can
		// change a namespace prefix without changing what the document means.
		attr[a.Name.Local] = a.Value
		if a.Name.Space != "" {
			attrns[a.Name.Local] = a.Name.Space
		}
	}
	children := make([]any, 0, len(e.Children))
	for _, c := range e.Children {
		if converted := elementToScript(c); converted != nil {
			children = append(children, converted)
		}
	}
	attributes := make([]any, 0, len(e.Attr))
	for i, a := range e.Attr {
		prefix := ""
		if i < len(e.AttrPrefixes) {
			prefix = e.AttrPrefixes[i]
		}
		attributes = append(attributes, map[string]any{"name": a.Name.Local, "ns": a.Name.Space, "prefix": prefix, "value": a.Value})
	}
	return map[string]any{
		"attribute_view": map[string]any{"attr": maps.Clone(attr), "attrns": maps.Clone(attrns)},
		"attributes":     attributes, "content": contentToScript(e.Content, e.Children), "prefix": e.Prefix, "qname": e.QName,
		"name":      e.Name.Local,
		"ns":        e.Name.Space,
		"attr":      attr,
		"attrns":    attrns,
		"text":      e.Text,
		"text_view": e.Text, // Baseline distinguishes legacy text edits from ordered content edits.
		"children":  children,
	}
}

// scriptToXML rebuilds a Document from the shape above. It is strict: a script
// that returns something else gets a message naming the expected shape rather
// than a half-built tree that would serialize into a malformed document.
func scriptToXML(v any) (transform.Document, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return transform.Document{}, fmt.Errorf("xml body must be an object {declaration, root}, got %T", v)
	}
	rootVal, exists := obj["root"]
	if !exists {
		return transform.Document{}, fmt.Errorf("xml body is missing \"root\"")
	}
	root, err := scriptToElement(rootVal, "root")
	if err != nil {
		return transform.Document{}, err
	}
	if root == nil {
		return transform.Document{}, fmt.Errorf("xml body \"root\" must not be null")
	}
	doc := transform.Document{Root: root}
	if decl, ok := obj["declaration"].(string); ok {
		doc.Declaration = decl
	}
	var convErr error
	doc.Before, convErr = scriptToContent(obj["before"], nil)
	if convErr != nil {
		return transform.Document{}, convErr
	}
	doc.After, convErr = scriptToContent(obj["after"], nil)
	if convErr != nil {
		return transform.Document{}, convErr
	}
	return doc, nil
}

func scriptToElement(v any, path string) (*transform.Element, error) {
	if v == nil {
		return nil, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("xml %s must be an object {name, attr, text, children}, got %T", path, v)
	}
	nameVal, exists := obj["name"]
	if !exists {
		return nil, fmt.Errorf("xml %s is missing \"name\"", path)
	}
	name, ok := nameVal.(string)
	if !ok || !transform.ValidXMLLocalName(name) {
		return nil, fmt.Errorf("xml %s \"name\" must be a non-empty string, got %v", path, nameVal)
	}
	out := &transform.Element{Name: xml.Name{Local: name}}
	if ns, ok := obj["ns"].(string); ok {
		out.Name.Space = ns
	}
	if textVal, exists := obj["text"]; exists && textVal != nil {
		// goja returns float64 for a number; stringify rather than reject so a
		// script can assign a computed value straight to text.
		out.Text = stringify(textVal)
	}
	spaces := map[string]string{}
	if nsVal, exists := obj["attrns"]; exists && nsVal != nil {
		nsMap, ok := nsVal.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("xml %s \"attrns\" must be an object, got %T", path, nsVal)
		}
		for k, val := range nsMap {
			spaces[k] = stringify(val)
		}
	}
	if attrVal, exists := obj["attr"]; exists && attrVal != nil {
		attrs, ok := attrVal.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("xml %s \"attr\" must be an object, got %T", path, attrVal)
		}
		for k, val := range attrs {
			if !transform.ValidXMLLocalName(k) {
				return nil, fmt.Errorf("xml invalid attribute name")
			}
			// An attribute with no attrns entry is unnamespaced, which is what
			// a script that assigned attr.foo directly intends.
			name := xml.Name{Local: k, Space: spaces[k]}
			out.Attr = append(out.Attr, xml.Attr{Name: name, Value: stringify(val)})
		}
		sortAttrs(out.Attr)
	}
	if kidsVal, exists := obj["children"]; exists && kidsVal != nil {
		kids, ok := kidsVal.([]any)
		if !ok {
			return nil, fmt.Errorf("xml %s \"children\" must be a list, got %T", path, kidsVal)
		}
		for i, kid := range kids {
			child, err := scriptToElement(kid, fmt.Sprintf("%s.children[%d]", path, i))
			if err != nil {
				return nil, err
			}
			if child != nil {
				out.Children = append(out.Children, child)
			}
		}
	}
	if prefix, ok := obj["prefix"].(string); ok {
		if prefix != "" && !transform.ValidXMLLocalName(prefix) {
			return nil, fmt.Errorf("xml invalid element prefix")
		}
		out.Prefix = prefix
	}
	if qname, ok := obj["qname"].(string); ok && (qname == name || strings.HasSuffix(qname, ":"+name)) {
		parts := strings.Split(qname, ":")
		if len(parts) > 2 || !transform.ValidXMLLocalName(parts[0]) {
			return nil, fmt.Errorf("xml invalid qualified name")
		}
		out.QName = qname
	}
	if values, ok := obj["attributes"].([]any); ok {
		ordered := make([]xml.Attr, 0, len(values))
		prefixes := make([]string, 0, len(values))
		facade := map[string]any{}
		facadens := map[string]any{}
		counts := map[string]int{}
		for _, v := range values {
			a, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("xml attributes must be objects")
			}
			local, ok := a["name"].(string)
			if !ok || !transform.ValidXMLLocalName(local) {
				return nil, fmt.Errorf("xml attribute name required")
			}
			ns, _ := a["ns"].(string)
			prefix, _ := a["prefix"].(string)
			if prefix != "" && !transform.ValidXMLLocalName(prefix) {
				return nil, fmt.Errorf("xml invalid attribute prefix")
			}
			value := stringify(a["value"])
			ordered = append(ordered, xml.Attr{Name: xml.Name{Local: local, Space: ns}, Value: value})
			prefixes = append(prefixes, prefix)
			facade[local] = value
			counts[local]++
			if ns != "" {
				facadens[local] = ns
			}
		}
		baselineAttr, baselineNS := any(facade), any(facadens)
		if view, ok := obj["attribute_view"].(map[string]any); ok {
			baselineAttr, baselineNS = view["attr"], view["attrns"]
		}
		if reflect.DeepEqual(baselineAttr, obj["attr"]) && reflect.DeepEqual(baselineNS, obj["attrns"]) {
			out.Attr = ordered
			out.AttrPrefixes = prefixes
		} else {
			// Legacy attr/attrns edits retain ordered attributes and lexical prefixes.
			attrs, _ := obj["attr"].(map[string]any)
			merged := []xml.Attr{}
			keptPrefixes := []string{}
			seen := map[string]bool{}
			for i, a := range ordered {
				val, exists := attrs[a.Name.Local]
				if !exists {
					continue
				}
				ns := spaces[a.Name.Local]
				if counts[a.Name.Local] > 1 {
					if !reflect.DeepEqual(val, facade[a.Name.Local]) || ns != stringify(facadens[a.Name.Local]) {
						return nil, fmt.Errorf("xml ambiguous attribute edit: use attributes for duplicate local names")
					}
					merged = append(merged, a)
				} else {
					a.Value = stringify(val)
					if a.Name.Space != ns {
						a.Name.Space = ns
						prefixes[i] = ""
					}
					merged = append(merged, a)
				}
				keptPrefixes = append(keptPrefixes, prefixes[i])
				seen[a.Name.Local] = true
			}
			for _, a := range out.Attr {
				if !seen[a.Name.Local] {
					merged = append(merged, a)
					keptPrefixes = append(keptPrefixes, "")
				}
			}
			out.Attr = merged
			out.AttrPrefixes = keptPrefixes
		}
	}
	content, err := scriptToContent(obj["content"], out.Children)
	if err != nil {
		return nil, err
	}
	out.Content = content
	if out.Content != nil {
		var text strings.Builder
		for _, c := range out.Content {
			if c.Kind == "text" {
				text.WriteString(c.Text)
			}
		}
		baseline, retained := obj["text_view"].(string)
		if retained && out.Text == baseline {
			out.Text = text.String()
		} else if text.String() != out.Text {
			updated := make([]transform.XMLContent, 0, len(out.Content)+1)
			inserted := false
			for _, c := range out.Content {
				if c.Kind == "text" {
					if !inserted {
						c.Text = out.Text
						updated = append(updated, c)
						inserted = true
					}
					continue
				}
				updated = append(updated, c)
			}
			if !inserted && out.Text != "" {
				updated = append([]transform.XMLContent{{Kind: "text", Text: out.Text}}, updated...)
			}
			out.Content = updated
		}
	}
	return out, nil
}

// sortAttrs gives attributes a deterministic order. They arrive from a JS
// object, whose key order Go's map does not preserve, and an unstable order
// would make the serialized document differ run to run.
func sortAttrs(attrs []xml.Attr) {
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name.Local < attrs[j].Name.Local })
}

func contentToScript(content []transform.XMLContent, children []*transform.Element) any {
	if content == nil {
		return nil
	}
	out := make([]any, 0, len(content))
	indices := map[*transform.Element]int{}
	for i, c := range children {
		indices[c] = i
	}
	for _, c := range content {
		item := map[string]any{"kind": c.Kind, "text": c.Text, "target": c.Target}
		if c.Element != nil {
			idx, ok := indices[c.Element]
			if !ok {
				continue
			}
			item["child"] = idx
		}
		out = append(out, item)
	}
	return out
}
func scriptToContent(v any, children []*transform.Element) ([]transform.XMLContent, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("xml content must be a list")
	}
	out := make([]transform.XMLContent, 0, len(items))
	seen := map[int]bool{}
	for _, v := range items {
		item, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("xml content must contain objects")
		}
		kind, _ := item["kind"].(string)
		text, _ := item["text"].(string)
		target, _ := item["target"].(string)
		c := transform.XMLContent{Kind: kind, Text: text, Target: target}
		if child, exists := item["child"]; exists {
			idx, err := strconv.Atoi(fmt.Sprint(child))
			if err != nil || idx < 0 || idx >= len(children) || seen[idx] {
				return nil, fmt.Errorf("xml invalid content child index")
			}
			c.Element = children[idx]
			seen[idx] = true
		} else if kind != "text" && kind != "comment" && kind != "pi" {
			return nil, fmt.Errorf("xml invalid content kind")
		}
		out = append(out, c)
	}
	// Newly appended child elements follow the existing content.
	for i, child := range children {
		if !seen[i] {
			out = append(out, transform.XMLContent{Element: child})
		}
	}
	return out, nil
}
