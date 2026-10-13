package transform

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

const xmlNamespaceURI = "http://www.w3.org/2000/xmlns/"

var ErrXMLLimit = errors.New("XML parse limit exceeded")

type XMLLimits struct {
	MaxBytes           int64
	MaxDepth, MaxNodes int
}

func (l XMLLimits) defaults() XMLLimits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = 1 << 20
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = 256
	}
	if l.MaxNodes <= 0 {
		l.MaxNodes = 100000
	}
	return l
}

// Document is a cloneable XML tree stored on graph.Envelope.Body. Raw request
// bytes stay on Verified; this tree is the post-auth rewrite surface.
type Document struct {
	Root          *Element
	Declaration   string
	Before, After []XMLContent
}

// Element is one XML element. Children are child elements in document order.
// Text is concatenated direct character data, not descendant text.
type Element struct {
	Name     xml.Name
	Attr     []xml.Attr
	Children []*Element
	Text     string
	// Content retains mixed-content order. Children remains the element index
	// used by pruning; removed children are omitted by the serializer.
	Content      []XMLContent
	Prefix       string
	QName        string
	AttrPrefixes []string
}

// XMLContent is an element, text, comment, or processing instruction.
type XMLContent struct {
	Element *Element
	Kind    string
	Text    string
	Target  string
}

func (d Document) CloneBody() (any, error) {
	return d.Clone(), nil
}

func (d Document) Clone() Document {
	out := Document{Declaration: d.Declaration, Before: slices.Clone(d.Before), After: slices.Clone(d.After)}
	if d.Root != nil {
		out.Root = d.Root.clone()
	}
	return out
}

func (e *Element) clone() *Element {
	if e == nil {
		return nil
	}
	out := &Element{Name: e.Name, Prefix: e.Prefix, QName: e.QName, Text: e.Text, Attr: slices.Clone(e.Attr), AttrPrefixes: slices.Clone(e.AttrPrefixes)}
	if e.Children != nil {
		out.Children = make([]*Element, 0, len(e.Children))
	}
	if e.Content != nil {
		out.Content = make([]XMLContent, 0, len(e.Content))
	}
	index := map[*Element]*Element{}
	for _, c := range e.Children {
		copy := c.clone()
		out.Children = append(out.Children, copy)
		index[c] = copy
	}
	for _, c := range e.Content {
		if c.Element != nil {
			c.Element = index[c.Element]
			if c.Element == nil {
				continue
			}
		}
		out.Content = append(out.Content, c)
	}
	return out
}

// syncContent removes references to pruned children while retaining all other nodes.
func (e *Element) syncContent() {
	if e.Content == nil {
		return
	}
	retained := make(map[*Element]bool, len(e.Children))
	for _, child := range e.Children {
		retained[child] = true
	}
	kept := e.Content[:0]
	for _, item := range e.Content {
		if item.Element == nil || retained[item.Element] {
			kept = append(kept, item)
		}
	}
	clear(e.Content[len(kept):])
	e.Content = kept
}

// ParseXMLWithLimits builds a bounded Document from bytes. Non-UTF-8 charset
// declarations fail closed rather than silently corrupting the tree.
func ParseXMLWithLimits(data []byte, limits XMLLimits) (Document, error) {
	limits = limits.defaults()
	if int64(len(data)) > limits.MaxBytes {
		return Document{}, fmt.Errorf("%w: max_bytes", ErrXMLLimit)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = utf8CharsetReader

	var doc Document
	var stack []*Element
	var namespaces []map[string]string
	nodes := 0
	appendContent := func(c XMLContent) {
		if len(stack) > 0 {
			el := stack[len(stack)-1]
			el.Content = append(el.Content, c)
		} else if doc.Root == nil {
			doc.Before = append(doc.Before, c)
		} else {
			doc.After = append(doc.After, c)
		}
	}
	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Document{}, fmt.Errorf("transform: xml parse: %w", err)
		}
		if _, end := tok.(xml.EndElement); !end {
			nodes++
		}
		if nodes > limits.MaxNodes {
			return Document{}, fmt.Errorf("%w: max_nodes", ErrXMLLimit)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) >= limits.MaxDepth {
				return Document{}, fmt.Errorf("%w: max_depth", ErrXMLLimit)
			}
			// Keep only this element's declarations. Cloning the inherited map
			// for every sibling multiplies a small wire document into many copies
			// of the same attacker-controlled namespace table.
			ns := map[string]string{"xml": "http://www.w3.org/XML/1998/namespace"}
			for _, a := range t.Attr {
				if prefix, ok := xmlnsPrefix(a.Name); ok {
					if prefix == "xmlns" || (prefix == "xml" && a.Value != ns["xml"]) || (prefix != "xml" && a.Value == ns["xml"]) || a.Value == xmlNamespaceURI || (prefix != "" && a.Value == "") {
						return Document{}, errors.New("transform: invalid namespace binding")
					}
					ns[prefix] = a.Value
				}
			}
			resolve := func(name xml.Name, attribute bool) (xml.Name, error) {
				if attribute && name.Space == "" {
					return name, nil
				}
				uri, ok := ns[name.Space]
				for i := len(namespaces) - 1; !ok && i >= 0; i-- {
					uri, ok = namespaces[i][name.Space]
				}
				if name.Space != "" && (!ok || uri == "") {
					return xml.Name{}, errors.New("transform: unbound XML prefix")
				}
				return xml.Name{Space: uri, Local: name.Local}, nil
			}
			name, err := resolve(t.Name, false)
			if err != nil {
				return Document{}, err
			}
			el := &Element{Name: name, Prefix: t.Name.Space, QName: t.Name.Local}
			if t.Name.Space != "" {
				el.QName = t.Name.Space + ":" + t.Name.Local
			}
			seen := map[xml.Name]bool{}
			for _, a := range t.Attr {
				prefix := a.Name.Space
				if _, declaration := xmlnsPrefix(a.Name); !declaration {
					a.Name, err = resolve(a.Name, true)
					if err != nil {
						return Document{}, err
					}
				}
				if seen[a.Name] {
					return Document{}, errors.New("transform: duplicate XML attribute")
				}
				seen[a.Name] = true
				el.Attr = append(el.Attr, a)
				el.AttrPrefixes = append(el.AttrPrefixes, prefix)
			}
			if len(stack) == 0 {
				if doc.Root != nil {
					return Document{}, errors.New("transform: xml parse: multiple roots")
				}
				doc.Root = el
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, el)
				appendContent(XMLContent{Element: el})
			}
			stack = append(stack, el)
			namespaces = append(namespaces, ns)
		case xml.EndElement:
			if len(stack) == 0 {
				return Document{}, errors.New("transform: unmatched XML end element")
			}
			el := stack[len(stack)-1]
			if el.Name.Local != t.Name.Local || el.Prefix != t.Name.Space {
				return Document{}, errors.New("transform: mismatched XML end element")
			}
			// Aggregate once: avoid quadratic text accumulation for large documents.
			var text strings.Builder
			for _, c := range el.Content {
				if c.Kind == "text" {
					text.WriteString(c.Text)
				}
			}
			el.Text = text.String()
			stack = stack[:len(stack)-1]
			namespaces = namespaces[:len(namespaces)-1]
		case xml.CharData:
			if len(stack) == 0 && len(bytes.TrimSpace(t)) > 0 {
				return Document{}, errors.New("transform: text outside XML root")
			}
			appendContent(XMLContent{Kind: "text", Text: string(t)})
		case xml.Comment:
			appendContent(XMLContent{Kind: "comment", Text: string(t)})
		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") {
				if t.Target != "xml" || doc.Root != nil || doc.Declaration != "" || len(doc.Before) != 0 {
					return Document{}, errors.New("transform: invalid XML declaration")
				}
				doc.Declaration = "<?xml " + string(t.Inst) + "?>"
			} else {
				appendContent(XMLContent{Kind: "pi", Target: t.Target, Text: string(t.Inst)})
			}
		case xml.Directive:
			return Document{}, errors.New("transform: XML directives and DTDs are unsupported")
		}
	}
	if doc.Root == nil || len(stack) != 0 {
		return Document{}, errors.New("transform: empty or unclosed XML document")
	}
	return doc, nil
}

func utf8CharsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii":
		return input, nil
	default:
		return nil, fmt.Errorf("unsupported XML charset %q", charset)
	}
}

// SerializeXML writes a compact XML document. Empty leaves become self-closing.
func SerializeXML(doc Document) ([]byte, error) {
	if doc.Root == nil {
		return nil, fmt.Errorf("transform: empty XML document")
	}
	var b strings.Builder
	if doc.Declaration != "" {
		b.WriteString(doc.Declaration)
	}
	for _, c := range doc.Before {
		if err := writeContent(&b, c); err != nil {
			return nil, err
		}
	}
	if err := writeElement(&b, doc.Root, nil); err != nil {
		return nil, err
	}
	for _, c := range doc.After {
		if err := writeContent(&b, c); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

func writeElement(b *strings.Builder, el *Element, parentNS map[string]string) error {
	ns := maps.Clone(parentNS)
	if ns == nil {
		ns = map[string]string{}
	}
	for _, a := range el.Attr {
		if prefix, ok := xmlnsPrefix(a.Name); ok {
			ns[a.Value] = prefix
		}
	}

	b.WriteByte('<')
	b.WriteString(elementQName(el, ns))
	for i, a := range el.Attr {
		b.WriteByte(' ')
		if prefix, ok := xmlnsPrefix(a.Name); ok {
			if prefix == "" {
				b.WriteString("xmlns")
			} else {
				b.WriteString("xmlns:")
				b.WriteString(prefix)
			}
		} else {
			if i < len(el.AttrPrefixes) {
				if el.AttrPrefixes[i] != "" {
					b.WriteString(el.AttrPrefixes[i] + ":")
				}
				b.WriteString(a.Name.Local)
			} else {
				b.WriteString(qualifiedName(a.Name, ns))
			}
		}
		b.WriteString(`="`)
		if err := xml.EscapeText(b, []byte(a.Value)); err != nil {
			return err
		}
		b.WriteByte('"')
	}
	if len(el.Children) == 0 && el.Text == "" && len(el.Content) == 0 {
		b.WriteString("/>")
		return nil
	}
	b.WriteByte('>')
	if len(el.Content) == 0 {
		if err := xml.EscapeText(b, []byte(el.Text)); err != nil {
			return err
		}
		for _, c := range el.Children {
			if err := writeElement(b, c, ns); err != nil {
				return err
			}
		}
	} else {
		kept := map[*Element]bool{}
		for _, c := range el.Children {
			kept[c] = true
		}
		for _, c := range el.Content {
			if c.Element != nil {
				if kept[c.Element] {
					if err := writeElement(b, c.Element, ns); err != nil {
						return err
					}
				}
			} else if err := writeContent(b, c); err != nil {
				return err
			}
		}
	}
	b.WriteString("</")
	b.WriteString(elementQName(el, ns))
	b.WriteByte('>')
	return nil
}

func xmlnsPrefix(name xml.Name) (string, bool) {
	switch {
	case name.Space == "xmlns" || name.Space == xmlNamespaceURI:
		return name.Local, true
	case name.Local == "xmlns" && name.Space == "":
		return "", true
	default:
		return "", false
	}
}

func qualifiedName(name xml.Name, ns map[string]string) string {
	if name.Space == "" || name.Space == "xmlns" || name.Space == xmlNamespaceURI {
		return name.Local
	}
	if prefix, ok := ns[name.Space]; ok {
		if prefix == "" {
			return name.Local
		}
		return prefix + ":" + name.Local
	}
	return name.Local
}

// CountElements returns the number of element nodes in doc, including root.
func CountElements(doc Document) int {
	return countElements(doc.Root)
}

func countElements(el *Element) int {
	if el == nil {
		return 0
	}
	n := 1
	for _, c := range el.Children {
		n += countElements(c)
	}
	return n
}

func localNameEq(el *Element, name string) bool {
	return el != nil && strings.EqualFold(el.Name.Local, name)
}

func hasChildLocal(el *Element, name string) bool {
	if el == nil {
		return false
	}
	for _, c := range el.Children {
		if strings.EqualFold(c.Name.Local, name) {
			return true
		}
	}
	return false
}

func hasDescendantLocal(el *Element, name string) bool {
	if el == nil {
		return false
	}
	if strings.EqualFold(el.Name.Local, name) {
		return true
	}
	for _, c := range el.Children {
		if hasDescendantLocal(c, name) {
			return true
		}
	}
	return false
}

func hasNonEmptyText(el *Element) bool {
	if el == nil {
		return false
	}
	if strings.TrimSpace(el.Text) != "" {
		return true
	}
	for _, c := range el.Children {
		if hasNonEmptyText(c) {
			return true
		}
	}
	return false
}

func boolAttr(el *Element, local string) bool {
	if el == nil {
		return false
	}
	for _, a := range el.Attr {
		if !strings.EqualFold(a.Name.Local, local) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(a.Value)) {
		case "true", "1", "yes":
			return true
		}
	}
	return false
}

func attrLocal(el *Element, local string) string {
	if el == nil {
		return ""
	}
	for _, a := range el.Attr {
		if strings.EqualFold(a.Name.Local, local) {
			return a.Value
		}
	}
	return ""
}

func elementQName(el *Element, ns map[string]string) string {
	if el.QName != "" {
		return el.QName
	}
	if el.Prefix != "" {
		return el.Prefix + ":" + el.Name.Local
	}
	return qualifiedName(el.Name, ns)
}
func writeContent(b *strings.Builder, c XMLContent) error {
	switch c.Kind {
	case "text":
		return xml.EscapeText(b, []byte(c.Text))
	case "comment":
		if strings.Contains(c.Text, "--") || strings.HasSuffix(c.Text, "-") {
			return errors.New("transform: invalid XML comment")
		}
		b.WriteString("<!--" + c.Text + "-->")
	case "pi":
		if !ValidXMLLocalName(c.Target) || strings.EqualFold(c.Target, "xml") || strings.Contains(c.Text, "?>") {
			return errors.New("transform: invalid XML processing instruction")
		}
		b.WriteString("<?" + c.Target + " " + c.Text + "?>")
	}
	return nil
}
