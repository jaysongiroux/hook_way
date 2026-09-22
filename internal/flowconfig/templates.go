package flowconfig

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/vars"
)

func validateTemplate(field, value string) error {
	if _, err := vars.Parse(value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func validateHTTPURL(raw string) error {
	_, err := parseHTTPURL(raw)
	return err
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("URL scheme must be http or https")
	}
	if u.Host == "" {
		return nil, errors.New("URL must include a host")
	}
	return u, nil
}

// validateEgressURL is the runtime check on a rendered http_request target.
// It is validateHTTPURL plus, when the egress policy applies, a refusal to
// address anything on the gateway's own host or private network.
//
// The policy applies when the url template's authority contains a
// {placeholder}, because only then is the destination host chosen by
// request-derived data (env.Vars is seeded from the trigger's path variables
// and extended by lookup and transform nodes, and Template.Render substitutes
// raw text). A fully literal authority is the operator's own explicit
// decision -- proxying to an internal service is a legitimate configuration,
// and a literal "http://10.0.0.5:8080" in the config is not an SSRF.
func validateEgressURL(raw string, enforce bool) error {
	u, err := parseHTTPURL(raw)
	if err != nil {
		return err
	}
	if !enforce {
		return nil
	}
	if reason := internalHostReason(u.Hostname()); reason != "" {
		return fmt.Errorf("URL host %q is not an allowed egress target (%s); set allow_private_targets: true on this node if the upstream is deliberately internal", u.Hostname(), reason)
	}
	return nil
}

// validateURLHostTemplate requires a {variable} in the url authority to be
// followed by a literal dotted domain suffix, so a request can pick at most
// one label under a fixed domain and never the whole host. This reimplements
// for flow configs the contract v1 enforced for webhook destination URLs
// (internal/config's webhookURLTemplateIssues), including its wording.
//
// The gate is "the parsed authority has placeholders", not "the authority
// text contains a brace": vars.Parse treats "${VAR}" as literal text, so an
// unexpanded environment reference such as "${UPSTREAM_BASE}/{rest}" is not a
// templated host. It becomes a literal authority once the environment is
// expanded at load.
func validateURLHostTemplate(raw string) error {
	authTpl, err := vars.Parse(urlAuthority(raw))
	if err != nil {
		return fmt.Errorf("url host template is malformed: %w", err)
	}
	if !authTpl.HasPlaceholders() {
		return nil
	}
	if !hostTemplateHasLiteralSuffix(authTpl) {
		return errors.New(`a {variable} in the url host must be followed by a literal domain suffix: ` +
			`write the host as "{envName}-api.example.com" so requests cannot choose an arbitrary host`)
	}
	return nil
}

func hostTemplateHasLiteralSuffix(authTpl vars.Template) bool {
	host := authTpl.LiteralSuffix()
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		host = host[:colon]
	}
	return strings.Contains(host, ".")
}

// urlAuthorityIsTemplated reports whether the host of a url template is
// chosen, even partly, by a request-derived variable.
func urlAuthorityIsTemplated(raw string) bool {
	authTpl, err := vars.Parse(urlAuthority(raw))
	if err != nil {
		// A malformed template is rejected elsewhere; assume the unsafe case.
		return true
	}
	return authTpl.HasPlaceholders()
}

// urlAuthority returns the authority (host[:port]) of a raw, possibly
// templated URL without parsing it, so placeholders survive intact.
func urlAuthority(rawURL string) string {
	rest := rawURL
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rest = rawURL[i+3:]
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	return rest
}

// blockedNets are the destinations an internet-facing gateway must never be
// talked into addressing on a caller's behalf: its own host, its own link and
// private networks, and the ranges cloud providers put instance credentials
// on. IPv4-mapped IPv6 forms collapse to the IPv4 rules via To4().
var blockedNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",          // "this network", and 0.0.0.0 meaning localhost
		"10.0.0.0/8",         // RFC1918 private
		"100.64.0.0/10",      // RFC6598 CGNAT
		"127.0.0.0/8",        // loopback
		"169.254.0.0/16",     // link-local, incl. 169.254.169.254 metadata
		"172.16.0.0/12",      // RFC1918 private
		"192.0.0.0/24",       // IETF protocol assignments (incl. 192.0.0.192)
		"192.168.0.0/16",     // RFC1918 private
		"198.18.0.0/15",      // benchmarking
		"224.0.0.0/4",        // multicast
		"240.0.0.0/4",        // reserved, incl. 255.255.255.255 broadcast
		"100.100.100.200/32", // Alibaba Cloud instance metadata
		"::/128",             // unspecified
		"::1/128",            // loopback
		"fc00::/7",           // unique local
		"fe80::/10",          // link-local
		"ff00::/8",           // multicast
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("flowconfig: bad blocked CIDR " + c)
		}
		nets = append(nets, n)
	}
	return nets
}()

// internalHostSuffixes are names that only ever resolve inside a host or a
// private zone. "metadata.google.internal" is covered by ".internal".
var internalHostSuffixes = []string{".localhost", ".local", ".internal", ".localdomain", ".home.arpa"}

// internalHostReason returns a short reason when host must not be addressed,
// or "" when it is an acceptable egress target.
func internalHostReason(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "empty host"
	}
	if ip := net.ParseIP(host); ip != nil {
		return blockedIPReason(ip)
	}
	if host == "localhost" {
		return "loopback name"
	}
	for _, suffix := range internalHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return "internal-only domain " + suffix
		}
	}
	if reason := ambiguousHostReason(host); reason != "" {
		return reason
	}
	return ""
}

// blockedIPReason classifies a literal IP address.
func blockedIPReason(ip net.IP) string {
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

// ambiguousHostReason rejects a host that is neither a valid DNS name nor an
// address net.ParseIP accepts, but that a resolver or libc may still read as
// an IPv4 address: the decimal form "2130706433", the hex form "0x7f000001",
// and short forms such as "127.1" or octal "0177.0.0.1". Classifying those
// correctly is a well-known source of parser-differential SSRF bugs, so they
// are refused rather than interpreted.
func ambiguousHostReason(host string) string {
	for i := 0; i < len(host); i++ {
		c := host[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_'
		if !ok {
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
	// A real top-level domain is never all digits, so an all-digit final
	// label means this is a numeric address in some abbreviated encoding.
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

// isBodyPathKey reports the lookup-only {body.a.b} dotted-path spelling.
// vars.Parse rejects the dots, but lookupKey still resolves those keys from
// the envelope body.
func isBodyPathKey(key string) bool {
	if !strings.HasPrefix(key, "{body.") || !strings.HasSuffix(key, "}") {
		return false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(key, "{body."), "}")
	if path == "" {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		if !vars.ValidName(part) {
			return false
		}
	}
	return true
}
