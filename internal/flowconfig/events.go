package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

// EventDefinition describes application data, independently of its destination.
type EventDefinition struct {
	Description   string               `yaml:"description" json:"description" jsonschema:"required"`
	PayloadSchema transform.JSONSchema `yaml:"payload_schema" json:"payload_schema" jsonschema:"required"`
}

type EventOptions struct {
	Event       string `yaml:"event" json:"event" jsonschema:"required"`
	EventIDPath string `yaml:"event_id_path,omitempty" json:"event_id_path,omitempty"`
}

// EventCatalog is immutable after construction and safe to share across flows.
// It contains no subscriptions, credentials, or delivery state.
type EventCatalog struct{ schemas map[string]*jsonschema.Schema }

func decodeEvents(node yaml.Node) (map[string]EventDefinition, []config.ValidationIssue) {
	definitions := map[string]EventDefinition{}
	var entries map[string]yaml.Node
	if node.Kind != yaml.MappingNode || node.Decode(&entries) != nil {
		return definitions, []config.ValidationIssue{cfgIssue("events", "must be a mapping of event names to definitions", "")}
	}
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(entries) {
		entry := entries[name]
		raw, err := yaml.Marshal(&entry)
		if err != nil {
			issues = append(issues, cfgIssue("events."+name, "invalid event definition", ""))
			continue
		}
		var definition EventDefinition
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if entry.Kind != yaml.MappingNode {
			err = errors.New("must be an event definition mapping")
		} else {
			err = decoder.Decode(&definition)
		}
		if err != nil {
			issues = append(issues, cfgIssue("events."+name, err.Error(), ""))
			continue
		}
		definitions[name] = definition
	}
	return definitions, issues
}

// CompileEventCatalog uses the same bounded, offline schema engine as MCP tools.
func CompileEventCatalog(definitions map[string]EventDefinition) (*EventCatalog, []config.ValidationIssue) {
	catalog := &EventCatalog{schemas: map[string]*jsonschema.Schema{}}
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(definitions) {
		definition := definitions[name]
		field := "events." + name
		if !graph.ValidID(name) {
			issues = append(issues, cfgIssue(field, "event name must be a valid named reference (printable, without whitespace, ':' or '>')", ""))
		}
		if strings.TrimSpace(definition.Description) == "" {
			issues = append(issues, cfgIssue(field+".description", "event description is required", ""))
		}
		if definition.PayloadSchema["type"] != "object" {
			issues = append(issues, cfgIssue(field+".payload_schema", "event payload_schema must declare type: object", ""))
			continue
		}
		schema, err := CompileMCPSchema(definition.PayloadSchema)
		if err != nil {
			issues = append(issues, cfgIssue(field+".payload_schema", err.Error(), ""))
			continue
		}
		catalog.schemas[name] = schema
	}
	return catalog, issues
}

func eventReferenceIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	for _, flow := range sortedKeys(cfg.Flows) {
		for _, node := range ExpandedNodes(cfg.Flows[flow]) {
			if node.Type != "event" {
				continue
			}
			o, err := decodeOptions[EventOptions](node)
			if err == nil && graph.ValidID(o.Event) {
				if _, exists := cfg.Events[o.Event]; !exists {
					issues = append(issues, cfgIssue("flows."+flow+".nodes."+node.ID+".event", "event is not defined in the events catalog", "define the event or select an existing definition"))
				}
			}
		}
	}
	return issues
}

func validateEventOptions(o EventOptions) error {
	if !graph.ValidID(o.Event) {
		return errors.New("event must reference a named event definition")
	}
	if o.EventIDPath != "" && !validSelectionPath(o.EventIDPath) {
		return errors.New("event_id_path must be a bounded dotted JSON path")
	}
	return nil
}

type eventNode struct {
	id     string
	opts   EventOptions
	schema *jsonschema.Schema
}

func newEventNode(id string, o EventOptions, catalog *EventCatalog, validateOnly bool) (graph.Node, error) {
	var schema *jsonschema.Schema
	if catalog != nil {
		schema = catalog.schemas[o.Event]
	}
	if schema == nil {
		if validateOnly {
			// Whole-document validation supplies a field-specific reference issue.
			return stubNode{name: "event validation only"}, nil
		}
		return nil, errors.New("event is not defined in the compiled event catalog")
	}
	return eventNode{id: id, opts: o, schema: schema}, nil
}

func (n eventNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	fail := func(message string) (graph.Payload, error) {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: errors.New(message)}
	}
	env, ok := in.(graph.Envelope)
	if !ok {
		return fail("event requires an envelope")
	}
	if env.BodyFormat != "" && env.BodyFormat != "json" {
		return fail("event requires a JSON object body")
	}
	raw, err := json.Marshal(env.Body)
	if err != nil {
		return fail("event requires a JSON object body")
	}
	// Decode a bounded copy, preserving JSON numbers and input ownership.
	var data map[string]any
	if err := DecodeMCPJSON(raw, &data); err != nil || data == nil {
		return fail("event requires a bounded JSON object body")
	}
	if err := n.schema.Validate(data); err != nil {
		return fail("event payload does not match its payload_schema")
	}
	id := ""
	if n.opts.EventIDPath != "" {
		value, _ := dotted(data, n.opts.EventIDPath)
		id, ok = value.(string)
		if !ok || strings.TrimSpace(id) == "" {
			return fail("event_id_path must resolve to a nonempty string")
		}
	} else {
		id = uuid.NewString()
	}
	body := map[string]any{"eventId": id, "name": n.opts.Event, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": data}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > MCPMaxBytes || jsonbudget.Check(encoded, jsonbudget.Limits{}) != nil {
		return fail("event exceeds JSON size limit")
	}
	env.Body, env.BodyFormat = body, "json"
	return env, nil
}
