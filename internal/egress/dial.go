package egress

import (
	"context"
	"fmt"
	"net"
)

// DialContext resolves once, classifies each candidate, and dials only vetted IP
// literals. No second hostname lookup can change the address after validation.
// The caller chooses the resolver/dialer and keeps protected connection pools
// separate from connections opened without this guard.
func DialContext(ctx context.Context, network, addr string, resolver *net.Resolver, dialer *net.Dialer) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	lastErr := fmt.Errorf("no allowed address for %q", host)
	for _, ip := range ips {
		if reason := BlockedIPReason(ip.IP); reason != "" {
			lastErr = fmt.Errorf("host %q resolves to %s, which is not an allowed egress target (%s)", host, ip.IP, reason)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
