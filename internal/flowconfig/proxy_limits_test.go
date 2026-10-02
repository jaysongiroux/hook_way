package flowconfig

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestProxyMultipartSeparatePartAndAggregateLimits(t *testing.T) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, name := range []string{"one", "two"} {
		p, err := w.CreateFormFile(name, name+".pdf")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.Write([]byte("1234"))
	}
	_ = w.Close()
	for _, tc := range []struct {
		part, total int64
		status      int
	}{{3, 8, 413}, {4, 7, 413}, {4, 8, 0}, {5, 9, 0}} {
		_, status, err := parseMultipart(w.FormDataContentType(), b.Bytes(), multipartLimits{MaxParts: 2, MaxPartBytes: tc.part, MaxTotalBytes: tc.total})
		if status != tc.status || (err != nil) != (tc.status != 0) {
			t.Fatalf("limits %+v: %d %v", tc, status, err)
		}
	}
}

func TestRawProxyEnvelopeDoesNotEncodeOrParseLargeBody(t *testing.T) {
	for _, body := range [][]byte{bytes.Repeat([]byte{0xff}, 12<<20), []byte(`{"content":"` + strings.Repeat("x", 12<<20) + `"}`)} {
		n := newEnvelopeNode("raw", EnvelopeOptions{BodyFormat: "raw"}, nil, nil)
		out, err := n.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{}, Body: body}})
		if err != nil {
			t.Fatal(err)
		}
		env := out.(graph.Envelope)
		event := env.Extra.(CapturedEvent).Event
		if env.Body != nil || event.Body != nil || event.RawBodyBase64 != nil || !bytes.Equal(event.RawBody, body) {
			t.Fatal("raw proxy made a parsed or encoded copy")
		}
		code, err := newCodeNode("metadata", CodeOptions{Script: `return {vars:{method:input.request.method || "POST"}}`})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := code.Run(context.Background(), env); err != nil {
			t.Fatalf("binary body entered VM budget: %v", err)
		}
	}
}

func TestProxyContractValidationRejectsAmbiguousOrUnsafeOptions(t *testing.T) {
	for _, fields := range []string{
		"upstream: {profile: sample, environment_variable: route}",
		"method: POST\nforward_method: true", "method: CONNECT", "body_methods: [get]", "query_mode: bad", "query_mode: last", "remove_query: [route]", "header_mode: bad", "remove_headers: ['bad header']", "remove_body_fields: [route]", "body: transformed\nremove_body_fields: ['a..b']", "max_response_bytes: 67108865", "redirects: {max_hops: 0}", "retry: {max_attempts: 2, on: all}", "retry: {max_attempts: 2, methods: [TRACE]}", "retry: {max_attempts: 2, total_timeout: nope}", "response_bindings: {fields: {id: access_token}}", "response_bindings: {fields: {api_key: data.id}}", "response_bindings: {status: status, fields: {status: data.id}}", "response_bindings: {fields: {id: data.id}, max_json_bytes: 1048577}",
	} {
		t.Run(fields, func(t *testing.T) {
			_, err := decodeOptions[HTTPRequestOptions](nodeConfigFromYAML(t, "http_request", "url: https://api.example.org/\n"+fields+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			reg, _ := NewRegistryWithOptions(RegistryOptions{})
			spec, _ := reg.Lookup("http_request")
			if _, err := spec.New(nodeConfigFromYAML(t, "http_request", "url: https://api.example.org/\n"+fields+"\n")); err == nil {
				t.Fatal("invalid proxy options accepted")
			}
		})
	}
	for _, options := range []string{"upstream: {profile: sample, environment_variable: route, path: //other}", "upstream: {profile: sample, environment_variable: route, scheme: file}", "upstream: {profile: sample, environment_variable: route, port: 65536}"} {
		o, err := decodeOptions[HTTPRequestOptions](nodeConfigFromYAML(t, "http_request", options))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateHTTPRequest(o); err == nil {
			t.Fatalf("invalid upstream accepted: %s", options)
		}
	}
}

func TestEnvelopeXMLLimitsAreExplicit(t *testing.T) {
	for _, tc := range []struct {
		body   string
		limit  XMLParseOptions
		status int
	}{
		{"<a>123</a>", XMLParseOptions{MaxBytes: 9}, 413},
		{"<a>123</a>", XMLParseOptions{MaxBytes: 10}, 0},
		{"<a><b/></a>", XMLParseOptions{MaxDepth: 1}, 413},
		{"<a><b/></a>", XMLParseOptions{MaxDepth: 2, MaxNodes: 2}, 0},
		{"<a><b/></a>", XMLParseOptions{MaxNodes: 1}, 413},
	} {
		n := newEnvelopeNode("xml", EnvelopeOptions{BodyFormat: "xml", XML: tc.limit}, nil, nil)
		_, err := n.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: []byte(tc.body)}})
		if tc.status == 0 {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(fmt.Sprint(err), "limit") {
			t.Fatalf("XML budget: %v", err)
		}
	}
}

func TestProxyParsedFileBudgetCanBeRaisedToTwelveMiB(t *testing.T) {
	const limit = 12 << 20
	xml := []byte("<sample>" + strings.Repeat("x", limit-17) + "</sample>")
	n := newEnvelopeNode("xml", EnvelopeOptions{BodyFormat: "xml", XML: XMLParseOptions{MaxBytes: limit}}, nil, nil)
	if _, err := n.Run(context.Background(), graph.Verified{Raw: graph.RawRequest{Body: xml}}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	p, err := w.CreateFormFile("document", "sample.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write(bytes.Repeat([]byte{'x'}, limit))
	_ = w.Close()
	parsed, status, err := parseMultipart(w.FormDataContentType(), b.Bytes(), multipartLimits{MaxParts: 1, MaxPartBytes: limit, MaxTotalBytes: limit})
	if err != nil || status != 0 {
		t.Fatal(status, err)
	}
	file := parsed["files"].(map[string]any)["document"].(map[string]any)
	if file["size"] != int64(limit) || file["content_omitted"] != true {
		t.Fatal("parsed file size changed")
	}
}
