//go:build js && wasm

// Command hookway-wasm binds Hookway's config validation and schema to browser
// globals so the config UI runs the real Go validator client-side. It holds no
// logic of its own: every call delegates to cmd/hookway-wasm/bridge, which
// wraps internal/configapi and internal/config.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/jasongiroux/hookway/cmd/hookway-wasm/bridge"
)

// errJSON renders an internal failure in a shape the JS side can detect via a
// top-level "error" key (distinct from a validation response's "issues").
func errJSON(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

func validate(_ js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return errJSON("hookwayValidate: expected 1 string argument (yaml)")
	}
	out, err := bridge.Validate(args[0].String())
	if err != nil {
		return errJSON(err.Error())
	}
	return out
}

func schema(_ js.Value, _ []js.Value) any {
	out, err := bridge.Schema()
	if err != nil {
		return errJSON(err.Error())
	}
	return out
}

func nodeSchemas(_ js.Value, _ []js.Value) any {
	out, err := bridge.NodeSchemas()
	if err != nil {
		return errJSON(err.Error())
	}
	return out
}

func main() {
	js.Global().Set("hookwayValidate", js.FuncOf(validate))
	js.Global().Set("hookwaySchema", js.FuncOf(schema))
	js.Global().Set("hookwayNodeSchemas", js.FuncOf(nodeSchemas))
	select {} // block forever so the exported functions stay callable
}
