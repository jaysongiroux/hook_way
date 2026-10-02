package flowconfig

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

func mustAuthNode(t *testing.T, body string) graph.Node {
	t.Helper()
	reg, err := NewRegistryWithOptions(RegistryOptions{})
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
	reg, _ := NewRegistryWithOptions(RegistryOptions{})
	spec, _ := reg.Lookup("auth")
	_, err := spec.New(nodeConfigFromYAML(t, "auth", "scheme: not_a_real_scheme\nsecret: s\nsignature_header: X-S\n"))
	if err == nil {
		t.Fatal("unknown scheme must fail compilation")
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
	reg, err := NewRegistryWithOptions(RegistryOptions{})
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

func TestConcurrentMultiSignatureAuthenticationDuringRotation(t *testing.T) {
	var keys atomic.Value
	keys.Store([][]byte{[]byte("new"), []byte("old")})
	n, err := newAuthNode("auth", AuthOptions{Scheme: "hmac_sha256_ts_dot_multi", SignatureHeader: "X-Sig", TimestampUnit: "seconds"}, func(context.Context) ([][]byte, error) { return keys.Load().([][]byte), nil })
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sign := func(secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + ".raw"))
		return "t=" + ts + ",s=bad,s=" + hex.EncodeToString(mac.Sum(nil))
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := n.Run(context.Background(), graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"X-Sig": {sign("old")}}}, Body: []byte("raw")}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			if j%2 == 0 {
				keys.Store([][]byte{[]byte("old"), []byte("new")})
			} else {
				keys.Store([][]byte{[]byte("new"), []byte("old")})
			}
		}
	}()
	wg.Wait()
	keys.Store([][]byte{[]byte("new")})
	if _, err := n.Run(context.Background(), graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"X-Sig": {sign("old")}}}, Body: []byte("raw")}); err == nil {
		t.Fatal("expired rotation key accepted")
	}
}

func TestSignedRepresentationDeadlineReturnsRetryableFailure(t *testing.T) {
	node := mustAuthNode(t, "scheme: hmac_sha256_headers_dot\nsecret: synthetic\nsignature_header: X-Sig\ntimestamp_header: X-Time\ntimestamp_unit: milliseconds\ntimestamp_policy: age\ntimestamp_tolerance: 1m\nbody_representation: json_js\n")
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte("synthetic"))
	mac.Write([]byte(ts + ".{}"))
	input := graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"X-Sig": {hex.EncodeToString(mac.Sum(nil))}, "X-Time": {ts}}}, Body: []byte(`{}`)}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	out, err := node.Run(ctx, input)
	var ne graph.NodeError
	if out != nil || !errors.As(err, &ne) || ne.Status != http.StatusServiceUnavailable || ne.Reason != "auth_unavailable" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("temporary failure = %v, output = %v", err, out)
	}
	// Malformed JSON remains a permanent authentication failure.
	input.Body = []byte("bad")
	out, err = node.Run(context.Background(), input)
	if out != nil || !errors.As(err, &ne) || ne.Status != http.StatusUnauthorized {
		t.Fatalf("invalid body = %v, output = %v", err, out)
	}
}

type unavailableAuthVerifier struct {
	calls *atomic.Int32
	err   error
}

func (v unavailableAuthVerifier) Verify(_ string, _ []byte, _ time.Time) error {
	v.calls.Add(1)
	return v.err
}
func TestAuthRotationDoesNotRepeatUnavailableRepresentationWork(t *testing.T) {
	for _, tc := range []struct {
		err       error
		wantCalls int32
	}{{auth.ErrVerificationUnavailable, 1}, {auth.ErrUnauthorized, 2}} {
		var calls atomic.Int32
		v := unavailableAuthVerifier{calls: &calls, err: tc.err}
		n := &authNode{secrets: func(context.Context) ([][]byte, error) { return [][]byte{[]byte("first"), []byte("second")}, nil }, cached: map[string]auth.Verifier{"first": v, "second": v}}
		if err := n.verify(context.Background(), auth.Input{}); !errors.Is(err, tc.err) || calls.Load() != tc.wantCalls {
			t.Fatalf("rotation result = %v, attempts = %d", err, calls.Load())
		}
	}
}
