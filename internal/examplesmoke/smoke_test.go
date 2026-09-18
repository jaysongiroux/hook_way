package examplesmoke

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jasongiroux/hookway/internal/auth"
	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/flowconfig"
	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/httpapi"
	"github.com/jasongiroux/hookway/internal/queue"
)

// TestExampleSmoke walks every committed Hookway config under examples/,
// validates it the same way `hookway` serve does, boots a FlowServer, and
// POSTs a signed request to each flow. Destinations are a capturing fake so
// this does not need Docker; outbound http_request URLs that are env-driven
// hit a local httptest server. That is the gap `make validate-examples` leaves:
// validate never constructs verifiers or runs the graph.
func TestExampleSmoke(t *testing.T) {
	jwtPriv, jwtPEM := rsaPublicPEM(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	setExampleEnv(t, jwtPEM, upstream.URL)

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

			pub := &capturingPublisher{}
			srv, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			if err != nil {
				t.Fatalf("NewFlowServer: %v", err)
			}
			t.Cleanup(func() { _ = srv.Close() })

			health := httptest.NewRecorder()
			srv.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if health.Code != http.StatusOK {
				t.Fatalf("GET /healthz = %d body %s", health.Code, health.Body.String())
			}

			for _, name := range flowNames(cfg) {
				t.Run(name, func(t *testing.T) {
					req := requestForFlow(t, cfg.Flows[name], jwtPriv)
					ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
					defer cancel()
					req = req.WithContext(ctx)
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					if rec.Code < 200 || rec.Code >= 300 {
						t.Fatalf("%s %s = %d body %s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
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
		"RABBITMQ_URL":            "amqp://guest:guest@localhost:5672/",
		"PADDLE_WEBHOOK_SECRET":   "paddle-secret",
		"GITHUB_WEBHOOK_SECRET":   "github-secret",
		"STRIPE_WEBHOOK_SECRET":   "stripe-secret",
		"ORDERS_WEBHOOK_SECRET":   "orders-secret",
		"NOTIFY_WEBHOOK_SECRET":   "notify-secret",
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
		"HUB_API_KEY":             "hub-key",
		"DIGIFI_WEBHOOK_TOKEN":    "digifi-token",
		"DIGIFI_BASE":             upstreamURL,
		"SF_TOKEN":                "sf-token",
		"ROUTING_TABLE":           "sandbox_ephemeral_routing_data",
		"ROUTING_DB_URL":          "postgres://localhost:5432/routing",
		"REPLAY_DB_URL":           "postgres://localhost:5432/db",
		"REPLAY_API_KEY":          "replay-key",
		"SLACK_WEBHOOK_URL":       upstreamURL,
		"SLACK_SOURCE_SECRET":     "slack-secret",
		"DISCORD_WEBHOOK_URL":     upstreamURL,
		"DISCORD_SOURCE_SECRET":   "discord-secret",
		"HOOKWAY_API_KEY":         "hookway-api-key",
		"UPLOAD_SOURCE_SECRET":    "upload-secret",
		"XML_SOURCE_SECRET":       "xml-secret",
		"XML_UPSTREAM_URL":        upstreamURL,
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

func requestForFlow(t *testing.T, fc graph.FlowConfig, jwtPriv *rsa.PrivateKey) *http.Request {
	t.Helper()
	path, method := triggerRoute(t, fc)
	// An xml envelope parses the raw bytes as XML, so the JSON payload every
	// other flow gets would 400 before reaching the nodes under test. The
	// document carries LOAN.Amount because examples/xml.yaml filters on it.
	body, contentType := []byte(`{"summary":"example-smoke","action":"opened","ref":"refs/heads/main","repository":{"private":false,"full_name":"acme/demo"},"data":{"object":{"organizationId":"acme"}},"order":{"total":150,"currency":"USD"},"customer":{"id":"cust_1"}}`), "application/json"
	switch envelopeBodyFormat(t, fc) {
	case "xml":
		body, contentType = []byte(`<MESSAGE><LOAN id="L1"><Amount>150</Amount></LOAN></MESSAGE>`), "application/xml"
	case "multipart":
		// A multipart envelope needs a real boundary, and examples/multipart.yaml
		// filters on files.document.content_type, so the part must claim PDF.
		body, contentType = multipartBody(t)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-GitHub-Event", "push")
	// showcase/production.yaml's trusted flow allowlists RFC1918 ranges.
	req.RemoteAddr = "10.0.0.1:3456"
	signRequest(t, req, fc, body, jwtPriv)
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
	path = strings.ReplaceAll(path, "{envName}", "qa")
	path = strings.ReplaceAll(path, "{rest...}", "echo")
	path = strings.ReplaceAll(path, "{rest}", "echo")
	return path
}

func signRequest(t *testing.T, req *http.Request, fc graph.FlowConfig, body []byte, jwtPriv *rsa.PrivateKey) {
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
	if opts.SecretRef.Provider != "env" {
		t.Fatalf("smoke signer does not resolve secret_ref provider %q", opts.SecretRef.Provider)
	}
	secret := os.Getenv(opts.SecretRef.Name)
	if secret == "" {
		t.Fatalf("secret_ref env %s is empty", opts.SecretRef.Name)
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
	mac.Write([]byte(fmt.Sprintf("%d:%s", ts, body)))
	return fmt.Sprintf("ts=%d;h1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func stripeHMAC(secret string, body []byte, now time.Time) string {
	ts := now.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, body)))
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

type capturingPublisher struct{}

func (p *capturingPublisher) Publish(context.Context, string, string, queue.Message) error {
	return nil
}
func (p *capturingPublisher) Ready(context.Context) error { return nil }
func (p *capturingPublisher) Close() error                { return nil }
