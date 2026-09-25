// Package bridge exposes Hookway config validation and schema as JSON strings.
// It is platform-independent (no syscall/js) so it is unit-testable on the host
// and reused verbatim by the WASM entrypoint. It adds NO validation logic — it
// is a faithful pass-through of internal/configapi, so an in-browser result is
// byte-identical to the CLI's (and to the HTTP editor, which shares configapi).
package bridge

import (
	"encoding/json"

	"github.com/jaysongiroux/hook_way/internal/configapi"
	replayconfigapi "github.com/jaysongiroux/hook_way/internal/replay/configapi"
)

// Validate validates raw YAML config and returns the JSON encoding of
// configapi.Validate's response ({"issues":[...]}). It uses the non-env-
// expanding path, so ${VAR} references are treated as opaque literals — this is
// mandatory because browser-supplied bytes are untrusted. The returned error is
// non-nil only if JSON marshalling fails; invalid config is reported as issues,
// not as an error.
func Validate(yaml string) (string, error) {
	resp := configapi.Validate([]byte(yaml))
	b, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Schema returns the JSON Schema describing the Hookway config. It routes
// through configapi.Schema (the same entry point the HTTP editor uses) rather
// than config.SchemaJSON directly, so the browser and server share one schema
// source of truth. The browser renders forms from it.
func Schema() (string, error) {
	b, err := configapi.Schema()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// NodeSchemas returns the per-node option schemas (the same document as
// `hookway schema --nodes`).
func NodeSchemas() (string, error) {
	b, err := configapi.NodeSchemas()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReplayValidate validates replay.yaml without expanding environment
// references against the browser or editor host.
func ReplayValidate(yaml string) (string, error) {
	return replayconfigapi.ValidateJSON([]byte(yaml))
}

// ReplaySchema returns the replay service's on-disk YAML shape.
func ReplaySchema() (string, error) {
	b, err := replayconfigapi.Schema()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
