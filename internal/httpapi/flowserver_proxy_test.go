package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyFlowReturnsUpstreamResponse(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotAuth, gotHops string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotHops = r.Header.Get("X-Hookway-Hops")
		w.Header().Set("X-Upstream", "yes")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "proxy-upstream-body")
	}))
	t.Cleanup(upstream.Close)

	src := `
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {}
flows:
  salesforce_proxy:
    nodes:
      - {id: in, type: http_trigger, path: "/salesforce/proxy/{rest...}", methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: exact, secret: testtoken, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: raw}
      - {id: up, type: http_request, url: "` + upstream.URL + `/{rest}", forward_method: true, forward_query: true}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
`
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := httptest.NewRequest(http.MethodPut, "/salesforce/proxy/api/v1/thing?a=1", strings.NewReader(`{"k":"v"}`))
	req.Header.Set("X-Auth", "testtoken")
	req.Header.Set("Authorization", "Bearer leaked")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d body = %s, want upstream 418", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "proxy-upstream-body" {
		t.Fatalf("body = %q, want the upstream body not a Hookway envelope", rec.Body.String())
	}
	if rec.Header().Get("X-Upstream") != "yes" {
		t.Fatalf("missing upstream header: %v", rec.Header())
	}
	if rec.Header().Get("Connection") != "" {
		t.Fatalf("hop-by-hop Connection leaked: %v", rec.Header())
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/thing" || gotQuery != "a=1" {
		t.Fatalf("upstream saw method=%s path=%s query=%s", gotMethod, gotPath, gotQuery)
	}
	if gotAuth != "" {
		t.Fatal("inbound Authorization was forwarded upstream")
	}
	if gotHops != "1" {
		t.Fatalf("outbound hops = %q, want 1", gotHops)
	}
}

func TestProxyFlowWrongTokenIs401(t *testing.T) {
	src := `
server: {addr: ":8080", max_body_bytes: 1024}
destinations: {}
flows:
  salesforce_proxy:
    nodes:
      - {id: in, type: http_trigger, path: "/salesforce/proxy/{rest...}", methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: exact, secret: testtoken, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: raw}
      - {id: up, type: http_request, url: "http://127.0.0.1:1/{rest}", forward_method: true}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
`
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := httptest.NewRequest(http.MethodPut, "/salesforce/proxy/x", strings.NewReader(`{}`))
	req.Header.Set("X-Auth", "nope")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestXMLProxyFlowSendsPrunedBody(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "<ok/>")
	}))
	t.Cleanup(upstream.Close)

	src := `
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {}
flows:
  docmagic_xml_proxy:
    nodes:
      - {id: in, type: http_trigger, path: /digifi/docmagic, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: exact, secret: testtoken, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: xml}
      - {id: prune, type: transform, kind: xml_prune}
      - {id: up, type: http_request, url: "` + upstream.URL + `", body: transformed, headers: {Content-Type: application/xml}}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>prune, prune>up, up>resp]
`
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	inbound := `<ROOT><EMPTY/><KEEP>ok</KEEP></ROOT>`
	req := httptest.NewRequest(http.MethodPost, "/digifi/docmagic", strings.NewReader(inbound))
	req.Header.Set("X-Auth", "testtoken")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || rec.Body.String() != "<ok/>" {
		t.Fatalf("status=%d body=%q, want upstream 202/<ok/>", rec.Code, rec.Body.String())
	}
	if strings.Contains(string(gotBody), "EMPTY") || !strings.Contains(string(gotBody), "KEEP") {
		t.Fatalf("upstream body = %q, want pruned XML", gotBody)
	}
}
