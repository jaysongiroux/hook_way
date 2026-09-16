// Package configapi exposes Hookway config validation and schema as UI-facing
// JSON. It adds no validation logic — it wraps internal/config and
// internal/flowconfig so a client (the config builder, the WASM validator,
// scripts) gets a stable JSON contract.
package configapi

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/flowconfig"
	"gopkg.in/yaml.v3"
)

// Issue is one validation problem, anchored to a dotted config path.
type Issue struct {
	Field      string `json:"field"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
	Severity   string `json:"severity"`
}

// ValidateResponse is the /validate reply. An empty Issues array means valid.
type ValidateResponse struct {
	Issues []Issue `json:"issues"`
}

// Validate parses and validates raw YAML config bytes, returning structured
// issues. Policy lint may emit "warning"; everything else is "error".
//
// This is the function internal/editor's POST /validate calls. It must NEVER
// expand ${VAR} references against the process environment: the editor
// accepts candidate config from an untrusted HTTP client, and expanding env
// here would let that client fish for server-side secrets/URLs by observing
// which fields end up populated. It delegates to flowconfig.ValidateBytes
// (non-expanding) — do not route this through ValidateWithEnv(..., true).
func Validate(data []byte) ValidateResponse {
	return preserveSeverity(flowconfig.ValidateBytes(data))
}

// ValidateWithEnv validates raw YAML config bytes like Validate, but when
// expand is true it first expands ${VAR}/$VAR references against the process
// environment (the same expansion config.Load uses for the running server) so
// `validate --json` matches plain `validate`'s view of the config. Unlike
// config.Load, a referenced-but-unset variable does not abort validation here:
// per config.ExpandEnvLenient, it is left as the opaque literal "${NAME}"
// (not emptied) so every other structural/required-field check still passes
// as if the reference had resolved, and is separately reported as a
// "config.env.<NAME>" issue at severity "warning" rather than "error",
// because CI commonly runs config validation without production secrets set.
//
// expand=false skips expansion entirely (pure syntax/structure check,
// equivalent to Validate) — this is the `--no-env` CLI flag's behavior for
// the JSON path.
//
// This is a CLI-facing helper, not a general "expand untrusted bytes"
// function: only call it with server-trusted input (a config file path
// resolved from --config/HOOKWAY_CONFIG), never with client-supplied bytes —
// that guarantee is what keeps the editor's Validate() above safe from env
// exfiltration.
func ValidateWithEnv(data []byte, expand bool) ValidateResponse {
	if !expand {
		return Validate(data)
	}
	expanded, missing := config.ExpandEnvLenient(string(data))
	out := ValidateResponse{Issues: []Issue{}}
	for _, iss := range config.MissingEnvIssues(missing) {
		out.Issues = append(out.Issues, Issue{
			Field:      iss.Field,
			Message:    iss.Message,
			Suggestion: iss.Suggestion,
			Severity:   "warning",
		})
	}
	structural := preserveSeverity(flowconfig.ValidateBytes([]byte(expanded)))

	var parsed any
	canCorrelate := yaml.Unmarshal([]byte(expanded), &parsed) == nil
	for _, iss := range structural.Issues {
		if canCorrelate && fieldStillHasMissingRef(parsed, iss.Field) {
			iss.Severity = "warning"
		}
		out.Issues = append(out.Issues, iss)
	}
	return out
}

// fieldStillHasMissingRef reports whether the value at the dotted/bracketed
// config path field (e.g. "destinations.forward.webhook.url" or
// "security.trusted_proxies[0]") — as found in the generically-parsed,
// lenient-expanded YAML tree root — still literally contains an unresolved
// "${" placeholder. It is used only to decide whether a structural issue on
// that field was caused by a missing env var (see ValidateWithEnv); it is not
// itself a validation rule.
func fieldStillHasMissingRef(root any, field string) bool {
	v, ok := lookupFieldPath(root, field)
	if !ok {
		return false
	}
	return containsMissingRef(v)
}

func containsMissingRef(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, "${")
	case map[string]any:
		for _, vv := range t {
			if containsMissingRef(vv) {
				return true
			}
		}
	case []any:
		for _, vv := range t {
			if containsMissingRef(vv) {
				return true
			}
		}
	}
	return false
}

// lookupFieldPath walks a dotted config field path (as used in
// ValidationIssue.Field), with optional "[N]" array indices per segment,
// against a generically-yaml-unmarshaled tree (map[string]any /
// []any / scalars). It returns (nil, false) if any segment doesn't resolve,
// which is expected and harmless for paths that don't correspond directly to
// a single scalar (e.g. bare "destinations").
func lookupFieldPath(root any, field string) (any, bool) {
	cur := root
	for _, seg := range strings.Split(field, ".") {
		name, idx, hasIdx := parseFieldSegment(seg)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := m[name]
		if !ok {
			return nil, false
		}
		cur = next
		if hasIdx {
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
		}
	}
	return cur, true
}

// parseFieldSegment splits a single path segment like "trusted_proxies[0]"
// into its map key ("trusted_proxies") and array index (0, true). A segment
// with no "[N]" suffix returns hasIdx=false.
func parseFieldSegment(seg string) (name string, idx int, hasIdx bool) {
	if i := strings.IndexByte(seg, '['); i >= 0 && strings.HasSuffix(seg, "]") {
		if n, err := strconv.Atoi(seg[i+1 : len(seg)-1]); err == nil {
			return seg[:i], n, true
		}
	}
	return seg, 0, false
}

// errorSeverity wraps raw config issues as JSON Issues, all at severity
// "error".
func preserveSeverity(issues []config.ValidationIssue) ValidateResponse {
	out := ValidateResponse{Issues: []Issue{}}
	for _, iss := range issues {
		severity := iss.Severity
		if severity == "" {
			severity = "error"
		}
		out.Issues = append(out.Issues, Issue{
			Field:      iss.Field,
			Message:    iss.Message,
			Suggestion: iss.Suggestion,
			Severity:   severity,
		})
	}
	return out
}


// Schema returns the JSON Schema describing the config YAML.
func Schema() ([]byte, error) {
	return config.SchemaJSON()
}

// NodeSchemas returns the per-node option schemas used by the config UI.
func NodeSchemas() ([]byte, error) {
	return json.MarshalIndent(flowconfig.NodeSchemas(), "", "  ")
}
