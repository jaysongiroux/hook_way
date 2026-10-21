// Package eventops is the shared, credential-free operator contract for MCP events.
package eventops

import (
	"encoding/json"
	"time"
)

const ControlPath = "/internal/mcp-events/v1/operate"
const Prefix = "/v1/mcp-events"

type Status struct {
	State string `json:"state"`
}
type Eligibility struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}
type Subscription struct {
	Actions        []Receipt   `json:"actions" openapi:"nonnullable"`
	ID             string      `json:"id"`
	Endpoint       string      `json:"endpoint"`
	Event          string      `json:"event"`
	Owner          string      `json:"owner"`
	ArgumentsJSON  string      `json:"arguments_json"`
	CallbackOrigin string      `json:"callback_origin"`
	State          string      `json:"state"`
	ExpiresAt      time.Time   `json:"expires_at"`
	Version        string      `json:"version"`
	RevokedAt      *time.Time  `json:"revoked_at,omitempty"`
	RevokedBy      string      `json:"revoked_by,omitempty"`
	Reason         string      `json:"reason,omitempty"`
	Revoke         Eligibility `json:"revoke"`
	Restore        Eligibility `json:"restore"`
}
type Attempt struct {
	Number     int        `json:"number"`
	Cycle      int        `json:"cycle"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	HTTPStatus *int       `json:"http_status,omitempty"`
	DurationMS *int64     `json:"duration_ms,omitempty"`
	Outcome    string     `json:"outcome"`
}
type Receipt struct {
	Action  string    `json:"action"`
	Target  string    `json:"target"`
	Actor   string    `json:"actor"`
	At      time.Time `json:"at"`
	Version string    `json:"version"`
	Reason  string    `json:"reason,omitempty"`
}
type Delivery struct {
	ID             string      `json:"id"`
	Endpoint       string      `json:"endpoint"`
	Event          string      `json:"event"`
	EventID        string      `json:"event_id"`
	SubscriptionID string      `json:"subscription_id"`
	Owner          string      `json:"owner"`
	State          string      `json:"state"`
	Outcome        string      `json:"outcome"`
	Version        string      `json:"version"`
	Attempts       int         `json:"attempts"`
	Cycle          int         `json:"cycle"`
	CycleAttempts  int         `json:"cycle_attempts"`
	CreatedAt      time.Time   `json:"created_at"`
	AvailableAt    time.Time   `json:"available_at"`
	FinishedAt     *time.Time  `json:"finished_at,omitempty"`
	HTTPStatus     *int        `json:"http_status,omitempty"`
	Retry          Eligibility `json:"retry"`
	History        []Attempt   `json:"history" openapi:"nonnullable"`
	Actions        []Receipt   `json:"actions" openapi:"nonnullable"`
}
type SubscriptionPage struct {
	Items      []Subscription `json:"items" openapi:"nonnullable"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
type DeliveryPage struct {
	Items      []Delivery `json:"items" openapi:"nonnullable"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
type ActionInput struct {
	Version                  string `json:"version"`
	Reason                   string `json:"reason,omitempty"`
	AcknowledgeDuplicateRisk bool   `json:"acknowledge_duplicate_risk,omitempty"`
}
type Filter struct {
	Endpoint     string `json:"endpoint,omitempty"`
	Event        string `json:"event,omitempty"`
	State        string `json:"state,omitempty"`
	Owner        string `json:"owner,omitempty"`
	Subscription string `json:"subscription,omitempty"`
	Since        string `json:"since,omitempty"`
	Until        string `json:"until,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}
type Request struct {
	Operation string      `json:"operation"`
	ID        string      `json:"id,omitempty"`
	Actor     string      `json:"actor"`
	Key       string      `json:"key,omitempty"`
	Filter    Filter      `json:"filter"`
	Input     ActionInput `json:"input"`
}
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string           { return e.Code }
func Fail(status int, code string) error { return &Error{status, code} }

// Decode rejects unknown fields and trailing values, retaining JSON numeric precision.
func Decode(raw []byte, out any) error { return decode(raw, out) }

type Route struct {
	Method, Path, Operation, Scope string
	Model                          any
	Name                           string
}

func Routes() []Route {
	return []Route{
		{"GET", "/status", "status", "events_read", Status{}, "MCPEventStatus"},
		{"GET", "/subscriptions", "subscriptions", "events_read", SubscriptionPage{}, "MCPSubscriptionPage"},
		{"GET", "/subscriptions/{id}", "subscription", "events_read", Subscription{}, "MCPSubscription"},
		{"POST", "/subscriptions/{id}/revoke", "revoke", "events_manage", Receipt{}, "MCPEventReceipt"},
		{"POST", "/subscriptions/{id}/restore", "restore", "events_manage", Receipt{}, "MCPEventReceipt"},
		{"GET", "/deliveries", "deliveries", "events_read", DeliveryPage{}, "MCPDeliveryPage"},
		{"GET", "/deliveries/{id}", "delivery", "events_read", Delivery{}, "MCPDelivery"},
		{"GET", "/deliveries/{id}/payload", "payload", "events_payload", json.RawMessage{}, "MCPEventPayload"},
		{"POST", "/deliveries/{id}/retry", "retry", "events_retry", Receipt{}, "MCPEventReceipt"},
	}
}
