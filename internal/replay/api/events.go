package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/problem"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

type EventControl interface {
	Call(context.Context, eventops.Request) (json.RawMessage, int, error)
}

func (a *api) eventOperation(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := eventops.Request{Operation: operation, ID: r.PathValue("id"), Actor: auth.Actor(r.Context())}
		for name, values := range r.URL.Query() {
			if len(values) != 1 {
				problem.Write(w, 400, "invalid_filter", "Query parameters must occur once.")
				return
			}
			v := values[0]
			switch name {
			case "endpoint":
				q.Filter.Endpoint = v
			case "event":
				q.Filter.Event = v
			case "state":
				q.Filter.State = v
			case "owner":
				q.Filter.Owner = v
			case "subscription":
				q.Filter.Subscription = v
			case "since":
				q.Filter.Since = v
			case "until":
				q.Filter.Until = v
			case "cursor":
				q.Filter.Cursor = v
			case "limit":
				n, e := strconv.Atoi(v)
				if e != nil || n < 1 || n > 200 {
					problem.Write(w, 400, "invalid_limit", "Limit must be between 1 and 200.")
					return
				}
				q.Filter.Limit = n
			default:
				problem.Write(w, 400, "invalid_filter", "Unknown query parameter.")
				return
			}
		}
		if len(r.URL.Query()) > 0 && operation != "subscriptions" && operation != "deliveries" {
			problem.Write(w, 400, "invalid_filter", "This operation does not accept filters.")
			return
		}
		if r.Method == "POST" {
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			q.Key = r.Header.Get("Idempotency-Key")
			if e != nil || eventops.Decode(raw, &q.Input) != nil || len(r.Header.Values("Idempotency-Key")) != 1 || len(uicontract.ValidateEventAction(operation, q.Key, q.Input).Issues) > 0 {
				problem.Write(w, 400, "invalid_action", "Provide a version, Idempotency-Key, and the required reason or duplicate-risk acknowledgement.")
				return
			}
		}
		if a.events == nil {
			if operation == "status" {
				writeJSON(w, 200, eventops.Status{State: "unsupported"})
			} else {
				problem.Write(w, 503, "events_unavailable", "Event operations are unavailable.")
			}
			return
		}
		raw, status, e := a.events.Call(r.Context(), q)
		if operation == "status" {
			if status == 404 {
				writeJSON(w, 200, eventops.Status{State: "unsupported"})
				return
			}
			if e != nil || status != 200 {
				writeJSON(w, 200, eventops.Status{State: "unavailable"})
				return
			}
		}
		if e != nil {
			problem.Write(w, 503, "events_unavailable", "Core event operations could not be reached.")
			return
		}
		if status < 200 || status >= 300 {
			var p problem.Details
			if json.Unmarshal(raw, &p) != nil {
				problem.Write(w, 503, "events_unavailable", "Core event operations failed.")
				return
			}
			w.Header().Set("Content-Type", problem.ContentType)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			_, _ = w.Write(raw)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if operation == "retry" {
			w.WriteHeader(202)
		}
		_, _ = w.Write(raw)
	}
}
