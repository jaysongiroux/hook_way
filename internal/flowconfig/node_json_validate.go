package flowconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

type jsonValidateNode struct {
	id        string
	validator *transform.Validator
}

func newJSONValidateNode(id string, opts JSONValidateOptions) (graph.Node, error) {
	validator, err := transform.NewValidator(opts.Schema, opts.Coercions)
	if err != nil {
		return nil, err
	}
	return jsonValidateNode{id: id, validator: validator}, nil
}
func (n jsonValidateNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusInternalServerError, Err: fmt.Errorf("json_validate %q: expected Envelope, got %T", n.id, in)}
	}
	body, err := validateJSONEnvelope(n.validator, env)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	env.Body = body
	return env, nil
}
func validateJSONEnvelope(validator *transform.Validator, env graph.Envelope) (any, error) {
	if env.BodyFormat != "" && env.BodyFormat != "json" {
		return nil, errors.New("JSON validation requires a JSON envelope body")
	}
	// The envelope adapter retains malformed input as raw bytes and leaves Body
	// nil. A parsed JSON null is valid; other upstream nodes may also emit null.
	if env.Body == nil && (env.BodyFormat == "json" || len(env.V.Raw.Body) > 0) && !json.Valid(env.V.Raw.Body) {
		return nil, errors.New("invalid JSON body")
	}
	return validator.Validate(env.Body)
}
