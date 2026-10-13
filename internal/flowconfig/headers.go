package flowconfig

import (
	"net/http"
	"strings"
)

// hopByHopHeaders are hop-by-hop and framing headers that must not be copied
// onto an outbound proxy request or a passthrough response.
var hopByHopHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Host": true, "Content-Length": true,
}

func isHopByHop(name string) bool {
	return hopByHopHeaders[http.CanonicalHeaderKey(name)]
}

// Connection nominates additional hop-by-hop fields beyond the standard list.
func connectionHeaders(h http.Header) map[string]bool {
	out := map[string]bool{}
	for k, values := range h {
		if !strings.EqualFold(k, "Connection") {
			continue
		}
		for _, value := range values {
			for _, name := range strings.Split(value, ",") {
				out[http.CanonicalHeaderKey(strings.TrimSpace(name))] = true
			}
		}
	}
	return out
}
