package flowconfig

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestHeadFromRequestCapturesPreBodyFields(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github?authID=abc", strings.NewReader("ignored"))
	r.Header.Set("X-Hub-Signature-256", "sha256=x")
	r.Header.Set(config.LoopHopsHeader, "2")

	head := HeadFromRequest(r, map[string]string{"provider": "github"}, "203.0.113.7")

	if head.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", head.Method)
	}
	if head.Query.Get("authID") != "abc" {
		t.Errorf("Query[authID] = %q, want abc (query-param auth needs the query on the pre-body head)", head.Query.Get("authID"))
	}
	if head.Hops != 2 {
		t.Errorf("Hops = %d, want 2", head.Hops)
	}
	if head.SourceIP != "203.0.113.7" {
		t.Errorf("SourceIP = %q, want 203.0.113.7", head.SourceIP)
	}
	if head.PathVars["provider"] != "github" {
		t.Errorf("PathVars[provider] = %q, want github", head.PathVars["provider"])
	}
	if head.RequestID == "" || head.TraceID == "" {
		t.Errorf("RequestID/TraceID empty: %q %q", head.RequestID, head.TraceID)
	}
}

func TestHeadFromRequestDoesNotReadBody(t *testing.T) {
	body := "sentinel-body-content"
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/w", strings.NewReader(body))
	_ = HeadFromRequest(r, nil, "1.2.3.4")

	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("ReadAll() = %v", err)
	}
	if string(got) != body {
		t.Errorf("body was consumed by the trigger: read %q, want the full %q", got, body)
	}
}

func TestHeadFromRequestCapturesGreedyRest(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/proxy/v1/orders/123", nil)
	head := HeadFromRequest(r, map[string]string{"rest": "v1/orders/123"}, "127.0.0.1")
	if head.PathVars["rest"] != "v1/orders/123" {
		t.Errorf("PathVars[rest] = %q", head.PathVars["rest"])
	}
}
