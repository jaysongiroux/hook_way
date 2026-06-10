package webhook

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	hooklogging "github.com/jasongiroux/hookway/internal/logging"
)

type Envelope struct {
	SchemaVersion      string         `json:"schema_version"`
	ID                 string         `json:"id"`
	IdempotencyKey     string         `json:"idempotency_key,omitempty"`
	RequestID          string         `json:"request_id,omitempty"`
	TraceID            string         `json:"trace_id,omitempty"`
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
}

type AuthInfo struct {
	Type     string `json:"type"`
	Scheme   string `json:"scheme,omitempty"`
	Verified bool   `json:"verified"`
}

func BuildEnvelope(req *http.Request, endpoint, queue string, rawBody []byte, receivedAt time.Time, sourceIP string) (Envelope, error) {
	return BuildEnvelopeWithOptions(req, EnvelopeOptions{
		Endpoint: endpoint, Queue: queue, RawBody: rawBody, ReceivedAt: receivedAt, SourceIP: sourceIP,
	})
}

type EnvelopeOptions struct {
	Endpoint           string
	DestinationQueueID string
	Queue              string
	RawBody            []byte
	ReceivedAt         time.Time
	SourceIP           string
	RequestID          string
	TraceID            string
	AuthType           string
	AuthScheme         string
	IdempotencyKey     string
	HeaderDenylist     []string
	HeaderAllowlist    []string
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
	var parsed any
	if err := json.Unmarshal(opts.RawBody, &parsed); err == nil {
		env.Body = parsed
	} else {
		encoded := base64.StdEncoding.EncodeToString(opts.RawBody)
		env.RawBodyBase64 = &encoded
	}
	return env, nil
}
