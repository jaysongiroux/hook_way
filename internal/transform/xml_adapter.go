package transform

import (
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"
)

// XMLSelector matches an expanded name exactly, independent of document prefixes.
type XMLSelector struct {
	Local     string `yaml:"local" json:"local" jsonschema:"required"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

func (s XMLSelector) matches(e *Element) bool {
	return e != nil && e.Name.Local == s.Local && e.Name.Space == s.Namespace
}

// XMLAdapterOptions explicitly supplies schema-specific bindings. The legacy
// xml_prune operation retains its existing predicates and configuration names.
type XMLAdapterOptions struct {
	Link              XMLSelector `yaml:"link" json:"link"`
	Entity            XMLSelector `yaml:"entity" json:"entity"`
	ExtensionItem     XMLSelector `yaml:"extension_item" json:"extension_item"`
	ItemName          XMLSelector `yaml:"item_name" json:"item_name"`
	ItemValue         XMLSelector `yaml:"item_value" json:"item_value"`
	SequenceAttribute string      `yaml:"sequence_attribute" json:"sequence_attribute"`
	PlaceholderTexts  []string    `yaml:"placeholder_texts" json:"placeholder_texts"`
	OptionalItems     []string    `yaml:"optional_items" json:"optional_items"`
}

func (o XMLAdapterOptions) Validate() error {
	for _, s := range []XMLSelector{o.Link, o.Entity, o.ExtensionItem, o.ItemName, o.ItemValue} {
		if !ValidXMLLocalName(s.Local) || len(s.Namespace) > 2048 {
			return errors.New("XML adapter requires valid expanded-name selectors")
		}
	}
	if !ValidXMLLocalName(o.SequenceAttribute) {
		return errors.New("XML sequence_attribute must be an unqualified XML name")
	}
	if len(o.PlaceholderTexts) == 0 || len(o.PlaceholderTexts) > 128 || len(o.OptionalItems) == 0 || len(o.OptionalItems) > 128 {
		return errors.New("XML text and item sets require 1..128 values")
	}
	for _, v := range append(append([]string(nil), o.PlaceholderTexts...), o.OptionalItems...) {
		if len(v) > 4096 || strings.TrimSpace(v) == "" {
			return errors.New("XML adapter set values must be nonblank and at most 4096 bytes")
		}
	}
	return nil
}

// ValidXMLLocalName accepts one unqualified XML name without markup.
func ValidXMLLocalName(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	// Parse a neutral empty element to let the standard XML parser validate names.
	d := xml.NewDecoder(strings.NewReader("<" + s + "/>"))
	t, err := d.Token()
	if err != nil {
		return false
	}
	start, ok := t.(xml.StartElement)
	return ok && start.Name.Space == "" && start.Name.Local == s && len(start.Attr) == 0
}

// PruneOrdered applies the explicitly configured operations in their fixed
// order: empty nodes, cascade/resequence, placeholders/resequence, optional
// items, dangling links. Later removals never restart the earlier cascade.
func PruneOrdered(doc Document, o XMLAdapterOptions, log PruneLogFunc) (Document, error) {
	if err := o.Validate(); err != nil {
		return Document{}, err
	}
	if doc.Root == nil {
		return Document{}, errors.New("XML adapter: empty document")
	}
	out := doc.Clone()
	rules := []struct {
		name string
		run  func()
	}{
		{"empty_nodes", func() {
			// Snapshot pre-order so removing an empty child does not also remove its parent.
			var candidates []*Element
			visitXML(out.Root, func(e *Element) {
				if e != out.Root && !o.Link.matches(e) && len(e.Children) == 0 && len(e.Content) == 0 && e.Text == "" {
					candidates = append(candidates, e)
				}
			})
			targets := map[*Element]bool{}
			for _, e := range candidates {
				targets[e] = true
			}
			removeMatching(out.Root, func(e *Element) bool { return targets[e] })
		}},
		{"empty_cascade", func() {
			// Post-order is the fixed point: each parent's predicate sees retained children.
			var walk func(*Element, int)
			walk = func(e *Element, depth int) {
				kept := e.Children[:0]
				for _, c := range e.Children {
					walk(c, depth+1)
					if depth > 0 && !o.Link.matches(c) && len(c.Children) == 0 && strings.TrimSpace(c.Text) == "" {
						continue
					}
					kept = append(kept, c)
				}
				e.Children = kept
				e.syncContent()
			}
			walk(out.Root, 0)
			resequenceXML(out.Root, o)
		}},
		{"placeholder_entities", func() {
			removeMatching(out.Root, func(e *Element) bool {
				if !o.Entity.matches(e) {
					return false
				}
				all := true
				visitXML(e, func(n *Element) {
					for _, text := range directXMLTexts(n) {
						v := strings.TrimSpace(text)
						if v != "" && !stringIn(o.PlaceholderTexts, v) {
							all = false
						}
					}
				})
				return all
			})
			resequenceXML(out.Root, o)
		}},
		{"blank_optional_items", func() {
			removeMatching(out.Root, func(e *Element) bool {
				if !o.ExtensionItem.matches(e) {
					return false
				}
				name, value := "", ""
				for _, c := range e.Children {
					if o.ItemName.matches(c) {
						name = xmlText(c)
					}
					if o.ItemValue.matches(c) {
						value = xmlText(c)
					}
				}
				return stringIn(o.OptionalItems, strings.TrimSpace(name)) && strings.TrimSpace(value) == ""
			})
		}},
		{"dangling_links", func() {
			labels := map[string]bool{}
			visitXML(out.Root, func(e *Element) {
				for _, a := range e.Attr {
					if a.Name.Local == "label" && (a.Name.Space == "" || a.Name.Space == "http://www.w3.org/1999/xlink") && a.Value != "" {
						labels[a.Value] = true
					}
				}
			})
			removeMatching(out.Root, func(e *Element) bool {
				if !o.Link.matches(e) {
					return false
				}
				for _, a := range e.Attr {
					if (a.Name.Local == "from" || a.Name.Local == "to") && (a.Name.Space == "" || a.Name.Space == "http://www.w3.org/1999/xlink") && a.Value != "" && !labels[a.Value] {
						return true
					}
				}
				return false
			})
		}},
	}
	for i, r := range rules {
		before := CountElements(out)
		start := time.Now()
		r.run()
		if log != nil {
			log(r.name, i, before, CountElements(out), time.Since(start))
		}
	}
	return out, nil
}
func visitXML(e *Element, f func(*Element)) {
	if e == nil {
		return
	}
	f(e)
	for _, c := range e.Children {
		visitXML(c, f)
	}
}
func resequenceXML(root *Element, o XMLAdapterOptions) {
	i := 0
	visitXML(root, func(e *Element) {
		if !o.Entity.matches(e) {
			return
		}
		i++
		for j, a := range e.Attr {
			if a.Name.Space == "" && a.Name.Local == o.SequenceAttribute {
				e.Attr[j].Value = strconv.Itoa(i)
				return
			}
		}
		e.Attr = append(e.Attr, xml.Attr{Name: xml.Name{Local: o.SequenceAttribute}, Value: strconv.Itoa(i)})
		e.AttrPrefixes = append(e.AttrPrefixes, "")
	})
}
func directXMLTexts(e *Element) []string {
	if len(e.Content) == 0 {
		return []string{e.Text}
	}
	var out []string
	for _, c := range e.Content {
		if c.Kind == "text" {
			out = append(out, c.Text)
		}
	}
	return out
}
func xmlText(e *Element) string {
	var b strings.Builder
	retained := map[*Element]bool{}
	for _, child := range e.Children {
		retained[child] = true
	}
	for _, c := range e.Content {
		if c.Element != nil { // Use only retained children.
			if retained[c.Element] {
				b.WriteString(xmlText(c.Element))
			}
		} else if c.Kind == "text" {
			b.WriteString(c.Text)
		}
	}
	if len(e.Content) == 0 {
		b.WriteString(e.Text)
		for _, c := range e.Children {
			b.WriteString(xmlText(c))
		}
	}
	return b.String()
}
func stringIn(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
