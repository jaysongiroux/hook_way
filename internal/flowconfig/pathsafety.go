package flowconfig

import "strings"

// UnsafePathVar reports the first path variable whose captured value can
// change the shape of an outbound URL rather than just fill a slot in it: a
// ".." segment, a "#" or "?" URL delimiter, a backslash, or an ASCII control
// character. None of those is ever legitimate for a trigger path.
//
// This exists because net/http hands handlers an already percent-decoded
// URL.Path. Go's ServeMux redirects a literal "/proxy/../admin", but
// "/proxy/..%2f..%2fadmin" decodes to "../.." only after routing, so a greedy
// {rest...} capture would carry the traversal into a proxied upstream URL.
// Most upstreams then normalize "/safe/../../admin" to "/admin", escaping the
// prefix the proxy config exists to confine callers to.
//
// The same decode-after-routing step turns "%23" into "#" and "%3F" into "?".
// Rendered into an operator template such as
// "https://up.example.com/tenants/{tenant}/readonly", url.Parse reads those
// back as fragment and query delimiters and silently drops everything after
// them -- including the mandatory "/readonly" suffix the template exists to
// enforce, and, for "?", replacing it with an attacker-chosen query string:
//
//	tenant="acme"      -> /tenants/acme/readonly              (intended)
//	tenant="acme#"     -> /tenants/acme                       (suffix dropped)
//	tenant="acme?x=1&" -> /tenants/acme?x=1&/readonly         (suffix dropped)
//
// Rejecting at the trigger fails closed and does not depend on how any
// particular upstream normalizes paths, or on which of the several URL
// parsers between here and the upstream re-reads a delimiter. http_request
// additionally cleans the outbound path as defense in depth.
func UnsafePathVar(pathVars map[string]string) (string, bool) {
	for name, value := range pathVars {
		if hasTraversalSegment(value) || hasURLDelimiterOrControl(value) {
			return name, true
		}
	}
	return "", false
}

// hasTraversalSegment reports whether value contains ".." as a whole path
// segment. A filename that merely contains dots (for example "..hidden" or
// "a..b") is not traversal and stays allowed.
func hasTraversalSegment(value string) bool {
	if value == ".." {
		return true
	}
	for _, seg := range strings.Split(value, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// hasURLDelimiterOrControl reports whether value contains a byte that can
// terminate or re-scope a URL path downstream: "#" (fragment), "?" (query),
// "\" (treated as "/" by several clients and by Windows path handling), or an
// ASCII control character (request smuggling / header injection, and rejected
// outright by url.Parse anyway). Non-ASCII bytes are left alone: they are
// ordinary path content and are percent-encoded on the way out.
func hasURLDelimiterOrControl(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 || c == 0x7f {
			return true
		}
		if c == '#' || c == '?' || c == '\\' {
			return true
		}
	}
	return false
}
