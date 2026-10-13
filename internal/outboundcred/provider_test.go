package outboundcred

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

func oauthProfile(t *testing.T, endpoint, origin string) flowconfig.CredentialProfile {
	t.Helper()
	t.Setenv("SAMPLE_CLIENT_ID", "synthetic-client")
	t.Setenv("SAMPLE_CLIENT_SECRET", "synthetic-password")
	return flowconfig.CredentialProfile{Kind: "oauth_client_credentials", TokenURL: endpoint, AllowedOrigins: []string{origin}, AllowPrivateTargets: true, Timeout: "2s", ClientID: &flowconfig.SecretRefOptions{Provider: "env", Name: "SAMPLE_CLIENT_ID"}, ClientSecret: &flowconfig.SecretRefOptions{Provider: "env", Name: "SAMPLE_CLIENT_SECRET"}, InvalidSession: &flowconfig.SessionMarker{Path: "0.code", Value: "expired"}}
}
func TestSingleFlightOwnDeadlinesAndGenerationRecovery(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 4)
	release := make(chan struct{}, 4)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Method != "POST" || r.FormValue("grant_type") != "client_credentials" {
			t.Error("bad acquisition request")
		}
		id, password, ok := r.BasicAuth()
		if !ok || id != "synthetic-client" || password != "synthetic-password" {
			t.Error("bad client credentials")
		}
		started <- struct{}{}
		<-release
		_, _ = fmt.Fprintf(w, `{"access_token":"synthetic-%d","expires_in":3600,"token_type":"Bearer"}`, n)
	}))
	defer endpoint.Close()
	p, e := New("sample", oauthProfile(t, endpoint.URL, endpoint.URL), nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = p.Close() }()
	// The first waiter's cancellation must not cancel acquisition for the others.
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := p.Acquire(ctx); first <- e }()
	<-started
	var wg sync.WaitGroup
	values := make(chan flowconfig.CredentialMaterial, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := p.Acquire(context.Background())
			if e != nil {
				t.Error(e)
			}
			values <- v
		}()
	}
	cancel()
	if <-first == nil {
		t.Fatal("cancelled waiter succeeded")
	}
	release <- struct{}{}
	wg.Wait()
	close(values)
	var old flowconfig.CredentialMaterial
	for v := range values {
		if v.Generation != 1 || v.Header.Get("Authorization") != "Bearer synthetic-1" {
			t.Fatal(v)
		}
		old = v
	}
	if calls.Load() != 1 {
		t.Fatal("stampede", calls.Load())
	}
	if p.Reject(old, 401, []byte(`{"code":"expired"}`)) || p.Reject(old, 403, []byte(`[{"code":"expired"}]`)) {
		t.Fatal("nonmatching rejection")
	}
	if !p.Reject(old, 401, []byte(`[{"code":"expired"}]`)) {
		t.Fatal("matching session not rejected")
	}
	refresh := make(chan flowconfig.CredentialMaterial, 1)
	go func() {
		v, e := p.Acquire(context.Background())
		if e != nil {
			t.Error(e)
		}
		refresh <- v
	}()
	<-started
	release <- struct{}{}
	current := <-refresh
	if current.Generation != 2 {
		t.Fatal(current)
	}
	// Delayed 401s from generation 1 reuse generation 2 without clearing it.
	for range 32 {
		if !p.Reject(old, 401, []byte(`[{"code":"expired"}]`)) {
			t.Fatal("late marker")
		}
		v, e := p.Acquire(context.Background())
		if e != nil || v.Generation != 2 {
			t.Fatal(v, e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("stale failures discarded newer token", calls.Load())
	}
	old.Header.Set("Authorization", "mutated")
	v, _ := p.Acquire(context.Background())
	if v.Header.Get("Authorization") != "Bearer synthetic-2" {
		t.Fatal("material aliases cache")
	}
}
func TestInstanceAdmissionNoRedirectAndBoundedParsing(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		redirect    bool
	}{{"missing", `{}`, false}, {"invalid", `{"access_token":"sensitive\r\nvalue"}`, false}, {"oversize", strings.Repeat("x", 65537), false}, {"instance", `{"access_token":"sensitive","instance_url":"https://approved.example.evil.example"}`, false}, {"userinfo", `{"access_token":"sensitive","instance_url":"https://user:pass@approved.example"}`, false}, {"redirect", "", true}, {"expiry", `{"access_token":"sensitive","expires_in":0}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var forbidden atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) }))
			defer other.Close()
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.redirect {
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
				} else {
					_, _ = fmt.Fprint(w, tc.reply)
				}
			}))
			defer endpoint.Close()
			c := oauthProfile(t, endpoint.URL, "https://approved.example")
			if tc.name == "instance" || tc.name == "userinfo" {
				c.InstancePath = "instance_url"
			}
			p, e := New("sample", c, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = p.Close() }()
			_, e = p.Acquire(context.Background())
			if e == nil || strings.Contains(e.Error(), "sensitive") {
				t.Fatal("unsafe failure", e)
			}
			if forbidden.Load() != 0 {
				t.Fatal("token followed redirect")
			}
			if _, e := p.Target(flowconfig.CredentialMaterial{}, other.URL); e == nil {
				t.Fatal("unapproved resource admitted")
			}
		})
	}
}
func TestPerRequestExchangeStaticRotationAndCloseCancellation(t *testing.T) {
	t.Setenv("SAMPLE_PASSWORD", "synthetic-password")
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("password") != "synthetic-password" {
			t.Error("bad exchange")
		}
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"data":{"token_field":"synthetic-token"}}`)
	}))
	defer endpoint.Close()
	c := flowconfig.CredentialProfile{Kind: "token_exchange", TokenURL: endpoint.URL, AllowedOrigins: []string{endpoint.URL}, AllowPrivateTargets: true, TokenPath: "data.token_field", SecretParameters: map[string]flowconfig.SecretRefOptions{"password": {Provider: "env", Name: "SAMPLE_PASSWORD"}}}
	p, e := New("sample", c, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	for range 3 {
		v, e := p.Acquire(context.Background())
		if e != nil || v.Header.Get("Authorization") != "Bearer synthetic-token" || p.Reject(v, 401, []byte(`{}`)) {
			t.Fatal(v, e)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("exchange cached")
	}
	_ = p.Close()
	if _, e := p.Acquire(context.Background()); e == nil {
		t.Fatal("closed provider acquired")
	}
	t.Setenv("SAMPLE_KEY", "first")
	c = flowconfig.CredentialProfile{Kind: "static", AllowedOrigins: []string{endpoint.URL}, AllowPrivateTargets: true, Headers: map[string]flowconfig.CredentialHeader{"X-Sample-Key": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "SAMPLE_KEY", Refresh: "10ms"}}}}
	p, e = New("sample", c, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = p.Close() }()
	t.Setenv("SAMPLE_KEY", "second")
	deadline := time.Now().Add(time.Second)
	for {
		v, e := p.Acquire(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if v.Header.Get("X-Sample-Key") == "second" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("static key did not rotate")
		}
		time.Sleep(time.Millisecond)
	}
	t.Setenv("SAMPLE_KEY", "invalid\r\nkey")
	time.Sleep(30 * time.Millisecond)
	v, e := p.Acquire(context.Background())
	if e != nil || v.Header.Get("X-Sample-Key") != "second" {
		t.Fatal("bad rotation displaced validated value", v, e)
	}
}
func TestCloseCancelsAcquisition(t *testing.T) {
	started := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer endpoint.Close()
	p, e := New("sample", oauthProfile(t, endpoint.URL, endpoint.URL), nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := p.Acquire(context.Background()); done <- e }()
	<-started
	_ = p.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("closed acquisition succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close left waiter running")
	}
	_ = p.Close()
}

func TestReportedExpiryAndApprovedInstanceOrigin(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"access_token":"synthetic-token","expires_in":0.02,"instance_url":"https://instance.example.org"}`)
	}))
	defer endpoint.Close()
	c := oauthProfile(t, endpoint.URL, "https://api.example.org")
	c.AllowedOrigins = append(c.AllowedOrigins, "https://instance.example.org")
	c.InstancePath = "instance_url"
	p, e := New("sample", c, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = p.Close() }()
	first, e := p.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	target, e := p.Target(first, "https://api.example.org/a%2Fb?sample=a%2Bb")
	if e != nil || target != "https://instance.example.org/a%2Fb?sample=a%2Bb" {
		t.Fatal(target, e)
	}
	time.Sleep(25 * time.Millisecond)
	second, e := p.Acquire(context.Background())
	if e != nil || second.Generation != 2 || calls.Load() != 2 {
		t.Fatal(second, e, calls.Load())
	}
}
