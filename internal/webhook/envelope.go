package webhook

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	hooklogging "github.com/jaysongiroux/hook_way/internal/logging"
)

type Envelope struct {
	SchemaVersion  string `json:"schema_version"`
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	// Traceparent is the outbound W3C traceparent header value (additive;
	// schema_version stays 1.0). It carries the inbound header verbatim when
	// syntactically valid, or a synthesized one derived from TraceID
	// otherwise. See httpapi.traceparentFromRequest.
	Traceparent        string         `json:"traceparent,omitempty"`
	Endpoint           string         `json:"endpoint"`
	DestinationQueueID string         `json:"destination_queue_id"`
	Queue              string         `json:"queue"`
	ReceivedAt         time.Time      `json:"received_at"`
	Method             string         `json:"method"`
	Path               string         `json:"path"`
	Headers            http.Header    `json:"headers"`
	Query              map[string]any `json:"query"`
	Body               any            `json:"body"`
	RawBodyBase64      *string        `json:"raw_body_base64"`
	SourceIP           string         `json:"source_ip"`
	Auth               AuthInfo       `json:"auth"`
	// Variables holds the per-request routing variables resolved for this
	// event (endpoint `variables`), when any are declared. Additive; schema
	// stays 1.0. Serialized so a replay can re-render a templated webhook URL.
	Variables map[string]string `json:"variables,omitempty"`
	// In-memory only (never serialized). Populated at intake for reverse-proxy
	// (webhook proxy mode) so the exact original bytes/headers can be forwarded.
	RawBody     []byte      `json:"-"`
	OrigHeaders http.Header `json:"-"`
	// Hops is the loop-protection hop count read from the inbound request. The
	// webhook destination stamps Hops+1 on the next outbound delivery. In-memory
	// only.
	Hops int `json:"-"`
}

type AuthInfo struct {
	Type     string `json:"type"`
	Scheme   string `json:"scheme,omitempty"`
	Verified bool   `json:"verified"`
}

type EnvelopeOptions struct {
	// SkipBodyParsing keeps raw proxy bodies out of JSON and base64 copies.
	SkipBodyParsing    bool
	Endpoint           string
	DestinationQueueID string
	Queue              string
	RawBody            []byte
	ReceivedAt         time.Time
	SourceIP           string
	RequestID          string
	TraceID            string
	Traceparent        string
	AuthType           string
	AuthScheme         string
	IdempotencyKey     string
	HeaderDenylist     []string
	HeaderAllowlist    []string
	Hops               int
}

func BuildEnvelopeWithOptions(req *http.Request, opts EnvelopeOptions) (Envelope, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, err
	}
	authType := opts.AuthType
	if authType == "" {
		authType = "hmac_sha256"
	}
	env := Envelope{
		SchemaVersion:      "1.0",
		ID:                 id.String(),
		IdempotencyKey:     opts.IdempotencyKey,
		RequestID:          opts.RequestID,
		TraceID:            opts.TraceID,
		Traceparent:        opts.Traceparent,
		Endpoint:           opts.Endpoint,
		DestinationQueueID: opts.DestinationQueueID,
		Queue:              opts.Queue,
		ReceivedAt:         opts.ReceivedAt.UTC(),
		Method:             req.Method,
		Path:               req.URL.Path,
		Headers:            hooklogging.FilterHeaders(req.Header, opts.HeaderAllowlist, opts.HeaderDenylist),
		Query:              map[string]any{},
		SourceIP:           opts.SourceIP,
		Auth:               AuthInfo{Type: authType, Scheme: opts.AuthScheme, Verified: true},
	}
	for key, values := range req.URL.Query() {
		copied := append([]string(nil), values...)
		env.Query[key] = copied
	}
	if !opts.SkipBodyParsing {
		var parsed any
		if err := json.Unmarshal(opts.RawBody, &parsed); err == nil {
			env.Body = parsed
		} else {
			encoded := base64.StdEncoding.EncodeToString(opts.RawBody)
			env.RawBodyBase64 = &encoded
		}
	}
	env.RawBody = opts.RawBody
	env.OrigHeaders = req.Header.Clone()
	env.Hops = opts.Hops
	return env, nil
}
