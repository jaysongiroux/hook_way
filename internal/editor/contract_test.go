package editor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

func TestEditorContractRoutingAndWireBehavior(t *testing.T) {
	data, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	handler := Handler("test-key")
	for _, route := range Routes() {
		t.Run(route.OperationID, func(t *testing.T) {
			op := doc["paths"].(map[string]any)[route.Path].(map[string]any)[strings.ToLower(route.Method)].(map[string]any)
			if op["operationId"] != route.OperationID {
				t.Fatal("operation drift")
			}
			wrong := httptest.NewRecorder()
			handler.ServeHTTP(wrong, httptest.NewRequestWithContext(context.Background(), "DELETE", route.Path, nil))
			if wrong.Code != 405 || !strings.HasPrefix(wrong.Header().Get("Content-Type"), "text/plain") {
				t.Fatalf("wrong method changed: %d", wrong.Code)
			}
			req := httptest.NewRequestWithContext(context.Background(), route.Method, route.Path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 401 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
				t.Fatalf("auth wire behavior: %d %s", rec.Code, rec.Body.String())
			}
			body := "flows: {}"
			if route.OperationID == "validateBundle" {
				body = `{"entry":"core.yaml","files":{"core.yaml":"flows: {}"}}`
			}
			if route.OperationID == "jsonSampleValidation" {
				body = `{"schema":{"type":"object"},"sample":"{}"}`
			}
			req = httptest.NewRequestWithContext(context.Background(), route.Method, route.Path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-key")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("live route %s: %d %s", route.Pattern(), rec.Code, rec.Body.String())
			}
			if route.Path == "/contract" {
				var got uicontract.InfoDTO
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, uicontract.Info()) {
					t.Fatalf("metadata drift: %s", rec.Body.String())
				}
			}
		})
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/validate", strings.NewReader(strings.Repeat("x", maxValidateBytes+1)))
	req.Header.Set("Authorization", "Bearer test-key")
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("oversized validation wire changed: %d", rec.Code)
	}
}
