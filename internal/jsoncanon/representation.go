package jsoncanon

import (
	"context"
	"crypto/sha256"
)

// Representation is an immutable JS-compatible representation of exact
// original bytes. Its fields are private: it cannot be supplied over the wire
// or forged from a caller-provided canonical string. It is not proof of auth.
// Requests own these values; there is no global cache or shared runtime.
type Representation struct {
	source [sha256.Size]byte
	size   int
	text   string
}

// Prepare retains no reference to raw, which must remain immutable while it
// is read, just as for Stringify. Result bytes are immutable after construction.
func Prepare(ctx context.Context, raw []byte) (*Representation, error) {
	if len(raw) > MaxBytes || ctx.Err() != nil {
		_, err := Stringify(ctx, raw)
		return nil, err
	}
	source := sha256.Sum256(raw)
	out, err := Stringify(ctx, raw)
	if err != nil {
		return nil, err
	}
	return &Representation{source: source, size: len(raw), text: string(out)}, nil
}

// Bytes returns privately owned bytes only when the source still matches.
// Graph branches may share the immutable representation, never writable output.
func (r *Representation) Bytes(ctx context.Context, raw []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if r == nil || len(raw) != r.size || sha256.Sum256(raw) != r.source {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return []byte(r.text), true, nil
}
