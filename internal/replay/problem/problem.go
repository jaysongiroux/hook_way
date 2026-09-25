// Package problem writes RFC 9457 problem details. Every replay API error
// uses it, so clients can switch on the stable code member.
package problem

import (
	"encoding/json"
	"net/http"
)

// ContentType is the RFC 9457 media type.
const ContentType = "application/problem+json"

// Details is an RFC 9457 problem object with the replay-specific code
// extension member.
type Details struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code"`
}

// Write sends a problem response. Type is about:blank, so Title is the HTTP
// status text and Code carries the machine-readable reason.
func Write(w http.ResponseWriter, status int, code, detail string) {
	h := w.Header()
	h.Set("Content-Type", ContentType)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Details{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code,
	})
}
