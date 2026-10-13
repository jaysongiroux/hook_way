package flowconfig

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
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

func TestCodeNodeConfigurableInputLimit(t *testing.T) {
	largeBody := map[string]any{"content": strings.Repeat("x", defaultCodeInputSize)}
	defaultNode, err := newCodeNode("code", CodeOptions{
		Script: `return {body: {ok: true}}`, Timeout: "1s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultNode.Run(context.Background(), graph.Envelope{Body: largeBody}); err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("default input limit error = %v", err)
	}

	expandedNode, err := newCodeNode("code", CodeOptions{
		Script: `return {body: {ok: true}}`, Timeout: "1s", MaxInputBytes: 2 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expandedNode.Run(context.Background(), graph.Envelope{Body: largeBody}); err != nil {
		t.Fatalf("expanded input limit rejected body: %v", err)
	}
}

func TestCodeNodeSyntaxErrorFailsAtLoad(t *testing.T) {
	_, err := newCodeNode("code", CodeOptions{Script: "function (", Timeout: "1s"})
	if err == nil {
		t.Fatal("syntax error accepted at node construction")
	}
	src := `
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

func TestCodeNodeReadsXMLBody(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<MESSAGE><ORDER id="O1"><Amount>500</Amount></ORDER></MESSAGE>`), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	node, err := newCodeNode("code", CodeOptions{Timeout: "1s", Script: `
		const order = input.body.root.children[0];
		return { body: input.body, vars: { amount: order.children[0].text, id: order.attr.id } };
	`})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Body: doc, Vars: map[string]string{}})
	if err != nil {
		t.Fatalf("Run() = %v, want the script to read the XML body", err)
	}
	env := out.(graph.Envelope)
	if env.Vars["amount"] != "500" {
		t.Fatalf("vars.amount = %q, want \"500\"", env.Vars["amount"])
	}
	if env.Vars["id"] != "O1" {
		t.Fatalf("vars.id = %q, want \"O1\"", env.Vars["id"])
	}
}

func TestCodeNodeRewritesXMLBodyAndKeepsItXML(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<MESSAGE><ORDER><Amount>500</Amount></ORDER></MESSAGE>`), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	node, err := newCodeNode("code", CodeOptions{Timeout: "1s", Script: `
		const body = input.body;
		body.root.children[0].children[0].text = '999';
		return { body: body };
	`})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Body: doc, Vars: map[string]string{}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	// The body must come back as a Document, not as the script's map: a
	// downstream xml_prune or an http_request with body: transformed both
	// type-switch on transform.Document.
	env := out.(graph.Envelope)
	got, ok := env.Body.(transform.Document)
	if !ok {
		t.Fatalf("body = %T, want transform.Document", env.Body)
	}
	raw, err := transform.SerializeXML(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "<Amount>999</Amount>") {
		t.Fatalf("serialized = %s, want the rewritten amount", raw)
	}
}

func TestCodeNodeRejectsMalformedXMLReturn(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<MESSAGE><ORDER/></MESSAGE>`), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	node, err := newCodeNode("code", CodeOptions{Script: `return { body: { nope: true } };`, Timeout: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.Envelope{Body: doc, Vars: map[string]string{}})
	if err == nil {
		t.Fatal("Run() = nil, want a rejection naming the expected xml shape")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Fatalf("error = %v, want it to name the missing \"root\"", err)
	}
}

// A JSON flow must be untouched by the XML path.
func TestCodeNodeJSONBodyUnaffected(t *testing.T) {
	node, err := newCodeNode("code", CodeOptions{Script: `return { body: { n: input.body.n + 1 } };`, Timeout: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"n": float64(1)}, Vars: map[string]string{}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	body := out.(graph.Envelope).Body.(map[string]any)
	if got := body["n"]; got != int64(2) {
		// goja exports integral arithmetic as int64, not float64, even when the
		// operand came in as a float64 -- pin the type so a change there is
		// visible rather than silently reshaping a JSON body.
		t.Fatalf("body.n = %v (%T), want int64(2)", got, got)
	}
}

// An XML script must not be able to reach back into the caller's Document, the
// way cloneJSONValue stops a JSON script from mutating its input.
func TestCodeNodeDoesNotMutateOriginalXMLBody(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte(`<MESSAGE><ORDER><Amount>500</Amount></ORDER></MESSAGE>`), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	node, err := newCodeNode("code", CodeOptions{Timeout: "1s", Script: `
		input.body.root.children[0].children[0].text = 'mutated';
		return { body: input.body };
	`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = node.Run(context.Background(), graph.Envelope{Body: doc, Vars: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := doc.Root.Children[0].Children[0].Text; got != "500" {
		t.Fatalf("script mutated the caller's Document: Amount = %q, want \"500\"", got)
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

// TestCodeNodeCannotReadCredentialsEvenWithPermissiveAllowlist is a security
// regression test. scriptHeaders passes the flow's configured header
// allowlist/denylist through to FilterHeaders, so an operator could plausibly
// widen it far enough to expose credentials to user-authored JavaScript.
// logging.SensitiveHeader redaction is unconditional -- it applies even when a
// header is explicitly allowlisted -- and this test pins that.
func TestCodeNodeCannotReadCredentialsEvenWithPermissiveAllowlist(t *testing.T) {
	sensitive := map[string]string{
		"Authorization":       "Bearer super-secret",
		"Cookie":              "session=secret",
		"X-Hub-Signature-256": "sha256=deadbeef",
		"X-Api-Key":           "api-key-value",
		"X-Webhook-Secret":    "shhh",
	}
	hdr := http.Header{}
	for k, v := range sensitive {
		hdr.Set(k, v)
	}
	hdr.Set("Content-Type", "application/json")

	// Deliberately permissive: every sensitive header is explicitly allowed.
	allowlist := []string{"Authorization", "Cookie", "X-Hub-Signature-256", "X-Api-Key", "X-Webhook-Secret", "Content-Type"}

	got := scriptHeaders(hdr, allowlist, nil)

	for name, secret := range sensitive {
		value, present := got[strings.ToLower(name)]
		if present && value == secret {
			t.Errorf("script can read %s = %q; credentials must be redacted regardless of allowlist", name, value)
		}
		if present && value != "redacted" {
			t.Errorf("%s = %q, want \"redacted\"", name, value)
		}
	}
	// A non-sensitive header must still be readable, or the node is useless.
	if got["content-type"] != "application/json" {
		t.Errorf("content-type = %q, want it readable", got["content-type"])
	}
}
