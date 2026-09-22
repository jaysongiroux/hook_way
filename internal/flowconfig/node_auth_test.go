package flowconfig

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func mustAuthNode(t *testing.T, body string) graph.Node {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("auth")
	node, err := spec.New(nodeConfigFromYAML(t, "auth", body))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestAuthVerifiesRawBytesNotReserialized(t *testing.T) {
	const secret = "shh"
	raw := []byte("{\n  \"z\": 1,\n  \"a\": 2\n}")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	node := mustAuthNode(t, "scheme: hmac_sha256_hex\nsecret: shh\nsignature_header: X-Sig\n")
	in := graph.RawRequest{
		Head: graph.RequestHead{Header: http.Header{"X-Sig": []string{sig}}},
		Body: raw,
	}
	out, err := node.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run() = %v; I6 requires verification against the raw bytes", err)
	}
	if out.PortType() != graph.PortVerified {
		t.Errorf("PortType() = %s, want Verified", out.PortType())
	}
}

func TestAuthUnknownSchemeFailsClosed(t *testing.T) {
	reg, _ := NewRegistry()
	spec, _ := reg.Lookup("auth")
	node, err := spec.New(nodeConfigFromYAML(t, "auth",
		"scheme: not_a_real_scheme\nsecret: s\nsignature_header: X-S\n"))
	if err != nil {
		return
	}
	if _, err := node.Run(context.Background(), graph.RawRequest{}); err == nil {
		t.Fatal("unknown scheme accepted a request; must fail closed (I11)")
	}
}

func TestAuthInvalidSignatureDoesNotLeakSecret(t *testing.T) {
	node := mustAuthNode(t, "scheme: hmac_sha256_hex\nsecret: super-secret-value\nsignature_header: X-Sig\n")
	_, err := node.Run(context.Background(), graph.RawRequest{
		Head: graph.RequestHead{Header: http.Header{"X-Sig": []string{"sha256=deadbeef"}}},
		Body: []byte(`{"ok":true}`),
	})
	if err == nil {
		t.Fatal("invalid signature accepted")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("error leaked secret: %v", err)
	}
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != http.StatusUnauthorized {
		t.Errorf("status = %+v, want 401", err)
	}
}

func TestAuthQueryParamSource(t *testing.T) {
	const secret = "shh"
	raw := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	node := mustAuthNode(t, "scheme: hmac_sha256_hex\nsecret: shh\nsource: {kind: query, key: authID}\n")
	_, err := node.Run(context.Background(), graph.RawRequest{
		Head: graph.RequestHead{Query: url.Values{"authID": []string{sig}}},
		Body: raw,
	})
	if err != nil {
		t.Fatalf("query-param auth: %v", err)
	}
}

func TestNoAuthProducesVerified(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("no_auth")
	node, err := spec.New(nodeConfigFromYAML(t, "no_auth", ""))
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.RawRequest{Body: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if out.PortType() != graph.PortVerified {
		t.Errorf("PortType() = %s, want Verified", out.PortType())
	}
}

func TestAuthSecretRefAcceptsAnyCurrentSecret(t *testing.T) {
	const current, previous = "new-secret", "old-secret"
	raw := []byte(`{"ok":true}`)
	sign := func(secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(raw)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	reg, err := NewRegistryWithOptions(RegistryOptions{
		Secrets: func(SecretRefOptions) (SecretsFunc, error) {
			return func(context.Context) ([][]byte, error) {
				return [][]byte{[]byte(current), []byte(previous)}, nil
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("auth")
	node, err := spec.New(nodeConfigFromYAML(t, "auth",
		"scheme: hmac_sha256_hex\nsecret_ref: {provider: env, name: IGNORED}\nsignature_header: X-Sig\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{current, previous} {
		_, err := node.Run(context.Background(), graph.RawRequest{
			Head: graph.RequestHead{Header: http.Header{"X-Sig": []string{sign(secret)}}},
			Body: raw,
		})
		if err != nil {
			t.Fatalf("secret %q rejected during overlap: %v", secret, err)
		}
	}
	_, err = node.Run(context.Background(), graph.RawRequest{
		Head: graph.RequestHead{Header: http.Header{"X-Sig": []string{sign("other")}}},
		Body: raw,
	})
	if err == nil {
		t.Fatal("unknown secret accepted")
	}
}

func TestReviewAuthRequiresExplicitTrustAndJWTBodyBinding(t *testing.T) {
	for _, scheme := range []string{"no_auth", "jwt_hs256", "jwt_rs256", "jwt_es256"} {
		if err := validateAuth(AuthOptions{Scheme: scheme, Secret: "s"}); err == nil {
			t.Fatalf("accepted %s without explicit trust/body binding", scheme)
		}
	}
	if err := validateAuth(AuthOptions{Scheme: "jwt_hs256", Secret: "s", BodyHashClaim: "body_sha256"}); err != nil {
		t.Fatal(err)
	}
}
