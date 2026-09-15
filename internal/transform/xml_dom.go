package transform

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"strings"
)

const xmlNamespaceURI = "http://www.w3.org/2000/xmlns/"

// Document is a cloneable XML tree stored on graph.Envelope.Body. Raw request
// bytes stay on Verified; this tree is the post-auth rewrite surface.
type Document struct {
	Root        *Element
	Declaration string
}

// Element is one XML element. Children are child elements in document order.
// Text is concatenated direct character data, not descendant text.
type Element struct {
	Name     xml.Name
	Attr     []xml.Attr
	Children []*Element
	Text     string
}

func (d Document) CloneBody() (any, error) {
	return d.Clone(), nil
}

func (d Document) Clone() Document {
	out := Document{Declaration: d.Declaration}
	if d.Root != nil {
		out.Root = d.Root.clone()
	}
	return out
}

func (e *Element) clone() *Element {
	if e == nil {
		return nil
	}
	out := &Element{Name: e.Name, Text: e.Text, Attr: append([]xml.Attr(nil), e.Attr...)}
	if len(e.Children) > 0 {
		out.Children = make([]*Element, len(e.Children))
		for i, c := range e.Children {
			out.Children[i] = c.clone()
		}
	}
	return out
}

// ParseXML builds a Document from bytes. Non-UTF-8 charset declarations fail
// closed rather than silently corrupting the tree.
func ParseXML(data []byte) (Document, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = utf8CharsetReader

	var (
		doc   Document
		stack []*Element
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Document{}, fmt.Errorf("transform: xml parse: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			t = t.Copy()
			el := &Element{Name: t.Name, Attr: t.Attr}
			if len(stack) == 0 {
				doc.Root = el
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, el)
			}
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) == 0 {
				return Document{}, fmt.Errorf("transform: xml parse: unmatched end element %s", t.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			stack[len(stack)-1].Text += string(bytes.Clone(t))
		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") && doc.Declaration == "" {
				doc.Declaration = "<?xml " + string(t.Inst) + "?>"
			}
		}
	}
	if doc.Root == nil {
		return Document{}, fmt.Errorf("transform: xml parse: empty document")
	}
	if len(stack) != 0 {
		return Document{}, fmt.Errorf("transform: xml parse: unclosed element %s", stack[len(stack)-1].Name.Local)
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
	if err := writeElement(&b, doc.Root, nil); err != nil {
		return nil, err
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
	b.WriteString(qualifiedName(el.Name, ns))
	for _, a := range el.Attr {
		b.WriteByte(' ')
		if prefix, ok := xmlnsPrefix(a.Name); ok {
			if prefix == "" {
				b.WriteString("xmlns")
			} else {
				b.WriteString("xmlns:")
				b.WriteString(prefix)
			}
		} else {
			b.WriteString(qualifiedName(a.Name, ns))
		}
		b.WriteString(`="`)
		if err := xml.EscapeText(b, []byte(a.Value)); err != nil {
			return err
		}
		b.WriteByte('"')
	}
	if len(el.Children) == 0 && el.Text == "" {
		b.WriteString("/>")
		return nil
	}
	b.WriteByte('>')
	if err := xml.EscapeText(b, []byte(el.Text)); err != nil {
		return err
	}
	for _, c := range el.Children {
		if err := writeElement(b, c, ns); err != nil {
			return err
		}
	}
	b.WriteString("</")
	b.WriteString(qualifiedName(el.Name, ns))
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
