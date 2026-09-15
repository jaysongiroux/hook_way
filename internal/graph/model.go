package graph

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// FlowConfig is one entry under the top-level flows mapping.
type FlowConfig struct {
	Nodes []NodeConfig `yaml:"nodes"`
	Edges []string     `yaml:"edges"`
	// DeliveryFailurePolicy is stop_on_failure (default) or skip_failed.
	// It applies when a node fans out to destination (Detached) successors.
	DeliveryFailurePolicy string `yaml:"delivery_failure_policy,omitempty"`
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
func validID(s string) bool {
	return s != "" && !strings.ContainsAny(s, ":>") && strings.IndexFunc(s, unicode.IsSpace) < 0
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

// UnmarshalYAML separates node identity from type-specific options. Missing
// identity is diagnosed by Compile so independent problems can be aggregated.
func (nc *NodeConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("graph: node at line %d must be a mapping", value.Line)
	}
	var head struct {
		ID   string `yaml:"id"`
		Type string `yaml:"type"`
	}
	if err := value.Decode(&head); err != nil {
		return err
	}
	nc.ID, nc.Type = head.ID, head.Type
	nc.Options = *value
	nc.Options.Content = nil
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i]
		if key.Value != "id" && key.Value != "type" {
			nc.Options.Content = append(nc.Options.Content, key, value.Content[i+1])
		}
	}
	return nil
}
