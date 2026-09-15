package flowconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/transform"
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

func TestEnvelopeXMLParsesBodyAndKeepsRawBytes(t *testing.T) {
	raw := []byte(`<MESSAGE xmlns:xlink="http://www.w3.org/1999/xlink"><KEEP>ok</KEEP></MESSAGE>`)
	node := mustEnvelopeNode(t, "body_format: xml\n")
	out, err := node.Run(context.Background(), graph.Verified{
		Raw: graph.RawRequest{Body: raw},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	doc, ok := env.Body.(transform.Document)
	if !ok || doc.Root == nil || !strings.EqualFold(doc.Root.Name.Local, "MESSAGE") {
		t.Fatalf("Body = %#v, want XML Document", env.Body)
	}
	if string(env.V.Raw.Body) != string(raw) {
		t.Fatalf("raw bytes overwritten: %q", env.V.Raw.Body)
	}
	cap, ok := env.Extra.(CapturedEvent)
	if !ok || string(cap.Event.RawBody) != string(raw) {
		t.Fatalf("captured raw bytes missing: %#v", env.Extra)
	}
	if cap.Event.Body != nil {
		t.Fatalf("webhook envelope Body = %#v, want nil so XML is not JSON-published", cap.Event.Body)
	}
}

func TestEnvelopeXMLInvalidIs400(t *testing.T) {
	node := mustEnvelopeNode(t, "body_format: xml\n")
	_, err := node.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: []byte("<a>")}})
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != 400 {
		t.Fatalf("err = %v, want 400 invalid XML", err)
	}
}
