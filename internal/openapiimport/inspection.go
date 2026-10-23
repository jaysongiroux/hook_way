package openapiimport

import (
	"encoding/json"
	"github.com/jaysongiroux/hook_way/internal/config"
)

// SecurityRequirement describes one member of an AND group. Outer groups are OR alternatives.
type SecurityRequirement struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	In     string   `json:"in,omitempty"`
	Header string   `json:"header,omitempty"`
	Scheme string   `json:"scheme,omitempty"`
	Scopes []string `json:"scopes"`
}

func securityRequirements(root map[string]any, security any) [][]SecurityRequirement {
	out := [][]SecurityRequirement{}
	list, _ := security.([]any)
	for _, entry := range list {
		group := []SecurityRequirement{}
		for _, name := range keys(object(entry)) {
			scheme, _ := resolve(root, object(object(root["components"])["securitySchemes"])[name])
			scopes := []string{}
			if values, ok := object(entry)[name].([]any); ok {
				for _, v := range values {
					scopes = append(scopes, text(v))
				}
			}
			group = append(group, SecurityRequirement{Name: name, Type: text(scheme["type"]), In: text(scheme["in"]), Header: text(scheme["name"]), Scheme: text(scheme["scheme"]), Scopes: scopes})
		}
		out = append(out, group)
	}
	return out
}
func operationDescription(op map[string]any, fallback string) string {
	for _, key := range []string{"summary", "description"} {
		if value := text(op[key]); value != "" {
			return value
		}
	}
	return fallback
}
func schemaText(value any) string { b, _ := json.Marshal(value); return string(b) }

// SettingsSchema describes the YAML settings shape using the same Go types as the CLI.
func SettingsSchema() ([]byte, error) {
	r := config.NewSchemaReflector()
	r.DoNotReference = true
	return json.MarshalIndent(r.Reflect(&Settings{}), "", "  ")
}
