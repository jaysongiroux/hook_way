package flowconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

// EventDeliveryConfig enables durable webhook subscriptions. It contains references,
// never caller credentials. Native runtime resources are opened only when serving.
type EventDeliveryConfig struct {
	PostgresURL      string           `yaml:"postgres_url" json:"postgres_url" jsonschema:"required"`
	Namespace        string           `yaml:"namespace" json:"namespace" jsonschema:"required"`
	EncryptionKey    SecretRefOptions `yaml:"encryption_key" json:"encryption_key" jsonschema:"required"`
	Workers          int              `yaml:"workers,omitempty" json:"workers,omitempty" jsonschema:"minimum=1,maximum=64"`
	MaxSubscriptions int              `yaml:"max_subscriptions,omitempty" json:"max_subscriptions,omitempty" jsonschema:"minimum=1"`
	MaxPending       int              `yaml:"max_pending,omitempty" json:"max_pending,omitempty" jsonschema:"minimum=1"`
	MaxBytes         int64            `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty" jsonschema:"minimum=262144"`
}

func (c EventDeliveryConfig) Defaults() EventDeliveryConfig {
	if c.Workers == 0 {
		c.Workers = 4
	}
	if c.MaxSubscriptions == 0 {
		c.MaxSubscriptions = 1000
	}
	if c.MaxPending == 0 {
		c.MaxPending = 10000
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 256 << 20
	}
	return c
}
func (c EventDeliveryConfig) Validate() error {
	if strings.TrimSpace(c.PostgresURL) == "" || !config.IsSafeName(c.Namespace) {
		return errors.New("event_delivery requires postgres_url and a safe namespace")
	}
	if err := validateCredentialSecret(c.EncryptionKey); err != nil {
		return err
	}
	if c.EncryptionKey.Refresh != "" || c.EncryptionKey.Overlap != "" {
		return errors.New("event encryption key must remain stable; refresh and overlap are unsupported")
	}
	c = c.Defaults()
	if c.Workers < 1 || c.Workers > 64 || c.MaxSubscriptions < 1 || c.MaxSubscriptions > 100000 || c.MaxPending < 1 || c.MaxPending > 1000000 || c.MaxBytes < 262144 || c.MaxBytes > 1<<40 {
		return errors.New("invalid event_delivery limits")
	}
	return nil
}

type MCPEventOptions struct {
	Event       string            `yaml:"event" json:"event" jsonschema:"required"`
	InputSchema map[string]any    `yaml:"input_schema" json:"input_schema" jsonschema:"required"`
	Filters     map[string]string `yaml:"filters,omitempty" json:"filters,omitempty"`
	Access      MCPAccessOptions  `yaml:"access" json:"access" jsonschema:"required"`
}

// EventPrincipal is a verified, expiring authorization snapshot; no bearer token.
type EventPrincipal struct {
	Audience      string
	Issuer        string
	ID            string
	Key           string
	Scopes, Roles []string
	ExpiresAt     time.Time
}
type EventRPCError struct {
	Code    int
	Message string
	Reason  string
}

func (e *EventRPCError) Error() string { return e.Message }

type EventRuntime interface {
	Ready(context.Context) error
	EventCall(context.Context, string, string, EventPrincipal, json.RawMessage) (any, error)
	BindEventAuthorizer(string, func(context.Context, EventPrincipal) bool)
}

func EventAllowed(p EventPrincipal, a MCPAccessOptions) bool {
	if p.ID == "" || a.Anonymous || (!p.ExpiresAt.IsZero() && !time.Now().Before(p.ExpiresAt)) {
		return false
	}
	if p.Key != "" {
		return slices.Contains(a.APIKeys, p.Key)
	}
	if len(a.Scopes)+len(a.Roles) == 0 {
		return false
	}
	for _, s := range a.Scopes {
		if !slices.Contains(p.Scopes, s) {
			return false
		}
	}
	if len(a.Roles) > 0 {
		for _, r := range a.Roles {
			if slices.Contains(p.Roles, r) {
				return true
			}
		}
		return false
	}
	return true
}
func validateMCPEvents(o MCPTriggerOptions) error {
	if len(o.Events) > 64 {
		return errors.New("at most 64 MCP events are supported")
	}
	seen := map[string]bool{}
	for _, e := range o.Events {
		if seen[e.Event] {
			return errors.New("duplicate MCP event")
		}
		seen[e.Event] = true
		if !config.IsSafeName(e.Event) || e.InputSchema["type"] != "object" || e.InputSchema["additionalProperties"] != false {
			return errors.New("event input_schema must be an object with additionalProperties: false")
		}
		for _, key := range []string{"$ref", "$dynamicRef", "patternProperties", "allOf", "anyOf", "oneOf", "if", "then", "else", "dependentSchemas"} {
			if _, exists := e.InputSchema[key]; exists {
				return errors.New("event argument schemas require explicit scalar properties")
			}
		}
		if _, err := CompileMCPSchema(e.InputSchema); err != nil {
			return err
		}
		props, _ := e.InputSchema["properties"].(map[string]any)
		if len(props) > 32 || len(props) != len(e.Filters) {
			return errors.New("each event argument requires one filter binding (maximum 32)")
		}
		for k, v := range props {
			shape, _ := v.(map[string]any)
			t, _ := shape["type"].(string)
			if !slices.Contains([]string{"string", "number", "integer", "boolean"}, t) || !validSelectionPath(e.Filters[k]) {
				return errors.New("event filters require scalar argument schemas and bounded dotted selectors")
			}
		}
		a := e.Access
		if a.Anonymous || a.AllowMutation || o.Auth.NoAuth || len(a.APIKeys)+len(a.Scopes)+len(a.Roles) == 0 || !validMCPScopes(a.Scopes) || len(a.Roles) > 64 || len(a.APIKeys) > 64 {
			return errors.New("event subscriptions require explicit authenticated grants")
		}
		for _, r := range a.Roles {
			if r == "" || len(r) > 256 {
				return errors.New("invalid event role")
			}
		}
		for _, k := range a.APIKeys {
			if _, ok := o.Auth.APIKeys[k]; !ok {
				return errors.New("event names unknown API key")
			}
		}
		if len(a.Scopes)+len(a.Roles) > 0 && o.Auth.OAuth == nil {
			return errors.New("event scope/role grants require OAuth")
		}
	}
	return nil
}
func EventDeliveryIssues(c Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	if c.EventDelivery != nil {
		if err := c.EventDelivery.Validate(); err != nil {
			issues = append(issues, cfgIssue("event_delivery", err.Error(), ""))
		}
	}
	endpoints := map[string]MCPTriggerOptions{}
	for name, f := range c.Flows {
		if o, ok := MCPOptions(f); ok {
			endpoints[name] = o
			for _, e := range o.Events {
				if _, ok := c.Events[e.Event]; !ok {
					issues = append(issues, cfgIssue("flows."+name, "MCP event is not defined: "+e.Event, ""))
				}
				if c.EventDelivery == nil {
					issues = append(issues, cfgIssue("event_delivery", "MCP events require event_delivery", ""))
				}
			}
		}
	}
	for id, d := range c.Destinations {
		if d.Type != "mcp_events" {
			continue
		}
		o, ok := endpoints[d.MCPEvents.Endpoint]
		if !ok || len(o.Events) == 0 || c.EventDelivery == nil {
			issues = append(issues, cfgIssue("destinations."+id, "mcp_events requires event_delivery and an MCP endpoint exposing events", ""))
		}
	}
	for name, f := range c.Flows {
		for _, n := range ExpandedNodes(f) {
			if n.Type != "publish" {
				continue
			}
			o, err := decodeOptions[PublishOptions](n)
			if err != nil {
				continue
			}
			d := c.Destinations[o.Destination]
			if d.Type != "mcp_events" {
				continue
			}
			exposed := false
			for _, e := range endpoints[d.MCPEvents.Endpoint].Events {
				exposed = exposed || e.Event == o.Target
			}
			if !exposed {
				issues = append(issues, cfgIssue(fmt.Sprintf("flows.%s.nodes.%s.target", name, n.ID), "target must be a literal event exposed by the destination's MCP endpoint", ""))
			}
			if o.Create || o.FIFO != nil || o.Retry != nil || o.SQS != nil || o.SNS != nil || o.Temporal != nil || o.GroupID != nil || len(o.MessageAttributes) > 0 || len(o.ResourceAttributes) > 0 || o.Payload == "object" || (o.WireBody != "" && o.WireBody != "json") {
				issues = append(issues, cfgIssue("flows."+name+".nodes."+n.ID, "mcp_events does not accept broker, object, or alternate wire options", ""))
			}
		}
	}
	return issues
}
