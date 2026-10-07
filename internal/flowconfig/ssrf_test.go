package flowconfig

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"golang.org/x/net/dns/dnsmessage"
)

// recordingDialer stands in for the real network. Every dial is recorded and
// then refused, so a test can distinguish "the SSRF guard blocked this before
// any socket was opened" from "the guard let it through and the connection
// merely failed". A status- or error-only assertion cannot tell those apart:
// dialing 169.254.169.254 from a test machine usually fails anyway.
type recordingDialer struct {
	mu    sync.Mutex
	addrs []string
}

func (d *recordingDialer) dial(_ context.Context, _, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	d.mu.Unlock()
	return nil, errors.New("dial blocked by test")
}

func (d *recordingDialer) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addrs...)
}

// useRecordingDialer swaps the package's shared proxy client for one that can
// never reach the network, and restores it afterwards.
func useRecordingDialer(t *testing.T) *recordingDialer {
	t.Helper()
	d := &recordingDialer{}
	transport := &http.Transport{DialContext: d.dial}
	saved := proxyHTTPClient
	savedGuarded := guardedProxyHTTPClient
	proxyHTTPClient = &http.Client{
		Transport:     transport,
		CheckRedirect: saved.CheckRedirect,
	}
	guardedProxyHTTPClient = proxyHTTPClient
	t.Cleanup(func() {
		proxyHTTPClient = saved
		guardedProxyHTTPClient = savedGuarded
		transport.CloseIdleConnections()
	})
	return d
}

func ssrfEnvelope(target string) graph.Envelope {
	return graph.Envelope{
		V: graph.Verified{Raw: graph.RawRequest{
			Head: graph.RequestHead{Method: http.MethodGet, Header: http.Header{}, Query: url.Values{}},
			Body: []byte("{}"),
		}},
		Vars: map[string]string{"target": target},
	}
}

// TestHTTPRequestRejectsInternalTargets is a security regression test for
// SSRF. The url template's host is request-derived (env.Vars is seeded from
// the trigger's path variables and extended by lookup/transform nodes), and
// Template.Render substitutes raw text, so without an egress policy a caller
// picks the upstream host -- including cloud metadata services and anything
// else reachable from the gateway's network position.
func TestHTTPRequestRejectsInternalTargets(t *testing.T) {
	// The node is built directly rather than through the registry: the
	// config-time guard (see TestValidateHTTPRequestRequiresLiteralHostSuffix)
	// rejects this template outright, and this test exercises the independent
	// runtime layer.
	node, err := newHTTPRequestNode("up", HTTPRequestOptions{URL: "http://{target}/x"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	blocked := []string{
		"169.254.169.254",
		"169.254.170.2",
		"metadata.google.internal",
		"100.100.100.200",
		"127.0.0.1:9090",
		"localhost:9091",
		"[::1]:9091",
		"0.0.0.0",
		"10.0.0.5",
		"172.16.0.1",
		"192.168.1.1",
		"100.64.0.1",
		"2130706433",
		"0x7f000001",
		"127.1",
		"[::ffff:127.0.0.1]",
	}
	for _, target := range blocked {
		t.Run("blocked/"+target, func(t *testing.T) {
			if err := validateEgressURL("http://"+target+"/x", true); err == nil {
				t.Fatalf("private target %q passed the URL policy", target)
			}
			dialer := useRecordingDialer(t)
			_, err := node.Run(context.Background(), ssrfEnvelope(target))
			if err == nil {
				t.Fatalf("target %q was allowed; want a rejection", target)
			}
			var nerr graph.NodeError
			if !errors.As(err, &nerr) {
				t.Fatalf("error = %T (%v), want graph.NodeError", err, err)
			}
			if got := dialer.dialed(); len(got) != 0 {
				t.Fatalf("target %q was dialed at %v; the guard must reject before any connection attempt", target, got)
			}
		})
	}

	allowed := []string{
		"api",
		"tenant-1",
	}
	for _, target := range allowed {
		t.Run("allowed/"+target, func(t *testing.T) {
			dialer := useRecordingDialer(t)
			// The dial is refused by the fake transport, so an error is
			// expected; what matters is that the guard did not stop the
			// request before it reached the transport.
			_, _ = node.Run(context.Background(), ssrfEnvelope(target))
			if got := dialer.dialed(); len(got) == 0 {
				t.Fatalf("target %q was blocked; a public host must still be reachable", target)
			}
		})
	}
}

// TestHTTPRequestAllowsInternalTargetsWhenOptedIn keeps the guard operable:
// an operator who genuinely proxies to an internal service must have a way to
// say so, and it must be an explicit opt-in (default deny).
func TestHTTPRequestAllowsInternalTargetsWhenOptedIn(t *testing.T) {
	node, err := newHTTPRequestNode("up", HTTPRequestOptions{
		URL:                 "http://{target}.internal/x",
		AllowPrivateTargets: true,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	dialer := useRecordingDialer(t)
	_, _ = node.Run(context.Background(), ssrfEnvelope("api"))
	if got := dialer.dialed(); len(got) == 0 {
		t.Fatal("allow_private_targets: true did not permit an internal target")
	}
}

// TestValidateHTTPRequestRequiresLiteralHostSuffix is the config-time half of
// the SSRF fix, and a regression guard against losing what v1 enforced in
// internal/config: a {variable} in the url authority must be followed by a
// literal dotted domain suffix, so a request can pick at most one label under
// a fixed domain and never the whole host.
func TestValidateHTTPRequestRequiresLiteralHostSuffix(t *testing.T) {
	rejected := []string{
		"http://{target}/x",
		"https://{target}",
		"https://{target}:8443/x",
		"https://api-{target}/x",
		"https://{a}.{b}/x",
	}
	for _, raw := range rejected {
		t.Run("rejected/"+raw, func(t *testing.T) {
			err := validateHTTPRequest(HTTPRequestOptions{URL: raw})
			if err == nil {
				t.Fatalf("url %q was accepted; a fully templated host lets a request choose an arbitrary upstream", raw)
			}
			if !strings.Contains(err.Error(), "literal domain suffix") {
				t.Fatalf("error = %v, want the v1 wording about a literal domain suffix", err)
			}
		})
	}

	accepted := []string{
		"https://{envName}-api.example.com/v1/events",
		"https://{tenant}.example.com/x",
		"https://api.example.com/{rest}",
		"https://api.example.com:8443/{rest}",
		"${UPSTREAM_BASE}/{rest}",
	}
	for _, raw := range accepted {
		t.Run("accepted/"+raw, func(t *testing.T) {
			if err := validateHTTPRequest(HTTPRequestOptions{URL: raw}); err != nil {
				t.Fatalf("url %q was rejected: %v", raw, err)
			}
		})
	}
}

// TestAllowPrivateTargetsRequiresATemplatedHost keeps the opt-out honest: on
// a literal host the egress policy never applies, so the option would read
// like a security decision while doing nothing.
func TestAllowPrivateTargetsRequiresATemplatedHost(t *testing.T) {
	err := validateHTTPRequest(HTTPRequestOptions{URL: "http://10.0.0.5/x", AllowPrivateTargets: true})
	if err == nil {
		t.Fatal("allow_private_targets on a literal host was accepted; it has no effect there")
	}
	if err := validateHTTPRequest(HTTPRequestOptions{
		URL:                 "https://{tenant}.internal.example.com/x",
		AllowPrivateTargets: true,
	}); err != nil {
		t.Fatalf("allow_private_targets on a templated host was rejected: %v", err)
	}
}

// TestCleanOutboundPathKeepsEncodedDotsEncoded is a regression test for a
// traversal the cleaner used to manufacture. It resolves "." and ".." for
// defense in depth, but it must do that on the ESCAPED path: cleaning the
// decoded path turned the literal text "%2e%2e%2f" -- which is inert, just a
// percent sign and some characters -- into "../" and then resolved it away,
// so an upstream received a path that escaped the prefix the template exists
// to confine callers to.
func TestCleanOutboundPathKeepsEncodedDotsEncoded(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"literal traversal is resolved", "https://up.test/a/../b", "https://up.test/b"},
		{"single dot is resolved", "https://up.test/a/./b", "https://up.test/a/b"},
		{"encoded dots stay literal", "https://up.test/tenants/%2e%2e%2fadmin/ro", "https://up.test/tenants/%2e%2e%2fadmin/ro"},
		{"trailing slash is preserved", "https://up.test/a/b/", "https://up.test/a/b/"},
		{"already clean is untouched", "https://up.test/a/b", "https://up.test/a/b"},
		{"root stays root", "https://up.test/", "https://up.test/"},
		{"no path is untouched", "https://up.test", "https://up.test"},
		{"unparseable input is returned as-is", "://nope", "://nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanOutboundPath(tc.in); got != tc.want {
				t.Fatalf("cleanOutboundPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEgressDialContextVetsResolvedAddresses covers the DNS-rebinding closure.
// The static host checks run against the name; this runs against what the name
// actually resolved to, and dials that exact IP -- so there is no second
// lookup between the check and the connection for an attacker to poison.
func TestEgressDialContextVetsResolvedAddresses(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	loopback := ln.Addr().String()

	t.Run("guard off dials without vetting", func(t *testing.T) {
		conn, err := egressDialContext(withEgressGuard(context.Background(), false), "tcp", loopback)
		if err != nil {
			t.Fatalf("unguarded dial to %s failed: %v", loopback, err)
		}
		_ = conn.Close()
	})

	t.Run("guard on refuses a loopback resolution", func(t *testing.T) {
		conn, err := egressDialContext(withEgressGuard(context.Background(), true), "tcp", loopback)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("guarded dial to %s succeeded; a loopback address must never be reached", loopback)
		}
		if !strings.Contains(err.Error(), "not an allowed egress target") {
			t.Fatalf("error = %v, want it to name the egress policy", err)
		}
	})

	t.Run("guard on rejects an address with no port", func(t *testing.T) {
		if _, err := egressDialContext(withEgressGuard(context.Background(), true), "tcp", "example.com"); err == nil {
			t.Fatal("a malformed host:port was accepted")
		}
	})

	t.Run("guard on fails closed when the name does not resolve", func(t *testing.T) {
		// .invalid is reserved by RFC 2606 and must never resolve.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := egressDialContext(withEgressGuard(ctx, true), "tcp", "no-such-host.invalid:80"); err == nil {
			t.Fatal("an unresolvable host was dialed; resolution failure must fail closed")
		}
	})
}

// Controlled DNS proves classification happens on resolved addresses, including
// IPv6/mapped IPv4, without ever contacting metadata or a public destination.
func TestEgressDialPinsControlledDNSAddresses(t *testing.T) {
	for _, ipText := range []string{"10.0.0.5", "169.254.169.254", "::1", "fd00::5", "fe80::5", "::ffff:169.254.169.254", "93.184.216.34", "2001:4860:4860::8888"} {
		t.Run(ipText, func(t *testing.T) {
			ip := net.ParseIP(ipText)
			installEgressDNS(t, ip, strings.Contains(ipText, ":"))
			const host = "controlled-egress.example.test"
			if err := validateEgressURL("https://"+host+"/request", true); err != nil {
				t.Fatalf("controlled public name failed the initial policy: %v", err)
			}
			var dialed []string
			refused := errors.New("fixture refused vetted connection")
			saved := egressDialer
			egressDialer = &net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
				dialed = append(dialed, address)
				return refused
			}}
			t.Cleanup(func() { egressDialer = saved })
			_, err := egressDialContext(withEgressGuard(t.Context(), true), "tcp", host+":443")
			if ipText != "93.184.216.34" && ipText != "2001:4860:4860::8888" {
				if err == nil || len(dialed) != 0 || !strings.Contains(err.Error(), "not an allowed egress target") {
					t.Fatalf("private DNS answer was not refused before dial: error=%v dials=%v", err, dialed)
				}
			} else if !errors.Is(err, refused) || len(dialed) != 1 || dialed[0] != net.JoinHostPort(ip.String(), "443") {
				t.Fatalf("vetted DNS IP was not pinned at dial: error=%v dials=%v", err, dialed)
			}
		})
	}
}

func installEgressDNS(t *testing.T, ip net.IP, ipv6 bool) {
	t.Helper()
	server, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [4096]byte
		for {
			n, peer, err := server.ReadFrom(buf[:])
			if err != nil {
				return
			}
			var question dnsmessage.Message
			if err := question.Unpack(buf[:n]); err != nil {
				t.Error(err)
				continue
			}
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: question.ID, Response: true, Authoritative: true, RecursionDesired: question.RecursionDesired, RecursionAvailable: true}, Questions: question.Questions}
			for _, q := range question.Questions {
				header := dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 60}
				if v4 := ip.To4(); !ipv6 && q.Type == dnsmessage.TypeA {
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AResource{A: [4]byte(v4)}})
				} else if ipv6 && q.Type == dnsmessage.TypeAAAA {
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AAAAResource{AAAA: [16]byte(ip.To16())}})
				}
			}
			raw, err := response.Pack()
			if err != nil {
				t.Error(err)
				continue
			}
			if _, err := server.WriteTo(raw, peer); err != nil {
				t.Error(err)
			}
		}
	}()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.LocalAddr().String())
	}}
	t.Cleanup(func() {
		net.DefaultResolver = previous
		_ = server.Close()
		<-done
	})
}
