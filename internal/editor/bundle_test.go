package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jaysongiroux/hook_way/cmd/hookway-wasm/bridge"
	"github.com/jaysongiroux/hook_way/internal/configapi"
)

func TestBundleHTTPWASMAndGoParity(t *testing.T) {
	input := configapi.BundleInput{Entry: "core.yaml", Files: map[string]string{}}
	for _, name := range []string{"core.yaml", "flows/orders.yaml", "flows/billing.yaml"} {
		content, err := os.ReadFile(filepath.Clean("../../examples/multi-yaml/" + name))
		if err != nil {
			t.Fatal(err)
		}
		input.Files[name] = string(content)
	}
	// A missing flow produces a useful file-scoped issue through all transports.
	delete(input.Files, "flows/billing.yaml")
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := configapi.ValidateBundle(input)
	wasm, err := bridge.ValidateBundle(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var fromWASM configapi.ValidateResponse
	if err := json.Unmarshal([]byte(wasm), &fromWASM); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "POST", "/validate-bundle", bytes.NewReader(raw)))
	var fromHTTP configapi.ValidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fromHTTP); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !reflect.DeepEqual(expected, fromWASM) || !reflect.DeepEqual(expected, fromHTTP) {
		t.Fatalf("parity: %+v %+v %+v", expected, fromWASM, fromHTTP)
	}
	for _, raw := range []string{`{}`, `{"entry":"core.yaml","files":{},"extra":true}`, `null`} {
		rec = httptest.NewRecorder()
		Handler("").ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "POST", "/validate-bundle", bytes.NewBufferString(raw)))
		if rec.Code != 400 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
}
