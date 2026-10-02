package auth

import (
	"context"
	"sync"

	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
)

type bodyCache struct {
	mu             sync.Mutex
	representation *jsoncanon.Representation
}

// WithCanonicalCache creates an empty request-local cache. Canonicalization
// remains lazy, after the verifier's existing credential/timestamp checks.
// A prepared representation never makes an input authenticated.
func (in Input) WithCanonicalCache() Input {
	in.cache = &bodyCache{}
	return in
}

// PreparedRepresentation is serialization metadata, not authentication proof.
// The serving auth node attaches it to Verified only after verification succeeds.
func (in Input) PreparedRepresentation() *jsoncanon.Representation {
	if in.cache == nil {
		return nil
	}
	in.cache.mu.Lock()
	defer in.cache.mu.Unlock()
	return in.cache.representation
}

func (in Input) canonicalBody(ctx context.Context) ([]byte, error) {
	if in.cache == nil {
		return jsoncanon.Stringify(ctx, in.Body)
	}
	in.cache.mu.Lock()
	defer in.cache.mu.Unlock()
	if data, match, err := in.cache.representation.Bytes(ctx, in.Body); match || err != nil {
		return data, err
	}
	representation, err := jsoncanon.Prepare(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	in.cache.representation = representation
	data, _, err := representation.Bytes(ctx, in.Body)
	return data, err
}
