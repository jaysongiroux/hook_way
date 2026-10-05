package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Exercise the deployable YAML example through the real ingress graph, not
// just the schema engine. Without an error branch invalid requests return 400.
func TestJSONValidationIngress(t *testing.T) {
	raw, err := os.ReadFile("../../examples/json-validation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		src := string(raw)
		if legacy {
			src = strings.Replace(src, "type: json_validate\n        schema:", "type: transform\n        kind: json_schema\n        json_schema:", 1)
		}
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, logger, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Close() })
		for _, tc := range []struct {
			name, body string
			want       int
		}{
			{"valid large ID", `{"order_id":9007199254740993,"customer":{"email":"dev@example.com"}}`, 200},
			{"integer beyond float range", `{"order_id":1e309,"customer":{"email":"dev@example.com"}}`, 200},
			{"string rejected", `{"order_id":"9007199254740993","customer":{"email":"dev@example.com"}}`, 400},
			{"required missing", `{"order_id":3}`, 400},
			{"format rejected", `{"order_id":3,"customer":{"email":"private-invalid"}}`, 400},
			{"null rejected", `{"order_id":null,"customer":{"email":"dev@example.com"}}`, 400},
			{"unknown field rejected", `{"order_id":3,"customer":{"email":"dev@example.com"},"extra":true}`, 400},
			{"non JSON", `not-json`, 400},
			{"trailing JSON", `{} {}`, 400},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Fatalf("legacy=%t status=%d body=%s want=%d", legacy, rec.Code, rec.Body.String(), tc.want)
				}
				if strings.Contains(rec.Body.String(), "private-invalid") {
					t.Fatal("validation error leaked submitted field value")
				}
			})
		}
	}
}

func TestJSONValidationErrorRouteAndExplicitConversion(t *testing.T) {
	raw, err := os.ReadFile("../../examples/json-validation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base := string(raw)
	for _, tc := range []struct {
		name, src, body, wantBody string
		want                      int
	}{
		{
			name: "on_error response",
			src:  strings.Replace(strings.Replace(base, "      - {id: accepted", "      - {id: rejected, type: respond, status: 422, body: 'Invalid order'}\n      - {id: accepted", 1), "check>accepted]", "check>accepted, check:on_error>rejected]", 1),
			body: `{"order_id":"wrong","customer":{"email":"dev@example.com"}}`, want: 422, wantBody: "Invalid order",
		},
		{
			name: "explicit string conversion",
			src:  strings.Replace(base, "        schema:", "        coercions: [{path: order_id, type: integer}]\n        schema:", 1),
			body: `{"order_id":"9007199254740993","customer":{"email":"dev@example.com"}}`, want: 200, wantBody: "Valid order",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := NewFlowServer(loadFlowCfg(t, tc.src), &fakePublisher{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Close() })
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			srv.ServeHTTP(rec, req)
			if rec.Code != tc.want || rec.Body.String() != tc.wantBody {
				t.Fatalf("response=%d %q want=%d %q", rec.Code, rec.Body.String(), tc.want, tc.wantBody)
			}
		})
	}
}
