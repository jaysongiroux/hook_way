package flowconfig

import "net/http"

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
