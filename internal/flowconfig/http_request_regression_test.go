package flowconfig

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestHTTPRequestRejectsAuthorityEscapes(t *testing.T) {
	for _, allowPrivate := range []bool{false, true} {
		for _, value := range []string{
			"attacker.example/", "attacker.example?", "attacker.example#", "attacker.example@",
			"attacker.example:8443/", "attacker%2f", "a.b", "a\\b", "a\n", "-tenant", "tenant-", strings.Repeat("a", 64),
		} {
			t.Run(value+"/private="+map[bool]string{false: "false", true: "true"}[allowPrivate], func(t *testing.T) {
				dialer := useRecordingDialer(t)
				node, err := newHTTPRequestNode("up", HTTPRequestOptions{
					URL: "https://{tenant}.trusted.example/resource", AllowPrivateTargets: allowPrivate,
					Headers: map[string]string{"Authorization": "Bearer configured-secret"},
				}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				env := retryEnvelope("sensitive-body")
				env.Vars["tenant"] = value
				_, err = node.Run(context.Background(), env)
				var nodeErr graph.NodeError
				if !errors.As(err, &nodeErr) || nodeErr.Status != http.StatusBadRequest {
					t.Fatalf("Run() = %v, want 400", err)
				}
				if got := dialer.dialed(); len(got) != 0 {
					t.Fatalf("escaped authority reached transport: %v", got)
				}
			})
		}
	}
}

func TestHTTPRequestAcceptsHostLabels(t *testing.T) {
	for _, value := range []string{"tenant", "Tenant-1", strings.Repeat("a", 63)} {
		t.Run(value, func(t *testing.T) {
			dialer := useRecordingDialer(t)
			node := mustHTTPRequestNode(t, "url: https://{tenant}-api.trusted.example/{rest}\n", nil, nil)
			env := retryEnvelope("{}")
			env.Vars = map[string]string{"tenant": value, "rest": "nested/path"}
			_, _ = node.Run(context.Background(), env)
			if len(dialer.dialed()) != 1 {
				t.Fatal("valid label did not reach transport")
			}
		})
	}
}

func TestHTTPRequestPreservesQueryURLs(t *testing.T) {
	query := "callback=https://callback.example/x//y&sig=a%2Fb%2F%2F&empty="
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/next/" || r.URL.RawQuery != query {
			t.Errorf("request URL = %s, want normalized path and exact query %s", r.URL, query)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: '"+upstream.URL+"//events/./next//?"+query+"'\n", nil, nil)
	if _, err := node.Run(context.Background(), retryEnvelope("{}")); err != nil {
		t.Fatal(err)
	}
}

func TestCleanOutboundPathPreservesOtherComponents(t *testing.T) {
	for _, raw := range []string{
		"https://up.example//events//?callback=https://callback.example/x#https://fragment.example/x",
		"https://up.example?callback=https://callback.example/x//y",
		"https://up.example//%2f//%2e%2e/?sig=%2F%2F&callback=https://callback.example",
	} {
		before, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		after, err := url.Parse(cleanOutboundPath(raw))
		if err != nil {
			t.Fatal(err)
		}
		if before.RawQuery != after.RawQuery || before.Fragment != after.Fragment || before.Host != after.Host {
			t.Fatalf("URL components changed: %s -> %s", before, after)
		}
	}
}

func TestURLAuthorityExcludesQueryAndFragmentVariables(t *testing.T) {
	for _, raw := range []string{"https://trusted.example?callback={callback}", "https://trusted.example#part={part}"} {
		if urlAuthorityIsTemplated(raw) {
			t.Fatalf("non-authority variable classified as a host variable: %s", raw)
		}
	}
}

func TestHTTPRequestIsolatesEgressConnectionPools(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	unguarded, guarded := newProxyHTTPClient(false), newProxyHTTPClient(true)
	unguardedTransport := unguarded.Transport.(*http.Transport)
	guardedTransport := guarded.Transport.(*http.Transport)
	// Map the public hostname to our private test server. The guarded dial
	// sees that same resolved address and must reject it even after warm-up.
	var unguardedDials, guardedDials atomic.Int32
	unguardedTransport.Proxy = nil
	guardedTransport.Proxy = nil
	unguardedTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		unguardedDials.Add(1)
		return egressDialContext(withEgressGuard(ctx, false), network, upstream.Listener.Addr().String())
	}
	guardedTransport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		guardedDials.Add(1)
		return egressDialContext(withEgressGuard(ctx, true), network, upstream.Listener.Addr().String())
	}
	saved, savedGuarded := proxyHTTPClient, guardedProxyHTTPClient
	proxyHTTPClient, guardedProxyHTTPClient = unguarded, guarded
	t.Cleanup(func() {
		proxyHTTPClient, guardedProxyHTTPClient = saved, savedGuarded
		unguardedTransport.CloseIdleConnections()
		guardedTransport.CloseIdleConnections()
	})

	literal := mustHTTPRequestNode(t, "url: http://tenant.trusted.example/health\n", nil, nil)
	for range 2 {
		if _, err := literal.Run(context.Background(), retryEnvelope("{}")); err != nil {
			t.Fatal(err)
		}
	}
	if unguardedDials.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("warm-up did not reuse a connection: dials=%d calls=%d", unguardedDials.Load(), calls.Load())
	}
	templated := mustHTTPRequestNode(t, "url: http://{tenant}.trusted.example/private\n", nil, nil)
	env := retryEnvelope("{}")
	env.Vars["tenant"] = "tenant"
	_, err := templated.Run(context.Background(), env)
	var transportErr *url.Error
	if !errors.As(err, &transportErr) || !strings.Contains(transportErr.Err.Error(), "not an allowed egress target") {
		t.Fatalf("guarded Run() = %v, want private-address rejection", err)
	}
	if guardedDials.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("guarded request reused private connection: guarded dials=%d calls=%d", guardedDials.Load(), calls.Load())
	}
}
