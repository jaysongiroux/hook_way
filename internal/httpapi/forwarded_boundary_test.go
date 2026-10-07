package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourceIPUsesAllForwardedFieldsInWireOrder(t *testing.T) {
	s := sourceIPHelper(true, []string{"127.0.0.0/8", "10.0.0.0/8"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, s.sourceIP(r)) // #nosec G705 -- local diagnostic fixture echoes a validated source IP as plain text, never HTML.
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name string
		xff  []string
		want string
	}{
		{"rightmost untrusted field wins", []string{"203.0.113.10", "198.51.100.77"}, "198.51.100.77"},
		{"rightmost field contains trusted chain", []string{"203.0.113.10", "198.51.100.77, 10.0.0.5"}, "198.51.100.77"},
		{"multiple trusted hops", []string{"203.0.113.10, 10.0.0.1", "10.0.0.5"}, "203.0.113.10"},
		{"invalid client field stops walk", []string{"203.0.113.10", "not-an-ip, 10.0.0.5"}, "10.0.0.5"},
		{"IPv6 client remains supported", []string{"203.0.113.10", "2001:db8::7"}, "2001:db8::7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header["X-Forwarded-For"] = tc.xff
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || string(data) != tc.want {
				t.Fatalf("identity = %q, error %v, want %q", data, err, tc.want)
			}
		})
	}
}

func TestSourceIPRejectsUnusableForwarding(t *testing.T) {
	const peer = "10.0.0.9"
	s := sourceIPHelper(true, []string{"10.0.0.0/8"})
	for _, tc := range []struct {
		name      string
		xff, real []string
		want      string
	}{
		{"no forwarding", nil, nil, peer},
		{"single real IPv4", nil, []string{" 203.0.113.10 "}, "203.0.113.10"},
		{"single real IPv6", nil, []string{"2001:db8::7"}, "2001:db8::7"},
		{"invalid real IP", nil, []string{"not-an-ip"}, peer},
		{"real IP list", nil, []string{"203.0.113.10, 198.51.100.77"}, peer},
		{"duplicate real IP", nil, []string{"203.0.113.10", "198.51.100.77"}, peer},
		{"identical duplicate real IP", nil, []string{"203.0.113.10", "203.0.113.10"}, peer},
		{"malformed XFF prevents secondary identity", []string{"not-an-ip"}, []string{"203.0.113.10"}, peer},
		{"empty XFF prevents secondary identity", []string{""}, []string{"203.0.113.10"}, peer},
		{"malformed appended XFF", []string{"203.0.113.10", "not-an-ip"}, []string{"203.0.113.10"}, peer},
		{"valid XFF has priority", []string{"198.51.100.77"}, []string{"203.0.113.10"}, "198.51.100.77"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			req.RemoteAddr = peer + ":1234"
			if tc.xff != nil {
				req.Header["X-Forwarded-For"] = tc.xff
			}
			if tc.real != nil {
				req.Header["X-Real-Ip"] = tc.real
			}
			if got := s.sourceIP(req); got != tc.want {
				t.Fatalf("identity = %q, want %q", got, tc.want)
			}
		})
	}
}
