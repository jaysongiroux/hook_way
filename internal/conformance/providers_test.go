package conformance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
)

type providerVector struct{ Provider, Body, Secret, Timestamp, Header, Signature, MessageID string }

func providerVectors(t *testing.T) []providerVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/provider-signatures.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []providerVector
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
}
func providerSpec(provider, secret string) auth.Spec {
	s := auth.Spec{Secret: secret, TimestampTolerance: 5 * time.Minute}
	switch provider {
	case "github":
		s.Scheme = auth.SchemeHMACSHA256Hex
	case "stripe":
		s.Scheme = auth.SchemeHMACSHA256TSDot
	case "paddle":
		s.Scheme = auth.SchemeHMACSHA256TSColon
		s.TimestampTolerance = 5 * time.Second
	case "slack":
		s.Scheme = auth.SchemeHMACSHA256SlackV0
		s.TimestampUnit = "seconds"
	case "polar", "polar_standard":
		s.Scheme = auth.SchemeHMACSHA256Polar
		s.TimestampUnit = "seconds"
	}
	return s
}

// Golden vectors were computed with Python's hmac/hashlib from the documented
// provider byte formulas, independently of Hookway. The newline, space and UTF-8
// make accidental JSON reserialization observable.
func TestProviderIndependentSignatureVectors(t *testing.T) {
	for _, v := range providerVectors(t) {
		t.Run(v.Provider, func(t *testing.T) {
			verifier, err := auth.NewVerifier(providerSpec(v.Provider, v.Secret))
			if err != nil {
				t.Fatal(err)
			}
			ts, _ := strconv.ParseInt(v.Timestamp, 10, 64)
			input := auth.Input{Signature: v.Signature, Timestamp: v.Timestamp, MessageID: v.MessageID, Body: []byte(v.Body)}
			verify := func(in auth.Input, clock time.Time) error {
				if rv, ok := verifier.(auth.RequestVerifier); ok {
					return rv.VerifyRequest(context.Background(), in, clock)
				}
				return verifier.Verify(in.Signature, in.Body, clock)
			}
			now := time.Unix(ts, 0)
			if err = verify(input, now); err != nil {
				t.Fatal(err)
			}
			tampered := input
			tampered.Body = bytes.TrimSpace(input.Body)
			if verify(tampered, now) == nil {
				t.Fatal("reserialized bytes authenticated")
			}
			if v.Provider != "github" {
				if verify(input, now.Add(10*time.Minute)) == nil || verify(input, now.Add(-10*time.Minute)) == nil {
					t.Fatal("out-of-window signature authenticated")
				}
			}
			if v.Provider == "stripe" || v.Provider == "paddle" {
				sep, label, stamp := ",", "v1=", "t="
				if v.Provider == "paddle" {
					sep, label, stamp = ";", "h1=", "ts="
				}
				rotated := input
				rotated.Signature = strings.Replace(input.Signature, label, label+strings.Repeat("0", 64)+sep+label, 1)
				if err = verify(rotated, now); err != nil {
					t.Fatalf("valid second rotation signature rejected: %v", err)
				}
				rotated.Signature = input.Signature + sep + stamp + v.Timestamp
				if verify(rotated, now) == nil {
					t.Fatal("ambiguous duplicate timestamp accepted")
				}
				rotated.Signature = input.Signature + strings.Repeat(sep+label+strings.Repeat("0", 64), 16)
				if verify(rotated, now) == nil {
					t.Fatal("unbounded signature candidates")
				}
			}
		})
	}
}

// This signer implements the documented byte formulas directly; it deliberately
// never calls the production auth package or any production signing helper.
func providerRequest(provider, secret, body string, stamp time.Time) *http.Request {
	ts := strconv.FormatInt(stamp.Unix(), 10)
	prefix, header := "", "X-Hub-Signature-256"
	switch provider {
	case "stripe":
		prefix, header = ts+".", "Stripe-Signature"
	case "paddle":
		prefix, header = ts+":", "Paddle-Signature"
	case "slack":
		prefix, header = "v0:"+ts+":", "X-Slack-Signature"
	case "polar", "polar_standard":
		prefix, header = "polar-event-1."+ts+".", "webhook-signature"
	}
	key := []byte(secret)
	if provider == "polar_standard" {
		key, _ = base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(prefix + body))
	digest := hex.EncodeToString(mac.Sum(nil))
	signature := "sha256=" + digest
	switch provider {
	case "stripe":
		signature = "t=" + ts + ",v1=" + digest
	case "paddle":
		signature = "ts=" + ts + ";h1=" + digest
	case "slack":
		signature = "v0=" + digest
	case "polar", "polar_standard":
		signature = "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/"+provider, strings.NewReader(body))
	if provider == "polar_standard" {
		req.URL.Path = "/webhooks/polar"
	}
	req.Header.Set(header, signature)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-GitHub-Delivery", "delivery-stable")
	if provider == "polar" || provider == "polar_standard" {
		req.Header.Set("webhook-id", "polar-event-1")
		req.Header.Set("webhook-timestamp", ts)
	}
	return req
}
func providerRecipe(t *testing.T) (string, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("../../examples/providers/webhooks.yaml")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	for _, p := range []string{"github", "stripe", "paddle", "slack", "polar"} {
		path := filepath.Join(t.TempDir(), "secret")
		if err = os.WriteFile(path, []byte("current"), 0600); err != nil {
			t.Fatal(err)
		}
		paths[p] = path
		t.Setenv(strings.ToUpper(p)+"_SECRET_FILE", path)
	}
	return os.ExpandEnv(string(raw)), paths
}
func TestProviderRecipesThroughFlowServer(t *testing.T) {
	yaml, _ := providerRecipe(t)
	for _, provider := range []string{"github", "stripe", "paddle", "slack", "polar"} {
		t.Run(provider, func(t *testing.T) {
			pub := &testPublisher{}
			srv := newServer(t, yaml, pub)
			body := `{"id":"evt_1", "event_id":"Ev-1", "type":"event_callback"}` + "\n"
			now := time.Now()
			request := func(secret string, stamp time.Time) *http.Request {
				return providerRequest(provider, secret, body, stamp)
			}

			if got := post(srv, request("wrong", now)).Code; got != 401 {
				t.Fatalf("wrong key: %d", got)
			}
			tampered := request("current", now)
			tampered.Body = io.NopCloser(strings.NewReader(strings.TrimSpace(body)))
			tampered.ContentLength = int64(len(strings.TrimSpace(body)))
			if got := post(srv, tampered).Code; got != 401 {
				t.Fatalf("changed raw bytes: %d", got)
			}
			start := time.Now()
			if got := post(srv, request("current", now)).Code; got != 200 {
				t.Fatalf("valid delivery: %d", got)
			}
			if time.Since(start) > time.Second {
				t.Fatal("local immediate publisher exceeded 1s; deployment latency remains a separate gate")
			}
			if got := post(srv, request("current", time.Now())).Code; got != 200 {
				t.Fatalf("duplicate delivery: %d", got)
			}
			if pub.published != 2 {
				t.Fatalf("consumer must see duplicates for durable deduplication, got %d", pub.published)
			}
			pub.err = errors.New("broker unavailable")
			if got := post(srv, request("current", time.Now())).Code; got != 503 {
				t.Fatalf("broker failure: %d", got)
			}
			pub.err = nil
			if got := post(srv, request("current", time.Now())).Code; got != 200 {
				t.Fatalf("recovery: %d", got)
			}
			if provider != "github" && post(srv, request("current", time.Now().Add(-10*time.Minute))).Code != 401 {
				t.Fatal("stale signature accepted")
			}
		})
	}
}
func TestSlackSignedURLChallengeDoesNotPublish(t *testing.T) {
	yaml, _ := providerRecipe(t)
	pub := &testPublisher{}
	srv := newServer(t, yaml, pub)
	body := `{"type":"url_verification","challenge":"challenge-42"}`
	rec := post(srv, providerRequest("slack", "current", body, time.Now()))
	if rec.Code != 200 || rec.Body.String() != "challenge-42" || pub.published != 0 {
		t.Fatalf("challenge: %d %q publishes=%d", rec.Code, rec.Body.String(), pub.published)
	}
	if got := post(srv, providerRequest("slack", "wrong", body, time.Now())).Code; got != 401 {
		t.Fatalf("unsigned challenge: %d", got)
	}
}
func TestProviderSecretFileRotation(t *testing.T) {
	yaml, paths := providerRecipe(t)
	yaml = strings.ReplaceAll(yaml, "refresh: 1m, overlap: 10m", "refresh: 10ms, overlap: 150ms")
	cfg, issues := flowconfig.LoadBytes([]byte(yaml), "provider rotation", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	srv, err := httpapi.NewFlowServer(cfg, &testPublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	body := `{"id":"evt_1","type":"event_callback"}`
	for provider, path := range paths {
		rotated := "rotated"
		mode := provider
		if provider == "polar" {
			rotated = "whsec_" + base64.StdEncoding.EncodeToString([]byte("rotated-polar-standard-key"))
			mode = "polar_standard"
		}
		if err = os.WriteFile(path, []byte(rotated), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for post(srv, providerRequest(mode, rotated, body, time.Now())).Code != 200 {
			if time.Now().After(deadline) {
				t.Fatal("new signing secret never activated for", provider)
			}
			time.Sleep(5 * time.Millisecond)
		}
		if got := post(srv, providerRequest(provider, "current", body, time.Now())).Code; got != 200 {
			t.Fatalf("old key absent during overlap for %s: %d", provider, got)
		}
		for post(srv, providerRequest(provider, "current", body, time.Now())).Code == 200 {
			if time.Now().After(deadline) {
				t.Fatal("old signing secret did not expire for", provider)
			}
			time.Sleep(5 * time.Millisecond)
		}
		if got := post(srv, providerRequest(mode, rotated, body, time.Now())).Code; got != 200 {
			t.Fatalf("new key lost for %s: %d", provider, got)
		}
	}
}

func TestProviderAcknowledgementBudget(t *testing.T) {
	yaml, _ := providerRecipe(t)
	for _, provider := range []string{"github", "stripe", "paddle", "slack", "polar"} {
		t.Run(provider, func(t *testing.T) {
			pub := &testPublisher{delay: 3 * time.Second}
			srv := newServer(t, yaml, pub)
			started := time.Now()
			rec := post(srv, providerRequest(provider, "current", `{"id":"evt_1","type":"event_callback"}`, time.Now()))
			if rec.Code != 503 || pub.published != 0 || !pub.sawCancel.Load() {
				t.Fatalf("expired publication: status=%d published=%d canceled=%v", rec.Code, pub.published, pub.sawCancel.Load())
			}
			if elapsed := time.Since(started); elapsed > 2800*time.Millisecond {
				t.Fatalf("2s budget took %s", elapsed)
			}
		})
	}
}
