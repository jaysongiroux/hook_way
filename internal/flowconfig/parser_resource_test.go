package flowconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
)

func TestEnvelopeJSONBudgetsAndExplicitDepthOverride(t *testing.T) {
	deep := strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129)
	for _, tc := range []struct {
		body   string
		opts   EnvelopeOptions
		status int
	}{
		{deep, EnvelopeOptions{}, 413},
		{deep, EnvelopeOptions{JSON: JSONParseOptions{MaxDepth: 129}}, 0},
		{deep, EnvelopeOptions{BodyFormat: "raw"}, 0},
		{`{"a":1,"a":2}`, EnvelopeOptions{JSON: JSONParseOptions{MaxNodes: 2}}, 413},
		{`{"a":1,"a":2}`, EnvelopeOptions{JSON: JSONParseOptions{MaxNodes: 3}}, 0},
		{`not-json`, EnvelopeOptions{}, 0},
	} {
		n := newEnvelopeNode("envelope", tc.opts, nil, nil)
		out, err := n.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: []byte(tc.body)}})
		if tc.status != 0 {
			var nodeErr graph.NodeError
			if !errors.As(err, &nodeErr) || nodeErr.Status != tc.status || !errors.Is(err, jsonbudget.ErrLimit) {
				t.Fatalf("budget %v: %v", tc.opts, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		event := out.(graph.Envelope).Extra.(CapturedEvent).Event
		if !bytes.Equal(event.RawBody, []byte(tc.body)) {
			t.Fatal("wire bytes changed")
		}
		if tc.body == "not-json" && (event.RawBodyBase64 == nil || *event.RawBodyBase64 != base64.StdEncoding.EncodeToString([]byte(tc.body))) {
			t.Fatal("non-JSON fallback changed")
		}
	}
}

func TestEnvelopeJSONOptionsValidation(t *testing.T) {
	for _, opts := range []EnvelopeOptions{
		{JSON: JSONParseOptions{MaxDepth: -1}}, {JSON: JSONParseOptions{MaxDepth: 1025}},
		{JSON: JSONParseOptions{MaxNodes: -1}}, {JSON: JSONParseOptions{MaxNodes: 1000001}},
		{BodyFormat: "raw", JSON: JSONParseOptions{MaxDepth: 128}},
		{BodyFormat: "xml", JSON: JSONParseOptions{MaxNodes: 100}},
		{BodyFormat: "multipart", JSON: JSONParseOptions{MaxDepth: 128}},
	} {
		if err := validateEnvelope(opts); err == nil {
			t.Fatalf("invalid options accepted: %+v", opts)
		}
	}
	if err := validateEnvelope(EnvelopeOptions{JSON: JSONParseOptions{MaxDepth: 1024, MaxNodes: 1000000}}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvelopeJSONOptionsAuthoringContract(t *testing.T) {
	registry, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := registry.Lookup("envelope")
	node, err := spec.New(nodeConfigFromYAML(t, "envelope", "json: {max_depth: 512, max_nodes: 10000}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := node.(envelopeNode).json; got.MaxDepth != 512 || got.MaxNodes != 10000 {
		t.Fatalf("JSON limits lost in option decoding: %+v", got)
	}
	for _, yaml := range []string{
		"json: {max_depth: 1025}\n", "json: {max_nodes: 1000001}\n",
		"json: {unknown: 1}\n", "body_format: raw\njson: {max_depth: 512}\n",
	} {
		if _, err := spec.New(nodeConfigFromYAML(t, "envelope", yaml)); err == nil {
			t.Fatalf("invalid authoring options accepted: %s", yaml)
		}
	}
}

func TestEnvelopeXMLResourceFailureStatus(t *testing.T) {
	for _, tc := range []struct {
		body   string
		limits XMLParseOptions
		status int
	}{
		{"<a>123</a>", XMLParseOptions{MaxBytes: 9}, 413},
		{"<a><b/></a>", XMLParseOptions{MaxDepth: 1}, 413},
		{"<a><b/></a>", XMLParseOptions{MaxNodes: 1}, 413},
		{strings.Repeat("<a>", 257) + strings.Repeat("</a>", 257), XMLParseOptions{}, 413},
		{`<!DOCTYPE a [<!ENTITY bomb "expanded">]><a>&bomb;</a>`, XMLParseOptions{}, 400},
		{"<a><b></a>", XMLParseOptions{}, 400},
	} {
		n := newEnvelopeNode("xml", EnvelopeOptions{BodyFormat: "xml", XML: tc.limits}, nil, nil)
		_, err := n.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: []byte(tc.body)}})
		var nodeErr graph.NodeError
		if !errors.As(err, &nodeErr) || nodeErr.Status != tc.status {
			t.Fatalf("XML resource/malformed boundary: %v, want%d", err, tc.status)
		}
	}
}

func TestMultipartAggregateBudgetAndBase64Expansion(t *testing.T) {
	ct, raw := buildMultipart(t, nil, []multipartFile{{field: "one", filename: "one.bin", content: []byte("abc")}, {field: "two", filename: "two.bin", content: []byte("def")}})
	for _, total := range []int64{5, 6} {
		out, status, err := parseMultipart(ct, raw, multipartLimits{MaxParts: 2, MaxPartBytes: 64 << 20, MaxTotalBytes: total, IncludeContent: true})
		if total == 5 {
			if err == nil || status != http.StatusRequestEntityTooLarge || !strings.Contains(err.Error(), "max_total_bytes") {
				t.Fatalf("aggregate cap: %d %v", status, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files := out["files"].(map[string]any)
		for _, name := range []string{"one", "two"} {
			file := files[name].(map[string]any)
			encoded := file["content"].(string)
			if file["size"] != int64(3) || len(encoded) != base64.StdEncoding.EncodedLen(3) {
				t.Fatal("decoded and encoded byte budgets conflated")
			}
		}
	}
}

func BenchmarkMultipartAggregateBudget(b *testing.B) {
	var raw bytes.Buffer
	// The wire body remains independently buffered by read_body. Here only a
	// tiny remaining aggregate budget may be allocated by the part parser.
	raw.WriteString("--budget\r\nContent-Disposition: form-data; name=\"file\"; filename=\"file.bin\"\r\n\r\n")
	raw.Write(bytes.Repeat([]byte{'x'}, 12<<20))
	raw.WriteString("\r\n--budget--\r\n")
	data := raw.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, status, err := parseMultipart("multipart/form-data; boundary=budget", data, multipartLimits{MaxParts: 1, MaxPartBytes: 12 << 20, MaxTotalBytes: 16})
		if err == nil || status != 413 {
			b.Fatal(status, err)
		}
	}
}
