package flowconfig

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

func TestProxyFailureDoesNotExposeRequestURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://other.example.org/private-resource?token=private-token")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	n := mustHTTPRequestNode(t, "url: "+srv.URL+"/private-resource?token=private-token\nredirects: {max_hops: 1}\n", nil, nil)
	_, err := n.Run(context.Background(), graph.Envelope{})
	if err == nil || !errors.Is(err, errProxyRedirect) {
		t.Fatalf("missing redirect failure: %v", err)
	}
	for _, sensitive := range []string{"private-resource", "private-token", srv.URL, "other.example.org"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("diagnostic exposed %q: %v", sensitive, err)
		}
	}
}

func TestProxyInvalidURLDoesNotExposeQueryCredentials(t *testing.T) {
	n := mustHTTPRequestNode(t, "url: https://api.example.org/{path}?token=private-token\n", nil, nil)
	_, err := n.Run(t.Context(), graph.Envelope{Vars: map[string]string{"path": "%invalid"}})
	if err == nil {
		t.Fatal("malformed rendered URL accepted")
	}
	var cause *url.Error
	if !errors.As(err, &cause) || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "api.example.org") {
		t.Fatalf("malformed URL diagnostics = %v", err)
	}
}

func TestProxyProjectionPreservesIngress(t *testing.T) {
	original := graph.Envelope{Body: map[string]any{"record_a": "sample", "nested": map[string]any{"route": "preview", "keep": true}}, Vars: map[string]string{"route": "preview"}, V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: "PATCH", Query: url.Values{"record_a": {"sample"}, "many": {"", "a+b", "a/b"}}, Header: http.Header{"X-Token": {"private"}, "X-Keep": {"yes"}, "Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Digest": {"stale"}}}, Body: []byte(`{"original":true}`)}}}
	for _, mode := range []string{"all", "first", "last"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"nested":{"keep":true}}` || r.ContentLength != int64(len(body)) {
					t.Errorf("body/framing = %s/%d", body, r.ContentLength)
				}
				if r.URL.Query().Has("record_a") || r.Header.Get("X-Token") != "" || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Digest") != "" || r.Header.Get("X-Keep") != "yes" {
					t.Errorf("projection leaked: %s %v", r.URL.RawQuery, r.Header)
				}
				want := projectedQuery(original.V.Raw.Head.Query, mode, []string{"record_a"})
				if !reflect.DeepEqual(r.URL.Query(), want) {
					t.Errorf("query %v != %v", r.URL.Query(), want)
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			n := mustHTTPRequestNode(t, fmt.Sprintf("url: %s\nforward_method: true\nforward_query: true\nquery_mode: %s\nremove_query: [record_a]\nremove_headers: [X-Token]\nbody: transformed\nremove_body_fields: [record_a, nested.route]\n", server.URL, mode), nil, nil)
			if _, err := n.Run(context.Background(), original); err != nil {
				t.Fatal(err)
			}
			if original.Body.(map[string]any)["record_a"] != "sample" || !original.V.Raw.Head.Query.Has("record_a") || original.V.Raw.Head.Header.Get("X-Token") != "private" {
				t.Fatal("ingress mutated")
			}
		})
	}
}

func TestProxyJSONRemovalRejectsNonObjectBeforeDelivery(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer srv.Close()
	n := mustHTTPRequestNode(t, "url: "+srv.URL+"\nbody: transformed\nremove_body_fields: [route]\n", nil, nil)
	for _, body := range []any{nil, "text", []any{"one"}, true} {
		_, err := n.Run(context.Background(), graph.Envelope{Body: body})
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 400 {
			t.Fatalf("projection error = %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid JSON projection reached upstream")
	}
}

func TestProxyBodyMethodsAndHeaderReplacement(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				want := ""
				if method == "POST" || method == "PUT" || method == "PATCH" {
					want = "raw"
				}
				if r.Method != method || string(b) != want || r.Header.Get("X-Inbound") != "" || r.Header.Get("X-Call") != "configured" {
					t.Errorf("projected request: %s %q %v", r.Method, b, r.Header)
				}
				w.WriteHeader(204)
			}))
			defer srv.Close()
			n := mustHTTPRequestNode(t, "url: "+srv.URL+"\nforward_method: true\nbody_methods: [POST, PUT, PATCH]\nheader_mode: replace\nheaders: {X-Call: configured}\n", nil, nil)
			_, err := n.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: method, Header: http.Header{"X-Inbound": {"private"}}}, Body: []byte("raw")}}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProxyResponseSelectionPreservesSuccessfulReply(t *testing.T) {
	for _, tc := range []struct{ body, reason, id string }{
		{`{"data":{"id":9007199254740993},"token":"never-exposed"}`, "", "9007199254740993"},
		{`{"other":true}`, "missing_field", ""}, {`broken`, "invalid_json", ""}, {`{"data":{"id":{}}}`, "invalid_field", ""},
		{`{"data":{"id":"` + strings.Repeat("x", 200) + `"}}`, "response_json_too_large", ""},
	} {
		t.Run(tc.reason+tc.id, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201); _, _ = io.WriteString(w, tc.body) }))
			defer srv.Close()
			n := mustHTTPRequestNode(t, "url: "+srv.URL+"\nresponse_bindings: {status: upstream_status, fields: {created_id: data.id}, max_json_bytes: 100}\n", nil, nil)
			in := graph.Envelope{Body: map[string]any{"unchanged": true}, Vars: map[string]string{"created_id": "stale", "keep": "yes"}}
			out, err := n.Run(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			env := out.(graph.Envelope)
			if env.Upstream.Status != 201 || string(env.Upstream.Body) != tc.body || env.Upstream.SelectionError != tc.reason || env.Vars["created_id"] != tc.id || env.Vars["upstream_status"] != "201" {
				t.Fatalf("reply = %+v vars=%v", env.Upstream, env.Vars)
			}
			if in.Vars["created_id"] != "stale" || !reflect.DeepEqual(env.Body, in.Body) {
				t.Fatal("caller state mutated")
			}
			code, err := newCodeNode("inspect", CodeOptions{Script: `if (input.upstream.body || input.upstream.headers || (input.upstream.values && input.upstream.values.token)) throw new Error("private reply exposed"); return {vars:input.vars};`})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = code.Run(context.Background(), env); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProxyRegistryHostCannotBeOverriddenByVariables(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/item" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	r, err := routing.NewRegistry(routing.Config{Profiles: map[string]routing.Profile{"sample": {AccountID: "000000000000", CredentialMode: "sample"}}, Environments: map[string]routing.Environment{"preview": {Aliases: []string{"staging"}, Profiles: []string{"sample"}, Accounts: []string{"000000000000"}, DestinationPrefix: "preview", BackendHost: u.Hostname()}}})
	if err != nil {
		t.Fatal(err)
	}
	opts := HTTPRequestOptions{Upstream: &HTTPUpstreamOptions{Profile: "sample", EnvironmentVariable: "route", Scheme: "http", Port: port, Path: "/{rest}"}, AllowPrivateTargets: true}
	n, err := newHTTPRequestNodeWithRegistry("up", opts, nil, nil, r, false)
	if err != nil {
		t.Fatal(err)
	}
	env := graph.Envelope{Vars: map[string]string{"route": "staging", "route_backend_host": "evil.example.org", "rest": "api/item"}}
	if _, err := n.Run(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	env.Vars = maps.Clone(env.Vars)
	env.Vars["route"] = "evil.example.org"
	if _, err := n.Run(context.Background(), env); err == nil {
		t.Fatal("unadmitted host accepted")
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}
	opts.AllowPrivateTargets = false
	guarded, _ := newHTTPRequestNodeWithRegistry("up", opts, nil, nil, r, false)
	env.Vars["route"] = "preview"
	if _, err := guarded.Run(context.Background(), env); err == nil {
		t.Fatal("registry private target bypassed guard")
	}
}

func TestProxyResponseBoundariesAndGzip(t *testing.T) {
	for _, size := range []int{7, 8, 9} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", size)) }))
			defer srv.Close()
			n := mustHTTPRequestNode(t, "url: "+srv.URL+"\nmax_response_bytes: 8\n", nil, nil)
			_, err := n.Run(context.Background(), graph.Envelope{})
			if (err != nil) != (size > 8) {
				t.Fatalf("size %d err %v", size, err)
			}
		})
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write([]byte("representation"))
	_ = zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="sample.pdf"`)
		_, _ = w.Write(zipped.Bytes())
	}))
	defer srv.Close()
	n := mustHTTPRequestNode(t, "url: "+srv.URL+"\n", nil, nil)
	out, err := n.Run(context.Background(), graph.Envelope{})
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	if !bytes.Equal(env.Upstream.Body, zipped.Bytes()) || env.Upstream.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("gzip representation changed")
	}
}

func TestProxyRetryControlsAndCancellation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer srv.Close()
	n := mustHTTPRequestNode(t, "url: "+srv.URL+"\nretry: {max_attempts: 3, on: network, initial_backoff: 1ms, max_backoff: 1ms}\n", nil, nil)
	if _, err := n.Run(context.Background(), graph.Envelope{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("HTTP status retried in network-only mode")
	}
	n = mustHTTPRequestNode(t, "url: "+srv.URL+"\nretry: {max_attempts: 3, methods: [GET], initial_backoff: 1ms, max_backoff: 1ms}\n", nil, nil)
	if _, err := n.Run(context.Background(), graph.Envelope{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("POST retried outside method policy")
	}
	n = mustHTTPRequestNode(t, "url: "+srv.URL+"\nretry: {max_attempts: 3, total_timeout: 20ms, initial_backoff: 1s, max_backoff: 1s}\n", nil, nil)
	start := time.Now()
	_, err := n.Run(context.Background(), graph.Envelope{})
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 504 || time.Since(start) > time.Second {
		t.Fatalf("total deadline: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := calls.Load()
	_, _ = n.Run(ctx, graph.Envelope{})
	if calls.Load() != before {
		t.Fatal("canceled call reached upstream")
	}
}

func TestProxyRedirectPolicyNoCredentialLeak(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { leaked.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/done", http.StatusTemporaryRedirect)
		case "/cross":
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
		default:
			if r.Header.Get("Authorization") != "configured" {
				t.Error("same-origin credential missing")
			}
			_, _ = io.WriteString(w, "done")
		}
	}))
	defer srv.Close()
	for _, route := range []string{"same", "cross", "loop"} {
		n := mustHTTPRequestNode(t, "url: "+srv.URL+"/"+route+"\nheaders: {Authorization: configured}\nredirects: {max_hops: 2}\n", nil, nil)
		out, err := n.Run(context.Background(), graph.Envelope{})
		if route == "same" {
			if err != nil || string(out.(graph.Envelope).Upstream.Body) != "done" {
				t.Fatalf("same origin: %v", err)
			}
		} else if err == nil {
			t.Fatal("unsafe redirect accepted")
		}
	}
	if leaked.Load() != 0 {
		t.Fatal("cross-origin request leaked")
	}
	n := mustHTTPRequestNode(t, "url: "+srv.URL+"/same\n", nil, nil)
	out, err := n.Run(context.Background(), graph.Envelope{})
	if err != nil || out.(graph.Envelope).Upstream.Status != 307 {
		t.Fatalf("default redirect: %v", err)
	}
}

func TestProxyRelayBodylessAndCookiePolicy(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
	}{{"HEAD", 200}, {"GET", 204}, {"GET", 304}, {"GET", 418}} {
		env := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: tc.method}}}, Upstream: &graph.UpstreamResponse{Status: tc.status, Header: http.Header{"Content-Length": {"99"}, "Set-Cookie": {"one=1", "two=2"}, "Connection": {"X-Private"}, "X-Private": {"remove"}, "X-Keep": {"yes"}}, Body: []byte("bytes")}}
		for _, cookies := range []bool{false, true} {
			n, _ := newRespondNode("reply", RespondOptions{Passthrough: true, ForwardSetCookie: cookies})
			out, err := n.Run(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			reply := out.(graph.Terminal).Response
			if reply.Status != tc.status || reply.Header.Get("X-Private") != "" || (len(reply.Body) > 0) != (tc.method != "HEAD" && tc.status != 204 && tc.status != 304) || (len(reply.Header.Values("Set-Cookie")) == 2) != cookies {
				t.Fatalf("reply %+v", reply)
			}
			want := "99"
			if tc.status == 204 {
				want = ""
			} else if tc.method != "HEAD" && tc.status != 304 {
				want = "5"
			}
			if reply.Header.Get("Content-Length") != want {
				t.Fatalf("length %s != %s", reply.Header.Get("Content-Length"), want)
			}
		}
	}
}

func TestProxyRequestViewRedactionAndIsolation(t *testing.T) {
	q := url.Values{"q": {"one", "two"}, "access_token": {"secret"}, "custom": {"secret"}}
	view := scriptQuery(q, []string{"custom"})
	view["q"][0] = "changed"
	if q["q"][0] != "one" || view["access_token"][0] != "redacted" || view["custom"][0] != "redacted" {
		t.Fatal("query view is not isolated and redacted")
	}
}

func TestProxyConcurrentRequestsDoNotShareProjectionState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"keep":true}` || r.URL.Query().Has("route") {
			t.Error("invalid concurrent projection")
		}
		_, _ = io.WriteString(w, `{"id":"sample-created"}`)
	}))
	defer srv.Close()
	n := mustHTTPRequestNode(t, "url: "+srv.URL+"\nforward_query: true\nremove_query: [route]\nbody: transformed\nremove_body_fields: [route]\nresponse_bindings: {fields: {created_id: id}}\n", nil, nil)
	in := graph.Envelope{Body: map[string]any{"route": "preview", "keep": true}, Vars: map[string]string{"keep": "yes"}, V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Query: url.Values{"route": {"preview"}}}}}}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := n.Run(context.Background(), in)
			if err != nil {
				t.Error(err)
				return
			}
			env := out.(graph.Envelope)
			env.Vars["created_id"] = "changed"
			env.Upstream.Values["created_id"] = "changed"
		}()
	}
	wg.Wait()
	if in.Body.(map[string]any)["route"] != "preview" || in.Vars["created_id"] != "" || in.V.Raw.Head.Query.Get("route") != "preview" {
		t.Fatal("concurrent requests changed ingress")
	}
}
