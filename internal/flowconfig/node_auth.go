package flowconfig

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jasongiroux/hookway/internal/auth"
	"github.com/jasongiroux/hookway/internal/graph"
)

type authNode struct {
	id       string
	opts     AuthOptions
	verifier auth.Verifier
	header   string
	queryKey string
}

func newAuthNode(id string, opts AuthOptions, logger *slog.Logger) graph.Node {
	if logger == nil {
		logger = slog.Default()
	}
	header := opts.SignatureHeader
	queryKey := ""
	if opts.Source != nil {
		switch opts.Source.Kind {
		case "query":
			queryKey = opts.Source.Key
		case "header":
			header = opts.Source.Key
		}
	}
	spec := auth.Spec{
		Scheme:           defaultAuthScheme(opts.Scheme),
		Secret:           opts.Secret,
		RemoveBearer:     opts.RemoveBearer,
		ExpectedIssuer:   opts.ExpectedIssuer,
		ExpectedAudience: opts.ExpectedAudience,
		BodyHashClaim:    opts.BodyHashClaim,
	}
	if opts.TimestampTolerance != "" {
		if d, err := time.ParseDuration(opts.TimestampTolerance); err == nil {
			spec.TimestampTolerance = d
		}
	}
	verifier, err := auth.NewVerifier(spec)
	if err != nil {
		logger.Error("signature scheme unavailable; node will reject all requests",
			"node_id", id, "scheme", opts.Scheme, "error", err.Error())
		verifier = auth.RejectAll{}
	}
	return authNode{id: id, opts: opts, verifier: verifier, header: header, queryKey: queryKey}
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

func (n authNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	raw, ok := in.(graph.RawRequest)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("auth %q: expected RawRequest, got %T", n.id, in),
		}
	}
	credential := n.credential(raw)
	if err := n.verifier.Verify(credential, raw.Body, time.Now()); err != nil {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusUnauthorized,
			Err: auth.ErrUnauthorized,
		}
	}
	return graph.Verified{Raw: raw, Identity: n.opts.Scheme}, nil
}

func (n authNode) credential(raw graph.RawRequest) string {
	if n.queryKey != "" {
		return raw.Head.Query.Get(n.queryKey)
	}
	if n.header != "" {
		return raw.Head.Header.Get(n.header)
	}
	return ""
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
