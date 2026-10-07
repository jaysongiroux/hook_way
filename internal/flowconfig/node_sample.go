package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

type sampleNode struct {
	id   string
	rate float64
	draw func() float64
}

func validateSample(o SampleOptions) error {
	if o.Rate == nil {
		return errors.New("rate is required")
	}
	if math.IsNaN(*o.Rate) || math.IsInf(*o.Rate, 0) || *o.Rate < 0 || *o.Rate > 1 {
		return errors.New("rate must be a finite number between 0 and 1 inclusive")
	}
	return nil
}

func newSampleNode(id string, o SampleOptions) graph.Node {
	return sampleNode{id: id, rate: *o.Rate, draw: rand.Float64}
}

func (n sampleNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("sample %q: expected Envelope, got %T", n.id, in),
		}
	}
	// Sampling is traffic selection, not a security decision. The package-level
	// random source is safe for concurrent requests; no request state is retained.
	if n.rate == 1 || (n.rate > 0 && n.draw() < n.rate) {
		return env, nil
	}
	return graph.Terminal{DropReason: "sampled_out"}, nil
}
