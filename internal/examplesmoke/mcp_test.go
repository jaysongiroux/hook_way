package examplesmoke

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
)

type mcpBearerTransport string

func (b mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// Exercise the example branches and their original policies against local
// upstream/identity fixtures. This does not prove a live external API or IdP.
func smokeMCP(t *testing.T, cfg flowconfig.Config, key *rsa.PrivateKey) {
	t.Helper()
	for _, name := range []string{"MCP_READER_TOKEN", "STATUS_API_TOKEN"} {
		t.Setenv(name, "example-smoke-independent-secret-"+name)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/customers/smoke":
			_, _ = io.WriteString(w, `{"id":"smoke","name":"Pat"}`)
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "smoke", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		default:
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		}
	}))
	t.Cleanup(upstream.Close)
	for name, profile := range cfg.Credentials {
		profile.AllowedOrigins = []string{upstream.URL}
		profile.AllowPrivateTargets = true
		cfg.Credentials[name] = profile
	}
	type endpoint struct{ name, path, token string }
	var endpoints []endpoint
	for name, flow := range cfg.Flows {
		for i := range flow.Nodes {
			n := &flow.Nodes[i]
			if n.Type == "http_request" {
				var o map[string]any
				if err := n.Options.Decode(&o); err != nil {
					t.Fatal(err)
				}
				u, _ := url.Parse(o["url"].(string))
				o["url"], o["allow_private_targets"] = upstream.URL+u.Path, true
				if err := n.Options.Encode(o); err != nil {
					t.Fatal(err)
				}
			}
			if n.Type == "mcp_trigger" {
				var token string
				var o flowconfig.MCPTriggerOptions
				if err := n.Options.Decode(&o); err != nil {
					t.Fatal(err)
				}
				for _, k := range o.Auth.APIKeys {
					if k.SecretRef.Provider == "env" {
						t.Setenv(k.SecretRef.Name, "example-smoke-independent-secret-"+k.SecretRef.Name)
					}
					token = os.Getenv(k.SecretRef.Name)
				}
				if a := o.Auth.OAuth; a != nil {
					a.Issuer, a.JWKSURL, a.AllowPrivateTargets = upstream.URL, upstream.URL+"/jwks", true
					var err error
					tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": a.Issuer, "aud": a.Audience, "sub": "example-reader", "exp": time.Now().Add(time.Minute).Unix(), "scope": "service:read"})
					tok.Header["kid"] = "smoke"
					token, err = tok.SignedString(key)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := n.Options.Encode(o); err != nil {
					t.Fatal(err)
				}
				endpoints = append(endpoints, endpoint{name, o.Path, token})
			}
		}
		cfg.Flows[name] = flow
	}
	server, err := httpapi.NewFlowServer(cfg, &capturingPublisher{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "example-smoke", Version: "1"}, nil)
			httpClient := http.DefaultClient
			if endpoint.token != "" {
				httpClient = &http.Client{Transport: mcpBearerTransport(endpoint.token)}
			}
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + endpoint.path, HTTPClient: httpClient}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil || len(listed.Tools) == 0 {
				t.Fatalf("list: %+v %v", listed, err)
			}
			for _, tool := range listed.Tools {
				args := map[string]any{}
				if tool.Name == "get_customer" || tool.Name == "update_customer" {
					args["path"] = map[string]any{"id": "smoke"}
					if tool.Name == "update_customer" {
						args["body"] = map[string]any{"name": "Pat"}
					}
				}
				if tool.Name == "format_greeting" {
					args["name"] = "Sam"
				}
				result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
				if err != nil || result.IsError {
					t.Fatalf("tool %s: %+v %v", tool.Name, result, err)
				}
			}
		})
	}
}
