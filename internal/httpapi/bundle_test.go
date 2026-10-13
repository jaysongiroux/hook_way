package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

func TestServingUnpackedMultiYAML(t *testing.T) {
	cfg, issues := flowconfig.Load("../../examples/multi-yaml/core.yaml")
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, flow := range []string{"orders", "billing"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/"+flow, bytes.NewBufferString(`{"id":1}`))
		req.Header.Set("X-Auth", "change-me-demo-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 202 || rec.Body.String() != "accepted "+flow {
			t.Fatalf("%s: %d %s", flow, rec.Code, rec.Body.String())
		}
	}
}
