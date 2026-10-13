package queue

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/egress/egresstest"
)

func TestWebhookTemplatedAuthorityVetsDNSAndPinsAddress(t *testing.T) {
	for _, address := range []string{"10.0.0.5", "169.254.169.254", "::1", "fd00::5", "fe80::5", "::ffff:169.254.169.254", "93.184.216.34", "2001:4860:4860::8888"} {
		t.Run(address, func(t *testing.T) {
			ip := net.ParseIP(address)
			resolver := egresstest.Resolver(t, func() []net.IP { return []net.IP{ip} }, strings.Contains(address, ":"))
			savedResolver, savedDialer := net.DefaultResolver, webhookEgressDialer
			net.DefaultResolver = resolver
			var dialed []string
			refused := errors.New("fixture refused vetted connection")
			webhookEgressDialer = &net.Dialer{Control: func(_, addr string, _ syscall.RawConn) error {
				dialed = append(dialed, addr)
				return refused
			}}
			t.Cleanup(func() { net.DefaultResolver, webhookEgressDialer = savedResolver, savedDialer })
			pub, err := NewWebhookPublisher(config.WebhookConfig{URL: "http://{tenant}-api.example.test/ingest", DeliveryMode: "sync", Timeout: time.Second, Retry: config.RetryPolicyConfig{MaxAttempts: 1}}, "notify", []WebhookTarget{{Name: "events"}}, WebhookPublisherOptions{Logger: testLogger()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = pub.Close() })
			d, _ := pub.delivery.lookup("events")
			if transport := d.client.Transport.(*http.Transport); transport.Proxy != nil {
				t.Fatal("guarded transport retained a forward proxy")
			}
			err = pub.Publish(t.Context(), "notify", "events", Message{ID: "fixture", Variables: map[string]string{"tenant": "metadata"}})
			public := address == "93.184.216.34" || address == "2001:4860:4860::8888"
			if !public {
				if err == nil || len(dialed) != 0 {
					t.Fatalf("private DNS reached dial: err=%v dials=%v", err, dialed)
				}
			} else if err == nil || len(dialed) != 1 || dialed[0] != net.JoinHostPort(ip.String(), "80") {
				t.Fatalf("public DNS did not pin vetted IP: err=%v dials=%v", err, dialed)
			}
		})
	}
}

func TestWebhookTemplatedAuthorityRechecksChangedDNS(t *testing.T) {
	var replaced atomic.Bool
	resolver := egresstest.Resolver(t, func() []net.IP {
		if replaced.Load() {
			return []net.IP{net.ParseIP("169.254.169.254")}
		}
		return []net.IP{net.ParseIP("93.184.216.34")}
	})
	savedResolver, savedDialer := net.DefaultResolver, webhookEgressDialer
	net.DefaultResolver = resolver
	var dials atomic.Int32
	webhookEgressDialer = &net.Dialer{Control: func(_, addr string, _ syscall.RawConn) error {
		dials.Add(1)
		if addr != "93.184.216.34:80" {
			t.Errorf("unexpected dial: %s", addr)
		}
		return errors.New("fixture refused public connection")
	}}
	t.Cleanup(func() { net.DefaultResolver, webhookEgressDialer = savedResolver, savedDialer })
	pub, err := NewWebhookPublisher(config.WebhookConfig{URL: "http://{tenant}-api.example.test/ingest", DeliveryMode: "sync", Timeout: time.Second, Retry: config.RetryPolicyConfig{MaxAttempts: 1}}, "notify", []WebhookTarget{{Name: "events"}}, WebhookPublisherOptions{Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	msg := Message{ID: "fixture", Variables: map[string]string{"tenant": "metadata"}}
	if err := pub.Publish(t.Context(), "notify", "events", msg); err == nil || dials.Load() != 1 {
		t.Fatalf("first vetted dial: %v, count=%d", err, dials.Load())
	}
	replaced.Store(true)
	if err := pub.Publish(t.Context(), "notify", "events", msg); err == nil || dials.Load() != 1 {
		t.Fatalf("DNS replacement reached metadata: %v, count=%d", err, dials.Load())
	}
}

func TestWebhookPrivateAuthorityOptInPreservesNormalAuthentication(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/ingest/events" || r.Header.Get("X-Delivery-Key") != "synthetic-delivery-key" || !strings.Contains(string(body), "fixture") {
			t.Error("local receiver lost path/auth/body")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	resolver := egresstest.Resolver(t, func() []net.IP { return []net.IP{net.ParseIP("127.0.0.1")} })
	saved := net.DefaultResolver
	net.DefaultResolver = resolver
	t.Cleanup(func() { net.DefaultResolver = saved })
	// Open an unguarded connection first. The guarded destination must not reuse
	// that connection to the same rendered authority after DNS policy rejects it.
	for _, optIn := range []bool{true, false} {
		cfg := config.WebhookConfig{URL: "http://{tenant}-api.example.test:" + u.Port() + "/ingest", AllowPrivateTargets: optIn, DeliveryMode: "sync", Timeout: time.Second, Retry: config.RetryPolicyConfig{MaxAttempts: 1}, Auth: config.WebhookAuthConfig{Type: "api_key", Header: "X-Delivery-Key", Value: "synthetic-delivery-key"}}
		pub, err := NewWebhookPublisher(cfg, "notify", []WebhookTarget{{Name: "events"}}, WebhookPublisherOptions{Logger: testLogger()})
		if err != nil {
			t.Fatal(err)
		}
		// Fixtures use direct dialing for the deliberate private exception, so
		// an ambient workstation proxy cannot hide the loopback receiver.
		d, _ := pub.delivery.lookup("events")
		d.client.Transport.(*http.Transport).Proxy = nil
		t.Cleanup(d.client.CloseIdleConnections)
		before := calls.Load()
		err = pub.Publish(t.Context(), "notify", "events", Message{ID: "fixture", Variables: map[string]string{"tenant": "orders"}})
		if optIn {
			if err != nil || calls.Load() != before+1 {
				t.Fatalf("explicit local opt-in failed: %v", err)
			}
		} else if err == nil || calls.Load() != before {
			t.Fatalf("private DNS allowed without opt-in: %v", err)
		}
		for _, value := range []string{"a.b", "a_b", "../admin", "a@attacker", "a:80", "-a", "a-", strings.Repeat("a", 64)} {
			if err := pub.Publish(t.Context(), "notify", "events", Message{ID: "fixture", Variables: map[string]string{"tenant": value}}); err == nil {
				t.Fatalf("opt-in=%v bypassed host constraints", optIn)
			}
		}
		_ = pub.Close()
	}
	if calls.Load() != 1 {
		t.Fatalf("unsafe substitution reached receiver: %d calls", calls.Load())
	}
}

func TestWebhookPrivateOptInRejectsLiteralHostAndPreservesPathSafety(t *testing.T) {
	if _, err := NewWebhookPublisher(config.WebhookConfig{URL: "http://localhost/{tenant}", AllowPrivateTargets: true}, "notify", []WebhookTarget{{Name: "events"}}, WebhookPublisherOptions{}); err == nil {
		t.Fatal("literal-host private permission was accepted")
	}
	pub, err := NewWebhookPublisher(config.WebhookConfig{URL: "http://{tenant}-api.example.test/{route}", AllowPrivateTargets: true, DeliveryMode: "sync"}, "notify", []WebhookTarget{{Name: "events"}}, WebhookPublisherOptions{Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	for _, value := range []string{"../admin", "..%2fadmin", "//attacker", "safe?token=secret", "safe#fragment", "safe\\admin"} {
		if err := pub.Publish(t.Context(), "notify", "events", Message{Variables: map[string]string{"tenant": "orders", "route": value}}); err == nil || !strings.Contains(err.Error(), "unsafe value") {
			t.Fatalf("private opt-in bypassed path constraints: %v", err)
		}
	}
}
