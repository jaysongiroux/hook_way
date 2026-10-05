package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

type authNode struct {
	id               string
	opts             AuthOptions
	verifier         auth.Verifier
	secrets          SecretsFunc
	header, queryKey string
	mu               sync.Mutex
	cached           map[string]auth.Verifier
}

// ValidateAuthKey applies the same bounds at startup and every refresh.
func ValidateAuthKey(o AuthOptions, value []byte) error {
	if len(value) == 0 || len(value) > 65536 {
		return errors.New("key material must be nonempty and at most 64 KiB")
	}
	spec := AuthSpec(o)
	spec.Secret = string(value)
	if _, err := auth.NewVerifier(spec); err != nil {
		return errors.New("invalid authentication key material")
	}
	return nil
}

// AuthSpec is shared by initial binding and refresh validation.
func AuthSpec(o AuthOptions) auth.Spec {
	spec := auth.Spec{Scheme: defaultAuthScheme(o.Scheme), Secret: o.Secret,
		TimestampTolerance: 5 * time.Minute, RemoveBearer: o.RemoveBearer, RemovePrefix: o.RemovePrefix,
		ExpectedIssuer: o.ExpectedIssuer, ExpectedAudience: o.ExpectedAudience, BodyHashClaim: o.BodyHashClaim,
		TimestampUnit: o.TimestampUnit, TimestampPolicy: o.TimestampPolicy, BodyRepresentation: o.BodyRepresentation, SignatureLabel: o.SignatureLabel}
	if o.TimestampTolerance != "" {
		spec.TimestampTolerance, _ = time.ParseDuration(o.TimestampTolerance)
	}
	return spec
}

func newAuthNode(id string, opts AuthOptions, secrets SecretsFunc) (graph.Node, error) {
	header := opts.SignatureHeader
	queryKey := ""
	if opts.Source != nil {
		if opts.Source.Kind == "query" {
			queryKey = opts.Source.Key
		} else {
			header = opts.Source.Key
		}
	}
	n := &authNode{id: id, opts: opts, secrets: secrets, header: header, queryKey: queryKey, cached: map[string]auth.Verifier{}}
	if secrets == nil {
		if opts.SecretRef != nil {
			return nil, fmt.Errorf("auth %q: secret resolver is required", id)
		}
		if err := ValidateAuthKey(opts, []byte(opts.Secret)); err != nil {
			return nil, fmt.Errorf("auth %q: invalid key material", id)
		}
		v, err := auth.NewVerifier(AuthSpec(opts))
		if err != nil {
			return nil, fmt.Errorf("auth %q: invalid key material", id)
		}
		n.verifier = v
	} else {
		values, err := secrets(context.Background())
		if err != nil || len(values) == 0 || len(values) > 2 {
			return nil, fmt.Errorf("auth %q: secret unavailable", id)
		}
		if _, err = n.verifiers(values); err != nil {
			return nil, fmt.Errorf("auth %q: invalid key material", id)
		}
	}
	return n, nil
}

func defaultAuthScheme(scheme string) string {
	switch scheme {
	case "":
		return auth.SchemeHMACSHA256TSColon
	case "header_exact":
		return auth.SchemeExact
	default:
		return scheme
	}
}

func (n *authNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	raw, ok := in.(graph.RawRequest)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("auth %q: expected RawRequest, got %T", n.id, in),
		}
	}
	credential, err := n.credential(raw)
	request := auth.Input{Signature: credential, Timestamp: raw.Head.Header.Get(n.opts.TimestampHeader), Body: raw.Body}
	if err == nil && n.opts.BodyRepresentation == "json_js" {
		request = request.WithCanonicalCache()
	}
	var identity auth.VerifiedIdentity
	if err == nil {
		identity, err = n.verifyIdentity(ctx, request)
	}
	if err != nil {
		status, reason := http.StatusUnauthorized, "auth_error"
		if errors.Is(err, errAuthUnavailable) || errors.Is(err, auth.ErrVerificationUnavailable) {
			status, reason = http.StatusServiceUnavailable, "auth_unavailable"
		}
		return nil, graph.NodeError{NodeID: n.id, Status: status, Reason: reason, Err: err}
	}
	return graph.Verified{Raw: raw, Identity: n.opts.Scheme, CredentialID: n.opts.CredentialID, Subject: identity.Subject, Tenant: identity.Tenant, Issuer: identity.Issuer, TrustScope: authTrustScope(n.opts), OriginalJSON: request.PreparedRepresentation()}, nil
}

var errAuthUnavailable = errors.New("authentication key unavailable")

func (n *authNode) verifiers(values [][]byte) ([]auth.Verifier, error) {
	if len(values) == 0 || len(values) > 2 {
		return nil, errAuthUnavailable
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	current := make(map[string]auth.Verifier, len(values))
	out := make([]auth.Verifier, 0, len(values))
	for _, value := range values {
		if len(value) == 0 || len(value) > 65536 {
			return nil, errAuthUnavailable
		}
		key := string(value)
		v := n.cached[key]
		if v == nil {
			spec := AuthSpec(n.opts)
			spec.Secret = key
			var err error
			v, err = auth.NewVerifier(spec)
			if err != nil {
				return nil, errAuthUnavailable
			}
		}
		current[key] = v
		out = append(out, v)
	}
	n.cached = current // bounded to the active rotation window
	return out, nil
}
func (n *authNode) verifyIdentity(ctx context.Context, in auth.Input) (auth.VerifiedIdentity, error) {
	verifiers := []auth.Verifier{n.verifier}
	if n.secrets != nil {
		values, err := n.secrets(ctx)
		if err != nil {
			return auth.VerifiedIdentity{}, errAuthUnavailable
		}
		verifiers, err = n.verifiers(values)
		if err != nil {
			return auth.VerifiedIdentity{}, err
		}
	}
	now := time.Now()
	var last error
	for _, v := range verifiers {
		identity := auth.VerifiedIdentity{}
		if typed, ok := v.(auth.IdentityVerifier); ok {
			identity, last = typed.VerifyIdentity(in.Signature, in.Body, now, n.opts.TenantClaim)
		} else if typed, ok := v.(auth.RequestVerifier); ok {
			last = typed.VerifyRequest(ctx, in, now)
		} else {
			last = v.Verify(in.Signature, in.Body, now)
		}
		if last == nil {
			return identity, nil
		}
		if errors.Is(last, auth.ErrVerificationUnavailable) {
			return auth.VerifiedIdentity{}, last // Retrying representation work with another key cannot help.
		}
	}
	return auth.VerifiedIdentity{}, last
}
func (n *authNode) credential(raw graph.RawRequest) (string, error) {
	var values []string
	if n.queryKey != "" {
		values = raw.Head.Query[n.queryKey]
	} else if n.header != "" {
		values = raw.Head.Header.Values(n.header)
	}
	if len(values) > 1 {
		return "", auth.Failure{Reason: "duplicate_header"}
	}
	if n.opts.TimestampHeader != "" && len(raw.Head.Header.Values(n.opts.TimestampHeader)) > 1 {
		return "", auth.Failure{Reason: "duplicate_header"}
	}
	if len(values) == 0 || values[0] == "" {
		return "", auth.Failure{Reason: "missing_header"}
	}
	if len(values[0]) > auth.MaxCredentialBytes {
		return "", auth.Failure{Reason: "header_too_large"}
	}
	return values[0], nil
}

type noAuthNode struct{ id string }

func newNoAuthNode(id string, logger *slog.Logger) graph.Node {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("no_auth accepts every request unverified", "node_id", id)
	return noAuthNode{id: id}
}

func (n noAuthNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	raw, ok := in.(graph.RawRequest)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("no_auth %q: expected RawRequest, got %T", n.id, in),
		}
	}
	return graph.Verified{Raw: raw, Identity: auth.AuthTypeNoAuth}, nil
}
