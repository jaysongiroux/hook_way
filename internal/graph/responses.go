package graph

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// ResponsePolicy maps a finite execution outcome to an explicit HTTP response.
// Bodies are static: internal errors, credentials and request bytes are never
// interpolated. An explicit respond node takes precedence on handled paths.
type ResponsePolicy struct {
	Status  int               `yaml:"status" json:"status"`
	Body    string            `yaml:"body,omitempty" json:"body,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

var ResponseOutcomes = []string{"responded", "published", "inspected", "dropped", "publish_error", "partial_failure", "auth_error", "body_error", "lookup_error", "payload_too_large", "error"}

func ValidateResponses(responses map[string]ResponsePolicy) []string {
	var problems []string
	for outcome, policy := range responses {
		known := false
		for _, key := range ResponseOutcomes {
			if key == outcome {
				known = true
			}
		}
		if !known {
			problems = append(problems, fmt.Sprintf("responses: unsupported outcome %q", outcome))
		}
		if policy.Status < 200 || policy.Status > 599 {
			problems = append(problems, fmt.Sprintf("responses.%s.status must be 200..599", outcome))
		}
		if len(policy.Body) > 65536 || !utf8.ValidString(policy.Body) {
			problems = append(problems, fmt.Sprintf("responses.%s.body must be UTF-8 and at most 64 KiB", outcome))
		}
		if (policy.Status == 204 || policy.Status == 304) && policy.Body != "" {
			problems = append(problems, fmt.Sprintf("responses.%s.body must be empty for status %d", outcome, policy.Status))
		}
		if len(policy.Headers) > 64 {
			problems = append(problems, fmt.Sprintf("responses.%s.headers is limited to 64 entries", outcome))
		}
		seenHeaders := map[string]bool{}
		for name, value := range policy.Headers {
			canonical := http.CanonicalHeaderKey(name)
			validValue := true
			for _, c := range []byte(value) {
				if c < 32 && c != 9 || c == 127 {
					validValue = false
				}
			}
			valid := name != "" && !seenHeaders[canonical]
			seenHeaders[canonical] = true
			for _, c := range name {
				validToken := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)
				if !validToken {
					valid = false
				}
			}
			switch http.CanonicalHeaderKey(name) {
			case "Connection", "Content-Length", "Transfer-Encoding", "Trailer", "Upgrade", "Keep-Alive", "Proxy-Connection":
				valid = false
			}
			if !valid || !validValue || strings.ContainsAny(value, "\r\n\x00") || !utf8.ValidString(value) || len(value) > 8192 {
				problems = append(problems, fmt.Sprintf("responses.%s.headers contains an invalid or framing header", outcome))
			}
		}
	}
	return problems
}
