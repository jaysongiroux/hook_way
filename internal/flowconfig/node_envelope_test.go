package flowconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
)

func mustEnvelopeNode(t *testing.T, body string) graph.Node {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("envelope")
	node, err := spec.New(nodeConfigFromYAML(t, "envelope", body))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestEnvelopeJSONParsesBody(t *testing.T) {
	node := mustEnvelopeNode(t, "body_format: json\n")
	out, err := node.Run(context.Background(), graph.Verified{
		Raw: graph.RawRequest{Body: []byte(`{"action":"opened"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	body, ok := env.Body.(map[string]any)
	if !ok || body["action"] != "opened" {
		t.Fatalf("Body = %#v", env.Body)
	}
}

func TestEnvelopeJSONNonJSONDoesNotError(t *testing.T) {
	node := mustEnvelopeNode(t, "body_format: json\n")
	out, err := node.Run(context.Background(), graph.Verified{
		Raw: graph.RawRequest{Body: []byte("not-json")},
	})
	if err != nil {
		t.Fatalf("non-JSON body should not error (V1 body_unparsed path): %v", err)
	}
	env := out.(graph.Envelope)
	if env.Body != nil {
		t.Errorf("Body = %#v, want nil for unparsed JSON", env.Body)
	}
}

func TestEnvelopeRawPreservesBytes(t *testing.T) {
	node := mustEnvelopeNode(t, "body_format: raw\n")
	raw := []byte("not json at all")
	out, err := node.Run(context.Background(), graph.Verified{
		Raw: graph.RawRequest{Body: raw},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	if env.Body != nil {
		t.Errorf("raw Body = %#v, want nil", env.Body)
	}
	cap, ok := env.Extra.(CapturedEvent)
	if !ok || string(cap.Event.RawBody) != string(raw) {
		t.Fatalf("raw bytes not preserved: %#v", env.Extra)
	}
}

func TestEnvelopeXMLNotImplemented(t *testing.T) {
	node := mustEnvelopeNode(t, "body_format: xml\n")
	_, err := node.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: []byte("<a/>")}})
	if err == nil || !errors.Is(err, errNotImplemented) || !strings.Contains(err.Error(), "xml") {
		t.Fatalf("xml = %v, want errNotImplemented naming xml", err)
	}
}
