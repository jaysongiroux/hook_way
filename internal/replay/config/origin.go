package config

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidOrigin accepts the serialized origin a browser sends, never a URL prefix.
func ValidOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(origin, "#?%") || u.String() != origin {
		return false
	}
	hostname := u.Hostname()
	if hostname == "" || hostname != strings.ToLower(hostname) || strings.HasSuffix(u.Host, ":") {
		return false
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.String() != hostname {
			return false
		}
	} else {
		if len(hostname) > 253 {
			return false
		}
		for _, label := range strings.Split(hostname, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, c := range label {
				if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
					return false
				}
			}
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || u.Scheme == "http" && n == 80 || u.Scheme == "https" && n == 443 {
			return false
		}
	}
	return true
}
