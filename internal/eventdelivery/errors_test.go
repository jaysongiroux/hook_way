package eventdelivery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

func TestPostgresProtocolErrorsTTLAndAuthorization(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	r.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var v map[string]any
		_ = json.NewDecoder(req.Body).Decode(&v)
		body, _ := json.Marshal(map[string]any{"challenge": v["challenge"]})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	cases := []struct {
		name  string
		patch map[string]any
		code  int
	}{
		{"cursor", map[string]any{"cursor": "resume"}, -32026},
		{"ttl", map[string]any{"ttlMs": -1}, -32602},
		{"ttl type", map[string]any{"ttlMs": "hour"}, -32602},
		{"unknown event", map[string]any{"name": "missing"}, -32023},
		{"extra argument", map[string]any{"arguments": map[string]any{"repository": "acme/app", "owner": "another"}}, -32602},
		{"missing argument", map[string]any{"arguments": map[string]any{}}, -32602},
		{"bad mode", map[string]any{"delivery": map[string]any{"mode": "push", "url": "https://receiver.example/events", "secret": "whsec_" + testKey}}, -32026},
		{"bad secret", map[string]any{"delivery": map[string]any{"mode": "webhook", "url": "https://receiver.example/events", "secret": "not-valid"}}, -32602},
		{"short secret", map[string]any{"delivery": map[string]any{"mode": "webhook", "url": "https://receiver.example/events", "secret": "whsec_" + base64.StdEncoding.EncodeToString([]byte("short"))}}, -32602},
		{"http", map[string]any{"delivery": map[string]any{"mode": "webhook", "url": "http://receiver.example/events", "secret": "whsec_" + testKey}}, -32602},
		{"unknown field", map[string]any{"owner": "another"}, -32602},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := subscribe(t, r, "https://receiver.example/events", tt.patch)
			var rpc *flowconfig.EventRPCError
			if !errors.As(err, &rpc) || rpc.Code != tt.code {
				t.Fatalf("got %v want %d", err, tt.code)
			}
		})
	}
	if count(t, r, "subscriptions", "") != 0 {
		t.Fatal("invalid request created subscription")
	}
	for _, ttl := range []any{nil, 1, 999999999999} {
		out, err := subscribe(t, r, "https://receiver.example/events", map[string]any{"ttlMs": ttl})
		if err != nil {
			t.Fatal(err)
		}
		expiry, err := time.Parse(time.RFC3339Nano, out["refreshBefore"].(string))
		if err != nil || time.Until(expiry) < 59*time.Second || time.Until(expiry) > 24*time.Hour {
			t.Fatal("invalid granted expiry", out)
		}
	}
	// A successful schema-bound OAuth snapshot expires at its token deadline.
	opt := r.endpoints["agent_tools"]["issue.opened"]
	opt.opts.Access = flowconfig.MCPAccessOptions{Scopes: []string{"events:read"}}
	r.endpoints["agent_tools"]["issue.opened"] = opt
	r.BindEventAuthorizer("agent_tools", func(_ context.Context, p flowconfig.EventPrincipal) bool { return p.ID == "oauth:alice" })
	deadline := time.Now().Add(20 * time.Second)
	p := flowconfig.EventPrincipal{ID: "oauth:alice", Scopes: []string{"events:read"}, ExpiresAt: deadline}
	raw := json.RawMessage(`{"name":"issue.opened","arguments":{"repository":"acme/app"},"delivery":{"mode":"webhook","url":"https://receiver.example/events","secret":"whsec_` + testKey + `"}}`)
	out, err := r.EventCall(context.Background(), "agent_tools", "events/subscribe", p, raw)
	if err != nil {
		t.Fatal(err)
	}
	expiry, _ := time.Parse(time.RFC3339Nano, out.(map[string]any)["refreshBefore"].(string))
	if !expiry.Equal(deadline) {
		t.Fatal("OAuth subscription outlived token")
	}
	if err = r.Publish(context.Background(), "agent_tools", "issue.opened", occurrence("revoked", "acme/app")); err != nil {
		t.Fatal(err)
	}
	r.BindEventAuthorizer("agent_tools", func(context.Context, flowconfig.EventPrincipal) bool { return false })
	j, err := r.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.attempt(context.Background(), j)
	if count(t, r, "deliveries", "AND state='cancelled'") != 1 {
		t.Fatal("revoked grant delivered")
	}
	p.ExpiresAt = time.Now().Add(-time.Second)
	if _, err = r.EventCall(context.Background(), "agent_tools", "events/subscribe", p, raw); err == nil {
		t.Fatal("expired OAuth refreshed")
	}
}
func TestPostgresAuthorizedPrincipalsCannotUnsubscribeEachOther(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	opt := r.endpoints["agent_tools"]["issue.opened"]
	opt.opts.Access.APIKeys = []string{"reader", "other"}
	r.endpoints["agent_tools"]["issue.opened"] = opt
	r.BindEventAuthorizer("agent_tools", func(_ context.Context, p flowconfig.EventPrincipal) bool { return p.ID == "key:"+p.Key })
	r.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var v map[string]any
		_ = json.NewDecoder(req.Body).Decode(&v)
		body, _ := json.Marshal(map[string]any{"challenge": v["challenge"]})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	if _, err := subscribe(t, r, "https://receiver.example/events", nil); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"name":"issue.opened","arguments":{"repository":"acme/app"},"delivery":{"mode":"webhook","url":"https://receiver.example/events"}}`)
	if _, err := r.EventCall(context.Background(), "agent_tools", "events/unsubscribe", flowconfig.EventPrincipal{ID: "key:other", Key: "other"}, raw); err != nil {
		t.Fatal(err)
	}
	if count(t, r, "subscriptions", "AND active AND owner='key:reader'") != 1 {
		t.Fatal("authorized other principal cancelled owner's subscription")
	}
}

func TestPostgresValidationAndCallbackFailuresDoNotAcceptWork(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	pub := &Publisher{Runtime: r, Endpoint: "agent_tools"}
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pub.Close(); err != nil {
		t.Fatal(err)
	}
	cases := []any{nil, []any{}, map[string]any{"type": "untrusted"}, map[string]any{"eventId": "", "name": "issue.opened"}, map[string]any{"eventId": "id", "name": "issue.opened", "timestamp": true}, map[string]any{"eventId": "id", "name": "issue.opened", "timestamp": "bad"}}
	for _, v := range cases {
		if err := r.Publish(context.Background(), "agent_tools", "issue.opened", v); err == nil {
			t.Fatalf("invalid occurrence accepted: %v", v)
		}
	}
	for _, patch := range []map[string]any{{"extra": true}, {"cursor": "resume"}, {"name": "wrong"}, {"eventId": "bad\nheader"}, {"data": map[string]any{"repository": 17}}, {"data": map[string]any{"issue_id": "x", "repository": "acme/app", "title": strings.Repeat("x", MaxEventBytes)}}} {
		v := occurrence("x", "acme/app")
		for k, value := range patch {
			v[k] = value
		}
		if err := r.Publish(context.Background(), "agent_tools", "issue.opened", v); err == nil {
			t.Fatal("invalid occurrence accepted", patch)
		}
	}
	if err := r.Publish(context.Background(), "missing", "issue.opened", occurrence("x", "acme/app")); err == nil {
		t.Fatal("unexposed event accepted")
	}
	r.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"challenge":"wrong"}`))}, nil
	})}
	_, err := subscribe(t, r, "https://receiver.example/events", nil)
	var rpc *flowconfig.EventRPCError
	if !errors.As(err, &rpc) || rpc.Code != -32015 {
		t.Fatal("callback failure not classified", err)
	}
	if count(t, r, "subscriptions", "") != 0 || count(t, r, "occurrences", "") != 0 {
		t.Fatal("invalid work persisted")
	}
	for _, status := range []int{404, 503} {
		r.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})}
		_, err := subscribe(t, r, "https://receiver.example/events", nil)
		var rpc *flowconfig.EventRPCError
		if !errors.As(err, &rpc) || rpc.Code != -32015 {
			t.Fatal("callback status not classified", err)
		}
	}
	r.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
	if _, err := subscribe(t, r, "https://receiver.example/events", nil); err == nil {
		t.Fatal("callback timeout activated subscription")
	}

	t.Setenv("EVENT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 32))))
	if other, err := New(context.Background(), cfg, nil); err == nil {
		_ = other.Close()
		t.Fatal("namespace key mismatch accepted")
	}
	t.Setenv("EVENT_ENCRYPTION_KEY", "invalid")
	if other, err := New(context.Background(), cfg, nil); err == nil {
		_ = other.Close()
		t.Fatal("invalid encryption key accepted")
	}
}
