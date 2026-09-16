package flowconfig

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/transform"
)

func TestCodeNodeTransformsBodyAndSetsVar(t *testing.T) {
	node, err := newCodeNode("code", CodeOptions{
		Script:  `return { body: { id: String(input.body.id).toUpperCase() }, vars: { computed: String(input.body.id).toUpperCase() } }`,
		Timeout: "1s",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{
		Body: map[string]any{"id": "abc"},
		Vars: map[string]string{"keep": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	body, _ := env.Body.(map[string]any)
	if body["id"] != "ABC" {
		t.Fatalf("body = %#v", env.Body)
	}
	if env.Vars["computed"] != "ABC" {
		t.Fatalf("vars = %#v, want computed for a downstream publish template", env.Vars)
	}
}

func TestCodeNodeNonObjectReturnIsError(t *testing.T) {
	node, err := newCodeNode("code", CodeOptions{Script: `return 1`, Timeout: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.Envelope{Body: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("err = %v, want non-object error", err)
	}
}

func TestCodeNodeSyntaxErrorFailsAtLoad(t *testing.T) {
	_, err := newCodeNode("code", CodeOptions{Script: "function (", Timeout: "1s"})
	if err == nil {
		t.Fatal("syntax error accepted at node construction")
	}
	src := `
schema_version: 2
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env, type: envelope, body_format: json}
      - {id: code, type: code, script: "function ("}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>auth, auth>env, env>code, code>out]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if !containsMessage(issues, "compile") && !containsMessage(issues, "SyntaxError") && !containsMessage(issues, "Unexpected") {
		t.Fatalf("validate issues = %v, want a compile/syntax error at load", issues)
	}
}

func TestCodeNodeCannotSeeIdentityOrSecret(t *testing.T) {
	node, err := newCodeNode("code", CodeOptions{
		Script:  `return { body: { dump: JSON.stringify(input) } }`,
		Timeout: "1s",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{
		V: graph.Verified{
			Identity: "hmac-passed",
			Raw: graph.RawRequest{
				Head: graph.RequestHead{
					Header: http.Header{
						"Authorization": []string{"super-secret-token"},
						"X-Request-Id":  []string{"req-1"},
					},
				},
				Body: []byte(`{"id":"x"}`),
			},
		},
		Body: map[string]any{"id": "x"},
		Vars: map[string]string{"keep": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dump, _ := out.(graph.Envelope).Body.(map[string]any)["dump"].(string)
	if dump == "" {
		t.Fatalf("dump missing: %#v", out.(graph.Envelope).Body)
	}
	for _, leaked := range []string{"super-secret-token", "hmac-passed", "Verified", "Identity"} {
		if strings.Contains(dump, leaked) {
			t.Errorf("script saw %q in %s", leaked, dump)
		}
	}
	if !strings.Contains(dump, "req-1") {
		t.Errorf("benign header missing from %s", dump)
	}
}

func TestCodeNodeRejectsXMLBody(t *testing.T) {
	doc, err := transform.ParseXML([]byte(`<ROOT/>`))
	if err != nil {
		t.Fatal(err)
	}
	node, err := newCodeNode("code", CodeOptions{Script: `return {body: input.body}`, Timeout: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.Envelope{Body: doc})
	if err == nil || !strings.Contains(err.Error(), "XML") {
		t.Fatalf("err = %v, want XML rejection", err)
	}
}

func TestCodeNodeDoesNotMutateOriginalBody(t *testing.T) {
	node, err := newCodeNode("code", CodeOptions{
		Script:  `input.body.id = "mutated"; return { body: input.body }`,
		Timeout: "1s",
	})
	if err != nil {
		t.Fatal(err)
	}
	orig := map[string]any{"id": "abc"}
	_, err = node.Run(context.Background(), graph.Envelope{Body: orig})
	if err != nil {
		t.Fatal(err)
	}
	if orig["id"] != "abc" {
		t.Fatalf("script mutated the caller's body: %#v", orig)
	}
}

func TestReviewCodeHonorsConfiguredHeaderPolicy(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{HeaderDenylist: []string{"X-Partner"}, HeaderAllowlist: []string{"X-Partner", "X-Request-Id"}})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("code")
	node, err := spec.New(nodeConfigFromYAML(t, "code", `script: 'return {body: input.headers}'`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"X-Partner": {"credential"}, "X-Hidden": {"private"}, "X-Request-Id": {"visible"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	body := out.(graph.Envelope).Body.(map[string]string)
	if body["x-partner"] != "redacted" || body["x-hidden"] != "" || body["x-request-id"] != "visible" {
		t.Fatalf("header policy bypass: %#v", body)
	}
}
