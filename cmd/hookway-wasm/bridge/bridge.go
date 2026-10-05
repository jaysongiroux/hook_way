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
	"github.com/jaysongiroux/hook_way/internal/uicontract"
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
	resp := replayconfigapi.Validate([]byte(yaml))
	b, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReplaySchema returns the replay service's on-disk YAML shape.
func ReplaySchema() (string, error) {
	b, err := replayconfigapi.Schema()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ContractInfo identifies the exact browser bridge contract and implemented
// capabilities. Keep this sourced from uicontract, also used by editor HTTP.
func ContractInfo() (string, error) {
	b, err := json.Marshal(uicontract.Info())
	return string(b), err
}

// ReplayActionValidate checks retry and resolution inputs without consulting secrets,
// authentication, or replay store state. The API is still the final authority.
func ReplayActionValidate(raw string) (string, error) {
	var input uicontract.ReplayActionInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
	}
	b, err := json.Marshal(uicontract.ValidateReplayAction(input))
	return string(b), err
}

// JSONSampleValidate runs the same pure validation contract as the editor API.
func JSONSampleValidate(raw string) (string, error) {
	input, err := uicontract.DecodeJSONSampleInput([]byte(raw))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(uicontract.ValidateJSONSample(input))
	return string(b), err
}
