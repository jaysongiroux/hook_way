package flowconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
)

const malformedPlaceholder = "{bad-name}"

func TestMalformedTemplatesRejectedAtValidateAndCompile(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		typ  string
		body string
	}{
		{"http_request", "url: '" + malformedPlaceholder + "'\n"},
		{"publish", "destination: primary\ntarget: '" + malformedPlaceholder + "'\n"},
		{"respond", "status: 200\nbody: '" + malformedPlaceholder + "'\n"},
		{"lookup", "provider: dynamodb\nkey: '" + malformedPlaceholder + "'\nname: route\n"},
		{"replay_guard", "ttl: 1m\nkey: '" + malformedPlaceholder + "'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			spec, ok := reg.Lookup(tc.typ)
			if !ok {
				t.Fatalf("missing type %s", tc.typ)
			}
			_, err := spec.New(nodeConfigFromYAML(t, tc.typ, tc.body))
			if err == nil {
				t.Fatal("constructor accepted malformed template")
			}
			src := strings.ReplaceAll(cleanLintYAML,
				"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
				nodeYAMLForType(tc.typ, tc.body))
			_, issues := LoadBytes([]byte(src), "hardening", false)
			if !issuesContain(issues, "invalid placeholder") && !issuesContain(issues, "bad-name") && !issuesContain(issues, "placeholder") {
				t.Fatalf("LoadBytes issues = %q, want a template parse error so hookway validate fails", issuesText(issues))
			}
		})
	}
}

func nodeYAMLForType(typ, body string) string {
	indented := "      - id: bad\n        type: " + typ + "\n"
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		indented += "        " + line + "\n"
	}
	switch typ {
	case "http_request", "lookup":
		indented += "      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>bad, bad>out]\n"
	case "replay_guard":
		indented += "      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>bad, bad>env, env>out]\n"
	default:
		indented += "    edges: [in>rl, rl>body, body>auth, auth>env, env>bad]\n"
	}
	return indented
}

func issuesContain(issues []config.ValidationIssue, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Message, want) || strings.Contains(issue.Field, want) {
			return true
		}
	}
	return false
}

func TestReplayGuardOmittedKeyHashesBody(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1m\n")
	body := []byte(`{"id":1}`)
	v := graph.Verified{Raw: graph.RawRequest{Body: body}}
	got, err := n.reservationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("omitted key = %q, want sha256(body) %q", got, want)
	}
}

func TestReplayGuardConfiguredUnboundKeyIs400(t *testing.T) {
	n := mustReplayGuard(t, "ttl: 1m\nkey: '{id}'\n")
	v := graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"id":1}`), Head: graph.RequestHead{}}}
	_, err := n.Run(context.Background(), v)
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusBadRequest {
		t.Fatalf("unbound configured key = %v, want NodeError 400 (no body-hash fallback)", err)
	}
	if _, hashErr := n.reservationKey(v); hashErr == nil {
		t.Fatal("unbound configured key must not fall back to a hash")
	}
}

func TestRespondStatusValidation(t *testing.T) {
	cases := []struct {
		name    string
		opts    RespondOptions
		wantErr string
	}{
		{name: "1xx", opts: RespondOptions{Status: 100}, wantErr: "200 and 599"},
		{name: "below 200", opts: RespondOptions{Status: 199}, wantErr: "200 and 599"},
		{name: "above 599", opts: RespondOptions{Status: 600}, wantErr: "200 and 599"},
		{name: "204 with body", opts: RespondOptions{Status: 204, Body: "no"}, wantErr: "must not include a body"},
		{name: "304 with body", opts: RespondOptions{Status: 304, Body: "no"}, wantErr: "must not include a body"},
		{name: "204 without body", opts: RespondOptions{Status: 204}},
		{name: "200 with body", opts: RespondOptions{Status: 200, Body: "ok"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRespond(tc.opts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRespond() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateRespond() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestRespondRuntimeOmitsBodyFor204And304(t *testing.T) {
	for _, status := range []int{204, 304} {
		node, err := newRespondNode("resp", RespondOptions{Status: status})
		if err != nil {
			t.Fatal(err)
		}
		out, err := node.Run(context.Background(), graph.Envelope{})
		if err != nil {
			t.Fatal(err)
		}
		term, ok := out.(graph.Terminal)
		if !ok || term.Response == nil {
			t.Fatalf("out = %#v", out)
		}
		if term.Response.Status != status || len(term.Response.Body) != 0 {
			t.Fatalf("status %d body = %q, want empty", status, term.Response.Body)
		}
	}
}

func TestHTTPRequestRenderedURLMustBeHTTPWithHost(t *testing.T) {
	node, err := newHTTPRequestNode("up", HTTPRequestOptions{URL: "{target}"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"ftp://example.test/x", "https://", "not-a-url"} {
		_, runErr := node.Run(context.Background(), graph.Envelope{Vars: map[string]string{"target": target}})
		var ne graph.NodeError
		if runErr == nil || !errors.As(runErr, &ne) || ne.Status != http.StatusBadRequest {
			t.Fatalf("target %q = %v, want NodeError 400", target, runErr)
		}
	}
}

func TestTraceIDFromRejectsInvalidAndAllZero(t *testing.T) {
	req := httptestRequestWithTrace("", strings.Repeat("0", 32))
	got := traceIDFrom(req)
	if got == strings.Repeat("0", 32) || len(got) != 32 {
		t.Fatalf("all-zero X-Trace-ID adopted: %q", got)
	}
	req = httptestRequestWithTrace("", "short")
	got = traceIDFrom(req)
	if got == "short" || len(got) != 32 {
		t.Fatalf("invalid X-Trace-ID adopted: %q", got)
	}
	valid := "4bf92f3577b34da6a3ce929d0e0e4736"
	req = httptestRequestWithTrace("", valid)
	if got := traceIDFrom(req); got != valid {
		t.Fatalf("valid X-Trace-ID = %q, want %q", got, valid)
	}
}

func httptestRequestWithTrace(traceparent, xTraceID string) *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.test/w", nil)
	if traceparent != "" {
		req.Header.Set("Traceparent", traceparent)
	}
	if xTraceID != "" {
		req.Header.Set("X-Trace-ID", xTraceID)
	}
	return req
}
