package graph

import (
	"errors"
	"testing"
)

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	spec := NodeSpec{
		Type: "read_body",
		In:   PortRequestHead,
		Out:  PortRawRequest,
		New:  func(NodeConfig) (Node, error) { return nil, nil },
	}
	if err := r.Register(spec); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	got, ok := r.Lookup("read_body")
	if !ok {
		t.Fatal("Lookup(read_body) not found after Register")
	}
	if got.In != PortRequestHead || got.Out != PortRawRequest {
		t.Errorf("spec ports = %s->%s, want RequestHead->RawRequest", got.In, got.Out)
	}
}

func TestRegistryRejectsDuplicateType(t *testing.T) {
	r := NewRegistry()
	spec := NodeSpec{Type: "auth", In: PortRawRequest, Out: PortVerified,
		New: func(NodeConfig) (Node, error) { return nil, nil }}
	if err := r.Register(spec); err != nil {
		t.Fatalf("first Register() = %v, want nil", err)
	}
	err := r.Register(spec)
	if !errors.Is(err, ErrDuplicateNodeType) {
		t.Errorf("second Register() = %v, want ErrDuplicateNodeType", err)
	}
}

func TestRegistryRejectsIncompleteSpec(t *testing.T) {
	cases := []struct {
		name string
		spec NodeSpec
	}{
		{"no type", NodeSpec{In: PortEnvelope, Out: PortTerminal,
			New: func(NodeConfig) (Node, error) { return nil, nil }}},
		{"no constructor", NodeSpec{Type: "sns", In: PortEnvelope, Out: PortTerminal}},
		{"none as output", NodeSpec{Type: "bad", In: PortEnvelope, Out: PortNone,
			New: func(NodeConfig) (Node, error) { return nil, nil }}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := NewRegistry().Register(tc.spec); err == nil {
				t.Error("Register() = nil, want error")
			}
		})
	}
}

func TestRegistryLookupMissing(t *testing.T) {
	if _, ok := NewRegistry().Lookup("nope"); ok {
		t.Error("Lookup(nope) found a spec in an empty registry")
	}
}
