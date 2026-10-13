package api

import (
	"net/http"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/problem"
	replayconfig "github.com/jaysongiroux/hook_way/internal/replay/config"
)

// operatorCORS applies only to operator routes. Capture, health and metrics
// remain server-to-server surfaces, and never receive browser permissions.
func operatorCORS(next *http.ServeMux, routes *http.ServeMux, origins []string) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		if replayconfig.ValidOrigin(origin) {
			allowed[origin] = true
		}
	}
	matches := func(r *http.Request, method string) string {
		probe := new(http.Request)
		*probe = *r
		probe.Method = method
		_, pattern := routes.Handler(probe)
		return pattern
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		preflight := r.Method == http.MethodOptions && requestedMethod != ""
		operatorPath := matches(r, http.MethodGet) != "" || matches(r, http.MethodPost) != ""
		_, actualPattern := next.Handler(r)
		if !operatorPath || (!preflight && actualPattern != "" && matches(r, r.Method) == "") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if len(r.Header.Values("Origin")) == 0 && !preflight {
			next.ServeHTTP(w, r)
			return
		}
		if len(r.Header.Values("Origin")) != 1 || !allowed[origin] {
			problem.Write(w, http.StatusForbidden, "origin_forbidden", "browser origin is not permitted")
			return
		}
		if preflight && (requestedMethod != http.MethodGet && requestedMethod != http.MethodPost || matches(r, requestedMethod) == "") {
			problem.Write(w, http.StatusForbidden, "cors_preflight_forbidden", "requested method is not permitted")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, X-Hookway-Payload-Content-Type, Retry-After")
		if !preflight {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			switch strings.ToLower(strings.TrimSpace(header)) {
			case "", "authorization", "content-type", "idempotency-key":
			default:
				problem.Write(w, http.StatusForbidden, "cors_preflight_forbidden", "requested headers are not permitted")
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", requestedMethod)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}
