package bridge

import (
	"bytes"
	"encoding/json"
	"github.com/jaysongiroux/hook_way/internal/openapiimport"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
	"os"
	"strings"
	"testing"
)

func TestOpenAPIParity(t *testing.T) {
	source, err := os.ReadFile("../../../examples/openapi/api.yaml")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile("../../../examples/openapi/settings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(uicontract.OpenAPIInspectInput{Document: string(source)})
	inspected, err := OpenAPIInspect(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(openapiimport.Inspect(source))
	if inspected != string(expected) {
		t.Fatal("inspection differs from native")
	}
	opts, err := openapiimport.DecodeSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	native := openapiimport.Generate(source, opts)
	if native.HasErrors() {
		t.Fatal(native.Diagnostics)
	}
	raw, _ = json.Marshal(uicontract.OpenAPIGenerateInput{Document: string(source), SettingsYAML: string(settings)})
	generated, err := OpenAPIGenerate(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var got uicontract.OpenAPIGenerateResult
	if err := json.Unmarshal([]byte(generated), &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(got.YAML), native.YAML) {
		t.Fatal("generation differs from CLI engine")
	}
	if strings.Contains(generated, "private-service-token") {
		t.Fatal("leaked credential")
	}
}
func TestOpenAPIInputsFailClosed(t *testing.T) {
	for _, raw := range []string{`{}`, `{"document":"openapi: 2.0"}`, `{"document":"x: ["}`, `{"document":"openapi: 3.1.0\ninfo: {title: X, version: '1'}\npaths: {}\ncomponents: {schemas: {X: {$ref: 'file:///etc/passwd'}}}"}`} {
		got, err := OpenAPIInspect(raw)
		if err != nil {
			t.Fatal(err)
		}
		var report openapiimport.Report
		if json.Unmarshal([]byte(got), &report) != nil || len(report.Diagnostics) == 0 {
			t.Fatalf("expected diagnostics: %s", got)
		}
	}
	for _, raw := range []string{`{"unknown":1}`, `{} {}`, `{"document":3}`} {
		if _, err := OpenAPIInspect(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw, _ := json.Marshal(uicontract.OpenAPIInspectInput{Document: strings.Repeat("x", openapiimport.MaxDocumentBytes+1)})
	got, err := OpenAPIInspect(string(raw))
	if err != nil || !strings.Contains(got, "8388608") {
		t.Fatalf("oversize: %s %v", got, err)
	}
	got, err = OpenAPIGenerate(`{"document":"{}","settings_yaml":"unknown: true"}`)
	if err != nil || strings.Contains(got, `"yaml"`) || !strings.Contains(got, "settings") {
		t.Fatalf("bad settings: %s %v", got, err)
	}
}
