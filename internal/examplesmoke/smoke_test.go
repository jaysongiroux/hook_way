package examplesmoke

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// TestExampleSmoke walks every committed Hookway config under examples/,
// validates it the same way `hookway` serve does, boots a FlowServer, and
// POSTs a signed request to each flow. Destinations are a capturing fake so
// this does not need Docker; outbound http_request URLs that are env-driven
// hit a local httptest server. That is the gap `make validate-examples` leaves:
// validate never constructs verifiers or runs the graph.
func TestExampleSmoke(t *testing.T) {
	jwtPriv, jwtPEM := rsaPublicPEM(t)
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&ecPriv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMPLE_ECDSA_KEY", base64.StdEncoding.EncodeToString(der))
	t.Setenv("SAMPLE_SIGNING_BUNDLE", `{"signing_key":"synthetic"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	setExampleEnv(t, jwtPEM, upstream.URL)
	t.Setenv("EVENT_DATABASE_URL", "postgres://example:example@127.0.0.1:5432/events?sslmode=disable")
	t.Setenv("PROTECTION_DATABASE_URL", "postgres://example:example@127.0.0.1:5432/hookway?sslmode=disable")
	t.Setenv("PROTECTION_REDIS_URL", "redis://127.0.0.1:6379/0")
	for _, provider := range []string{"GITHUB", "STRIPE", "PADDLE", "SLACK"} {
		path := filepath.Join(t.TempDir(), "signing-secret")
		if err := os.WriteFile(path, []byte("provider-smoke-secret"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(provider+"_SECRET_FILE", path)
	}

	valid, invalid := discoverExamples(t)
	if len(valid) < 20 {
		t.Fatalf("discovered %d valid example configs, want at least 20", len(valid))
	}
	if len(invalid) != 2 {
		t.Fatalf("discovered invalid examples %v, want the two showcase invalid-* fixtures", invalid)
	}

	t.Run("invalid", func(t *testing.T) {
		for _, path := range invalid {
			t.Run(filepath.Base(path), func(t *testing.T) {
				_, issues := flowconfig.Load(path)
				if len(blocking(issues)) == 0 {
					t.Fatalf("%s validated cleanly; it is documented as invalid", path)
				}
			})
		}
	})

	for _, path := range valid {
		rel := exampleRel(t, path)
		t.Run(rel, func(t *testing.T) {
			cfg, issues := flowconfig.Load(path)
			if blocking := blocking(issues); len(blocking) > 0 {
				t.Fatalf("load: %v", blocking)
			}
			issues = append(issues, flowconfig.Lint(cfg)...)
			issues = append(issues, flowconfig.StandaloneIssues(cfg)...)
			if blocking := blocking(issues); len(blocking) > 0 {
				t.Fatalf("serve-path issues: %v", blocking)
			}
			if cfg.EventDelivery != nil {
				t.Skip("durable MCP event delivery has real PostgreSQL and authenticated webhook acceptance coverage in internal/eventdelivery; offline validation passed")
			}
			mcpFlows := 0
			for _, flow := range cfg.Flows {
				if _, ok := flowconfig.MCPOptions(flow); ok {
					mcpFlows++
				}
			}
			if mcpFlows > 0 {
				smokeMCP(t, cfg, jwtPriv)
				if mcpFlows == len(cfg.Flows) {
					return
				}
			}
			for _, store := range cfg.StateStores {
				if store.Memory == nil {
					t.Skip("shared Redis/PostgreSQL protection stores have adapter conformance and integration coverage; offline validation already passed")
				}
			}

			if len(cfg.LaunchDarkly) > 0 {
				t.Skip("LaunchDarkly uses SDK test data and local mock servers in dedicated runtime and example HTTP tests; offline validation passed")
			}
			if rel == "routing/shared.yaml" || rel == "routing/registration.yaml" {
				t.Skip("native DynamoDB binding has dedicated integration coverage; offline validation already passed")
			}
			if strings.HasPrefix(rel, "adapters/") {
				t.Skip("specialized signed batches, schema projection, SNS diff and managed XML have dedicated acceptance tests; offline validation passed")
			}
			if strings.HasPrefix(rel, "credentials/") {
				t.Skip("native secret/token profiles have dedicated HTTP and provider acceptance tests; offline validation already passed")
			}
			if rel == "proxy/admitted.yaml" {
				u, _ := url.Parse(upstream.URL)
				for name, binding := range cfg.Routing.Environments {
					binding.BackendHost = u.Hostname()
					cfg.Routing.Environments[name] = binding
				}
				port, _ := strconv.Atoi(u.Port())
				flow := cfg.Flows["admitted_proxy"]
				for i := range flow.Nodes {
					if flow.Nodes[i].Type == "http_request" {
						var options map[string]any
						if err := flow.Nodes[i].Options.Decode(&options); err != nil {
							t.Fatal(err)
						}
						options["upstream"].(map[string]any)["scheme"] = "http"
						options["upstream"].(map[string]any)["port"] = port
						options["allow_private_targets"] = true
						var node yaml.Node
						if err := node.Encode(options); err != nil {
							t.Fatal(err)
						}
						flow.Nodes[i].Options = node
					}
				}
				cfg.Flows["admitted_proxy"] = flow
			}
			pub := &capturingPublisher{}
			srv, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			if err != nil {
				t.Fatalf("NewFlowServer: %v", err)
			}
			t.Cleanup(func() { _ = srv.Close() })

			health := httptest.NewRecorder()
			srv.ServeHTTP(health, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
			if health.Code != http.StatusOK {
				t.Fatalf("GET /healthz = %d body %s", health.Code, health.Body.String())
			}

			for _, name := range flowNames(cfg) {
				if _, ok := flowconfig.MCPOptions(cfg.Flows[name]); ok {
					continue
				}
				t.Run(name, func(t *testing.T) {
					pub.reset()
					req := requestForFlow(t, cfg.Flows[name], jwtPriv, ecPriv)
					ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
					defer cancel()
					req = req.WithContext(ctx)
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					if rec.Code < 200 || rec.Code >= 300 {
						t.Fatalf("%s %s = %d body %s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
					}
					if strings.Contains(rel, "multipart-s3-lambda") {
						got := pub.targets()
						if len(got) < 2 {
							t.Fatalf("publishes = %v, want S3 then Lambda", got)
						}
						if got[0] != "uploads" || got[1] != "upload-worker" {
							t.Fatalf("publish order = %v, want [uploads upload-worker]", got)
						}
					}
				})
			}
		})
	}
}

func discoverExamples(t *testing.T) (valid, invalid []string) {
	t.Helper()
	root := examplesDir(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		if d.Name() == "docker-compose.yaml" {
			return nil
		}
		// These three files configure optional monitoring dependencies, not
		// Hookway. metrics/prometheus validates their actual tool contracts.
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if strings.HasPrefix(filepath.ToSlash(rel), "multi-yaml/flows/") {
			return nil
		} // Flow bodies are exercised through their entry YAML.
		switch filepath.ToSlash(rel) {
		case "openapi/api.yaml", "openapi/settings.yaml", "production-local/monitoring/compose.yaml", "production-local/monitoring/prometheus.yaml", "production-local/monitoring/alertmanager.yaml":
			return nil
		}
		// Every replay.yaml (examples/replay/, examples/dev/) configures
		// hookway-replay, not core; TestTheReplayExamplesValidate in
		// internal/replay/config covers them.
		if d.Name() == "replay.yaml" {
			return nil
		}
		if strings.Contains(d.Name(), "invalid") {
			invalid = append(invalid, path)
			return nil
		}
		valid = append(valid, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return valid, invalid
}

func examplesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "examples"))
}

func exampleRel(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(examplesDir(t), path)
	if err != nil {
		return path
	}
	return rel
}

func blocking(issues []config.ValidationIssue) []config.ValidationIssue {
	var out []config.ValidationIssue
	for _, iss := range issues {
		if iss.EffectiveSeverity() == config.SeverityError {
			out = append(out, iss)
		}
	}
	return out
}

func flowNames(cfg flowconfig.Config) []string {
	names := make([]string, 0, len(cfg.Flows))
	for name := range cfg.Flows {
		names = append(names, name)
	}
	return names
}

func setExampleEnv(t *testing.T, jwtPEM, upstreamURL string) {
	t.Helper()
	// YAML is `secret: "${JWT_PUBLIC_KEY}"`. A PEM with real newlines would
	// break that quoted scalar; `\n` escapes survive expansion and decode.
	t.Setenv("JWT_PUBLIC_KEY", strings.ReplaceAll(strings.TrimSpace(jwtPEM), "\n", `\n`))
	vals := map[string]string{
		"RABBITMQ_URL":            "amqp://guest:guest@localhost:5672/", // #nosec G101 -- example smoke fixture
		"PADDLE_WEBHOOK_SECRET":   "paddle-secret",
		"GITHUB_WEBHOOK_SECRET":   "github-secret",
		"STRIPE_WEBHOOK_SECRET":   "stripe-secret",
		"ORDERS_WEBHOOK_SECRET":   "orders-secret",
		"NOTIFY_WEBHOOK_SECRET":   "notify-secret",
		"SAMPLE_WEBHOOK_SECRET":   "synthetic-secret",
		"REVENUECAT_WEBHOOK_AUTH": "revenuecat-token",
		"BASIC_WEBHOOK_SECRET":    "basic-secret",
		"REDIS_URL":               "redis://localhost:6379",
		"POSTGRES_URL":            "postgres://localhost:5432/db",
		"MONGODB_URI":             "mongodb://localhost:27017",
		"KAFKA_BROKER_1":          "localhost:9092",
		"KAFKA_BROKER_2":          "localhost:9092",
		"KAFKA_USERNAME":          "dummy",
		"KAFKA_PASSWORD":          "dummy",
		"WEBHOOK_SECRET":          "webhook-secret",
		"WEBHOOK_URL":             upstreamURL,
		"WEBHOOK_API_KEY":         "webhook-api-key",
		"TEMPORAL_ADDRESS":        "localhost:7233",
		"TEMPORAL_NAMESPACE":      "default",
		"TEMPORAL_API_KEY":        "dummy",
		"ARCHIVE_BUCKET":          "archive",
		"DOWNSTREAM_API_KEY":      "downstream-key",
		"VENDOR_WEBHOOK_TOKEN":    "vendor-token",
		"UPSTREAM_BASE":           upstreamURL,
		"PROXY_TOKEN":             "proxy-token",
		"ROUTING_TABLE":           "routing_data",
		"ROUTING_DB_URL":          "postgres://localhost:5432/routing",
		"SLACK_WEBHOOK_URL":       upstreamURL,
		"SLACK_SOURCE_SECRET":     "slack-secret",
		"DISCORD_WEBHOOK_URL":     upstreamURL,
		"DISCORD_SOURCE_SECRET":   "discord-secret",
		"HOOKWAY_API_KEY":         "hookway-api-key",
		"UPLOAD_SOURCE_SECRET":    "upload-secret",
		"XML_SOURCE_SECRET":       "xml-secret",
		"XML_UPSTREAM_URL":        upstreamURL,
		"REPLAY_CAPTURE_TOKEN":    "capture-token-0123456789abcdefghijkl",
		"REPLAY_CONTROL_TOKEN":    "control-token-0123456789abcdefghij",
	}
	for k, v := range vals {
		t.Setenv(k, v)
	}
}

func rsaPublicPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return priv, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func requestForFlow(t *testing.T, fc graph.FlowConfig, jwtPriv *rsa.PrivateKey, ecPriv *ecdsa.PrivateKey) *http.Request {
	t.Helper()
	path, method := triggerRoute(t, fc)
	// An xml envelope parses the raw bytes as XML, so the JSON payload every
	// other flow gets would 400 before reaching the nodes under test. The
	// document carries ORDER.Amount because examples/xml.yaml filters on it.
	body, contentType := []byte(`{"summary":"example-smoke","action":"opened","ref":"refs/heads/main","repository":{"private":false,"full_name":"acme/demo"},"data":{"object":{"organizationId":"acme"}},"order":{"total":150,"currency":"USD"},"customer":{"id":"cust_1"}}`), "application/json"
	switch envelopeBodyFormat(t, fc) {
	case "xml":
		body, contentType = []byte(`<MESSAGE><ORDER id="O1"><Amount>150</Amount></ORDER></MESSAGE>`), "application/xml"
	case "multipart":
		// A multipart envelope needs a real boundary, and examples/multipart.yaml
		// filters on files.document.content_type, so the part must claim PDF.
		body, contentType = multipartBody(t)
	}
	if strings.HasPrefix(path, "/proxy/admitted/") {
		body = []byte(`{"env_name":"staging"}`)
	}
	if path == "/webhooks/shared" {
		body = []byte(`{"x_env_name":"staging"}`)
	}
	if path == "/orders" {
		for _, node := range fc.Nodes {
			if node.Type == "json_validate" {
				body = []byte(`{"order_id":9007199254740993,"customer":{"email":"dev@example.com"}}`)
				break
			}
		}
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-GitHub-Event", "push")
	// showcase/production.yaml's trusted flow allowlists RFC1918 ranges.
	req.RemoteAddr = "10.0.0.1:3456"
	signRequest(t, req, fc, body, jwtPriv, ecPriv)
	return req
}

// multipartBody builds the upload examples/multipart.yaml expects: one
// application/pdf part named "document" plus the sibling form field its code
// node reads.
func multipartBody(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("account", "acct_1"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="document"; filename="report.pdf"`},
		"Content-Type":        []string{"application/pdf"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("%PDF-1.4 example-smoke")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// envelopeBodyFormat reports the body_format of the flow's first envelope node,
// defaulting to the node's own default of json. A flow with envelopes on
// different switch branches would need a per-path answer; no example has one.
func envelopeBodyFormat(t *testing.T, fc graph.FlowConfig) string {
	t.Helper()
	for _, nc := range fc.Nodes {
		if nc.Type != "envelope" {
			continue
		}
		var opts flowconfig.EnvelopeOptions
		if err := nc.Options.Decode(&opts); err != nil {
			t.Fatalf("envelope options: %v", err)
		}
		if opts.BodyFormat != "" {
			return opts.BodyFormat
		}
		return "json"
	}
	return "json"
}

func triggerRoute(t *testing.T, fc graph.FlowConfig) (path, method string) {
	t.Helper()
	for _, nc := range fc.Nodes {
		if nc.Type != "http_trigger" {
			continue
		}
		opts, err := flowconfig.TriggerOptions(nc)
		if err != nil {
			t.Fatalf("trigger options: %v", err)
		}
		path = fillPath(opts.Path)
		method = http.MethodPost
		for _, m := range opts.Methods {
			if strings.EqualFold(m, http.MethodPost) {
				return path, http.MethodPost
			}
		}
		if len(opts.Methods) > 0 && !strings.EqualFold(opts.Methods[0], "ANY") {
			method = strings.ToUpper(opts.Methods[0])
		}
		return path, method
	}
	t.Fatal("flow has no http_trigger")
	return "", ""
}

func fillPath(path string) string {
	// examples/variables.yaml's code node allowlists qa-1, staging, and prod.
	// A generic "qa" would 400 as an unknown environment.
	path = strings.ReplaceAll(path, "{envName}", "qa-1")
	path = strings.ReplaceAll(path, "{rest...}", "echo")
	path = strings.ReplaceAll(path, "{rest}", "echo")
	return path
}

func signRequest(t *testing.T, req *http.Request, fc graph.FlowConfig, body []byte, jwtPriv *rsa.PrivateKey, ecPriv *ecdsa.PrivateKey) {
	t.Helper()
	for _, nc := range fc.Nodes {
		switch nc.Type {
		case "no_auth":
			return
		case "auth":
			var opts flowconfig.AuthOptions
			if err := nc.Options.Decode(&opts); err != nil {
				t.Fatalf("auth options: %v", err)
			}
			header := opts.SignatureHeader
			if opts.Source != nil && opts.Source.Kind == "header" {
				header = opts.Source.Key
			}
			scheme := opts.Scheme
			if scheme == "header_exact" {
				scheme = auth.SchemeExact
			}
			secret := authSecret(t, opts)
			switch scheme {
			case auth.SchemeHMACSHA256SlackV0, auth.SchemeHMACSHA256HeadersDot, auth.SchemeHMACSHA256TSDotMulti, auth.SchemeECDSAP256SHA256Headers:
				ts := strconv.FormatInt(time.Now().Unix(), 10)
				if opts.TimestampUnit == "milliseconds" {
					ts = strconv.FormatInt(time.Now().UnixMilli(), 10)
				}
				signed := body
				if opts.BodyRepresentation == "json_js" {
					var err error
					signed, err = jsoncanon.Stringify(context.Background(), body)
					if err != nil {
						t.Fatal(err)
					}
				}
				separator := "."
				if scheme == auth.SchemeECDSAP256SHA256Headers {
					separator = ""
				}
				prefix := ts + separator
				if scheme == auth.SchemeHMACSHA256SlackV0 {
					prefix = "v0:" + ts + ":"
				}
				payload := append([]byte(prefix), signed...)
				var signature string
				if scheme == auth.SchemeECDSAP256SHA256Headers {
					digest := sha256.Sum256(payload)
					sig, err := ecdsa.SignASN1(rand.Reader, ecPriv, digest[:])
					if err != nil {
						t.Fatal(err)
					}
					signature = base64.StdEncoding.EncodeToString(sig)
				} else {
					mac := hmac.New(sha256.New, []byte(secret))
					mac.Write(payload)
					signature = hex.EncodeToString(mac.Sum(nil))
				}
				if scheme == auth.SchemeHMACSHA256SlackV0 {
					signature = "v0=" + signature
				}
				if scheme == auth.SchemeHMACSHA256TSDotMulti {
					label := opts.SignatureLabel
					if label == "" {
						label = "s"
					}
					signature = "t=" + ts + "," + label + "=bad," + label + "=" + signature
				} else {
					req.Header.Set(opts.TimestampHeader, ts)
				}
				req.Header.Set(header, signature)
			case auth.SchemeHMACSHA256Hex:
				req.Header.Set(header, githubHMAC(secret, body))
			case auth.SchemeHMACSHA256TSColon:
				req.Header.Set(header, paddleHMAC(secret, body, time.Now()))
			case auth.SchemeHMACSHA256TSDot:
				req.Header.Set(header, stripeHMAC(secret, body, time.Now()))
			case auth.SchemeExact:
				value := secret
				if opts.RemoveBearer {
					value = "Bearer " + value
				}
				req.Header.Set(header, value)
			case auth.SchemeJWTRS256:
				req.Header.Set(header, "Bearer "+signJWTRS256(t, jwtPriv, opts, body))
			default:
				t.Fatalf("smoke signer does not implement scheme %q", scheme)
			}
			return
		}
	}
}

func authSecret(t *testing.T, opts flowconfig.AuthOptions) string {
	t.Helper()
	if opts.Secret != "" {
		return opts.Secret
	}
	if opts.SecretRef == nil {
		t.Fatal("auth node has neither secret nor secret_ref")
	}
	if opts.SecretRef.Provider == "file" {
		data, err := os.ReadFile(opts.SecretRef.Name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	if opts.SecretRef.Provider != "env" {
		t.Fatalf("smoke signer does not resolve secret_ref provider %q", opts.SecretRef.Provider)
	}
	secret := os.Getenv(opts.SecretRef.Name)
	if secret == "" {
		t.Fatalf("secret_ref env %s is empty", opts.SecretRef.Name)
	}
	if opts.SecretRef.JSONField != "" {
		var bundle map[string]string
		if err := json.Unmarshal([]byte(secret), &bundle); err != nil {
			t.Fatal(err)
		}
		secret = bundle[opts.SecretRef.JSONField]
	}
	return secret
}

func githubHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func paddleHMAC(secret string, body []byte, now time.Time) string {
	ts := now.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d:%s", ts, body)
	return fmt.Sprintf("ts=%d;h1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func stripeHMAC(secret string, body []byte, now time.Time) string {
	ts := now.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", ts, body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func signJWTRS256(t *testing.T, priv *rsa.PrivateKey, opts flowconfig.AuthOptions, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	claims := jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	if opts.ExpectedIssuer != "" {
		claims["iss"] = opts.ExpectedIssuer
	}
	if opts.ExpectedAudience != "" {
		claims["aud"] = opts.ExpectedAudience
	}
	if opts.BodyHashClaim != "" {
		claims[opts.BodyHashClaim] = hex.EncodeToString(sum[:])
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type capturingPublisher struct {
	mu    sync.Mutex
	order []string
}

func (p *capturingPublisher) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.order = nil
}

func (p *capturingPublisher) targets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.order...)
}

func (p *capturingPublisher) Publish(_ context.Context, _, target string, _ queue.Message) error {
	p.mu.Lock()
	p.order = append(p.order, target)
	p.mu.Unlock()
	return nil
}
func (p *capturingPublisher) Ready(context.Context) error { return nil }
func (p *capturingPublisher) Close() error                { return nil }
