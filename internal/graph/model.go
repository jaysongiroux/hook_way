package graph

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// FlowConfig is one entry under the top-level flows mapping.
type FlowConfig struct {
	Responses map[string]ResponsePolicy `yaml:"responses,omitempty"`
	Nodes     []NodeConfig              `yaml:"nodes"`
	Edges     []string                  `yaml:"edges"`
	// DeliveryFailurePolicy is stop_on_failure (default) or skip_failed.
	// It applies when a node fans out to destination (Detached) successors.
	DeliveryFailurePolicy string `yaml:"delivery_failure_policy,omitempty"`
	// CaptureFailures set to false turns failure capture off for this flow.
	// Nil means true. It matters only when the replay block is configured.
	CaptureFailures *bool `yaml:"capture_failures,omitempty"`
}

// CapturesFailures reports whether failures in this flow are captured.
func (fc FlowConfig) CapturesFailures() bool {
	return fc.CaptureFailures == nil || *fc.CaptureFailures
}

// Edge carries an optional output label (switch ports, or the reserved
// on_error port). Unlabelled edges are ordinary fan-out.
type Edge struct{ From, FromPort, To string }

func (e Edge) String() string {
	if e.FromPort == "" {
		return e.From + ">" + e.To
	}
	return e.From + ":" + e.FromPort + ">" + e.To
}

// maxIDLen bounds a node id. Ids travel in capture metadata and redrive
// results, which the replay service stores as printable text of at most 1024
// bytes; an id outside that would make every failure at the node unstorable.
const maxIDLen = 1024

// ValidID reports whether s can name a node or a flow: non-empty printable
// UTF-8 of at most 1024 bytes, with no whitespace, ':' or '>'.
func ValidID(s string) bool { return validID(s) }

func validID(s string) bool {
	return s != "" && len(s) <= maxIDLen && utf8.ValidString(s) && !strings.ContainsAny(s, ":>") &&
		strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func ParseEdge(s string) (Edge, error) {
	from, to, found := strings.Cut(strings.TrimSpace(s), ">")
	if !found {
		return Edge{}, fmt.Errorf("graph: edge %q is missing >", s)
	}
	if strings.Contains(to, ">") {
		return Edge{}, fmt.Errorf("graph: edge %q has more than one >", s)
	}
	e := Edge{From: strings.TrimSpace(from), To: strings.TrimSpace(to)}
	if name, port, label := strings.Cut(e.From, ":"); label {
		e.From = strings.TrimSpace(name)
		e.FromPort = strings.TrimSpace(port)
		if !validID(e.FromPort) {
			return Edge{}, fmt.Errorf("graph: edge %q has invalid output port", s)
		}
	}
	if !validID(e.From) || !validID(e.To) {
		return Edge{}, fmt.Errorf("graph: edge %q has empty or invalid endpoint", s)
	}
	return e, nil
}

// ParseEdges returns valid edges plus every indexed parse error, allowing the
// compiler to diagnose those edges as well as independent node problems.
func ParseEdges(ss []string) ([]Edge, error) {
	out := make([]Edge, 0, len(ss))
	var errs []error
	for i, s := range ss {
		e, err := ParseEdge(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("edges[%d]: %w", i, err))
			continue
		}
		out = append(out, e)
	}
	return out, errors.Join(errs...)
}

// identityKeys are the node fields every type shares. They are decoded here
// and withheld from Options, because each NodeSpec decodes its options with
// KnownFields(true) and would reject them as unknown.
var identityKeys = map[string]bool{"id": true, "type": true, "description": true}

// UnmarshalYAML separates node identity from type-specific options. Missing
// identity is diagnosed by Compile so independent problems can be aggregated.
func (nc *NodeConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("graph: node at line %d must be a mapping", value.Line)
	}
	var head struct {
		ID          string `yaml:"id"`
		Type        string `yaml:"type"`
		Description string `yaml:"description"`
	}
	if err := value.Decode(&head); err != nil {
		return err
	}
	nc.ID, nc.Type, nc.Description = head.ID, head.Type, head.Description
	nc.Options = *value
	nc.Options.Content = nil
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i]
		if !identityKeys[key.Value] {
			nc.Options.Content = append(nc.Options.Content, key, value.Content[i+1])
		}
	}
	return nil
}
