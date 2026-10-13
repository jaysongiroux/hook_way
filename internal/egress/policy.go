// Package egress shares outbound address classification and pinned DNS dialing
// between HTTP nodes and webhook delivery. Pure checks do not resolve DNS.
package egress

import (
	"fmt"
	"net"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/vars"
)

// Authority returns the unparsed URL authority so template braces survive.
func Authority(raw string) string {
	rest := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		rest = raw[i+3:]
	}
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// ValidateAuthorityVariables confines substitutions to single DNS labels,
// including when an operator permits private targets explicitly.
func ValidateAuthorityVariables(tpl vars.Template, values map[string]string) error {
	for _, name := range tpl.Names() {
		value := values[name]
		valid := len(value) > 0 && len(value) <= 63 && value[0] != '-' && value[len(value)-1] != '-'
		for i := 0; valid && i < len(value); i++ {
			c := value[i]
			valid = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
		}
		if !valid {
			return fmt.Errorf("URL authority variable %q must be a DNS label", name)
		}
	}
	return nil
}

// IPv4-mapped IPv6 addresses collapse to IPv4 before classification.
var blockedNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
		"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4", "100.100.100.200/32",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("egress: bad blocked CIDR " + c)
		}
		nets = append(nets, n)
	}
	return nets
}()

// BlockedIPReason returns why an IP is unsuitable for public egress, or "".
func BlockedIPReason(ip net.IP) string {
	if ip == nil {
		return "unparseable address"
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return "address in blocked range " + n.String()
		}
	}
	return ""
}

// BlockedHostReason classifies literal IPs, internal names and ambiguous numeric
// forms without consulting DNS. Resolved addresses must also be checked at dial.
func BlockedHostReason(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "empty host"
	}
	if ip := net.ParseIP(host); ip != nil {
		return BlockedIPReason(ip)
	}
	if host == "localhost" {
		return "loopback name"
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".localdomain", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return "internal-only domain " + suffix
		}
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		valid := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_'
		if !valid {
			return "host contains a character that is not valid in a DNS name"
		}
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return "host has an empty label"
		}
		if strings.HasPrefix(label, "0x") {
			return "hex-encoded address form"
		}
	}
	if allDigits(labels[len(labels)-1]) {
		return "numeric address form"
	}
	return ""
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
