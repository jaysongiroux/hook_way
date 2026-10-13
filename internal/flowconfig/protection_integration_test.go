package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/protection"
)

const verifiedQuotaYAML = `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1024}
destinations: {primary: {type: webhook, webhook: {url: "https://example.test"}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: jwt_hs256, secret: secret, signature_header: Authorization, body_hash_claim: body_hash, credential_id: credential, expected_issuer: issuer, expected_audience: hookway, tenant_claim: tenant}
      - {id: quota, type: rate_limit, store: test, stage: verified, key_by: jwt_subject, rpm: 60, burst: 1}
      - {id: env, type: envelope}
      - {id: ok, type: respond, status: 200}
    edges: [in>body, body>auth, auth>quota, quota>env, env>ok]
`

func TestProtectionPoliciesRequireStoresAndVerifiedTrust(t *testing.T) {
	cfg, issues := LoadBytes([]byte(verifiedQuotaYAML), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	policies, err := ProtectionPolicies(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies["test"]) != 1 || policies["test"][0].ID != "flow/demo/node/quota" {
		t.Fatal(policies)
	}
	for _, src := range []string{
		strings.Replace(verifiedQuotaYAML, "store: test, ", "", 1),
		strings.Replace(verifiedQuotaYAML, "store: test", "store: missing", 1),
		strings.Replace(verifiedQuotaYAML, "credential_id: credential, ", "", 1),
		strings.Replace(verifiedQuotaYAML, "expected_issuer: issuer, ", "", 1),
		strings.Replace(verifiedQuotaYAML, "expected_audience: hookway, ", "", 1),
		strings.Replace(strings.Replace(verifiedQuotaYAML, "key_by: jwt_subject", "key_by: jwt_tenant", 1), ", tenant_claim: tenant", "", 1),
		strings.Replace(verifiedQuotaYAML, "stage: verified", "stage: request_head", 1),
	} {
		_, issues := LoadBytes([]byte(src), "test", false)
		if len(issues) == 0 {
			t.Fatalf("invalid quota accepted: %s", src)
		}
	}
	if schema := NodeSchemas()["rate_limit"]; schema.StagePorts["verified"].Input != "Verified" || schema.StagePorts["verified"].Output != "Verified" {
		t.Fatal(schema)
	}
}

func TestSharedProtectionPolicyRejectsLimitAndTrustMismatch(t *testing.T) {
	src := strings.Replace(verifiedQuotaYAML, "store: test, stage:", "store: test, policy_id: shared, stage:", 1)
	cfg, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	cfg.Flows["second"] = cfg.Flows["demo"]
	policies, err := ProtectionPolicies(cfg)
	if err != nil || len(policies["test"]) != 1 {
		t.Fatalf("%+v %v", policies, err)
	}
	for _, other := range []string{strings.Replace(src, "rpm: 60", "rpm: 61", 1), strings.Replace(src, "expected_issuer: issuer", "expected_issuer: other", 1)} {
		second, issues := LoadBytes([]byte(other), "test", false)
		if len(issues) > 0 {
			t.Fatal(issues)
		}
		cfg.Flows["second"] = second.Flows["demo"]
		if _, err := ProtectionPolicies(cfg); err == nil {
			t.Fatal("shared policy mismatch accepted")
		}
	}
}

func TestRateLimitMissingKeyNeverFallsBackAndStoreFailureIsCritical(t *testing.T) {
	store := protection.NewMemory("tests")
	defer func() { _ = store.Close() }()
	o := RateLimitOptions{Store: "test", KeyBy: "header.X-Identity", RPM: 60, Burst: 1}
	p := ratePolicy("demo", "quota", o)
	if err := store.EnsurePolicy(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	opts := RegistryOptions{FlowID: "demo", Protection: func(string) protection.Store { return store }}
	node, err := bindRateLimit("quota", o, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []http.Header{nil, {"X-Identity": []string{"a", "b"}}} {
		_, err := node.Run(context.Background(), graph.RequestHead{SourceIP: "192.0.2.1", Header: header})
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 400 || ne.Reason != "protection_key_missing" {
			t.Fatal(err)
		}
	}
	if _, err := node.Run(context.Background(), graph.RequestHead{Header: http.Header{"x-identity": []string{"a"}}}); err != nil {
		t.Fatalf("case-insensitive identity header: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.RequestHead{Header: http.Header{"X-Identity": []string{"a"}}})
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 503 || !ne.AbortRecovery {
		t.Fatal(err)
	}
}

func TestVerifiedQuotaSeparatesSubjectsAndRejectsAbsentClaim(t *testing.T) {
	cfg, issues := LoadBytes([]byte(verifiedQuotaYAML), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	policies, err := ProtectionPolicies(cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := protection.NewMemory("tests")
	defer func() { _ = store.Close() }()
	for _, p := range policies["test"] {
		if err := store.EnsurePolicy(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	node, err := bindRateLimit("quota", RateLimitOptions{Store: "test", Stage: "verified", KeyBy: "jwt_subject", RPM: 60, Burst: 1}, RegistryOptions{FlowID: "demo", ProtectionPolicies: policies, Protection: func(string) protection.Store { return store }})
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{"alice", "bob"} {
		v := graph.Verified{CredentialID: "credential", TrustScope: "trusted", Subject: subject}
		out, err := node.Run(context.Background(), v)
		if err != nil || out.PortType() != graph.PortVerified {
			t.Fatalf("%v %v", out, err)
		}
		if _, err = node.Run(context.Background(), v); err == nil {
			t.Fatal("subject quota ignored")
		}
	}
	_, err = node.Run(context.Background(), graph.Verified{CredentialID: "credential", TrustScope: "trusted"})
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 400 {
		t.Fatal(err)
	}
}

func TestReplayGuardOwnershipSurvivesExpiredOriginal(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1ms\nmax_entries: 100\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte("event")}}
	original, err := n.Run(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	// New reservations get a longer TTL while the expired owner's reservation
	// remains expired. Avoid making ownership assertions race a 1 ms TTL.
	next := n.policy
	next.TTL = time.Minute
	if err := n.store.UpdatePolicy(context.Background(), next, n.policy.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	n.policy = next
	replacement, err := n.Run(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	if original.(graph.Verified).Reservations[n.id].Owner == replacement.(graph.Verified).Reservations[n.id].Owner {
		t.Fatal("reservation owner reused")
	}
	if err := n.Compensate(context.Background(), original, errors.New("original failed")); err != nil {
		t.Fatal(err)
	}
	_, err = n.Run(context.Background(), v)
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 409 {
		t.Fatalf("old owner released replacement: %v", err)
	}
}

func TestProtectionRecoveryCannotDispatchOutboundWork(t *testing.T) {
	src := strings.Replace(verifiedQuotaYAML, "      - {id: env", "      - {id: fallback, type: publish, destination: primary, target: failed}\n      - {id: env", 1)
	src = strings.Replace(src, "quota>env,", "quota>env, quota:on_error>fallback,", 1)
	// The destination consumes Envelope, so use a verified -> envelope bridge.
	src = strings.Replace(src, "quota:on_error>fallback", "quota:on_error>env2, env2>fallback", 1)
	src = strings.Replace(src, "      - {id: fallback", "      - {id: env2, type: envelope}\n      - {id: fallback", 1)
	_, issues := LoadBytes([]byte(src), "test", false)
	if !strings.Contains(issuesText(issues), "cannot bypass admission") {
		t.Fatal(issues)
	}
}

type selectedIdentityVerifier struct{ calls int }

func (v *selectedIdentityVerifier) Verify(string, []byte, time.Time) error {
	return errors.New("legacy verification must not run")
}
func (v *selectedIdentityVerifier) VerifyIdentity(_ string, _ []byte, _ time.Time, selector string) (auth.VerifiedIdentity, error) {
	v.calls++
	if selector != "tenant" {
		return auth.VerifiedIdentity{}, errors.New("wrong selector")
	}
	return auth.VerifiedIdentity{Subject: "subject", Tenant: "tenant", Issuer: "issuer"}, nil
}
func TestAuthPropagatesSelectedIdentityAfterOneVerification(t *testing.T) {
	verifier := &selectedIdentityVerifier{}
	o := AuthOptions{Scheme: auth.SchemeJWTHS256, CredentialID: "credential", TenantClaim: "tenant", ExpectedIssuer: "issuer", ExpectedAudience: "hookway"}
	n := &authNode{id: "auth", opts: o, verifier: verifier, header: "Authorization"}
	out, err := n.Run(context.Background(), graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"Authorization": []string{"opaque token"}}}})
	if err != nil {
		t.Fatal(err)
	}
	v := out.(graph.Verified)
	if verifier.calls != 1 || v.CredentialID != "credential" || v.Subject != "subject" || v.Tenant != "tenant" || v.Issuer != "issuer" || v.TrustScope != authTrustScope(o) {
		t.Fatal(v, verifier.calls)
	}
	rotated := o
	rotated.Secret = "replacement-secret"
	if authTrustScope(rotated) != v.TrustScope {
		t.Fatal("credential rotation reset quota trust")
	}
}
