package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/filter"
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
		t.Fatalf("non-JSON body should not error (body_unparsed path): %v", err)
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

func TestEnvelopeMultipartParsesIntoBody(t *testing.T) {
	ct, raw := buildMultipart(t,
		map[string]string{"event": "upload"},
		[]multipartFile{{field: "document", filename: "report.pdf", contentType: "application/pdf", content: []byte("PDF")}},
	)
	node := mustEnvelopeNode(t, "body_format: multipart\n")
	out, err := node.Run(context.Background(), multipartVerified(ct, raw))
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	body, ok := out.(graph.Envelope).Body.(map[string]any)
	if !ok {
		t.Fatalf("body = %T, want map[string]any", out.(graph.Envelope).Body)
	}
	doc := body["files"].(map[string]any)["document"].(map[string]any)
	if doc["filename"] != "report.pdf" || doc["content_type"] != "application/pdf" {
		t.Fatalf("files.document = %#v", doc)
	}
	if body["fields"].(map[string]any)["event"] != "upload" {
		t.Fatalf("fields = %#v", body["fields"])
	}
}

// The parsed body must be composed only of types graph's branch cloning
// accepts, which is the payoff for using plain maps instead of a custom
// struct. graph.clonePayload is unexported, so this asserts the invariant that
// makes cloning work rather than calling the cloner: cloneBody handles
// map[string]any and []any recursively and returns immutable scalars as-is
// (internal/graph/clone.go:86-118), and anything else needs a BodyCloner.
func TestEnvelopeMultipartBodyUsesOnlyCloneableTypes(t *testing.T) {
	ct, raw := buildMultipart(t,
		map[string]string{"account": "acct_1"},
		[]multipartFile{{field: "document", filename: "a.pdf", contentType: "application/pdf", content: []byte("x")}},
	)
	node := mustEnvelopeNode(t, "body_format: multipart\n")
	out, err := node.Run(context.Background(), multipartVerified(ct, raw))
	if err != nil {
		t.Fatal(err)
	}
	assertCloneableTypes(t, out.(graph.Envelope).Body, "body")
}

// assertCloneableTypes walks the parsed body and fails on any type that
// graph's cloneBody would reject.
func assertCloneableTypes(t *testing.T, v any, path string) {
	t.Helper()
	switch typed := v.(type) {
	case nil, string, bool, int, int64, float64:
		return
	case map[string]any:
		for k, child := range typed {
			assertCloneableTypes(t, child, path+"."+k)
		}
	case []any:
		for i, child := range typed {
			assertCloneableTypes(t, child, fmt.Sprintf("%s[%d]", path, i))
		}
	default:
		t.Fatalf("%s is %T, which graph.cloneBody would reject without a BodyCloner", path, v)
	}
}

func TestEnvelopeMultipartHonorsLimits(t *testing.T) {
	ct, raw := buildMultipart(t, nil, []multipartFile{
		{field: "a", filename: "a.txt", content: []byte("1")},
		{field: "b", filename: "b.txt", content: []byte("2")},
	})
	node := mustEnvelopeNode(t, "body_format: multipart\nmultipart:\n  max_parts: 1\n")
	_, err := node.Run(context.Background(), multipartVerified(ct, raw))
	var nodeErr graph.NodeError
	if !errors.As(err, &nodeErr) {
		t.Fatalf("Run() = %v, want a graph.NodeError", err)
	}
	if nodeErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 so the limit is distinguishable from malformed input", nodeErr.Status)
	}
}

func TestEnvelopeMultipartMalformedBodyIs400(t *testing.T) {
	node := mustEnvelopeNode(t, "body_format: multipart\n")
	_, err := node.Run(context.Background(), multipartVerified("application/json", []byte(`{"a":1}`)))
	var nodeErr graph.NodeError
	if !errors.As(err, &nodeErr) {
		t.Fatalf("Run() = %v, want a graph.NodeError", err)
	}
	if nodeErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", nodeErr.Status)
	}
}

func TestEnvelopeMultipartMetadataOnly(t *testing.T) {
	ct, raw := buildMultipart(t, nil,
		[]multipartFile{{field: "document", filename: "a.pdf", content: []byte("PDFPDFPDF")}},
	)
	node := mustEnvelopeNode(t, "body_format: multipart\nmultipart:\n  include_content: false\n")
	out, err := node.Run(context.Background(), multipartVerified(ct, raw))
	if err != nil {
		t.Fatal(err)
	}
	doc := out.(graph.Envelope).Body.(map[string]any)["files"].(map[string]any)["document"].(map[string]any)
	if _, exists := doc["content"]; exists {
		t.Fatal("content carried despite include_content: false")
	}
	if doc["size"] != int64(9) {
		t.Fatalf("size = %v, want 9", doc["size"])
	}
}

// The other formats must be untouched.
func TestEnvelopeJSONAndRawUnaffected(t *testing.T) {
	jsonNode := mustEnvelopeNode(t, "body_format: json\n")
	out, err := jsonNode.Run(context.Background(), multipartVerified("application/json", []byte(`{"n":1}`)))
	if err != nil {
		t.Fatalf("json envelope = %v", err)
	}
	if body, ok := out.(graph.Envelope).Body.(map[string]any); !ok || body["n"] != float64(1) {
		t.Fatalf("json body = %#v", out.(graph.Envelope).Body)
	}
	rawNode := mustEnvelopeNode(t, "body_format: raw\n")
	out, err = rawNode.Run(context.Background(), multipartVerified("application/octet-stream", []byte("bytes")))
	if err != nil {
		t.Fatalf("raw envelope = %v", err)
	}
	if out.(graph.Envelope).Body != nil {
		t.Fatalf("raw body = %#v, want nil", out.(graph.Envelope).Body)
	}
}

// multipartVerified builds the node's input: a verified request carrying the
// body bytes and the Content-Type the parser needs for its boundary.
func multipartVerified(contentType string, body []byte) graph.Verified {
	head := graph.RequestHead{
		Method: http.MethodPost,
		Path:   "/w",
		Header: http.Header{"Content-Type": []string{contentType}},
	}
	return graph.Verified{Raw: graph.RawRequest{Head: head, Body: body}, Identity: "test"}
}

// TestMultipartBodyIsFilterable is the contract in decision 3: the parsed shape
// must be reachable by the existing dotted-path filter, with no filter changes.
func TestMultipartBodyIsFilterable(t *testing.T) {
	ct, raw := buildMultipart(t, nil,
		[]multipartFile{{field: "document", filename: "a.pdf", contentType: "application/pdf", content: []byte("x")}},
	)
	node := mustEnvelopeNode(t, "body_format: multipart\n")
	out, err := node.Run(context.Background(), multipartVerified(ct, raw))
	if err != nil {
		t.Fatal(err)
	}
	chain, issues := filter.Compile([]filter.Spec{
		{Source: "body", Path: "files.document.content_type", Operator: "eq", Value: "application/pdf"},
	})
	if len(issues) > 0 {
		t.Fatalf("Compile issues: %+v", issues)
	}
	if ok, _ := chain.Match(http.Header{}, out.(graph.Envelope).Body); !ok {
		t.Fatal("filter did not match files.document.content_type; the parsed shape is not dotted-path addressable")
	}
}
