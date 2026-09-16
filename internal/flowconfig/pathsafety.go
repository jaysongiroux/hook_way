package flowconfig

import "strings"

// UnsafePathVar reports the first path variable whose captured value contains a
// ".." segment, which is never legitimate for a trigger path.
//
// This exists because net/http hands handlers an already percent-decoded
// URL.Path. Go's ServeMux redirects a literal "/proxy/../admin", but
// "/proxy/..%2f..%2fadmin" decodes to "../.." only after routing, so a greedy
// {rest...} capture would carry the traversal into a proxied upstream URL.
// Most upstreams then normalize "/safe/../../admin" to "/admin", escaping the
// prefix the proxy config exists to confine callers to.
//
// Rejecting at the trigger fails closed and does not depend on how any
// particular upstream normalizes paths. http_request additionally cleans the
// outbound path as defense in depth.
func UnsafePathVar(pathVars map[string]string) (string, bool) {
	for name, value := range pathVars {
		if hasTraversalSegment(value) {
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
