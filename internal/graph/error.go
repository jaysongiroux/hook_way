package graph

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
