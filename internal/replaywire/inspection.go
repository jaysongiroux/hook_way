package replaywire

import "net/http"

const InspectionPayloadVersion = 1

// InspectionPayload contains display data only, never an executable snapshot.
// Headers and variables remain behind the payload permission, like body bytes.
type InspectionPayload struct {
	Version   int               `json:"version"`
	Body      InspectionBody    `json:"body"`
	Headers   http.Header       `json:"headers"`
	Variables map[string]string `json:"variables"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
}
type InspectionBody struct {
	ContentType string `json:"content_type"`
	Encoding    string `json:"encoding"`
	Content     string `json:"content"`
}
