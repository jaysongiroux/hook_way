package flowconfig

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/jasongiroux/hookway/internal/graph"
)

type bodyReaderKey struct{}

// WithBodyReader attaches the request body to ctx for the read_body node.
// flowserver.go calls this; nothing else should.
func WithBodyReader(ctx context.Context, r io.Reader) context.Context {
	return context.WithValue(ctx, bodyReaderKey{}, r)
}

type readBodyNode struct {
	id       string
	maxBytes int64
}

func newReadBody(id string, opts ReadBodyOptions) graph.Node {
	return readBodyNode{id: id, maxBytes: opts.MaxBytes}
}

func (n readBodyNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	head, ok := in.(graph.RequestHead)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("read_body %q: expected RequestHead, got %T", n.id, in),
		}
	}
	reader, _ := ctx.Value(bodyReaderKey{}).(io.Reader)
	if reader == nil {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("read_body %q: no body reader in context", n.id),
		}
	}
	limited := io.LimitReader(reader, n.maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err: fmt.Errorf("read_body %q: %w", n.id, err),
		}
	}
	if int64(len(body)) > n.maxBytes {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusRequestEntityTooLarge,
			Err: fmt.Errorf("body exceeds max_bytes %d", n.maxBytes),
		}
	}
	if len(body) == 0 {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err:    fmt.Errorf("empty body"),
		}
	}
	return graph.RawRequest{Head: head, Body: body}, nil
}
