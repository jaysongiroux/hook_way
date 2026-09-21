package graph

import "fmt"

// NodeError wraps a node failure with the HTTP status the server should
// return. Nodes return these so the server does not string-match error text.
type NodeError struct {
	NodeID string
	Status int
	Err    error
}

func (e NodeError) Error() string {
	if e.Err == nil {
		return "graph: node error"
	}
	return e.Err.Error()
}

func (e NodeError) Unwrap() error { return e.Err }

// PanicError is a recovered node panic. Observers distinguish it from a
// normal node error so hookway_panics_total{location="node"} can increment
// separately from handler panic telemetry.
type PanicError struct {
	NodeID string
	Value  any
}

func (e PanicError) Error() string {
	return fmt.Sprintf("panicked: %v", e.Value)
}

func (e PanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}
