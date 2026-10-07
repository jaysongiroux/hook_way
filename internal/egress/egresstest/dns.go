// Package egresstest provides a local DNS fixture for outbound boundary tests.
package egresstest

import (
	"context"
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// Resolver answers A/AAAA questions from the supplied fixture without querying
// external DNS. answers may change over time to exercise address replacement.
func Resolver(t *testing.T, answers func() []net.IP, forceAAAA ...bool) *net.Resolver {
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
				header := dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 0}
				for _, ip := range answers() {
					ipv6 := ip.To4() == nil || len(forceAAAA) > 0 && forceAAAA[0]
					if v4 := ip.To4(); !ipv6 && q.Type == dnsmessage.TypeA {
						response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AResource{A: [4]byte(v4)}})
					} else if ipv6 && q.Type == dnsmessage.TypeAAAA {
						response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AAAAResource{AAAA: [16]byte(ip.To16())}})
					}
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
	t.Cleanup(func() { _ = server.Close(); <-done })
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.LocalAddr().String())
	}}
}
