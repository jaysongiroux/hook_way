// Package apikey provides a minimal bearer-token guard for admin-style HTTP
// endpoints (metrics, the config API). It performs a constant-time equality
// check against a single configured key.
package apikey

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Middleware guards next with a bearer-token API key. When key is empty, auth is
// disabled and next is returned unwrapped. Otherwise every request must carry an
// "Authorization: Bearer <key>" header equal to key (constant-time compared);
// missing or mismatched credentials receive 401 with a WWW-Authenticate: Bearer
// challenge.
func Middleware(key string, next http.Handler) http.Handler {
	if key == "" {
		return next
	}
	expected := []byte(key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(token), expected) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken returns the token from an Authorization header value, or "" when
// the scheme is not a case-insensitive "Bearer".
func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
