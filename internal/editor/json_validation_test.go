package editor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/cmd/hookway-wasm/bridge"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

func TestJSONSampleHTTPMatchesWASM(t *testing.T) {
	for _, raw := range []string{
		`{"schema":{"type":"integer"},"sample":"9007199254740993"}`,
		`{"schema":{"type":"integer"},"sample":"\"sensitive-value\""}`,
		`{"schema":{"$ref":"https://sensitive-value.invalid/schema"},"sample":"{}"}`,
		`{"schema":{"type":"object"},"sample":"{"}`,
	} {
		want, err := bridge.JSONSampleValidate(raw)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		Handler("").ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/json-sample-validation", strings.NewReader(raw)))
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		var gotValue, wantValue uicontract.JSONSampleValidationResult
		if err := json.Unmarshal(rec.Body.Bytes(), &gotValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
			t.Fatal(err)
		}
		gotJSON, _ := json.Marshal(gotValue)
		if string(gotJSON) != want {
			t.Fatalf("HTTP/WASM drift: %s != %s", gotJSON, want)
		}
		if strings.Contains(rec.Body.String(), "sensitive-value") {
			t.Fatal("private value echoed")
		}
	}
}

type unreadableSampleBody struct{}

func (unreadableSampleBody) Read([]byte) (int, error) { return 0, errors.New("private read error") }
func (unreadableSampleBody) Close() error             { return nil }

func TestJSONSampleTransportStatusAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		body   io.Reader
		status int
	}{
		{strings.NewReader(`{"schema":{},"sample":"{}","unknown":"private"}`), 400},
		{strings.NewReader(`{"schema":{},"sample":"{}"} {}`), 400},
		{strings.NewReader(strings.Repeat(" ", uicontract.MaxJSONSampleRequestBytes+1)), 413},
		{unreadableSampleBody{}, 400},
	} {
		rec := httptest.NewRecorder()
		Handler("").ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/json-sample-validation", tc.body))
		if rec.Code != tc.status || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || strings.Contains(rec.Body.String(), "private") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
	data, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/json-sample-validation"].(map[string]any)["post"].(map[string]any)
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"200", "400", "413", "401", "405"} {
		if responses[code] == nil {
			t.Fatalf("missing status %s", code)
		}
	}
	if op["x-max-body-bytes"] != float64(uicontract.MaxJSONSampleRequestBytes) {
		t.Fatal("body size metadata drift")
	}
}
